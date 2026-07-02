// @name: tyrell_source_config
// @description: Adds per-candidate Source configuration for the Tyrell Information Seed example.
// @type: engine_plugin
// @event_type: information_seed_candidate
// @version: 1.0.0

var candidate = params.candidate || {};
var defaults = params.source_defaults || {};
var candidateURL = String(candidate.url || candidate.URL || "");
var candidateHost = String(candidate.host || candidate.Host || "").toLowerCase();
var candidateTitle = String(candidate.title || candidate.Title || candidateHost || candidateURL);

var accepted = candidateURL.indexOf("https://") === 0;
var result = {
  accepted: accepted,
  score: accepted ? Number(candidate.score || candidate.Score || 0.5) : 0.0,
  reason: accepted ? "candidate has per-source crawl configuration" : "candidate is not an HTTPS URL",
  tags: accepted ? ["source-configured"] : [],
  metadata: {
    configured_site: accepted ? candidateURL : ""
  }
};

if (accepted) {
  result.source_overrides = {
    name: defaults.name || candidateTitle,
    priority: defaults.priority || "normal",
    restricted: defaults.restricted || 1,
    flags: defaults.flags || 0,
    source_config: {
      version: "1.0",
      format_version: "1.0",
      source_name: candidateTitle,
      crawling_config: {
        site: candidateURL,
        source_type: "website"
      },
      custom: {
        created_by: "information_seed",
        seed_label: "tyrell-corporation",
        discovery_host: candidateHost
      }
    }
  };
}
