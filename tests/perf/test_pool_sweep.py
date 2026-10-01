import importlib.util
import unittest
from pathlib import Path

SPEC = importlib.util.spec_from_file_location(
    "pool_sweep", Path(__file__).with_name("pool_sweep.py"))
pool_sweep = importlib.util.module_from_spec(SPEC)
assert SPEC.loader
SPEC.loader.exec_module(pool_sweep)


class PoolSweepTest(unittest.TestCase):
    def test_setting_keeps_service_pools_independent(self):
        self.assertEqual((8, 4, 3), pool_sweep.parse_setting("8:4:3"))

    def test_measurement_requires_every_signal(self):
        with self.assertRaisesRegex(ValueError, "api_latency_ms"):
            pool_sweep.validate_measurement(
                {metric: 1 for metric in pool_sweep.METRICS[:-1]})

    def test_summary_uses_medians(self):
        rows = []
        for repeat, value in enumerate((1, 100, 3), 1):
            rows.append({"engine_pool": 8, "events_pool": 4, "api_pool": 3,
                         "repeat": repeat,
                         **{metric: value for metric in pool_sweep.METRICS}})
        result = pool_sweep.summarize(rows)
        self.assertEqual(3, result[0]["completed_pages_per_minute"])


if __name__ == "__main__":
    unittest.main()
