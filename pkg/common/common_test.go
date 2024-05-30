package common

import (
	"log"
	"os"
	"strings"
	"testing"
	"time"
)

// Define Debug constant
const (
	None DbgLevel = iota // Define Debug as the first level
	Info
	Debug
	Error
	Fatal
)

const (
	testCase = "Test case "
)

func TestSetDebugLevel(t *testing.T) {
	// Test cases
	tests := []struct {
		name     string
		dbgLvl   DbgLevel
		expected DbgLevel
	}{
		{
			name:     testCase + "1",
			dbgLvl:   Debug,
			expected: Debug,
		},
		{
			name:     testCase + "2",
			dbgLvl:   Info,
			expected: Info,
		},
		{
			name:     testCase + "3",
			dbgLvl:   Fatal,
			expected: Fatal,
		},
		{
			name:     testCase + "4",
			dbgLvl:   Error,
			expected: Error,
		},
	}

	// Run tests
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			SetDebugLevel(test.dbgLvl)
			if debugLevel != test.expected {
				t.Errorf("Expected debug level %v, but got %v", test.expected, debugLevel)
			}
		})
	}
}

func TestGetDebugLevel(t *testing.T) {
	expected := debugLevel
	result := GetDebugLevel()
	if result != expected {
		t.Errorf("Expected debug level %v, but got %v", expected, result)
	}
}

func TestDebugMsg(t *testing.T) {
	tests := []struct {
		name     string
		dbgLvl   DbgLevel
		msg      string
		args     []interface{}
		expected string
	}{
		{
			name:     testCase + "1",
			dbgLvl:   Debug,
			msg:      "Debug message",
			args:     []interface{}{},
			expected: "Debug message\n",
		},
		{
			name:     testCase + "2",
			dbgLvl:   Info,
			msg:      "Info message",
			args:     []interface{}{},
			expected: "",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			logOutput := captureLogOutput(func() {
				DebugMsg(test.dbgLvl, test.msg, test.args...)
			})

			if !strings.Contains(logOutput, test.expected) {
				t.Errorf("Expected log output %q, but got %q", test.expected, logOutput)
			}
		})
	}
}

// Helper function to capture log output
func captureLogOutput(f func()) string {
	logOutput := ""
	log.SetOutput(&logWriter{&logOutput})
	f()
	log.SetOutput(log.Writer())
	return logOutput
}

// Custom log writer to capture log output
type logWriter struct {
	output *string
}

func (lw *logWriter) Write(p []byte) (n int, err error) {
	*lw.output += string(p)
	return len(p), nil
}
func TestIsDisallowedIP(t *testing.T) {
	tests := []struct {
		name     string
		hostIP   string
		level    int
		expected bool
	}{
		{
			name:     "IP Test case 1",
			hostIP:   "127.0.0.1",
			level:    0,
			expected: true,
		},
		{
			name:     "IP Test case 2",
			hostIP:   "192.168.0.1",
			level:    1,
			expected: true,
		},
		{
			name:     "IP Test case 3",
			hostIP:   "10.0.0.1",
			level:    1,
			expected: true,
		},
		{
			name:     "IP Test case 4",
			hostIP:   "172.16.0.1",
			level:    1,
			expected: true,
		},
		{
			name:     "IP Test case 5",
			hostIP:   "8.8.8.8",
			level:    3,
			expected: false,
		},
		{
			name:     "IP Test case 6",
			hostIP:   "2001:0db8:85a3:0000:0000:8a2e:0370:7335",
			level:    3,
			expected: false,
		},
		{
			name:     "IP Test case 7",
			hostIP:   "2001:0db8:85a3:0000:0000:8a2e:0370:7334",
			level:    6,
			expected: false,
		},
		{
			name:     "IP Test case 8",
			hostIP:   "invalid",
			level:    3,
			expected: true,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			result := IsDisallowedIP(test.hostIP, test.level)
			if result != test.expected {
				t.Errorf("Expected %v for hostIP %s and level %d, but got %v", test.expected, test.hostIP, test.level, result)
			}
		})
	}
}
func TestSetLoggerPrefix(t *testing.T) {
	// Test cases
	tests := []struct {
		name     string
		appName  string
		expected string
	}{
		{
			name:     "LoggerPrefix Test case 1",
			appName:  "TestApp",
			expected: "TestApp",
		},
		{
			name:     "LoggerPrefix Test case 2",
			appName:  "AnotherApp",
			expected: "AnotherApp",
		},
	}

	// Run tests
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			SetLoggerPrefix(test.appName)
			if !strings.Contains(loggerPrefix, test.expected) {
				t.Errorf("Expected logger prefix %q, but got %q", test.expected, loggerPrefix)
			}
		})
	}
}

func TestInitLogger(t *testing.T) {
	// Test case
	tests := []struct {
		name     string
		appName  string
		expected string
	}{
		{
			name:     "LoggerInit Test case 1",
			appName:  "TestApp",
			expected: "TestApp",
		},
		{
			name:     "LoggerInit Test case 2",
			appName:  "AnotherApp",
			expected: "AnotherApp",
		},
	}

	// Run tests
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			InitLogger(test.appName)
			if !strings.Contains(loggerPrefix, test.expected) {
				t.Errorf("Expected logger prefix %q, but got %q", test.expected, loggerPrefix)
			}
		})
	}
}
func TestUpdateLoggerConfig(t *testing.T) {
	tests := []struct {
		name       string
		debugLevel DbgLevel
		expected   int
	}{
		{
			name:       "LoggerUpdate Test case 1",
			debugLevel: 1,
			expected:   log.LstdFlags | log.Ldate | log.Ltime | log.Lmicroseconds | log.Lshortfile,
		},
		{
			name:       "LoggerUpdate Test case 2",
			debugLevel: 0,
			expected:   log.LstdFlags | log.Ldate | log.Ltime | log.Lmicroseconds,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			debugLevel = test.debugLevel
			UpdateLoggerConfig("")
			flags := log.Flags()
			if flags != test.expected {
				t.Errorf("Expected log flags %v, but got %v", test.expected, flags)
			}
		})
	}
}
func TestIsPathCorrect(t *testing.T) {
	tests := []struct {
		name     string
		path     string
		expected bool
	}{
		{
			name:     "CheckPath Test case 1",
			path:     "./*.jpg",
			expected: false,
		},
		{
			name:     "CheckPath Test case 2",
			path:     "/path/to/nonexistent/file.txt",
			expected: false,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			result := IsPathCorrect(test.path)
			if result != test.expected {
				t.Errorf("Expected %v for path %s, but got %v", test.expected, test.path, result)
			}
		})
	}
}
func TestCheckIPVersion(t *testing.T) {
	tests := []struct {
		name     string
		ipVal    string
		expected int
	}{
		{
			name:     testCase + "1",
			ipVal:    "192.168.0.1",
			expected: 4,
		},
		{
			name:     testCase + "2",
			ipVal:    "2001:0db8:85a3:0000:0000:8a2e:0370:7334",
			expected: 6,
		},
		{
			name:     testCase + "3",
			ipVal:    "invalid",
			expected: -1,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			result := CheckIPVersion(test.ipVal)
			if result != test.expected {
				t.Errorf("Expected IP version %d, but got %d", test.expected, result)
			}
		})
	}
}
func TestGetFileExt(t *testing.T) {
	tests := []struct {
		name     string
		filePath string
		expected string
	}{
		{
			name:     testCase + "1",
			filePath: "file.txt",
			expected: "txt",
		},
		{
			name:     testCase + "2",
			filePath: "image.jpg",
			expected: "jpg",
		},
		{
			name:     testCase + "3",
			filePath: "document.pdf",
			expected: "pdf",
		},
		{
			name:     testCase + "4",
			filePath: "script.js",
			expected: "js",
		},
		{
			name:     testCase + "5",
			filePath: "data.csv",
			expected: "csv",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			result := GetFileExt(test.filePath)
			if result != test.expected {
				t.Errorf("Expected file extension %q, but got %q", test.expected, result)
			}
		})
	}
}
func TestSafeTransport(t *testing.T) {
	tests := []struct {
		name        string
		timeout     int
		sslmode     string
		expectedTLS bool
	}{
		{
			name:        testCase + "1",
			timeout:     5,
			sslmode:     "disable",
			expectedTLS: false,
		},
		{
			name:        testCase + "2",
			timeout:     10,
			sslmode:     "ignore",
			expectedTLS: false,
		},
		{
			name:        testCase + "3",
			timeout:     15,
			sslmode:     "enabled",
			expectedTLS: true,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			transport := SafeTransport(test.timeout, test.sslmode)

			// Check if DialContext is set correctly
			if transport.DialContext == nil {
				t.Error("DialContext is not set")
			}

			// Check if DialTLSContext and TLSHandshakeTimeout are set correctly
			if test.expectedTLS {
				if transport.DialTLSContext == nil {
					t.Error("DialTLSContext is not set")
				}
				if transport.TLSHandshakeTimeout != time.Second*time.Duration(test.timeout) {
					t.Errorf("Expected TLSHandshakeTimeout %v, but got %v", time.Second*time.Duration(test.timeout), transport.TLSHandshakeTimeout)
				}
			} else {
				if transport.DialTLSContext != nil {
					t.Error("DialTLSContext should not be set")
				}
				if transport.TLSHandshakeTimeout != 0 {
					t.Errorf("Expected TLSHandshakeTimeout 0, but got %v", transport.TLSHandshakeTimeout)
				}
			}
		})
	}
}

func TestInterpolateEnvVars(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     testCase + "1",
			input:    "Hello, ${NAME}!",
			expected: "Hello, world!",
		},
		{
			name:     testCase + "2",
			input:    "The value is ${VALUE}.",
			expected: "The value is 42.",
		},
		{
			name:     testCase + "3",
			input:    "No environment variable.",
			expected: "No environment variable.",
		},
	}

	// Set environment variables
	err := os.Setenv("NAME", "world")
	if err != nil {
		t.Errorf("Unable to set environment variable: %v", err)
	}
	err = os.Setenv("VALUE", "42")
	if err != nil {
		t.Errorf("Unable to set environment variable: %v", err)
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			result := InterpolateEnvVars(test.input)
			if result != test.expected {
				t.Errorf("Expected %q, but got %q", test.expected, result)
			}
		})
	}
}
