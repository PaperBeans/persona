"""deploy.sh's is_here: which machines it treats as this computer (local copy instead of ssh, #1725).

Runs the function as it is in scripts/deploy/deploy.sh with `field` stubbed and a PATH that holds only stub
`hostname` / `scutil` commands, so it behaves the same on macOS and Linux.
"""
import os
from pathlib import Path
import re
import shutil
import subprocess
import tempfile
import unittest

ROOT = Path(__file__).resolve().parents[2]
DEPLOY = ROOT / 'scripts/deploy/deploy.sh'
BASH = shutil.which('bash') or '/bin/bash'


def is_here_source():
    match = re.search(r'^is_here\(\) \{\n.*?^\}\n', DEPLOY.read_text(), re.S | re.M)
    assert match, 'is_here not found in deploy.sh'
    return match.group(0)


class DeployIsHereTests(unittest.TestCase):
    def is_here(self, hostnames, hostname='box1', scutil=None):
        """hostnames: what the config lists; hostname: `hostname -s`; scutil: LocalHostName, None = no scutil."""
        with tempfile.TemporaryDirectory() as directory:
            stubs = Path(directory)
            commands = {'hostname': hostname}
            if scutil is not None:
                commands['scutil'] = scutil
            for name, output in commands.items():
                path = stubs / name
                path.write_text('#!%s\nprintf "%%s\\n" %s\n' % (BASH, "'" + output.replace("'", "'\\''") + "'"))
                path.chmod(0o755)
            script = 'field() { printf "%%s\\n" %s; }\n%sis_here m && echo here || echo remote\n' % (
                "'" + hostnames + "'", is_here_source())
            # Its own HOME and no stdin: with stdin a socket (ssh, a tool's shell) and no HOME, bash -c sources the
            # passwd home's ~/.bashrc, whose PATH puts the real hostname ahead of the stub.
            result = subprocess.run([BASH, '-c', script], env={'PATH': str(stubs), 'HOME': str(stubs)},
                                    stdin=subprocess.DEVNULL, capture_output=True, text=True,
                                    timeout=10)
            self.assertEqual(result.returncode, 0, result.stderr)
            return result.stdout.strip() == 'here'

    def test_without_scutil_a_machine_without_hostnames_is_remote(self):
        self.assertFalse(self.is_here(''))

    def test_without_scutil_a_machine_listing_this_host_name_is_here(self):
        self.assertTrue(self.is_here('box1'))
        self.assertTrue(self.is_here('other box1'))

    def test_another_host_name_is_remote(self):
        self.assertFalse(self.is_here('other'))
        self.assertFalse(self.is_here('other', scutil='MyMac'))

    def test_empty_names_never_match(self):
        self.assertFalse(self.is_here('', hostname='', scutil=''))
        self.assertFalse(self.is_here('other', hostname='', scutil=''))
        self.assertFalse(self.is_here('', scutil='MyMac'))

    def test_scutil_local_host_name_counts(self):
        self.assertTrue(self.is_here('MyMac', hostname='', scutil='MyMac'))
        self.assertTrue(self.is_here('MyMac', scutil='MyMac'))

    def test_partial_names_do_not_match(self):
        self.assertFalse(self.is_here('box10'))
        self.assertFalse(self.is_here('xbox1'))


if __name__ == '__main__':
    unittest.main()
