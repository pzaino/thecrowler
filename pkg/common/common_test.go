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
