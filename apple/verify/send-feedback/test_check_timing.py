import importlib.util
from pathlib import Path
import unittest

spec = importlib.util.spec_from_file_location("check_timing", Path(__file__).with_name("check_timing.py"))
checker = importlib.util.module_from_spec(spec)
spec.loader.exec_module(checker)


class TimingTests(unittest.TestCase):
    def log(self, name, command=1.364, event=59.22, visible="true"):
        return (f't = 58.42s Tap "shell-composer-send" Button\n'
                f't = {event}s Synthesize event\n'
                f'SEND_FEEDBACK {name} processing_visible={visible} seconds={command}\n')

    def test_excludes_pre_input_lookup_and_keeps_conservative_margin(self):
        rows = checker.measurements(self.log("agent") + self.log("assistant"))
        self.assertEqual(rows[0]["input_to_visible_seconds"], 0.564)
        self.assertEqual(rows[0]["conservative_upper_seconds"], 0.584)
        self.assertEqual(rows[0]["pre_input_automation_seconds"], 0.8)

    def test_rejects_actual_slow_presentation(self):
        with self.assertRaises(ValueError):
            checker.measurements(self.log("agent", command=2) + self.log("assistant"))

    def test_rejects_missing_input_or_failed_observation(self):
        for text in (self.log("agent", visible="false"),
                     "SEND_FEEDBACK agent processing_visible=true seconds=0.5", self.log("agent")):
            with self.assertRaises(ValueError):
                checker.measurements(text)


if __name__ == "__main__":
    unittest.main()
