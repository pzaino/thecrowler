#!/usr/bin/env python3
"""Static regressions for the opt-in MCP authorization E2E workflow."""

from __future__ import annotations

import pathlib
import re
import unittest

import yaml

ROOT = pathlib.Path(__file__).resolve().parents[3]
WORKFLOW = ROOT / ".github" / "workflows" / "mcp-authorization-e2e.yml"


class WorkflowTests(unittest.TestCase):
    def load(self) -> dict:
        data = yaml.safe_load(WORKFLOW.read_text(encoding="utf-8"))
        # PyYAML resolves the unquoted GitHub key `on` as boolean true.
        return data

    def step(self, name: str) -> dict:
        data = self.load()
        for step in data["jobs"]["isolated-e2e"]["steps"]:
            if step.get("name") == name:
                return step
        raise AssertionError(f"workflow step not found: {name}")

    def test_manual_only_and_least_privilege(self) -> None:
        data = self.load()
        triggers = data.get(True, data.get("on"))
        if isinstance(triggers, str):
            self.assertEqual(triggers, "workflow_dispatch")
        else:
            self.assertEqual(set(triggers.keys()), {"workflow_dispatch"})
        self.assertEqual(data.get("permissions"), {"contents": "read"})

    def test_dind_pull_inspect_and_run_use_same_digest(self) -> None:
        run = self.step("Start disposable Docker daemon")["run"]
        match = re.search(r'dind_image="(docker:dind@sha256:[0-9a-f]{64})"', run)
        self.assertIsNotNone(match)
        image = match.group(1)
        self.assertIn('docker pull "$dind_image"', run)
        self.assertNotIn('docker pull ""', run)
        self.assertIn('docker image inspect "$dind_image"', run)
        self.assertIn('"$dind_image")', run)
        self.assertIn("image_id_before", run)
        self.assertIn("image_id_container", run)
        self.assertIn('"$image_id_before" != "$image_id_container"', run)
        self.assertNotRegex(run, r'docker run[^\n]*\n(?:[^\n]*\n){0,8}\s+docker:dind(?:\s|$)')

    def test_attempt_label_and_metadata_are_bound(self) -> None:
        step = self.step("Start disposable Docker daemon")
        run = step["run"]
        self.assertIn('org.crowler.e2e.attempt=${{ github.run_attempt }}', run)
        self.assertIn('"name":"%s"', run)
        self.assertIn('"run_attempt":"%s"', run)
        self.assertEqual(step.get("env", {}).get("RUN_ATTEMPT"), "${{ github.run_attempt }}")

    def test_cleanup_script_rejects_non_exact_ownership(self) -> None:
        script = (ROOT / "scripts" / "cleanup-e2e-dind.sh").read_text(encoding="utf-8")
        for needle in (
            'org.crowler.e2e.attempt',
            'actual_name',
            'actual_attempt',
            'expected_id',
            'recorded container ID',
            'preserving',
        ):
            self.assertIn(needle, script)
        for comparison in (
            'if [ "$actual_name" != "$dind_name" ]; then',
            'if [ "$actual_run" != "$run_id" ]; then',
            'if [ "$actual_attempt" != "$run_attempt" ]; then',
            'if [ -n "$expected_id" ] && [ "$actual_id" != "$expected_id" ]; then',
        ):
            self.assertIn(comparison, script)
        self.assertNotIn("docker system prune", script)
        self.assertNotIn("down -v", script)

    def test_cleanup_script_uses_host_daemon_only(self) -> None:
        script = (ROOT / "scripts" / "cleanup-e2e-dind.sh").read_text(encoding="utf-8")
        for selector in ("DOCKER_HOST", "DOCKER_CONTEXT", "DOCKER_TLS_VERIFY", "DOCKER_CERT_PATH"):
            self.assertIn(f"-u {selector}", script)
        self.assertIn('host_docker info', script)
        self.assertIn('host_docker context show', script)
        self.assertIn('unexpected host Docker context', script)
        self.assertIn('name=${name_pattern}', script)

    def test_fast_ci_runs_workflow_and_cleanup_tests(self) -> None:
        workflow = yaml.safe_load((ROOT / ".github" / "workflows" / "go.yml").read_text(encoding="utf-8"))
        steps = workflow["jobs"]["build"]["steps"]
        runs = "\n".join(step.get("run", "") for step in steps)
        self.assertIn("PyYAML==6.0.2", runs)
        self.assertIn("scripts/tests/cleanup-e2e-dind.sh", runs)
        self.assertIn("actionlint@v1.7.7", runs)
        self.assertIn("mcp-authorization-e2e.yml", runs)
        triggers = workflow.get(True, workflow.get("on"))
        self.assertIn("pull_request", triggers)
        self.assertIn("push", triggers)

    def test_host_endpoint_is_guarded_before_dind(self) -> None:
        run = self.step("Start disposable Docker daemon")["run"]
        for selector in ("DOCKER_HOST", "DOCKER_CONTEXT", "DOCKER_TLS_VERIFY", "DOCKER_CERT_PATH"):
            self.assertIn(selector, run)
        self.assertIn("host Docker endpoint selectors must be unset", run)
        self.assertIn('host_context="$(docker context show)"', run)
        self.assertIn('[ "$host_context" != "default" ]', run)
        self.assertIn("unexpected host Docker context", run)

    def test_cleanup_uses_host_scoped_script(self) -> None:
        step = self.step("Remove disposable Docker daemon from host endpoint")
        self.assertEqual(step.get("if"), "always()")
        run = step["run"]
        self.assertIn('bash scripts/cleanup-e2e-dind.sh "$RUN_ID" "$RUN_ATTEMPT" "$DIND_NAME" "$DIND_METADATA"', run)
        self.assertIn("cleanup.log", run)
        self.assertEqual(step.get("env", {}).get("RUN_ATTEMPT"), "${{ github.run_attempt }}")
        self.assertIn("DIND_METADATA", step.get("env", {}))
        self.assertNotIn("docker rm", run)
        self.assertNotIn("prune", run)
        self.assertNotIn("down -v", run)

    def test_upload_runs_after_cleanup(self) -> None:
        steps = self.load()["jobs"]["isolated-e2e"]["steps"]
        names = [step.get("name") for step in steps]
        self.assertLess(names.index("Remove disposable Docker daemon from host endpoint"),
                        names.index("Upload sanitized E2E evidence"))
        upload = steps[names.index("Upload sanitized E2E evidence")]
        self.assertEqual(upload.get("if"), "always()")


if __name__ == "__main__":
    unittest.main()
