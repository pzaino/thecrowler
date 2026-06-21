package crawler

import "testing"

func TestScraperRuntimeAdapterParamsWithPageInfoSeparatesXHRFromJSONData(t *testing.T) {
	pageInfo := &PageInfo{ScrapedData: []ScrapedItem{
		{"title": "collected"},
		{"xhr": []map[string]interface{}{{"url": "https://example.test/api"}}},
	}}
	adapter := &scraperRuntimeAdapter{pageInfo: pageInfo}

	params := adapter.paramsWithPageInfo(
		map[string]interface{}{"json_data": map[string]interface{}{"existing": true, "xhr": []interface{}{map[string]interface{}{"url": "https://example.test/param"}}}},
		[]byte(`{"rule":"value","xhr":[{"url":"https://example.test/rule"}]}`),
	)
	jsonData, ok := params["json_data"].(map[string]interface{})
	if !ok {
		t.Fatalf("json_data type = %T, want map[string]interface{}", params["json_data"])
	}
	if jsonData["existing"] != true || jsonData["rule"] != "value" || jsonData["title"] != "collected" {
		t.Fatalf("json_data did not preserve existing/rule/collected values: %#v", jsonData)
	}
	if _, exists := jsonData["xhr"]; exists {
		t.Fatalf("json_data should not contain xhr: %#v", jsonData["xhr"])
	}
	xhr, ok := params["xhr"].([]interface{})
	if !ok || len(xhr) != 3 {
		t.Fatalf("xhr = %#v, want collected, parameter, and rule XHR entries", params["xhr"])
	}
	wantURLs := []string{"https://example.test/api", "https://example.test/param", "https://example.test/rule"}
	for i, want := range wantURLs {
		entry, ok := xhr[i].(map[string]interface{})
		if !ok || entry["url"] != want {
			t.Fatalf("xhr[%d] = %#v, want URL %q", i, xhr[i], want)
		}
	}
}
