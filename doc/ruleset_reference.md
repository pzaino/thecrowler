# The CROWler Ruleset Reference

This reference documents the **current ruleset model** used by CROWler, aligned with:

- `pkg/ruleset/types.go`
- `schemas/crowler-ruleset-schema.json`

Use this page as a practical guide to authoring rulesets for scraping, actions, detection, crawling/fuzzing, and rule-time orchestration (`plugin_call` and `agent_call`).

For a full introduction to the ruleset concept, design principles, see the [Rulesets documentation](./rulesets.md).

For a reference to the rulesets architecture and how the engine executes rulesets, see the [Ruleset Engine documentation](./ruleset_architecture.md).

For all possible details check the [ruleset schema](../schemas/crowler-ruleset-schema.json).

---

## 1) Top-level structure

A ruleset file contains one or more rulesets:

```yaml
rulesets:
  - format_version: "1.0"
    author: "Your Name"
    created_at: "2026-05-06"
    description: "Describe intent"
    ruleset_name: "example-ruleset"
    rule_groups: []
```

### Top-level fields (per `ruleset` item)

- `format_version` *(string)*: ruleset format version.
- `author` *(string)*: owner/author.
- `created_at` *(date/time string)*: creation timestamp.
- `description` *(string)*: human description.
- `ruleset_name` *(string)*: unique ruleset name.
- `rule_groups` *(array of RuleGroup)*: executable logic.

---

## 2) Rule groups

`rule_groups` partition rules by target/site/context.

### RuleGroup fields

- `group_name` *(string)*: unique group name.
- `valid_from` *(date/time, optional)*: activation start.
- `valid_to` *(date/time, optional)*: activation end.
- `is_enabled` *(bool)*: enable/disable group.
- `url` *(string)*: URL scope anchor for this group.
- `scraping_rules` *(array, optional)*.
- `action_rules` *(array, optional)*.
- `detection_rules` *(array, optional)*.
- `crawling_rules` *(array, optional)*.
- `post_processing` *(array, optional)*: group-level post-processing.
- `environment_settings` *(array, optional)*.
- `logging_configuration` *(object, optional)*.

> `environment_settings` are key/value settings with type/property metadata and can carry arrays, strings, booleans, or numbers.

---

## 3) Shared call mechanisms: `plugin_call` vs `agent_call`

Both are first-class across subsystems.

- Use **`plugin_call`** for deterministic in-process plugin execution.
- Use **`agent_call`** for policy/trust-aware orchestration, external context, or richer failure policies.

`agent_call` objects generally use:

- `agent_name` *(string, required)*
- `params` *(object, optional)*
- `timeout` *(number, optional)*
- `max_steps` *(number, optional)*
- `on_error` *(string, optional; e.g. `continue`/`abort`)*
- `store_as` *(string, optional)*

**Note**: When calling an agent from a **detection rule**, it's important that the agent stores the final results in the CROWler KV store using the key pattern `{ctx_id}:{agent_name}`. This allows the detection engine to retrieve and process the results after the agent execution.

---

## 4) Scraping rules

### ScrapingRule fields

- `rule_name` *(string)*
- `scope` *(string)*
- `pre_conditions` *(array)*: `{ url, path }`
- `conditions` *(object)*
- `wait_conditions` *(array)*
- `elements` *(array of Element)*
- `js_files` *(bool)*: extract scripts as web objects
- `json_field_mappings` *(object map[string]string)*: rename output keys
- `post_processing` *(array)*

### Wait conditions

Each wait condition supports:

- `condition_type`: `element_presence | element_visible | plugin_call | agent_call | delay`
- `selector` *(Selector object; commonly CSS/XPath etc.)*
- `value` *(string, e.g. delay or expression like `random(1,3)`)*
- `custom_js` *(string, optional)*
- `agent_call` *(object, required when `condition_type: agent_call`)*

### Elements and selectors

`elements[]`:

- `key` *(string)*
- `selectors` *(array of Selector)*
- `critical` *(bool)*
- `transform_html_to_json` *(bool)*

`selectors[]`:

- `selector_type`: `css | xpath | id | class_name | name | tag_name | link_text | partial_link_text | regex | plugin_call | agent_call`
- `selector` *(string)*
- `attribute` *(object: `{name,value}`, optional)*
- `selector_attributes` *(array `{name,value}`, optional)*
- `value` *(string, optional)*
- `extract` *(object `{type,pattern}`, optional)*
- `extract_all_occurrences` *(bool)*
- `agent_call` *(object, optional for `agent_call` selectors)*

### Scraping post-processing

Each step:

- `step_type`: `replace | remove | transform | validate | clean | plugin_call | agent_call`
- `details` *(object; shape depends on step type)*
- `agent_call` *(object, when `step_type: agent_call`)*

---

## 5) Action rules

### ActionRule fields

- `rule_name` *(string)*
- `scope` *(string)*
- `action_type` *(string)*:
  `click | input_text | clear | drag_and_drop | mouse_hover | right_click | double_click | click_and_hold | release | key_down | key_up | navigate_to_url | forward | back | refresh | switch_to_window | switch_to_frame | close_window | accept_alert | dismiss_alert | get_alert_text | send_keys_to_alert | scroll_to_element | scroll_by_amount | take_screenshot | custom`
- `selectors` *(array of Selector)*
- `value` *(string, optional)*: e.g. input text or URL for `navigate_to_url`
- `details` *(object, optional)*
- `url` *(string, optional)*: rule applicability filter (not navigation payload)
- `wait_conditions` *(array)*
- `conditions` *(object; supports `element`, `language`, `plugin_call`, `agent_call` flows)*
- `post_processing` *(array)*
- `error_handling` *(object: `ignore`, `retry_count`, `retry_delay`)*

> For plugin-driven custom actions, use `action_type: custom` and a selector with `selector_type: plugin_call`.

---

## 6) Detection rules

### DetectionRule fields

- `rule_name` *(string)*
- `scope` *(string)*
- `object_name` *(string)*: output object/technology key.
- `http_header_fields` *(array)*: `{ key, value[], confidence }`
- `page_content_patterns` *(array)*: `{ key, attribute, value[], text[], confidence }`
- `ssl_patterns` *(array)*: `{ key, value[], confidence }`
- `url_micro_signatures` *(array)*: `{ value, confidence }`
- `meta_tags` *(array)*: `{ name, content, confidence }`
- `implies` *(array[string], optional)*
- `plugin_calls` *(array, optional)*
- `agent_calls` *(array, optional)*
- `external_detection` *(array, optional)*

### Plugin and agent detection calls

- `plugin_calls[]`: `plugin_name`, `plugin_args[]` (`parameter_name`, `parameter_value`)
- `agent_calls[]`: one or more `agent_call` definitions to run detection logic via agents.

---

## 7) Crawling rules (fuzzing and lifecycle)

### CrawlingRule fields

- `rule_name` *(string)*
- `request_type` *(string)*: typically `GET` or `POST`
- `target_elements` *(array)*
- `fuzzing_parameters` *(array)*
- `lifecycle` *(object, optional)*

`target_elements[]`:

- `selector_type`: `css | xpath | form | agent_call`
- `selector` *(string)*
- `agent_call` *(object, optional)*

`fuzzing_parameters[]`:

- `parameter_name` *(string)*
- `fuzzing_type`: `fixed_list | pattern_based`
- `selector` *(string)*
- `values[]` *(for fixed_list)*
- `pattern` *(for pattern_based)*

### Lifecycle hooks

`lifecycle` supports hook points (for example `on_start`, `on_finish`, etc.), each optionally containing an `agent_call` block for orchestration around crawl execution.

---

## 8) Logging and environment settings

### `environment_settings[]`

Each item:

- `key`
- `values` *(can be string/number/bool/array)*
- `properties`:
  - `persistent`
  - `static`
  - `session_valid`
  - `shared`
  - `type`
  - `source`

### `logging_configuration`

- `log_level`
- `log_file` *(optional)*

---

## 9) Minimal runnable examples

### A) Scraping selector via `agent_call`

```yaml
scraping_rules:
  - rule_name: "scrape-title"
    scope: "website"
    elements:
      - key: "title"
        selectors:
          - selector_type: "agent_call"
            selector: "title-resolver"
            agent_call:
              agent_name: "SelectorResolver"
              timeout: 10
              on_error: "continue"
```

### B) Detection with plugin + agent

```yaml
detection_rules:
  - rule_name: "detect-custom-stack"
    object_name: "CustomStack"
    plugin_calls:
      - plugin_name: "HeaderFingerprint"
        plugin_args:
          - parameter_name: "header"
            parameter_value: "server"
    agent_calls:
      - agent_name: "DetectionAgent"
        params:
          url: "%source_url%"
```

### C) Crawling lifecycle hook with `agent_call`

```yaml
crawling_rules:
  - rule_name: "crawl-login-surfaces"
    request_type: "GET"
    target_elements:
      - selector_type: "form"
        selector: "login"
    fuzzing_parameters:
      - parameter_name: "username"
        selector: "input[name='username']"
        fuzzing_type: "fixed_list"
        values: ["admin", "test"]
    lifecycle:
      on_start:
        agent_call:
          agent_name: "CrawlLifecycleAgent"
          params:
            stage: "on_start"
```

---

## 10) Authoring guidance (for users and AI agents)

1. Start with one `rule_group` per target site/area.
2. Keep selectors ordered from most stable to fallback.
3. Use `critical: true` for must-have fields, but remember with this setting a failure will halt the rule execution.
4. Put retries in `error_handling` for brittle UI actions.
5. Prefer `plugin_call` for fast deterministic transforms.
6. Use `agent_call` where policy, context, or approvals matter.
7. Keep detection confidence values consistent with `detection_config` thresholds.
8. Validate your ruleset against `schemas/crowler-ruleset-schema.json` before production rollout.
