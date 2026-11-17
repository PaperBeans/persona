import hashlib
import json
from pathlib import Path
import tempfile
import unittest
from unittest.mock import patch

import beta_channel_release as beta
import tag_release


class BetaChannelReleaseTest(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.root = Path(self.temp.name)
        self.commit = "a" * 40
        self.manifest = self.root / "latest.json"
        self.manifest.write_text(json.dumps({"version": "0.7.0-beta.1", "build": 202611171300,
                                             "channel": "beta", "files": {"darwin-arm64": {"name": "personad-darwin-arm64.gz"}}}))
        self.receipt = self.root / "receipt.json"
        self.receipt.write_text(json.dumps({"commit": self.commit, "assets": [
            {"path": str(self.manifest), "name": "latest.json", "sha256": hashlib.sha256(self.manifest.read_bytes()).hexdigest()}]}))
        self.digest = "sha256:" + hashlib.sha256(self.manifest.read_bytes()).hexdigest()

    def test_prerelease_is_required(self):
        with patch.object(tag_release.subprocess, "check_output", return_value=json.dumps(
                {"tagName": "v0.7.0-beta.1", "isDraft": False, "isPrerelease": False, "assets": []})):
            with self.assertRaisesRegex(tag_release.ConfigError, "prerelease"):
                tag_release.upload("example/project", "v0.7.0-beta.1", [])

    def test_pointer_verifies_tag_and_remote_index(self):
        uploaded = []
        calls = []

        def output(gh, path):
            tag = path.rsplit("/", 1)[-1]
            return {"tag_name": tag, "draft": False, "prerelease": True,
                    "target_commitish": self.commit,
                    "assets": [{"name": "latest.json", "digest": self.digest}] if uploaded else []}

        def run(argv, **kwargs):
            calls.append(argv)
            if argv[:3] == ["gh", "release", "upload"]:
                uploaded.append(True)

        with patch.object(beta, "resolve_tag", return_value=self.commit), \
                patch.object(beta, "get_json", side_effect=output), \
                patch.object(beta.subprocess, "check_output", return_value=json.dumps(
                    {"tag_name": "v0.7.0-beta.1", "draft": False, "prerelease": True,
                     "assets": [{"name": "latest.json", "digest": self.digest}]})), \
                patch.object(beta.subprocess, "run", side_effect=run):
            with patch.object(beta.sys, "argv", ["beta", "--repo", str(self.root), "--repository", "example/project",
                                                "--tag", "v0.7.0-beta.1", "--receipt", str(self.receipt)]):
                beta.main()
        self.assertTrue(any(argv[:3] == ["gh", "release", "upload"] for argv in calls))

    def test_pointer_rejects_stable_manifest(self):
        self.manifest.write_text(json.dumps({"version": "0.7.0-beta.1", "build": 1, "files": {}}))
        self.receipt.write_text(json.dumps({"commit": self.commit, "assets": [{"path": str(self.manifest), "name": "latest.json"}]}))
        with patch.object(beta, "resolve_tag", return_value=self.commit), patch.object(
                beta.sys, "argv", ["beta", "--repo", str(self.root), "--repository", "example/project",
                                   "--tag", "v0.7.0-beta.1", "--receipt", str(self.receipt)]):
            with self.assertRaisesRegex(tag_release.ConfigError, "beta-channel"):
                beta.main()


if __name__ == "__main__":
    unittest.main()
