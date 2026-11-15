# WP-04C2N CI and E2E Evidence

## Regular GitHub CI at reviewed HEAD

Eight checks passed on `d5314ce`:

- PostgreSQL integration: https://github.com/pzaino/thecrowler/actions/runs/43654921200/job/132631232007
- build: https://github.com/pzaino/thecrowler/actions/runs/43654921242/job/132631231978
- SQL parser CGO=0: https://github.com/pzaino/thecrowler/actions/runs/43654921206/job/132631232069
- SQL parser CGO=1: https://github.com/pzaino/thecrowler/actions/runs/43654921206/job/132631231856
- Static linux/amd64: https://github.com/pzaino/thecrowler/actions/runs/43654921206/job/132631232080
- Static linux/arm64: https://github.com/pzaino/thecrowler/actions/runs/43654921206/job/132631232030
- Static darwin/amd64: https://github.com/pzaino/thecrowler/actions/runs/43654921206/job/132631232188
- Static darwin/arm64: https://github.com/pzaino/thecrowler/actions/runs/43654921206/job/132631232153

PostgreSQL job steps 1–11 and 20–23 all succeeded, including client selection,
schema, grants, migrations, the six dedicated MCP tests and the broad integration
suite.

This is evidence for `d5314ce`, not for the WP-04C2N workflow patch. The patch
has not been pushed, and GitHub CI has not run it.

## Opt-in E2E workflow

**PENDING — workflow not registered.**

GitHub’s workflow list for the repository does not contain
`mcp-authorization-e2e.yml`. The file is on `develop`, but the repository
default branch is `main`, and `main` does not contain the file. Consequence:

- `workflow_dispatch` cannot be selected in the Actions UI;
- the workflow-runs API returns 404 for this workflow;
- no GitHub E2E run URL or artifact exists.

This is not a test failure; it is a repository/default-branch integration gate.
Required maintainer action:

1. merge the reviewed workflow to `main` through normal review;
2. dispatch it manually on the exact applied commit;
3. confirm the workflow first pulls/verifies the pinned dind digest;
4. inspect the safety and E2E artifacts;
5. record the GitHub run URL, head SHA, attempt and artifact ID.

Do not add a push/PR trigger or change the default branch just to force a check.

## Local E2E evidence

The final harness ran on a disposable Docker-in-Docker daemon:

- log: `/home/pzaino/tmp/opencode/wp04c2n-e2e.log`
- result:
  `/tmp/opencode/wp04c2n-e2e/48edce50ec854b9966b32d00bc3d4677/results.json`
- 60 PASS, 0 FAIL, 0 BLOCKED, 0 SKIP
- cleanup PASS
- zero mutation fixture calls
- zero dispatch attempts
- no dispatch/uncertain/reconciliation state
- source SHA `d5314ce2272c7136352e772b2e3b394b17ee319e`
- source tree `387bd2b31cb2f6c200d16fce39084576dbfc886dafb9b14391ab4208ef8b492c`
- API/Events/model image IDs matched that tree label

Safety suite on the same dind: 9/9 PASS
(`/home/pzaino/tmp/opencode/wp04c2n-safety.log`).

Artifact secret scan found no passwords, DSNs, JWTs, bearer tokens, raw
`source_id` values or `fixture-resource`.

## PostgreSQL local evidence

Six MCP tagged tests passed on PostgreSQL 17 and 15.10:

- log: `/home/pzaino/tmp/opencode/wp04c2n-pg.log`
- no SKIP
- cascade contention observed `delete=<nil>, lock=source mutation is forbidden`

The current GitHub PostgreSQL workflow remains green at `d5314ce` and its
version-selection script is unchanged by this task except for the workflow
already repaired at HEAD.
