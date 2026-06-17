package crawler

import (
	"encoding/json"
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
