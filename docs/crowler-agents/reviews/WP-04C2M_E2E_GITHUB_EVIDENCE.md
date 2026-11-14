# WP-04C2M GitHub E2E Evidence

## Workflow

`.github/workflows/mcp-authorization-e2e.yml`

- Trigger: `workflow_dispatch` only.
- Permissions: `contents: read`.
- Isolated dind runtime pinned by digest:
  `docker:dind@sha256:7bf70dcb85c822e3fa6f2d332143e474b99147b9d7b39102b0cfe8b04a7dd61b`
- Dind API is published on `127.0.0.1` with a dynamically allocated host port.
- Dind container has a unique name and a run label; cleanup inspects the label
  before removal.
- Workspace and runner temp are bind-mounted into dind so nested bind mounts
  resolve on the runner.
- `CROWLER_E2E_KEEP`, `CROWLER_E2E_STOP_AFTER` and
  `CROWLER_E2E_PREFLIGHT_ONLY` are forbidden in the workflow.
- Safety tests run before the full fixture.
- The structured result must be PASS, cleanup PASS, PASS count >= 50, no
  FAIL/BLOCKED/SKIP, and a final `fixture completed` check.
- Sanitized artifacts upload on `always()`.

## GitHub execution status

**Not run.**

No GitHub token or `gh` client is available in this environment, no commit or
push was authorized, and `workflow_dispatch` requires an authenticated
repository permission. The workflow was not triggered, and no GitHub E2E run
URL or artifact can be claimed.

## Local isolated evidence

The same fixture ran on a disposable Docker-in-Docker daemon locally:

- log: `/home/pzaino/tmp/opencode/wp04c2m-e2e-dind.log`
- result:
  `/tmp/opencode/wp04c2m-e2e/b4fecdf563b8a982a7685d0168d563d6/results.json`

Result:

- 60 PASS
- 0 FAIL
- 0 BLOCKED
- 0 SKIP
- cleanup PASS
- no mutation fixture execution
- zero `MCPMutationDispatchAttempts`
- no dispatch/uncertain/reconciliation state
- source SHA `3247de3d`
- source tree `ded39c819c1c3494cf9bc85b5b297d57d6ba9a71ecfff2385991854154d2ecac`
- API/Events/model image IDs matched that tree label

Harness safety suite on the same dind:

- 9/9 PASS
- log: `/home/pzaino/tmp/opencode/wp04c2m-safety-dind.log`

## Artifact inspection

The structured result and uploaded-log directory were scanned for:

- passwords, DSNs, bearer tokens, JWTs;
- raw `source_id` values and `fixture-resource`;
- external credentials.

No match was found. The result contains only counts, check names, image IDs,
non-secret labels and cleanup metadata.

## Exact manual action required

After the patch is reviewed and pushed by the maintainer:

1. open Actions → “MCP authorization E2E (opt-in)”;
2. choose `Run workflow` on the exact applied commit;
3. inspect the `isolated-e2e` job and its uploaded artifact
   `mcp-authorization-e2e-<run_id>`;
4. verify `results.json` conclusion PASS, 60+ checks, cleanup PASS, no
   FAIL/BLOCKED/SKIP;
5. record the GitHub run URL and artifact ID.

Do not add a push/PR trigger merely to obtain a check. The workflow stays opt-in.
