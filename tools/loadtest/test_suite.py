import run
import suite

from pathlib import Path
import tempfile
import threading
import unittest
from unittest.mock import patch


class SuiteTest(unittest.TestCase):
    def test_threshold_boundaries_and_empty_samples(self):
        self.assertIsNone(suite.threshold(suite.metrics([], 10)))
        self.assertIsNone(suite.threshold(suite.metrics([(0.999, True)] * 100, 10)))
        self.assertEqual(suite.threshold(suite.metrics([(1, True)] * 100, 10)), "p95>=1000ms")
        self.assertEqual(suite.threshold(suite.metrics([(0.01, True)] * 99 + [(0.01, False)], 10)),
                         "error_percent>=1")

    def test_monitor_uses_new_samples_and_stops_on_window_breach(self):
        stop = threading.Event()
        monitor = suite.Monitor(stop)
        result = run.Result()
        with patch("suite.time.monotonic", side_effect=[0, 10, 20]):
            monitor(result)
            result.samples["GET"] = [(0.02, True)] * 100
            monitor(result)
            self.assertFalse(stop.is_set())
            result.samples["POST"] = [(0.1, False)]
            monitor(result)
        self.assertTrue(stop.is_set())
        self.assertEqual(monitor.windows[1]["requests"], 1)
        self.assertEqual(monitor.breach, "error_percent>=1")

    def test_suite_skips_remaining_stages_for_failed_target(self):
        def experiment(config, stop, observe):
            result = run.Result(elapsed=1, reason="duration", prepared=100)
            result.samples["GET"] = [(1.5 if config.label.startswith("vps-memory") else 0.01, True)]
            return result

        with tempfile.TemporaryDirectory() as temporary:
            output = Path(temporary) / "suite"
            with patch("suite.run.experiment", side_effect=experiment) as execute, \
                 patch("suite.STAGES", [("first", 10, 120), ("second", 25, 120)]), \
                 patch("suite.time.monotonic", side_effect=range(0, 1000, 20)), \
                 patch("sys.argv", ["suite.py", "--memory-url", "http://memory.invalid",
                                    "--postgres-url", "http://postgres.invalid", "--output-dir", str(output)]):
                self.assertEqual(suite.main(), 1)
            self.assertEqual(execute.call_count, 3)
            self.assertFalse((output / "memory-second.md").exists())
            self.assertTrue((output / "postgres-second.md").exists())
            self.assertIn("SKIP", (output / "summary.md").read_text())
            self.assertIn("threshold:", (output / "memory-first.md").read_text())


if __name__ == "__main__":
    unittest.main()
