package common

import "testing"

func TestNormalizeURL(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "Lowercase",
			input:    "http://example.com/",
			expected: "http://example.com",
		},
		{
			name:     "TrimSpaces",
			input:    "  http://example.com/  ",
			expected: "http://example.com",
		},
		{
			name:     "TrimTrailingSlash",
			input:    "http://example.com/",
			expected: "http://example.com",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			res := NormalizeURL(test.input)
			if res != test.expected {
				t.Errorf("expected %q, got %q", test.expected, res)
			}
		})
	}
}

func TestIsURLValidEmailSources(t *testing.T) {
	tests := []struct {
		name  string
		url   string
		valid bool
	}{
		{name: "IMAP", url: "imap://mail.example.test:143/INBOX", valid: true},
		{name: "secure IMAP", url: "imaps://mail.example.test:993/Archive", valid: true},
		{name: "invalid IMAP", url: "imap://", valid: false},
		{name: "POP3", url: "pop3://mail.example.test:110", valid: true},
		{name: "secure POP3", url: "pop3s://mail.example.test:995", valid: true},
		{name: "invalid POP3", url: "pop3s://", valid: false},
		{name: "Gmail", url: "gmail://user@example.com", valid: true},
		{name: "invalid Gmail", url: "gmail://", valid: false},
		{name: "Graph Mail", url: "graph-mail://tenant/mailbox", valid: true},
		{name: "invalid Graph Mail", url: "graph-mail://", valid: false},
		{name: "Maildir", url: "maildir:///var/mail/user", valid: true},
		{name: "invalid Maildir", url: "maildir://relative/path", valid: false},
		{name: "Mbox", url: "mbox:///var/mail/user.mbox", valid: true},
		{name: "invalid Mbox", url: "mbox:///", valid: false},
		{name: "generic email", url: "email://mail.example.test", valid: true},
		{name: "invalid generic email", url: "email://", valid: false},
		{name: "embedded whitespace", url: "imap://mail.example.test/IN BOX", valid: false},
		{name: "unsupported email-like scheme", url: "smtp://mail.example.test", valid: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := IsURLValid(tt.url); got != tt.valid {
				t.Fatalf("IsURLValid(%q) = %t, want %t", tt.url, got, tt.valid)
			}
		})
	}
}
