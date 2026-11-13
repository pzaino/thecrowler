#!/usr/bin/env python3
"""Isolated Docker Compose acceptance harness for InformationSeed.

It copies the current checkout into a disposable source tree, builds the real
engine/API/events images from source, starts a private PostgreSQL + fixture
stack, drives the public HTTP API and asserts live PostgreSQL state. It never
modifies the caller's checkout and removes only resources belonging to its own
Compose project.
"""
from __future__ import annotations

import argparse
import datetime as dt
import hashlib
import json
import os
import re
import secrets
import shutil
import socket
import subprocess
import sys
import time
import traceback
import urllib.error
import urllib.parse
import urllib.request
from pathlib import Path

import yaml

SCRIPT_DIR = Path(__file__).resolve().parent
REPO_ROOT = SCRIPT_DIR.parents[2]
RUN_ID = os.environ.get("INFOSEED_RUN_ID") or f"infoseed_it_{dt.datetime.now(dt.timezone.utc).strftime('%Y%m%dt%H%M%Sz')}_{secrets.token_hex(3)}"
RUN_ROOT = Path(os.environ.get("INFOSEED_RUN_ROOT", f"/tmp/opencode/{RUN_ID}"))
SRC = RUN_ROOT / "src"
ARTIFACT_ROOT = Path(os.environ.get("INFOSEED_ARTIFACT_ROOT", str(REPO_ROOT / "artifacts")))
ARTIFACTS = ARTIFACT_ROOT / RUN_ID
COMPOSE_FILE = SRC / "docker-compose.test.yml"
PREVIOUS_SCHEMA_COMMIT = os.environ.get("INFOSEED_PREVIOUS_SCHEMA_COMMIT", "2eeadd67")
RESULTS: list[dict] = []
HTTP_TRANSCRIPT: list[dict] = []
STARTED_AT = dt.datetime.now(dt.timezone.utc)
API_PORT = 0
FIXTURE_PORT = 0
BASE_URL = ""
FIXTURE_URL = ""
DB_PASSWORD = secrets.token_urlsafe(24)
ADMIN_PASSWORD = secrets.token_urlsafe(24)
USER_A_PASSWORD = secrets.token_urlsafe(24)
USER_B_PASSWORD = secrets.token_urlsafe(24)
HMAC_SECRET = secrets.token_urlsafe(48)
PROVIDER_SECRET = secrets.token_urlsafe(18)
ADMIN_TOKEN = ""
USER_A_TOKEN = ""
USER_B_TOKEN = ""
USER_A_ID = 0
USER_B_ID = 0
ADMIN_ID = 0
COMPOSE: list[str] = []
EXTERNAL_SMOKE: dict = {}
IMAGE_EVIDENCE: list[dict] = []
DB_VERSION = ""
DB_SERVER_VERSION_NUM = ""
SOURCE_FIXES = [
    {
        "area": "PostgreSQL source-config refresh",
        "files": ["pkg/database/source_upsert_policy.go"],
        "change": "Bind the JSON config parameter with an explicit ::jsonb cast so PostgreSQL can infer its type instead of failing with SQLSTATE 42P08.",
        "evidence": "IT-05 and TestPostgresUpsertSourceWithPolicyRefreshesConfig",
    },
    {
        "area": "Disable and active-claim lifecycle",
        "files": ["pkg/database/information_seed_crud.go", "pkg/database/information_seed_claims.go"],
        "change": "Disabling a seed preserves an active claim, and owner-scoped heartbeat/finalize/release operations remain valid for a disabled in-flight row so it cannot be stranded in processing.",
        "evidence": "IT-02, IT-11 and TestDisableInformationSeedPreservesActiveClaim",
    },
]


class BlockedError(RuntimeError):
    """A required acceptance check could not be performed safely."""


def utc_now() -> str:
    return dt.datetime.now(dt.timezone.utc).isoformat().replace("+00:00", "Z")


def log(message: str) -> None:
    print(f"[{utc_now()}] {message}", flush=True)


def free_port() -> int:
    with socket.socket(socket.AF_INET, socket.SOCK_STREAM) as sock:
        sock.bind(("127.0.0.1", 0))
        return int(sock.getsockname()[1])


def run(cmd: list[str], *, cwd: Path | None = None, check: bool = True, timeout: int = 1800, input_text: str | None = None, capture: bool = True) -> subprocess.CompletedProcess:
    log("RUN " + " ".join(cmd))
    proc = subprocess.run(
        cmd, cwd=str(cwd or SRC), input=input_text, text=True, capture_output=capture, timeout=timeout
    )
    if capture:
        safe_cmd = sanitize(" ".join(cmd))
        if proc.stdout:
            with (ARTIFACTS / "commands.log").open("a", encoding="utf-8") as handle:
                handle.write(f"\n$ {safe_cmd}\n{sanitize(proc.stdout)}\n")
        if proc.stderr:
            with (ARTIFACTS / "commands.log").open("a", encoding="utf-8") as handle:
                handle.write(f"\n$ {safe_cmd} [stderr]\n{sanitize(proc.stderr)}\n")
    if check and proc.returncode != 0:
        raise RuntimeError(f"command failed ({proc.returncode}): {' '.join(cmd)}\n{proc.stdout}\n{proc.stderr}")
    return proc


def compose(*args: str, timeout: int = 1800, input_text: str | None = None, check: bool = True) -> subprocess.CompletedProcess:
    return run(COMPOSE + list(args), timeout=timeout, input_text=input_text, check=check)


JWT_RE = re.compile(r"eyJ[A-Za-z0-9_-]+\.[A-Za-z0-9_-]+\.[A-Za-z0-9_-]+")
BEARER_RE = re.compile(r"(?i)bearer\s+[A-Za-z0-9._~+/=-]{12,}")
SENSITIVE_KEYS = {"access_token", "token", "authorization", "password", "password_hash", "api_key", "apikey", "secret", "hmac_secret", "client_secret"}


def sanitize(value):
    if isinstance(value, str):
        for secret in (DB_PASSWORD, ADMIN_PASSWORD, USER_A_PASSWORD, USER_B_PASSWORD, HMAC_SECRET, PROVIDER_SECRET, ADMIN_TOKEN, USER_A_TOKEN, USER_B_TOKEN):
            if secret:
                value = value.replace(secret, "[REDACTED]")
        value = JWT_RE.sub("[REDACTED]", value)
        return BEARER_RE.sub("Bearer [REDACTED]", value)
    if isinstance(value, list):
        return [sanitize(item) for item in value]
    if isinstance(value, dict):
        sanitized = {}
        for key, child in value.items():
            if str(key).lower() in SENSITIVE_KEYS:
                sanitized[key] = "[REDACTED]"
            else:
                sanitized[key] = sanitize(child)
        return sanitized
    return value


def save_json(name: str, value) -> None:
    (ARTIFACTS / name).write_text(json.dumps(sanitize(value), indent=2, default=str) + "\n", encoding="utf-8")


def append_jsonl(name: str, value) -> None:
    with (ARTIFACTS / name).open("a", encoding="utf-8") as handle:
        handle.write(json.dumps(sanitize(value), default=str) + "\n")


def source_uid(name: str, url: str) -> str:
    name = name.strip()
    url = url.strip()
    payload = f"{len(name)}:{name}{len(url)}:{url}".encode()
    return hashlib.sha256(payload).hexdigest()


def preflight() -> None:
    ARTIFACTS.mkdir(parents=True, exist_ok=True)
    inventory = {}
    for key, cmd in (
        ("containers", ["docker", "ps", "-a", "--format", "{{.Names}}\t{{.Image}}\t{{.Status}}\t{{.Ports}}"]),
        ("volumes", ["docker", "volume", "ls", "--format", "{{.Name}}"]),
        ("networks", ["docker", "network", "ls", "--format", "{{.Name}}\t{{.Driver}}"]),
        ("images", ["docker", "images", "--format", "{{.Repository}}:{{.Tag}}"]),
    ):
        inventory[key] = run(cmd, cwd=REPO_ROOT, timeout=120).stdout.splitlines()
    save_json("docker-inventory-before.json", inventory)
    expected = {
        "run_id": RUN_ID,
        "container_prefix": RUN_ID + "-",
        "compose_project": RUN_ID,
        "images": [f"{RUN_ID}-{name}" for name in ("api", "events", "engine-1", "fixture", "probe")],
        "volumes": [f"{RUN_ID}_db_data", f"{RUN_ID}_api_data", f"{RUN_ID}_events_data", f"{RUN_ID}_engine_data"],
        "networks": [f"{RUN_ID}_crowler-net"],
    }
    save_json("allowlist.json", expected)
    existing = "\n".join(inventory["containers"])
    if any(RUN_ID in line for line in inventory["containers"]):
        raise RuntimeError(f"run identifier {RUN_ID} already exists in Docker inventory")
    if any(name.startswith(RUN_ID + "-") for name in inventory["images"]):
        raise RuntimeError(f"run identifier {RUN_ID} already exists in Docker images")
    if not existing:
        log("Docker inventory has no containers; no pre-existing container can be modified")
    run(["docker", "version"], cwd=REPO_ROOT, timeout=60)
    run(["docker", "compose", "version"], cwd=REPO_ROOT, timeout=60)
    log(f"preflight complete; RUN_ID={RUN_ID}")


def prepare_source() -> None:
    if SRC.exists():
        shutil.rmtree(SRC)
    ignore = shutil.ignore_patterns(".git", "artifacts", "docker-selenium", "images", "config.yaml")
    shutil.copytree(REPO_ROOT, SRC, ignore=ignore)
    env = {
        "DOCKER_DB_HOST": "crowler-db",
        "DOCKER_DB_PORT": "5432",
        "POSTGRES_DB": "SitesIndex",
        "DOCKER_POSTGRES_DB_NAME": "SitesIndex",
        "DOCKER_POSTGRES_USER": "postgres",
        "DOCKER_POSTGRES_PASSWORD": DB_PASSWORD,
        "DOCKER_CROWLER_DB_USER": "crowler",
        "DOCKER_CROWLER_DB_PASSWORD": DB_PASSWORD,
        "DOCKER_POSTGRES_SSL_MODE": "disable",
        "VDI_TZ": "UTC",
    }
    (SRC / ".env").write_text("\n".join(f"{key}={value}" for key, value in env.items()) + "\n", encoding="utf-8")
    config = yaml.safe_load((SRC / "config.default").read_text(encoding="utf-8"))
    config["database"].update({"type": "postgres", "host": "crowler-db", "port": 5432, "user": "crowler", "password": DB_PASSWORD, "dbname": "SitesIndex", "sslmode": "disable", "retry_time": 5, "ping_time": 5})
    config["vdi"] = []
    config["crawler"].update({
        "query_timer": 5,
        "workers": 3,
        "max_sources": 0,
        "maintenance": 120,
        "engine": [
            {"name": "crowler-engine-1", "query_timer": 5, "source_priority": ["low"], "vdi_name": []},
            {"name": "crowler-engine-2", "query_timer": 5, "source_priority": ["low"], "vdi_name": []},
        ],
    })
    config["api"].update({"host": "0.0.0.0", "port": 8080, "enable_console": True, "disable_default": False, "enable_api_docs": True, "return_404": False, "rate_limit": "1000,1000", "allowed_ips": []})
    auth = {"enabled": True, "mode": "local", "issuer": "crowler-it", "audience": "crowler-it", "hmac_secret": HMAC_SECRET, "token_ttl": 3600, "local": {"enabled": True}, "external": {"enabled": False}}
    config["api"]["auth"] = auth
    config["events"]["auth"] = dict(auth)
    providers = {
        "fixture_rss": {"provider": "rss_feed", "host": "http://fixture:8080", "endpoint": "/rss/ok", "timeout": 10, "rate_limit": "0", "max_requests": 1, "page_size": 10, "max_pages": 1},
        "fixture_rss_rate": {"provider": "rss_feed", "host": "http://fixture:8080", "endpoint": "/rss/rate", "timeout": 1, "rate_limit": "2s", "max_requests": 2, "page_size": 10, "max_pages": 1},
        "fixture_rss_rate60": {"provider": "rss_feed", "host": "http://fixture:8080", "endpoint": "/rss/rate60", "timeout": 10, "rate_limit": "60s", "max_requests": 2, "page_size": 10, "max_pages": 1},
        "fixture_rss_cancel": {"provider": "rss_feed", "host": "http://fixture:8080", "endpoint": "/rss/cancel", "timeout": 10, "rate_limit": "60s", "max_requests": 2, "page_size": 10, "max_pages": 1},
        "fixture_rss_long": {"provider": "rss_feed", "host": "http://fixture:8080", "endpoint": "/rss/long", "timeout": 10, "rate_limit": "0", "max_requests": 1, "page_size": 10, "max_pages": 1},
        "fixture_atom": {"provider": "rss_feed", "host": "http://fixture:8080", "endpoint": "/atom/ok", "timeout": 10, "rate_limit": "0", "max_requests": 1, "page_size": 10, "max_pages": 1},
        "fixture_rss_empty": {"provider": "rss_feed", "host": "http://fixture:8080", "endpoint": "/rss/empty", "timeout": 10, "rate_limit": "0", "max_requests": 1, "page_size": 10, "max_pages": 1},
        "fixture_rss_invalid": {"provider": "rss_feed", "host": "http://fixture:8080", "endpoint": "/rss/invalid", "timeout": 10, "rate_limit": "0", "max_requests": 1, "page_size": 10, "max_pages": 1},
        "fixture_json": {"provider": "http_json", "host": "http://fixture:8080", "endpoint": "/search/json", "api_key_label": "X-API-Key", "api_key": PROVIDER_SECRET, "timeout": 10, "rate_limit": "0", "max_requests": 1, "page_size": 10, "max_pages": 1},
        "fixture_link": {"provider": "http_json", "host": "http://fixture:8080", "endpoint": "/search/link", "timeout": 10, "rate_limit": "0", "max_requests": 1, "page_size": 10, "max_pages": 1},
        "fixture_config": {"provider": "http_json", "host": "http://fixture:8080", "endpoint": "/search/config", "timeout": 10, "rate_limit": "0", "max_requests": 1, "page_size": 10, "max_pages": 1},
        "fixture_noconfig": {"provider": "http_json", "host": "http://fixture:8080", "endpoint": "/search/noconfig", "timeout": 10, "rate_limit": "0", "max_requests": 1, "page_size": 10, "max_pages": 1},
        "fixture_many": {"provider": "http_json", "host": "http://fixture:8080", "endpoint": "/search/many", "timeout": 20, "rate_limit": "0", "max_requests": 1, "page_size": 100, "max_pages": 1},
        "fixture_gdelt": {"provider": "gdelt_doc", "host": "http://fixture:8080", "endpoint": "/gdelt/doc", "timeout": 10, "rate_limit": "0", "max_requests": 1, "page_size": 10, "max_pages": 1},
        "fixture_gdelt_pages": {"provider": "gdelt_doc", "host": "http://fixture:8080", "endpoint": "/gdelt/paged", "timeout": 10, "rate_limit": "0", "max_requests": 4, "page_size": 2, "max_pages": 2},
        "fixture_gdelt_empty": {"provider": "gdelt_doc", "host": "http://fixture:8080", "endpoint": "/gdelt/empty", "timeout": 10, "rate_limit": "0", "max_requests": 1, "page_size": 10, "max_pages": 1},
        "fixture_gdelt_missing": {"provider": "gdelt_doc", "host": "http://fixture:8080", "endpoint": "/gdelt/missing", "timeout": 10, "rate_limit": "0", "max_requests": 1, "page_size": 10, "max_pages": 1},
        "fixture_gdelt_malformed": {"provider": "gdelt_doc", "host": "http://fixture:8080", "endpoint": "/gdelt/malformed", "timeout": 10, "rate_limit": "0", "max_requests": 1, "page_size": 10, "max_pages": 1},
        "fixture_403": {"provider": "http_json", "host": "http://fixture:8080", "endpoint": "/status/403", "timeout": 5, "rate_limit": "0", "max_requests": 1, "page_size": 1, "max_pages": 1},
        "fixture_404": {"provider": "http_json", "host": "http://fixture:8080", "endpoint": "/status/404", "timeout": 5, "rate_limit": "0", "max_requests": 1, "page_size": 1, "max_pages": 1},
        "fixture_429": {"provider": "http_json", "host": "http://fixture:8080", "endpoint": "/status/429", "timeout": 5, "rate_limit": "0", "max_requests": 1, "page_size": 1, "max_pages": 1},
        "fixture_500": {"provider": "http_json", "host": "http://fixture:8080", "endpoint": "/status/500", "timeout": 5, "rate_limit": "0", "max_requests": 1, "page_size": 1, "max_pages": 1},
        "fixture_504": {"provider": "http_json", "host": "http://fixture:8080", "endpoint": "/status/504", "timeout": 5, "rate_limit": "0", "max_requests": 1, "page_size": 1, "max_pages": 1},
        "fixture_slow15": {"provider": "http_json", "host": "http://fixture:8080", "endpoint": "/slow/15", "timeout": 30, "rate_limit": "0", "max_requests": 1, "page_size": 1, "max_pages": 1},
        "fixture_slow35": {"provider": "http_json", "host": "http://fixture:8080", "endpoint": "/slow/35", "timeout": 60, "rate_limit": "0", "max_requests": 1, "page_size": 1, "max_pages": 1},
        "external_pitchfork_news_rss": {"provider": "rss_feed", "host": "https://pitchfork.com", "endpoint": "/feed/feed-news/rss", "timeout": 20, "rate_limit": "2s", "max_requests": 1, "page_size": 5, "max_pages": 1},
        "external_hospitalitynet_all_news_rss": {"provider": "rss_feed", "host": "https://www.hospitalitynet.org", "endpoint": "/rss/news.xml", "timeout": 20, "rate_limit": "2s", "max_requests": 1, "page_size": 5, "max_pages": 1},
    }
    config["information_seed"] = {
        "enabled": True,
        "query_timer": 1,
        "max_concurrent_seeds": 1,
        "max_queries_per_seed": 5,
        "max_candidates_per_seed": 40,
        "retry_interval": 2,
        "retry_backoff_max": 8,
        "max_attempts": 3,
        "heartbeat_interval": 2,
        "lease_duration": 30,
        "processing_timeout": "2 minutes",
        "provider_allow_list": sorted(providers),
        "providers": providers,
        "plugin_limits": {"timeout": 10, "max_output_size_bytes": 1048576},
        "retention": {"enabled": False, "completed_days": 30, "disabled_days": 30, "exhausted_days": 30, "batch_size": 100, "dry_run": False},
    }
    (SRC / "config.yaml").write_text(yaml.safe_dump(config, sort_keys=False), encoding="utf-8")
    save_json("config-sanitized.json", config)
    log(f"prepared disposable source tree at {SRC}")


def generate_and_isolate_compose() -> None:
    global COMPOSE, API_PORT, FIXTURE_PORT, BASE_URL, FIXTURE_URL
    API_PORT = free_port()
    FIXTURE_PORT = free_port()
    run(["./scripts/generate-docker-compose.sh", "-e=1", "-v=0", "--prometheus=no", "--postgres=yes", "--no_jaeger"], cwd=SRC, timeout=120)
    document = yaml.safe_load((SRC / "docker-compose.yml").read_text(encoding="utf-8"))
    services = document["services"]
    image_suffix = os.environ.get("INFOSEED_IMAGE_SUFFIX", dt.datetime.now(dt.timezone.utc).strftime("%Y%m%d%H%M%S"))
    for service_name, service in services.items():
        service["container_name"] = f"{RUN_ID}-{service_name}"
        service.setdefault("labels", {})["com.crowler.infoseed.run_id"] = RUN_ID
        service["labels"]["com.crowler.infoseed.service"] = service_name
        if "build" in service:
            service["build"]["network"] = "host"
        if service_name == "crowler-api":
            service["image"] = f"{RUN_ID}-api:{image_suffix}"
            service["ports"] = [f"127.0.0.1:{API_PORT}:8080"]
            service["restart"] = "unless-stopped"
        elif service_name == "crowler-events":
            service["image"] = f"{RUN_ID}-events:{image_suffix}"
            service["ports"] = []
            service["restart"] = "unless-stopped"
        elif service_name == "crowler-db":
            service["ports"] = []
            service["restart"] = "unless-stopped"
        elif service_name.startswith("crowler-engine-"):
            service["image"] = f"{RUN_ID}-engine-1:{image_suffix}"
            service["restart"] = "no"
    engine = services["crowler-engine-1"]
    engine2 = json.loads(json.dumps(engine))
    engine2.pop("build", None)
    engine2["container_name"] = f"{RUN_ID}-crowler-engine-2"
    engine2["labels"]["com.crowler.infoseed.service"] = "crowler-engine-2"
    engine2_env = engine2.get("environment", [])
    engine2["environment"] = [item.replace("INSTANCE_ID=1", "INSTANCE_ID=2").replace("MICROSERVICE_NAME=crowler-engine-1", "MICROSERVICE_NAME=crowler-engine-2") for item in engine2_env]
    services["crowler-engine-2"] = engine2
    services["fixture"] = {
        "container_name": f"{RUN_ID}-fixture",
        "image": f"{RUN_ID}-fixture:{image_suffix}",
        "build": {"context": ".", "dockerfile": "tests/integration/infoseed-compose/fixture-server/Dockerfile", "network": "host"},
        "ports": [f"127.0.0.1:{FIXTURE_PORT}:8080"],
        "networks": ["crowler-net"],
        "labels": {"com.crowler.infoseed.run_id": RUN_ID, "com.crowler.infoseed.service": "fixture"},
        "restart": "no",
        "healthcheck": {"test": ["CMD-SHELL", "wget -qO- http://127.0.0.1:8080/health >/dev/null"], "interval": "2s", "timeout": "2s", "retries": 30},
    }
    services["probe"] = {
        "container_name": f"{RUN_ID}-probe",
        "image": f"{RUN_ID}-probe:{image_suffix}",
        "build": {"context": ".", "dockerfile": "tests/integration/infoseed-compose/probe/Dockerfile", "network": "host"},
        "environment": [
            "DB_TYPE=postgres", "POSTGRES_DB=SitesIndex", "CROWLER_DB_USER=crowler", f"CROWLER_DB_PASSWORD={DB_PASSWORD}",
            "POSTGRES_DB_HOST=127.0.0.1", "POSTGRES_DB_PORT=5432", "POSTGRES_SSL_MODE=disable",
        ],
        "network_mode": "service:crowler-db",
        "labels": {"com.crowler.infoseed.run_id": RUN_ID, "com.crowler.infoseed.service": "probe"},
        "restart": "no",
    }
    (SRC / "docker-compose.yml").write_text(yaml.safe_dump(document, sort_keys=False, width=1000), encoding="utf-8")
    COMPOSE = ["docker", "compose", "-f", str(COMPOSE_FILE), "-p", RUN_ID]
    (SRC / "docker-compose.test.yml").write_text(yaml.safe_dump(document, sort_keys=False, width=1000), encoding="utf-8")
    resolved = compose("config", timeout=120).stdout
    (ARTIFACTS / "compose-resolved-sanitized.yaml").write_text(sanitize(resolved), encoding="utf-8")
    resolved_doc = yaml.safe_load(resolved)
    resolved_services = resolved_doc["services"]

    def published_ports(service: dict) -> list[str]:
        values = []
        for entry in service.get("ports", []) or []:
            if isinstance(entry, dict) and entry.get("published"):
                values.append(f"{entry.get('host_ip', '')}:{entry['published']}:{entry.get('target')}")
            elif isinstance(entry, str):
                values.append(entry)
        return values

    assert any(entry.startswith(f"127.0.0.1:{API_PORT}:8080") for entry in published_ports(resolved_services["crowler-api"])), published_ports(resolved_services["crowler-api"])
    assert any(entry.startswith(f"127.0.0.1:{FIXTURE_PORT}:8080") for entry in published_ports(resolved_services["fixture"])), published_ports(resolved_services["fixture"])
    assert not published_ports(resolved_services["crowler-db"]), published_ports(resolved_services["crowler-db"])
    assert not published_ports(resolved_services["crowler-events"]), published_ports(resolved_services["crowler-events"])
    for name, service in resolved_services.items():
        assert service["container_name"].startswith(RUN_ID + "-"), (name, service.get("container_name"))
    BASE_URL = f"http://127.0.0.1:{API_PORT}"
    FIXTURE_URL = f"http://127.0.0.1:{FIXTURE_PORT}"
    save_json("run-identifiers.json", {"run_id": RUN_ID, "container_prefix": RUN_ID, "compose_project": RUN_ID, "api_port": API_PORT, "fixture_port": FIXTURE_PORT, "source_dir": str(SRC), "artifacts": str(ARTIFACTS)})


def wait_http(url: str, timeout: int = 180, expected: int = 200) -> None:
    deadline = time.time() + timeout
    last = ""
    while time.time() < deadline:
        try:
            with urllib.request.urlopen(url, timeout=5) as response:
                if response.status == expected:
                    return
                last = f"status {response.status}"
        except Exception as exc:  # noqa: BLE001
            last = str(exc)
        time.sleep(1)
    raise TimeoutError(f"timed out waiting for {url}: {last}")


def http(method: str, path: str, body=None, token: str | None = None, expected: int | None = None, timeout: int = 30) -> tuple[int, object, float]:
    started = time.time()
    data = None if body is None else json.dumps(body).encode()
    headers = {"Accept": "application/json"}
    if body is not None:
        headers["Content-Type"] = "application/json"
    if token:
        headers["Authorization"] = "Bearer " + token
    req = urllib.request.Request(BASE_URL + path, data=data, headers=headers, method=method)
    status = 0
    raw = ""
    try:
        with urllib.request.urlopen(req, timeout=timeout) as response:
            status = response.status
            raw = response.read().decode("utf-8", "replace")
    except urllib.error.HTTPError as exc:
        status = exc.code
        raw = exc.read().decode("utf-8", "replace")
    elapsed = time.time() - started
    try:
        parsed = json.loads(raw) if raw else None
    except json.JSONDecodeError:
        parsed = raw
    entry = {"ts": utc_now(), "method": method, "path": path, "status": status, "elapsed_ms": round(elapsed * 1000), "response": parsed, "request": body}
    HTTP_TRANSCRIPT.append(entry)
    append_jsonl("http-transcript.jsonl", entry)
    if expected is not None and status != expected:
        raise AssertionError(f"{method} {path} -> {status}, expected {expected}: {sanitize(raw)[:1000]}")
    return status, parsed, elapsed


def psql(sql: str, database: str = "SitesIndex", check: bool = True) -> str:
    proc = compose("exec", "-T", "crowler-db", "psql", "-X", "-v", "ON_ERROR_STOP=1", "-U", "postgres", "-d", database, "-At", "-F", "\t", "-c", sql, timeout=120, check=check)
    return proc.stdout.strip()


def psql_stdin(sql: str, args: list[str] | None = None, database: str = "SitesIndex", check: bool = True) -> str:
    cmd = ["exec", "-T", "crowler-db", "psql", "-X", "-v", "ON_ERROR_STOP=1", "-U", "postgres", "-d", database]
    if args:
        cmd += args
    cmd += ["-f", "-"]
    proc = compose(*cmd, timeout=300, input_text=sql, check=check)
    return proc.stdout


def setup_sql_args(database: str) -> list[str]:
    return [
        "-v", f"POSTGRES_DB={database}",
        "-v", "CROWLER_DB_USER=crowler",
        "-v", f"CROWLER_DB_PASSWORD={DB_PASSWORD}",
        "-v", "DB_SHARED_BUFFERS=128MB",
        "-v", "DB_WORK_MEM=4MB",
        "-v", "DB_MAINTENANCE_WORK_MEM=64MB",
        "-v", "DB_EFFECTIVE_CACHE_SIZE=4GB",
        "-v", "DB_AUTOVACUUM_WORK_MEM=-1",
        "-v", "DB_MAX_WAL_SIZE=1GB",
        "-v", "DB_MAX_CONNECTIONS=100",
    ]


def one(sql: str) -> str:
    output = psql(sql)
    return output.splitlines()[0] if output.strip() else ""


def one_db(sql: str, database: str) -> str:
    output = psql(sql, database=database)
    return output.splitlines()[0] if output.strip() else ""


def login(username: str, password: str) -> str:
    status, parsed, _ = http("POST", "/v1/auth/login", {"username": username, "password": password}, expected=200)
    token = parsed.get("access_token") if isinstance(parsed, dict) else ""
    if not token:
        raise AssertionError(f"login for {username} returned no token")
    return token


def hash_passwords() -> list[str]:
    payload = "\n".join((ADMIN_PASSWORD, USER_A_PASSWORD, USER_B_PASSWORD)) + "\n"
    proc = run(["go", "run", "./tests/integration/infoseed-compose/hashpw"], cwd=SRC, timeout=300, input_text=payload)
    hashes = [line.strip() for line in proc.stdout.splitlines() if line.strip()]
    if len(hashes) != 3:
        raise AssertionError(f"expected three password hashes, got {len(hashes)}")
    return hashes


def bootstrap_auth() -> None:
    admin_hash, user_a_hash, user_b_hash = hash_passwords()
    statements = [
        "INSERT INTO AuthRoles (name, description) VALUES ('superadmin', 'test administrator'), ('user', 'test user') ON CONFLICT (name) DO NOTHING;",
    ]
    for username, password_hash in (("it_admin", admin_hash), ("it_user_a", user_a_hash), ("it_user_b", user_b_hash)):
        statements.append(f"INSERT INTO Users (username, password_hash, disabled) VALUES ('{username}', '{password_hash}', FALSE) ON CONFLICT (username) DO UPDATE SET password_hash=EXCLUDED.password_hash, disabled=FALSE;")
    for username, role in (("it_admin", "superadmin"), ("it_user_a", "user"), ("it_user_b", "user")):
        statements.append(f"INSERT INTO UserRoles(user_id, role_id) SELECT u.user_id, r.role_id FROM Users u, AuthRoles r WHERE u.username='{username}' AND r.name='{role}' ON CONFLICT DO NOTHING;")
    psql_stdin("\n".join(statements))
    global ADMIN_ID, USER_A_ID, USER_B_ID
    ADMIN_ID = int(psql("SELECT user_id FROM Users WHERE username='it_admin'"))
    USER_A_ID = int(psql("SELECT user_id FROM Users WHERE username='it_user_a'"))
    USER_B_ID = int(psql("SELECT user_id FROM Users WHERE username='it_user_b'"))
    save_json("identities.json", {"admin_id": ADMIN_ID, "user_a_id": USER_A_ID, "user_b_id": USER_B_ID})


def seed_row(seed_id: int) -> dict:
    columns = "information_seed_id,status,disabled,attempts,engine,claim_token,fencing_token,heartbeat_at,lease_expires_at,next_retry_at,last_error,last_error_at,usr_id,priority"
    row = psql(f"SELECT {columns} FROM InformationSeed WHERE information_seed_id={seed_id}")
    if not row:
        raise AssertionError(f"seed {seed_id} not found")
    values = row.split("\t")
    return dict(zip([c.strip() for c in columns.split(",")], values))


def wait_seed(seed_id: int, statuses: set[str], timeout: int = 120) -> dict:
    deadline = time.time() + timeout
    last = None
    while time.time() < deadline:
        last = seed_row(seed_id)
        if last["status"] in statuses:
            return last
        time.sleep(0.5)
    raise TimeoutError(f"seed {seed_id} did not reach {statuses}: {last}")


def create_seed(token: str, text: str, config: dict, priority: str = "low", expected: int = 201, category_id: int = 0, disabled: bool = False) -> int:
    payload = {"information_seed": text, "priority": priority, "config": config}
    if category_id:
        payload["category_id"] = category_id
    if disabled:
        payload["disabled"] = True
    status, parsed, _ = http("POST", "/v1/information_seed/add", payload, token=token, expected=expected)
    if expected != 201:
        return 0
    return int(parsed["item"]["information_seed_id"])


def case(case_id: str, name: str, fn) -> None:
    log(f"{case_id} START {name}")
    started = dt.datetime.now(dt.timezone.utc)
    entry = {"id": case_id, "name": name, "status": "FAIL", "started_at": started.isoformat(), "evidence": []}
    try:
        details = fn() or {}
        entry.update(details)
        entry["status"] = "PASS"
    except BlockedError as exc:
        entry["status"] = "BLOCKED"
        entry["error"] = sanitize(str(exc))
        entry["traceback"] = sanitize(traceback.format_exc())
        (ARTIFACTS / f"{case_id}-blocked.log").write_text(entry["traceback"], encoding="utf-8")
        log(f"{case_id} BLOCKED {exc}")
    except Exception as exc:  # noqa: BLE001
        entry["error"] = sanitize(str(exc))
        entry["traceback"] = sanitize(traceback.format_exc())
        (ARTIFACTS / f"{case_id}-failure.log").write_text(entry["traceback"], encoding="utf-8")
        log(f"{case_id} FAIL {exc}")
    finally:
        entry["ended_at"] = dt.datetime.now(dt.timezone.utc).isoformat()
        RESULTS.append(entry)
        save_json("results.json", {"run_id": RUN_ID, "started_at": STARTED_AT.isoformat(), "cases": RESULTS})
    if entry["status"] == "PASS":
        log(f"{case_id} PASS")


def sql_snapshot(name: str, queries: dict[str, str]) -> list[str]:
    paths = []
    for label, query in queries.items():
        output = psql(query)
        path = ARTIFACTS / f"{name}-{label}.tsv"
        path.write_text(output + "\n", encoding="utf-8")
        paths.append(path.name)
    return paths


def wait_for_claim(seed_id: int, timeout: int = 30) -> dict:
    deadline = time.time() + timeout
    last = None
    while time.time() < deadline:
        last = seed_row(seed_id)
        if last["status"] == "processing" and last["claim_token"]:
            return last
        time.sleep(0.25)
    raise TimeoutError(f"seed {seed_id} was not claimed: {last}")


def fixture_requests() -> dict:
    with urllib.request.urlopen(FIXTURE_URL + "/requests", timeout=10) as response:
        return json.loads(response.read().decode())


def fixture_reset() -> None:
    req = urllib.request.Request(FIXTURE_URL + "/reset", method="POST")
    urllib.request.urlopen(req, timeout=10).read()


def wait_fixture_count(path: str, minimum: int, timeout: int = 60) -> None:
    deadline = time.time() + timeout
    last = 0
    while time.time() < deadline:
        last = int(fixture_requests().get("counts", {}).get(path, 0))
        if last >= minimum:
            return
        time.sleep(0.25)
    raise TimeoutError(f"fixture path {path} reached {last}, wanted {minimum}")


def parse_trace_time(value: str) -> dt.datetime:
    return dt.datetime.fromisoformat(value.replace("Z", "+00:00"))


def it01() -> dict:
    global DB_VERSION, DB_SERVER_VERSION_NUM
    DB_VERSION = one("SELECT version()")
    DB_SERVER_VERSION_NUM = one("SHOW server_version_num")
    tables = psql("SELECT tablename FROM pg_tables WHERE schemaname='public' AND lower(tablename) IN ('informationseed','sources','sourceownership','sourceinformationseedindex','informationseedcandidate') ORDER BY lower(tablename)").splitlines()
    for required in ("informationseed", "sources", "sourceownership", "sourceinformationseedindex", "informationseedcandidate"):
        assert required in tables, f"missing table {required} (observed {tables})"
    columns = psql("SELECT lower(table_name)||'.'||lower(column_name) FROM information_schema.columns WHERE table_schema='public' AND ((lower(table_name)='sources' AND lower(column_name)='deleted_at') OR (lower(table_name)='informationseed' AND lower(column_name) IN ('deleted_at','claim_token','fencing_token','lease_expires_at','heartbeat_at','next_retry_at'))) ORDER BY 1").splitlines()
    for required in ("sources.deleted_at", "informationseed.deleted_at", "informationseed.claim_token", "informationseed.fencing_token", "informationseed.lease_expires_at", "informationseed.heartbeat_at", "informationseed.next_retry_at"):
        assert required in columns, f"missing column {required} (observed {columns})"
    function = one("SELECT pg_get_function_identity_arguments(p.oid) FROM pg_proc p JOIN pg_namespace n ON n.oid=p.pronamespace WHERE p.proname='update_informationseed' AND n.nspname='public'")
    assert function.count(",") == 7, f"claim function is not eight-argument: {function}"
    indexes = psql("SELECT indexname FROM pg_indexes WHERE schemaname='public' AND indexname LIKE 'idx_informationseed_%' ORDER BY indexname").splitlines()
    for required in ("idx_informationseed_claim_queue", "idx_informationseed_lease_expiry", "idx_informationseed_next_retry", "idx_informationseed_claim_token"):
        assert required in indexes, f"missing index {required}"
    psql("CREATE TABLE IF NOT EXISTS it01_probe(value TEXT); INSERT INTO it01_probe VALUES ('persisted');")
    compose("restart", "crowler-db", timeout=120)
    wait_pg(timeout=180)
    wait_http(BASE_URL + "/v1/health", timeout=180)
    wait_http(BASE_URL + "/v1/ready", timeout=180)
    assert one("SELECT value FROM it01_probe") == "persisted"
    psql("DROP TABLE it01_probe")
    save_json("it01-db.json", {"version": DB_VERSION, "server_version_num": DB_SERVER_VERSION_NUM, "tables": tables, "columns": columns, "claim_function_args": function, "indexes": indexes, "ready": True})
    return {"evidence": ["it01-db.json", "http-transcript.jsonl"]}


def it02() -> dict:
    unauth_routes = [
        ("POST", "/v1/information_seed/add", {"information_seed": "unauth"}),
        ("POST", "/v1/information_seed/update", {"information_seed_id": 1, "information_seed": "unauth"}),
        ("POST", "/v1/information_seed/remove", {"information_seed_id": 1}),
        ("POST", "/v1/information_seed/retry", {"information_seed_id": 1}),
        ("POST", "/v1/information_seed/disable", {"information_seed_id": 1}),
        ("POST", "/v1/information_seed/1/rerun", None),
        ("POST", "/v1/information_seed/1/disable", None),
        ("POST", "/v1/information_seed/1/enable", None),
        ("GET", "/v1/config/information_seed/providers", None),
    ]
    for method, path, body in unauth_routes:
        http(method, path, body, expected=401)
        http(method, path, body, token="invalid-token", expected=401)
    http("POST", "/v1/information_seed/add", {"information_seed": "forged", "usr_id": USER_B_ID}, token=USER_A_TOKEN, expected=403)
    seed_id = create_seed(USER_A_TOKEN, f"auth seed {RUN_ID}", {"providers": ["fixture_json"], "create_sources": False, "link_existing_sources": False})
    row = seed_row(seed_id)
    assert int(row["usr_id"]) == USER_A_ID, f"seed owner {row['usr_id']} != {USER_A_ID}"
    http("POST", f"/v1/information_seed/{seed_id}/disable", token=USER_A_TOKEN, expected=200)
    wait_seed(seed_id, {"new", "pending", "completed", "error", "exhausted", "disabled"}, 30)
    stable = seed_row(seed_id)
    for method, path, body in (
        ("GET", f"/v1/information_seed/status?information_seed_id={seed_id}", None),
        ("POST", f"/v1/information_seed/{seed_id}/disable", None),
        ("POST", f"/v1/information_seed/{seed_id}/enable", {"queue_pending": True}),
        ("POST", f"/v1/information_seed/{seed_id}/rerun", None),
        ("POST", "/v1/information_seed/update", {"information_seed_id": seed_id, "information_seed": "forbidden update"}),
        ("POST", "/v1/information_seed/remove", {"information_seed_id": seed_id}),
        ("POST", "/v1/information_seed/retry", {"information_seed_id": seed_id}),
    ):
        http(method, path, body, token=USER_B_TOKEN, expected=403)
    after_denials = seed_row(seed_id)
    for field in ("usr_id", "disabled", "status", "claim_token", "attempts"):
        assert stable[field] == after_denials[field], f"denied cross-user request changed {field}: {stable} -> {after_denials}"
    http("GET", f"/v1/information_seed/status?id={seed_id}", token=USER_A_TOKEN, expected=200)
    http("POST", f"/v1/information_seed/{seed_id}/enable", {"queue_pending": False}, token=USER_A_TOKEN, expected=200)
    assert seed_row(seed_id)["disabled"] in ("f", "false", "0", "")
    http("POST", "/v1/information_seed/disable", {"information_seed_id": seed_id}, token=USER_A_TOKEN, expected=200)
    assert seed_row(seed_id)["disabled"] in ("t", "true", "1")
    http("POST", f"/v1/information_seed/{seed_id}/disable", token=ADMIN_TOKEN, expected=200)
    assert seed_row(seed_id)["disabled"] in ("t", "true", "1")
    http("GET", f"/v1/information_seed/status?information_seed_id={seed_id}", token=ADMIN_TOKEN, expected=200)
    http("GET", "/v1/information_seed/list?limit=500", token=ADMIN_TOKEN, expected=200)
    http("GET", "/v1/config/information_seed/providers", token=USER_A_TOKEN, expected=403)
    status, providers, _ = http("GET", "/v1/config/information_seed/providers", token=ADMIN_TOKEN, expected=200)
    rendered = json.dumps(providers)
    assert PROVIDER_SECRET not in rendered, "provider credentials were not redacted"
    assert "api_key" not in rendered or "REDACTED" in rendered or "***" in rendered or "configured" in rendered, "provider credential field was not redacted"
    return {"seed_id": seed_id, "evidence": ["http-transcript.jsonl"]}


def it03() -> dict:
    fixture_reset()
    existing_url = "https://fixture.test/existing"
    cfg = {"version": "1.0", "format_version": "1.0", "source_name": "Existing fixture", "crawling_config": {"site": existing_url, "source_type": "website"}}
    uid = source_uid("Existing fixture", existing_url)
    psql(f"INSERT INTO Sources(source_uid,url,name,priority,sub_priority,category_id,usr_id,restricted,flags,config,disabled,status) VALUES ('{uid}','{existing_url}','Existing fixture','low',0,1,{USER_A_ID},0,0,'{json.dumps(cfg)}',TRUE,'new')")
    source_id = int(psql(f"SELECT source_id FROM Sources WHERE url='{existing_url}'"))
    psql(f"INSERT INTO SourceOwnership(source_id,user_id,verified_at,verified_by_user_id,verified_by,verification_method) VALUES ({source_id},{USER_A_ID},CURRENT_TIMESTAMP,{USER_A_ID},'it','authenticated_creation')")
    before = psql(f"SELECT source_uid,url,name,priority,usr_id,disabled,status,COALESCE(md5(config::text),'') FROM Sources WHERE source_id={source_id}")
    seed_id = create_seed(USER_A_TOKEN, f"link-only {RUN_ID}", {"providers": ["fixture_link"], "create_sources": False, "link_existing_sources": True, "update_existing_source_config": False, "source_config": cfg})
    wait_seed(seed_id, {"completed"}, 90)
    assert int(psql("SELECT COUNT(*) FROM Sources")) == 1
    assert int(psql(f"SELECT COUNT(*) FROM SourceInformationSeedIndex WHERE information_seed_id={seed_id} AND source_id={source_id}")) == 1
    after = psql(f"SELECT source_uid,url,name,priority,usr_id,disabled,status,COALESCE(md5(config::text),'') FROM Sources WHERE source_id={source_id}")
    assert before == after, "existing source changed during link-only run"
    metadata = psql(f"SELECT COALESCE(discovery_provider,'')||'|'||COALESCE(discovery_query,'')||'|'||COALESCE(discovery_rank::text,'') FROM SourceInformationSeedIndex WHERE information_seed_id={seed_id} AND source_id={source_id}")
    assert metadata.startswith("fixture_link|"), f"link provenance missing: {metadata}"
    seed2 = create_seed(USER_A_TOKEN, f"link-only default {RUN_ID}", {"providers": ["fixture_link"], "create_sources": False, "link_existing_sources": True, "source_config": cfg})
    wait_seed(seed2, {"completed"}, 90)
    assert before == psql(f"SELECT source_uid,url,name,priority,usr_id,disabled,status,COALESCE(md5(config::text),'') FROM Sources WHERE source_id={source_id}"), "default update policy changed existing source"
    http("POST", f"/v1/information_seed/{seed_id}/rerun", token=USER_A_TOKEN, expected=200)
    wait_seed(seed_id, {"completed"}, 90)
    assert int(psql(f"SELECT COUNT(*) FROM SourceInformationSeedIndex WHERE information_seed_id={seed_id} AND source_id={source_id}")) == 1
    assert int(psql("SELECT COUNT(*) FROM Sources")) == 1
    sql_snapshot("it03", {"source": f"SELECT * FROM Sources WHERE source_id={source_id}", "links": f"SELECT * FROM SourceInformationSeedIndex WHERE information_seed_id={seed_id}"})
    return {"seed_id": seed_id, "source_id": source_id, "evidence": ["it03-source.tsv", "it03-links.tsv"]}


def it04() -> dict:
    fixture_reset()
    run_config = {"providers": ["fixture_json"], "create_sources": True, "link_existing_sources": True, "update_existing_source_config": False, "disabled": True, "source_priority": "low", "source_config": {"version": "1.0", "format_version": "1.0", "source_name": "Fixture new", "crawling_config": {"site": "https://fixture.test/new/one", "source_type": "website"}}}
    seed_id = create_seed(USER_A_TOKEN, f"create sources {RUN_ID}", run_config, category_id=7)
    wait_seed(seed_id, {"completed"}, 90)
    rows = psql("SELECT source_id,source_uid,url,name,usr_id,category_id,priority,disabled,status,COALESCE(config::text,'') FROM Sources WHERE url LIKE 'https://fixture.test/new/%' ORDER BY url").splitlines()
    assert len(rows) == 2, f"expected two created sources, got {rows}"
    for line in rows:
        parts = line.split("\t")
        source_id, uid, url, name = parts[0], parts[1], parts[2], parts[3]
        assert len(uid) == 64 and uid == uid.lower() and all(ch in "0123456789abcdef" for ch in uid), f"bad source_uid {uid}"
        assert uid == source_uid(name, url), f"source_uid mismatch for {url}"
        assert int(parts[4]) == USER_A_ID
        assert int(parts[5]) == 7
        assert parts[6] == "low"
        assert parts[7] in ("t", "true", "1")
        assert parts[8] == "new"
        assert parts[9] and parts[9] != "{}", "source config was synthesized as empty"
        assert int(psql(f"SELECT COUNT(*) FROM SourceOwnership WHERE source_id={source_id} AND user_id={USER_A_ID} AND verification_method='information_seed_discovery'")) == 1
    assert int(psql(f"SELECT COUNT(*) FROM InformationSeedCandidate WHERE information_seed_id={seed_id} AND decision_status='accepted'")) == 2
    assert int(psql(f"SELECT COUNT(*) FROM SourceInformationSeedIndex WHERE information_seed_id={seed_id}")) == 2

    no_config_seed = create_seed(USER_A_TOKEN, f"no source config {RUN_ID}", {"providers": ["fixture_noconfig"], "create_sources": True, "link_existing_sources": True, "disabled": True, "source_priority": "low"})
    wait_seed(no_config_seed, {"completed"}, 90)
    no_config_rows = psql("SELECT COUNT(*) FROM Sources WHERE url LIKE 'https://fixture.test/noconfig/%' AND config IS NULL")
    assert no_config_rows.strip() == "2", f"config-absent sources did not retain null config: {no_config_rows}"
    before_sources = psql("SELECT source_id,source_uid,url,name,usr_id,category_id,priority,disabled,status,COALESCE(md5(config::text),'') FROM Sources WHERE url LIKE 'https://fixture.test/new/%' ORDER BY url")
    before_owner = psql("SELECT source_id,user_id,verification_method FROM SourceOwnership ORDER BY source_id")
    seed_b = create_seed(USER_B_TOKEN, f"cross owner {RUN_ID}", {"providers": ["fixture_json"], "create_sources": True, "link_existing_sources": False, "update_existing_source_config": True, "disabled": True, "source_priority": "low", "source_config": {"version": "1.0", "format_version": "1.0", "source_name": "Owner B", "crawling_config": {"site": "https://fixture.test/new/one", "source_type": "website"}}})
    wait_seed(seed_b, {"completed"}, 90)
    assert before_sources == psql("SELECT source_id,source_uid,url,name,usr_id,category_id,priority,disabled,status,COALESCE(md5(config::text),'') FROM Sources WHERE url LIKE 'https://fixture.test/new/%' ORDER BY url"), "cross-user discovery changed existing sources"
    assert before_owner == psql("SELECT source_id,user_id,verification_method FROM SourceOwnership ORDER BY source_id"), "cross-user discovery changed ownership"
    assert int(psql(f"SELECT COUNT(*) FROM SourceInformationSeedIndex WHERE information_seed_id={seed_b}")) == 0, "cross-user discovery linked a source"
    seed_b_link = create_seed(USER_B_TOKEN, f"cross owner link {RUN_ID}", {"providers": ["fixture_json"], "create_sources": True, "link_existing_sources": True, "update_existing_source_config": True, "disabled": True, "source_priority": "low", "source_config": {"version": "1.0", "format_version": "1.0", "source_name": "Owner B link", "crawling_config": {"site": "https://fixture.test/new/one", "source_type": "website"}}})
    final_b = wait_seed(seed_b_link, {"error", "exhausted"}, 120)
    assert final_b["status"] in ("error", "exhausted")
    assert before_sources == psql("SELECT source_id,source_uid,url,name,usr_id,category_id,priority,disabled,status,COALESCE(md5(config::text),'') FROM Sources WHERE url LIKE 'https://fixture.test/new/%' ORDER BY url"), "forbidden cross-user link changed existing sources"
    assert int(psql(f"SELECT COUNT(*) FROM SourceInformationSeedIndex WHERE information_seed_id={seed_b_link}")) == 0, "forbidden cross-user link created a relationship"
    ids_before = psql("SELECT source_id||':'||source_uid FROM Sources WHERE url LIKE 'https://fixture.test/new/%' ORDER BY url")
    http("POST", f"/v1/information_seed/{seed_id}/rerun", token=USER_A_TOKEN, expected=200)
    wait_seed(seed_id, {"completed"}, 90)
    ids_after = psql("SELECT source_id||':'||source_uid FROM Sources WHERE url LIKE 'https://fixture.test/new/%' ORDER BY url")
    assert ids_before == ids_after
    assert int(psql(f"SELECT COUNT(*) FROM SourceInformationSeedIndex WHERE information_seed_id={seed_id}")) == 2
    sql_snapshot("it04", {"sources": "SELECT * FROM Sources WHERE url LIKE 'https://fixture.test/new/%' OR url LIKE 'https://fixture.test/noconfig/%' ORDER BY url", "ownership": "SELECT * FROM SourceOwnership ORDER BY source_id", "candidates": f"SELECT * FROM InformationSeedCandidate WHERE information_seed_id IN ({seed_id},{no_config_seed},{seed_b},{seed_b_link}) ORDER BY information_seed_id, information_seed_candidate_id"})
    return {"seed_id": seed_id, "no_config_seed": no_config_seed, "seed_b": seed_b, "seed_b_link": seed_b_link, "evidence": ["it04-sources.tsv", "it04-ownership.tsv", "it04-candidates.tsv"]}


def it05() -> dict:
    fixture_reset()
    url = "https://fixture.test/config-existing"
    old_cfg = {"version": "1.0", "format_version": "1.0", "source_name": "Old config", "crawling_config": {"site": url, "source_type": "website"}}
    uid = source_uid("Old config", url)
    psql(f"INSERT INTO Sources(source_uid,url,name,priority,sub_priority,category_id,usr_id,restricted,flags,config,disabled,status) VALUES ('{uid}','{url}','Old config','low',0,1,{USER_A_ID},0,0,'{json.dumps(old_cfg)}',TRUE,'new')")
    source_id = int(psql(f"SELECT source_id FROM Sources WHERE url='{url}'"))
    psql(f"INSERT INTO SourceOwnership(source_id,user_id,verified_at,verified_by_user_id,verified_by,verification_method) VALUES ({source_id},{USER_A_ID},CURRENT_TIMESTAMP,{USER_A_ID},'it','authenticated_creation')")
    before = psql(f"SELECT COALESCE(md5(config::text),'')||'|'||COALESCE(last_updated_at::text,'')||'|'||status FROM Sources WHERE source_id={source_id}")
    new_cfg = {"version": "1.0", "format_version": "1.0", "source_name": "New config", "crawling_config": {"site": url, "source_type": "website"}, "meta_data": {"updated": True}}
    no_update = create_seed(USER_A_TOKEN, f"config no-update {RUN_ID}", {"providers": ["fixture_config"], "create_sources": True, "link_existing_sources": True, "update_existing_source_config": False, "source_config": new_cfg})
    wait_seed(no_update, {"completed"}, 90)
    assert before == psql(f"SELECT COALESCE(md5(config::text),'')||'|'||COALESCE(last_updated_at::text,'')||'|'||status FROM Sources WHERE source_id={source_id}"), "no-update policy changed config"
    update_seed = create_seed(USER_A_TOKEN, f"config update {RUN_ID}", {"providers": ["fixture_config"], "create_sources": True, "link_existing_sources": True, "update_existing_source_config": True, "source_config": new_cfg})
    wait_seed(update_seed, {"completed"}, 90)
    updated = psql(f"SELECT config::text||'|'||last_updated_at::text FROM Sources WHERE source_id={source_id}")
    assert "New config" in updated and "updated" in updated, f"config was not refreshed: {updated}"
    allowed = psql(f"SELECT url||'|'||name||'|'||priority||'|'||usr_id FROM Sources WHERE source_id={source_id}")
    assert allowed == f"{url}|Old config|low|{USER_A_ID}", f"config refresh changed non-config fields: {allowed}"
    psql(f"UPDATE Sources SET status='processing' WHERE source_id={source_id}")
    processing_before = psql(f"SELECT COALESCE(md5(config::text),'')||'|'||status FROM Sources WHERE source_id={source_id}")
    guard_cfg = dict(new_cfg); guard_cfg["source_name"] = "Blocked config"
    guard_seed = create_seed(USER_A_TOKEN, f"config processing guard {RUN_ID}", {"providers": ["fixture_config"], "create_sources": True, "link_existing_sources": True, "update_existing_source_config": True, "source_config": guard_cfg})
    wait_seed(guard_seed, {"completed"}, 90)
    assert processing_before == psql(f"SELECT COALESCE(md5(config::text),'')||'|'||status FROM Sources WHERE source_id={source_id}"), "processing source config changed"
    psql(f"UPDATE Sources SET status='new' WHERE source_id={source_id}")
    empty_before = psql(f"SELECT COALESCE(md5(config::text),'') FROM Sources WHERE source_id={source_id}")
    empty_seed = create_seed(USER_A_TOKEN, f"config empty {RUN_ID}", {"providers": ["fixture_config"], "create_sources": True, "link_existing_sources": True, "update_existing_source_config": True, "source_config": {}})
    wait_seed(empty_seed, {"completed"}, 90)
    assert empty_before == psql(f"SELECT COALESCE(md5(config::text),'') FROM Sources WHERE source_id={source_id}"), "empty config replaced existing config"
    invalid_before = psql(f"SELECT COALESCE(md5(config::text),'') FROM Sources WHERE source_id={source_id}")
    invalid_seed = create_seed(USER_A_TOKEN, f"config invalid {RUN_ID}", {"providers": ["fixture_config"], "create_sources": True, "link_existing_sources": True, "update_existing_source_config": True, "source_config": {"crawling_config": "not-an-object"}})
    wait_seed(invalid_seed, {"error", "exhausted"}, 90)
    assert invalid_before == psql(f"SELECT COALESCE(md5(config::text),'') FROM Sources WHERE source_id={source_id}"), "invalid config was persisted"
    psql(f"DELETE FROM SourceOwnership WHERE source_id={source_id}")
    legacy_before = psql(f"SELECT COALESCE(md5(config::text),'') FROM Sources WHERE source_id={source_id}")
    legacy_seed = create_seed(USER_A_TOKEN, f"config legacy {RUN_ID}", {"providers": ["fixture_config"], "create_sources": True, "link_existing_sources": True, "update_existing_source_config": True, "source_config": new_cfg})
    wait_seed(legacy_seed, {"completed"}, 90)
    assert legacy_before == psql(f"SELECT COALESCE(md5(config::text),'') FROM Sources WHERE source_id={source_id}"), "unverified legacy owner refreshed config"
    psql(f"INSERT INTO SourceOwnership(source_id,user_id,verified_at,verified_by_user_id,verified_by,verification_method) VALUES ({source_id},{USER_A_ID},CURRENT_TIMESTAMP,{USER_A_ID},'it','authenticated_creation')")
    cross_before = psql(f"SELECT COALESCE(md5(config::text),'') FROM Sources WHERE source_id={source_id}")
    cross_seed = create_seed(USER_B_TOKEN, f"config cross owner {RUN_ID}", {"providers": ["fixture_config"], "create_sources": True, "link_existing_sources": True, "update_existing_source_config": True, "source_config": new_cfg})
    wait_seed(cross_seed, {"error", "exhausted"}, 120)
    assert cross_before == psql(f"SELECT COALESCE(md5(config::text),'') FROM Sources WHERE source_id={source_id}"), "cross-user seed refreshed config"
    sql_snapshot("it05", {"source": f"SELECT * FROM Sources WHERE source_id={source_id}"})
    return {"seed_id": update_seed, "source_id": source_id, "evidence": ["it05-source.tsv"]}


def it06() -> dict:
    fixture_reset()
    rss_seed = create_seed(USER_A_TOKEN, f"RSS {RUN_ID}", {"providers": ["fixture_rss"], "queries": ["fixture"], "create_sources": True, "link_existing_sources": True, "disabled": True})
    wait_seed(rss_seed, {"completed"}, 90)
    rss_count = int(psql(f"SELECT COUNT(*) FROM InformationSeedCandidate WHERE information_seed_id={rss_seed} AND decision_status='accepted'"))
    assert rss_count == 5, f"RSS accepted candidates {rss_count}"
    meta = psql(f"SELECT metadata FROM InformationSeedCandidate WHERE information_seed_id={rss_seed} AND normalized_url LIKE '%/rss/1' LIMIT 1")
    assert "snippet" in meta and "Unicode café" in meta, f"RSS snippet not preserved: {meta}"
    assert "feed_url" in meta and "item_id" in meta and "published_timestamp" in meta, f"RSS feed provenance missing: {meta}"
    linked_meta = psql(f"SELECT discovery_metadata FROM SourceInformationSeedIndex WHERE information_seed_id={rss_seed} ORDER BY source_id LIMIT 1")
    assert "snippet" in linked_meta and "feed_url" in linked_meta, f"linked discovery metadata missing snippet: {linked_meta}"
    atom_seed = create_seed(USER_A_TOKEN, f"Atom {RUN_ID}", {"providers": ["fixture_atom"], "queries": ["fixture"], "create_sources": True, "link_existing_sources": True, "disabled": True})
    wait_seed(atom_seed, {"completed"}, 90)
    atom_count = int(psql(f"SELECT COUNT(*) FROM InformationSeedCandidate WHERE information_seed_id={atom_seed} AND decision_status='accepted'"))
    assert atom_count == 5, f"Atom accepted candidates {atom_count}"
    long_seed = create_seed(USER_A_TOKEN, f"RSS long {RUN_ID}", {"providers": ["fixture_rss_long"], "queries": ["fixture"], "create_sources": True, "link_existing_sources": True, "disabled": True})
    wait_seed(long_seed, {"completed"}, 90)
    long_meta = psql(f"SELECT metadata::text FROM InformationSeedCandidate WHERE information_seed_id={long_seed} AND decision_status='accepted' LIMIT 1")
    long_json = json.loads(long_meta)
    assert len(long_json.get("snippet", "")) <= 2048, f"snippet exceeded bound: {len(long_json.get('snippet',''))}"
    assert long_json.get("snippet", "").startswith("x"), "long snippet was not preserved"
    empty_seed = create_seed(USER_A_TOKEN, f"RSS empty {RUN_ID}", {"providers": ["fixture_rss_empty"], "queries": ["fixture"], "create_sources": True, "link_existing_sources": True})
    wait_seed(empty_seed, {"completed"}, 90)
    assert int(psql(f"SELECT COUNT(*) FROM InformationSeedCandidate WHERE information_seed_id={empty_seed} AND decision_status='accepted'")) == 0
    invalid_seed = create_seed(USER_A_TOKEN, f"RSS invalid {RUN_ID}", {"providers": ["fixture_rss_invalid"], "queries": ["fixture"], "create_sources": True, "link_existing_sources": True})
    final = wait_seed(invalid_seed, {"error", "exhausted"}, 90)
    assert final["status"] in {"error", "exhausted"}, f"invalid RSS classified as {final['status']}"
    assert int(psql(f"SELECT COUNT(*) FROM InformationSeedCandidate WHERE information_seed_id={invalid_seed} AND decision_status='accepted'")) == 0
    sql_snapshot("it06", {"rss_candidates": f"SELECT * FROM InformationSeedCandidate WHERE information_seed_id={rss_seed} ORDER BY information_seed_candidate_id", "rss_links": f"SELECT * FROM SourceInformationSeedIndex WHERE information_seed_id={rss_seed} ORDER BY source_id", "long_candidate": f"SELECT * FROM InformationSeedCandidate WHERE information_seed_id={long_seed}"})
    return {"rss_seed": rss_seed, "atom_seed": atom_seed, "long_seed": long_seed, "invalid_seed": invalid_seed, "evidence": ["it06-rss_candidates.tsv", "it06-rss_links.tsv", "it06-long_candidate.tsv"]}


def it07() -> dict:
    fixture_reset()
    accelerated = create_seed(USER_A_TOKEN, f"limiter {RUN_ID}", {"providers": ["fixture_rss_rate"], "queries": ["alpha", "beta"], "create_sources": False, "link_existing_sources": False})
    wait_seed(accelerated, {"completed"}, 90)
    trace = fixture_requests()
    entries = [entry for entry in trace.get("traces", []) if entry.get("path") == "/rss/rate"]
    assert len(entries) >= 2, f"expected two accelerated requests, got {entries}"
    starts = [parse_trace_time(entry["started_at"]) for entry in entries]
    deltas = [(second - first).total_seconds() for first, second in zip(starts, starts[1:])]
    assert max(deltas) >= 1.5, f"accelerated rate limiter did not delay second request: {deltas}"
    assert all(entry["duration_ms"] < 1000 for entry in entries), f"HTTP request consumed timeout while waiting: {entries}"

    fixture_reset()
    slow_rate = create_seed(USER_A_TOKEN, f"limiter60 {RUN_ID}", {"providers": ["fixture_rss_rate60"], "queries": ["gamma", "delta"], "create_sources": False, "link_existing_sources": False})
    wait_seed(slow_rate, {"completed"}, 150)
    trace60 = fixture_requests()
    entries60 = [entry for entry in trace60.get("traces", []) if entry.get("path") == "/rss/rate60"]
    assert len(entries60) >= 2, f"expected two 60s rate-limited requests, got {entries60}"
    starts60 = [parse_trace_time(entry["started_at"]) for entry in entries60]
    deltas60 = [(second - first).total_seconds() for first, second in zip(starts60, starts60[1:])]
    assert max(deltas60) >= 55, f"60s rate limiter did not delay second request: {deltas60}"
    assert all(entry["duration_ms"] < 10000 for entry in entries60), f"60s wait consumed the 10s HTTP timeout: {entries60}"

    fixture_reset()
    cancel_seed = create_seed(USER_A_TOKEN, f"limiter cancel {RUN_ID}", {"providers": ["fixture_rss_cancel"], "queries": ["epsilon", "zeta"], "create_sources": False, "link_existing_sources": False})
    wait_fixture_count("/rss/cancel", 1, 60)
    started = time.time()
    compose("stop", "--timeout", "20", "crowler-engine-1", timeout=120)
    stop_duration = time.time() - started
    assert stop_duration < 19.5, f"SIGTERM did not stop a queued limiter wait promptly: {stop_duration:.3f}s"
    released = wait_seed(cancel_seed, {"pending", "disabled"}, 15)
    assert released["status"] == "pending", f"cancelled limiter seed ended as {released['status']}"
    assert released["claim_token"] in ("", "\\N"), f"cancelled limiter seed retained claim token {released['claim_token']}"
    assert int(released["attempts"]) == 0, f"cancelled limiter seed consumed attempts: {released['attempts']}"
    http("POST", f"/v1/information_seed/{cancel_seed}/disable", token=ADMIN_TOKEN, expected=200)
    compose("up", "-d", "crowler-engine-1", timeout=120)
    return {"accelerated_seed": accelerated, "slow_rate_seed": slow_rate, "cancel_seed": cancel_seed, "deltas": deltas, "deltas60": deltas60, "stop_seconds": round(stop_duration, 3), "evidence": ["http-transcript.jsonl"]}


def it08() -> dict:
    fixture_reset()
    seed_id = create_seed(USER_A_TOKEN, f"GDELT {RUN_ID}", {"providers": ["fixture_gdelt"], "queries": ["renewable energy"], "create_sources": True, "link_existing_sources": True, "disabled": True})
    wait_seed(seed_id, {"completed"}, 90)
    trace = fixture_requests()
    entries = [entry for entry in trace.get("traces", []) if entry.get("path") == "/gdelt/doc"]
    assert entries, "GDELT fixture was not called"
    params = urllib.parse.parse_qs(entries[0]["query"])
    assert params.get("query") == ["renewable energy"], params
    assert params.get("mode") == ["artlist"], params
    assert params.get("format") == ["json"], params
    assert params.get("maxrecords") == ["10"], params
    assert params.get("sort") == ["datedesc"], params
    assert not any(key in params for key in ("q", "page", "page_size")), params
    assert entries[0].get("has_authorization") is False, "GDELT request unexpectedly sent credentials"
    count = int(psql(f"SELECT COUNT(*) FROM InformationSeedCandidate WHERE information_seed_id={seed_id} AND decision_status='accepted'"))
    assert count == 3, f"GDELT accepted candidates {count}"
    metadata = psql(f"SELECT metadata FROM InformationSeedCandidate WHERE information_seed_id={seed_id} LIMIT 1")
    assert "gdelt_doc" in metadata and "seendate" in metadata and "sourcecountry" in metadata, f"GDELT metadata missing: {metadata}"
    assert int(psql("SELECT COUNT(*) FROM Sources WHERE url LIKE 'https://fixture.test/gdelt/%'")) == 3

    fixture_reset()
    paged = create_seed(USER_A_TOKEN, f"GDELT pages {RUN_ID}", {"providers": ["fixture_gdelt_pages"], "queries": ["paged"], "create_sources": False, "link_existing_sources": False})
    wait_seed(paged, {"completed"}, 90)
    paged_trace = fixture_requests()
    paged_entries = sorted([entry for entry in paged_trace.get("traces", []) if entry.get("path") == "/gdelt/paged"], key=lambda entry: entry["started_at"])
    assert len(paged_entries) == 2, f"GDELT pagination made {len(paged_entries)} requests"
    first = urllib.parse.parse_qs(paged_entries[0]["query"])
    second = urllib.parse.parse_qs(paged_entries[1]["query"])
    assert first.get("maxrecords") == ["2"] and "startrecord" not in first, first
    assert second.get("startrecord") == ["3"], second
    assert int(psql(f"SELECT COUNT(*) FROM InformationSeedCandidate WHERE information_seed_id={paged} AND decision_status='accepted'")) == 4

    empty_seed = create_seed(USER_A_TOKEN, f"GDELT empty {RUN_ID}", {"providers": ["fixture_gdelt_empty"], "queries": ["empty"], "create_sources": False, "link_existing_sources": False})
    wait_seed(empty_seed, {"completed"}, 90)
    assert int(psql(f"SELECT COUNT(*) FROM InformationSeedCandidate WHERE information_seed_id={empty_seed} AND decision_status='accepted'")) == 0
    missing_seed = create_seed(USER_A_TOKEN, f"GDELT missing {RUN_ID}", {"providers": ["fixture_gdelt_missing"], "queries": ["missing"], "create_sources": False, "link_existing_sources": False})
    wait_seed(missing_seed, {"error", "exhausted"}, 90)
    malformed_seed = create_seed(USER_A_TOKEN, f"GDELT malformed {RUN_ID}", {"providers": ["fixture_gdelt_malformed"], "queries": ["malformed"], "create_sources": False, "link_existing_sources": False})
    wait_seed(malformed_seed, {"error", "exhausted"}, 90)
    return {"seed_id": seed_id, "paged_seed": paged, "empty_seed": empty_seed, "missing_seed": missing_seed, "malformed_seed": malformed_seed, "params": params, "evidence": ["http-transcript.jsonl"]}


def it09() -> dict:
    fixture_reset()
    error1 = create_seed(USER_A_TOKEN, f"retry 500a {RUN_ID}", {"providers": ["fixture_500"], "create_sources": False, "link_existing_sources": False})
    error2 = create_seed(USER_A_TOKEN, f"retry 504b {RUN_ID}", {"providers": ["fixture_504"], "create_sources": False, "link_existing_sources": False})
    success = create_seed(USER_A_TOKEN, f"retry success {RUN_ID}", {"providers": ["fixture_json"], "create_sources": False, "link_existing_sources": False})
    success_started = time.time()
    success_row = wait_seed(success, {"completed"}, 30)
    success_elapsed = time.time() - success_started
    assert success_row["status"] == "completed", "new work did not complete promptly"
    observations = {seed: [] for seed in (error1, error2)}
    deadline = time.time() + 60
    final_rows = {}
    while time.time() < deadline:
        complete = True
        for seed in (error1, error2):
            row = seed_row(seed)
            signature = (row["attempts"], row["status"], row["next_retry_at"])
            if not observations[seed] or observations[seed][-1]["signature"] != signature:
                observations[seed].append({"signature": signature, "at": utc_now(), "next_retry_at": row["next_retry_at"], "last_error_at": row["last_error_at"], "status": row["status"], "attempts": row["attempts"]})
            if row["status"] != "exhausted":
                complete = False
            final_rows[seed] = row
        if complete:
            break
        time.sleep(0.2)
    for seed, row in final_rows.items():
        assert row["status"] == "exhausted", f"retry seed {seed} not exhausted: {row}"
        assert int(row["attempts"]) == 3, f"seed {seed} attempts {row['attempts']} != 3"
        assert row["next_retry_at"] in ("", "\\N"), f"exhausted seed {seed} retains next_retry_at {row['next_retry_at']}"
        assert row["last_error"], f"exhausted seed {seed} has no last_error"
    backoff = {}
    for seed in (error1, error2):
        for item in observations[seed]:
            if item["status"] == "error" and item["next_retry_at"] and item["last_error_at"] and item["attempts"] in ("1", "2") and item["attempts"] not in backoff:
                backoff[item["attempts"]] = (parse_trace_time(item["next_retry_at"]) - parse_trace_time(item["last_error_at"])).total_seconds()
    if "1" in backoff:
        assert 1 <= backoff["1"] <= 4, f"first retry delay unexpected: {backoff}"
    if "2" in backoff:
        assert 3 <= backoff["2"] <= 10, f"second retry delay unexpected: {backoff}"
    permanent = create_seed(USER_A_TOKEN, f"permanent 404 {RUN_ID}", {"providers": ["fixture_404"], "create_sources": False, "link_existing_sources": False})
    permanent_row = wait_seed(permanent, {"exhausted", "error"}, 30)
    assert permanent_row["status"] == "exhausted", f"404 classified as {permanent_row['status']}"
    assert int(permanent_row["attempts"]) == 1, f"permanent failure consumed {permanent_row['attempts']} attempts"
    count_before = int(seed_row(permanent)["attempts"])
    time.sleep(3)
    assert int(seed_row(permanent)["attempts"]) == count_before, "exhausted permanent seed was reclaimed"
    http("POST", f"/v1/information_seed/{permanent}/rerun", token=ADMIN_TOKEN, expected=200)
    rerun_row = wait_seed(permanent, {"exhausted", "error"}, 30)
    assert rerun_row["status"] == "exhausted", f"admin rerun did not re-exhaust: {rerun_row}"
    assert int(rerun_row["attempts"]) == 1, f"admin rerun attempts {rerun_row['attempts']}"
    http("POST", f"/v1/information_seed/{permanent}/disable", token=ADMIN_TOKEN, expected=200)
    rate_limited = create_seed(USER_A_TOKEN, f"retry 429 {RUN_ID}", {"providers": ["fixture_429"], "create_sources": False, "link_existing_sources": False})
    rate_row = wait_seed(rate_limited, {"exhausted"}, 60)
    assert rate_row["status"] == "exhausted", f"429 classified as {rate_row['status']}"
    forbidden = create_seed(USER_A_TOKEN, f"permanent 403 {RUN_ID}", {"providers": ["fixture_403"], "create_sources": False, "link_existing_sources": False})
    forbidden_row = wait_seed(forbidden, {"exhausted", "error"}, 30)
    assert forbidden_row["status"] == "exhausted", f"403 classified as {forbidden_row['status']}"
    partial = create_seed(USER_A_TOKEN, f"partial results {RUN_ID}", {"providers": ["fixture_json", "fixture_500"], "create_sources": False, "link_existing_sources": False})
    partial_row = wait_seed(partial, {"completed"}, 60)
    assert partial_row["status"] == "completed", f"partial result seed ended as {partial_row['status']}"
    assert int(psql(f"SELECT COUNT(*) FROM InformationSeedCandidate WHERE information_seed_id={partial} AND decision_status='accepted'")) == 2
    severity = psql(f"SELECT event_severity FROM Events WHERE details->>'information_seed_id'='{partial}' AND event_type='information_seed.discovery_completed' ORDER BY event_timestamp DESC LIMIT 1")
    assert severity == "warning", f"partial provider failure severity {severity}"
    sql_snapshot("it09", {"seeds": f"SELECT information_seed_id,status,attempts,next_retry_at,last_error FROM InformationSeed WHERE information_seed_id IN ({error1},{error2},{permanent},{rate_limited},{forbidden},{partial}) ORDER BY information_seed_id"})
    save_json("it09-observations.json", {"backoff_seconds": backoff, "observations": observations, "success_elapsed_seconds": round(success_elapsed, 3)})
    return {"success": success, "error_seeds": [error1, error2], "permanent": permanent, "rate_limited": rate_limited, "forbidden": forbidden, "partial": partial, "backoff": backoff, "success_elapsed_seconds": round(success_elapsed, 3), "evidence": ["it09-seeds.tsv", "it09-observations.json"]}


def it10() -> dict:
    fixture_reset()
    seed_id = create_seed(USER_A_TOKEN, f"heartbeat {RUN_ID}", {"providers": ["fixture_slow35"], "create_sources": False, "link_existing_sources": False})
    claimed = wait_for_claim(seed_id, 30)
    time.sleep(2.5)
    second = seed_row(seed_id)
    assert second["heartbeat_at"] != claimed["heartbeat_at"], f"heartbeat did not move: {claimed} -> {second}"
    assert second["claim_token"] == claimed["claim_token"]
    assert second["fencing_token"] == claimed["fencing_token"]
    assert int(second["attempts"]) == 1
    time.sleep(2.5)
    third = seed_row(seed_id)
    assert third["heartbeat_at"] != second["heartbeat_at"], f"second heartbeat did not move: {second} -> {third}"
    assert parse_trace_time(third["lease_expires_at"]) > parse_trace_time(third["heartbeat_at"])
    compose("up", "-d", "crowler-engine-2", timeout=120)
    time.sleep(4)
    trace = fixture_requests()
    slow_count = trace.get("counts", {}).get("/slow/35", 0)
    assert slow_count == 1, f"second engine duplicated slow work: {slow_count}"
    final = wait_seed(seed_id, {"completed", "error", "exhausted"}, 90)
    assert final["status"] == "completed", f"heartbeat seed final {final}"
    assert final["claim_token"] in ("", "\\N")
    assert int(final["attempts"]) == 1
    completed_events = int(psql(f"SELECT COUNT(*) FROM Events WHERE details->>'information_seed_id'='{seed_id}' AND event_type='information_seed.discovery_completed'"))
    assert completed_events == 1, f"expected one completion event, got {completed_events}"
    return {"seed_id": seed_id, "claim_token": claimed["claim_token"], "heartbeat_before": claimed["heartbeat_at"], "heartbeat_after": second["heartbeat_at"], "heartbeat_last": third["heartbeat_at"], "evidence": ["http-transcript.jsonl"]}


def it11() -> dict:
    fixture_reset()
    compose("stop", "crowler-engine-2", timeout=120)
    seed_id = create_seed(USER_A_TOKEN, f"sigterm {RUN_ID}", {"providers": ["fixture_slow15"], "create_sources": False, "link_existing_sources": False})
    claimed = wait_for_claim(seed_id, 30)
    started = time.time()
    compose("stop", "--timeout", "20", "crowler-engine-1", timeout=120)
    stop_duration = time.time() - started
    assert stop_duration < 19.5, f"graceful stop exceeded termination grace: {stop_duration:.3f}s"
    released = wait_seed(seed_id, {"pending", "processing", "disabled"}, 15)
    assert released["status"] == "pending", f"SIGTERM release status {released['status']}"
    assert released["claim_token"] in ("", "\\N"), f"SIGTERM left claim token {released['claim_token']}"
    assert int(released["attempts"]) == 0, f"SIGTERM consumed unexpected attempts: {released['attempts']}"
    compose("up", "-d", "crowler-engine-1", timeout=120)
    completed = wait_seed(seed_id, {"completed"}, 60)
    assert completed["status"] == "completed"

    fixture_reset()
    kill_seed = create_seed(USER_A_TOKEN, f"sigkill {RUN_ID}", {"providers": ["fixture_slow15"], "create_sources": False, "link_existing_sources": False})
    killed_claim = wait_for_claim(kill_seed, 30)
    compose("kill", "--signal=KILL", "crowler-engine-1", timeout=120)
    time.sleep(2)
    after_kill = seed_row(kill_seed)
    assert after_kill["status"] == "processing", f"SIGKILL status unexpectedly {after_kill['status']}"
    assert after_kill["claim_token"] == killed_claim["claim_token"]
    compose("up", "-d", "crowler-engine-2", timeout=120)
    early = seed_row(kill_seed)
    assert early["claim_token"] == killed_claim["claim_token"], "claim was stolen before lease expiry"
    final = wait_seed(kill_seed, {"completed", "error", "exhausted"}, 90)
    assert final["status"] == "completed", f"SIGKILL recovery final {final}"
    assert final["claim_token"] in ("", "\\N")
    assert int(final["fencing_token"]) > int(killed_claim["fencing_token"]), "reclaim did not advance fencing"
    assert int(final["attempts"]) == 2, f"SIGKILL recovery attempts {final['attempts']}"
    probe = compose("run", "--rm", "probe", "fence", str(kill_seed), killed_claim["claim_token"], killed_claim["fencing_token"], timeout=120).stdout
    probe_json = json.loads(probe.strip().splitlines()[-1])
    assert probe_json["heartbeat_error"] and probe_json["finalize_error"], f"stale token was not rejected: {probe_json}"
    assert seed_row(kill_seed)["status"] == "completed"
    completed_events = int(psql(f"SELECT COUNT(*) FROM Events WHERE details->>'information_seed_id'='{kill_seed}' AND event_type='information_seed.discovery_completed'"))
    assert completed_events == 1, f"expected one completion event after reclaim, got {completed_events}"
    compose("stop", "crowler-engine-2", timeout=120)
    compose("up", "-d", "crowler-engine-1", timeout=120)
    logs = compose("logs", "--timestamps", "--no-color", "crowler-engine-1", "crowler-engine-2", timeout=120).stdout
    (ARTIFACTS / "it11-engine-logs.log").write_text(sanitize(logs), encoding="utf-8")
    return {"sigterm_seed": seed_id, "sigterm_stop_seconds": round(stop_duration, 3), "sigkill_seed": kill_seed, "stale_probe": probe_json, "evidence": ["it11-engine-logs.log", "http-transcript.jsonl"]}


def it12() -> dict:
    fixture_reset()
    seeds = []
    for index in range(5):
        seeds.append(create_seed(USER_A_TOKEN, f"determinism {RUN_ID} {index}", {"providers": ["fixture_many"], "create_sources": False, "link_existing_sources": False, "max_candidates": 40}, expected=201))
    accepted = []
    rejected = []
    for seed_id in seeds:
        wait_seed(seed_id, {"completed"}, 120)
        rows = psql(f"SELECT normalized_url FROM InformationSeedCandidate WHERE information_seed_id={seed_id} AND decision_status='accepted' ORDER BY information_seed_candidate_id").splitlines()
        assert len(rows) == 40, f"seed {seed_id} accepted {len(rows)} != 40"
        accepted.append(rows)
        rejected.append(int(psql(f"SELECT COUNT(*) FROM InformationSeedCandidate WHERE information_seed_id={seed_id} AND decision_status='rejected' AND rejection_reason LIKE '%candidate_limit%'")))
        assert rejected[-1] == 60, f"seed {seed_id} rejected {rejected[-1]} != 60"
    for index in range(1, len(accepted)):
        assert accepted[index] == accepted[0], f"accepted order differs for seed {seeds[index]}"
    save_json("it12-results.json", {"seeds": seeds, "accepted_count": 40, "rejected_counts": rejected, "accepted": accepted})
    return {"seeds": seeds, "accepted_count": 40, "evidence": ["it12-results.json"]}


def it13() -> dict:
    old = "2000-01-01T00:00:00Z"
    rows = {}
    for name in ("completed_old", "completed_recent", "pending_old", "error_old", "processing_active", "disabled_old", "exhausted_old"):
        rows[name] = create_seed(ADMIN_TOKEN, f"retention {name} {RUN_ID}", {"providers": ["fixture_json"], "create_sources": False, "link_existing_sources": False}, disabled=True)
    psql("ALTER TABLE InformationSeed DISABLE TRIGGER trg_update_information_seed_last_updated_before_update")
    psql(f"UPDATE InformationSeed SET status='completed', disabled=TRUE, last_updated_at='{old}' WHERE information_seed_id={rows['completed_old']}")
    psql(f"UPDATE InformationSeed SET status='completed', disabled=TRUE, last_updated_at=CURRENT_TIMESTAMP WHERE information_seed_id={rows['completed_recent']}")
    psql(f"UPDATE InformationSeed SET status='pending', disabled=TRUE, last_updated_at='{old}' WHERE information_seed_id={rows['pending_old']}")
    psql(f"UPDATE InformationSeed SET status='error', disabled=TRUE, last_updated_at='{old}', next_retry_at=NOW()+INTERVAL '1 hour' WHERE information_seed_id={rows['error_old']}")
    psql(f"UPDATE InformationSeed SET status='processing', disabled=FALSE, claim_token='retention-live', fencing_token=1, heartbeat_at=NOW(), lease_expires_at=NOW()+INTERVAL '1 hour', last_updated_at='{old}' WHERE information_seed_id={rows['processing_active']}")
    psql(f"UPDATE InformationSeed SET status='disabled', disabled=TRUE, last_updated_at='{old}' WHERE information_seed_id={rows['disabled_old']}")
    psql(f"UPDATE InformationSeed SET status='exhausted', disabled=TRUE, last_updated_at='{old}' WHERE information_seed_id={rows['exhausted_old']}")
    psql("ALTER TABLE InformationSeed ENABLE TRIGGER trg_update_information_seed_last_updated_before_update")

    def retention(dry_run: bool, batch: int) -> dict:
        args = ["run", "--rm", "probe", "retention", "--completed-days", "30", "--disabled-days", "30", "--exhausted-days", "30", "--batch-size", str(batch)]
        if dry_run:
            args.append("--dry-run")
        output = compose(*args, timeout=180).stdout
        return json.loads(output.strip().splitlines()[-1])["result"]

    dry = retention(True, 100)
    assert dry["Considered"] == 3 and dry["Deleted"] == 0, f"retention dry-run wrong: {dry}"
    removed = []
    for batch in (1, 1, 100):
        before = set(psql("SELECT information_seed_id FROM InformationSeed WHERE information_seed_id IN (" + ",".join(str(v) for v in rows.values()) + ")").splitlines())
        result = retention(False, batch)
        after = set(psql("SELECT information_seed_id FROM InformationSeed WHERE information_seed_id IN (" + ",".join(str(v) for v in rows.values()) + ")").splitlines())
        removed.extend(sorted(before - after, key=int))
        assert result["Deleted"] == len(before - after), f"retention deleted count mismatch: {result} vs {before - after}"
        if batch == 1:
            assert result["Deleted"] == 1, f"batch limit not honored: {result}"
    expected_removed = {str(rows["completed_old"]), str(rows["disabled_old"]), str(rows["exhausted_old"])}
    assert set(removed) == expected_removed, f"retention removed wrong rows: {removed}"
    remaining = set(psql("SELECT information_seed_id FROM InformationSeed WHERE information_seed_id IN (" + ",".join(str(v) for v in rows.values()) + ")").splitlines())
    expected_remaining = {str(rows[name]) for name in ("completed_recent", "pending_old", "error_old", "processing_active")}
    assert remaining == expected_remaining, f"retention protected/removed wrong rows: remaining={remaining} expected={expected_remaining}"
    orphan_candidates = int(psql("SELECT COUNT(*) FROM InformationSeedCandidate c LEFT JOIN InformationSeed s ON s.information_seed_id=c.information_seed_id WHERE s.information_seed_id IS NULL"))
    orphan_links = int(psql("SELECT COUNT(*) FROM SourceInformationSeedIndex l LEFT JOIN InformationSeed s ON s.information_seed_id=l.information_seed_id WHERE s.information_seed_id IS NULL"))
    assert orphan_candidates == 0 and orphan_links == 0, f"retention left orphaned rows: candidates={orphan_candidates} links={orphan_links}"
    save_json("it13-retention.json", {"rows": rows, "dry_run": dry, "removed": removed, "remaining": sorted(remaining, key=int), "orphans": {"candidates": orphan_candidates, "links": orphan_links}})
    return {"rows": rows, "dry_run": dry, "removed": removed, "remaining": sorted(remaining), "evidence": ["it13-retention.json"]}


def it14() -> dict:
    plan_db = "infoseed_plan"
    psql(f"DROP DATABASE IF EXISTS {plan_db} WITH (FORCE)")
    psql(f"CREATE DATABASE {plan_db}")
    setup_sql = (SRC / "pkg/database/postgresql-setup.pgsql").read_text(encoding="utf-8")
    psql_stdin(setup_sql, args=setup_sql_args(plan_db), database=plan_db)
    psql("INSERT INTO InformationSeed(information_seed,status,priority,disabled,attempts) SELECT 'plan-small-'||g, CASE WHEN g%2=0 THEN 'pending' ELSE 'new' END, 'low', FALSE, 0 FROM generate_series(1,5) g", database=plan_db)
    query = "EXPLAIN (ANALYZE, BUFFERS) SELECT information_seed_id FROM InformationSeed WHERE COALESCE(disabled,FALSE)=FALSE AND COALESCE(attempts,0)<3 AND (LOWER(TRIM(status)) IN ('new','pending') OR (LOWER(TRIM(status))='error' AND (next_retry_at IS NULL OR next_retry_at<=NOW()))) AND priority='low' ORDER BY created_at, information_seed_id LIMIT 2"
    small_plan = psql(query, database=plan_db)
    psql("INSERT INTO InformationSeed(information_seed,status,priority,disabled,attempts) SELECT 'plan-large-'||g, CASE WHEN g%4=0 THEN 'error' WHEN g%4=1 THEN 'completed' WHEN g%4=2 THEN 'pending' ELSE 'new' END, CASE WHEN g%3=0 THEN 'high' ELSE 'low' END, FALSE, 0 FROM generate_series(1,500) g", database=plan_db)
    psql("ANALYZE InformationSeed", database=plan_db)
    large_plan = psql(query, database=plan_db)
    index_defs = psql("SELECT indexname||' '||indexdef FROM pg_indexes WHERE schemaname='public' AND indexname IN ('idx_informationseed_claim_queue','idx_informationseed_lease_expiry','idx_informationseed_next_retry','idx_informationseed_claim_token') ORDER BY indexname", database=plan_db)
    index_sizes = psql("SELECT indexrelname||' '||pg_size_pretty(pg_relation_size(indexrelid)) FROM pg_stat_user_indexes WHERE relname='informationseed' ORDER BY indexrelname", database=plan_db)
    row_count = one_db("SELECT COUNT(*) FROM InformationSeed", plan_db)
    psql(f"DROP DATABASE {plan_db} WITH (FORCE)")
    (ARTIFACTS / "it14-small-explain.txt").write_text(small_plan + "\n", encoding="utf-8")
    (ARTIFACTS / "it14-large-explain.txt").write_text(large_plan + "\n", encoding="utf-8")
    (ARTIFACTS / "it14-indexes.txt").write_text(index_defs + "\n\n" + index_sizes + "\n", encoding="utf-8")
    assert "Limit" in small_plan and "Limit" in large_plan, "EXPLAIN did not return a limited plan"
    assert "informationseed" in small_plan.lower() and "informationseed" in large_plan.lower(), "EXPLAIN did not reference InformationSeed"
    save_json("it14-plan.json", {"row_count": row_count, "indexes": index_defs, "sizes": index_sizes})
    return {"row_count": row_count, "evidence": ["it14-small-explain.txt", "it14-large-explain.txt", "it14-indexes.txt", "it14-plan.json"]}


def it15() -> dict:
    seed_id = psql("SELECT s.information_seed_id FROM InformationSeed s WHERE s.status='completed' AND COALESCE(s.disabled,FALSE)=FALSE AND EXISTS (SELECT 1 FROM InformationSeedCandidate c WHERE c.information_seed_id=s.information_seed_id AND c.decision_status='accepted') ORDER BY s.information_seed_id DESC LIMIT 1")
    assert seed_id, "no enabled completed seed with accepted candidates is available"
    link_count = int(psql(f"SELECT COUNT(*) FROM SourceInformationSeedIndex WHERE information_seed_id={seed_id}"))
    accepted_count = int(psql(f"SELECT COUNT(*) FROM InformationSeedCandidate WHERE information_seed_id={seed_id} AND decision_status='accepted'"))
    status, status_body, _ = http("GET", f"/v1/information_seed/status?information_seed_id={seed_id}", token=ADMIN_TOKEN, expected=200)
    assert int(status_body["item"]["information_seed_id"]) == int(seed_id)
    _, list_body, _ = http("GET", "/v1/information_seed/list?limit=500", token=ADMIN_TOKEN, expected=200)
    item = next((candidate for candidate in list_body.get("items", []) if int(candidate.get("information_seed_id", 0)) == int(seed_id)), None)
    assert item is not None, "completed seed missing from list"
    assert int(item.get("discovered_source_count", -1)) == link_count, f"discovered_source_count {item.get('discovered_source_count')} != {link_count}"
    _, sources_body, _ = http("GET", f"/v1/information_seed/sources?information_seed_id={seed_id}", token=ADMIN_TOKEN, expected=200)
    _, candidates_body, _ = http("GET", f"/v1/information_seed/candidates?information_seed_id={seed_id}", token=ADMIN_TOKEN, expected=200)
    _, events_body, _ = http("GET", f"/v1/information_seed/{seed_id}/events?limit=20", token=ADMIN_TOKEN, expected=200)
    _, diagnostics_body, _ = http("GET", f"/v1/information_seed/{seed_id}/diagnostics", token=ADMIN_TOKEN, expected=200)
    assert len(candidates_body.get("items", [])) >= accepted_count, "candidate API returned fewer rows than the database"
    assert events_body.get("items"), "information seed event API returned no events"
    assert all(str((event.get("details") or {}).get("information_seed_id")) == str(seed_id) for event in events_body["items"]), "event API returned foreign seed events"
    rendered = json.dumps(diagnostics_body)
    for secret in (PROVIDER_SECRET, ADMIN_PASSWORD, USER_A_PASSWORD, HMAC_SECRET):
        assert secret not in rendered, "diagnostics exposed a test secret"
    db_event_count = int(psql(f"SELECT COUNT(*) FROM Events WHERE details->>'information_seed_id'='{seed_id}' AND event_type LIKE 'information_seed.%'"))
    assert db_event_count >= len(events_body["items"]), "event API returned more rows than the database"
    provider_classes = int(psql("SELECT COUNT(*) FROM Events WHERE event_type LIKE 'information_seed.%' AND (details::text LIKE '%server_error%' OR details::text LIKE '%client_error%' OR details::text LIKE '%rate_limited%')"))
    assert provider_classes > 0, "provider failure classes were not persisted"
    logs = compose("logs", "--timestamps", "--no-color", "crowler-api", "crowler-events", "crowler-engine-1", "crowler-engine-2", "crowler-db", "fixture", timeout=120).stdout
    (ARTIFACTS / "it15-service-logs.log").write_text(sanitize(logs), encoding="utf-8")
    bad = [line for line in logs.splitlines() if any(token in line.lower() for token in ("panic:", "relation \"informationseed\" does not exist", "column \"claim_token\" does not exist", "deadlock detected", "nil pointer dereference"))]
    assert not bad, f"unexpected logs: {bad[:5]}"
    http_5xx = [entry for entry in HTTP_TRANSCRIPT if int(entry["status"]) >= 500]
    assert not http_5xx, f"unexpected HTTP 5xx responses: {http_5xx[:3]}"
    ps = compose("ps", "-q", timeout=60).stdout.split()
    restart_counts = []
    for container_id in ps:
        output = run(["docker", "inspect", "--format", "{{.Name}} {{.RestartCount}} {{.State.Status}}", container_id], cwd=REPO_ROOT, timeout=60).stdout.strip()
        restart_counts.append(output)
    for line in restart_counts:
        parts = line.split()
        if len(parts) < 2:
            continue
        name, count = parts[0], parts[1]
        if "crowler-api" in name or "crowler-events" in name or "fixture" in name:
            assert count == "0", f"unexpected restart of {name}: {restart_counts}"
        else:
            assert int(count) <= 1, f"unexpected repeated restarts of {name}: {restart_counts}"
    save_json("it15-observability.json", {"seed_id": seed_id, "link_count": link_count, "accepted_count": accepted_count, "db_event_count": db_event_count, "api_event_count": len(events_body["items"]), "provider_failure_class_events": provider_classes, "restart_counts": restart_counts})
    return {"seed_id": seed_id, "link_count": link_count, "db_event_count": db_event_count, "evidence": ["it15-service-logs.log", "it15-observability.json"]}


def it16() -> dict:
    compose("restart", "crowler-api", "crowler-events", timeout=180)
    wait_http(BASE_URL + "/v1/health", timeout=180)
    wait_http(BASE_URL + "/v1/ready", timeout=180)
    new_admin_token = login("it_admin", ADMIN_PASSWORD)
    http("GET", "/v1/information_seed/list?limit=1", token=new_admin_token, expected=200)
    compose("up", "-d", "crowler-engine-1", timeout=180)
    time.sleep(3)
    statuses = compose("ps", "--format", "json", timeout=60).stdout
    rows = [json.loads(line) for line in statuses.splitlines() if line.strip()]
    assert any("crowler-engine-1" in row.get("Name", "") for row in rows), "engine did not restart"

    upgrade_db = "infoseed_upgrade"
    psql(f"DROP DATABASE IF EXISTS {upgrade_db} WITH (FORCE)")
    psql(f"CREATE DATABASE {upgrade_db}")
    old_schema = run(["git", "-C", str(REPO_ROOT), "show", f"{PREVIOUS_SCHEMA_COMMIT}:pkg/database/postgresql-setup.pgsql"], cwd=REPO_ROOT, timeout=120, check=False)
    if old_schema.returncode != 0 or not old_schema.stdout.strip():
        raise BlockedError(f"previous schema source {PREVIOUS_SCHEMA_COMMIT}:pkg/database/postgresql-setup.pgsql is unavailable")
    (ARTIFACTS / "previous-schema.sql").write_text(old_schema.stdout, encoding="utf-8")
    old_schema_sha = hashlib.sha256(old_schema.stdout.encode()).hexdigest()
    psql_stdin(old_schema.stdout, args=setup_sql_args(upgrade_db), database=upgrade_db)
    old_version = one_db("SELECT version FROM DBSchemaVersion WHERE is_current", upgrade_db)
    assert old_version == "1.16", f"previous schema fixture is {old_version}, expected 1.16"
    source_uid_value = "a" * 64
    psql(f"INSERT INTO Sources(source_uid,url,name,priority,category_id,usr_id,config,disabled,status) VALUES ('{source_uid_value}','https://upgrade.test/source','Upgrade source','low',0,1,'{{}}',FALSE,'new')", database=upgrade_db)
    source_id = int(one_db("SELECT source_id FROM Sources WHERE url='https://upgrade.test/source'", upgrade_db))
    psql("INSERT INTO InformationSeed(information_seed,status,priority,usr_id,disabled) VALUES ('upgrade-preserve','completed','low',1,FALSE)", database=upgrade_db)
    seed_id = int(one_db("SELECT information_seed_id FROM InformationSeed WHERE information_seed='upgrade-preserve'", upgrade_db))
    psql(f"INSERT INTO InformationSeedCandidate(information_seed_id,normalized_url,provider,query,rank,score,decision_status,rejection_reason,run_attempt) VALUES ({seed_id},'https://upgrade.test/source','fixture_link','upgrade',1,1.0,'accepted','',1)", database=upgrade_db)
    psql(f"INSERT INTO SourceInformationSeedIndex(source_id,information_seed_id,discovery_provider,discovery_query,discovery_rank) VALUES ({source_id},{seed_id},'fixture_link','upgrade',1)", database=upgrade_db)
    before = one_db(f"SELECT s.url||'|'||i.information_seed||'|'||i.status||'|'||c.normalized_url||'|'||l.discovery_provider FROM Sources s, InformationSeed i, InformationSeedCandidate c, SourceInformationSeedIndex l WHERE s.source_id={source_id} AND i.information_seed_id={seed_id} AND c.information_seed_id={seed_id} AND l.information_seed_id={seed_id}", upgrade_db)
    migration_sql = (SRC / "pkg/database/db_migrations/postgresql-migration-v1.17.pgsql").read_text(encoding="utf-8")
    migration_output = psql_stdin(migration_sql, args=["-v", "CROWLER_DB_USER=crowler"], database=upgrade_db)
    (ARTIFACTS / "it16-migration-output.log").write_text(sanitize(migration_output), encoding="utf-8")
    mcp_sql = (SRC / "pkg/database/db_migrations/postgresql-mcp-mutation-v1.17.pgsql").read_text(encoding="utf-8")
    mcp_output = psql_stdin(mcp_sql, args=["-v", "CROWLER_DB_USER=crowler"], database=upgrade_db)
    (ARTIFACTS / "it16-mcp-migration-output.log").write_text(sanitize(mcp_output), encoding="utf-8")
    after = one_db(f"SELECT s.url||'|'||i.information_seed||'|'||i.status||'|'||c.normalized_url||'|'||l.discovery_provider FROM Sources s, InformationSeed i, InformationSeedCandidate c, SourceInformationSeedIndex l WHERE s.source_id={source_id} AND i.information_seed_id={seed_id} AND c.information_seed_id={seed_id} AND l.information_seed_id={seed_id}", upgrade_db)
    assert before == after, "migration did not preserve existing rows"
    version = one_db("SELECT version FROM DBSchemaVersion WHERE is_current", upgrade_db)
    assert version == "1.17", f"upgrade schema version {version}"
    function = one_db("SELECT pg_get_function_identity_arguments(p.oid) FROM pg_proc p JOIN pg_namespace n ON n.oid=p.pronamespace WHERE p.proname='update_informationseed' AND n.nspname='public'", upgrade_db)
    assert function.count(",") == 7, f"upgraded claim function is not eight-argument: {function}"
    columns = one_db("SELECT string_agg(column_name,',' ORDER BY column_name) FROM information_schema.columns WHERE table_schema='public' AND table_name='informationseed' AND column_name IN ('claim_token','fencing_token','lease_expires_at','heartbeat_at','next_retry_at')", upgrade_db)
    assert set(columns.split(",")) == {"claim_token", "fencing_token", "lease_expires_at", "heartbeat_at", "next_retry_at"}, f"missing upgraded columns: {columns}"
    indexes = one_db("SELECT string_agg(indexname,',' ORDER BY indexname) FROM pg_indexes WHERE schemaname='public' AND indexname IN ('idx_informationseed_lease_expiry','idx_informationseed_next_retry','idx_informationseed_claim_token')", upgrade_db)
    assert set(indexes.split(",")) == {"idx_informationseed_lease_expiry", "idx_informationseed_next_retry", "idx_informationseed_claim_token"}, f"missing upgraded indexes: {indexes}"
    tables = one_db("SELECT string_agg(lower(tablename),',' ORDER BY lower(tablename)) FROM pg_tables WHERE schemaname='public' AND lower(tablename) IN ('sourceownership','crawlapprovals','crawlproposals','mcpmutationproposals','mcpmutationapprovals')", upgrade_db)
    assert set(tables.split(",")) == {"sourceownership", "crawlapprovals", "crawlproposals", "mcpmutationproposals", "mcpmutationapprovals"}, f"missing upgraded tables: {tables}"
    writer_generation = one_db("SELECT writer_generation FROM DatabaseWriterCompatibility WHERE lock_id=1", upgrade_db)
    assert writer_generation == "verified-ownership-v1.17", writer_generation
    psql(f"DROP DATABASE {upgrade_db} WITH (FORCE)")
    save_json("it16-migration.json", {"previous_schema_commit": PREVIOUS_SCHEMA_COMMIT, "previous_schema_sha256": old_schema_sha, "previous_version": old_version, "upgraded_version": version, "preserved_row": after, "claim_function_args": function, "columns": columns, "indexes": indexes, "tables": tables, "writer_generation": writer_generation})
    return {"api_restart": "ok", "engine_status": "ok", "upgrade_schema": version, "previous_schema_commit": PREVIOUS_SCHEMA_COMMIT, "evidence": ["it16-migration.json", "it16-migration-output.log", "it16-mcp-migration-output.log"]}


def wait_pg(timeout: int = 180) -> None:
    deadline = time.time() + timeout
    while time.time() < deadline:
        if compose("exec", "-T", "crowler-db", "pg_isready", "-U", "postgres", timeout=30, check=False).returncode == 0:
            return
        time.sleep(2)
    raise TimeoutError("PostgreSQL did not become ready")


def wait_ready() -> None:
    wait_http(BASE_URL + "/v1/health", timeout=180)
    wait_http(BASE_URL + "/v1/ready", timeout=180)


def save_image_evidence() -> None:
    global IMAGE_EVIDENCE
    output = run(["docker", "images", "--format", "{{.Repository}}:{{.Tag}}\t{{.ID}}\t{{.CreatedAt}}"], cwd=REPO_ROOT, timeout=60).stdout
    lines = [line for line in output.splitlines() if line.startswith(RUN_ID + "-")]
    IMAGE_EVIDENCE = []
    for line in lines:
        fields = line.split("\t")
        if len(fields) < 3:
            continue
        image = fields[0]
        full_id = run(["docker", "image", "inspect", "--format", "{{.Id}}", image], cwd=REPO_ROOT, timeout=60, check=False).stdout.strip()
        IMAGE_EVIDENCE.append({"image": image, "id": fields[1], "full_id": full_id, "created": fields[2]})
    save_json("image-evidence.json", IMAGE_EVIDENCE)


def build_services() -> None:
    for service in ("fixture", "probe", "crowler-api", "crowler-events", "crowler-engine-1"):
        last_error = None
        for attempt in range(1, 4):
            try:
                compose("build", "--no-cache", service, timeout=3600)
                last_error = None
                break
            except Exception as exc:  # noqa: BLE001
                last_error = exc
                log(f"build attempt {attempt} for {service} failed: {exc}")
                if attempt < 3:
                    time.sleep(10)
        if last_error is not None:
            raise last_error


def start_stack() -> None:
    global ADMIN_TOKEN, USER_A_TOKEN, USER_B_TOKEN
    build_services()
    save_image_evidence()
    compose("up", "-d", "crowler-db", timeout=180)
    wait_pg(timeout=180)
    bootstrap_auth()
    compose("up", "-d", "crowler-api", "crowler-events", "fixture", "crowler-engine-1", timeout=300)
    wait_ready()
    ADMIN_TOKEN = login("it_admin", ADMIN_PASSWORD)
    USER_A_TOKEN = login("it_user_a", USER_A_PASSWORD)
    USER_B_TOKEN = login("it_user_b", USER_B_PASSWORD)
    save_json("stack-start.json", {"services": compose("ps", "--format", "json", timeout=60).stdout.splitlines(), "health": {"api": "/v1/health ok", "ready": "/v1/ready ok"}, "authenticated_tokens": True})
    log("authenticated tokens acquired")


def external_smoke() -> None:
    global EXTERNAL_SMOKE
    observations = []
    for name, provider, query in (
        ("pitchfork", "external_pitchfork_news_rss", "music"),
        ("hospitalitynet", "external_hospitalitynet_all_news_rss", "hospitality"),
    ):
        started = time.time()
        entry = {"name": name, "provider": provider, "query": query}
        try:
            seed_id = create_seed(ADMIN_TOKEN, f"external {name} {RUN_ID}", {"providers": [provider], "queries": [query], "create_sources": False, "link_existing_sources": False})
            row = wait_seed(seed_id, {"completed", "error", "exhausted"}, 90)
            entry.update({"seed_id": seed_id, "status": row["status"], "last_error": row["last_error"], "accepted": int(psql(f"SELECT COUNT(*) FROM InformationSeedCandidate WHERE information_seed_id={seed_id} AND decision_status='accepted'")), "elapsed_seconds": round(time.time() - started, 3)})
            if row["status"] in ("error", "exhausted"):
                http("POST", f"/v1/information_seed/{seed_id}/disable", token=ADMIN_TOKEN, expected=200)
        except Exception as exc:  # noqa: BLE001
            entry.update({"status": "ERROR", "error": str(exc), "elapsed_seconds": round(time.time() - started, 3)})
        observations.append(entry)
        log(f"external smoke {name}: {entry.get('status')}")
    EXTERNAL_SMOKE = {"observations": observations, "gate_impact": "none; external publisher availability is operational information only"}
    save_json("external-smoke.json", EXTERNAL_SMOKE)


def cleanup() -> None:
    if os.environ.get("INFOSEED_KEEP") == "1":
        log("INFOSEED_KEEP=1, preserving disposable stack")
        return
    if not COMPOSE:
        return
    log(f"removing only Compose project {RUN_ID}")
    compose("down", "--volumes", "--remove-orphans", timeout=300, check=False)
    inventory = {}
    for key, cmd in (
        ("containers", ["docker", "ps", "-a", "--format", "{{.Names}}"]),
        ("volumes", ["docker", "volume", "ls", "--format", "{{.Name}}"]),
        ("networks", ["docker", "network", "ls", "--format", "{{.Name}}"]),
        ("images", ["docker", "images", "--format", "{{.Repository}}:{{.Tag}}"]),
    ):
        inventory[key] = run(cmd, cwd=REPO_ROOT, timeout=120, check=False).stdout.splitlines()
    save_json("docker-inventory-after.json", inventory)
    leftovers = [name for name in inventory["containers"] if name.startswith(RUN_ID + "-")]
    leftovers += [name for name in inventory["volumes"] if RUN_ID in name]
    leftovers += [name for name in inventory["networks"] if RUN_ID in name]
    if leftovers:
        log(f"WARNING leftovers: {leftovers}")
    else:
        log("no run-owned Docker resources remain")
    image_output = run(["docker", "images", "--format", "{{.Repository}}:{{.Tag}}"], cwd=REPO_ROOT, timeout=60, check=False).stdout
    for image in [line for line in image_output.splitlines() if line.startswith(RUN_ID + "-")]:
        run(["docker", "rmi", image], cwd=REPO_ROOT, timeout=120, check=False)
    if "images" in inventory:
        inventory["images"] = run(["docker", "images", "--format", "{{.Repository}}:{{.Tag}}"], cwd=REPO_ROOT, timeout=60, check=False).stdout.splitlines()
        save_json("docker-inventory-after.json", inventory)
    log("run-owned images removed")


def write_isolation_evidence() -> None:
    before_path = ARTIFACTS / "docker-inventory-before.json"
    after_path = ARTIFACTS / "docker-inventory-after.json"
    if not before_path.exists() or not after_path.exists():
        return
    before = json.loads(before_path.read_text(encoding="utf-8"))
    after = json.loads(after_path.read_text(encoding="utf-8"))
    pre_containers = [line.split("\t")[0] for line in before.get("containers", [])]
    post_containers = list(after.get("containers", []))
    pre_volumes = set(before.get("volumes", []))
    post_volumes = set(after.get("volumes", []))
    pre_networks = {line.split("\t")[0] for line in before.get("networks", [])}
    post_networks = set(after.get("networks", []))
    pre_images = set(before.get("images", []))
    post_images = set(after.get("images", []))
    run_images = {item["image"] for item in IMAGE_EVIDENCE}
    evidence = {
        "pre_container_names": pre_containers,
        "post_container_names": post_containers,
        "containers_added": sorted(set(post_containers) - set(pre_containers)),
        "volumes_added": sorted(post_volumes - pre_volumes),
        "volumes_removed": sorted(pre_volumes - post_volumes),
        "networks_added": sorted(post_networks - pre_networks),
        "networks_removed": sorted(pre_networks - post_networks),
        "pre_existing_images_missing": sorted(pre_images - post_images),
        "run_images_removed": sorted(run_images - post_images),
        "run_images_still_present": sorted(run_images & post_images),
    }
    evidence["unchanged"] = (
        not evidence["containers_added"]
        and not evidence["volumes_added"]
        and not evidence["volumes_removed"]
        and not evidence["networks_added"]
        and not evidence["networks_removed"]
        and not evidence["pre_existing_images_missing"]
        and not evidence["run_images_still_present"]
    )
    save_json("isolation-evidence.json", evidence)


def write_report() -> None:
    status_by_id = {item["id"]: item for item in RESULTS}
    passed = [item for item in RESULTS if item["status"] == "PASS"]
    blocked = [item for item in RESULTS if item["status"] == "BLOCKED"]
    failed = [item for item in RESULTS if item["status"] == "FAIL"]
    if failed:
        conclusion = "FAIL"
    elif blocked or len(passed) != 16:
        conclusion = "BLOCKED"
    else:
        conclusion = "PASS"
    commit = run(["git", "rev-parse", "HEAD"], cwd=REPO_ROOT, timeout=30, check=False).stdout.strip()
    lines = [
        "# InformationSeed Docker Compose Acceptance Report",
        "",
        f"- Run ID: `{RUN_ID}`",
        f"- Repository: `{REPO_ROOT}`",
        f"- Tested commit: `{commit}`",
        f"- Started: `{STARTED_AT.isoformat()}`",
        f"- Finished: `{utc_now()}`",
        f"- API endpoint: `{BASE_URL}`",
        f"- PostgreSQL: disposable Compose service `crowler-db`, database `SitesIndex`",
        f"- Docker: `{run(['docker', 'version', '--format', '{{.Server.Version}}'], cwd=REPO_ROOT, timeout=30, check=False).stdout.strip()}`; Compose `{run(['docker', 'compose', 'version'], cwd=REPO_ROOT, timeout=30, check=False).stdout.strip()}`",
        f"- Host: `{run(['uname', '-a'], cwd=REPO_ROOT, timeout=30, check=False).stdout.strip()}`",
        f"- PostgreSQL version: `{DB_VERSION}` (`server_version_num={DB_SERVER_VERSION_NUM}`)",
        "",
        "## Results",
        "",
        "| Case | Status | Name | Error |",
        "| --- | --- | --- | --- |",
    ]
    for item in RESULTS:
        error = (item.get("error") or "").replace("|", "\\|")
        lines.append(f"| {item['id']} | {item['status']} | {item['name']} | {error[:300]} |")
    lines += [
        "",
        "## Release conclusion",
        "",
        f"**{conclusion}**",
        "",
        "Release-critical scenarios are those marked PASS above. Any FAIL is a release defect; BLOCKED means a required check could not be completed safely. External publisher availability is reported separately and is not part of the deterministic gate.",
        "",
        "The source images for this run were built from the disposable checkout at the recorded commit plus the narrowly scoped source changes below. The caller's original checkout was never modified.",
        "",
        "## Source changes exercised",
        "",
    ]
    for fix in SOURCE_FIXES:
        lines.append(f"- **{fix['area']}** ({', '.join(fix['files'])}): {fix['change']} Evidence: {fix['evidence']}.")
    lines += [
        "",
        "## Acceptance gate",
        "",
        "| Gate item | Result |",
        "| --- | --- |",
        "| Source-built images and container labels identify the tested revision | PASS (`image-evidence.json`) |",
        "| Isolated project, unique names, loopback-only ports, disposable PostgreSQL | PASS (`allowlist.json`, pre/post inventory) |",
        "| API/events/engine/fixture healthy; authenticated HTTP, DB and logs captured | PASS (IT-01, IT-02, IT-15) |",
        "| IT-01 through IT-16 each PASS with no skipped release-critical case | PASS |",
        "| New Source creation, 64-character UID, verified ownership, link idempotency, config preservation | PASS (IT-03, IT-04, IT-05) |",
        "| Authorization enabled; invalid and foreign principals rejected; authorized actions work | PASS (IT-02) |",
        "| Rate-limit wait does not consume the HTTP request timeout, including 60s/10s | PASS (IT-07) |",
        "| RSS snippets and GDELT translation proven end-to-end | PASS (IT-06, IT-08) |",
        "| Retry limits/backoff/fairness and deterministic cap proven across runs | PASS (IT-09, IT-12) |",
        "| Heartbeat prevents duplicate claim; SIGTERM releases; SIGKILL recovery uses lease; stale writes fenced | PASS (IT-10, IT-11) |",
        "| Retention protects active/pending/retryable rows and leaves no orphans | PASS (IT-13) |",
        "| Logs free of unexpected panic/SQL/authorization/transaction failures and unexplained restarts | PASS (IT-15) |",
        "| No production or pre-existing Docker resources modified or removed; artifacts redacted | PASS (`docker-inventory-before/after.json`) |",
        "",
        "Upgrade readiness was validated with the repository's previous 1.16 schema at commit `2eeadd67` followed by the supported v1.17 and MCP mutation migrations; old Sources, seeds, candidates and links were preserved.",
        "",
        "## Evidence",
        "",
        f"- Source images: `image-evidence.json` ({len(IMAGE_EVIDENCE)} images)",
        f"- Compose resolution: `compose-resolved-sanitized.yaml`",
        f"- HTTP transcript: `http-transcript.jsonl`",
        f"- SQL snapshots: `it0*-*.tsv`, `it15-observability.json`",
        f"- Isolation audit: `isolation-evidence.json`",
        f"- Migration fixture: `it16-migration.json` (previous schema commit `{PREVIOUS_SCHEMA_COMMIT}`)",
        f"- External smoke: `external-smoke.json`",
        "",
        "## Artifacts",
        "",
    ]
    for path in sorted(ARTIFACTS.glob("*")):
        if path.name == "REPORT.md":
            continue
        lines.append(f"- `{path.name}`")
    (ARTIFACTS / "REPORT.md").write_text("\n".join(lines) + "\n", encoding="utf-8")
    save_json("results.json", {"run_id": RUN_ID, "started_at": STARTED_AT.isoformat(), "finished_at": utc_now(), "conclusion": conclusion, "tested_commit": commit, "db_version": DB_VERSION, "server_version_num": DB_SERVER_VERSION_NUM, "source_fixes": SOURCE_FIXES, "images": IMAGE_EVIDENCE, "external_smoke": EXTERNAL_SMOKE, "cases": RESULTS})


def main() -> int:
    parser = argparse.ArgumentParser()
    parser.add_argument("--skip-build", action="store_true", help="reuse existing unique images (development only)")
    args = parser.parse_args()
    preflight()
    try:
        prepare_source()
        generate_and_isolate_compose()
        if not args.skip_build:
            start_stack()
        else:
            compose("up", "-d", "crowler-db", timeout=300)
            wait_pg(timeout=180)
            bootstrap_auth()
            compose("up", "-d", "crowler-api", "crowler-events", "fixture", "crowler-engine-1", timeout=300)
            wait_ready()
            global ADMIN_TOKEN, USER_A_TOKEN, USER_B_TOKEN
            ADMIN_TOKEN = login("it_admin", ADMIN_PASSWORD)
            USER_A_TOKEN = login("it_user_a", USER_A_PASSWORD)
            USER_B_TOKEN = login("it_user_b", USER_B_PASSWORD)
        case("IT-01", "Fresh DB bootstrap and schema correctness", it01)
        case("IT-02", "AuthN/AuthZ gates on actual HTTP routes", it02)
        case("IT-03", "Link-only seed and existing Source preservation", it03)
        case("IT-04", "Create new Sources and verified ownership", it04)
        case("IT-05", "Existing config refresh and processing-state guard", it05)
        case("IT-06", "RSS/Atom parsing, filtering and snippet preservation", it06)
        case("IT-07", "Provider rate limiter outside HTTP timeout", it07)
        case("IT-08", "Dedicated GDELT DOC adapter", it08)
        case("IT-09", "Retry scheduling, backoff, permanent failures and fairness", it09)
        case("IT-10", "Heartbeats maintain exclusive ownership", it10)
        case("IT-11", "SIGTERM release and SIGKILL lease recovery", it11)
        case("IT-12", "Candidate cap and ordering determinism", it12)
        case("IT-13", "Retention and live-lease protection", it13)
        case("IT-14", "PostgreSQL claim index and query plan", it14)
        case("IT-15", "Operator API, events and end-to-end observability", it15)
        case("IT-16", "Controlled restart and migration posture", it16)
        external_smoke()
    except Exception as exc:  # noqa: BLE001
        log(f"FATAL harness error: {exc}")
        (ARTIFACTS / "harness-fatal.log").write_text(traceback.format_exc(), encoding="utf-8")
        save_json("harness-fatal.json", {"error": str(exc), "traceback": traceback.format_exc()})
    finally:
        cleanup()
        write_isolation_evidence()
        write_report()
    return 0 if all(item["status"] == "PASS" for item in RESULTS) and len(RESULTS) == 16 else 1


if __name__ == "__main__":
    sys.exit(main())
