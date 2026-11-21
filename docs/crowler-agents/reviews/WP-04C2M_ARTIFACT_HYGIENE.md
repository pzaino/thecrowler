# WP-04C2M Artifact Hygiene and Source Provenance

## Tracked bytecode removal

Removed exactly these tracked generated files:

```
tests/integration/mcp-authorization/__pycache__/e2e_support.cpython-313.pyc
tests/integration/mcp-authorization/__pycache__/safety_tests.cpython-313.pyc
```

`git ls-files | grep -E '__pycache__|\.pyc$|\.pyo$'` returns no matches after
the change. No source `.py` file or fixture data was removed.

`.gitignore` now contains:

```
__pycache__/
*.py[cod]
```

## Source digest

`source_tree_digest` in `e2e_support.py` now excludes:

- `.git`, `bin`, `data`, `.env`, `config.yaml`, `artifacts`, `__pycache__`;
- files with `.pyc` or `.pyo` suffixes.

`run.sh` copies the source with matching tar exclusions:

```
--exclude=config.yaml --exclude=__pycache__ --exclude='*.pyc' --exclude='*.pyo'
```

The digest, source copy, image build context and runtime tree label therefore
use the same source representation.

## Deterministic tests

`tests/integration/mcp-authorization/test_provenance.py` covers:

| Test | Result |
| --- | --- |
| identical source has identical digest | PASS |
| adding `__pycache__`/`.pyc` does not change digest | PASS |
| changing a real fixture file changes digest | PASS |
| symlink records its target without following it | PASS |

Command:

```sh
python3 -m unittest discover -s tests/integration/mcp-authorization -p 'test_*.py' -v
```

Log: `/home/pzaino/tmp/opencode/wp04c2m-provenance.log` (recorded locally; the
test output is also reproduced in the test report).

## Image provenance

Run-scoped images remain labelled with:

- run ID;
- project;
- source SHA;
- source tree digest;
- role.

Runtime verification compares the running container's image ID with the image
ID recorded in `images.json`, then inspects that image's own source labels.
Compose service labels alone are not accepted as provenance.

The final local isolated E2E used source tree digest:

```
ded39c819c1c3494cf9bc85b5b297d57d6ba9a71ecfff2385991854154d2ecac
```

and source SHA:

```
3247de3d0a4a90b16d3fa6980bee92ea4695a709
```

All three image roles matched that tree digest. Stale/mismatched image reuse is
still rejected by the existing provenance check.

## Preserved boundaries

- No generic `crowler-api:latest` / `crowler-events:latest` fallback.
- Artifact redaction remains.
- Ownership-marker and exact-label cleanup remain.
- API/Events host ports remain dynamic loopback-only.
- PostgreSQL and fake models remain internal.

## Evidence

- Provenance unit tests: PASS
- Full isolated E2E after the digest change: 60 PASS, 0 FAIL/BLOCKED/SKIP
- Cleanup: PASS, no owned resources left
- Log: `/home/pzaino/tmp/opencode/wp04c2m-e2e-dind.log`
