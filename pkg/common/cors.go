package common

import (
	"net/http"
	"strings"
)

const (
	corsAllowMethods = "GET, POST, OPTIONS"
	corsAllowHeaders = "Content-Type, Authorization, Accept"
	corsMaxAge       = "86400"
)

// CORSOptions controls CORS response header behavior for an HTTP service.
type CORSOptions struct {
	Enabled        bool
	AllowedOrigins []string
}

// CORSHeadersMiddleware returns a middleware that emits production-oriented CORS headers.
//
// When CORS is disabled, requests pass through without CORS headers. When enabled,
// origins must exactly match AllowedOrigins, unless AllowedOrigins contains "*".
func CORSHeadersMiddleware(options CORSOptions) func(http.Handler) http.Handler {
	allowedOrigins, allowAllOrigins := normalizeAllowedOrigins(options.AllowedOrigins)

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if !options.Enabled {
				next.ServeHTTP(w, r)
				return
			}

			origin := strings.TrimSpace(r.Header.Get("Origin"))
			if origin != "" {
				if allowAllOrigins {
					w.Header().Set("Access-Control-Allow-Origin", "*")
				} else if _, ok := allowedOrigins[origin]; ok {
					w.Header().Set("Access-Control-Allow-Origin", origin)
					appendVaryHeader(w.Header(), "Origin")
				}
			}

			w.Header().Set("Access-Control-Allow-Methods", corsAllowMethods)
			w.Header().Set("Access-Control-Allow-Headers", corsAllowHeaders)
			w.Header().Set("Access-Control-Max-Age", corsMaxAge)

			if r.Method == http.MethodOptions {
				w.WriteHeader(http.StatusNoContent)
				return
			}

			next.ServeHTTP(w, r)
		})
	}
}

func normalizeAllowedOrigins(origins []string) (map[string]struct{}, bool) {
	allowedOrigins := make(map[string]struct{}, len(origins))
	for _, origin := range origins {
		origin = strings.TrimSpace(origin)
		if origin == "" {
			continue
		}
		if origin == "*" {
			return nil, true
		}
		allowedOrigins[origin] = struct{}{}
	}
	return allowedOrigins, false
}

func appendVaryHeader(header http.Header, value string) {
	for _, existing := range strings.Split(header.Get("Vary"), ",") {
		if strings.EqualFold(strings.TrimSpace(existing), value) {
			return
		}
	}
	header.Add("Vary", value)
}
