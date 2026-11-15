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

    def test_host_endpoint_is_guarded_before_dind(self) -> None:
        run = self.step("Start disposable Docker daemon")["run"]
        for selector in ("DOCKER_HOST", "DOCKER_CONTEXT", "DOCKER_TLS_VERIFY", "DOCKER_CERT_PATH"):
            self.assertIn(selector, run)
        self.assertIn("host Docker endpoint selectors must be unset", run)

    def test_cleanup_uses_host_scoped_script(self) -> None:
        step = self.step("Remove disposable Docker daemon from host endpoint")
        self.assertEqual(step.get("if"), "always()")
        run = step["run"]
        self.assertIn('bash scripts/cleanup-e2e-dind.sh "$RUN_ID" "$DIND_NAME"', run)
        self.assertIn("cleanup.log", run)
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
