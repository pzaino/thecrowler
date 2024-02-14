package common

import (
	"log"
	"strings"
	"testing"
)

// Define Debug constant
const (
	None DbgLevel = iota // Define Debug as the first level
	Info
	Debug
	Error
	Fatal
)

func TestSetDebugLevel(t *testing.T) {
	// Test cases
	tests := []struct {
		name     string
		dbgLvl   DbgLevel
		expected DbgLevel
	}{
		{
			name:     "Test case 1",
			dbgLvl:   Debug,
			expected: Debug,
		},
		{
			name:     "Test case 2",
			dbgLvl:   Info,
			expected: Info,
		},
		{
			name:     "Test case 3",
			dbgLvl:   Fatal,
			expected: Fatal,
		},
		{
			name:     "Test case 4",
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
			name:     "Test case 1",
			dbgLvl:   Debug,
			msg:      "Debug message",
			args:     []interface{}{},
			expected: "Debug message\n",
		},
		{
			name:     "Test case 2",
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
			level:    1,
			expected: false,
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
			UpdateLoggerConfig()
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
