#!/usr/bin/env python3
"""Run repeatable database-pool experiments without owning the deployment."""

from __future__ import annotations

import argparse
import csv
import json
import os
import statistics
import subprocess
import sys
from pathlib import Path

METRICS = (
    "postgres_cpu_percent", "completed_pages_per_minute",
    "transaction_latency_ms", "pool_waits_per_minute",
    "aggregation_catchup_seconds", "api_latency_ms",
)


def parse_setting(value: str) -> tuple[int, int, int]:
    try:
        setting = tuple(int(part) for part in value.split(":"))
    except ValueError as exc:
        raise argparse.ArgumentTypeError("pool setting must contain integers") from exc
    if len(setting) != 3 or any(size < 1 for size in setting):
        raise argparse.ArgumentTypeError(
            "pool setting must be ENGINE:EVENTS:API with positive sizes")
    return setting  # type: ignore[return-value]


def validate_measurement(value: object) -> dict[str, float]:
    if not isinstance(value, dict):
        raise ValueError("workload output must be a JSON object")
    missing = [metric for metric in METRICS if metric not in value]
    if missing:
        raise ValueError("workload output is missing: " + ", ".join(missing))
    result = {}
    for metric in METRICS:
        number = float(value[metric])
        if number < 0:
            raise ValueError(f"{metric} must not be negative")
        result[metric] = number
    return result


def run_command(command: str, environment: dict[str, str], capture: bool = False) -> str:
    completed = subprocess.run(command, shell=True, check=True, text=True,
                               env=environment,
                               stdout=subprocess.PIPE if capture else None)
    return completed.stdout if capture else ""


def summarize(rows: list[dict[str, object]]) -> list[dict[str, object]]:
    grouped: dict[tuple[int, int, int], list[dict[str, object]]] = {}
    for row in rows:
        key = (int(row["engine_pool"]), int(row["events_pool"]), int(row["api_pool"]))
        grouped.setdefault(key, []).append(row)
    output = []
    for pools, samples in grouped.items():
        item: dict[str, object] = dict(zip(
            ("engine_pool", "events_pool", "api_pool"), pools))
        for metric in METRICS:
            item[metric] = statistics.median(float(sample[metric]) for sample in samples)
        output.append(item)
    return output


def write_csv(path: Path, rows: list[dict[str, object]]) -> None:
    fields = ["engine_pool", "events_pool", "api_pool", "repeat", *METRICS]
    with path.open("w", newline="", encoding="utf-8") as stream:
        writer = csv.DictWriter(stream, fieldnames=fields, extrasaction="ignore")
        writer.writeheader()
        writer.writerows(rows)


def main(argv: list[str] | None = None) -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--setting", action="append", type=parse_setting, required=True,
                        help="ENGINE:EVENTS:API maximum connections; repeat for a sweep")
    parser.add_argument("--repeats", type=int, default=3)
    parser.add_argument("--apply-command", required=True,
                        help="apply POOL_SWEEP_*_POOL and wait until healthy")
    parser.add_argument("--workload-command", required=True,
                        help="print one JSON object containing every required metric")
    parser.add_argument("--output", type=Path, required=True)
    args = parser.parse_args(argv)
    if args.repeats < 3:
        parser.error("--repeats must be at least 3")

    rows: list[dict[str, object]] = []
    for engine, events, api in args.setting:
        environment = os.environ.copy()
        environment.update({"POOL_SWEEP_ENGINE_POOL": str(engine),
                            "POOL_SWEEP_EVENTS_POOL": str(events),
                            "POOL_SWEEP_API_POOL": str(api)})
        for repeat in range(1, args.repeats + 1):
            run_command(args.apply_command, environment)
            raw = run_command(args.workload_command, environment, capture=True)
            measurement = validate_measurement(json.loads(raw))
            rows.append({"engine_pool": engine, "events_pool": events,
                         "api_pool": api, "repeat": repeat, **measurement})
            write_csv(args.output, rows)

    summary_path = args.output.with_name(args.output.stem + "-median.csv")
    write_csv(summary_path,
              [{**row, "repeat": "median"} for row in summarize(rows)])
    return 0


if __name__ == "__main__":
    try:
        raise SystemExit(main())
    except (OSError, subprocess.CalledProcessError, json.JSONDecodeError, ValueError) as exc:
        print(f"pool_sweep: {exc}", file=sys.stderr)
        raise SystemExit(1) from exc
