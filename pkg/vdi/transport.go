package vdi

import (
	"context"
	"net/http"
	"strings"
	"time"
)

type seleniumTransport struct {
	base                   http.RoundTripper
	sessionCreationTimeout time.Duration
}

func (t *seleniumTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if isSeleniumSessionCreation(req) {
		ctx, cancel := context.WithTimeout(
			req.Context(),
			t.sessionCreationTimeout,
		)
		defer cancel()

		req = req.Clone(ctx)
	}

	return t.base.RoundTrip(req)
}

func isSeleniumSessionCreation(req *http.Request) bool {
	if req == nil || req.URL == nil {
		return false
	}

	if req.Method != http.MethodPost {
		return false
	}

	return strings.HasSuffix(
		strings.TrimRight(req.URL.Path, "/"),
		"/session",
	)
}
