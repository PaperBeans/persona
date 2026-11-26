from contextlib import redirect_stdout
import io
import json
import os
from pathlib import Path
import runpy
import subprocess
import tempfile
import unittest
from unittest.mock import patch


SCRIPT = Path(__file__).with_name('android_scroll_perf.py')
SAMPLE = 'Total frames rendered: 10\nJanky frames: 2\n90th percentile: 19ms\n'


class AndroidScrollPerfTest(unittest.TestCase):
    def run_scroll(self, output, *, failed_command=None, command_exception=None, sample=SAMPLE):
        def adb(command, **kwargs):
            args = command[4:]
            stdout = ''
            if args == ['wm', 'size']:
                stdout = 'Physical size: 1080x2400'
            elif args[-1] == 'framestats':
                stdout = sample
            failed = failed_command is not None and args[:len(failed_command)] == failed_command
            if command_exception is not None and (failed or failed_command is None):
                raise command_exception
            if failed:
                if kwargs.get('check'):
                    raise subprocess.CalledProcessError(1, command)
                return subprocess.CompletedProcess(command, 1, '')
            return subprocess.CompletedProcess(command, 0, stdout)

        argv = [str(SCRIPT), 'fixture', 'test.app', str(output), 'glass']
        with patch('sys.argv', argv), patch.dict(os.environ, {'ONLY': 'agents', 'RUNS': '1'}), \
                patch('time.sleep'), patch('subprocess.run', side_effect=adb), redirect_stdout(io.StringIO()):
            runpy.run_path(str(SCRIPT), run_name='__main__')

    def test_complete_sample_keeps_frame_and_jank_counts(self):
        with tempfile.TemporaryDirectory() as directory:
            output = Path(directory) / 'out.json'
            self.run_scroll(output)
            row = json.loads(output.read_text())['glass/agents']
            self.assertEqual((row['frames_total'], row['janky_percent_total']), (10, 20.0))
            self.assertEqual(row['gfx_p90_ms_per_run']['p90'], 19)

    def test_unusable_optional_detail_preserves_the_aggregate_report(self):
        for detail in ('', 'Flags,IntendedVsync\n0,1000000\n'):
            with self.subTest(detail=detail), tempfile.TemporaryDirectory() as directory:
                output = Path(directory) / 'out.json'
                sample = SAMPLE + '---PROFILEDATA---\n' + detail + '\n---PROFILEDATA---\n'
                self.run_scroll(output, sample=sample)
                row = json.loads(output.read_text())['glass/agents']
                self.assertEqual((row['frames_total'], row['janky_percent_total']), (10, 20.0))
                self.assertIsNone(row['frame_ms'])

    def test_valid_frame_times_survive_malformed_rows(self):
        sample = SAMPLE + '\n'.join((
            '---PROFILEDATA---',
            'FrameCompleted,Flags,IntendedVsync',
            '17000000,0',
            '17000000,0,invalid',
            '26000000,1,1000000',
            '17000000,0,1000000',
            '26000000,0,1000000',
            '---PROFILEDATA---',
        ))
        with tempfile.TemporaryDirectory() as directory:
            output = Path(directory) / 'out.json'
            self.run_scroll(output, sample=sample)
            stats = json.loads(output.read_text())['glass/agents']['frame_ms']
            self.assertEqual((stats['n'], stats['min'], stats['max']), (2, 16.0, 25.0))

    def test_failed_or_unusable_sample_preserves_existing_report(self):
        for args, error in (
            ({'failed_command': ['dumpsys', 'gfxinfo', 'test.app', 'reset']}, 'adb shell dumpsys failed'),
            ({'failed_command': ['input', 'swipe']}, 'adb shell input failed'),
            ({'failed_command': ['dumpsys', 'gfxinfo', 'test.app', 'framestats']}, 'adb shell dumpsys failed'),
            ({'failed_command': ['dumpsys', 'gfxinfo', 'test.app', 'framestats'],
              'command_exception': subprocess.TimeoutExpired(['adb', '-s', 'fixture'], 60)},
             'adb shell dumpsys timed out after 60s'),
            ({'command_exception': FileNotFoundError('adb')}, 'adb not found on PATH'),
            ({'sample': 'Total frames rendered: 0\nJanky frames: 0'}, 'gfxinfo did not report a usable frame sample'),
            ({'sample': 'Total frames rendered: 10'}, 'gfxinfo did not report a usable frame sample'),
        ):
            with self.subTest(args=args), tempfile.TemporaryDirectory() as directory:
                output = Path(directory) / 'out.json'
                output.write_text('existing report\n')
                with self.assertRaisesRegex(SystemExit, error) as failure:
                    self.run_scroll(output, **args)
                self.assertNotIn('fixture', str(failure.exception))
                self.assertEqual(output.read_text(), 'existing report\n')


if __name__ == '__main__':
    unittest.main()
