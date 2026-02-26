// Package common package is used to store common functions and variables
package common

import (
	"encoding/json"
	"net/http"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"time"
)

// APIRoute represents the structure of an API route, including its path, supported methods, description, and other metadata.
type APIRoute struct {
	Path              string   `json:"path"`
	Methods           []string `json:"methods"`
	Description       string   `json:"description"`
	RequiresQ         bool     `json:"requires_q"`
	ConsoleOnly       bool     `json:"console_only"`
	Plugin            bool     `json:"plugin"`
	SuccessStatus     int      `json:"success_status,omitempty"`   // optional, e.g. 200, 201, etc.
	BodyType          any      `json:"body_type,omitempty"`        // optional, can be used to generate OpenAPI schema for request body
	BodySchemaRef     string   `json:"body_schema_ref,omitempty"`  // url to schema in components, optional
	QueryType         any      `json:"query_type,omitempty"`       // optional, can be used to generate OpenAPI schema for query parameters
	QuerySchemaRef    string   `json:"query_schema_ref,omitempty"` // url to schema in components, optional
	ResponseType      any      `json:"response_type,omitempty"`
	ResponseSchemaRef string   `json:"response_schema_ref,omitempty"` // url to schema in components, optional
}

// OpenAPISpec represents the structure of an OpenAPI specification, including the OpenAPI version, API information, servers, paths, and components.
type OpenAPISpec struct {
	OpenAPI    string                 `json:"openapi"`
	Info       OpenAPIInfo            `json:"info"`
	Servers    []OpenAPIServer        `json:"servers,omitempty"`
	Paths      map[string]OpenAPIPath `json:"paths"`
	Components OpenAPIComponents      `json:"components,omitempty"`
}

// OpenAPIInfo represents the metadata information about the API, including its title, version, and an optional description.
type OpenAPIInfo struct {
	Title       string `json:"title"`
	Version     string `json:"version"`
	Description string `json:"description,omitempty"`
}

// OpenAPIServer represents a server that serves the API, including its URL and an optional description.
type OpenAPIServer struct {
	URL         string `json:"url"`
	Description string `json:"description,omitempty"`
}

// OpenAPIComponents represents the reusable components of the API, such as schemas, which can be referenced throughout the specification.
type OpenAPIComponents struct {
	Schemas map[string]any `json:"schemas,omitempty"`
}

// OpenAPIPath represents the structure of an API path, which maps HTTP methods to their corresponding operations (e.g., GET, POST, etc.).
type OpenAPIPath map[string]OpenAPIOperation

// OpenAPIOperation represents the structure of an API operation, including its summary, description, operation ID, tags, parameters, request body, and responses.
type OpenAPIOperation struct {
	Summary     string                     `json:"summary,omitempty"`
	Description string                     `json:"description,omitempty"`
	OperationID string                     `json:"operationId,omitempty"`
	Tags        []string                   `json:"tags,omitempty"`
	Parameters  []OpenAPIParameter         `json:"parameters,omitempty"`
	RequestBody *OpenAPIRequestBody        `json:"requestBody,omitempty"`
	Responses   map[string]OpenAPIResponse `json:"responses"`
}

// OpenAPIParameter represents the structure of a parameter used in an API operation, including its name, location (e.g., query, path, header), whether it is required, a description, and its schema definition.
type OpenAPIParameter struct {
	Name        string        `json:"name"`
	In          string        `json:"in"`
	Required    bool          `json:"required,omitempty"`
	Description string        `json:"description,omitempty"`
	Schema      OpenAPISchema `json:"schema"`
}

// OpenAPIRequestBody represents the structure of a request body used in an API operation, including whether it is required and the content types it supports, along with their corresponding schema definitions.
type OpenAPIRequestBody struct {
	Required bool                      `json:"required,omitempty"`
	Content  map[string]OpenAPIContent `json:"content"`
}

// OpenAPIContent represents the structure of the content for a specific media type in a request body, including its schema definition.
type OpenAPIContent struct {
	//Schema OpenAPISchema `json:"schema"`
	Schema any `json:"schema"`
}

// OpenAPIResponse represents the structure of a response for an API operation, including its description and any additional metadata that may be relevant for documenting the response.
type OpenAPIResponse struct {
	Description string                    `json:"description"`
	Content     map[string]OpenAPIContent `json:"content,omitempty"`
}

// OpenAPISchema represents the structure of a schema definition used in parameters, request bodies, and responses, including its type, properties (for object types), and items (for array types).
type OpenAPISchema struct {
	Type                 string                   `json:"type,omitempty"`
	Properties           map[string]OpenAPISchema `json:"properties,omitempty"`
	Items                *OpenAPISchema           `json:"items,omitempty"`
	AdditionalProperties *OpenAPISchema           `json:"additionalProperties,omitempty"`
	Format               string                   `json:"format,omitempty"`
	Required             []string                 `json:"required,omitempty"`
}

// OpenAPIOptions represents the options for generating an OpenAPI specification, including the title, version, description, and an optional server URL for the API.
type OpenAPIOptions struct {
	Title       string
	Version     string
	Description string
	ServerURL   string // optional, e.g. "http://localhost:8080"
}

// StdAPIQuery represents a standard query structure that can be used for API endpoints
// that support querying with pagination.
type StdAPIQuery struct {
	Q      string `json:"q" yaml:"q" desc:"Search query string, e.g. to filter results based on certain criteria. criteria can be expressed as a simple keyword, or as a dorking format, for example q=title:example (to search for items with 'example' in the title) or even more complex queries like q=title:Harry||Potter&summary:\"a magic story\"."`
	Limit  int    `json:"limit,omitempty" yaml:"limit,omitempty" desc:"Maximum number of results to return. e.g. limit=10 will return at most 10 results. If not specified, the default is typically 20 or 100 depending on the API design."`
	Offset int    `json:"offset,omitempty" yaml:"offset,omitempty" desc:"Pagination offset. e.g. offset=20 will skip the first 20 results and return results starting from the 21st item. This is used in conjunction with limit for paginated results."`
}

type StdAPIError struct {
	ErrCode int    `json:"error_code"`
	Err     string `json:"error"`
	Message string `json:"message"`
}

const getStr = "get"

var apiRegistry []APIRoute
var apiRegistryMutex sync.Mutex

// RegisterAPIRoute registers a new API route with the given parameters.
func RegisterAPIRoute(
	path string,
	methods []string,
	description string,
	consoleOnly bool,
	plugin bool,
	successStatus int,
	hasBody any,
	hasQuery any,
) {
	if successStatus == 0 {
		successStatus = 200
	}

	apiRegistryMutex.Lock()
	defer apiRegistryMutex.Unlock()

	apiRegistry = append(apiRegistry, APIRoute{
		Path:          path,
		Methods:       methods,
		Description:   description,
		ConsoleOnly:   consoleOnly,
		Plugin:        plugin,
		SuccessStatus: successStatus,
		BodyType:      hasBody,
		QueryType:     hasQuery,
	})
}

// RegisterAPIPluginRoute is a helper function to register an API route for a plugin, which includes additional metadata for the plugin's API. The hasBody and hasQuery parameters can be used to specify the expected structure of the request body and query parameters, respectively, which can be used for generating OpenAPI documentation and validating incoming requests.
func RegisterAPIPluginRoute(
	path string,
	methods []string,
	description string,
	consoleOnly bool,
	successStatus int,
	querySchemaJSON string,
	requestSchemaJSON string,
	responseSchemaJSON string,
) {
	// store into APIRoute using BodyType/QueryType nil, but stash JSON in new fields
	if successStatus == 0 {
		successStatus = 200
	}

	apiRegistryMutex.Lock()
	defer apiRegistryMutex.Unlock()

	// Get each method and check if it allows body. If it does and requestSchemaJSON is empty, log a warning.
	for _, method := range methods {
		if methodAllowsBody(method) && strings.TrimSpace(requestSchemaJSON) == "" {
			DebugMsg(DbgLvlWarn,
				"API plugin route '%s' allows body but api_request_json is not defined",
				path,
			)
		}
	}

	apiRegistry = append(apiRegistry, APIRoute{
		Path:              path,
		Methods:           methods,
		Description:       description,
		ConsoleOnly:       consoleOnly,
		Plugin:            true,
		SuccessStatus:     successStatus,
		QueryType:         schemaAnyFromJSON(querySchemaJSON),
		BodyType:          schemaAnyFromJSON(requestSchemaJSON),
		ResponseType:      schemaAnyFromJSON(responseSchemaJSON),
		QuerySchemaRef:    "", // not used directly, but can be set to a reference in components if needed
		BodySchemaRef:     "", // not used directly, but can be set to a reference in components if needed
		ResponseSchemaRef: "", // not used directly, but can be set to a reference in components if needed
	})
}

// GetAPIRoutes returns the list of registered API routes.
func GetAPIRoutes() []APIRoute {
	apiRegistryMutex.Lock()
	defer apiRegistryMutex.Unlock()

	out := make([]APIRoute, len(apiRegistry))
	copy(out, apiRegistry)
	return out
}

func parametersFromJSONSchemaObject(schemaJSON string) []OpenAPIParameter {
	schemaJSON = strings.TrimSpace(schemaJSON)
	if schemaJSON == "" {
		return nil
	}

	var raw map[string]any
	if err := json.Unmarshal([]byte(schemaJSON), &raw); err != nil {
		return nil
	}

	props, _ := raw["properties"].(map[string]any)
	if len(props) == 0 {
		return nil
	}

	out := make([]OpenAPIParameter, 0, len(props))
	for name, v := range props {
		prop, _ := v.(map[string]any)
		if len(prop) == 0 {
			continue
		}

		typ, _ := prop["type"].(string)
		format, _ := prop["format"].(string)
		desc, _ := prop["description"].(string)

		// very small mapper for common cases
		s := OpenAPISchema{Type: typ, Format: format}
		if s.Type == "" {
			s.Type = "string"
		}

		out = append(out, OpenAPIParameter{
			Name:        name,
			In:          "query",
			Required:    false,
			Description: desc,
			Schema:      s,
		})
	}

	return out
}

func queryParamsFromValue(v any) []OpenAPIParameter {
	if v == nil {
		return nil
	}
	t := reflect.TypeOf(v)
	for t.Kind() == reflect.Ptr {
		t = t.Elem()
	}
	if t.Kind() != reflect.Struct {
		return nil
	}

	var params []OpenAPIParameter

	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)

		// skip unexported
		if f.PkgPath != "" {
			continue
		}

		// use query tag if present, else json tag
		name := strings.TrimSpace(f.Tag.Get("query"))
		if name == "" {
			var ok bool
			name, ok = jsonFieldName(f)
			if !ok {
				continue
			}
		}
		if name == "-" {
			continue
		}

		// optional: required tag
		required := false
		if strings.EqualFold(strings.TrimSpace(f.Tag.Get("required")), "true") {
			required = true
		}

		s := schemaFromType(f.Type)

		// Query params cannot be arbitrary objects in practice.
		// If schema is object/any, degrade to string (safe default).
		if s.Type == "" || s.Type == "object" {
			s = OpenAPISchema{Type: "string"}
		}

		params = append(params, OpenAPIParameter{
			Name:        name,
			In:          "query",
			Required:    required,
			Description: strings.TrimSpace(f.Tag.Get("desc")),
			Schema:      s,
		})
	}

	return params
}

func jsonFieldName(f reflect.StructField) (string, bool) {
	tag := strings.TrimSpace(f.Tag.Get("json"))
	if tag == "" {
		return "", false
	}
	name := strings.Split(tag, ",")[0]
	name = strings.TrimSpace(name)
	if name == "" || name == "-" {
		return "", false
	}
	return name, true
}

func schemaFromValue(v any) OpenAPISchema {
	t := reflect.TypeOf(v)
	if t == nil {
		return OpenAPISchema{} // any
	}
	return schemaFromType(t)
}

var rawMessageType = reflect.TypeOf(json.RawMessage{})
var timeType = reflect.TypeOf(time.Time{})
var CustTime = reflect.TypeOf(CustomTime{})

func schemaFromType(t reflect.Type) OpenAPISchema {
	return schemaFromTypeInternal(t, map[reflect.Type]bool{})
}

func schemaFromTypeInternal(t reflect.Type, seen map[reflect.Type]bool) OpenAPISchema {
	// unwrap pointers
	for t.Kind() == reflect.Ptr {
		t = t.Elem()
	}

	// cycle detection: if we've seen this type before, return empty schema to avoid infinite recursion
	if seen[t] {
		return OpenAPISchema{}
	}
	seen[t] = true
	defer delete(seen, t)

	// Detect json.RawMessage
	if t == rawMessageType {
		return OpenAPISchema{
			Type:                 "object",
			AdditionalProperties: &OpenAPISchema{},
		}
	}

	// interface{} => any
	if t.Kind() == reflect.Interface {
		return OpenAPISchema{} // empty schema means "any"
	}

	// Detect time.Time
	if t == timeType {
		return OpenAPISchema{
			Type:   "string",
			Format: "date-time",
		}
	}

	// Detect CustomTime
	if t == CustTime {
		return OpenAPISchema{
			Type:   "string",
			Format: "date-time",
		}
	}

	switch t.Kind() {
	case reflect.Bool:
		return OpenAPISchema{Type: "boolean"}

	case reflect.String:
		return OpenAPISchema{Type: "string"}

	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32:
		return OpenAPISchema{Type: "integer", Format: "int32"}

	case reflect.Int64:
		return OpenAPISchema{Type: "integer", Format: "int64"}

	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32:
		return OpenAPISchema{Type: "integer", Format: "int32"}

	case reflect.Uint64:
		return OpenAPISchema{Type: "integer", Format: "int64"}

	case reflect.Float32:
		return OpenAPISchema{Type: "number", Format: "float"}

	case reflect.Float64:
		return OpenAPISchema{Type: "number", Format: "double"}

	case reflect.Slice, reflect.Array:
		item := schemaFromTypeInternal(t.Elem(), seen)
		return OpenAPISchema{
			Type:  "array",
			Items: &item,
		}

	case reflect.Map:
		// OpenAPI requires map keys to be string for JSON objects. If not string, fall back.
		if t.Key().Kind() != reflect.String {
			return OpenAPISchema{Type: "object"}
		}

		// map[string]interface{} => additionalProperties: {}
		valSchema := schemaFromTypeInternal(t.Elem(), seen)
		return OpenAPISchema{
			Type:                 "object",
			AdditionalProperties: &valSchema,
		}

	case reflect.Struct:
		props := make(map[string]OpenAPISchema)
		requiredFields := []string{}

		for i := 0; i < t.NumField(); i++ {
			f := t.Field(i)

			if f.PkgPath != "" {
				continue
			}

			fieldType := f.Type
			for fieldType.Kind() == reflect.Ptr {
				fieldType = fieldType.Elem()
			}

			jsonTag := strings.TrimSpace(f.Tag.Get("json"))

			// Flatten ONLY if:
			// - Anonymous
			// - No json tag at all
			if f.Anonymous && jsonTag == "" && fieldType.Kind() == reflect.Struct {
				embeddedSchema := schemaFromTypeInternal(fieldType, seen)

				for k, v := range embeddedSchema.Properties {
					props[k] = v
				}

				requiredFields = append(requiredFields, embeddedSchema.Required...)
				continue
			}

			name, ok := jsonFieldName(f)
			if !ok {
				continue
			}

			fieldSchema := schemaFromTypeInternal(f.Type, seen)
			props[name] = fieldSchema

			// Only mark required if explicitly declared
			if strings.EqualFold(f.Tag.Get("required"), "true") {
				requiredFields = append(requiredFields, name)
			}
		}

		s := OpenAPISchema{
			Type:       "object",
			Properties: props,
		}

		if len(requiredFields) > 0 {
			s.Required = requiredFields
		}

		return s

	default:
		// safe fallback: free-form
		return OpenAPISchema{}
	}
}

func schemaAnyFromJSON(schemaJSON string) *OpenAPISchema {
	schemaJSON = strings.TrimSpace(schemaJSON)
	if schemaJSON == "" {
		return nil
	}

	var schema OpenAPISchema
	if err := json.Unmarshal([]byte(schemaJSON), &schema); err != nil {
		DebugMsg(DbgLvlError, "Invalid plugin OpenAPI schema: %v", err)
		return nil
	}

	return &schema
}

// BuildOpenAPISpec generates an OpenAPI specification based on the registered API routes and the provided options.
func BuildOpenAPISpec(routes []APIRoute, opt OpenAPIOptions) OpenAPISpec {
	spec := OpenAPISpec{
		OpenAPI: "3.0.3",
		Info: OpenAPIInfo{
			Title:       defaultIfEmpty(opt.Title, "CROWler API"),
			Version:     defaultIfEmpty(opt.Version, "v1"),
			Description: opt.Description,
		},
		Paths: make(map[string]OpenAPIPath),
		Components: OpenAPIComponents{
			Schemas: map[string]any{},
		},
	}

	if strings.TrimSpace(opt.ServerURL) != "" {
		spec.Servers = []OpenAPIServer{
			{URL: strings.TrimRight(opt.ServerURL, "/")},
		}
	}

	for _, r := range routes {
		if strings.TrimSpace(r.Path) == "" || len(r.Methods) == 0 {
			continue
		}

		if _, ok := spec.Paths[r.Path]; !ok {
			spec.Paths[r.Path] = OpenAPIPath{}
		}

		for _, m := range r.Methods {
			method := strings.ToLower(strings.TrimSpace(m))
			if method == "" {
				continue
			}

			var respSchema any

			switch v := r.ResponseType.(type) {
			case *OpenAPISchema:
				if v != nil {
					respSchema = v
				}
			case OpenAPISchema:
				respSchema = v
			default:
				if v != nil {
					respSchema = schemaFromValue(v)
				}
			}

			successCode := strconv.Itoa(r.SuccessStatus)

			successResponse := OpenAPIResponse{
				Description: http.StatusText(r.SuccessStatus),
			}

			if respSchema != nil {
				successResponse.Content = map[string]OpenAPIContent{
					"application/json": {
						Schema: respSchema,
					},
				}
			}

			responses := map[string]OpenAPIResponse{
				successCode: successResponse,
				"400": {
					Description: "Bad Request",
					Content: map[string]OpenAPIContent{
						"application/json": {
							Schema: OpenAPISchema{
								Type:       "object",
								Properties: schemaFromValue(StdAPIError{}).Properties,
							},
						},
					},
				},
				"500": {
					Description: "Internal Server Error",
					Content: map[string]OpenAPIContent{
						"application/json": {
							Schema: OpenAPISchema{
								Type:       "object",
								Properties: schemaFromValue(StdAPIError{}).Properties,
							},
						},
					},
				},
			}

			op := OpenAPIOperation{
				Summary:     shortSummary(r.Description),
				Description: r.Description,
				OperationID: makeOperationID(method, r.Path),
				Tags:        tagsForRoute(r),
				Responses:   responses,
			}

			// Add query parameters from QueryType for GET
			if (method == getStr) && (r.QueryType != nil) {

				switch v := r.QueryType.(type) {

				case *OpenAPISchema:
					if v == nil {
						break
					}
					// plugin JSON schema
					if v.Type == "object" && v.Properties != nil {
						for name, prop := range v.Properties {
							op.Parameters = append(op.Parameters, OpenAPIParameter{
								Name:   name,
								In:     "query",
								Schema: prop,
							})
						}
					}

				case OpenAPISchema:
					if v.Type == "object" && v.Properties != nil {
						for name, prop := range v.Properties {
							op.Parameters = append(op.Parameters, OpenAPIParameter{
								Name:   name,
								In:     "query",
								Schema: prop,
							})
						}
					}

				default:
					// normal struct reflection
					if v != nil {
						op.Parameters = append(op.Parameters, queryParamsFromValue(v)...)
					}
				}
			}

			var bodySchema any

			switch v := r.BodyType.(type) {
			case *OpenAPISchema:
				if v != nil {
					bodySchema = v
				}
			case OpenAPISchema:
				bodySchema = v
			default:
				if v != nil {
					bodySchema = schemaFromValue(v)
				}
			}

			// Add JSON body for non-GET requests if BodyType is something other than nil.
			if (bodySchema != nil) && methodAllowsBody(method) {
				op.RequestBody = &OpenAPIRequestBody{
					Required: true,
					Content: map[string]OpenAPIContent{
						"application/json": {
							Schema: bodySchema,
						},
					},
				}
			}

			spec.Paths[r.Path][method] = op
		}
	}

	return spec
}

func defaultIfEmpty(v, def string) string {
	if strings.TrimSpace(v) == "" {
		return def
	}
	return v
}

func methodAllowsBody(method string) bool {
	method = strings.ToLower(strings.TrimSpace(method))
	switch method {
	case http.MethodPost,
		http.MethodPut,
		http.MethodPatch,
		http.MethodDelete:
		return true
	default:
		return false
	}
}

func makeOperationID(method, path string) string {
	clean := strings.Trim(path, "/")
	clean = strings.ReplaceAll(clean, "/", "_")

	// Replace anything not [A-Za-z0-9_] with underscore
	b := make([]rune, 0, len(clean))
	for _, r := range clean {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '_' {
			b = append(b, r)
		} else {
			b = append(b, '_')
		}
	}
	clean = string(b)

	if clean == "" {
		clean = "root"
	}
	return method + "_" + clean
}

func tagsForRoute(r APIRoute) []string {
	if r.Plugin {
		return []string{"plugins"}
	}
	if r.ConsoleOnly {
		return []string{"console"}
	}
	return []string{"api"}
}

func shortSummary(desc string) string {
	d := strings.TrimSpace(desc)
	if d == "" {
		return ""
	}
	// Keep it short for UI, but avoid being clever.
	if len(d) > 80 {
		return d[:80]
	}
	return d
}
