package crawler

import (
	"encoding/json"
	"fmt"
	"strings"

	cdb "github.com/pzaino/thecrowler/pkg/database"
)

const CrowlerMetaKey = "crowler_meta"
const CrowlerMetaDataKey = "meta_data"

type CrowlerMeta map[string]interface{}

func NewCrowlerMeta(metaData map[string]interface{}) CrowlerMeta {
	cm := CrowlerMeta{}
	cm[CrowlerMetaDataKey] = cloneMap(metaData)
	return cm
}

func NewCrowlerMetaFromSource(source *cdb.Source) CrowlerMeta {
	return NewCrowlerMeta(sourceMetaData(source))
}

func (cm CrowlerMeta) SetTag(section, key string, value interface{}) error {
	section = strings.TrimSpace(section)
	key = strings.TrimSpace(key)
	if section == "" || section == CrowlerMetaKey || key == "" {
		return fmt.Errorf("invalid crowler_meta section or key")
	}
	m, _ := cm[section].(map[string]interface{})
	if m == nil {
		m = map[string]interface{}{}
		cm[section] = m
	}
	m[key] = value
	return nil
}

func (cm CrowlerMeta) SetSection(section string, value map[string]interface{}) error {
	section = strings.TrimSpace(section)
	if section == "" || section == CrowlerMetaKey {
		return fmt.Errorf("invalid crowler_meta section")
	}
	if section == CrowlerMetaDataKey && value == nil {
		value = map[string]interface{}{}
	}
	cm[section] = cloneMap(value)
	return nil
}

func (cm CrowlerMeta) GetSection(section string) (map[string]interface{}, bool) {
	m, ok := cm[strings.TrimSpace(section)].(map[string]interface{})
	return m, ok
}

func (cm CrowlerMeta) DeleteTag(section, key string) error {
	section = strings.TrimSpace(section)
	key = strings.TrimSpace(key)
	if section == "" || section == CrowlerMetaKey || key == "" || (section == CrowlerMetaDataKey && key == CrowlerMetaDataKey) {
		return fmt.Errorf("invalid crowler_meta section or key")
	}
	if m, ok := cm[section].(map[string]interface{}); ok {
		delete(m, key)
	}
	return nil
}

func (cm CrowlerMeta) DeleteSection(section string) error {
	section = strings.TrimSpace(section)
	if section == "" || section == CrowlerMetaKey || section == CrowlerMetaDataKey {
		return fmt.Errorf("cannot delete required crowler_meta section %q", section)
	}
	delete(cm, section)
	return nil
}

func EnsureCrowlerMeta(doc map[string]interface{}, source *cdb.Source) CrowlerMeta {
	if doc == nil {
		return NewCrowlerMetaFromSource(source)
	}
	if existing, ok := doc[CrowlerMetaKey].(map[string]interface{}); ok {
		cm := CrowlerMeta(existing)
		if _, ok := cm[CrowlerMetaDataKey]; !ok {
			cm[CrowlerMetaDataKey] = sourceMetaData(source)
		}
		doc[CrowlerMetaKey] = cm
		return cm
	}
	cm := NewCrowlerMetaFromSource(source)
	doc[CrowlerMetaKey] = cm
	return cm
}

func sourceMetaData(source *cdb.Source) map[string]interface{} {
	if source == nil || source.Config == nil || len(*source.Config) == 0 {
		return map[string]interface{}{}
	}
	var cfg map[string]interface{}
	if err := json.Unmarshal(*source.Config, &cfg); err != nil {
		return map[string]interface{}{}
	}
	if md, ok := cfg[CrowlerMetaDataKey].(map[string]interface{}); ok {
		return cloneMap(md)
	}
	return map[string]interface{}{}
}

func cloneMap(in map[string]interface{}) map[string]interface{} {
	out := map[string]interface{}{}
	for k, v := range in {
		out[k] = v
	}
	return out
}
