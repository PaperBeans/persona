"""android_build_perf.py's arithmetic: the histogram gfxinfo prints, the percentiles and shares read from it, medians,
the order packages run in, which starts count as cold, and the links a release build is opened with."""
import importlib.util
import pathlib
import unittest

spec = importlib.util.spec_from_file_location("android_build_perf", pathlib.Path(__file__).with_name("android_build_perf.py"))
perf = importlib.util.module_from_spec(spec)
spec.loader.exec_module(perf)

GFXINFO = """Total frames rendered: 10
Janky frames: 2 (20.00%)
50th percentile: 9ms
GPU HISTOGRAM: 1ms=9 2ms=1
HISTOGRAM: 5ms=1 6ms=0 8ms=4 16ms=3 32ms=1 150ms=1
50th gpu percentile: 3ms
GPU HISTOGRAM: 3ms=7 4ms=3
"""


class AndroidBuildPerfTest(unittest.TestCase):
    def test_reads_the_frame_histogram_not_the_gpu_one(self):
        self.assertEqual(perf.histogram(GFXINFO), dict([(5, 1), (6, 0), (8, 4), (16, 3), (32, 1), (150, 1)]))
        self.assertEqual(perf.histogram("Total frames rendered: 0"), dict())

    def test_percentiles_are_the_bucket_they_fall_in(self):
        found = perf.histogram_stats(perf.histogram(GFXINFO))
        self.assertEqual(found["frames"], 10)
        self.assertEqual((found["p50"], found["p90"], found["p99"]), (8, 32, 150))

    def test_shares_count_from_a_bucket_up(self):
        found = perf.histogram_stats(perf.histogram(GFXINFO))
        # the 16 ms bucket is under 17; the 32 ms bucket (32 to 33.99) is under 34
        self.assertEqual(found["from_17ms_percent"], 20.0)
        self.assertEqual(found["from_34ms_percent"], 10.0)

    def test_no_frames_is_no_statistics(self):
        self.assertIsNone(perf.histogram_stats(dict()))
        self.assertIsNone(perf.histogram_stats(dict([(8, 0)])))

    def test_runs_add_up_bucket_by_bucket(self):
        first = dict(histogram=dict([(8, 2), (16, 1)]))
        second = dict(histogram=dict([(8, 3), (32, 1)]))
        self.assertEqual(perf.merged([first, second]), dict([(8, 5), (16, 1), (32, 1)]))

    def test_median_of_an_even_count_is_between_the_middle_two(self):
        six = perf.stats([2489, 2422, 2405, 2439, 2443, 2411])
        self.assertEqual((six["n"], six["min"], six["median"], six["max"]), (6, 2405, 2430.5, 2489))
        self.assertEqual(perf.stats(list(range(1, 11)))["median"], 5.5)
        self.assertEqual(perf.stats([3, None, 1, 2])["median"], 2)
        self.assertIsNone(perf.stats([None]))

    def test_packages_swap_places_every_other_round(self):
        packages = [["debug", "a"], ["release", "b"]]
        self.assertEqual(perf.ordered(packages, 0), packages)
        self.assertEqual(perf.ordered(packages, 1), [["release", "b"], ["debug", "a"]])
        self.assertEqual(perf.ordered(packages, 2), packages)

    def test_only_a_cold_start_is_timed(self):
        self.assertEqual(perf.cold_start_ms("LaunchState: COLD\nTotalTime: 557\nWaitTime: 560"), 557)
        self.assertIsNone(perf.cold_start_ms("LaunchState: WARM\nTotalTime: 120"))
        # another activity took the front: no TotalTime at all
        self.assertIsNone(perf.cold_start_ms("LaunchState: UNKNOWN (0)\nWaitTime: 455"))
        # an older `am` that prints no LaunchState
        self.assertEqual(perf.cold_start_ms("TotalTime: 2422"), 2422)

    def test_links_open_the_list_and_a_conversation(self):
        self.assertEqual(perf.scenarios(None, None), dict(agents="persona://open?tab=agents"))
        both = perf.scenarios("server/s-1", None)
        self.assertEqual(both["chat"], "persona://open?tab=agents&machine=server&session=s-1")
        self.assertEqual(list(perf.scenarios("server/s-1", ["chat"])), ["chat"])


if __name__ == "__main__":
    unittest.main()
