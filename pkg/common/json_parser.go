package common

// JsonParser recursively traverses the JSON document (represented as map[string]interface{})
// following the sequence of keys provided.
func JsonParser(doc map[string]interface{}, keys ...string) interface{} {
	if len(keys) == 0 {
		return doc
	}

	key := keys[0]
	value, exists := doc[key]
	if !exists {
		return nil // key not found
	}

	if len(keys) == 1 {
		return value
	}

	nestedDoc, ok := value.(map[string]interface{})
	if !ok {
		return nil // value is not a map so we cannot traverse further
	}

	return JsonParser(nestedDoc, keys[1:]...)
}
