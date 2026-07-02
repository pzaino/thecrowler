// Copyright 2023 Paolo Fabio Zaino
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package crawler

import "testing"

func TestClassifySourceProtocol(t *testing.T) {
	tests := []struct {
		name   string
		rawURL string
		want   SourceProtocol
	}{
		{name: "http is web", rawURL: "http://example.com", want: SourceProtocolWeb},
		{name: "https is web", rawURL: "https://example.com", want: SourceProtocolWeb},
		{name: "ftp is web", rawURL: "ftp://example.com/file", want: SourceProtocolWeb},
		{name: "ftps is web", rawURL: "ftps://example.com/file", want: SourceProtocolWeb},
		{name: "web with surrounding whitespace", rawURL: " \t\nhttps://example.com/path\r\n ", want: SourceProtocolWeb},
		{name: "email is email", rawURL: "email://mail.example.com", want: SourceProtocolEmail},
		{name: "imap is email", rawURL: "imap://mail.example.com", want: SourceProtocolEmail},
		{name: "imaps is email", rawURL: "imaps://mail.example.com", want: SourceProtocolEmail},
		{name: "pop3 is email", rawURL: "pop3://mail.example.com", want: SourceProtocolEmail},
		{name: "pop3s is email", rawURL: "pop3s://mail.example.com", want: SourceProtocolEmail},
		{name: "gmail is email", rawURL: "gmail://user@example.com", want: SourceProtocolEmail},
		{name: "graph mail is email", rawURL: "graph-mail://tenant/mailbox", want: SourceProtocolEmail},
		{name: "maildir is email", rawURL: "maildir:///var/mail/user", want: SourceProtocolEmail},
		{name: "mbox is email", rawURL: "mbox:///var/mail/user.mbox", want: SourceProtocolEmail},
		{name: "email with surrounding whitespace", rawURL: " \tmaildir:///var/mail/user\n", want: SourceProtocolEmail},
		{name: "bare host falls back to network", rawURL: "example.com", want: SourceProtocolNetwork},
		{name: "empty string falls back to network", rawURL: "", want: SourceProtocolNetwork},
		{name: "whitespace falls back to network", rawURL: " \t\n\r ", want: SourceProtocolNetwork},
		{name: "unknown scheme falls back to network", rawURL: "s3://bucket/key", want: SourceProtocolNetwork},
		{name: "unknown string falls back to network", rawURL: "not-a-supported-protocol", want: SourceProtocolNetwork},
		{name: "unknown string with surrounding whitespace falls back to network", rawURL: " \tunknown://example.com\n", want: SourceProtocolNetwork},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := classifySourceProtocol(tt.rawURL); got != tt.want {
				t.Errorf("classifySourceProtocol(%q) = %q, want %q", tt.rawURL, got, tt.want)
			}
		})
	}
}
