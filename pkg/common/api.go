// Package common package is used to store common functions and variables
package common

import (
	"net/http"
	"reflect"
	"strconv"
	"strings"
	"sync"
)

// APIRoute represents the structure of an API route, including its path, supported methods, description, and other metadata.
type APIRoute struct {
	Path          string   `json:"path"`
	Methods       []string `json:"methods"`
	Description   string   `json:"description"`
	RequiresQ     bool     `json:"requires_q"`
	ConsoleOnly   bool     `json:"console_only"`
	Plugin        bool     `json:"plugin"`
	SuccessStatus int      `json:"success_status,omitempty"` // optional, e.g. 200, 201, etc.

	BodyType      any
	BodySchemaRef string // optional future upgrade
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
	Schema OpenAPISchema `json:"schema"`
}

// OpenAPIResponse represents the structure of a response for an API operation, including its description and any additional metadata that may be relevant for documenting the response.
type OpenAPIResponse struct {
	Description string `json:"description"`
}

// OpenAPISchema represents the structure of a schema definition used in parameters, request bodies, and responses, including its type, properties (for object types), and items (for array types).
type OpenAPISchema struct {
	Type                 string                   `json:"type,omitempty"`
	Properties           map[string]OpenAPISchema `json:"properties,omitempty"`
	Items                *OpenAPISchema           `json:"items,omitempty"`
	AdditionalProperties *OpenAPISchema           `json:"additionalProperties,omitempty"`
	Format               string                   `json:"format,omitempty"`
}

// OpenAPIOptions represents the options for generating an OpenAPI specification, including the title, version, description, and an optional server URL for the API.
type OpenAPIOptions struct {
	Title       string
	Version     string
	Description string
	ServerURL   string // optional, e.g. "http://localhost:8080"
}

const getStr = "get"

var apiRegistry []APIRoute
var apiRegistryMutex sync.Mutex

// RegisterAPIRoute registers a new API route with the given parameters.
func RegisterAPIRoute(
	path string,
	methods []string,
	description string,
	requiresQ bool,
	consoleOnly bool,
	plugin bool,
	successStatus int,
	hasBody any,
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
		RequiresQ:     requiresQ,
		ConsoleOnly:   consoleOnly,
		Plugin:        plugin,
		SuccessStatus: successStatus,
		BodyType:      hasBody,
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

func schemaFromType(t reflect.Type) OpenAPISchema {
	// unwrap pointers
	for t.Kind() == reflect.Ptr {
		t = t.Elem()
	}

	// interface{} => any
	if t.Kind() == reflect.Interface {
		return OpenAPISchema{} // empty schema means "any"
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
		item := schemaFromType(t.Elem())
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
		valSchema := schemaFromType(t.Elem())
		return OpenAPISchema{
			Type:                 "object",
			AdditionalProperties: &valSchema,
		}

	case reflect.Struct:
		props := make(map[string]OpenAPISchema)

		for i := 0; i < t.NumField(); i++ {
			f := t.Field(i)

			// skip unexported fields
			if f.PkgPath != "" {
				continue
			}

			name, ok := jsonFieldName(f)
			if !ok {
				continue
			}

			props[name] = schemaFromType(f.Type)
		}

		return OpenAPISchema{
			Type:       "object",
			Properties: props,
		}

	default:
		// safe fallback: free-form
		return OpenAPISchema{}
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

			responses := map[string]OpenAPIResponse{
				strconv.Itoa(r.SuccessStatus): {
					Description: http.StatusText(r.SuccessStatus),
				},
				"400": {Description: "Bad Request"},
				"500": {Description: "Internal Server Error"},
			}

			op := OpenAPIOperation{
				Summary:     shortSummary(r.Description),
				Description: r.Description,
				OperationID: makeOperationID(method, r.Path),
				Tags:        tagsForRoute(r),
				Responses:   responses,
			}

			// Add q param for GET (and optionally for others too if you support q in query string)
			if r.RequiresQ && (method == getStr) {
				op.Parameters = append(op.Parameters, OpenAPIParameter{
					Name:        "q",
					In:          "query",
					Required:    method == getStr,
					Description: "Search query",
					Schema:      OpenAPISchema{Type: "string"},
				})
			}

			// Add JSON body for non-GET requests if BodyType is something other than nil.
			if r.BodyType != nil && methodAllowsBody(method) {
				op.RequestBody = &OpenAPIRequestBody{
					Required: true,
					Content: map[string]OpenAPIContent{
						"application/json": {
							Schema: schemaFromValue(r.BodyType),
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
