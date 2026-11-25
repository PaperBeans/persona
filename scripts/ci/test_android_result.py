import contextlib
import importlib.util
import io
import json
from pathlib import Path
import tempfile
import unittest

HERE = Path(__file__).resolve().parent
SPEC = importlib.util.spec_from_file_location("android_result", HERE / "android_result.py")
result = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(result)
SHARDS_SPEC = importlib.util.spec_from_file_location("android_shards", HERE / "android-shards.py")
shards = importlib.util.module_from_spec(SHARDS_SPEC)
SHARDS_SPEC.loader.exec_module(shards)

COMMIT = "a" * 40
RUN_ID = "123456"
RUN_ATTEMPT = "2"


class AndroidResultTests(unittest.TestCase):
    def setUp(self):
        self.temporary = tempfile.TemporaryDirectory(prefix="persona-android-result-")
        self.addCleanup(self.temporary.cleanup)
        self.root = Path(self.temporary.name)

    def write(self, lane, checks="success", evidence="success", job="success", **changes):
        receipt = result.bind_receipt(COMMIT, lane, checks, evidence, job, RUN_ID, RUN_ATTEMPT)
        receipt.update(changes)
        (self.root / f"receipt-{lane}.json").write_text(json.dumps(receipt))

    def verify(self):
        return result.verify(COMMIT, self.root, RUN_ID, RUN_ATTEMPT)

    def test_lanes_match_the_partition_definition(self):
        self.assertEqual(result.LANES, tuple(shards.LANES[1:]))

    def test_success_receipt_is_run_and_source_bound(self):
        receipt = result.bind_receipt(COMMIT, "test-1", "success", "success", "success", RUN_ID, RUN_ATTEMPT)
        self.assertTrue(receipt["verified"])
        self.assertEqual(receipt["outcome"], "success")
        self.assertEqual((receipt["commit"], receipt["run_id"], receipt["run_attempt"]),
                         (COMMIT, RUN_ID, RUN_ATTEMPT))

    def test_a_failed_lane_still_writes_a_diagnostic_failure_receipt(self):
        receipt = result.bind_receipt(
            COMMIT, "lint-build-test-0", "failure", "success", "failure", RUN_ID, RUN_ATTEMPT)
        self.assertFalse(receipt["verified"])
        self.assertEqual(receipt["outcome"], "failure")
        output = self.root / "receipt.json"
        with contextlib.redirect_stdout(io.StringIO()), contextlib.redirect_stderr(io.StringIO()):
            status = result.main([
                "bind", "--commit", COMMIT, "--lane", "lint-build-test-0",
                "--checks-outcome", "failure", "--evidence-outcome", "failure", "--job-status", "failure",
                "--run-id", RUN_ID, "--run-attempt", RUN_ATTEMPT, "--output", str(output),
            ])
        self.assertEqual(status, 1)
        receipt = json.loads(output.read_text())
        self.assertEqual((receipt["outcome"], receipt["verified"]), ("failure", False))

    def test_evidence_publication_failure_is_unavailable_not_a_test_failure(self):
        receipt = result.bind_receipt(
            COMMIT, "lint-build-test-0", "success", "failure", "failure", RUN_ID, RUN_ATTEMPT)
        self.assertEqual((receipt["outcome"], receipt["verified"]), ("unavailable", False))
        self.assertIn("evidence", receipt["reason"])
        later_failure = result.bind_receipt(
            COMMIT, "lint-build-test-0", "success", "success", "failure", RUN_ID, RUN_ATTEMPT)
        self.assertEqual((later_failure["outcome"], later_failure["verified"]), ("unavailable", False))

    def test_both_success_receipts_are_the_only_verified_result(self):
        for lane in result.LANES:
            directory = self.root / lane
            directory.mkdir()
            self.write(lane)
            (self.root / f"receipt-{lane}.json").replace(directory / f"receipt-{lane}.json")
        published = self.verify()
        self.assertEqual(published["status"], "verified")
        self.assertTrue(published["verified"])
        self.assertEqual({lane: value["status"] for lane, value in published["lanes"].items()},
                         {lane: "verified" for lane in result.LANES})

    def test_missing_receipts_are_unavailable_not_test_failures(self):
        published = self.verify()
        self.assertEqual(published["status"], "unavailable")
        self.assertFalse(published["verified"])
        for lane in result.LANES:
            self.assertEqual(published["lanes"][lane]["status"], "unavailable")
        output = self.root / "result.json"
        with contextlib.redirect_stdout(io.StringIO()), contextlib.redirect_stderr(io.StringIO()):
            status = result.main([
                "verify", "--commit", COMMIT, "--lanes-dir", str(self.root),
                "--run-id", RUN_ID, "--run-attempt", RUN_ATTEMPT, "--output", str(output),
            ])
        self.assertEqual(status, 1)
        self.assertEqual(json.loads(output.read_text())["status"], "unavailable")

    def test_one_missing_lane_keeps_the_other_result_visible_as_incomplete(self):
        self.write("test-1", checks="failure", job="failure")
        published = self.verify()
        self.assertEqual(published["status"], "incomplete")
        self.assertEqual(published["lanes"]["lint-build-test-0"]["status"], "unavailable")
        self.assertEqual(published["lanes"]["test-1"]["status"], "failed")

    def test_completed_lane_failures_remain_failures(self):
        for lane in result.LANES:
            self.write(lane, checks="failure", job="failure")
        published = self.verify()
        self.assertEqual(published["status"], "failed")
        self.assertEqual({value["status"] for value in published["lanes"].values()}, {"failed"})

    def test_another_commit_run_attempt_or_lane_cannot_verify_this_result(self):
        for lane in result.LANES:
            self.write(lane)
        self.assertEqual(result.verify("b" * 40, self.root, RUN_ID, RUN_ATTEMPT)["status"], "invalid")
        self.assertEqual(result.verify(COMMIT, self.root, "999", RUN_ATTEMPT)["status"], "invalid")
        self.assertEqual(result.verify(COMMIT, self.root, RUN_ID, "1")["status"], "invalid")
        self.write("test-1")
        path = self.root / "receipt-test-1.json"
        receipt = json.loads(path.read_text())
        receipt["lane"] = "lint-build-test-0"
        path.write_text(json.dumps(receipt))
        self.assertEqual(self.verify()["status"], "invalid")

    def test_malformed_and_disagreeing_receipts_fail_closed(self):
        (self.root / "receipt-lint-build-test-0.json").write_text("not json")
        self.write("test-1", outcome="success", verified=False)
        published = self.verify()
        self.assertEqual(published["status"], "invalid")
        self.assertEqual(published["lanes"]["lint-build-test-0"]["status"], "invalid")
        self.assertEqual(published["lanes"]["test-1"]["status"], "invalid")

    def test_duplicate_receipts_are_invalid(self):
        self.write("lint-build-test-0")
        nested = self.root / "nested"
        nested.mkdir()
        (self.root / "receipt-lint-build-test-0.json").replace(nested / "receipt-lint-build-test-0.json")
        self.write("lint-build-test-0")
        self.write("test-1")
        self.assertEqual(self.verify()["lanes"]["lint-build-test-0"]["status"], "invalid")

    def test_a_failed_check_cannot_be_relabelled_as_unavailable(self):
        self.write("lint-build-test-0", checks="failure", outcome="unavailable", verified=False)
        self.write("test-1")
        self.assertEqual(self.verify()["lanes"]["lint-build-test-0"]["status"], "invalid")


if __name__ == "__main__":
    unittest.main()
