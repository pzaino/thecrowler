package common

import (
	"reflect"
	"testing"
)

func TestConvertInterfaceMapToStringMap(t *testing.T) {
	// Test case 1: map[interface{}]interface{}
	input1 := map[interface{}]interface{}{
		"key1": "value1",
		"key2": 2,
		"key3": []interface{}{"a", "b", "c"},
	}
	expected1 := map[string]interface{}{
		"key1": "value1",
		"key2": 2,
		"key3": []interface{}{"a", "b", "c"},
	}
	result1 := ConvertInterfaceMapToStringMap(input1)
	if !reflect.DeepEqual(result1, expected1) {
		t.Errorf("ConvertInterfaceMapToStringMap() = %v; want %v", result1, expected1)
	}

	// Test case 2: []interface{}
	input2 := []interface{}{"a", 1, map[interface{}]interface{}{"key": "value"}}
	expected2 := []interface{}{"a", 1, map[string]interface{}{"key": "value"}}
	result2 := ConvertInterfaceMapToStringMap(input2)
	if !reflect.DeepEqual(result2, expected2) {
		t.Errorf("ConvertInterfaceMapToStringMap() = %v; want %v", result2, expected2)
	}

	// Test case 3: other types
	input3 := "test"
	expected3 := "test"
	result3 := ConvertInterfaceMapToStringMap(input3)
	if !reflect.DeepEqual(result3, expected3) {
		t.Errorf("ConvertInterfaceMapToStringMap() = %v; want %v", result3, expected3)
	}
}
