# TheCROWler Ruleset Reference

*The CROWler ruleset schema defines the structure of a ruleset file, which
contains rules for scraping, action execution, detection, and crawling.*

## Items

- **Items** *(object)*
  - **`format_version`** *(string)*: Version of the ruleset format, to ensure
   compatibility.
  - **`author`** *(string)*: The author or owner of the ruleset.
  - **`created_at`** *(string)*: Creation date of the ruleset.
  - **`description`** *(string)*: A brief description of what the ruleset does.
  - **`ruleset_name`** *(string)*: A unique name identifying the ruleset.
  - **`rule_groups`** *(array)*
    - **Items** *(object)*
      - **`group_name`** *(string)*: A unique name identifying the group of
      rules.
      - **`valid_from`** *(string)*: The start date from which the rule group
      becomes active.
      - **`valid_to`** *(string)*: The end date until which the rule group
      remains active.
      - **`is_enabled`** *(boolean)*: Flag to enable or disable the rule group.
      - **`scraping_rules`** *(array)*
        - **Items** *(object)*
          - **`rule_name`** *(string)*: A unique name identifying the scraping
           rule.
          - **`pre_conditions`** *(array)*: Conditions that must be met for the
           scraping to be executed.
            - **Items** *(object)*
              - **`path`** *(string)*: The specific path or pattern to match
              for scraping.
              - **`url`** *(string)*: Optional. The specific URL to which this
               rule applies. If omitted, the rule is considered applicable to
                any URL matching the path.
          - **`elements`** *(array)*: Defines multiple ways to find and
          interact with elements, allowing for CSS, XPath, and other
          Selenium-supported strategies.
            - **Items** *(object)*
              - **`key`** *(string)*
              - **`selectors`** *(array)*
                - **Items** *(object)*
                  - **`selector_type`** *(string)*: Must be one of: `['css',
                   'xpath', 'id', 'class_name', 'name', 'tag_name',
                   'link_text', 'partial_link_text', 'regex']`.
                  - **`selector`** *(string)*
                  - **`attribute`** *(string)*: Optional. The attribute of the
                   element to extract, e.g., 'innerText'. Mainly relevant for
                   scraping actions.
          - **`js_files`** *(boolean)*: Indicates whether JavaScript files are
           relevant for the scraping.
          - **`objects`** *(array)*: Identifies specific technologies, requires
           correspondent detection rules.
            - **Items**: A unique name identifying the detection rule.
          - **`json_field_mappings`** *(object)*: Maps scraped elements to JSON
           fields using PostgreSQL JSON path expressions. Can contain
           additional properties.
            - **Additional Properties** *(string)*
          - **`wait_conditions`** *(array)*: Conditions to wait for before
          performing scraping, ensuring page readiness.
            - **Items** *(object)*
              - **`condition_type`** *(string)*: Must be one of:
              `['element_presence', 'element_visible', 'custom_js', 'delay']`.
              - **`value`** *(string)*: a generic value to use with the
              condition, e.g., a delay in seconds, applicable for delay
              condition type.
              - **`selector`** *(string)*: The CSS selector for the element,
               applicable for element_presence and element_visible conditions.
              - **`custom_js`** *(string)*: Custom JavaScript condition to
               evaluate, applicable for custom_js condition type.
          - **`post_processing`** *(array)*: Post-processing steps for the
           scraped data to transform, validate, or clean it.
            - **Items** *(object)*
              - **`step_type`** *(string)*: Must be one of: `['replace',
               'remove', 'transform', 'validate', 'clean']`.
              - **`details`** *(object)*: Detailed configuration for the
              post-processing step, structure depends on the step_type.
      - **`action_rules`** *(array)*
        - **Items** *(object)*
          - **`rule_name`** *(string)*: A unique name identifying the action
           rule.
          - **`action_type`** *(string)*: The type of action to perform,
           including advanced interactions. Must be one of: `['click',
            'input_text', 'clear', 'drag_and_drop', 'mouse_hover',
             'right_click', 'double_click', 'click_and_hold', 'release',
              'key_down', 'key_up', 'navigate_to_url', 'forward', 'back',
               'refresh', 'switch_to_window', 'switch_to_frame',
                'close_window', 'accept_alert', 'dismiss_alert',
                 'get_alert_text', 'send_keys_to_alert', 'scroll_to_element',
                  'scroll_by_amount', 'take_screenshot',
                   'execute_javascript']`.
          - **`selectors`** *(array)*
            - **Items** *(object)*
              - **`selector_type`** *(string)*: Must be one of: `['css',
               'xpath', 'id', 'class_name', 'name', 'tag_name', 'link_text',
                'partial_link_text']`.
              - **`selector`** *(string)*: The actual selector or pattern used
               to find the element based on the selector_type.
              - **`attribute`** *(object)*: Optional. The attribute of the
               element to match.
                - **`name`** *(string)*: The name of the attribute to match for
                 the selector match to be valid.
                - **`value`** *(string)*: The value to of the attribute to
                 match for the selector to be valid.
              - **`value`** *(string)*: The value within the selector that we
               need to match for the action. (this is NOT the value to input!).
          - **`value`** *(string)*: The value to use with the action, e.g.,
           text to input, applicable for input_text.
          - **`url`** *(string)*: Optional. The specific URL to which this
           action applies or the URL to navigate to, applicable for navigate
            action.
          - **`wait_conditions`** *(array)*: Conditions to wait for before
           performing the action, ensuring page readiness.
            - **Items** *(object)*
              - **`condition_type`** *(string)*: Must be one of:
              `['element_presence', 'element_visible', 'custom_js', 'delay']`.
              - **`value`** *(string)*: a generic value to use with the
               condition, e.g., a delay in seconds, applicable for delay
                condition type.
              - **`selector`** *(string)*: The CSS selector for the element,
               applicable for element_presence and element_visible conditions.
              - **`custom_js`** *(string)*: Custom JavaScript condition to
               evaluate, applicable for custom_js condition type.
          - **`conditions`** *(object)*: Conditions that must be met for the
           action to be executed. Can contain additional properties.
          - **`error_handling`** *(object)*: Error handling strategies for the
           action.
            - **`retry_count`** *(integer)*: The number of times to retry the
             action on failure.
            - **`retry_delay`** *(integer)*: The delay between retries in
             seconds.
      - **`detection_rules`** *(array)*
        - **Items** *(object)*
          - **`rule_name`** *(string)*: A unique name identifying the
           detection rule.
          - **`object_name`** *(string)*: The name of the object or technology
           to identify.
          - **`object_version`** *(string)*: Optional. The version of the
           object or technology to identify.
          - **`http_header_fields`** *(array)*: Matching patterns for HTTP
           header fields to identify technology.
            - **Items** *(object)*
              - **`key`** *(string)*: The name of the HTTP header field.
              - **`value`** *(array)*: The expected value of the HTTP header
               field.
                - **Items** *(string)*
              - **`confidence`** *(number)*: Optional. The confidence level for
               the match, ranging from 0 to 10.
          - **`page_content_patterns`** *(array)*: Patterns within the page
           content that match specific technologies.
            - **Items** *(object)*: Phrases or character sequences within page
             content indicative of specific technology.
              - **`key`** *(string)*: The name of the tag to find in the page
               content.
              - **`attribute`** *(string)*: Optional. The attribute of the tag
               to match, e.g., 'src' for img tag etc. (use 'text' for text
                content).
              - **`value`** *(array)*: The pattern to match within the page
               content's tag.
                - **Items** *(string)*
              - **`confidence`** *(number)*: Optional. The confidence level
               for the detection, decimal number ranging from 0 to 10 (or
                whatever set in the detection_configuration).
          - **`url_micro_signatures`** *(array)*: URL patterns indicative of
           specific technologies.
            - **Items** *(object)*: Micro-signatures in URLs that indicate a
             specific technology, like '/wp-admin' for WordPress.
              - **`value`** *(string)*: The micro-signature to match in the
               URL.
              - **`confidence`** *(number)*: Optional. The confidence level for
               the match, decimal number ranging from 0 to 10 (or whatever set
                in the detection_configuration).
      - **`crawling_rules`** *(array)*
        - **Items** *(object)*
          - **`rule_name`** *(string)*: A unique name identifying the crawling
           rule.
          - **`request_type`** *(string)*: The type of request to perform for
           fuzzing. Must be one of: `['GET', 'POST']`.
          - **`target_elements`** *(array)*: Specifies the elements to target
           for fuzzing, including forms.
            - **Items** *(object)*
              - **`selector_type`** *(string)*: Must be one of: `['css',
               'xpath', 'form']`.
              - **`selector`** *(string)*: The actual selector or form name
               used to find and interact with the target elements for fuzzing.
          - **`fuzzing_parameters`** *(array)*: Defines the parameters to fuzz
           and the strategy for generating fuzz values.
            - **Items** *(object)*
              - **`parameter_name`** *(string)*: Name of the parameter to fuzz.
              - **`fuzzing_type`** *(string)*: The fuzzing strategy to use for
               the parameter. Must be one of: `['fixed_list',
                'pattern_based']`.
              - **`values`** *(array)*: List of values to use for fuzzing,
               applicable if 'fuzzing_type' is 'fixed_list'.
                - **Items** *(string)*
              - **`pattern`** *(string)*: A pattern to generate fuzzing values,
               applicable if 'fuzzing_type' is 'pattern_based'.
      - **`environment_settings`** *(object)*: Custom settings for the
       WebDriver environment.
        - **`headless_mode`** *(boolean)*: Specifies if the WebDriver should
         operate in headless mode.
        - **`custom_browser_options`** *(object)*: Custom options for browser
         instances, such as proxies or window size.
      - **`logging_configuration`** *(object)*: Configuration for logging and
       monitoring rule execution.
        - **`log_level`** *(string)*: Specifies the logging level for actions
         and scraping activities. Must be one of: `['DEBUG', 'INFO', 'WARNING'
         , 'ERROR', 'CRITICAL']`.
        - **`log_file`** *(string)*: Optional. The file path to store logs if
         file logging is desired.
