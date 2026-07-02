package database

// SourceConfig represents crowler-source-config-schema.json.
type SourceConfig struct {
	Version        string                `json:"version,omitempty"`
	FormatVersion  string                `json:"format_version" required:"true"`
	Author         string                `json:"author,omitempty"`
	Description    string                `json:"description,omitempty"`
	CreatedAt      string                `json:"created_at,omitempty"`
	SourceName     string                `json:"source_name" required:"true"`
	CrawlingConfig SourceCrawlingConfig  `json:"crawling_config" required:"true"`
	ExecutionPlan  []SourceExecutionStep `json:"execution_plan,omitempty"`
	Custom         map[string]any        `json:"custom,omitempty"`
	MetaData       map[string]any        `json:"meta_data,omitempty"`
}

// SourceCrawlingConfig represents the crawling configuration for a source.
type SourceCrawlingConfig struct {
	Proxy             string                `json:"proxy,omitempty"`
	Site              string                `json:"site" required:"true"`
	URLReferrer       string                `json:"url_referrer,omitempty"`
	AlternativeLinks  []string              `json:"alternative_links,omitempty"`
	RetriesOnRedirect int                   `json:"retries_on_redirect,omitempty"`
	UnwantedURLs      []string              `json:"unwanted_urls,omitempty"`
	SourceType        SourceConfigType      `json:"source_type,omitempty"`
	LoadValidation    *SourceLoadValidation `json:"load_validation,omitempty"`
}

// SourceConfigType defines the type of the source, which can influence crawling and processing behavior.
type SourceConfigType string

const (
	// SourceConfigTypeWebsite represents the string "website"
	SourceConfigTypeWebsite SourceConfigType = "website"
	// SourceConfigTypeAPI represents the string "api"
	SourceConfigTypeAPI SourceConfigType = "api"
	// SourceConfigTypeFile represents the string "file"
	SourceConfigTypeFile SourceConfigType = "file"
	// SourceConfigTypeDB represents the string "db"
	SourceConfigTypeDB SourceConfigType = "db"
	// SourceConfigTypeEmail represents the string "email"
	SourceConfigTypeEmail SourceConfigType = "email"
)

// SourceLoadValidation defines the structure for validating the loaded content of a source before processing.
type SourceLoadValidation struct {
	Groups []SourceValidationGroup `json:"groups" required:"true"`
}

// SourceValidationGroup represents a group of validations that apply to URLs matching a specific pattern, with defined actions on validation failure.
type SourceValidationGroup struct {
	URLPattern             string             `json:"url_pattern" required:"true"`
	Scope                  ValidationScope    `json:"scope,omitempty"`
	Validations            []SourceValidation `json:"validations" required:"true"`
	AnyValidationSatisfies bool               `json:"any_validation_satisfies,omitempty"`
	OnFail                 GroupOnFailAction  `json:"on_fail,omitempty"`
	MaxRetries             int                `json:"max_retries,omitempty"`
}

// ValidationScope defines the scope of URLs that a validation group applies to, which can help optimize validation by limiting it to relevant URLs.
type ValidationScope string

const (
	// ValidationScopeSeedOnly represents the string "seed_only"
	ValidationScopeSeedOnly ValidationScope = "seed_only"
	// ValidationScopeSourceDomain represents the string "source_domain"
	ValidationScopeSourceDomain ValidationScope = "source_domain"
	// ValidationScopeSameOrigin represents the string "same_origin"
	ValidationScopeSameOrigin ValidationScope = "same_origin"
	// ValidationScopeAllCrawled represents the string "all_crawled"
	ValidationScopeAllCrawled ValidationScope = "all_crawled"
)

// GroupOnFailAction defines the possible actions to take when a validation group fails, allowing for flexible handling of validation failures based on the specific needs of the source and the criticality of the validations.
type GroupOnFailAction string

const (
	// GroupOnFailRetry represents the string "retry"
	GroupOnFailRetry GroupOnFailAction = "retry"
	// GroupOnFailMarkInvalid represents the string "mark_invalid"
	GroupOnFailMarkInvalid GroupOnFailAction = "mark_invalid"
	// GroupOnFailSkip represents the string "skip"
	GroupOnFailSkip GroupOnFailAction = "skip"
	// GroupOnFailLogOnly represents the string "log_only"
	GroupOnFailLogOnly GroupOnFailAction = "log_only"
)

// SourceValidation represents a set of DOM checks that must be performed on a page to validate its content before processing. It includes options for how to handle validation failures and whether all checks must pass or if any single check passing is sufficient.
type SourceValidation struct {
	DOMChecks         []SourceDOMCheck       `json:"dom_checks" required:"true"`
	AllChecksMustPass bool                   `json:"all_checks_must_pass,omitempty"`
	OnFail            ValidationOnFailAction `json:"on_fail,omitempty"`
	MaxRetries        int                    `json:"max_retries,omitempty"`
}

// ValidationOnFailAction defines the possible actions to take when a validation fails, allowing for flexible handling of validation failures based on the specific needs of the source and the criticality of the validations.
type ValidationOnFailAction string

const (
	// ValidationOnFailRetry represents the string "retry"
	ValidationOnFailRetry ValidationOnFailAction = "retry"
)

// SourceDOMCheck represents a single DOM check that can be performed on a page, including the CSS selector to target elements, optional timeout for the check, and a list of conditions that must be satisfied for the check to pass.
type SourceDOMCheck struct {
	Selector   string               `json:"selector" required:"true"`
	Timeout    string               `json:"timeout,omitempty"`
	Conditions []SourceDOMCondition `json:"conditions,omitempty"`
}

// SourceDOMCondition represents a specific condition to check on a DOM element, such as whether it exists, its text content, attributes, or the count of matching elements. The Type field defines the type of condition, and additional fields provide parameters for the condition based on its type.
type SourceDOMCondition struct {
	Type      DOMConditionType `json:"type" required:"true"`
	Attribute string           `json:"attribute,omitempty"`
	Pattern   string           `json:"pattern,omitempty"`
	MinCount  int              `json:"min_count,omitempty"`
	MaxCount  int              `json:"max_count,omitempty"`
}

// DOMConditionType defines the type of condition to check on a DOM element, which can be used to validate the presence, content, attributes, or count of elements matching a CSS selector during source validation.
type DOMConditionType string

const (
	// DOMConditionExists represents the exists string
	DOMConditionExists DOMConditionType = "exists"
	// DOMConditionNotExists represents the not_exists string
	DOMConditionNotExists DOMConditionType = "not_exists"
	// DOMConditionText represents the text string
	DOMConditionText DOMConditionType = "text"
	// DOMConditionAttribute represents the attribute string
	DOMConditionAttribute DOMConditionType = "attribute"
	// DOMConditionCount represents the count string
	DOMConditionCount DOMConditionType = "count"
)

// SourceExecutionStep represents a single step in the execution plan for processing a source, including the label for the step, conditions that determine when the step should be executed, and optional rulesets, rule groups, and individual rules to apply during the step. Additional conditions can also be specified for more complex execution logic.
type SourceExecutionStep struct {
	Label                string                    `json:"label" required:"true"`
	Conditions           SourceExecutionConditions `json:"conditions" required:"true"`
	Rulesets             []string                  `json:"rulesets,omitempty"`
	RuleGroups           []string                  `json:"rule_groups,omitempty"`
	Rules                []string                  `json:"rules,omitempty"`
	AdditionalConditions map[string]any            `json:"additional_conditions,omitempty"`
}

// SourceExecutionConditions defines the conditions under which a particular execution step should be executed. This can include URL patterns that determine which pages the step applies to, allowing for targeted processing based on the structure of the website or specific URLs.
type SourceExecutionConditions struct {
	URLPatterns []string `json:"url_patterns" required:"true"`
}
