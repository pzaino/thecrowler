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

package searchproviders

import (
	"context"
	"io"
	"net/http"
	"time"
)

func doPublicDocumentRequest(ctx context.Context, providerName string, client HTTPClient, endpoint, accept string, timeout time.Duration, rateLimit string, budget *requestBudget, maxBodyBytes int64, headers map[string]string) ([]byte, error) {
	if timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	}
	if client == nil {
		client = &http.Client{Timeout: timeout}
	}
	limiter := limiterFor(providerName, rateLimit)
	if err := limiter.wait(ctx); err != nil {
		return nil, safeProviderError(providerName, err)
	}
	budget.consume()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, safeProviderError(providerName, err)
	}
	req.Header.Set("Accept", accept)
	applyHeaders(req.Header, headers)
	resp, err := client.Do(req)
	if err != nil {
		return nil, safeProviderError(providerName, err)
	}
	defer resp.Body.Close()
	body, readErr := io.ReadAll(io.LimitReader(resp.Body, maxBodyBytes))
	if readErr != nil {
		return nil, safeProviderError(providerName, readErr)
	}
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return nil, providerStatusError(providerName, resp.StatusCode, body)
	}
	return body, nil
}
