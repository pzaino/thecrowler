# Release 2.3.0 RC3 sign-off request

This is a **decision request, not an approval**. It contains no recorded
approver. Only the maintainer can accept, reject or amend it.

Frozen SHA under review: `ef28d7672fe7134f4d277f673fe28a9d2f4424a3`
Release: `v2.3.0` · Schema: `1.17` · Writer: `verified-ownership-v1.17`

## 1. Scope acceptance

**A1.** Accept the 2.3.0 release scope as: local Ollama provider; synchronous
acceptance-driven goals; trusted read-only native tools; authorization-gated
native crawl scheduling; read-only stdio and Streamable HTTP MCP; record-only MCP
mutation proposals and approvals; operator-gated experimental fenced crawl
foundation; the existing crawler, rulesets, plugins, Information Seed, email,
time-series and API functionality.

- [ ] Accept
- [ ] Reject (state the required change)

**A2.** Confirm these are explicitly deferred and must not be described as
delivered: automatic asynchronous goal resumption; MCP mutation
execution/reconciliation; ownership-epoch DDL; fenced `null:` deletion;
production activation of `fenced` mode; the execution-profile simulator and any
external execution backend.

- [ ] Confirmed
- [ ] Change required

## 2. Residual defects needing a disposition

**B1 (P2-1) — `refresh_content` serialises page indexing.**
With `refresh_content: true`, all page indexing takes one advisory lock.

- [ ] Accept and document (recommended; correctness unaffected, default is off)
- [ ] Require a fix before tag

**B2 (P2-2) — legacy refresh failure still reports source `completed`.**
A non-critical page-indexing failure leaves `Sources.status='completed'` with
`last_error` populated. Data is correct and the old generation is preserved; the
status column alone is misleading. Changing it alters established legacy
terminal semantics.

- [ ] Accept and document (recommended; operator guidance is to treat a
      non-empty `last_error` as significant regardless of `status`)
- [ ] Authorize a separate narrow change to legacy terminal classification
- [ ] Require a fix before tag

**B3 (P3-1) — SQLite Events janitor uses `now()`.** Pre-existing, SQLite is not a
supported Events deployment in 2.3.0.

- [ ] Accept as documented limitation
- [ ] Require a fix before tag

**B4 (P3-2/P3-3) — cosmetic startup warnings and finite event TTL.**
- [ ] Accept as documented
- [ ] Require a change

## 3. Evidence gaps

The following were **not executed** in this environment. Decide whether any of
them blocks a *pre-release*.

**C1. Live Ollama inference and tool calling.** No Ollama endpoint or
tool-capable model was configured. Hermetic provider, capability, serialisation
and tool-round-trip tests pass; live connectivity is unverified.

- [ ] Does not block pre-release (hermetic coverage + opt-in live test documented)
- [ ] Blocks pre-release; environment will be provided

**C2. Live external MCP endpoint.** Only local stdio and local TLS Streamable
HTTP fixtures were used.

- [ ] Does not block pre-release
- [ ] Blocks pre-release

**C3. MySQL runtime.** MySQL remains legacy-only by contract; no server was
available. Static/portable checks only.

- [ ] Does not block pre-release
- [ ] Blocks pre-release

**C4. `mcp-authorization-e2e` workflow.** `workflow_dispatch`-only and not
registered on the default branch, so it cannot be dispatched for this SHA.

- [ ] Accept as a named hosted-gate limitation
- [ ] Blocks pre-release

## 4. Authorization boundaries

**D1.** Confirm this work package did not authorize, and does not constitute, a
tag, publish, merge, push, production deployment or migration of a persistent
database. RC3 produced a reviewable patch only.

- [ ] Confirmed

**D2.** Confirm no feature expansion is authorized by RC3: no new schema, no
async resume worker, no MCP mutation dispatcher, no public API, no default-mode
change, no production activation.

- [ ] Confirmed

## 5. Release decision

**E1. Which decision is being recorded?**

- [ ] **GO FOR PRE-RELEASE TESTING** — proceed to the maintainer-run
      pre-release procedure on `ef28d767` (or on the RC3 docs-only tree), then
      decide tagging separately.
- [ ] **GO TO TAG / PUBLISH** — only if exact-SHA hosted CI is green (it is, on
      `ef28d767`: 8/8) **and** any gap marked blocking in section 3 has been
      resolved or explicitly waived.
- [ ] **NO-GO** — state the blocking condition.

**E2. If GO, the exact commit to be tagged must be confirmed.** RC3 adds
documentation only. Two options:

- [ ] Tag the code SHA `ef28d767` (hosted CI verified on exactly this commit)
      and accept that the RC3 review documents are not part of the tagged tree.
- [ ] Apply the RC3 docs, then re-run hosted CI on the resulting SHA before
      tagging.

**E3. Rollback plan acknowledged?** `dbDeploy -disable` for the additive
capability marker; `set_source_crawl_claim_mode('legacy')` for claim mode;
`refresh_content: false` for refresh; image rollback for code. Table drops are
not a routine rollback.

- [ ] Acknowledged

## 6. Decision record

```
Approver:
UTC date:
Contract version: WP-REL230-RC3/1
A1:            accept / reject
A2:            confirmed / change required
B1:            accept / fix
B2:            accept / separate change / fix
B3:            accept / fix
B4:            accept / change
C1..C4:        non-blocking / blocking
D1, D2:        confirmed
E1:            GO FOR PRE-RELEASE TESTING / GO TO TAG / NO-GO
E2:            tag ef28d767 / re-run CI after docs
E3:            acknowledged
Notes:
```
