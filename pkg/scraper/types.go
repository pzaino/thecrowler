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

// Package scraper implements the scraper library for the Crowler and
// the Crowler search engine.
package scraper

import (
	rs "github.com/pzaino/thecrowler/pkg/ruleset"
)

// ScraperRuleEngine extends RuleEngine from the ruleset package
type ScraperRuleEngine struct {
	*rs.RuleEngine // generic rule engine
}
