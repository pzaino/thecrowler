// Copyright 2026 Paolo Fabio Zaino
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

package mail

import mailconfig "github.com/pzaino/thecrowler/pkg/mail/config"

// Mode controls how mailbox changes trigger reconciliation.
type Mode = mailconfig.Mode

const RedactedValue = mailconfig.RedactedValue

const (
	// ModePoll discovers changes on a schedule.
	ModePoll = mailconfig.ModePoll
	// ModeListen uses provider notifications as reconciliation hints while
	// retaining polling as a safety net.
	ModeListen = mailconfig.ModeListen
)

// Provider-neutral source configuration types are aliases of the dependency-
// free configuration model. Keeping aliases here preserves the pkg/mail API
// while allowing project configuration packages to compose the same types.
type SourceConfig = mailconfig.SourceConfig
type ConnectorConfig = mailconfig.ConnectorConfig
type AuthConfig = mailconfig.AuthConfig
type MailboxConfig = mailconfig.MailboxConfig
type CrawlConfig = mailconfig.CrawlConfig
type ExtractionConfig = mailconfig.ExtractionConfig
type LinkPolicy = mailconfig.LinkPolicy
type AttachmentPolicy = mailconfig.AttachmentPolicy
type ListenerConfig = mailconfig.ListenerConfig
type ReconciliationConfig = mailconfig.ReconciliationConfig
type SafetyConfig = mailconfig.SafetyConfig
type Config = mailconfig.Config
type MailboxSelector = mailconfig.MailboxSelector
type TLSConfig = mailconfig.TLSConfig
type Limits = mailconfig.Limits

// DefaultSourceConfig returns the shared provider-neutral mail defaults.
func DefaultSourceConfig() SourceConfig {
	return mailconfig.DefaultSourceConfig()
}

// ValidateSourceConfig applies the shared provider-neutral mail validation
// rules.
func ValidateSourceConfig(config SourceConfig) error {
	return mailconfig.ValidateSourceConfig(config)
}
