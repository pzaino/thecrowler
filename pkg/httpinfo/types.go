// Copyright 2023 Paolo Fabio Zaino
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

// Package httpinfo provides functionality to extract HTTP header information
package httpinfo

import "net/http"

// Config is a struct to specify the configuration for header extraction
type Config struct {
	URL             string
	CustomHeader    map[string]string
	FollowRedirects bool
}

// Response is a struct to store the collected HTTP header information
type Response struct {
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
