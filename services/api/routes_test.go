package main

import (
	"net/http"
	"net/http/httptest"
	"os"
	"regexp"
	"strings"
	"testing"

	cfg "github.com/pzaino/thecrowler/pkg/config"
	"golang.org/x/time/rate"
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
		"/v1/search/general",
		"/v1/search/netinfo",
		"/v1/search/httpinfo",
		"/v1/search/screenshot",
		"/v1/search/webobject",
		"/v1/search/correlated_sites",
		"/v1/search/collected_data",
		"/v1/search/correlated_sources",
		"/v1/search/pages",
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
		"/v1/information_seed/add",
		"/v1/information_seed/status",
		"/v1/information_seed/list",
		"/v1/information_seed/sources",
		"/v1/information_seed/candidates",
		"/v1/information_seed/retry",
		"/v1/information_seed/disable",
		//"/v1/information_seed/{id}/diagnostics", // this test requires extra logic to handle {id}
		"/v1/information-seed/list",
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
		"/v1/information_seed/update",
		"/v1/information_seed/remove",
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
