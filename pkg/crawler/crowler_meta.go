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
const CrowlerMetaObjectTypeKey = "object_type"
const CrowlerMetaSourceUIDKey = "source_uid"

type CrowlerMeta map[string]interface{}

// NewCrowlerMeta creates a new CrowlerMeta structure, merging the provided metaData and srcCfg into a single map under the key "meta_data".
func NewCrowlerMeta(metaData map[string]interface{}, srcCfg map[string]interface{}) CrowlerMeta {
	cm := CrowlerMeta{}
	finalMetaData := map[string]interface{}{}
	mergeMap(finalMetaData, metaData)
	mergeMap(finalMetaData, extractMetaData(srcCfg))
	cm[CrowlerMetaDataKey] = cloneMap(finalMetaData)
	cm[CrowlerMetaObjectTypeKey] = []string{}
	return cm
}

func NewCrowlerMetaFromSource(source *cdb.Source, srcCfg map[string]interface{}) CrowlerMeta {
	cm := NewCrowlerMeta(sourceMetaData(source), srcCfg)
	cm.EnsureSourceUID(source)
	return cm
}

func (cm CrowlerMeta) SetTag(section, key string, value interface{}) error {
	section = strings.TrimSpace(section)
	key = strings.TrimSpace(key)
	// Key cannot be empty
	if key == "" {
		return fmt.Errorf("invalid crowler_meta key")
	}
	if section == "" || section == CrowlerMetaKey {
		// Tag is in the root of crowler_meta
		if key == CrowlerMetaSourceUIDKey && isEmptyCrowlerMetaValue(value) {
			return fmt.Errorf("crowler_meta %q cannot be empty", CrowlerMetaSourceUIDKey)
		}
		cm[key] = value
		return nil
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

// EnsureSourceUID guarantees that crowler_meta has the source_uid stored on
// the Sources row for the root URL currently being crawled. It deliberately
// does not derive a UID from Source.Name/Source.URL because URL fields can
// represent redirected or child pages during processing.
func (cm CrowlerMeta) EnsureSourceUID(source *cdb.Source) {
	if cm == nil {
		return
	}
	if !isEmptyCrowlerMetaValue(cm[CrowlerMetaSourceUIDKey]) {
		return
	}
	uid := sourceUID(source)
	if uid == "" {
		return
	}
	cm[CrowlerMetaSourceUIDKey] = uid
}

// AddObjectType appends normalized object type labels to crowler_meta.object_type.
// Empty labels are ignored. Existing labels are preserved in insertion order and
// duplicates are ignored after normalization.
func (cm CrowlerMeta) AddObjectType(labels ...string) {
	if cm == nil {
		return
	}
	existing := cm.ObjectTypes()
	seen := make(map[string]struct{}, len(existing)+len(labels))
	for _, label := range existing {
		seen[label] = struct{}{}
	}
	for _, label := range labels {
		normalized := NormalizeObjectTypeLabel(label)
		if normalized == "" {
			continue
		}
		if _, ok := seen[normalized]; ok {
			continue
		}
		existing = append(existing, normalized)
		seen[normalized] = struct{}{}
	}
	cm[CrowlerMetaObjectTypeKey] = existing
}

// ObjectTypes returns the normalized unique crowler_meta.object_type labels.
func (cm CrowlerMeta) ObjectTypes() []string {
	if cm == nil {
		return nil
	}
	labels := normalizeObjectTypeValue(cm[CrowlerMetaObjectTypeKey])
	if labels == nil {
		labels = []string{}
	}
	cm[CrowlerMetaObjectTypeKey] = labels
	return labels
}

// NormalizeObjectTypeLabel normalizes a user-provided object_type label.
func NormalizeObjectTypeLabel(label string) string {
	return strings.ToLower(strings.Join(strings.Fields(strings.TrimSpace(label)), "_"))
}

func (cm CrowlerMeta) DeleteTag(section, key string) error {
	section = strings.TrimSpace(section)
	key = strings.TrimSpace(key)
	if section == "" || section == CrowlerMetaKey || key == "" || key == CrowlerMetaSourceUIDKey || (section == CrowlerMetaDataKey && key == CrowlerMetaDataKey) {
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
		cm.EnsureSourceUID(source)
		cm.ObjectTypes()
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

func normalizeObjectTypeValue(value interface{}) []string {
	values := []string{}
	seen := map[string]struct{}{}
	add := func(raw string) {
		label := NormalizeObjectTypeLabel(raw)
		if label == "" {
			return
		}
		if _, ok := seen[label]; ok {
			return
		}
		seen[label] = struct{}{}
		values = append(values, label)
	}
	switch v := value.(type) {
	case []string:
		for _, item := range v {
			add(item)
		}
	case []interface{}:
		for _, item := range v {
			if s, ok := item.(string); ok {
				add(s)
			}
		}
	case string:
		add(v)
	}
	return values
}

func currentCrowlerMeta(ctx *ProcessContext) CrowlerMeta {
	if ctx == nil {
		return NewCrowlerMeta(nil, nil)
	}
	if ctx.crowlerMeta == nil {
		ctx.crowlerMeta = NewCrowlerMetaFromSource(ctx.source, ctx.srcCfg)
	}
	ctx.crowlerMeta.EnsureSourceUID(ctx.source)
	return ctx.crowlerMeta
}

func sourceUID(source *cdb.Source) string {
	if source == nil {
		return ""
	}
	return strings.TrimSpace(source.UID)
}

func isEmptyCrowlerMetaValue(value interface{}) bool {
	if value == nil {
		return true
	}
	if s, ok := value.(string); ok {
		return strings.TrimSpace(s) == ""
	}
	return false
}
