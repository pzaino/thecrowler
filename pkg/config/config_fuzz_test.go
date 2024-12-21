//go:build go1.22
// +build go1.22

// Package config contains the configuration file parsing logic.
package config

import (
	"testing"
)

func FuzzParseConfig(f *testing.F) {
	// Add some initial seed inputs to start the fuzzing process
	f.Add([]byte(`
remote:
  host: "example.com"
  path: "/config"
  port: 8080
  timeout: 10
  type: "http"
  sslmode: "disable"
database:
  type: "postgres"
  host: "db.example.com"
  port: 5432
  user: "admin"
  password: "secret"
  dbname: "mydb"
  retry_time: 5
  ping_time: 10
  sslmode: "disable"
crawler:
  workers: 10
  interval: "10s"
  timeout: 30
  maintenance: 60
  source_screenshot: true
  full_site_screenshot: true
  max_depth: 5
  max_sources: 100
  delay: "1s"
api:
  host: "api.example.com"
  port: 8080
  timeout: 10
  content_search: true
  return_content: true
  sslmode: "disable"
  rate_limit: "10,5"
  enable_console: true
  readheader_timeout: 5
  read_timeout: 10
  write_timeout: 15
  return_404: false
selenium:
  - path: "/usr/bin/selenium"
    driver_path: "/usr/bin/chromedriver"
    type: "standalone"
    service_type: "standalone"
    port: 4444
    host: "selenium.example.com"
    headless: true
    use_service: true
    sslmode: "disable"
    proxy_url: "http://proxy.example.com"
image_storage:
  host: "storage.example.com"
  path: "/images"
  port: 9000
  region: "us-east-1"
  token: "mytoken"
  secret: "mysecret"
  timeout: 10
  type: "s3"
  sslmode: "disable"
file_storage:
  host: "storage.example.com"
  path: "/files"
  port: 9000
  region: "us-east-1"
  token: "mytoken"
  secret: "mysecret"
  timeout: 10
  type: "s3"
  sslmode: "disable"
http_headers:
  enabled: true
  timeout: 10
  follow_redirects: true
  ssl_discovery: true
network_info:
  dns:
    enabled: true
    timeout: 10
    rate_limit: "100ms"
  whois:
    enabled: true
    timeout: 10
    rate_limit: "100ms"
  netlookup:
    enabled: true
    timeout: 10
    rate_limit: "100ms"
  service_scout:
    enabled: true
    timeout: 60
    ping_scan: true
    connect_scan: true
    syn_scan: true
    udp_scan: true
    no_dns_resolution: true
    service_detection: true
    os_finger_print: true
    aggressive_scan: true
    script_scan:
      - "default"
    targets:
      - "target1.example.com"
    excluded_hosts:
      - "exclude1.example.com"
    timing_template: "3"
    host_timeout: "30m"
    min_rate: "10"
    max_retries: 3
    max_port_number: 9000
    source_port: 12345
    interface: "eth0"
    spoof_ip: "1.2.3.4"
    randomize_hosts: true
    data_length: 120
    delay: "1s"
    mtu_discovery: true
    scan_flags: "SYN"
rulesets_schema_path: "/schemas/ruleset.json"
rulesets:
  - schema_path: "/schemas/ruleset1.json"
    path:
      - "/rules/ruleset1.yaml"
    host: "ruleset.example.com"
    port: 8080
    region: "us-east-1"
    token: "mytoken"
    secret: "mysecret"
    timeout: 10
    type: "http"
    sslmode: "disable"
    refresh: 60
os: "linux"
debug_level: 3
`))

	f.Fuzz(func(t *testing.T, data []byte) {
		_, err := ParseConfig(data)
		if err != nil {
			t.Skip()
		}
	})
}
