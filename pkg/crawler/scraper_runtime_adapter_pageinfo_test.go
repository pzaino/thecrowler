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
	if _, ok := jsonData["scraped_data"].([]ScrapedItem); !ok {
		t.Fatalf("json_data scraped_data type = %T, want []ScrapedItem", jsonData["scraped_data"])
	}
	if xhr, ok := jsonData["xhr"].([]interface{}); !ok || len(xhr) != 1 {
		t.Fatalf("json_data xhr = %#v, want one XHR entry", jsonData["xhr"])
	}
	if xhr, ok := jsonData["XHR"].([]interface{}); !ok || len(xhr) != 1 {
		t.Fatalf("json_data XHR = %#v, want one XHR entry", jsonData["XHR"])
	}
	if params["page_info"] != pageInfo {
		t.Fatalf("page_info was not attached to params")
	}
}
