package ruleset

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestPluginParams_UnmarshalJSON(t *testing.T) {
	tests := []struct {
		name     string
		jsonData string
		expected PluginParams
		wantErr  bool
	}{
		{
			name:     "string value",
			jsonData: `{"parameter_value": "test"}`,
			expected: PluginParams{
				ArgValue: "test",
				Properties: PluginParamsProperties{
					Type: varTypeStr,
				},
			},
			wantErr: false,
		},
		{
			name:     "number value",
			jsonData: `{"parameter_value": 123.45}`,
			expected: PluginParams{
				ArgValue: 123.45,
				Properties: PluginParamsProperties{
					Type: varTypeNum,
				},
			},
			wantErr: false,
		},
		{
			name:     "boolean value",
			jsonData: `{"parameter_value": true}`,
			expected: PluginParams{
				ArgValue: true,
				Properties: PluginParamsProperties{
					Type: varTypeBool,
				},
			},
			wantErr: false,
		},
		{
			name:     "null value",
			jsonData: `{"parameter_value": null}`,
			expected: PluginParams{
				ArgValue: nil,
				Properties: PluginParamsProperties{
					Type: varTypeNull,
				},
			},
			wantErr: false,
		},
		{
			name:     "array of strings",
			jsonData: `{"parameter_value": ["a", "b", "c"]}`,
			expected: PluginParams{
				ArgValue: []string{"a", "b", "c"},
				Properties: PluginParamsProperties{
					Type: arrTypeStr,
				},
			},
			wantErr: false,
		},
		{
			name:     "array of numbers",
			jsonData: `{"parameter_value": [1.1, 2.2, 3.3]}`,
			expected: PluginParams{
				ArgValue: []float64{1.1, 2.2, 3.3},
				Properties: PluginParamsProperties{
					Type: arrTypeFloat64,
				},
			},
			wantErr: false,
		},
		{
			name:     "array of booleans",
			jsonData: `{"parameter_value": [true, false, true]}`,
			expected: PluginParams{
				ArgValue: []bool{true, false, true},
				Properties: PluginParamsProperties{
					Type: arrTypeBool,
				},
			},
			wantErr: false,
		},
		{
			name:     "unknown type",
			jsonData: `{"parameter_value": {"key": "value"}}`,
			expected: PluginParams{
				ArgValue: nil,
				Properties: PluginParamsProperties{
					Type: varTypeUnknown,
				},
			},
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var pp PluginParams
			err := json.Unmarshal([]byte(tt.jsonData), &pp)
			if tt.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
				assert.Equal(t, tt.expected.ArgValue, pp.ArgValue)
				assert.Equal(t, tt.expected.Properties.Type, pp.Properties.Type)
			}
		})
	}
}

func TestProcessArray(t *testing.T) {
	tests := []struct {
		name     string
		input    []interface{}
		expected interface{}
		typeStr  string
	}{
		{
			name:     "empty array",
			input:    []interface{}{},
			expected: []interface{}{},
			typeStr:  varTypeArr,
		},
		{
			name:     "array of strings",
			input:    []interface{}{"a", "b", "c"},
			expected: []string{"a", "b", "c"},
			typeStr:  arrTypeStr,
		},
		{
			name:     "array of numbers",
			input:    []interface{}{1.1, 2.2, 3.3},
			expected: []float64{1.1, 2.2, 3.3},
			typeStr:  arrTypeFloat64,
		},
		{
			name:     "array of booleans",
			input:    []interface{}{true, false, true},
			expected: []bool{true, false, true},
			typeStr:  arrTypeBool,
		},
		{
			name:     "array of unknown type",
			input:    []interface{}{map[string]interface{}{"key": "value"}},
			expected: []interface{}{map[string]interface{}{"key": "value"}},
			typeStr:  arrTypeUnknown,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			envSetting := &EnvSetting{}
			result := processArray(tt.input, envSetting)
			assert.Equal(t, tt.expected, result)
			assert.Equal(t, tt.typeStr, envSetting.Properties.Type)
		})
	}
}

// TestMarshalJSON tests the MarshalJSON method of the EnvSetting struct
func TestMarshalJSON(t *testing.T) {
	tests := []struct {
		name     string
		input    EnvSetting
		expected string
	}{
		{
			name: "String value",
			input: EnvSetting{
				Key:    "test_key",
				Values: "test string",
				Properties: EnvProperties{
					Persistent:   true,
					Static:       false,
					SessionValid: true,
					Shared:       false,
					Type:         varTypeStr,
					Source:       "source1",
				},
			},
			expected: `{"values":"test string","key":"test_key","properties":{"persistent":true,"static":false,"session_valid":true,"shared":false,"type":"string","source":"source1"}}`,
		},
		{
			name: "Number value",
			input: EnvSetting{
				Key:    "test_key",
				Values: 42.5,
				Properties: EnvProperties{
					Persistent:   false,
					Static:       true,
					SessionValid: false,
					Shared:       false,
					Type:         varTypeNum,
					Source:       "source2",
				},
			},
			expected: `{"values":42.5,"key":"test_key","properties":{"persistent":false,"static":true,"session_valid":false,"shared":false,"type":"number","source":"source2"}}`,
		},
		{
			name: "Array of strings",
			input: EnvSetting{
				Key:    "test_key",
				Values: []string{"a", "b", "c"},
				Properties: EnvProperties{
					Persistent:   true,
					Static:       true,
					SessionValid: true,
					Shared:       false,
					Type:         arrTypeStr,
					Source:       "source3",
				},
			},
			expected: `{"values":["a","b","c"],"key":"test_key","properties":{"persistent":true,"static":true,"session_valid":true,"shared":false,"type":"[]string","source":"source3"}}`,
		},
		{
			name: "Null value",
			input: EnvSetting{
				Key:    "test_key",
				Values: nil,
				Properties: EnvProperties{
					Persistent:   false,
					Static:       false,
					SessionValid: false,
					Shared:       false,
					Type:         varTypeNull,
					Source:       "source4",
				},
			},
			expected: `{"values":null,"key":"test_key","properties":{"persistent":false,"static":false,"session_valid":false,"shared":false,"type":"null","source":"source4"}}`,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			data, err := json.Marshal(&test.input)
			if err != nil {
				t.Fatalf("Failed to marshal JSON: %v", err)
			}

			if !reflect.DeepEqual(string(data), test.expected) {
				t.Errorf("Test %s failed. Expected: %s, Got: %s", test.name, test.expected, string(data))
			}
		})
	}
}

// TestUnmarshalJSON tests the UnmarshalJSON method of the EnvSetting struct
func TestUnmarshalJSON(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected EnvSetting
	}{
		{
			name:  "String value",
			input: `{"values":"test string","key":"test_key","properties":{"persistent":true,"static":false,"session_valid":true,"type":"string","source":"source1"}}`,
			expected: EnvSetting{
				Key:    "test_key",
				Values: "test string",
				Properties: EnvProperties{
					Persistent:   true,
					Static:       false,
					SessionValid: true,
					Type:         varTypeStr,
					Source:       "source1",
				},
			},
		},
		{
			name:  "Number value",
			input: `{"values":42.5,"key":"test_key","properties":{"persistent":false,"static":true,"session_valid":false,"type":"number","source":"source2"}}`,
			expected: EnvSetting{
				Key:    "test_key",
				Values: 42.5,
				Properties: EnvProperties{
					Persistent:   false,
					Static:       true,
					SessionValid: false,
					Type:         varTypeNum,
					Source:       "source2",
				},
			},
		},
		{
			name:  "Array of strings",
			input: `{"values":["a","b","c"],"key":"test_key","properties":{"persistent":true,"static":true,"session_valid":true,"type":"[]string","source":"source3"}}`,
			expected: EnvSetting{
				Key:    "test_key",
				Values: []string{"a", "b", "c"},
				Properties: EnvProperties{
					Persistent:   true,
					Static:       true,
					SessionValid: true,
					Type:         arrTypeStr,
					Source:       "source3",
				},
			},
		},
		{
			name:  "Null value",
			input: `{"values":null,"key":"test_key","properties":{"persistent":false,"static":false,"session_valid":false,"type":"null","source":"source4"}}`,
			expected: EnvSetting{
				Key:    "test_key",
				Values: nil,
				Properties: EnvProperties{
					Persistent:   false,
					Static:       false,
					SessionValid: false,
					Type:         varTypeNull,
					Source:       "source4",
				},
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var envSetting EnvSetting
			err := json.Unmarshal([]byte(test.input), &envSetting)
			if err != nil {
				t.Fatalf("Failed to unmarshal JSON: %v", err)
			}

			if !reflect.DeepEqual(envSetting, test.expected) {
				t.Errorf("Test %s failed. Expected: %v, Got: %v", test.name, test.expected, envSetting)
			}
		})
	}
}

// TestUnmarshalJSON tests the UnmarshalJSON method of the PluginParams struct
func TestPluginParams_MarshalJSON(t *testing.T) {
	tests := []struct {
		name     string
		input    PluginParams
		expected string
	}{
		{
			name: "String value",
			input: PluginParams{
				ArgName:  "test_arg",
				ArgValue: "test string",
				Properties: PluginParamsProperties{
					Type: varTypeStr,
				},
			},
			expected: `{"parameter_value":"test string","ArgName":"test_arg","ArgValue":"test string","Properties":{"Type":"string"}}`,
		},
		{
			name: "Number value",
			input: PluginParams{
				ArgName:  "test_arg",
				ArgValue: 42.5,
				Properties: PluginParamsProperties{
					Type: varTypeNum,
				},
			},
			expected: `{"parameter_value":42.5,"ArgName":"test_arg","ArgValue":42.5,"Properties":{"Type":"number"}}`,
		},
		{
			name: "Boolean value",
			input: PluginParams{
				ArgName:  "test_arg",
				ArgValue: true,
				Properties: PluginParamsProperties{
					Type: varTypeBool,
				},
			},
			expected: `{"parameter_value":true,"ArgName":"test_arg","ArgValue":true,"Properties":{"Type":"boolean"}}`,
		},
		{
			name: "Null value",
			input: PluginParams{
				ArgName:  "test_arg",
				ArgValue: nil,
				Properties: PluginParamsProperties{
					Type: varTypeNull,
				},
			},
			expected: `{"parameter_value":null,"ArgName":"test_arg","ArgValue":null,"Properties":{"Type":"null"}}`,
		},
		{
			name: "Array of strings",
			input: PluginParams{
				ArgName:  "test_arg3",
				ArgValue: []string{"a", "b", "c"},
				Properties: PluginParamsProperties{
					Type: arrTypeStr,
				},
			},
			expected: `{"parameter_value":["a","b","c"],"ArgName":"test_arg3","ArgValue":["a","b","c"],"Properties":{"Type":"[]string"}}`,
		},
		{
			name: "Array of numbers",
			input: PluginParams{
				ArgName:  "test_arg4",
				ArgValue: []float64{1.1, 2.2, 3.3},
				Properties: PluginParamsProperties{
					Type: arrTypeFloat64,
				},
			},
			expected: `{"parameter_value":[1.1,2.2,3.3],"ArgName":"test_arg4","ArgValue":[1.1,2.2,3.3],"Properties":{"Type":"[]float64"}}`,
		},
		{
			name: "Array of booleans",
			input: PluginParams{
				ArgName:  "test_arg5",
				ArgValue: []bool{true, false, true},
				Properties: PluginParamsProperties{
					Type: arrTypeBool,
				},
			},
			expected: `{"parameter_value":[true,false,true],"ArgName":"test_arg5","ArgValue":[true,false,true],"Properties":{"Type":"[]bool"}}`,
		},
		{
			name: "Unknown type",
			input: PluginParams{
				ArgName:  "test_null",
				ArgValue: nil,
				Properties: PluginParamsProperties{
					Type: varTypeUnknown,
				},
			},
			expected: `{"parameter_value":null,"ArgName":"test_null","ArgValue":null,"Properties":{"Type":"unknown"}}`,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			data, err := json.Marshal(&test.input)
			if err != nil {
				t.Fatalf("Failed to marshal JSON: %v", err)
			}

			if !reflect.DeepEqual(string(data), test.expected) {
				t.Errorf("Test %s failed. Expected: %s, Got: %s", test.name, test.expected, string(data))
			}
		})
	}
}

// TestProcessPlgArgArray tests the processPlgArgArray function
func TestProcessPlgArgArray(t *testing.T) {
	tests := []struct {
		name     string
		input    []interface{}
		expected interface{}
		typeStr  string
	}{
		{
			name:     "empty array",
			input:    []interface{}{},
			expected: []interface{}{},
			typeStr:  varTypeArr,
		},
		{
			name:     "array of strings",
			input:    []interface{}{"a", "b", "c"},
			expected: []string{"a", "b", "c"},
			typeStr:  arrTypeStr,
		},
		{
			name:     "array of numbers",
			input:    []interface{}{1.1, 2.2, 3.3},
			expected: []float64{1.1, 2.2, 3.3},
			typeStr:  arrTypeFloat64,
		},
		{
			name:     "array of booleans",
			input:    []interface{}{true, false, true},
			expected: []bool{true, false, true},
			typeStr:  arrTypeBool,
		},
		{
			name:     "array of unknown type",
			input:    []interface{}{map[string]interface{}{"key": "value"}},
			expected: []interface{}{map[string]interface{}{"key": "value"}},
			typeStr:  arrTypeUnknown,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			plgParams := &PluginParams{}
			result := processPlgArgArray(tt.input, plgParams)
			assert.Equal(t, tt.expected, result)
			assert.Equal(t, tt.typeStr, plgParams.Properties.Type)
		})
	}
}

// TestMarshalJSON tests the MarshalJSON method of the PluginParams struct
func TestMarshalJSON_PluginParams(t *testing.T) {
	tests := []struct {
		name     string
		input    PluginParams
		expected string
	}{
		{
			name: "String value",
			input: PluginParams{
				ArgName:  "test_arg",
				ArgValue: "test string",
				Properties: PluginParamsProperties{
					Type: varTypeStr,
				},
			},
			expected: `{"parameter_value":"test string","ArgName":"test_arg","ArgValue":"test string","Properties":{"Type":"string"}}`,
		},
		{
			name: "Number value",
			input: PluginParams{
				ArgName:  "test_arg",
				ArgValue: 42.5,
				Properties: PluginParamsProperties{
					Type: varTypeNum,
				},
			},
			expected: `{"parameter_value":42.5,"ArgName":"test_arg","ArgValue":42.5,"Properties":{"Type":"number"}}`,
		},
		{
			name: "Boolean value",
			input: PluginParams{
				ArgName:  "test_arg",
				ArgValue: true,
				Properties: PluginParamsProperties{
					Type: varTypeBool,
				},
			},
			expected: `{"parameter_value":true,"ArgName":"test_arg","ArgValue":true,"Properties":{"Type":"boolean"}}`,
		},
		{
			name: "Null value",
			input: PluginParams{
				ArgName:  "test_arg",
				ArgValue: nil,
				Properties: PluginParamsProperties{
					Type: varTypeNull,
				},
			},
			expected: `{"parameter_value":null,"ArgName":"test_arg","ArgValue":null,"Properties":{"Type":"null"}}`,
		},
		{
			name: "Array of strings",
			input: PluginParams{
				ArgName:  "test_arg3",
				ArgValue: []string{"a", "b", "c"},
				Properties: PluginParamsProperties{
					Type: arrTypeStr,
				},
			},
			expected: `{"parameter_value":["a","b","c"],"ArgName":"test_arg3","ArgValue":["a","b","c"],"Properties":{"Type":"[]string"}}`,
		},
		{
			name: "Array of numbers",
			input: PluginParams{
				ArgName:  "test_arg4",
				ArgValue: []float64{1.1, 2.2, 3.3},
				Properties: PluginParamsProperties{
					Type: arrTypeFloat64,
				},
			},
			expected: `{"parameter_value":[1.1,2.2,3.3],"ArgName":"test_arg4","ArgValue":[1.1,2.2,3.3],"Properties":{"Type":"[]float64"}}`,
		},
		{
			name: "Array of booleans",
			input: PluginParams{
				ArgName:  "test_arg5",
				ArgValue: []bool{true, false, true},
				Properties: PluginParamsProperties{
					Type: arrTypeBool,
				},
			},
			expected: `{"parameter_value":[true,false,true],"ArgName":"test_arg5","ArgValue":[true,false,true],"Properties":{"Type":"[]bool"}}`,
		},
		{
			name: "Unknown type",
			input: PluginParams{
				ArgName:  "test_null",
				ArgValue: nil,
				Properties: PluginParamsProperties{
					Type: varTypeUnknown,
				},
			},
			expected: `{"parameter_value":null,"ArgName":"test_null","ArgValue":null,"Properties":{"Type":"unknown"}}`,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			data, err := json.Marshal(&test.input)
			if err != nil {
				t.Fatalf("Failed to marshal JSON: %v", err)
			}

			if !reflect.DeepEqual(string(data), test.expected) {
				t.Errorf("Test %s failed. Expected: %s, Got: %s", test.name, test.expected, string(data))
			}
		})
	}
}
