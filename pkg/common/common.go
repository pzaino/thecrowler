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

// Package common package is used to store common functions and variables
package common

import (
	"context"
	"errors"
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
)

const (
	errIPNotAllowed = "ip address is not allowed"
)

var (
	// DebugLevel is the debug level for logging
	debugLevel   DbgLevel
	loggerPrefix string
	sysLoc       *time.Location
)

// GenerateUID generates a unique identifier using Google's UID format and package
func GenerateUID() string {
	return uuid.New().String()
}

// GetEngineID returns the engine ID
func GetEngineID() string {
	// Retrieve process PID
	pid := os.Getpid()

	// Retrieve process PPID
	ppid := os.Getppid()

	// Retrieve default network address
	hostname, err := os.Hostname()
	if err != nil {
		hostname = LoalhostStr
	}

	// create process instance name: <hostname>:<pid>:<ppid>
	processName := hostname + ":" + strconv.Itoa(pid) + ":" + strconv.Itoa(ppid)

	return processName
}

// InitLogger initializes the logger
func InitLogger(appName string) {
	log.SetOutput(os.Stdout)

	// create process instance name: <hostname>:<pid>:<ppid>
	processName := GetEngineID()

	// Setting the log prefix
	loggerPrefix = appName + " [" + processName + "]: "

	// Setting the log flags (date, time, microseconds, short file name)
	log.SetFlags(log.LstdFlags | log.Ldate | log.Ltime | log.Lmicroseconds)

	// Check if TZ is set in environment
	if tz := os.Getenv("TZ"); tz != "" {
		var err error
		sysLoc, err = time.LoadLocation(tz)
		if err != nil {
			log.Printf("Warning: Invalid TZ value %q. Falling back to default: %v", tz, err)
		} else {
			time.Local = sysLoc
			log.Printf("Timezone set to: %s", sysLoc)
		}
	} else {
		log.Printf("Warning: TZ environment variable not set. Using system default timezone.")
	}
}

// SetLoggerPrefix sets the logger prefix
func SetLoggerPrefix(prefix string) {
	loggerPrefix = prefix
}

// UpdateLoggerConfig Updates the logger configuration
func UpdateLoggerConfig(logType string) {
	if debugLevel > 0 {
		log.SetFlags(log.LstdFlags | log.Ldate | log.Ltime | log.Lmicroseconds | log.Lshortfile)
	} else {
		log.SetFlags(log.LstdFlags | log.Ldate | log.Ltime | log.Lmicroseconds)
	}
	logType = strings.ToLower(strings.TrimSpace(logType))
	if logType == "file" {
		// Set log to log to a file
		logFile, err := os.OpenFile("log.txt", os.O_RDWR|os.O_CREATE|os.O_APPEND, 0600)
		if err == nil {
			log.SetOutput(logFile)
		}
	}
	// TODO: Add support for syslog
}

// SetDebugLevel allows to set the current debug level
func SetDebugLevel(dbgLvl DbgLevel) {
	if dbgLvl > DbgLvlDebug5 {
		debugLevel = DbgLvlDebug5
		return
	}
	if dbgLvl < DbgLvlFatal {
		debugLevel = DbgLvlFatal
		return
	}
	debugLevel = dbgLvl
}

// GetDebugLevel returns the value of the current debug level
func GetDebugLevel() DbgLevel {
	return debugLevel
}

// SetDebugLevelFromString sets the debug level based on a string input
func SetDebugLevelFromString(dbgLvlStr string) {
	switch strings.ToLower(strings.TrimSpace(dbgLvlStr)) {
	case "fatal":
		SetDebugLevel(DbgLvlFatal)
	case "error":
		SetDebugLevel(DbgLvlError)
	case "info":
		SetDebugLevel(DbgLvlInfo)
	case "debug":
		SetDebugLevel(DbgLvlDebug)
	case "debug1":
		SetDebugLevel(DbgLvlDebug1)
	case "debug2":
		SetDebugLevel(DbgLvlDebug2)
	case "debug3":
		SetDebugLevel(DbgLvlDebug3)
	case "debug4":
		SetDebugLevel(DbgLvlDebug4)
	case "debug5":
		SetDebugLevel(DbgLvlDebug5)
	default:
		SetDebugLevel(DbgLvlInfo) // Default to Info level
	}
}

// DebugMsg is a function that prints debug information
func DebugMsg(dbgLvl DbgLevel, msg string, args ...interface{}) {
	// For always-log messages (Info, Warning, Error, Fatal)
	if dbgLvl <= DbgLvlInfo {
		if dbgLvl == DbgLvlError {
			// For Error messages, log always
			log.Printf(loggerPrefix+"Error "+msg, args...)
			return
		}
		log.Printf(loggerPrefix+msg, args...)
		if dbgLvl == DbgLvlFatal {
			os.Exit(1)
		}
		return
	}
	if debugLevel >= dbgLvl {
		// For Debug messages, log only if the set debug level is equal or higher
		log.Printf(loggerPrefix+msg, args...)
	}
}

//// ----- File related shared functions ----- ////

// GetFileExt returns a file extension (if any)
func GetFileExt(filePath string) string {
	fileType := strings.ToLower(strings.TrimSpace(filepath.Ext(filePath)))
	fileType = strings.TrimPrefix(fileType, ".")
	return fileType
}

// IsPathCorrect checks if the given path exists
func IsPathCorrect(path string) bool {
	if _, err := os.Stat(path); os.IsNotExist(err) {
		return false
	}
	return true
}

//// ----- HTTP related shared functions ----- ////

// URLToHost extracts the host from a URL
func URLToHost(url string) string {
	host := url
	if strings.Contains(host, "://") {
		host = host[strings.Index(host, "://")+3:]
	}
	if strings.Contains(host, "/") {
		host = host[:strings.Index(host, "/")]
	}
	host = strings.TrimSuffix(host, "/")
	host = strings.TrimSpace(host)
	return host
}

// HostToIP returns the IP address of a given host
func HostToIP(host string) []string {
	ips, err := net.LookupIP(host)
	if err != nil {
		return []string{}
	}
	ipList := make([]string, 0, len(ips))
	for _, ip := range ips {
		ipList = append(ipList, ip.String())
	}
	return ipList
}

// CheckIPVersion checks the IP version of the given IP address
func CheckIPVersion(ipVal string) int {
	ipStr := strings.TrimSpace(ipVal)
	ip := net.ParseIP(ipStr)
	if ip == nil {
		return -1
	}

	if ip.To4() != nil {
		return 4
	}

	return 6
}

// IsDisallowedIP parses the ip to determine if we should allow the HTTP client to continue
func IsDisallowedIP(hostIP string, level int) bool {
	ip := net.ParseIP(hostIP)
	if ip == nil {
		return true
	}
	switch level {
	case 0: // Allow only public IPs
		return ip.IsMulticast() || ip.IsUnspecified() || ip.IsLoopback() || ip.IsPrivate()
	case 1: // Allow only private IPs
		return ip.IsMulticast() || ip.IsUnspecified() || ip.IsLoopback() || ip.IsGlobalUnicast()
	case 2: // Allow only loopback IPs
		return ip.IsMulticast() || ip.IsUnspecified() || ip.IsPrivate() || ip.IsGlobalUnicast()
	case 3: // Allow only valid IPs
		return ip.IsMulticast() || ip.IsUnspecified()
	default:
		return ip.IsMulticast() || ip.IsUnspecified() || ip.IsLoopback() || ip.IsPrivate()
	}
}

// SafeTransport creates a safe HTTP transport
func SafeTransport(timeout int, sslmode string) *http.Transport {
	// Start with cloning the DefaultTransport (type assertion is required)
	transport := http.DefaultTransport.(*http.Transport).Clone()

	// Set the common DialContext, including IP check and timeout
	transport.DialContext = dialContextWithIPCheck(time.Second * time.Duration(timeout))

	transport.TLSHandshakeTimeout = 0

	// Apply the TLS handshake timeout and DialTLS only if SSL is not disabled
	sslmode = strings.ToLower(strings.TrimSpace(sslmode))
	if sslmode != "ignore" {
		if sslmode != DisableStr && sslmode != "disabled" {
			transport.DialTLSContext = dialTLSWithIPCheck(time.Second * time.Duration(timeout))
			transport.TLSHandshakeTimeout = time.Second * time.Duration(timeout)
		}
	}

	return transport
}

func dialContextWithIPCheck(timeout time.Duration) func(ctx context.Context, network, addr string) (net.Conn, error) {
	return func(_ context.Context, network, addr string) (net.Conn, error) {
		c, err := net.DialTimeout(network, addr, timeout)
		if err != nil {
			return nil, err
		}
		ip, _, _ := net.SplitHostPort(c.RemoteAddr().String())
		if IsDisallowedIP(ip, 3) {
			return nil, errors.New(errIPNotAllowed)
		}
		return c, err
	}
}

func dialTLSWithIPCheck(timeout time.Duration) func(ctx context.Context, network, addr string) (net.Conn, error) {
	return func(ctx context.Context, network, addr string) (net.Conn, error) {
		// Your logic to dial TLS with IP checks here.
		// Make sure to respect the provided context.
		// For demonstration, using net.Dialer with context:
		dialer := net.Dialer{
			Timeout: timeout,
		}
		// Example: Directly dial without modifying TLS settings
		// In real usage, you'd likely want to configure TLS settings appropriately.
		conn, err := dialer.DialContext(ctx, network, addr)
		if err != nil {
			return nil, err
		}
		// Perform any necessary TLS handshakes or configurations here.
		// This is a simplified example.
		return conn, nil
	}
}

//// ----- ENV related shared functions ----- ////

// InterpolateEnvVars replaces occurrences of `${VAR}` or `$VAR` in the input string
// with the value of the VAR environment variable.
/*
func InterpolateEnvVars(input string) string {
	envVarPattern := regexp.MustCompile(`\$\{?(\w+)\}?`)
	return envVarPattern.ReplaceAllStringFunc(input, func(varName string) string {
		// Trim ${ and } from varName
		trimmedVarName := varName
		trimmedVarName = strings.TrimPrefix(trimmedVarName, "${")
		trimmedVarName = strings.TrimSuffix(trimmedVarName, "}")

		// Return the environment variable value
		return os.Getenv(trimmedVarName)
	})
}
*/
func InterpolateEnvVars(input string) string {
	envVarPattern := regexp.MustCompile(`\$\{(\w+)\}`)
	return envVarPattern.ReplaceAllStringFunc(input, func(match string) string {
		submatches := envVarPattern.FindStringSubmatch(match)
		if len(submatches) != 2 {
			return match
		}
		return os.Getenv(submatches[1])
	})
}

// StringToInt converts a string to an integer
func StringToInt(s string) int {
	i, err := strconv.Atoi(s)
	if err != nil {
		return 0
	}
	return i
}

// StringToFloat converts a string to a float
func StringToFloat(s string) float64 {
	f, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return 0
	}
	return f
}

// StringToFloat32 converts String to FLoat32
func StringToFloat32(s string) float32 {
	f, err := strconv.ParseFloat(s, 32)
	if err != nil {
		return 0
	}
	return float32(f)
}

// GetHostIP returns the host IP address
func GetHostIP() string {
	hostIP := ""
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		return hostIP
	}
	for _, addr := range addrs {
		ip, _, err := net.ParseCIDR(addr.String())
		if err != nil {
			continue
		}
		if ip.IsGlobalUnicast() {
			hostIP = ip.String()
			break
		}
	}
	return hostIP
}

// GetMicroServiceName returns the microservice name from the environment variable
func GetMicroServiceName() string {
	microServiceName := os.Getenv("MICROSERVICE_NAME")
	if microServiceName == "" {
		microServiceName = GetHostName()
	}
	return microServiceName
}
