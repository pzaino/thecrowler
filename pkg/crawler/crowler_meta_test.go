package crawler

import (
	"encoding/json"
	"reflect"
	"testing"

	cdb "github.com/pzaino/thecrowler/pkg/database"
)

func TestNewCrowlerMetaFromSourceExtractsMetaDataFromRawConfig(t *testing.T) {
	raw := json.RawMessage(`{
		"crawling_config":{"site":"https://www.instagram.com/"},
		"meta_data":{
			"URL":"https://www.instagram.com/iamstevent",
			"username":"iamstevent",
			"context":{"max_depth":4,"max_links":7},
			"push_port":5002
		}
	}`)
	source := &cdb.Source{Config: &raw}

	cm := NewCrowlerMetaFromSource(source, nil)
	md, ok := cm.GetSection(CrowlerMetaDataKey)
	if !ok {
		t.Fatalf("crowler_meta missing %q section: %#v", CrowlerMetaDataKey, cm)
	}
	if got := md["username"]; got != "iamstevent" {
		t.Fatalf("username metadata = %#v, want iamstevent; metadata=%#v", got, md)
	}
	if got := md["push_port"]; got != float64(5002) {
		t.Fatalf("push_port metadata = %#v, want 5002 decoded from JSON", got)
	}
}

func TestNewCrowlerMetaFromSourceExtractsMetaDataFromNestedConfigEnvelope(t *testing.T) {
	raw := json.RawMessage(`{"source_id":34,"config":{"meta_data":{"username":"iamstevent"}}}`)
	source := &cdb.Source{Config: &raw}

	cm := NewCrowlerMetaFromSource(source, nil)
	md, _ := cm.GetSection(CrowlerMetaDataKey)
	if got := md["username"]; got != "iamstevent" {
		t.Fatalf("username metadata from nested config envelope = %#v, want iamstevent", got)
	}
}

func TestEnsureCrowlerMetaBackfillsEmptyMetaDataSection(t *testing.T) {
	raw := json.RawMessage(`{"meta_data":{"username":"iamstevent","source":"config"}}`)
	source := &cdb.Source{Config: &raw}
	doc := map[string]interface{}{
		CrowlerMetaKey: map[string]interface{}{
			CrowlerMetaDataKey: map[string]interface{}{},
			"custom":           map[string]interface{}{"keep": true},
		},
	}

	cm := EnsureCrowlerMeta(doc, source, nil)
	md, _ := cm.GetSection(CrowlerMetaDataKey)
	if got := md["username"]; got != "iamstevent" {
		t.Fatalf("backfilled username metadata = %#v, want iamstevent; metadata=%#v", got, md)
	}
	if _, ok := cm["custom"]; !ok {
		t.Fatalf("EnsureCrowlerMeta removed existing custom section: %#v", cm)
	}
}

func TestEnsureCrowlerMetaPreservesExistingMetaDataOverrides(t *testing.T) {
	raw := json.RawMessage(`{"meta_data":{"username":"from-source","source":"config"}}`)
	source := &cdb.Source{Config: &raw}
	doc := map[string]interface{}{
		CrowlerMetaKey: map[string]interface{}{
			CrowlerMetaDataKey: map[string]interface{}{"username": "from-rule"},
		},
	}

	cm := EnsureCrowlerMeta(doc, source, nil)
	md, _ := cm.GetSection(CrowlerMetaDataKey)
	if got := md["username"]; got != "from-rule" {
		t.Fatalf("existing metadata override = %#v, want from-rule", got)
	}
	if got := md["source"]; got != "config" {
		t.Fatalf("source metadata = %#v, want config", got)
	}
}

func TestEnsureCrowlerMetaHandlesCrowlerMetaType(t *testing.T) {
	raw := json.RawMessage(`{"meta_data":{"username":"from-source","source":"config"}}`)
	source := &cdb.Source{Config: &raw}
	doc := map[string]interface{}{
		CrowlerMetaKey: CrowlerMeta{
			CrowlerMetaDataKey: map[string]interface{}{"username": "from-rule"},
			"custom":           map[string]interface{}{"keep": true},
		},
	}

	cm := EnsureCrowlerMeta(doc, source, nil)
	md, _ := cm.GetSection(CrowlerMetaDataKey)
	if got := md["username"]; got != "from-rule" {
		t.Fatalf("existing CrowlerMeta metadata override = %#v, want from-rule", got)
	}
	if got := md["source"]; got != "config" {
		t.Fatalf("source metadata = %#v, want config", got)
	}
	if _, ok := cm["custom"]; !ok {
		t.Fatalf("EnsureCrowlerMeta removed existing custom section from CrowlerMeta: %#v", cm)
	}
}

func TestEnsurePageCrowlerMetaBackfillsWorkerPageInfo(t *testing.T) {
	raw := json.RawMessage(`{"meta_data":{"username":"iamstevent"},"config":{"meta_data":{"ignored":"nested"}}}`)
	source := &cdb.Source{Config: &raw}
	srcCfg := map[string]interface{}{"meta_data": map[string]interface{}{"source": "runtime"}}
	pageInfo := &PageInfo{}

	cm := EnsurePageCrowlerMeta(pageInfo, source, srcCfg)
	md, ok := cm.GetSection(CrowlerMetaDataKey)
	if !ok {
		t.Fatalf("worker pageInfo crowler_meta missing %q section: %#v", CrowlerMetaDataKey, cm)
	}
	if got := md["username"]; got != "iamstevent" {
		t.Fatalf("worker pageInfo source metadata = %#v, want iamstevent; metadata=%#v", got, md)
	}
	if got := md["source"]; got != "runtime" {
		t.Fatalf("worker pageInfo runtime config metadata = %#v, want runtime; metadata=%#v", got, md)
	}
	if pageInfo.CrowlerMeta == nil {
		t.Fatalf("EnsurePageCrowlerMeta did not assign pageInfo.CrowlerMeta")
	}
}

func TestCrowlerMetaAddObjectTypeNormalizesAndDeduplicates(t *testing.T) {
	cm := NewCrowlerMeta(nil, nil)
	cm.AddObjectType(" Product ", "product", "News Article", "", "news   article")

	got := cm.ObjectTypes()
	want := []string{"product", "news_article"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("ObjectTypes() = %#v, want %#v", got, want)
	}
}

func TestEnsureCrowlerMetaNormalizesExistingObjectTypes(t *testing.T) {
	doc := map[string]interface{}{
		CrowlerMetaKey: map[string]interface{}{
			CrowlerMetaObjectTypeKey: []interface{}{" Product ", "product", 42, "News Article"},
		},
	}
	cm := EnsureCrowlerMeta(doc, nil, nil)
	got := cm.ObjectTypes()
	want := []string{"product", "news_article"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("ObjectTypes() = %#v, want %#v", got, want)
	}
}

func TestCrowlerMetaSetTagObjectTypeAppendsWithoutReset(t *testing.T) {
	cm := NewCrowlerMeta(nil, nil)
	cm.AddObjectType("product")
	if err := cm.SetTag("", CrowlerMetaObjectTypeKey, []interface{}{" News Article ", "product"}); err != nil {
		t.Fatalf("SetTag object_type returned error: %v", err)
	}
	if err := cm.SetTag(CrowlerMetaKey, CrowlerMetaObjectTypeKey, "Profile"); err != nil {
		t.Fatalf("SetTag crowler_meta.object_type returned error: %v", err)
	}

	got := cm.ObjectTypes()
	want := []string{"product", "news_article", "profile"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("ObjectTypes() = %#v, want %#v", got, want)
	}
}

func TestNewCrowlerMetaFromSourcePopulatesSourceUID(t *testing.T) {
	source := &cdb.Source{UID: "source-uid-1", Name: "Example", URL: "https://example.test"}

	cm := NewCrowlerMetaFromSource(source, nil)
	if got := cm[CrowlerMetaSourceUIDKey]; got != "source-uid-1" {
		t.Fatalf("source_uid = %#v, want source-uid-1; crowler_meta=%#v", got, cm)
	}
}

func TestEnsureCrowlerMetaBackfillsMissingSourceUID(t *testing.T) {
	source := &cdb.Source{UID: "source-uid-2", Name: "Example", URL: "https://example.test"}
	doc := map[string]interface{}{
		CrowlerMetaKey: map[string]interface{}{
			CrowlerMetaDataKey: map[string]interface{}{"username": "from-rule"},
		},
	}

	cm := EnsureCrowlerMeta(doc, source, nil)
	if got := cm[CrowlerMetaSourceUIDKey]; got != "source-uid-2" {
		t.Fatalf("backfilled source_uid = %#v, want source-uid-2; crowler_meta=%#v", got, cm)
	}
}

func TestEnsureCrowlerMetaReplacesEmptySourceUID(t *testing.T) {
	source := &cdb.Source{UID: "source-uid-3", Name: "Example", URL: "https://example.test"}
	doc := map[string]interface{}{
		CrowlerMetaKey: map[string]interface{}{
			CrowlerMetaSourceUIDKey: " ",
		},
	}

	cm := EnsureCrowlerMeta(doc, source, nil)
	if got := cm[CrowlerMetaSourceUIDKey]; got != "source-uid-3" {
		t.Fatalf("replaced source_uid = %#v, want source-uid-3; crowler_meta=%#v", got, cm)
	}
}

func TestEnsureCrowlerMetaDoesNotRecalculateSourceUIDFromURL(t *testing.T) {
	source := &cdb.Source{Name: "Example", URL: "https://example.test/current-page"}
	doc := map[string]interface{}{
		CrowlerMetaKey: map[string]interface{}{
			CrowlerMetaDataKey: map[string]interface{}{"username": "from-rule"},
		},
	}

	cm := EnsureCrowlerMeta(doc, source, nil)
	if got := cm[CrowlerMetaSourceUIDKey]; got != nil {
		t.Fatalf("source_uid = %#v, want nil when Sources.source_uid is not hydrated; crowler_meta=%#v", got, cm)
	}
}

func TestCrowlerMetaRejectsEmptySourceUIDOverride(t *testing.T) {
	cm := CrowlerMeta{CrowlerMetaSourceUIDKey: "source-uid-4"}
	if err := cm.SetTag("", CrowlerMetaSourceUIDKey, " "); err == nil {
		t.Fatal("SetTag allowed empty source_uid override")
	}
	if got := cm[CrowlerMetaSourceUIDKey]; got != "source-uid-4" {
		t.Fatalf("source_uid changed after rejected override: %#v", got)
	}
	if err := cm.DeleteTag(CrowlerMetaKey, CrowlerMetaSourceUIDKey); err == nil {
		t.Fatal("DeleteTag allowed source_uid deletion")
	}
}
