package config

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
)

const (
	MCPToolAliasPrefix   = "mcp_"
	MCPMaxToolAliasBytes = 64
)

// MCPToolAlias derives the stable, bounded model-facing tool name for one
// exact server/tool identity. The same implementation is used by the operator
// configuration validator and the MCP runtime.
func MCPToolAlias(serverID, rawTool string) string {
	serverID = strings.TrimSpace(serverID)
	sanitized := sanitizeMCPToolName(rawTool)
	base := MCPToolAliasPrefix + serverID + "_" + sanitized
	if len(base) <= MCPMaxToolAliasBytes {
		return base
	}
	sum := sha256.Sum256([]byte(serverID + "\x00" + rawTool))
	suffix := hex.EncodeToString(sum[:])[:10]
	limit := MCPMaxToolAliasBytes - len(suffix) - 1
	if limit < len(MCPToolAliasPrefix)+len(serverID)+1 {
		limit = len(MCPToolAliasPrefix) + len(serverID) + 1
	}
	if limit > len(base) {
		limit = len(base)
	}
	return strings.TrimRight(base[:limit], "_-") + "_" + suffix
}

func sanitizeMCPToolName(raw string) string {
	raw = strings.TrimSpace(raw)
	var b strings.Builder
	for _, r := range raw {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '_', r == '-':
			b.WriteRune(r)
		default:
			b.WriteByte('_')
		}
	}
	out := strings.Trim(b.String(), "_-")
	if out == "" {
		out = "tool"
	}
	return out
}
