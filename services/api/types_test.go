package main

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestSourceConfigRequestAcceptsLegacyEmailEnvelope(t *testing.T) {
	payload := `{
		"url":"maildir:///var/mail/archive",
		"config":{
			"version":"1.0",
			"format_version":"1.0",
			"source_name":"archive",
			"crawling_config":{"site":"maildir:///var/mail/archive","source_type":"email"},
			"mail":{
				"connector":{"provider":"maildir","endpoint":"maildir:///var/mail/archive"},
				"auth":{"credential_ref":"secret/archive"},
				"extensions":{"password":"request-secret"}
			}
		}
	}`

	var request addSourceRequest
	if err := json.Unmarshal([]byte(payload), &request); err != nil {
		t.Fatalf("unmarshal add-source request: %v", err)
	}
	if request.Config.Email == nil {
		t.Fatal("legacy mail envelope did not populate config.email")
	}
	if got := request.Config.Email.Auth.CredentialRef; got != "secret/archive" {
		t.Fatalf("credential_ref = %q, want secret/archive", got)
	}
	if got := request.Config.Email.Extensions["password"]; got != "request-secret" {
		t.Fatalf("request secret was not preserved for storage: %#v", got)
	}

	encoded, err := json.Marshal(request)
	if err != nil {
		t.Fatalf("marshal add-source request: %v", err)
	}
	if !strings.Contains(string(encoded), `"email"`) || strings.Contains(string(encoded), `"mail"`) {
		t.Fatalf("request did not serialize with the canonical email envelope: %s", encoded)
	}
}

func TestSourceConfigResponseRedactsEmailSecrets(t *testing.T) {
	payload := `{
		"version":"1.0",
		"format_version":"1.0",
		"source_name":"archive",
		"crawling_config":{"site":"imaps://mail.example.test","source_type":"email"},
		"custom":{"password":"unrelated-source-value"},
		"email":{
			"connector":{"provider":"imap","endpoint":"imaps://mail.example.test"},
			"auth":{
				"credential_ref":"secret/archive",
				"identity":"reader@example.test",
				"extensions":{"access_token":"access-secret","empty_secret":""}
			},
			"extensions":{
				"password":"password-secret",
				"oauth_json":"oauth-secret",
				"provider":{"client_secret":"client-secret","private_key":"private-key"},
				"label":"archive"
			}
		}
	}`

	var request SourceConfigRequest
	if err := json.Unmarshal([]byte(payload), &request); err != nil {
		t.Fatalf("unmarshal source config: %v", err)
	}
	response := SourceConfigResponse(request)
	encoded, err := json.Marshal(response)
	if err != nil {
		t.Fatalf("marshal source config response: %v", err)
	}

	body := string(encoded)
	for _, secret := range []string{"secret/archive", "access-secret", "password-secret", "oauth-secret", "client-secret", "private-key"} {
		if strings.Contains(body, secret) {
			t.Fatalf("response leaked %q: %s", secret, body)
		}
	}
	for _, retained := range []string{"reader@example.test", "archive", "unrelated-source-value"} {
		if !strings.Contains(body, retained) {
			t.Fatalf("response removed non-secret value %q: %s", retained, body)
		}
	}
	if got := strings.Count(body, sourceConfigRedactionMarker); got != 6 {
		t.Fatalf("redaction marker count = %d, want 6: %s", got, body)
	}
	if request.Email.Extensions["password"] != "password-secret" {
		t.Fatalf("response serialization mutated request configuration: %#v", request.Email.Extensions)
	}
}

func TestStatusResponseSerializesRedactedEmailConfiguration(t *testing.T) {
	payload := `{
		"format_version":"1.0",
		"source_name":"archive",
		"crawling_config":{"site":"gmail://reader@example.test","source_type":"email"},
		"email":{
			"connector":{"provider":"gmail","endpoint":"gmail://reader@example.test"},
			"auth":{"credential_ref":"secret/gmail"},
			"extensions":{"refresh_token":"refresh-secret"}
		}
	}`
	var config SourceConfigRequest
	if err := json.Unmarshal([]byte(payload), &config); err != nil {
		t.Fatalf("unmarshal source config: %v", err)
	}

	encoded, err := json.Marshal(StatusResponse{
		Message: "All Sources status",
		Items: []StatusResponseRow{{
			SourceID: 42,
			Config:   SourceConfigResponse(config),
		}},
	})
	if err != nil {
		t.Fatalf("marshal status response: %v", err)
	}
	body := string(encoded)
	if strings.Contains(body, "refresh-secret") || strings.Contains(body, "secret/gmail") {
		t.Fatalf("status response leaked email secret or reference: %s", body)
	}
	if !strings.Contains(body, `"refresh_token":"[REDACTED]"`) || !strings.Contains(body, `"source_id":42`) {
		t.Fatalf("status response lost its compatible shape: %s", body)
	}
}
