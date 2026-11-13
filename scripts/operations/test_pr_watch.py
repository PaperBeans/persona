"""Isolated tests for the watcher gates. No real GitHub, model or PR is used."""
import hashlib
import hmac
import importlib.util
import json
import os
from pathlib import Path
import subprocess
import sys
import tempfile
import threading
import unittest
from contextlib import contextmanager
from unittest.mock import patch

HERE = Path(__file__).resolve().parent
sys.path.insert(0, str(HERE))
import pr_watch
import pr_watch_common
import pr_watch_events
import pr_watch_merge
import pr_watch_review
import pr_watch_verify

SHA_A, SHA_B, SHA_BASE = "a" * 40, "b" * 40, "c" * 40


def pr(head=SHA_A, **overrides):
    result = {"number": 7, "title": "fix: an example", "body": "Summary.\n\nFixes #6",
              "state": "OPEN", "isDraft": False, "headRefOid": head, "headRefName": "fix/example",
              "baseRefName": "main", "baseRefOid": SHA_BASE, "isCrossRepository": False,
              "mergeable": "MERGEABLE", "mergeStateStatus": "CLEAN", "reviewDecision": "", "url": "https://example.com/pr/7"}
    result.update(overrides)
    return result


class WatcherTests(unittest.TestCase):
    def setUp(self):
        temp = tempfile.TemporaryDirectory(prefix="pr-watch-test-")
        self.addCleanup(temp.cleanup)
        self.root = Path(temp.name)
        (self.root / "checkout").mkdir()
        self.config = {"repositories": [{"name": "example/one", "checkout": str(self.root / "checkout"),
                                        "base": "main", "verify": {"profile": "commands", "commands": [["true"]]},
                                        "merge": True, "privacy": None, "notify_command": None}],
                       "reviewer": {}, "webhook": {}, "poll_seconds": 60, "workers": 1,
                       "state": str(self.root / "state.json")}
        self.watcher = pr_watch.Watcher(self.config, verbose=False)
        @contextmanager
        def no_file_lock(root):
            yield
        self.lock_patch = patch.object(pr_watch.pr_watch_merge, "merge_lock", no_file_lock)
        self.lock_patch.start()
        self.addCleanup(self.lock_patch.stop)
        self.watcher.remove_worktree = lambda *args: None
        self.watcher.worktree = lambda *args: self.root / "checkout"
        self.comments = []
        self.watcher.comment = lambda repo, number, body: self.comments.append(body)
        self.watcher.success_review = lambda *args: self.comments.append("passed")

    def test_clean_head_runs_both_gates_then_merges(self):
        calls = []
        with patch.object(pr_watch, "pr_snapshot", return_value=pr()), \
                patch.object(pr_watch, "semantic_review", return_value=[]), \
                patch.object(pr_watch, "verify", return_value=[{"name": "test", "command": ["true"], "ok": True, "summary": ""}]), \
                patch.object(self.watcher, "merge", return_value=True) as merge:
            self.watcher.process("example/one", 7)
            self.watcher.process("example/one", 7)
        self.assertEqual(merge.call_count, 1)
        self.assertEqual(self.watcher.state.get("example/one", 7)["outcome"], "merged")

    def test_findings_are_published_once_and_never_merge(self):
        findings = [{"path": "x.py", "line": 2, "severity": "P1", "summary": "wrong", "suggestion": "fix it"}]
        with patch.object(pr_watch, "pr_snapshot", return_value=pr()), \
                patch.object(pr_watch, "semantic_review", return_value=findings), \
                patch.object(pr_watch, "verify") as verify, \
                patch.object(self.watcher, "merge") as merge, \
                patch.object(pr_watch, "run", return_value=subprocess.CompletedProcess([], 0, "{}", "")) as api:
            self.watcher.process("example/one", 7)
            self.watcher.process("example/one", 7)
        verify.assert_not_called()
        merge.assert_not_called()
        self.assertEqual(api.call_count, 1)
        self.assertEqual(api.call_args.args[0][:2], ["gh", "api"])

    def test_new_sha_reviews_again(self):
        with patch.object(pr_watch, "pr_snapshot", side_effect=[pr(), pr(), pr(SHA_B), pr(SHA_B)]), \
                patch.object(pr_watch, "semantic_review", return_value=[]) as review, \
                patch.object(pr_watch, "verify", return_value=[{"name": "test", "ok": True, "command": [], "summary": ""}]), \
                patch.object(self.watcher, "merge", return_value=False):
            self.watcher.process("example/one", 7)
            self.watcher.process("example/one", 7)
        self.assertEqual(review.call_count, 2)
        self.assertEqual(self.watcher.state.get("example/one", 7)["head"], SHA_B)

    def test_verification_failure_is_not_an_infrastructure_retry(self):
        with patch.object(pr_watch, "pr_snapshot", return_value=pr()), \
                patch.object(pr_watch, "semantic_review", return_value=[]), \
                patch.object(pr_watch, "verify", side_effect=pr_watch_common.WatchError("real test failed")), \
                patch.object(self.watcher, "merge") as merge:
            self.watcher.process("example/one", 7)
        merge.assert_not_called()
        self.assertEqual(self.watcher.state.get("example/one", 7)["outcome"], "failed")
        self.assertEqual(len(self.comments), 1)
        self.assertIn("real test failed", self.comments[0])

    def test_semantic_failure_retries_then_posts_once(self):
        for attempt in range(3):
            with patch.object(pr_watch, "pr_snapshot", return_value=pr()), \
                    patch.object(pr_watch, "semantic_review", side_effect=pr_watch_common.WatchError("invalid output")), \
                    patch.object(self.watcher, "merge") as merge:
                self.watcher.process("example/one", 7)
            merge.assert_not_called()
            self.watcher.state.data["prs"]["example/one#7"]["retry_after"] = 0
        self.assertEqual(len(self.comments), 1)
        self.assertIn("invalid output", self.comments[0])

    def test_unknown_mergeability_waits(self):
        with patch.object(pr_watch, "pr_snapshot", return_value=pr(mergeable="UNKNOWN")), \
                patch.object(pr_watch, "semantic_review") as review:
            self.watcher.process("example/one", 7)
        review.assert_not_called()

    def test_moved_head_discards_passed_checks(self):
        with patch.object(pr_watch, "pr_snapshot", side_effect=[pr(), pr(SHA_B)]), \
                patch.object(pr_watch, "semantic_review", return_value=[]), \
                patch.object(pr_watch, "verify", return_value=[{"name": "test", "ok": True, "command": [], "summary": ""}]), \
                patch.object(self.watcher, "merge") as merge:
            self.watcher.process("example/one", 7)
        merge.assert_not_called()

    def test_another_persons_review_never_skips_verification(self):
        # The service reads only its own versioned state, not an arbitrary GitHub review.
        with patch.object(pr_watch, "pr_snapshot", return_value=pr(reviewDecision="COMMENTED")), \
                patch.object(pr_watch, "semantic_review", return_value=[]) as review, \
                patch.object(pr_watch, "verify", return_value=[{"name": "test", "ok": True, "command": [], "summary": ""}]), \
                patch.object(self.watcher, "merge", return_value=False):
            self.watcher.process("example/one", 7)
        review.assert_called_once()

    def test_inline_review_uses_stdin_payload_and_falls_back(self):
        findings = [{"path": "x.py", "line": 2, "severity": "P1", "summary": "wrong", "suggestion": "fix"}]
        requests = []
        def fake_run(argv, **kwargs):
            requests.append((argv, kwargs))
            if argv[:2] == ["gh", "api"]:
                return subprocess.CompletedProcess(argv, 422, "", "bad anchor")
            return subprocess.CompletedProcess(argv, 0, "{}", "")
        with patch.object(pr_watch, "run", side_effect=fake_run):
            self.watcher.comment_findings("example/one", 7, pr(), findings)
        payload = json.loads(requests[0][1]["input_text"])
        self.assertEqual(payload["comments"][0]["line"], 2)
        self.assertEqual(payload["commit_id"], SHA_A)
        self.assertEqual(requests[1][0][:2], ["gh", "pr"])

    def test_lock_excludes_a_second_process(self):
        first = pr_watch.Watcher(self.config, verbose=False)
        second = pr_watch.Watcher(self.config, verbose=False)
        first.single_instance()
        self.addCleanup(first.instance.close)
        with self.assertRaises(pr_watch_common.WatchError):
            second.single_instance()
        second.instance.close()

    def test_config_has_no_implicit_merge_or_repositories(self):
        path = self.root / "config.json"
        path.write_text(json.dumps({"repositories": [{"name": "example/one", "checkout": str(self.root), "verify": {}}]}))
        config = pr_watch.load_config(path)
        self.assertFalse(config["repositories"][0]["merge"])
        self.assertEqual(len(config["repositories"]), 1)


class ReviewTests(unittest.TestCase):
    def test_schema_and_changed_line_validation(self):
        envelope = {"subtype": "success", "is_error": False, "permission_denials": [],
                    "structured_output": {"findings": [{"path": "a.py", "line": 2, "severity": "P1",
                                                        "summary": "wrong", "suggestion": "fix"}]}}
        self.assertEqual(len(pr_watch_review.parse_result(json.dumps(envelope), {"a.py": {2}})), 1)
        for anchors in ({"a.py": {1}}, {"other.py": {2}}):
            with self.assertRaises(pr_watch_common.WatchError):
                pr_watch_review.parse_result(json.dumps(envelope), anchors)
        envelope["permission_denials"] = [{"tool": "Bash"}]
        with self.assertRaises(pr_watch_common.WatchError):
            pr_watch_review.parse_result(json.dumps(envelope), {"a.py": {2}})
        with self.assertRaises(pr_watch_common.WatchError):
            pr_watch_review.parse_result("not json", {"a.py": {2}})

    def test_diff_anchors_include_only_added_lines(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            env = dict(os.environ, GIT_CONFIG_NOSYSTEM="1", GIT_CONFIG_GLOBAL=os.devnull,
                       GIT_AUTHOR_NAME="Test", GIT_AUTHOR_EMAIL="test@example.com",
                       GIT_COMMITTER_NAME="Test", GIT_COMMITTER_EMAIL="test@example.com")
            def git(*args):
                subprocess.run(["git"] + list(args), cwd=root, env=env, check=True, capture_output=True)
            git("init", "-q", "-b", "main")
            (root / "a.py").write_text("one\ntwo\n")
            git("add", "a.py")
            git("commit", "-qm", "base")
            base = subprocess.check_output(["git", "rev-parse", "HEAD"], cwd=root, text=True).strip()
            (root / "a.py").write_text("one\nchanged\nthree\n")
            git("commit", "-qam", "head")
            head = subprocess.check_output(["git", "rev-parse", "HEAD"], cwd=root, text=True).strip()
            self.assertEqual(pr_watch_review.changed_lines(root, base, head), {"a.py": {2, 3}})


class EventTests(unittest.TestCase):
    def test_webhook_authentication_repository_and_callback(self):
        secret = "x" * 32
        body = json.dumps({"action": "synchronize", "repository": {"full_name": "example/one"},
                           "pull_request": {"number": 7, "draft": False, "head": {"sha": SHA_A}}}).encode()
        signature = "sha256=" + hmac.new(secret.encode(), body, hashlib.sha256).hexdigest()
        event = pr_watch_events.parse_pull_request(body, signature, "pull_request", secret, {"example/one"})
        self.assertEqual(event, ("example/one", 7, SHA_A))
        with self.assertRaises(pr_watch_common.WatchError):
            pr_watch_events.parse_pull_request(body, "sha256=bad", "pull_request", secret, {"example/one"})
        with self.assertRaises(pr_watch_common.WatchError):
            pr_watch_events.parse_pull_request(body, signature, "pull_request", secret, {"example/other"})


class VerifyTests(unittest.TestCase):
    def test_persona_apple_cannot_be_silently_skipped(self):
        with patch.object(pr_watch_verify, "checked", return_value="apple/Persona/App/View.swift\n"), \
                patch.object(pr_watch_verify, "load_paths") as selector:
            selector.return_value.select.return_value = {"apple": True, "scripts": True}
            with self.assertRaises(pr_watch_common.WatchError):
                pr_watch_verify.persona_checks(".", pr(), 1, {})
        with patch.object(pr_watch_verify, "checked", return_value="apple/Persona/App/View.swift\n"), \
                patch.object(pr_watch_verify, "load_paths") as selector:
            selector.return_value.select.return_value = {"apple": True, "scripts": True}
            commands = pr_watch_verify.persona_checks(".", pr(), 1, {"apple_command": ["verify", "{head}"],
                                                                      "ui_evidence_command": ["visual", "{worktree}"]})
        self.assertIn(("trusted Apple verification", ["verify", SHA_A]), commands)
        self.assertIn(("visual evidence", ["visual", "."]), commands)

    def test_extension_shared_changes_use_full_ci(self):
        with patch.object(pr_watch_verify, "extension_modules", return_value=None), \
                patch.object(pr_watch_verify, "checked", return_value="scripts/ci.sh\n"):
            self.assertEqual(pr_watch_verify.extension_checks(".", pr(), 1),
                             [("extension repository CI", ["bash", "scripts/ci.sh"])])


class MergeTests(unittest.TestCase):
    def test_issue_link_parser_ignores_code_and_other_repositories(self):
        self.assertEqual(pr_watch_merge.linked_issue("```\nFixes #1\n```\nRefs #2", "example/one"), [2])
        self.assertEqual(pr_watch_merge.linked_issue("Fixes example/two#3", "example/one"), [])
        self.assertEqual(pr_watch_merge.linked_issue("See https://github.com/example/one/issues/4", "example/one"), [4])


if __name__ == "__main__":
    unittest.main()
