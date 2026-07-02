// name: deterministic_candidate_processor
// description: Deterministic information-seed candidate processor fixture.
// type: engine_plugin
// version: 1.0.0

var candidate = params.candidate || {};
var host = String(candidate.host || candidate.Host || "").toLowerCase();
var score = Number(candidate.score || candidate.Score || 0);
var accepted = true;
var reason = "accepted by deterministic fixture";
var sourceOverrides = null;
var tags = ["deterministic-fixture"];
var metadata = {
  fixture_plugin: "deterministic_candidate_processor",
  input_host: host
};

if (host === "reject.example.test") {
  accepted = false;
  score = 0.01;
  reason = "rejected by deterministic fixture";
} else if (host === "accepted.example.test") {
  score = 0.91;
  reason = "accepted URL normalized and boosted";
} else if (host === "existing.example.test") {
  score = 0.88;
  reason = "existing source accepted for upsert/link";
} else if (host === "override.example.test") {
  score = 0.97;
  reason = "accepted with safe source overrides";
  sourceOverrides = {
    name: "Fixture Override Source",
    priority: "critical",
    restricted: 4,
    flags: 9,
    source_config: {
      version: "1.0",
      format_version: "1.0",
      source_name: "Fixture Override Source",
      crawling_config: {
        site: "https://override.example.test/discover",
        source_type: "website"
      },
      custom: {
        no_default_headers: true
      }
    }
  };
}

var result = {
  accepted: accepted,
  score: score,
  reason: reason,
  tags: tags,
  metadata: metadata
};

if (sourceOverrides !== null) {
  result.source_overrides = sourceOverrides;
}
