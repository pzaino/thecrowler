# WP-04C2D Baseline

## Repository state

- Repository: `pzaino/thecrowler`
- Branch: `develop`
- Starting HEAD: `1c98d753d298dde4c42fa0555584e8aa863475c4`
- Working tree at start: clean (`git status --short` empty).
- Go: `go1.27.3 linux/amd64`.
- Node: `v24.22.1` (also tested with Node 20.20.2 and 22.23.5 via disposable
  Docker containers).
- PostgreSQL test engine: PostgreSQL 15.10 in a disposable Docker container.
- SQLite: `mattn/go-sqlite3 v1.14.52` (repository dependency).
- Release: `v2.3.0`.
- Schema: `RequiredSchemaVersion = "1.17"`,
  `WriterGeneration = "verified-ownership-v1.17"`.

`1c98d753` contains the WP-04C2C patch (17 files, 3,111 insertions, 125
deletions) on top of `cc2461d4`. There are no unrelated working-tree changes.
The WP-04C2C review documents are tracked under
`docs/crowler-agents/reviews/`.

## Documentation availability

- `TESTING.md` is present locally but is gitignored by `.gitignore`; it is not
  part of the tracked GitHub tree.
- `docs/test_policy.md`, the MCP mutation/read-only/crawl documents and the
  GitHub Actions workflows are tracked.
- The master plan is available locally at
  `/home/pzaino/Documents/opencode/crowler-tasks/CROWler_Agentic_AI_OpenCode_Master_Plan_2026-11-05.md`.
- WP-04C2C review files are present in the tracked tree.

## Baseline observations

- The WP-04C2C durable store/API hardening is present.
- The production inert proposal producer remains unregistered.
- `MCPMutationPolicy` remains a Go-only contract; `mcp.mutation_policy` is not
  implemented in `pkg/config` or the public JSON schema.
- `ToolLoopConfig.Principal` and `goalRunConfig.Principal` exist, but production
  constructors do not populate them.
- The additive schema 1.17 MCP capability remains present but not
  deployment-approved.
- `MCPMutationDispatchAttempts` remains unused.

## Baseline failure evidence

The reviewed commit's GitHub `build` check failed at
`pkg/vdi/camouflage_node_test.go:864`:

```text
control failed: harness did NOT detect the legacy AudioBuffer
cumulative-mutation defect (audioRawIntact=true on legacy)
```

Local reproduction succeeded with the same source:

```sh
go test ./pkg/vdi -run '^TestCamouflageControlsDetectLegacyDefects$' -count=20
```

The negative control failed intermittently because the harness sample baseline
was `1.0`, where the legacy patch's `Math.random()*1e-7` increment is often
below float32 resolution. The VDI harness and test source are byte-identical
between the parent `cc2461d4` and `1c98d753`, so the defect predates WP-04C2C.

## Baseline test status

- The current working tree's focused suites (`pkg/database`, `pkg/agent`,
  `services/api`, `pkg/vdi`) pass after the WP-04C2D safe fixes.
- The VDI negative control passes repeatedly after the deterministic-baseline
  fix.
- PostgreSQL disposable integration passes after removing maintenance-database
  sentinel DDL.
- Full, race, vet and build results are recorded in
  `WP-04C2D_TEST_REPORT.md`.

## Implemented versus described

| Area | Baseline disposition |
| --- | --- |
| VDI negative control | Flaky; root cause proven and fixed |
| PostgreSQL test isolation | Unique DB; maintenance-sentinel DDL still present before fix |
| Policy example alias | Incorrect placeholder before fix |
| SQLite FK enforcement | Declared but runtime enforcement not proven; test added |
| Public mutation policy | Proposed only; not approved |
| Principal provenance | Fields exist; production propagation absent |
| Production proposal registration | Disabled |
| Dispatch/execution | Not implemented; prohibited |
