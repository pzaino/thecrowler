package httpinfo

import (
	"os"
	"testing"
)

// TestJARMCollector_Collect tests the Collect method of the JARMCollector.
func TestJARMCollector_Collect(t *testing.T) {
	if os.Getenv("GITHUB_ACTIONS") == "true" {
		t.Skip("Skipping this test in GitHub Actions.")
	}

	jc := JARMCollector{
		Proxy: nil, // Set the proxy configuration if needed
	}

	host := "example.com"
	port := "443"

	jarm, err := jc.Collect(host, port)

	if err != nil {
		t.Errorf("Unexpected error: %v", err)
	}

	// Print JARM
	t.Logf("JARM: %s", jarm)

	// Add assertions to validate the JARM fingerprint
	// For example:
	// if jarm != "expected_jarm" {
	//     t.Errorf("Expected JARM: %s, got: %s", "expected_jarm", jarm)
	// }
}
