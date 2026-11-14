#!/usr/bin/env python3
"""Negative safety tests for the ownership-safe MCP authorization E2E harness.

These tests exercise collision refusal, work-root containment, concurrent run
identity, unsafe port rejection, exact-label cleanup, idempotent cleanup, stale
PID immunity and image provenance rejection. Run them against a disposable
Docker daemon (DOCKER_HOST) because several cases create and remove only
test-owned containers, volumes and networks.
"""

from __future__ import annotations

import argparse
import json
import os
import shutil
import subprocess
import sys
import tempfile
import time
from pathlib import Path

FIXTURE = Path(__file__).resolve().parent
sys.path.insert(0, str(FIXTURE))
import e2e_support as support  # noqa: E402


class SafetyFailure(RuntimeError):
    pass


def support_cmd(args: list[str]) -> subprocess.CompletedProcess[str]:
    return subprocess.run([sys.executable, str(FIXTURE / "e2e_support.py"), *args], text=True, capture_output=True)


def expect_failure(name: str, args: list[str], detail: str) -> None:
    proc = support_cmd(args)
    if proc.returncode == 0:
        raise SafetyFailure(f"{name}: expected failure but command passed: {detail}")
    print(f"PASS: {name}")


def expect_success(name: str, args: list[str]) -> subprocess.CompletedProcess[str]:
    proc = support_cmd(args)
    if proc.returncode != 0:
        raise SafetyFailure(f"{name}: expected success but command failed: {proc.stderr or proc.stdout}")
    print(f"PASS: {name}")
    return proc


def docker(*args: str, check: bool = True) -> subprocess.CompletedProcess[str]:
    return support.docker(*args, check=check)


def ensure_alpine() -> None:
    if support.docker("image", "inspect", "alpine:3.20", check=False).returncode != 0:
        support.docker("pull", "alpine:3.20")


def test_project_collision(repo: Path, artifacts: Path) -> None:
    run_id = os.urandom(16).hex()
    project = f"wp04c2l-collision-{run_id[:10]}"
    ensure_alpine()
    name = f"{project}-db"
    support.docker(
        "run", "-d", "--name", name, "--label", f"com.docker.compose.project={project}",
        "--label", "org.crowler.e2e.run=foreign", "alpine:3.20", "sleep", "60",
    )
    try:
        expect_failure(
            "existing Compose project is not taken over",
            [
                "prepare", "--run-id", run_id, "--project", project, "--repo-root", str(repo),
                "--artifacts", str(artifacts / "collision"),
            ],
            "prepare should refuse a foreign project",
        )
        if support.docker("inspect", name, check=False).returncode != 0:
            raise SafetyFailure("collision test: foreign container was removed")
    finally:
        support.docker("rm", "-f", name, check=False)


def test_populated_work_root(repo: Path, artifacts: Path) -> None:
    root = Path(tempfile.mkdtemp(prefix="wp04c2l-work-root.", dir=os.environ.get("TMPDIR", "/tmp")))
    sentinel = root / "user-data.txt"
    sentinel.write_text("must survive\n", encoding="utf-8")
    run_id = os.urandom(16).hex()
    project = f"wp04c2l-work-{run_id[:10]}"
    try:
        proc = expect_success(
            "prepopulated work root accepted as a root, not a target",
            [
                "prepare", "--run-id", run_id, "--project", project, "--repo-root", str(repo),
                "--work-root", str(root), "--artifacts", str(artifacts / "work-root"),
            ],
        )
        data = json.loads(proc.stdout)
        work = Path(data["work"])
        if root not in work.parents:
            raise SafetyFailure("work directory is not a child of the supplied root")
        if not sentinel.exists():
            raise SafetyFailure("user sentinel disappeared during prepare")
        expect_success(
            "cleanup removes only the run-owned work child",
            [
                "cleanup", "--run-id", run_id, "--project", project, "--work", str(work),
                "--artifacts", str(artifacts / "work-root"),
            ],
        )
        if not sentinel.exists():
            raise SafetyFailure("user sentinel disappeared during cleanup")
        if work.exists():
            raise SafetyFailure("run-owned work child was not removed")
    finally:
        shutil.rmtree(root, ignore_errors=True)


def test_concurrent_runs(repo: Path, artifacts: Path) -> None:
    first = os.urandom(16).hex()
    second = os.urandom(16).hex()
    p1 = f"wp04c2l-concurrent-{first[:10]}"
    p2 = f"wp04c2l-concurrent-{second[:10]}"
    proc1 = expect_success(
        "first concurrent run receives an independent work identity",
        ["prepare", "--run-id", first, "--project", p1, "--repo-root", str(repo), "--artifacts", str(artifacts / "c1")],
    )
    proc2 = expect_success(
        "second concurrent run receives an independent work identity",
        ["prepare", "--run-id", second, "--project", p2, "--repo-root", str(repo), "--artifacts", str(artifacts / "c2")],
    )
    w1 = Path(json.loads(proc1.stdout)["work"])
    w2 = Path(json.loads(proc2.stdout)["work"])
    if w1 == w2 or w1.parent != w2.parent:
        raise SafetyFailure("concurrent runs did not receive disjoint work directories")
    for run_id, project, work, art in ((first, p1, w1, "c1"), (second, p2, w2, "c2")):
        expect_success(
            "concurrent run cleanup is independent",
            ["cleanup", "--run-id", run_id, "--project", project, "--work", str(work), "--artifacts", str(artifacts / art)],
        )


def test_unsafe_bindings() -> None:
    run_id = "a" * 32
    images = {"api": "api:test", "events": "events:test", "model": "model:test"}
    labels = {support.RUN_LABEL: run_id}
    valid = {
        "services": {
            "crowler-api": {"image": images["api"], "labels": labels, "ports": [{"target": 8080, "host_ip": "127.0.0.1", "published": ""}]},
            "crowler-events": {"image": images["events"], "labels": labels, "ports": [{"target": 8082, "host_ip": "127.0.0.1", "published": ""}]},
            "crowler-db": {"image": "postgres:15", "labels": labels, "ports": []},
            "crowler-e2e-model": {"image": images["model"], "labels": labels, "ports": []},
            "crowler-e2e-slow-model": {"image": images["model"], "labels": labels, "ports": []},
        },
        "networks": {"crowler-net": {"labels": labels}},
    }
    if support.validate_compose_document(valid, run_id, images["api"], images["events"], images["model"]):
        raise SafetyFailure("valid loopback-only Compose document was rejected")
    print("PASS: loopback-only Compose document accepted")

    cases = []
    config = json.loads(json.dumps(valid))
    config["services"]["crowler-api"]["ports"][0]["host_ip"] = "0.0.0.0"
    cases.append(("wildcard API bind rejected", config))
    config = json.loads(json.dumps(valid))
    config["services"]["crowler-api"]["ports"][0]["published"] = "18085"
    cases.append(("fixed API port rejected", config))
    config = json.loads(json.dumps(valid))
    config["services"]["crowler-db"]["ports"] = [{"target": 5432, "host_ip": "127.0.0.1", "published": "55485"}]
    cases.append(("database host publication rejected", config))
    config = json.loads(json.dumps(valid))
    config["services"]["crowler-e2e-model"]["network_mode"] = "host"
    cases.append(("host networking for fixtures rejected", config))
    for name, config in cases:
        if not support.validate_compose_document(config, run_id, images["api"], images["events"], images["model"]):
            raise SafetyFailure(f"{name}: unsafe document was accepted")
        print(f"PASS: {name}")


def test_invalid_project(repo: Path, artifacts: Path) -> None:
    expect_failure(
        "unsafe project name rejected before Docker mutation",
        ["prepare", "--run-id", os.urandom(16).hex(), "--project", "Unsafe_Project", "--repo-root", str(repo), "--artifacts", str(artifacts / "invalid")],
        "uppercase/underscore project must fail",
    )


def test_exact_cleanup(repo: Path, artifacts: Path) -> None:
    run_id = os.urandom(16).hex()
    project = f"wp04c2l-cleanup-{run_id[:10]}"
    art = artifacts / "cleanup"
    proc = expect_success(
        "cleanup fixture work directory prepared",
        ["prepare", "--run-id", run_id, "--project", project, "--repo-root", str(repo), "--artifacts", str(art)],
    )
    work = Path(json.loads(proc.stdout)["work"])
    ensure_alpine()
    support.docker("run", "-d", "--name", f"{project}-db", "--label", f"org.crowler.e2e.run={run_id}",
                   "--label", f"com.docker.compose.project={project}", "alpine:3.20", "sleep", "60")
    support.docker("volume", "create", "--label", f"org.crowler.e2e.run={run_id}",
                   "--label", f"com.docker.compose.project={project}", f"{project}_db_data")
    support.docker("network", "create", "--label", f"org.crowler.e2e.run={run_id}",
                   "--label", f"com.docker.compose.project={project}", f"{project}_crowler-net")
    expect_success(
        "exact-label cleanup removes owned resources",
        ["cleanup", "--run-id", run_id, "--project", project, "--work", str(work), "--artifacts", str(art)],
    )
    for kind, name in (("container", f"{project}-db"), ("volume", f"{project}_db_data"), ("network", f"{project}_crowler-net")):
        if kind == "container":
            exists = support.docker("inspect", name, check=False).returncode == 0
        elif kind == "volume":
            exists = name in support.docker_names(["volume", "ls", "--format", "{{.Name}}"])
        else:
            exists = name in support.docker_names(["network", "ls", "--format", "{{.Name}}"])
        if exists:
            raise SafetyFailure(f"owned {kind} survived cleanup: {name}")
    expect_success(
        "cleanup is idempotent after successful removal",
        ["cleanup", "--run-id", run_id, "--project", project, "--work", str(work), "--artifacts", str(art)],
    )


def test_stale_pid_file(repo: Path, artifacts: Path) -> None:
    run_id = os.urandom(16).hex()
    project = f"wp04c2l-pid-{run_id[:10]}"
    proc = expect_success(
        "stale-PID fixture work directory prepared",
        ["prepare", "--run-id", run_id, "--project", project, "--repo-root", str(repo), "--artifacts", str(artifacts / "pid")],
    )
    work = Path(json.loads(proc.stdout)["work"])
    decoy = subprocess.Popen(["sleep", "60"])
    try:
        (work / "model.pid").write_text(f"{decoy.pid}\n", encoding="utf-8")
        expect_success(
            "cleanup ignores arbitrary PID files",
            ["cleanup", "--run-id", run_id, "--project", project, "--work", str(work), "--artifacts", str(artifacts / "pid")],
        )
        if decoy.poll() is not None:
            raise SafetyFailure("stale PID file caused an unrelated process to be killed")
    finally:
        decoy.terminate()
        try:
            decoy.wait(timeout=5)
        except subprocess.TimeoutExpired:
            decoy.kill()


def test_artifact_redaction(artifacts: Path) -> None:
    source = artifacts / "redaction-source.txt"
    target = artifacts / "redaction-output.txt"
    source.parent.mkdir(parents=True, exist_ok=True)
    source.write_text(
        "password=supersecret\npostgres://user:pass@127.0.0.1/db\n"
        "source_id=42\nfixture-resource\nAuthorization: Bearer abc.def.ghi\n",
        encoding="utf-8",
    )
    expect_success(
        "artifact redaction removes secrets and raw fixture arguments",
        ["sanitize", "--input", str(source), "--output", str(target)],
    )
    text = target.read_text(encoding="utf-8")
    for forbidden in ("supersecret", "postgres://", "source_id=42", "fixture-resource", "Bearer abc"):
        if forbidden in text:
            raise SafetyFailure(f"artifact redaction left {forbidden!r}: {text}")


def test_stale_image(artifacts: Path) -> None:
    image = f"wp04c2l-stale-image:{os.urandom(4).hex()}"
    dockerfile = "FROM scratch\nLABEL org.crowler.e2e.run=foreign\nLABEL org.crowler.e2e.source.sha=deadbeef\nLABEL org.crowler.e2e.source.tree=deadbeef\n"
    proc = subprocess.run(["docker", "build", "-t", image, "-"], input=dockerfile, text=True, capture_output=True)
    if proc.returncode != 0:
        raise SafetyFailure(f"build stale test image: {proc.stderr}")
    try:
        expect_failure(
            "stale image provenance rejected",
            ["image-info", "--image", image, "--run-id", "a" * 32, "--source-sha", "b" * 40,
             "--source-tree", "c" * 64, "--require-source"],
            "wrong source labels must not be accepted",
        )
    finally:
        support.docker("image", "rm", "-f", image, check=False)


def write_report(artifacts: Path, checks: list[dict[str, str]]) -> None:
    failures = [c for c in checks if c["status"] == "FAIL"]
    results = {"conclusion": "FAIL" if failures else "PASS", "checks": checks}
    artifacts.mkdir(parents=True, exist_ok=True)
    (artifacts / "safety-results.json").write_text(json.dumps(results, indent=2) + "\n", encoding="utf-8")
    lines = ["# WP-04C2L E2E Safety Tests", "", f"Conclusion: **{results['conclusion']}**", "", "| Status | Check |", "| --- | --- |"]
    lines += [f"| {c['status']} | {c['name']} |" for c in checks]
    (artifacts / "SAFETY_REPORT.md").write_text("\n".join(lines) + "\n", encoding="utf-8")


def main() -> int:
    parser = argparse.ArgumentParser()
    parser.add_argument("--repo-root", required=True)
    parser.add_argument("--artifacts", required=True)
    args = parser.parse_args()
    repo = Path(args.repo_root).resolve()
    artifacts = Path(args.artifacts).resolve()
    artifacts.mkdir(parents=True, exist_ok=True)
    checks: list[dict[str, str]] = []

    tests = [
        ("project collision", lambda: test_project_collision(repo, artifacts)),
        ("populated work root", lambda: test_populated_work_root(repo, artifacts)),
        ("concurrent runs", lambda: test_concurrent_runs(repo, artifacts)),
        ("unsafe bindings", test_unsafe_bindings),
        ("invalid project", lambda: test_invalid_project(repo, artifacts)),
        ("exact cleanup", lambda: test_exact_cleanup(repo, artifacts)),
        ("stale PID file", lambda: test_stale_pid_file(repo, artifacts)),
        ("artifact redaction", lambda: test_artifact_redaction(artifacts)),
        ("stale image", lambda: test_stale_image(artifacts)),
    ]
    for name, fn in tests:
        try:
            fn()
            checks.append({"name": name, "status": "PASS"})
        except Exception as exc:  # noqa: BLE001
            print(f"FAIL: {name}: {exc}", file=sys.stderr)
            checks.append({"name": name, "status": "FAIL", "detail": str(exc)})
    write_report(artifacts, checks)
    return 0 if all(c["status"] == "PASS" for c in checks) else 1


if __name__ == "__main__":
    raise SystemExit(main())
