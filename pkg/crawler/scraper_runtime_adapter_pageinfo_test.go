package crawler

import "testing"

func TestScraperRuntimeAdapterParamsWithPageInfoAddsXHRToJSONData(t *testing.T) {
	pageInfo := &PageInfo{ScrapedData: []ScrapedItem{{"xhr": []map[string]interface{}{{"url": "https://example.test/api"}}}}}
	adapter := &scraperRuntimeAdapter{pageInfo: pageInfo}

	params := adapter.paramsWithPageInfo(map[string]interface{}{"json_data": map[string]interface{}{"existing": true}}, []byte(`{"rule":"value"}`))
	jsonData, ok := params["json_data"].(map[string]interface{})
	if !ok {
		t.Fatalf("json_data type = %T, want map[string]interface{}", params["json_data"])
	}
	if jsonData["existing"] != true || jsonData["rule"] != "value" {
		t.Fatalf("json_data did not preserve existing/rule values: %#v", jsonData)
	}
	if xhr, ok := jsonData["xhr"].([]map[string]interface{}); !ok || len(xhr) != 1 {
		t.Fatalf("json_data xhr = %#v, want original XHR entry", jsonData["xhr"])
	}
	if _, exists := jsonData["XHR"]; exists {
		t.Fatalf("json_data should not duplicate xhr into XHR: %#v", jsonData["XHR"])
	}
}
