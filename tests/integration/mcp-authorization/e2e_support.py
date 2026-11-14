#!/usr/bin/env python3
"""Ownership-safe support for the MCP authorization E2E fixture.

The shell scenario driver owns assertions; this module owns the risky parts:
run identity, Docker preflight, Compose override generation, resource
provenance, structured artifacts and exact-label cleanup. It never runs broad
prune/down commands and never removes a resource whose ownership label does not
match the current run.
"""

from __future__ import annotations

import argparse
import json
import os
import re
import shlex
import shutil
import socket
import subprocess
import sys
import tarfile
import tempfile
import time
from pathlib import Path
from typing import Any

RUN_LABEL = "org.crowler.e2e.run"
PROJECT_LABEL = "org.crowler.e2e.project"
SOURCE_SHA_LABEL = "org.crowler.e2e.source.sha"
SOURCE_TREE_LABEL = "org.crowler.e2e.source.tree"
ROLE_LABEL = "org.crowler.e2e.role"
PROJECT_RE = re.compile(r"^[a-z0-9][a-z0-9-]{0,47}$")


class SupportError(RuntimeError):
    pass


def run(args: list[str], *, check: bool = True, capture: bool = True, input_data: str | None = None) -> subprocess.CompletedProcess[str]:
    proc = subprocess.run(
        args,
        check=False,
        text=True,
        input=input_data,
        stdout=subprocess.PIPE if capture else None,
        stderr=subprocess.PIPE if capture else None,
    )
    if check and proc.returncode != 0:
        raise SupportError(f"command failed ({proc.returncode}): {' '.join(args)}\n{proc.stderr or proc.stdout or ''}")
    return proc


def docker(*args: str, check: bool = True, capture: bool = True) -> subprocess.CompletedProcess[str]:
    return run(["docker", *args], check=check, capture=capture)


def docker_json(args: list[str]) -> Any:
    proc = docker(*args)
    return json.loads(proc.stdout or "null")


def label_map(obj: dict[str, Any]) -> dict[str, str]:
    labels = obj.get("Config", {}).get("Labels") or obj.get("Labels") or {}
    return {str(k): str(v) for k, v in labels.items()}


def new_run_id() -> str:
    return os.urandom(16).hex()


def source_tree_digest(root: Path) -> tuple[str, int]:
    excludes = {".git", "bin", "data", ".env", "artifacts"}
    digest = __import__("hashlib").sha256()
    total = 0
    for path in sorted(root.rglob("*")):
        rel = path.relative_to(root)
        if any(part in excludes for part in rel.parts):
            continue
        if path.is_symlink():
            target = os.readlink(path)
            digest.update(f"L\\0{rel}\\0{target}\\0".encode())
            total += 1
            continue
        if not path.is_file():
            continue
        digest.update(f"F\\0{rel}\\0".encode())
        with path.open("rb") as handle:
            while True:
                chunk = handle.read(1024 * 1024)
                if not chunk:
                    break
                digest.update(chunk)
                total += len(chunk)
        digest.update(b"\\0")
    return digest.hexdigest(), total


def validate_project(project: str) -> None:
    if not PROJECT_RE.match(project):
        raise SupportError("project name must match ^[a-z0-9][a-z0-9-]{0,47}$")


def docker_names(args: list[str]) -> list[str]:
    proc = docker(*args, check=False)
    if proc.returncode != 0:
        raise SupportError(f"docker inventory failed: {proc.stderr.strip()}")
    return [line.strip() for line in proc.stdout.splitlines() if line.strip()]


def project_resources(project: str) -> dict[str, list[str]]:
    filters = ["--filter", f"label=com.docker.compose.project={project}"]
    containers = docker_names(["ps", "-a", "-q", *filters])
    volumes = docker_names(["volume", "ls", "-q", *filters])
    networks = docker_names(["network", "ls", "-q", *filters])
    return {"containers": containers, "volumes": volumes, "networks": networks}


def run_label_resources(run_id: str) -> dict[str, list[str]]:
    label = f"label={RUN_LABEL}={run_id}"
    return {
        "containers": docker_names(["ps", "-a", "-q", "--filter", label]),
        "volumes": docker_names(["volume", "ls", "-q", "--filter", label]),
        "networks": docker_names(["network", "ls", "-q", "--filter", label]),
        "images": sorted(set(docker_names(["image", "ls", "-q", "--filter", label]))),
    }


def require_absent(name: str, values: list[str], kind: str) -> None:
    if values:
        raise SupportError(f"{kind} collision for {name}: {', '.join(values)}")


def inspect_labels(kind: str, identifier: str) -> dict[str, str]:
    if kind == "container":
        data = docker_json(["inspect", identifier])[0]
    elif kind == "volume":
        data = docker_json(["volume", "inspect", identifier])[0]
    elif kind == "network":
        data = docker_json(["network", "inspect", identifier])[0]
    elif kind == "image":
        data = docker_json(["image", "inspect", identifier])[0]
    else:
        raise SupportError(f"unknown resource kind {kind}")
    return label_map(data)


def expected_names(project: str) -> dict[str, list[str]]:
    return {
        "containers": [
            f"{project}-db",
            f"{project}-api",
            f"{project}-events",
            f"{project}-model",
            f"{project}-slow-model",
        ],
        "volumes": [f"{project}_db_data", f"{project}_api_data", f"{project}_events_data"],
        "networks": [f"{project}_crowler-net"],
    }


def validate_base_dir(path: Path, repo_root: Path, label: str) -> Path:
    if path.is_symlink():
        raise SupportError(f"{label} must not be a symlink")
    root = path.expanduser().resolve(strict=True)
    if not root.is_dir():
        raise SupportError(f"{label} must be an existing directory")
    if root == Path("/"):
        raise SupportError(f"{label} must not be /")
    if root == Path.home().resolve():
        raise SupportError(f"{label} must not be the home directory")
    repo = repo_root.resolve()
    if root == repo or repo in root.parents:
        raise SupportError(f"{label} must not be inside the repository")
    return root


def prepare_work(work_root: str | None, run_id: str, repo_root: Path) -> Path:
    if work_root:
        root = validate_base_dir(Path(work_root), repo_root, "CROWLER_E2E_WORK")
        work = root / f"crowler-e2e-{run_id}"
        if work.exists():
            raise SupportError(f"run work directory already exists: {work}")
        work.mkdir(mode=0o700)
    else:
        base = validate_base_dir(Path(os.environ.get("TMPDIR", "/tmp")), repo_root, "TMPDIR")
        work = Path(tempfile.mkdtemp(prefix=f"crowler-e2e-{run_id}.", dir=base))
    (work / ".crowler-e2e-owner").write_text(run_id + "\n", encoding="utf-8")
    return work


def cmd_new_run_id(_: argparse.Namespace) -> int:
    print(new_run_id())
    return 0


def cmd_source_digest(args: argparse.Namespace) -> int:
    digest, size = source_tree_digest(Path(args.root))
    print(json.dumps({"sha256": digest, "bytes": size}))
    return 0


def cmd_prepare(args: argparse.Namespace) -> int:
    run_id = args.run_id
    if not re.fullmatch(r"[0-9a-f]{32}", run_id):
        raise SupportError("run ID must be 32 lowercase hex characters")
    validate_project(args.project)
    repo_root = Path(args.repo_root)
    work = prepare_work(args.work_root, run_id, repo_root)
    try:
        docker("version")
        names = expected_names(args.project)
        current_containers = set(docker_names(["ps", "-a", "--format", "{{.Names}}"]))
        current_volumes = set(docker_names(["volume", "ls", "--format", "{{.Name}}"]))
        current_networks = set(docker_names(["network", "ls", "--format", "{{.Name}}"]))
        require_absent("containers", sorted(current_containers.intersection(names["containers"])), "container")
        require_absent("volumes", sorted(current_volumes.intersection(names["volumes"])), "volume")
        require_absent("networks", sorted(current_networks.intersection(names["networks"])), "network")
        project = project_resources(args.project)
        for kind, values in project.items():
            require_absent(f"project {args.project}", values, kind)
        labels = run_label_resources(run_id)
        for kind, values in labels.items():
            require_absent(f"run {run_id}", values, kind)
        if args.api_image or args.events_image or args.model_image:
            for image in [args.api_image, args.events_image, args.model_image]:
                if image:
                    proc = docker("image", "inspect", image, check=False)
                    if proc.returncode == 0:
                        raise SupportError(f"image already exists: {image}")
        preflight = {
            "run_id": run_id,
            "project": args.project,
            "work": str(work),
            "repo_root": str(repo_root),
            "docker": docker("version", "--format", "{{.Server.Version}}").stdout.strip(),
            "expected": names,
            "checked_at": time.strftime("%Y-%m-%dT%H:%M:%SZ", time.gmtime()),
        }
        artifacts = Path(args.artifacts) if args.artifacts else work / "artifacts"
        if args.artifacts:
            validate_base_dir(artifacts.parent, repo_root, "CROWLER_E2E_ARTIFACT_ROOT")
        artifacts.mkdir(mode=0o700, parents=True, exist_ok=True)
        (artifacts / "preflight.json").write_text(json.dumps(preflight, indent=2) + "\n", encoding="utf-8")
        print(json.dumps({"work": str(work), "artifacts": str(artifacts), "preflight": preflight}))
        return 0
    except Exception:
        shutil.rmtree(work, ignore_errors=True)
        raise


def labels_block(run_id: str, project: str, source_sha: str, source_tree: str, role: str, indent: str) -> str:
    return "\n".join(
        f"{indent}{key}: \"{value}\""
        for key, value in [
            (RUN_LABEL, run_id),
            (PROJECT_LABEL, project),
            (SOURCE_SHA_LABEL, source_sha),
            (SOURCE_TREE_LABEL, source_tree),
            (ROLE_LABEL, role),
        ]
    )


def cmd_write_overrides(args: argparse.Namespace) -> int:
    work = Path(args.work)
    config = Path(args.config)
    override = Path(args.output)
    labels_api = labels_block(args.run_id, args.project, args.source_sha, args.source_tree, "api", "      ")
    labels_events = labels_block(args.run_id, args.project, args.source_sha, args.source_tree, "events", "      ")
    labels_db = labels_block(args.run_id, args.project, args.source_sha, args.source_tree, "db", "      ")
    labels_model = labels_block(args.run_id, args.project, args.source_sha, args.source_tree, "model", "      ")
    labels_slow = labels_block(args.run_id, args.project, args.source_sha, args.source_tree, "slow-model", "      ")
    common_depends = f"""    depends_on:
      crowler-db:
        condition: service_healthy
      crowler-e2e-model:
        condition: service_healthy
      crowler-e2e-slow-model:
        condition: service_healthy
"""
    text = f"""services:
  crowler-db:
    container_name: {args.project}-db
    ports: !reset []
    labels:
{labels_db}
  crowler-api:
    container_name: {args.project}-api
    image: {args.api_image}
    ports: !override
      - "127.0.0.1::8080"
    volumes:
      - {work}/agents:/app/agents:ro
      - {config}:/app/config.yaml:ro
      - api_data:/app/data
    labels:
{labels_api}
{common_depends}  crowler-events:
    container_name: {args.project}-events
    image: {args.events_image}
    ports: !override
      - "127.0.0.1::8082"
    volumes:
      - {work}/agents:/app/agents:ro
      - {work}/bin:/opt/crowler/bin:ro
      - {work}/state:/tmp/crowler-mcp-state
      - {config}:/app/config.yaml:ro
      - events_data:/app/data
    labels:
{labels_events}
{common_depends}  crowler-e2e-model:
    image: {args.model_image}
    container_name: {args.project}-model
    environment:
      CROWLER_E2E_MODEL_PORT: "18095"
    networks:
      - crowler-net
    read_only: true
    labels:
{labels_model}
    healthcheck:
      test: ["CMD-SHELL", "wget -qO- http://127.0.0.1:18095/health >/dev/null"]
      interval: 2s
      timeout: 2s
      retries: 30
  crowler-e2e-slow-model:
    image: {args.model_image}
    container_name: {args.project}-slow-model
    environment:
      CROWLER_E2E_MODEL_PORT: "18095"
      CROWLER_E2E_MODEL_DELAY_MS: "10000"
    networks:
      - crowler-net
    read_only: true
    labels:
{labels_slow}
    healthcheck:
      test: ["CMD-SHELL", "wget -qO- http://127.0.0.1:18095/health >/dev/null"]
      interval: 2s
      timeout: 2s
      retries: 30
volumes:
  api_data:
    labels:
{labels_block(args.run_id, args.project, args.source_sha, args.source_tree, "volume-api", "      ")}
  events_data:
    labels:
{labels_block(args.run_id, args.project, args.source_sha, args.source_tree, "volume-events", "      ")}
  db_data:
    labels:
{labels_block(args.run_id, args.project, args.source_sha, args.source_tree, "volume-db", "      ")}
networks:
  crowler-net:
    labels:
{labels_block(args.run_id, args.project, args.source_sha, args.source_tree, "network", "      ")}
"""
    override.write_text(text, encoding="utf-8")
    print(override)
    return 0


def compose_config(args: argparse.Namespace) -> dict[str, Any]:
    cmd = [
        "docker",
        "compose",
        "-p",
        args.project,
        "--env-file",
        str(Path(args.work) / "repo" / ".env"),
        "-f",
        str(Path(args.work) / "repo" / "docker-compose.yml"),
        "-f",
        args.override,
        "config",
        "--format",
        "json",
    ]
    proc = run(cmd)
    return json.loads(proc.stdout)


def validate_compose_document(config: dict[str, Any], run_id: str, api_image: str, events_image: str, model_image: str) -> list[str]:
    services = config.get("services", {})
    errors: list[str] = []
    expected = {
        "crowler-api": ("8080", api_image),
        "crowler-events": ("8082", events_image),
        "crowler-db": (None, None),
        "crowler-e2e-model": (None, model_image),
        "crowler-e2e-slow-model": (None, model_image),
    }
    for name, (target, image) in expected.items():
        service = services.get(name)
        if not service:
            errors.append(f"missing service {name}")
            continue
        if image and service.get("image") != image:
            errors.append(f"{name} image is {service.get('image')}, expected {image}")
        if service.get("network_mode") == "host":
            errors.append(f"{name} uses host networking")
        ports = service.get("ports") or []
        labels = service.get("labels") or {}
        if labels.get(RUN_LABEL) != run_id:
            errors.append(f"{name} missing run label")
        if target is None:
            if ports:
                errors.append(f"{name} must not publish host ports: {ports}")
        else:
            if len(ports) != 1:
                errors.append(f"{name} must have exactly one loopback port")
            else:
                port = ports[0]
                if str(port.get("target")) != target:
                    errors.append(f"{name} target {port.get('target')} != {target}")
                if port.get("host_ip") != "127.0.0.1":
                    errors.append(f"{name} host_ip {port.get('host_ip')} is not 127.0.0.1")
                published = str(port.get("published") or "")
                if published and published != "0":
                    errors.append(f"{name} fixed host port is not allowed: {published}")
    networks = config.get("networks") or {}
    net = networks.get("crowler-net") or {}
    if (net.get("labels") or {}).get(RUN_LABEL) != run_id:
        errors.append("crowler-net missing run label")
    return errors


def cmd_validate_compose(args: argparse.Namespace) -> int:
    config = compose_config(args)
    errors = validate_compose_document(config, args.run_id, args.api_image, args.events_image, args.model_image)
    if errors:
        raise SupportError("unsafe Compose configuration:\n- " + "\n- ".join(errors))
    print(json.dumps({"services": sorted(config.get("services", {})), "network": "crowler-net", "validated": True}))
    return 0


def cmd_image_info(args: argparse.Namespace) -> int:
    data = docker_json(["image", "inspect", args.image])[0]
    labels = label_map(data)
    info = {
        "image": args.image,
        "id": data.get("Id"),
        "repo_digests": data.get("RepoDigests") or [],
        "labels": labels,
        "owned": labels.get(RUN_LABEL) == args.run_id,
    }
    if args.require_source:
        if labels.get(SOURCE_SHA_LABEL) != args.source_sha or labels.get(SOURCE_TREE_LABEL) != args.source_tree:
            raise SupportError(
                f"image provenance mismatch for {args.image}: "
                f"sha={labels.get(SOURCE_SHA_LABEL)} tree={labels.get(SOURCE_TREE_LABEL)}"
            )
    print(json.dumps(info))
    return 0


def cmd_verify_runtime(args: argparse.Namespace) -> int:
    services = ["crowler-api", "crowler-events", "crowler-db", "crowler-e2e-model", "crowler-e2e-slow-model"]
    role_for_service = {
        "crowler-api": "api",
        "crowler-events": "events",
        "crowler-e2e-model": "model",
        "crowler-e2e-slow-model": "model",
    }
    images = {row["role"]: row for row in json.loads(Path(args.images_json).read_text(encoding="utf-8"))}
    result: dict[str, Any] = {}
    errors: list[str] = []
    for service in services:
        cid = run(
            ["docker", "compose", "-p", args.project, "-f", str(Path(args.work) / "repo" / "docker-compose.yml"), "-f", args.override, "ps", "-q", service]
        ).stdout.strip()
        if not cid:
            errors.append(f"service {service} has no container")
            continue
        data = docker_json(["inspect", cid])[0]
        labels = label_map(data)
        if labels.get(RUN_LABEL) != args.run_id:
            errors.append(f"{service} container missing run label")
        role = role_for_service.get(service)
        if role:
            expected = images.get(role)
            if not expected:
                errors.append(f"{service} has no verified image metadata for role {role}")
            else:
                if data.get("Image") != expected.get("id"):
                    errors.append(f"{service} runs image {data.get('Image')}, expected {expected.get('id')}")
                image_data = docker_json(["image", "inspect", expected.get("id")])[0]
                image_labels = label_map(image_data)
                if image_labels.get(SOURCE_SHA_LABEL) != args.source_sha:
                    errors.append(f"{service} image source SHA label mismatch")
                if image_labels.get(SOURCE_TREE_LABEL) != args.source_tree:
                    errors.append(f"{service} image source tree label mismatch")
        bindings = data.get("HostConfig", {}).get("PortBindings") or {}
        for target, expected_port in (("8080/tcp", service == "crowler-api"), ("8082/tcp", service == "crowler-events")):
            if expected_port:
                entries = bindings.get(target) or []
                if len(entries) != 1 or entries[0].get("HostIp") != "127.0.0.1":
                    errors.append(f"{service} {target} is not loopback-only: {entries}")
        result[service] = {"id": cid, "image": data.get("Image"), "labels": labels, "ports": bindings}
    if errors:
        raise SupportError("runtime provenance/port verification failed:\n- " + "\n- ".join(errors))
    print(json.dumps(result))
    return 0


def append_json(path: Path, value: Any) -> None:
    path.parent.mkdir(parents=True, exist_ok=True)
    with path.open("a", encoding="utf-8") as handle:
        handle.write(json.dumps(value, sort_keys=True) + "\n")


def cmd_record(args: argparse.Namespace) -> int:
    append_json(
        Path(args.results),
        {
            "ts": time.strftime("%Y-%m-%dT%H:%M:%SZ", time.gmtime()),
            "name": args.name,
            "status": args.status.upper(),
            "detail": args.detail or "",
        },
    )
    return 0


def cmd_finalize(args: argparse.Namespace) -> int:
    artifacts = Path(args.artifacts)
    results_path = Path(args.results)
    checks = []
    if results_path.exists():
        checks = [json.loads(line) for line in results_path.read_text(encoding="utf-8").splitlines() if line.strip()]
    counts = {"PASS": 0, "FAIL": 0, "BLOCKED": 0, "SKIP": 0}
    for check in checks:
        counts[check["status"]] = counts.get(check["status"], 0) + 1
    known = {"PASS", "FAIL", "BLOCKED", "SKIP"}
    unknown = sum(1 for check in checks if check.get("status") not in known)
    conclusion = "PASS"
    if counts["FAIL"]:
        conclusion = "FAIL"
    if counts["BLOCKED"] or counts["SKIP"] or unknown:
        conclusion = "BLOCKED"
    if not checks:
        conclusion = "BLOCKED"
    images = []
    images_path = artifacts / "images.json"
    if images_path.exists():
        images = json.loads(images_path.read_text(encoding="utf-8"))
    cleanup_path = artifacts / "cleanup.json"
    cleanup = json.loads(cleanup_path.read_text(encoding="utf-8")) if cleanup_path.exists() else None
    if cleanup and cleanup.get("status") == "FAIL":
        conclusion = "FAIL"
    results = {
        "run_id": args.run_id,
        "project": args.project,
        "source_sha": args.source_sha,
        "source_tree": args.source_tree,
        "conclusion": conclusion,
        "counts": counts,
        "checks": checks,
        "images": images,
        "cleanup": cleanup,
    }
    (artifacts / "results.json").write_text(json.dumps(results, indent=2) + "\n", encoding="utf-8")
    lines = [
        "# MCP Authorization E2E Report",
        "",
        f"- Run ID: `{args.run_id}`",
        f"- Project: `{args.project}`",
        f"- Source SHA: `{args.source_sha}`",
        f"- Source tree digest: `{args.source_tree}`",
        f"- Conclusion: **{conclusion}**",
        f"- Checks: PASS {counts['PASS']}, FAIL {counts['FAIL']}, BLOCKED {counts['BLOCKED']}, SKIP {counts['SKIP']}",
    ]
    if images:
        lines += ["", "## Images", "", "| Role | Image | ID | Owned |", "| --- | --- | --- | --- |"]
        for image in images:
            lines.append(f"| {image.get('role')} | `{image.get('image')}` | `{image.get('id')}` | {image.get('owned')} |")
    lines += ["", "## Checks", "", "| Status | Check | Detail |", "| --- | --- | --- |"]
    for check in checks:
        lines.append(f"| {check['status']} | {check['name']} | {check['detail']} |")
    if cleanup is not None:
        lines += ["", "## Cleanup", "", f"- Status: **{cleanup.get('status')}**", f"- Detail: {cleanup.get('detail')}"]
    (artifacts / "REPORT.md").write_text("\n".join(lines) + "\n", encoding="utf-8")
    print(json.dumps({"conclusion": conclusion, "counts": counts, "results": str(artifacts / "results.json")}))
    return 0 if conclusion == "PASS" else 1


def safe_remove_container(cid: str, run_id: str) -> tuple[bool, str]:
    try:
        labels = inspect_labels("container", cid)
    except Exception as exc:  # noqa: BLE001
        return False, f"inspect container {cid}: {exc}"
    if labels.get(RUN_LABEL) != run_id:
        return False, f"container {cid} lacks run label"
    proc = docker("rm", "-f", cid, check=False)
    return proc.returncode == 0, (proc.stderr or proc.stdout).strip()


def safe_remove_volume(name: str, run_id: str) -> tuple[bool, str]:
    try:
        labels = inspect_labels("volume", name)
    except Exception as exc:  # noqa: BLE001
        return False, f"inspect volume {name}: {exc}"
    if labels.get(RUN_LABEL) != run_id:
        return False, f"volume {name} lacks run label"
    proc = docker("volume", "rm", name, check=False)
    return proc.returncode == 0, (proc.stderr or proc.stdout).strip()


def safe_remove_network(name: str, run_id: str) -> tuple[bool, str]:
    try:
        labels = inspect_labels("network", name)
    except Exception as exc:  # noqa: BLE001
        return False, f"inspect network {name}: {exc}"
    if labels.get(RUN_LABEL) != run_id:
        return False, f"network {name} lacks run label"
    proc = docker("network", "rm", name, check=False)
    return proc.returncode == 0, (proc.stderr or proc.stdout).strip()


def safe_remove_image(image_id: str, run_id: str) -> tuple[bool, str]:
    proc = docker("image", "inspect", image_id, check=False)
    if proc.returncode != 0:
        return True, "already absent"
    try:
        labels = inspect_labels("image", image_id)
    except Exception as exc:  # noqa: BLE001
        return False, f"inspect image {image_id}: {exc}"
    if labels.get(RUN_LABEL) != run_id:
        return False, f"image {image_id} is not owned by this run"
    proc = docker("image", "rm", image_id, check=False)
    return proc.returncode == 0, (proc.stderr or proc.stdout).strip()


def cmd_cleanup(args: argparse.Namespace) -> int:
    artifacts = Path(args.artifacts)
    artifacts.mkdir(parents=True, exist_ok=True)
    if args.keep:
        cleanup = {
            "status": "KEPT",
            "detail": "CROWLER_E2E_KEEP=1; operator cleanup required",
            "commands": [
                "python3 "
                + " ".join(
                    shlex.quote(value)
                    for value in [
                        str(Path(__file__).resolve()),
                        "cleanup", "--run-id", args.run_id, "--project", args.project,
                        "--work", args.work, "--artifacts", args.artifacts,
                    ]
                ),
            ],
        }
        (artifacts / "cleanup.json").write_text(json.dumps(cleanup, indent=2) + "\n", encoding="utf-8")
        cleanup_script = artifacts / "cleanup.sh"
        cleanup_script.write_text(
            "#!/usr/bin/env bash\nset -euo pipefail\n"
            "exec python3 "
            + " ".join(
                shlex.quote(value)
                for value in [
                    str(Path(__file__).resolve()),
                    "cleanup", "--run-id", args.run_id, "--project", args.project,
                    "--work", args.work, "--artifacts", args.artifacts,
                ]
            )
            + "\n",
            encoding="utf-8",
        )
        cleanup_script.chmod(0o700)
        print(json.dumps(cleanup))
        return 0

    errors: list[str] = []
    removed: list[str] = []
    resources = run_label_resources(args.run_id)
    # A matching name without the run label is a collision that must be
    # preserved for the operator, not removed as a fallback.
    current_containers = set(docker_names(["ps", "-a", "--format", "{{.Names}}"]))
    run_container_names = set(docker_names(["ps", "-a", "--format", "{{.Names}}", "--filter", f"label={RUN_LABEL}={args.run_id}"]))
    for name in expected_names(args.project)["containers"]:
        if name in current_containers and name not in run_container_names:
            errors.append(f"container name {name} exists but is not owned by this run")
    current_volumes = set(docker_names(["volume", "ls", "--format", "{{.Name}}"]))
    run_volume_names = set(resources["volumes"])
    for name in expected_names(args.project)["volumes"]:
        if name in current_volumes and name not in run_volume_names:
            errors.append(f"volume name {name} exists but is not owned by this run")
    current_networks = set(docker_names(["network", "ls", "--format", "{{.Name}}"]))
    run_network_names = set(docker_names(["network", "ls", "--format", "{{.Name}}", "--filter", f"label={RUN_LABEL}={args.run_id}"]))
    for name in expected_names(args.project)["networks"]:
        if name in current_networks and name not in run_network_names:
            errors.append(f"network name {name} exists but is not owned by this run")
    for cid in resources["containers"]:
        ok, detail = safe_remove_container(cid, args.run_id)
        (removed if ok else errors).append(f"container {cid}: {detail}" if detail else f"container {cid}")
    for name in resources["volumes"]:
        ok, detail = safe_remove_volume(name, args.run_id)
        (removed if ok else errors).append(f"volume {name}: {detail}" if detail else f"volume {name}")
    for name in resources["networks"]:
        ok, detail = safe_remove_network(name, args.run_id)
        (removed if ok else errors).append(f"network {name}: {detail}" if detail else f"network {name}")
    removed_images = set()
    for image_id in resources.get("images", []):
        ok, detail = safe_remove_image(image_id, args.run_id)
        removed_images.add(image_id)
        (removed if ok else errors).append(f"image {image_id}: {detail}" if detail else f"image {image_id}")
    images_path = artifacts / "images.json"
    if images_path.exists():
        for image in json.loads(images_path.read_text(encoding="utf-8")):
            if not image.get("owned"):
                continue
            image_id = image.get("id") or image.get("image")
            if image_id in removed_images:
                continue
            ok, detail = safe_remove_image(image_id, args.run_id)
            (removed if ok else errors).append(f"image {image.get('image')}: {detail}" if detail else f"image {image.get('image')}")

    work = Path(args.work)
    if not work.exists():
        pass
    elif work.is_symlink():
        errors.append(f"work path is a symlink: {work}")
    else:
        marker = work / ".crowler-e2e-owner"
        work_owned = marker.exists() and marker.read_text(encoding="utf-8").strip() == args.run_id
        if not work_owned:
            errors.append(f"work directory exists without the matching owner marker: {work}")
        elif errors:
            errors.append(f"work {work} kept because Docker cleanup failed")
        elif work.resolve() == Path("/") or work.resolve() == Path.home().resolve():
            errors.append(f"refusing to remove unsafe work path: {work}")
        else:
            shutil.rmtree(work)
            removed.append(f"work {work}")

    cleanup = {
        "status": "PASS" if not errors else "FAIL",
        "detail": "removed run-owned resources" if not errors else "; ".join(errors),
        "removed": removed,
        "errors": errors,
    }
    (artifacts / "cleanup.json").write_text(json.dumps(cleanup, indent=2) + "\n", encoding="utf-8")
    print(json.dumps(cleanup))
    return 0 if not errors else 2


def cmd_sanitize(args: argparse.Namespace) -> int:
    text = Path(args.input).read_text(encoding="utf-8", errors="replace")
    secrets = []
    if args.secret_file:
        for line in Path(args.secret_file).read_text(encoding="utf-8").splitlines():
            if "=" in line:
                value = line.split("=", 1)[1].strip()
                if len(value) >= 8:
                    secrets.append(value)
    for secret in secrets:
        text = text.replace(secret, "<redacted>")
    patterns = [
        r"postgres(?:ql)?://[^\s\"']+",
        r"Bearer\s+[A-Za-z0-9._~+/=-]+",
        r"eyJ[A-Za-z0-9_-]+\.[A-Za-z0-9_-]+\.[A-Za-z0-9_-]+",
        r"(?i)(password|secret|token|hmac_secret|api_key)\s*[:=]\s*[^\s,}]+",
        r"\"access_token\"\s*:\s*\"[^\"]+\"",
        r"\"source_id\"\s*:\s*[0-9]+",
        r"source_id\s*[:=]\s*[0-9]+",
        r"fixture-resource",
    ]
    for pattern in patterns:
        text = re.sub(pattern, "<redacted>", text)
    Path(args.output).write_text(text, encoding="utf-8")
    return 0


def main() -> int:
    parser = argparse.ArgumentParser()
    sub = parser.add_subparsers(dest="command", required=True)

    p = sub.add_parser("new-run-id")
    p.set_defaults(func=cmd_new_run_id)

    p = sub.add_parser("source-digest")
    p.add_argument("--root", required=True)
    p.set_defaults(func=cmd_source_digest)

    p = sub.add_parser("prepare")
    p.add_argument("--run-id", required=True)
    p.add_argument("--project", required=True)
    p.add_argument("--repo-root", required=True)
    p.add_argument("--work-root", default="")
    p.add_argument("--artifacts", default="")
    p.add_argument("--api-image", default="")
    p.add_argument("--events-image", default="")
    p.add_argument("--model-image", default="")
    p.set_defaults(func=cmd_prepare)

    p = sub.add_parser("write-overrides")
    p.add_argument("--work", required=True)
    p.add_argument("--output", required=True)
    p.add_argument("--project", required=True)
    p.add_argument("--run-id", required=True)
    p.add_argument("--source-sha", required=True)
    p.add_argument("--source-tree", required=True)
    p.add_argument("--api-image", required=True)
    p.add_argument("--events-image", required=True)
    p.add_argument("--model-image", required=True)
    p.add_argument("--config", required=True)
    p.set_defaults(func=cmd_write_overrides)

    p = sub.add_parser("validate-compose")
    p.add_argument("--work", required=True)
    p.add_argument("--project", required=True)
    p.add_argument("--run-id", required=True)
    p.add_argument("--override", required=True)
    p.add_argument("--api-image", required=True)
    p.add_argument("--events-image", required=True)
    p.add_argument("--model-image", required=True)
    p.set_defaults(func=cmd_validate_compose)

    p = sub.add_parser("image-info")
    p.add_argument("--image", required=True)
    p.add_argument("--run-id", required=True)
    p.add_argument("--source-sha", default="")
    p.add_argument("--source-tree", default="")
    p.add_argument("--require-source", action="store_true")
    p.set_defaults(func=cmd_image_info)

    p = sub.add_parser("verify-runtime")
    p.add_argument("--work", required=True)
    p.add_argument("--project", required=True)
    p.add_argument("--run-id", required=True)
    p.add_argument("--source-sha", required=True)
    p.add_argument("--source-tree", required=True)
    p.add_argument("--images-json", required=True)
    p.add_argument("--override", required=True)
    p.set_defaults(func=cmd_verify_runtime)

    p = sub.add_parser("record")
    p.add_argument("--results", required=True)
    p.add_argument("--name", required=True)
    p.add_argument("--status", required=True)
    p.add_argument("--detail", default="")
    p.set_defaults(func=cmd_record)

    p = sub.add_parser("finalize")
    p.add_argument("--results", required=True)
    p.add_argument("--artifacts", required=True)
    p.add_argument("--run-id", required=True)
    p.add_argument("--project", required=True)
    p.add_argument("--source-sha", required=True)
    p.add_argument("--source-tree", required=True)
    p.set_defaults(func=cmd_finalize)

    p = sub.add_parser("cleanup")
    p.add_argument("--run-id", required=True)
    p.add_argument("--project", required=True)
    p.add_argument("--work", required=True)
    p.add_argument("--artifacts", required=True)
    p.add_argument("--keep", action="store_true")
    p.set_defaults(func=cmd_cleanup)

    p = sub.add_parser("sanitize")
    p.add_argument("--input", required=True)
    p.add_argument("--output", required=True)
    p.add_argument("--secret-file", default="")
    p.set_defaults(func=cmd_sanitize)

    args = parser.parse_args()
    try:
        return args.func(args)
    except SupportError as exc:
        print(f"BLOCKED: {exc}", file=sys.stderr)
        return 2


if __name__ == "__main__":
    raise SystemExit(main())
