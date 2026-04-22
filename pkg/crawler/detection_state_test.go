package crawler

import (
	"testing"

	cmn "github.com/pzaino/thecrowler/pkg/common"
	cdb "github.com/pzaino/thecrowler/pkg/database"
	detect "github.com/pzaino/thecrowler/pkg/detection"
)

func TestPublishDetectionResultsStoresKVEntries(t *testing.T) {
	cmn.KVStore = cmn.NewKeyValueStore()
	ctx := &ProcessContext{
		SelID: 7,
		source: &cdb.Source{
			ID:  42,
			URL: "https://example.com",
		},
	}
	defer cmn.KVStore.DeleteByCID(ctx.GetContextID(), true)

	detected := map[string]detect.DetectedEntity{
		"WordPress": {
			EntityName: "WordPress",
			EntityType: "html",
			Confidence: 87.5,
		},
	}

	publishDetectionResults(ctx, "https://example.com/blog", &detected)

	value, _, err := cmn.KVStore.Get("detection.detected_tech", ctx.GetContextID())
	if err != nil {
		t.Fatalf("expected detected tech in KVStore, got error: %v", err)
	}
	if _, ok := value.(map[string]detect.DetectedEntity); !ok {
		t.Fatalf("unexpected detected_tech value type: %T", value)
	}

	_, _, err = cmn.KVStore.Get("detection.tech.wordpress.confidence", ctx.GetContextID())
	if err != nil {
		t.Fatalf("expected confidence key in KVStore, got error: %v", err)
	}
}

func TestCheckEnvironmentCondition(t *testing.T) {
	cmn.KVStore = cmn.NewKeyValueStore()
	ctx := &ProcessContext{
		SelID: 11,
		source: &cdb.Source{
			ID:  99,
			URL: "https://example.org",
		},
	}
	defer cmn.KVStore.DeleteByCID(ctx.GetContextID(), true)

	props := cmn.NewKVStoreProperty(true, false, true, false, ctx.source.URL, ctx.GetContextID(), "")
	_ = cmn.KVStore.Set("detection.detected_tech_names", []string{"WordPress", "NGINX"}, props)
	_ = cmn.KVStore.Set("detection.target_url", "https://example.org", props)

	if !checkEnvironmentCondition(ctx, "detection.target_url") {
		t.Fatal("expected env key existence condition to pass")
	}

	if !checkEnvironmentCondition(ctx, map[string]interface{}{
		"detection.detected_tech_names": []interface{}{"WordPress"},
	}) {
		t.Fatal("expected env list membership condition to pass")
	}

	if checkEnvironmentCondition(ctx, map[string]interface{}{
		"detection.detected_tech_names": []interface{}{"Drupal"},
	}) {
		t.Fatal("expected env list membership condition to fail")
	}
}
