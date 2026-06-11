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

import (
	"context"
	"errors"
	"fmt"

	cdb "github.com/pzaino/thecrowler/pkg/database"
	mail "github.com/pzaino/thecrowler/pkg/mail"
)

// ErrEmailCrawlingNotConfigured identifies email sources that cannot be
// dispatched because their provider-neutral configuration or handler is absent.
var ErrEmailCrawlingNotConfigured = errors.New("email crawling not configured")

// EmailCrawlingNotConfiguredError describes the missing email crawl input.
type EmailCrawlingNotConfiguredError struct {
	Missing string
}

// Error implements error.
func (err *EmailCrawlingNotConfiguredError) Error() string {
	if err == nil || err.Missing == "" {
		return ErrEmailCrawlingNotConfigured.Error()
	}
	return fmt.Sprintf("%s: missing %s", ErrEmailCrawlingNotConfigured, err.Missing)
}

// Is lets callers use errors.Is with ErrEmailCrawlingNotConfigured.
func (err *EmailCrawlingNotConfiguredError) Is(target error) bool {
	return target == ErrEmailCrawlingNotConfigured
}

// EmailCrawlRequest contains only provider-neutral inputs required by an
// injected email handler. Provider selection and connector construction belong
// outside package crawler.
type EmailCrawlRequest struct {
	Source cdb.Source
	Config mail.SourceConfig
}

// EmailCrawlHandler is the provider-neutral email crawling dependency.
type EmailCrawlHandler interface {
	CrawlEmail(ctx context.Context, request EmailCrawlRequest) error
}

func crawlEmail(ctx context.Context, args *Pars) error {
	if args == nil || args.EmailConfig == nil {
		return &EmailCrawlingNotConfiguredError{Missing: "mail configuration"}
	}
	if args.EmailHandler == nil {
		return &EmailCrawlingNotConfiguredError{Missing: "email crawl dependencies"}
	}

	return args.EmailHandler.CrawlEmail(ctx, EmailCrawlRequest{
		Source: args.Src,
		Config: *args.EmailConfig,
	})
}
