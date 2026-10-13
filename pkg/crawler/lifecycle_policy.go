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
	cmn "github.com/pzaino/thecrowler/pkg/common"
)

// Lifecycle policy decisions for User-Agent rotation and session reset.
//
// Preservation note: these helpers name the two
// historical conditions without changing them. In particular, setupBrowser
// refreshes the User-Agent when ResetCookiesPolicy == "always" (not when
// ChangeUserAgent == "always"), while the per-request full VDI reset is
// gated on ChangeUserAgent == "always". Whether that coupling is intended is
// an owner decision; the characterization tests
// pin the current behavior so any future change is reviewable.
const (
	// policyAlways is the policy value enabling per-request identity work.
	// It aliases the shared constant to keep the comparison exact.
	policyAlways = cmn.AlwaysStr
)

// resetSessionForUserAgent reports whether the per-request full VDI reset
// applies for the configured change_useragent policy (getURLContent path).
func resetSessionForUserAgent(changeUserAgent string) bool {
	return changeUserAgent == policyAlways
}

// refreshUserAgentOnSetup reports whether setupBrowser refreshes the
// User-Agent on every setup (historical coupling to ResetCookiesPolicy).
func refreshUserAgentOnSetup(resetCookiesPolicy string) bool {
	return resetCookiesPolicy == policyAlways
}
