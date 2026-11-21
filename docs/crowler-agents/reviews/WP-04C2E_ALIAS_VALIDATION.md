# WP-04C2E Alias and Server-Identity Validation

## Production identity grammar

`pkg/agent/mcp_config.go` defines:

```go
mcpServerIDPattern = regexp.MustCompile(`^[a-z][a-z0-9-]{0,31}$`)
mcpMaxRawToolLen   = 128
mcpToolPrefix      = "mcp_"
maxToolNameLen     = 64
```

A server ID must:

- start with a lowercase ASCII letter;
- contain only lowercase letters, digits and hyphens;
- be at most 32 characters.

There is no underscore form. `approved-server` is valid; `approved_server` is
not. Production normalization trims whitespace and rejects IDs outside the
pattern. Tool names are trimmed, must be non-empty, at most 128 bytes, free of
control characters, and unique within a server.

The internal mutation policy now applies the same server-ID pattern and the
same tool-name length/control checks. This avoids a second, weaker grammar:
agreement with `MCPToolAlias` alone is not enough, because the alias function
will happily derive an alias from an inventory-invalid ID.

## Alias derivation

`MCPToolAlias(serverID, rawTool)`:

1. Trims the server ID.
2. Sanitizes the raw tool name: letters, digits, `_` and `-` are kept; every
   other rune becomes `_`; leading/trailing `_-` are trimmed; an empty result
   becomes `tool`.
3. Forms `mcp_` + server ID + `_` + sanitized tool.
4. If the result is at most 64 characters, returns it unchanged. Otherwise it
   truncates to leave room for `_` plus the first 10 hex characters of
   `SHA-256(serverID + "\x00" + rawTool)`, producing a deterministic, bounded
   alias.

The alias pattern (`^[A-Za-z0-9][A-Za-z0-9_-]{0,63}$`) permits the hyphen that
remains in a valid server ID.

### Documented example

```text
MCPToolAlias("approved-server", "approved_tool")
= mcp_approved-server_approved_tool
```

This exact value replaced the invalid `approved_server` examples in:

- `docs/crowler-agents/reviews/WP-04C2D_POLICY_CONTRACT.md`;
- `docs/crowler-agents/reviews/WP-04C2C_AUTHORIZATION_MODEL.md`;
- `docs/crowler-agents/mcp-mutation-authorization.md`.

## Collision and truncation behavior

- Duplicate raw tool names in one server are rejected during normalization.
- `normalizeMCPOperatorConfig` collects aliases across every server and rejects
  an alias that maps to a different server/tool pair.
- Truncated aliases include a hash of the full server/tool identity, so two
  long names do not deterministically collapse to the same prefix. If a
  collision still occurs, the global alias map rejects it rather than binding
  a proposal to the wrong descriptor.
- `MCPMutationPolicy.Validate` rejects duplicate operation aliases and requires
  each operation's `tool_alias` to equal `MCPToolAlias(server_id, tool_name)`
  exactly.
- `NewMCPMutationProposalTool` still selects a descriptor by exact alias and
  policy operation; registration remains test-only and no production path was
  added.

## Tests

- `TestDocumentedMutationInventoryUsesNormalizedServerIdentity` builds a real
  `cfg.MCPServerConfig` for `approved-server` with an `effect: mutation`,
  `allow_mutation_proposal: true` tool. It runs the actual
  `normalizeMCPOperatorConfig`, derives the alias with `MCPToolAlias`, validates
  the matching internal policy, and proves `approved_server` is rejected by
  `normalizeMCPServerConfig`.
- `TestDocumentedMCPMutationPolicyAliasAndValidation` now uses
  `approved-server` and the exact derived alias.
- `TestMCPToolAliasIsStableAndBounded` covers the deterministic 64-character
  truncation.
- `TestMCPOperatorConfigValidation` covers an alias collision (`a.b` versus
  `a_b`).
- `TestMCPMutationPolicyFailsClosed` now includes an invalid server ID with a
  *recomputed* matching alias, plus a tool name with a control character. These
  cases prove the grammar checks are real and not incidental alias mismatches.

No public policy field or loader was added; the internal validator change is a
Go-only hardening that is safe before B1.
