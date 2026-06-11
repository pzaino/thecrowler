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

package config

import (
	"encoding/json"

	mailconfig "github.com/pzaino/thecrowler/pkg/mail/config"
)

// EmailSourceConfig adapts mailconfig.SourceConfig to the project source
// configuration. Embedding keeps pkg/mail as the owner of the portable mail
// schema and its validation rules.
type EmailSourceConfig struct {
	mailconfig.SourceConfig `json:",inline" yaml:",inline"`
}

// MailSourceConfig is retained as a descriptive alias for callers that use the
// package name rather than the project's "email" source type terminology.
type MailSourceConfig = EmailSourceConfig

// DefaultEmailSourceConfig returns the defaults maintained by pkg/mail.
func DefaultEmailSourceConfig() EmailSourceConfig {
	return EmailSourceConfig{SourceConfig: mailconfig.DefaultSourceConfig()}
}

// DefaultMailSourceConfig is the mail-named equivalent of
// DefaultEmailSourceConfig.
func DefaultMailSourceConfig() MailSourceConfig {
	return DefaultEmailSourceConfig()
}

// Validate delegates validation to pkg/mail so project configuration cannot
// drift from the runtime mail configuration rules.
func (config EmailSourceConfig) Validate() error {
	return mailconfig.ValidateSourceConfig(config.SourceConfig)
}

// UnmarshalJSON applies mail defaults before overlaying explicitly supplied
// JSON fields.
func (config *EmailSourceConfig) UnmarshalJSON(data []byte) error {
	type sourceConfig mailconfig.SourceConfig
	decoded := sourceConfig(mailconfig.DefaultSourceConfig())
	if err := json.Unmarshal(data, &decoded); err != nil {
		return err
	}
	config.SourceConfig = mailconfig.SourceConfig(decoded)
	return nil
}

// UnmarshalYAML applies mail defaults before overlaying explicitly supplied
// YAML fields. This callback form is supported by both yaml.v2, used by
// pkg/config, and yaml.v3.
func (config *EmailSourceConfig) UnmarshalYAML(unmarshal func(any) error) error {
	type sourceConfig mailconfig.SourceConfig
	decoded := sourceConfig(mailconfig.DefaultSourceConfig())
	if err := unmarshal(&decoded); err != nil {
		return err
	}
	config.SourceConfig = mailconfig.SourceConfig(decoded)
	return nil
}

// mailSourceEnvelopes contains the historical names accepted for an email
// source configuration. New configurations are serialized using "email".
type mailSourceEnvelopes struct {
	Email       *EmailSourceConfig `json:"email" yaml:"email"`
	Mail        *EmailSourceConfig `json:"mail" yaml:"mail"`
	EmailConfig *EmailSourceConfig `json:"email_config" yaml:"email_config"`
	MailConfig  *EmailSourceConfig `json:"mail_config" yaml:"mail_config"`
	Custom      struct {
		Email       *EmailSourceConfig `json:"email" yaml:"email"`
		Mail        *EmailSourceConfig `json:"mail" yaml:"mail"`
		EmailConfig *EmailSourceConfig `json:"email_config" yaml:"email_config"`
		MailConfig  *EmailSourceConfig `json:"mail_config" yaml:"mail_config"`
	} `json:"custom" yaml:"custom"`
}

func (envelopes mailSourceEnvelopes) first() *EmailSourceConfig {
	for _, config := range []*EmailSourceConfig{
		envelopes.Email,
		envelopes.Mail,
		envelopes.EmailConfig,
		envelopes.MailConfig,
		envelopes.Custom.Email,
		envelopes.Custom.Mail,
		envelopes.Custom.EmailConfig,
		envelopes.Custom.MailConfig,
	} {
		if config != nil {
			return config
		}
	}
	return nil
}

// UnmarshalJSON preserves the existing source configuration shape while
// normalizing historical mail envelopes to the canonical Email field.
func (config *SourceConfig) UnmarshalJSON(data []byte) error {
	type sourceConfig SourceConfig
	var decoded sourceConfig
	if err := json.Unmarshal(data, &decoded); err != nil {
		return err
	}
	if decoded.Email == nil {
		var envelopes mailSourceEnvelopes
		if err := json.Unmarshal(data, &envelopes); err != nil {
			return err
		}
		decoded.Email = envelopes.first()
	}
	*config = SourceConfig(decoded)
	return nil
}

// UnmarshalYAML preserves existing source configuration composition while
// accepting the same historical mail envelopes as JSON decoding.
func (config *SourceConfig) UnmarshalYAML(unmarshal func(any) error) error {
	type sourceConfig SourceConfig
	var decoded sourceConfig
	if err := unmarshal(&decoded); err != nil {
		return err
	}
	if decoded.Email == nil {
		var envelopes mailSourceEnvelopes
		if err := unmarshal(&envelopes); err != nil {
			return err
		}
		decoded.Email = envelopes.first()
	}
	*config = SourceConfig(decoded)
	return nil
}
