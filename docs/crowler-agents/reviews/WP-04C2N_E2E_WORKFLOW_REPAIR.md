# WP-04C2N E2E Workflow Repair

## Defect

`.github/workflows/mcp-authorization-e2e.yml` previously executed:

```bash
dind_image="docker:dind@sha256:..."
docker pull "" >/dev/null
```

Under `set -euo pipefail`, the empty image name fails immediately after image
selection. The later `docker run` never executes.

## Fix

The start step now:

1. rejects pre-set `DOCKER_HOST`, `DOCKER_CONTEXT`, `DOCKER_TLS_VERIFY` and
   `DOCKER_CERT_PATH`, ensuring the outer container is created on the host
   daemon;
2. logs the host Docker context and server version;
3. pulls the exact digest-qualified dind image;
4. inspects its image ID before launch;
5. launches the same `"$dind_image"` reference;
6. inspects the running container's image ID and fails if it differs;
7. records `dind-image.json` and `dind-container.json` in the artifact root;
8. binds dind only to a dynamic `127.0.0.1` port;
9. exports the nested `DOCKER_HOST` only after the image identity is verified.

No mutable `docker:dind` tag fallback exists. A missing or unresolvable digest
fails closed.

## Static regression

`tests/integration/mcp-authorization/test_workflow.py` asserts:

- the trigger is exactly `workflow_dispatch`;
- permissions are `contents: read`;
- the digest-qualified dind reference is nonempty and 64-hex SHA-256;
- `docker pull`, `docker image inspect` and `docker run` all use it;
- `docker pull ""` is absent;
- the image ID is compared before launch;
- the cleanup step uses the host-scoped script;
- upload runs after cleanup and on `always()`;
- no broad `rm`, `prune` or `down -v` appears in cleanup.

Result: 5/5 workflow tests PASS.

## Actionlint

```sh
go run github.com/rhysd/actionlint/cmd/actionlint@v1.7.7 \
  .github/workflows/mcp-authorization-e2e.yml \
  .github/workflows/postgresql-integration.yml
```

No findings. Log: `/home/pzaino/tmp/opencode/wp04c2n-actionlint.log`.

## Workflow trigger

The workflow remains manual-only:

```yaml
on:
  workflow_dispatch:
permissions:
  contents: read
```

No push/PR trigger was added.

## GitHub dispatch status

**PENDING.** The workflow is not present on the repository default branch
`main`, so GitHub Actions does not register the `workflow_dispatch` workflow.
The GitHub workflow list contains no `mcp-authorization-e2e.yml` entry.

Required maintainer action: make the reviewed workflow available on the default
branch through the normal review/merge process, then dispatch it manually on the
exact applied SHA. Do not change the default branch or add a push trigger merely
to obtain a check.
