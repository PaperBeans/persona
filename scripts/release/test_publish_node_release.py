"""Bind the final node receipt to a successful SSH exit and the original run."""
import hashlib
import json
from pathlib import Path
import sys
import unittest
from unittest.mock import MagicMock, Mock, patch

with patch.dict(sys.modules, {'paramiko': Mock()}):
    import publish_node_release as publisher
import test_pinned_node_release as fixture


class NodePublisherReceiptTests(unittest.TestCase):
    setUp = fixture.PinnedNodeTests.setUp

    def publish(self, code=0, failure=None, changed=False, extra=None):
        config = self.root / 'publisher.json'
        config.write_text(json.dumps(dict(node_target=self.payload['target'], candidate=self.candidate,
                                         remote_root=str(self.root), activation_enabled=True, **(extra or {}))))
        receipt = self.root / 'receipt.json'
        self.receipt = receipt
        result = dict(self.payload, phase='confirmed', machine_id=self.payload['target']['machine_id'],
                      machine_uid=self.payload['target']['machine_uid'])
        result['target_identity'] = hashlib.sha256(config.read_bytes()).hexdigest()
        if changed:
            result['run_id'] = 'another-run'
        sftp = Mock()
        sftp.lstat.side_effect = lambda path: Path(path).lstat()
        sftp.normalize.side_effect = lambda path: str(Path(path).resolve(strict=True))
        sftp.mkdir.side_effect = lambda path, mode: Path(path).mkdir(mode=mode)
        client = MagicMock()
        client.open_sftp.return_value.__enter__.return_value = sftp
        stdin, stdout, stderr = Mock(), Mock(), Mock()
        client.exec_command.return_value = stdin, stdout, stderr
        argv = ['publisher', '--config', str(config), '--config-sha256', result['target_identity'],
                '--artifact-dir', str(self.staged), '--source', self.manifest['commit'],
                '--run-id', self.payload['run_id'], '--receipt', str(receipt), '--apply']
        with patch('sys.argv', argv), patch.object(publisher, 'connect_publisher', return_value=(client, None)), \
                patch.object(publisher, 'collect_output', side_effect=failure,
                             return_value=(json.dumps(result), 'remote traceback', code)):
            try:
                publisher.main()
            finally:
                client.close.assert_called_once()
                script = stdin.write.call_args.args[0]
                self.assertIn("types.ModuleType('node_relay_retry')", script)
                self.assertEqual(client.exec_command.call_args.kwargs['timeout'], 1020)
                self.command = client.exec_command.call_args.args[0]

    def test_success_requires_matching_run_and_zero_exit(self):
        self.publish()
        self.assertEqual(json.loads(self.receipt.read_text())['phase'], 'confirmed')

    def test_remote_program_runs_under_the_configured_python(self):
        self.publish()
        self.assertEqual(self.command, 'python3 -')
        self.publish(extra={'remote_python': '/opt/python3.13/bin/python3'})
        self.assertEqual(self.command, '/opt/python3.13/bin/python3 -')

    def test_nonzero_exit_preserves_error_and_never_writes_confirmation(self):
        with self.assertRaisesRegex(RuntimeError, 'exited 7: remote traceback'):
            self.publish(code=7)
        self.assertFalse(self.receipt.exists())

    def test_missing_exit_status_never_writes_confirmation(self):
        with self.assertRaisesRegex(RuntimeError, 'missing status'):
            self.publish(failure=RuntimeError('missing status'))
        self.assertFalse(self.receipt.exists())

    def test_foreign_run_receipt_rejected(self):
        with self.assertRaisesRegex(ValueError, 'does not match'):
            self.publish(changed=True)
        self.assertFalse(self.receipt.exists())


if __name__ == '__main__':
    unittest.main()
