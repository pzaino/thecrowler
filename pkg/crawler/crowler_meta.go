package crawler

import (
	"encoding/json"
	"fmt"
	"reflect"
	"strings"

	cdb "github.com/pzaino/thecrowler/pkg/database"
)

const CrowlerMetaKey = "crowler_meta"
const CrowlerMetaDataKey = "meta_data"

type CrowlerMeta map[string]interface{}

// NewCrowlerMeta creates a new CrowlerMeta structure, merging the provided metaData and srcCfg into a single map under the key "meta_data".
func NewCrowlerMeta(metaData map[string]interface{}, srcCfg map[string]interface{}) CrowlerMeta {
	cm := CrowlerMeta{}
	finalMetaData := map[string]interface{}{}
	mergeMap(finalMetaData, metaData)
	mergeMap(finalMetaData, extractMetaData(srcCfg))
	cm[CrowlerMetaDataKey] = cloneMap(finalMetaData)
	return cm
}

func NewCrowlerMetaFromSource(source *cdb.Source, srcCfg map[string]interface{}) CrowlerMeta {
	return NewCrowlerMeta(sourceMetaData(source), srcCfg)
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

func EnsureCrowlerMeta(doc map[string]interface{}, source *cdb.Source, srcCfg map[string]interface{}) CrowlerMeta {
	if doc == nil {
		return NewCrowlerMetaFromSource(source, srcCfg)
	}
	fresh := NewCrowlerMetaFromSource(source, srcCfg)
	if cm, ok := normalizeCrowlerMeta(doc[CrowlerMetaKey]); ok {
		merged := cloneMap(fresh[CrowlerMetaDataKey].(map[string]interface{}))
		mergeMap(merged, extractMetaData(cm))
		cm[CrowlerMetaDataKey] = merged
		doc[CrowlerMetaKey] = cm
		return cm
	}
	cm := fresh
	doc[CrowlerMetaKey] = cm
	return cm
}

func EnsurePageCrowlerMeta(pageInfo *PageInfo, source *cdb.Source, srcCfg map[string]interface{}) CrowlerMeta {
	if pageInfo == nil {
		return NewCrowlerMetaFromSource(source, srcCfg)
	}
	doc := map[string]interface{}{}
	if pageInfo.CrowlerMeta != nil {
		doc[CrowlerMetaKey] = pageInfo.CrowlerMeta
	}
	cm := EnsureCrowlerMeta(doc, source, srcCfg)
	pageInfo.CrowlerMeta = cm
	return cm
}

func normalizeCrowlerMeta(value interface{}) (CrowlerMeta, bool) {
	switch v := value.(type) {
	case CrowlerMeta:
		return v, true
	case map[string]interface{}:
		return CrowlerMeta(v), true
	default:
		return nil, false
	}
}

func sourceMetaData(source *cdb.Source) map[string]interface{} {
	if source == nil || source.Config == nil || len(*source.Config) == 0 {
		return map[string]interface{}{}
	}
	return extractMetaData(source.Config)
}

func extractMetaData(value interface{}) map[string]interface{} {
	if value == nil {
		return map[string]interface{}{}
	}
	switch v := value.(type) {
	case map[string]interface{}:
		return extractMetaDataFromMap(v)
	case json.RawMessage:
		return extractMetaDataFromJSON(v)
	case *json.RawMessage:
		if v != nil {
			return extractMetaDataFromJSON(*v)
		}
	case []byte:
		return extractMetaDataFromJSON(v)
	case string:
		return extractMetaDataFromJSON([]byte(v))
	}
	rv := reflect.ValueOf(value)
	if rv.Kind() == reflect.Map {
		return extractMetaDataFromMap(normalizeMap(value))
	}
	return extractMetaDataFromStruct(value)
}

func extractMetaDataFromMap(cfg map[string]interface{}) map[string]interface{} {
	if md, ok := cfg[CrowlerMetaDataKey]; ok {
		return normalizeMap(md)
	}
	if nestedCfg, ok := cfg["config"]; ok {
		return extractMetaData(nestedCfg)
	}
	return map[string]interface{}{}
}

func extractMetaDataFromJSON(raw []byte) map[string]interface{} {
	var cfg map[string]interface{}
	if err := json.Unmarshal(raw, &cfg); err != nil {
		return map[string]interface{}{}
	}
	return extractMetaData(cfg)
}

func extractMetaDataFromStruct(value interface{}) map[string]interface{} {
	rv := reflect.ValueOf(value)
	if rv.Kind() == reflect.Pointer {
		if rv.IsNil() {
			return map[string]interface{}{}
		}
		rv = rv.Elem()
	}
	if rv.Kind() != reflect.Struct {
		return map[string]interface{}{}
	}
	field := rv.FieldByName("MetaData")
	if !field.IsValid() || field.IsNil() {
		return map[string]interface{}{}
	}
	return normalizeMap(field.Interface())
}

func normalizeMap(value interface{}) map[string]interface{} {
	if md, ok := value.(map[string]interface{}); ok {
		return cloneMap(md)
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		return map[string]interface{}{}
	}
	var out map[string]interface{}
	if err := json.Unmarshal(encoded, &out); err != nil {
		return map[string]interface{}{}
	}
	return out
}

func mergeMap(dst, src map[string]interface{}) {
	for k, v := range src {
		dst[k] = v
	}
}

func cloneMap(in map[string]interface{}) map[string]interface{} {
	out := map[string]interface{}{}
	for k, v := range in {
		out[k] = v
	}
	return out
}
