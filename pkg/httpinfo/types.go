package httpinfo

import "net/http"

// HTTPInfoConfig is a struct to specify the configuration for header extraction
type HTTPInfoConfig struct {
	URL             string
	CustomHeader    map[string]string
	FollowRedirects bool
}

// HTTPInfoResponse is a struct to store the collected HTTP header information
type HTTPInfoResponse struct {
	URL                           string            `json:"url"`
	CustomHeaders                 map[string]string `json:"custom_headers"`
	FollowRedirects               bool              `json:"follow_redirects"`
	ResponseHeaders               http.Header       `json:"response_headers"`
	ServerType                    string            `json:"server_type"`
	PoweredBy                     string            `json:"powered_by"`
	AspNetVersion                 string            `json:"asp_net_version"`
	FrameOptions                  string            `json:"frame_options"`
	XSSProtection                 string            `json:"xss_protection"`
	ContentType                   string            `json:"content_type"`
	ContentTypeOptions            string            `json:"content_type_options"`
	ContentSecurityPolicy         string            `json:"content_security_policy"`
	StrictTransportSecurity       string            `json:"strict_transport_security"`
	AccessControlAllowOrigin      string            `json:"access_control_allow_origin"`
	AccessControlAllowMethods     string            `json:"access_control_allow_methods"`
	AccessControlAllowHeaders     string            `json:"access_control_allow_headers"`
	AccessControlAllowCredentials string            `json:"access_control_allow_credentials"`
	AccessControlExposeHeaders    string            `json:"access_control_expose_headers"`
	SetCookie                     string            `json:"set_cookie"`
	WwwAuthenticate               string            `json:"www_authenticate"`
	ProxyAuthenticate             string            `json:"proxy_authenticate"`
	KeepAlive                     string            `json:"keep_alive"`
	Expires                       string            `json:"expires"`
	LastModified                  string            `json:"last_modified"`
	ETag                          string            `json:"etag"`
	ContentDisposition            string            `json:"content_disposition"`
	ContentLength                 string            `json:"content_length"`
	ContentEncoding               string            `json:"content_encoding"`
	TransferEncoding              string            `json:"transfer_encoding"`
	HSTS                          string            `json:"hsts"`
	ResponseBodyInfo              []string          `json:"response_body_info"`
}
