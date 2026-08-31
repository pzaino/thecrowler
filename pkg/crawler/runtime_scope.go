package crawler

// runtimeScope returns the canonical public rules scope for the source being
// processed. Source.GetSourceType owns the configuration lookup and its
// backwards-compatible website default; unsupported source types are treated
// as website rather than leaking an internal source classification into rules.
func runtimeScope(ctx *ProcessContext) string {
	if ctx == nil || ctx.source == nil {
		return "website"
	}
	switch scope := ctx.source.GetSourceType(); scope {
	case "website", "api", "file", "db", "data", "any":
		return scope
	default:
		return "website"
	}
}
