// Package common package is used to store common functions and variables
package common

import (
	"context"
	"net/http"
	"strings"

	"google.golang.org/api/idtoken"
)

func NewAuthenticatedClient(ctx context.Context, audience string) (*http.Client, error) {
	return idtoken.NewClient(ctx, audience)
}

type OutboundAuthConfig struct {
	Type     string // none | bearer | gcp_id_token
	Audience string
	Header   string // optional, default Authorization
	Token    string // for static bearer only
}

func ApplyOutboundAuth(ctx context.Context, req *http.Request, cfg OutboundAuthConfig) error {
	authType := strings.ToLower(strings.TrimSpace(cfg.Type))
	if authType == "" || authType == "none" {
		return nil
	}

	header := strings.TrimSpace(cfg.Header)
	if header == "" {
		header = "Authorization"
	}

	switch authType {
	case "bearer":
		if strings.TrimSpace(cfg.Token) == "" {
			return nil
		}
		req.Header.Set(header, "Bearer "+cfg.Token)
		return nil

	case "gcp_id_token":
		if strings.TrimSpace(cfg.Audience) == "" {
			return nil
		}
		ts, err := idtoken.NewTokenSource(ctx, cfg.Audience)
		if err != nil {
			return err
		}
		tok, err := ts.Token()
		if err != nil {
			return err
		}
		req.Header.Set(header, "Bearer "+tok.AccessToken)
		return nil
	}

	return nil
}
