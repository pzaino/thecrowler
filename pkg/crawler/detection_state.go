package crawler

import (
	"reflect"
	"regexp"
	"sort"
	"strings"

	cmn "github.com/pzaino/thecrowler/pkg/common"
	detect "github.com/pzaino/thecrowler/pkg/detection"
)

var detectionKeySanitizer = regexp.MustCompile(`[^a-z0-9]+`)

func publishDetectionResults(ctx *ProcessContext, targetURL string, detectedTech *map[string]detect.DetectedEntity) {
	if ctx == nil || detectedTech == nil {
		return
	}
	ensureKVStore()

	source := ""
	if ctx.source != nil {
		source = ctx.source.URL
	}
	props := cmn.NewKVStoreProperty(true, false, true, false, source, ctx.GetContextID(), "")

	setDetectionStateKV("detection.target_url", targetURL, props)
	setDetectionStateKV("detection.detected_tech", *detectedTech, props)

	names := make([]string, 0, len(*detectedTech))
	for name, entity := range *detectedTech {
		names = append(names, name)
		slug := sanitizeDetectionKey(name)
		setDetectionStateKV("detection.tech."+slug, entity, props)
		setDetectionStateKV("detection.tech."+slug+".confidence", entity.Confidence, props)
		setDetectionStateKV("detection.tech."+slug+".type", entity.EntityType, props)
	}
	sort.Strings(names)
	setDetectionStateKV("detection.detected_tech_names", names, props)
}

func setDetectionStateKV(key string, value interface{}, props cmn.Properties) {
	if err := cmn.KVStore.Set(key, value, props); err != nil {
		cmn.DebugMsg(cmn.DbgLvlError, "storing detection state in KVStore for key '%s': %v", key, err)
	}
}

func sanitizeDetectionKey(input string) string {
	normalized := strings.ToLower(strings.TrimSpace(input))
	normalized = detectionKeySanitizer.ReplaceAllString(normalized, "_")
	return strings.Trim(normalized, "_")
}

func checkEnvironmentCondition(ctx *ProcessContext, condition interface{}) bool {
	if ctx == nil || condition == nil {
		return false
	}
	ensureKVStore()

	switch typed := condition.(type) {
	case string:
		return kvKeyExists(ctx, typed)
	case []string:
		for _, key := range typed {
			if !kvKeyExists(ctx, key) {
				return false
			}
		}
		return true
	case []interface{}:
		for _, rawKey := range typed {
			key, ok := rawKey.(string)
			if !ok || !kvKeyExists(ctx, key) {
				return false
			}
		}
		return true
	case map[string]interface{}:
		for key, expected := range typed {
			actual, _, err := cmn.KVStore.Get(key, ctx.GetContextID())
			if err != nil {
				return false
			}
			if !compareConditionValues(actual, expected) {
				return false
			}
		}
		return true
	default:
		return false
	}
}

func ensureKVStore() {
	if cmn.KVStore == nil {
		cmn.KVStore = cmn.NewKeyValueStore()
	}
}

func kvKeyExists(ctx *ProcessContext, key string) bool {
	_, _, err := cmn.KVStore.Get(strings.TrimSpace(key), ctx.GetContextID())
	return err == nil
}

func compareConditionValues(actual interface{}, expected interface{}) bool {
	switch expectedTyped := expected.(type) {
	case nil:
		return actual != nil
	case []interface{}:
		return containsAll(actual, expectedTyped)
	default:
		return reflect.DeepEqual(actual, expected)
	}
}

func containsAll(actual interface{}, expected []interface{}) bool {
	actualValue := reflect.ValueOf(actual)
	if actualValue.Kind() != reflect.Slice && actualValue.Kind() != reflect.Array {
		return false
	}

	for _, expectedItem := range expected {
		found := false
		for i := 0; i < actualValue.Len(); i++ {
			if reflect.DeepEqual(actualValue.Index(i).Interface(), expectedItem) {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	return true
}
