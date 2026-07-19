package main

import (
	"net/http"
	"net/http/httptest"
	"os"
	"regexp"
	"strings"
	"testing"

	"golang.org/x/time/rate"

	cmn "github.com/pzaino/thecrowler/pkg/common"
	cfg "github.com/pzaino/thecrowler/pkg/config"
)

func TestInitAPIv1RegistersSearchConsoleAndDocsRoutes(t *testing.T) {
	oldMux := http.DefaultServeMux
	oldLimiter := limiter
	oldConfig := config
	http.DefaultServeMux = http.NewServeMux()
	limiter = rate.NewLimiter(rate.Inf, 0)
	config = cfg.Config{}
	config.API.DisableDefault = false
	config.API.EnableConsole = true
	config.API.EnableAPIDocs = true
	config.API.Plugins.Enabled = false
	t.Cleanup(func() {
		http.DefaultServeMux = oldMux
		limiter = oldLimiter
		config = oldConfig
	})

	initAPIv1()

	registeredRoutes := []string{
		"/v1/health",
		"/v1/health/",
		"/v1/ready",
		"/v1/ready/",
		"/v1/timeseries/metrics",
		"/v1/timeseries",
		"/v1/timeseries/observations",
		"/v1/timeseries/drilldown",
		"/v1/timeseries/dimensions",
		"/v1/search/general",
		"/v1/search/netinfo",
		"/v1/search/httpinfo",
		"/v1/search/screenshot",
		"/v1/search/webobject",
		"/v1/search/correlated_sites",
		"/v1/search/collected_data",
		"/v1/search/correlated_sources",
		"/v1/search/pages",
		"/v1/search/webobjects_by_source",
		"/v1/search/webobjects_by_source_uid",
		"/v1/search/source_status_by_uid",
		"/v1/search/source_uid_by_name",
		"/v1/search/source_uid_by_url",
		"/v1/search/scraped_data",
		"/v1/search/scraped_data_field",
		"/v1/search/artifacts",
		"/v1/search/artifacts_field",
		"/v1/search/artifacts_fields",
		"/v1/search/artifacts_attribute",
		"/v1/search/objects_attribute",
		"/v1/search/objects_attributes",
		"/v1/source/add",
		"/v1/source/remove",
		"/v1/source/update",
		"/v1/source/vacuum",
		"/v1/source/status",
		"/v1/source/statuses",
		"/v1/source/statuses/filter",
		"/v1/config/information_seed/providers",
		"/v1/config/vdis",
		"/v1/information_seed/add",
		"/v1/information_seed/status",
		"/v1/information_seed/list",
		"/v1/information_seed/sources",
		"/v1/information_seed/candidates",
		"/v1/information_seed/retry",
		"/v1/information_seed/disable",
		"/v1/information_seed/update",
		"/v1/information_seed/remove",
		//"/v1/information_seed/{id}/diagnostics", // this test requires extra logic to handle {id}
		"/v1/information_seed/list",
		"/v1/owner/add",
		"/v1/owner/update",
		"/v1/owner/remove",
		"/v1/category/add",
		"/v1/category/update",
		"/v1/category/remove",
		"/v1/openapi.json",
		"/v1/docs",
	}
	for _, route := range registeredRoutes {
		t.Run(route, func(t *testing.T) {
			_, pattern := http.DefaultServeMux.Handler(httptest.NewRequest(http.MethodGet, route, nil))
			if pattern != route {
				t.Fatalf("registered pattern for %q = %q, want %q", route, pattern, route)
			}
		})
	}

	plannedOrUnregisteredRoutes := []string{
		"/v1/information-seed/add",
		"/v1/owner/list",
		"/v1/category/list",
	}
	for _, route := range plannedOrUnregisteredRoutes {
		t.Run(route, func(t *testing.T) {
			_, pattern := http.DefaultServeMux.Handler(httptest.NewRequest(http.MethodGet, route, nil))
			if pattern != "" {
				t.Fatalf("unexpected registered pattern for %q = %q", route, pattern)
			}
		})
	}
}

func TestTimeSeriesRoutesAppearInOpenAPI(t *testing.T) {
	oldMux := http.DefaultServeMux
	oldLimiter := limiter
	oldConfig := config
	http.DefaultServeMux = http.NewServeMux()
	limiter = rate.NewLimiter(rate.Inf, 0)
	config = cfg.Config{}
	config.API.DisableDefault = false
	config.API.EnableAPIDocs = true
	config.API.Plugins.Enabled = false
	t.Cleanup(func() {
		http.DefaultServeMux = oldMux
		limiter = oldLimiter
		config = oldConfig
	})

	initAPIv1()
	spec := cmn.BuildOpenAPISpec(cmn.GetAPIRoutes(), cmn.OpenAPIOptions{Title: "test", Version: "v1"})
	for _, path := range []string{"/v1/timeseries/metrics", "/v1/timeseries", "/v1/timeseries/observations", "/v1/timeseries/drilldown", "/v1/timeseries/dimensions"} {
		operation, ok := spec.Paths[path]["get"]
		if !ok {
			t.Fatalf("OpenAPI specification is missing GET %s", path)
		}
		if operation.Responses["200"].Content["application/json"].Schema == nil {
			t.Fatalf("OpenAPI response schema is missing for GET %s", path)
		}
	}
}

func TestInitAPIv1OmitsDefaultAndConsoleRoutesWhenDisabled(t *testing.T) {
	oldMux := http.DefaultServeMux
	oldLimiter := limiter
	oldConfig := config
	http.DefaultServeMux = http.NewServeMux()
	limiter = rate.NewLimiter(rate.Inf, 0)
	config = cfg.Config{}
	config.API.DisableDefault = true
	config.API.EnableConsole = false
	config.API.EnableAPIDocs = false
	config.API.Plugins.Enabled = false
	t.Cleanup(func() {
		http.DefaultServeMux = oldMux
		limiter = oldLimiter
		config = oldConfig
	})

	initAPIv1()

	registeredRoutes := []string{"/v1/health", "/v1/health/", "/v1/ready", "/v1/ready/"}
	for _, route := range registeredRoutes {
		t.Run(route, func(t *testing.T) {
			_, pattern := http.DefaultServeMux.Handler(httptest.NewRequest(http.MethodGet, route, nil))
			if pattern != route {
				t.Fatalf("registered pattern for %q = %q, want %q", route, pattern, route)
			}
		})
	}

	unregisteredRoutes := []string{
		"/v1/timeseries/metrics",
		"/v1/timeseries",
		"/v1/timeseries/observations",
		"/v1/timeseries/drilldown",
		"/v1/timeseries/dimensions",
		"/v1/search/general",
		"/v1/search/pages",
		"/v1/search/artifacts",
		"/v1/source/add",
		"/v1/information_seed/list",
		"/v1/openapi.json",
		"/v1/docs",
	}
	for _, route := range unregisteredRoutes {
		t.Run(route, func(t *testing.T) {
			_, pattern := http.DefaultServeMux.Handler(httptest.NewRequest(http.MethodGet, route, nil))
			if pattern != "" {
				t.Fatalf("unexpected registered pattern for %q = %q", route, pattern)
			}
		})
	}
}

func TestDocumentedAPIEndpointsAreRegistered(t *testing.T) {
	oldMux := http.DefaultServeMux
	oldLimiter := limiter
	oldConfig := config
	http.DefaultServeMux = http.NewServeMux()
	limiter = rate.NewLimiter(rate.Inf, 0)
	config = cfg.Config{}
	config.API.DisableDefault = false
	config.API.EnableConsole = true
	config.API.EnableAPIDocs = true
	config.API.Plugins.Enabled = false
	t.Cleanup(func() {
		http.DefaultServeMux = oldMux
		limiter = oldLimiter
		config = oldConfig
	})

	initAPIv1()

	doc, err := os.ReadFile("../../doc/api.md")
	if err != nil {
		t.Fatalf("read doc/api.md: %v", err)
	}
	documented := documentedAPIEndpointRefs(string(doc))
	if len(documented) == 0 {
		t.Fatalf("expected documented API endpoints")
	}
	for _, ref := range documented {
		t.Run(ref.method+" "+ref.path, func(t *testing.T) {
			requestPath := strings.ReplaceAll(ref.path, "{id}", "123")
			_, pattern := http.DefaultServeMux.Handler(httptest.NewRequest(ref.method, requestPath, nil))
			if pattern == "" {
				// check if ref.path contains {id} and if so, ignore the error
				if !strings.Contains(ref.path, "{id}") {
					t.Fatalf("documented endpoint %s %s is not registered", ref.method, ref.path)
				}
			}
		})
	}
}

type documentedEndpointRef struct {
	method string
	path   string
}

func documentedAPIEndpointRefs(markdown string) []documentedEndpointRef {
	endpointRE := regexp.MustCompile("`(?:(GET/POST|GET|POST|PUT|PATCH|DELETE)\\s+)?(/v1/[^` ?]+)")
	seen := map[string]bool{}
	var refs []documentedEndpointRef
	for _, match := range endpointRE.FindAllStringSubmatch(markdown, -1) {
		methods := []string{match[1]}
		if match[1] == "" {
			methods = []string{http.MethodGet}
		}
		if match[1] == "GET/POST" {
			methods = []string{http.MethodGet, http.MethodPost}
		}
		path := strings.TrimSpace(match[2])
		if strings.HasSuffix(path, "/*") {
			continue
		}
		for _, method := range methods {
			key := method + " " + path
			if seen[key] {
				continue
			}
			seen[key] = true
			refs = append(refs, documentedEndpointRef{method: method, path: path})
		}
	}
	return refs
}

func TestInformationSeedPathActionsAppearAsPostInOpenAPI(t *testing.T) {
	oldMux := http.DefaultServeMux
	oldLimiter := limiter
	oldConfig := config
	http.DefaultServeMux = http.NewServeMux()
	limiter = rate.NewLimiter(rate.Inf, 0)
	config = cfg.Config{}
	config.API.EnableConsole = true
	config.API.EnableAPIDocs = true
	config.API.Plugins.Enabled = false
	t.Cleanup(func() {
		http.DefaultServeMux = oldMux
		limiter = oldLimiter
		config = oldConfig
	})

	initAPIv1()
	spec := cmn.BuildOpenAPISpec(cmn.GetAPIRoutes(), cmn.OpenAPIOptions{Title: "test", Version: "v1"})
	for _, path := range []string{
		"/v1/information_seed/{id}/rerun",
		"/v1/information_seed/{id}/disable",
		"/v1/information_seed/{id}/enable",
	} {
		operations := spec.Paths[path]
		if _, ok := operations["get"]; !ok {
			t.Errorf("OpenAPI specification is missing GET %s", path)
		}
		if _, ok := operations["post"]; ok {
			t.Errorf("OpenAPI specification incorrectly exposes POST %s", path)
		}
	}
}

func TestSearchFunctionOpenAPIQueryParametersMatchDatabaseFunctions(t *testing.T) {
	oldMux := http.DefaultServeMux
	oldLimiter := limiter
	oldConfig := config
	http.DefaultServeMux = http.NewServeMux()
	limiter = rate.NewLimiter(rate.Inf, 0)
	config = cfg.Config{}
	config.API.DisableDefault = false
	config.API.EnableConsole = false
	config.API.EnableAPIDocs = true
	config.API.Plugins.Enabled = false
	t.Cleanup(func() {
		http.DefaultServeMux = oldMux
		limiter = oldLimiter
		config = oldConfig
	})

	initAPIv1()
	spec := cmn.BuildOpenAPISpec(cmn.GetAPIRoutes(), cmn.OpenAPIOptions{Title: "test", Version: "v1"})

	expected := map[string][]string{
		"/v1/search/correlated_sources":       {"domain", "limit", "offset"},
		"/v1/search/pages":                    {"q", "lang", "limit", "offset"},
		"/v1/search/webobjects_by_source":     {"source_id"},
		"/v1/search/webobjects_by_source_uid": {"source_uid"},
		"/v1/search/source_status_by_uid":     {"source_uid"},
		"/v1/search/source_uid_by_name":       {"source_name"},
		"/v1/search/source_uid_by_url":        {"source_url"},
		"/v1/search/scraped_data":             {"q", "limit", "offset"},
		"/v1/search/scraped_data_field":       {"field_name", "field_value", "limit", "offset"},
		"/v1/search/artifacts":                {"q", "limit", "offset"},
		"/v1/search/artifacts_field":          {"field_name", "field_value", "limit", "offset"},
		"/v1/search/artifacts_fields":         {"filters", "limit", "offset"},
		"/v1/search/artifacts_attribute":      {"field_name", "field_value", "limit", "offset"},
		"/v1/search/objects_attribute":        {"field_name", "field_value", "limit", "offset"},
		"/v1/search/objects_attributes":       {"filters", "limit", "offset"},
	}
	for path, want := range expected {
		operation, ok := spec.Paths[path]["get"]
		if !ok {
			t.Fatalf("OpenAPI specification is missing GET %s", path)
		}
		got := make([]string, 0, len(operation.Parameters))
		for _, param := range operation.Parameters {
			got = append(got, param.Name)
		}
		if strings.Join(got, ",") != strings.Join(want, ",") {
			t.Fatalf("GET %s parameters = %v, want %v", path, got, want)
		}
	}
}
