# WP-04C2E Baseline

## Repository state

- Repository: `pzaino/thecrowler`
- Branch: `develop`
- Starting HEAD: `5846ef53bbcbf8172bdeca2e26a0476411a4d0c2`
- Working tree at start: clean (`git status --short` empty).
- Go: `go1.27.3 linux/amd64`.
- Node: `v24.22.1`.
- SQLite driver: `github.com/mattn/go-sqlite3 v1.14.52`.
- PostgreSQL CI engine: `postgres:17` (the dedicated MCP local simulation used
  the same image).
- Release: `v2.3.0`.
- Schema: `RequiredSchemaVersion = "1.17"`,
  `WriterGeneration = "verified-ownership-v1.17"`.

The isolated worktree is `/home/pzaino/tmp/opencode/thecrowler-wp04c2e`, cloned
from the original checkout and checked out at `5846ef53`. The original checkout
was clean before the work and is not used as an edit target.

## Commit boundaries

- `1c98d753` is the WP-04C2C commit.
- `0a53553f` is the applied WP-04C2D commit. It contains exactly the 15 files
  and 964 insertions / 26 deletions delivered in the previous work package,
  including the seven `WP-04C2D_*` reports.
- `5846ef53` is the unrelated Information Seed follow-up. It changes only
  `pkg/database/information_seed_*`, `source_upsert_policy*` and the new
  `tests/integration/infoseed-compose/` suite (15 files, 2,396 insertions,
  8 deletions). It does not touch MCP, config, schema, VDI or the WP-04C2D
  reports.

The WP-04C2E patch is therefore based on `5846ef53` and leaves the Information
Seed implementation and Compose suite untouched.

## WP-04C2D disposition

The WP-04C2D reports are present and were read. Gate A repairs landed:
deterministic VDI legacy-audio control, disposable-database PostgreSQL sentinel,
corrected documented alias, SQLite FK runtime evidence, and the gate decision
sheet.

The following WP-04C2D claims or omissions required WP-04C2E work:

1. `WP-04C2D_POLICY_CONTRACT.md` and the MCP authorization doc still used
   `approved_server`, which production normalization rejects. The WP-04C2D
   test covered only `MCPMutationPolicy.Validate`, not a real MCP inventory.
2. The policy contract claimed unknown fields would be rejected by the strict
   config decoder. The actual legacy parser silently ignores them.
3. The GitHub PostgreSQL workflow never supplied
   `CROWLER_MCP_POSTGRES_TEST_DISPOSABLE` or `CROWLER_MCP_POSTGRES_DSN`, so the
   MCP test skipped even when the broad workflow passed.
4. `docs/test_policy.md` still told maintainers to preserve a sentinel in the
   maintenance database, contradicting the corrected test.
5. The internal mutation-policy validator compared aliases but did not enforce
   the production server/tool grammar, so it could accept an operation the
   inventory could never name.

## Starting CI evidence

The reviewed baseline `5846ef53` has no WP-04C2E GitHub run. The prior
WP-04C2D reports recorded the VDI negative-control failure and its local fix;
they did not claim a fresh green GitHub run. WP-04C2E also did not push, so all
GitHub statements in these reports remain unverified until the maintainer
pushes the patch and observes the workflows.

Local baseline checks of the untouched `5846ef53` tree confirmed the reported
`approved_server` examples, the permissive loader paths, the unexercised MCP
CI test and the contradictory sentinel wording. Final post-change test results
are in `WP-04C2E_TEST_REPORT.md`.
