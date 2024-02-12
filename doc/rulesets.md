# TheCROWler Rulesets

TheCROWler uses rulesets to both, interact with a website and determine which
information to extract and which to ignore. Rulesets are defined in a YAML file
and are used to define the following:

- Detect and interact with a website fields and buttons
- Define which portions of a page to extract and how to store the extracted
  information

Extracted data is stored in a JSON document, which is then stored in the DB and
each field is indexed and mapped to a "dorking" category. The mapping is defined
by the ruleset and is used to enrich and facilitate information searching.

## Ruleset Structure

A ruleset can be stored in a YAML file, each JSON file can contains multiple
rulesets. The ruleset structure is as follows:

```yaml
---
- ruleset_name: "Example Items Extraction Ruleset"
  format_version: "1.0"
  rule_groups:
    - group_name: "Group1"
      valid_from: "2021-01-01T00:00:00Z"
      valid_to: "2029-12-31T00:00:00Z"
      is_enabled: true
      scraping_rules:
        - rule_name: "Articles"
          path: "/articles"
          elements:
            - key: "title"
              selectors:
                - selector_type: "css"
                  selector: "h1.article-title"
                - selector_type: "xpath"
                  selector: "//h1[@class='article-title']"
            - key: "content"
              selectors:
                - selector_type: "css"
                  selector: "div.article-content"
            - key: "date"
              selectors:
                - selector_type: "css"
                  selector: "span.date"
          js_files: true
          technology_patterns:
            - "jquery"
            - "bootstrap"

    - group_name: "Group2"
      valid_from: "2021-01-01T00:00:00Z"
      valid_to: "2021-12-31T00:00:00Z"
      is_enabled: false
      scraping_rules:
        - rule_name: "News"
          path: "/news"
          elements:
            - key: "headline"
              selectors:
                - selector_type: "css"
                  selector: "h1.headline"
            - key: "summary"
              selectors:
                - selector_type: "css"
                  selector: "p.summary"
          js_files: false

- ruleset_name: "another-example.com"
  format_version: "1.0"
  rule_groups:
    - group_name: "GroupA"
      valid_from: "2021-01-01T00:00:00Z"
      valid_to: "2023-12-31T00:00:00Z"
      is_enabled: true
      scraping_rules:
        - rule_name: "Products"
          path: "/products"
          elements:
            - key: "name"
              selectors:
              - selector_type: "css"
                selector: "div.product-name"
            - key: "price"
              selectors:
              - selector_type: "css"
                selector: "span.price"

```

For more info on all the available fields and their meaning, please refer to the
[ruleset schema](./schemas/ruleset-schema.json).

Let's have a look at the structure of the ruleset:

- `ruleset_name`: The name of the ruleset (this is required and you can decide
  the strategy for your ruleset names):
  - One way to call a ruleset is to use the domain name of the website you are
    scraping (e.g. "example.com"). This is useful when you have multiple
    rulesets and you want to easily identify which ruleset is for which
    website. The ruleset SDK supports a function to find ruleset by the URL of
    the website, so you can use the domain name as the ruleset name and then
    use the SDK function to find the ruleset by the URL of the website. This
    function also checks if the URL is valid and if a ruleset name is indeed a
    URL.
  - Another way is to use a descriptive name for the ruleset (e.g. "Example
    Items Extraction Ruleset"). This is useful when you have multiple rulesets
    and you want to easily identify which ruleset is for which purpose. The
    ruleset SDK supports a function to find ruleset by the name of the ruleset,
    so you can use a descriptive name as the ruleset name and then use the SDK
    function to find the ruleset by the name of the ruleset.

- `format_version`: The version of the ruleset format. This is used to validate
  the ruleset format. The current version is "1.0".

- `rule_groups`: A list of rule groups. Each rule group contains a set of rules
  and a set of metadata.

  - `group_name`: The name of the rule group (this is required). A group name is
    usually a descriptive (or functional) name for the rule group. In terms of
    functionalities, a group is usually fetched as a whole and then rules are
    applied in the order to which they have been defined in a group.

  - `valid_from`: The date and time from which the rule group is valid. This is
    used to validate the rule group. This is a string in the format
    "YYYY-MM-DDTHH:MM:SSZ". This field is optional.

  - `valid_to`: The date and time to which the rule group is valid. This is used
    to validate the rule group. This is a string in the format
    "YYYY-MM-DDTHH:MM:SSZ". This field is optional.

  - `is_enabled`: A boolean to indicate if the rule group is enabled. This is
    used to validate the rule group.

  - `scraping_rules`: A list of scraping rules. Each scraping rule contains a set
    of elements and a set of metadata.

    - `rule_name`: The name of the scraping rule (this is required and you can
      decide the strategy for your rule names):
      - One way to call a scraping rule is to use the domain name of the website
        you are scraping (e.g. "example.com"). This is useful when you have
        multiple scraping rules and you want to easily identify which scraping
        rule is for which website. In this case, if the current URL is different
        than the rule name URL, then the rule will be ignored.
      - Another way is to use a descriptive name for the scraping rule
        (e.g. "Articles"). This is useful when you have multiple scraping rules
        and you want to easily identify which scraping rule is for which
        purpose. In this case, the rule will be applied to any URL.

    - `path`: The path of the URL to which the scraping rule is applied. This is
      a string. This field is optional. If the path is not defined, then the
      scraping rule will be applied to any URL. If the path is specified, then
      the scraping rule will be applied to any URL with the URL portion after
      the hostname/domain name that matches the path.

    - `elements`: A list of elements. Each element contains a set of selectors.

      - `key`: The key of the element (this is required). The key is used to
        store the extracted information in the JSON document. The key is also
        used to map the extracted information to a "dorking" category.

      - `selectors`: A list of selectors. Each selector contains a set of
        metadata.

        - `selector_type`: The type of the selector (this is required). The type
          of the selector can be "css" or "xpath".

        - `selector`: The selector (this is required). The selector is used to
          extract the information from the HTML document.
        The way the list of selectors is applied is as follow:
        - If the first selector is successful, then the next selectors are
          ignored. If the first selector is not successful, then the next
          selectors are tried in order. If no selector is successful, then the
          element is not extracted.

    - `js_files`: A boolean to indicate if the scraping has to extract and store
      all the JavaScript files found on the current URL. This field is optional.
      If you specify `true`, then all the JavaScript files found on the current
      URL will be extracted and stored in the JSON document. If you specify
      `false`, then no JavaScript file will be extracted and stored in the JSON
      document (unless you have made a rule specifically targeted to extract
      JavaScript files).

    - `technology_patterns`: A list of technology patterns. Each technology
      pattern is a string. This field is optional. If you specify a list of
      technology patterns, then the scraping rule will be applied to any URL
      which presents such technologies. If you do not specify a list of
      technology patterns, then the scraping rule will be applied to any URL.
      Keep in mind that this approach may not always work as expected (because
      certain technologies might be cloaked or obfuscated).

## How to use a ruleset

A ruleset can be used "automatically" or "manually".

### Automatically

The rules in each ruleset will be executed automatically if their name and path match the URL of the website being crawled.

### Manually

To use rules "manually" (aka have them executed automatically for a given source), you'll need to specify them in the source configuration.

More details on the source configuration [here](./sources.md).

## Ruleset Validation

The ruleset is validated using a JSON schema. The schema is defined in the
[ruleset schema](./schemas/ruleset-schema.json). The schema is used to validate
the ruleset format and to validate the ruleset content.

## Adding rules and rulesets validation in VSCode

To add rules and validate the ruleset in VSCode, you can use the following
extension:

Open (or create) your VSCode settings.json file and add the following:

```json
"yaml.schemas": {
    "./schemas/ruleset-schema.json": "*-ruleset.y*ml"
}
```

Then, ensure you call all your ruleset files with the `-ruleset.yaml` or
 `-ruleset.yml` extension.

This will allow you to validate the ruleset in VSCode as you type them.
