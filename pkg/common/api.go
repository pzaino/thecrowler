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
	Tag               []string `json:"tag,omitempty"`
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
	Tags       []OpenAPITag           `json:"tags,omitempty"`
	Servers    []OpenAPIServer        `json:"servers,omitempty"`
	Paths      map[string]OpenAPIPath `json:"paths"`
	Components OpenAPIComponents      `json:"components,omitempty"`
}

// OpenAPIInfo represents the metadata information about the API, including its title, version, and an optional description.
type OpenAPIInfo struct {
	Title       string          `json:"title"`
	Version     string          `json:"version"`
	Description string          `json:"description,omitempty"`
	Contact     *OpenAPIContact `json:"contact,omitempty"`
}

// OpenAPIContact represents the contact information for the API, including name, URL, and email.
type OpenAPIContact struct {
	Name  string `json:"name,omitempty"`
	URL   string `json:"url,omitempty"`
	Email string `json:"email,omitempty"`
}

// OpenAPITag represents a tag that can be used to group API operations, including its name and an optional description.
type OpenAPITag struct {
	Name        string `json:"name"`
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
	AdditionalProperties any                      `json:"additionalProperties,omitempty"`
	Format               string                   `json:"format,omitempty"`
	Required             []string                 `json:"required,omitempty"`
}

// OpenAPIOptions represents the options for generating an OpenAPI specification, including the title, version, description, and an optional server URL for the API.
type OpenAPIOptions struct {
	Title       string
	Version     string
	Description string
	ServerURL   string          // optional, e.g. "http://localhost:8080"
	Contact     *OpenAPIContact // optional contact information for the API
	Tags        []OpenAPITag    // optional tags to include in the specification
}

// StdAPIQuery represents a standard query structure that can be used for API endpoints
// that support querying with pagination.
type StdAPIQuery struct {
	Q      string `json:"q" yaml:"q" desc:"Search query string, e.g. to filter results based on certain criteria. criteria can be expressed as a simple keyword, or as a dorking format, for example q=title:example (to search for items with 'example' in the title) or even more complex queries like q=title:Harry||Potter&summary:\"a magic story\"."`
	Limit  int    `json:"limit,omitempty" yaml:"limit,omitempty" desc:"Maximum number of results to return. e.g. limit=10 will return at most 10 results. If not specified, the default is typically 20 or 100 depending on the API design."`
	Offset int    `json:"offset,omitempty" yaml:"offset,omitempty" desc:"Pagination offset. e.g. offset=20 will skip the first 20 results and return results starting from the 21st item. This is used in conjunction with limit for paginated results."`
}

// StdAPIError represents a standard error response structure for API endpoints, including an error code, a short error message, and a more detailed message that can provide additional context about the error.
type StdAPIError struct {
	ErrCode int    `json:"error_code"`
	Err     string `json:"error"`
	Message string `json:"message"`
}

// StdAPISuccess represents a standard success response structure for API endpoints, including an operation code and a message that can provide additional context about the successful operation.
type StdAPISuccess struct {
	OpCode  int    `json:"op_code"`
	Message string `json:"message"`
}

const (
	getStr     = "get"
	postStr    = "post"
	typeObject = "object"
)

var apiRegistry []APIRoute
var apiRegistryMutex sync.Mutex

// RegisterAPIRoute registers a new API route with the given parameters.
func RegisterAPIRoute(
	path string,
	methods []string,
	description string,
	tags []string,
	consoleOnly bool,
	plugin bool,
	successStatus int,
	hasBody any,
	hasQuery any,
	hasResponse any,
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
		Tag:           tags,
		ConsoleOnly:   consoleOnly,
		Plugin:        plugin,
		SuccessStatus: successStatus,
		BodyType:      hasBody,
		QueryType:     hasQuery,
		ResponseType:  hasResponse,
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

	const inQuery = "query"
	const inPath = "path"

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

		// Check if the property is in the path
		typeIn := inQuery
		if strings.Contains(name, "{"+name+"}") {
			typeIn = inPath
		}

		out = append(out, OpenAPIParameter{
			Name:        name,
			In:          typeIn,
			Required:    false,
			Description: desc,
			Schema:      s,
		})
	}

	return out
}

func rawSchemaToOpenAPIParameterSchema(raw map[string]any) OpenAPISchema {
	s := OpenAPISchema{}

	if typ, ok := raw["type"].(string); ok {
		s.Type = typ
	}

	if format, ok := raw["format"].(string); ok {
		s.Format = format
	}

	if props, ok := raw["properties"].(map[string]any); ok {
		s.Properties = make(map[string]OpenAPISchema, len(props))
		for name, propRaw := range props {
			if propMap, ok := propRaw.(map[string]any); ok {
				s.Properties[name] = rawSchemaToOpenAPIParameterSchema(propMap)
			}
		}
	}

	if items, ok := raw["items"].(map[string]any); ok {
		itemSchema := rawSchemaToOpenAPIParameterSchema(items)
		s.Items = &itemSchema
	}

	if additionalProperties, ok := raw["additionalProperties"]; ok {
		s.AdditionalProperties = additionalProperties
	}

	if requiredRaw, ok := raw["required"].([]any); ok {
		for _, item := range requiredRaw {
			if name, ok := item.(string); ok {
				s.Required = append(s.Required, name)
			}
		}
	}

	if s.Type == "" {
		s.Type = "string"
	}

	return s
}

func queryParamsFromSchemaAny(schema any, path string) []OpenAPIParameter {
	switch s := schema.(type) {
	case nil:
		return nil

	case map[string]any:
		return queryParamsFromRawSchema(s, path)

	case *OpenAPISchema:
		if s == nil {
			return nil
		}
		return queryParamsFromOpenAPISchema(*s, path)

	case OpenAPISchema:
		return queryParamsFromOpenAPISchema(s, path)

	default:
		return queryParamsFromValue(s, path)
	}
}

func queryParamsFromOpenAPISchema(schema OpenAPISchema, path string) []OpenAPIParameter {
	if schema.Type != typeObject || len(schema.Properties) == 0 {
		return nil
	}

	requiredSet := make(map[string]bool, len(schema.Required))
	for _, name := range schema.Required {
		requiredSet[name] = true
	}

	params := make([]OpenAPIParameter, 0, len(schema.Properties))

	for name, prop := range schema.Properties {
		typeIn := "query"
		required := requiredSet[name]

		if strings.Contains(path, "{"+name+"}") {
			typeIn = "path"
			required = true
		}

		params = append(params, OpenAPIParameter{
			Name:     name,
			In:       typeIn,
			Required: required,
			Schema:   prop,
		})
	}

	return params
}

func queryParamsFromRawSchema(schema map[string]any, path string) []OpenAPIParameter {
	props, _ := schema["properties"].(map[string]any)
	if len(props) == 0 {
		return nil
	}

	requiredSet := map[string]bool{}
	if requiredRaw, ok := schema["required"].([]any); ok {
		for _, item := range requiredRaw {
			if name, ok := item.(string); ok {
				requiredSet[name] = true
			}
		}
	}

	params := make([]OpenAPIParameter, 0, len(props))

	for name, rawProp := range props {
		prop, ok := rawProp.(map[string]any)
		if !ok || len(prop) == 0 {
			continue
		}

		typeIn := "query"
		required := requiredSet[name]

		if strings.Contains(path, "{"+name+"}") {
			typeIn = "path"
			required = true
		}

		desc, _ := prop["description"].(string)

		params = append(params, OpenAPIParameter{
			Name:        name,
			In:          typeIn,
			Required:    required,
			Description: desc,
			Schema:      rawSchemaToOpenAPIParameterSchema(prop),
		})
	}

	return params
}

func queryParamsFromValue(v any, path string) []OpenAPIParameter {
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

	const inQuery = "query"
	const inPath = "path"

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
		if (s.Type == "") || (s.Type == typeObject) {
			s = OpenAPISchema{Type: "string"}
		}

		// Check if the field is in the path
		typeIn := inQuery
		if strings.Contains(path, "{"+name+"}") {
			typeIn = inPath
			// Path parameters must be required
			required = true
		}

		params = append(params, OpenAPIParameter{
			Name:        name,
			In:          typeIn,
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
var customTimeType = reflect.TypeOf(CustomTime{})

func schemaFromType(t reflect.Type) OpenAPISchema {
	return schemaFromTypeInternal(t, map[reflect.Type]bool{})
}

func isDateTimeField(name string) bool {
	n := strings.ToLower(name)
	return n == "created_at" ||
		n == "last_updated_at" ||
		strings.HasSuffix(n, "_at") ||
		strings.HasSuffix(n, "_time") ||
		strings.HasSuffix(n, "_timestamp")
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
			Type:                 typeObject,
			AdditionalProperties: &OpenAPISchema{},
		}
	}

	// interface{} => any
	if t.Kind() == reflect.Interface {
		return OpenAPISchema{} // empty schema means "any"
	}

	// Detect time-like types
	if t == timeType ||
		t == customTimeType ||
		t.ConvertibleTo(timeType) {
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
			return OpenAPISchema{Type: typeObject}
		}

		// map[string]interface{} => additionalProperties: {}
		valSchema := schemaFromTypeInternal(t.Elem(), seen)
		return OpenAPISchema{
			Type:                 typeObject,
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

			var fieldSchema OpenAPISchema

			// If it's a string type and name looks like a datetime field
			if f.Type.Kind() == reflect.String && isDateTimeField(name) {
				fieldSchema = OpenAPISchema{
					Type:   "string",
					Format: "date-time",
				}
			} else {
				fieldSchema = schemaFromTypeInternal(f.Type, seen)
			}
			props[name] = fieldSchema

			// Only mark required if explicitly declared
			if strings.EqualFold(f.Tag.Get("required"), "true") {
				requiredFields = append(requiredFields, name)
			}
		}

		s := OpenAPISchema{
			Type:       typeObject,
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

func schemaAnyFromJSON(schemaJSON string) any {
	schemaJSON = strings.TrimSpace(schemaJSON)
	if schemaJSON == "" {
		return nil
	}

	var schema map[string]any
	if err := json.Unmarshal([]byte(schemaJSON), &schema); err != nil {
		DebugMsg(DbgLvlError, "Invalid plugin OpenAPI schema: %v", err)
		return nil
	}

	if len(schema) == 0 {
		DebugMsg(DbgLvlError, "Invalid plugin OpenAPI schema: empty JSON object")
		return nil
	}

	if !looksLikeOpenAPISchema(schema) {
		DebugMsg(DbgLvlWarn, "Plugin OpenAPI schema does not contain common schema keywords")
	}

	return schema
}

func looksLikeOpenAPISchema(schema map[string]any) bool {
	commonKeys := []string{
		"$ref",
		"type",
		"format",
		"properties",
		"items",
		"required",
		"additionalProperties",
		"oneOf",
		"anyOf",
		"allOf",
		"enum",
		"description",
		"default",
		"minimum",
		"maximum",
		"minLength",
		"maxLength",
		"minItems",
		"maxItems",
		"pattern",
		"nullable",
	}

	for _, key := range commonKeys {
		if _, ok := schema[key]; ok {
			return true
		}
	}

	return false
}

func schemaForRouteValue(v any) any {
	switch s := v.(type) {
	case nil:
		return nil

	case map[string]any:
		return s

	case *OpenAPISchema:
		if s != nil {
			return s
		}
		return nil

	case OpenAPISchema:
		return s

	default:
		return schemaFromValue(s)
	}
}

// BuildOpenAPISpec generates an OpenAPI specification based on the registered API routes and the provided options.
func BuildOpenAPISpec(routes []APIRoute, opt OpenAPIOptions) OpenAPISpec {
	spec := OpenAPISpec{
		OpenAPI: "3.0.3",
		Info: OpenAPIInfo{
			Title:       defaultIfEmpty(opt.Title, "CROWler API"),
			Version:     defaultIfEmpty(opt.Version, "v1"),
			Description: opt.Description,
			Contact:     opt.Contact,
		},
		Tags:  opt.Tags,
		Paths: make(map[string]OpenAPIPath),
		Components: OpenAPIComponents{
			Schemas: map[string]any{},
		},
	}

	if strings.TrimSpace(opt.ServerURL) != "" {
		spec.Servers = []OpenAPIServer{
			{URL: strings.TrimRight(opt.ServerURL, "/")},
		}
	} else {
		// default to localhost with no port, since we don't necessarily know the port and it can be proxied
		spec.Servers = []OpenAPIServer{
			{URL: "http://localhost"},
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

			respSchema := schemaForRouteValue(r.ResponseType)

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
								Type:       typeObject,
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
								Type:       typeObject,
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

			const inQuery = "query"
			const inPath = "path"

			// Add query parameters from QueryType for GET
			if (method == getStr) && (r.QueryType != nil) {
				op.Parameters = append(op.Parameters, queryParamsFromSchemaAny(r.QueryType, r.Path)...)
			}

			bodySchema := schemaForRouteValue(r.BodyType)

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
	switch method {
	case strings.ToLower(http.MethodPost),
		strings.ToLower(http.MethodPut),
		strings.ToLower(http.MethodPatch),
		strings.ToLower(http.MethodDelete):
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
	if len(r.Tag) > 0 {
		return r.Tag
	}
	if r.Plugin {
		return []string{"Plugins"}
	}
	if r.ConsoleOnly {
		return []string{"Console"}
	}
	return []string{"API"}
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
