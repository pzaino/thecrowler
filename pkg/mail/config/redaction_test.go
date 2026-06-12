package config

import (
	"fmt"
	"reflect"
	"strings"
	"testing"
)

func TestRedactValueProtectsStructuredAuthenticationMaterial(t *testing.T) {
	t.Parallel()

	input := map[string]any{
		"password": "password-value",
		"oauth": map[string]any{
			"access_token":  "access-value",
			"refresh-token": "refresh-value",
			"client.secret": "client-value",
		},
		"headers": map[string]any{
			"Authorization": []any{"Bearer bearer-value"},
		},
		"credential_ref": "secret/mail/archive",
		"proxy_url":      "http://proxy-user:proxy-password@proxy.example.test:8080",
		"secret_ref":     "vault/mail/archive",
		"description":    "password=embedded-value authorization: Bearer embedded-token",
		"empty_password": "",
		"label":          "archive",
	}

	got := RedactValue(input).(map[string]any)
	encoded := fmt.Sprintf("%v", got)
	for _, secret := range []string{
		"password-value", "access-value", "refresh-value", "client-value",
		"bearer-value", "secret/mail/archive", "vault/mail/archive",
		"embedded-value", "embedded-token", "proxy-user", "proxy-password",
	} {
		if strings.Contains(encoded, secret) {
			t.Fatalf("structured redaction leaked %q: %s", secret, encoded)
		}
	}
	if got["label"] != "archive" || got["empty_password"] != "" {
		t.Fatalf("non-secret or empty values changed: %#v", got)
	}
	if !reflect.DeepEqual(input["password"], "password-value") {
		t.Fatalf("redaction mutated input: %#v", input)
	}
}

func TestRedactStringProtectsAuthenticationRepresentations(t *testing.T) {
	t.Parallel()

	input := strings.Join([]string{
		`password=hunter2`,
		`"access_token":"access-value"`,
		`refresh_token='refresh-value'`,
		`client-secret: client-value`,
		`Authorization: Bearer bearer-value`,
		`credential_ref=secret/mail/archive`,
		`secret-reference: vault/mail/archive`,
	}, "\n")
	got := RedactString(input)
	for _, secret := range []string{"hunter2", "access-value", "refresh-value", "client-value", "bearer-value", "secret/mail/archive", "vault/mail/archive"} {
		if strings.Contains(got, secret) {
			t.Fatalf("string redaction leaked %q: %s", secret, got)
		}
	}
	if count := strings.Count(got, RedactedValue); count != 7 {
		t.Fatalf("redaction marker count = %d, want 7: %s", count, got)
	}
}

func TestSourceConfigFormattingIsRedactedWithoutMutation(t *testing.T) {
	t.Parallel()

	source := DefaultSourceConfig()
	source.Auth.CredentialRef = "secret/mail/archive"
	source.Auth.Extensions = map[string]any{"access_token": "access-value"}
	source.Connector.Extensions = map[string]any{
		"password":      "password-value",
		"refresh_token": "refresh-value",
		"client_secret": "client-value",
		"headers":       map[string]any{"Authorization": "Bearer bearer-value"},
	}

	for _, formatted := range []string{
		fmt.Sprint(source),
		fmt.Sprintf("%+v", source),
		fmt.Sprintf("%#v", source),
	} {
		for _, secret := range []string{"secret/mail/archive", "access-value", "password-value", "refresh-value", "client-value", "bearer-value"} {
			if strings.Contains(formatted, secret) {
				t.Fatalf("formatted source config leaked %q: %s", secret, formatted)
			}
		}
		if !strings.Contains(formatted, RedactedValue) {
			t.Fatalf("formatted source config has no redaction marker: %s", formatted)
		}
	}
	if source.Auth.CredentialRef != "secret/mail/archive" || source.Connector.Extensions["password"] != "password-value" {
		t.Fatalf("formatting mutated source config: %#v", source)
	}
}
