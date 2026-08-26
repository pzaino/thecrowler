package crawler

import (
	"fmt"
	"strings"

	cmn "github.com/pzaino/thecrowler/pkg/common"
)

var ruleParameterFamilies = []string{
	"action",
	"scraping",
	"detection",
	"crawling",
}

func (ctx *ProcessContext) loadRuleParameters() error {
	if ctx == nil || ctx.srcCfg == nil {
		return nil
	}

	if cmn.KVStore == nil {
		return fmt.Errorf("KVStore is nil")
	}

	raw, exists := ctx.srcCfg["rule_parameters"]
	if !exists {
		return nil
	}

	root, ok := raw.(map[string]interface{})
	if !ok {
		return fmt.Errorf("source rule_parameters must be an object")
	}

	for family := range root {
		if !isRuleParameterFamily(family) {
			return fmt.Errorf("unsupported rule parameter family: %s", family)
		}
	}

	for _, family := range ruleParameterFamilies {
		rawFamily, exists := root[family]
		if !exists {
			continue
		}

		parameters, ok := rawFamily.(map[string]interface{})
		if !ok {
			return fmt.Errorf(
				"source rule_parameters.%s must be an object",
				family,
			)
		}

		for name, value := range parameters {
			name = strings.TrimSpace(name)
			if name == "" {
				return fmt.Errorf(
					"source rule_parameters.%s contains an empty parameter name",
					family,
				)
			}

			key := "rule_parameters." + family + "." + name

			properties := cmn.NewKVStoreProperty(
				true,
				true,
				false,
				false,
				"source_config",
				ctx.GetContextID(),
				"",
			)

			if err := cmn.KVStore.Set(key, value, properties); err != nil {
				return fmt.Errorf(
					"setting source rule parameter %s: %w",
					key,
					err,
				)
			}
		}
	}

	return nil
}

func isRuleParameterFamily(family string) bool {
	for _, valid := range ruleParameterFamilies {
		if family == valid {
			return true
		}
	}

	return false
}
