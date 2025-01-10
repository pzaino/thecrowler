package common

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestDetectLocalNetwork_RealEnvironment(t *testing.T) {
	cidr, err := DetectLocalNetwork()

	if err != nil {
		t.Errorf("Error detecting local network: %v", err)
	} else {
		t.Logf("Detected local network CIDR: %s", cidr)
		assert.NotEmpty(t, cidr, "Local network CIDR should not be empty")
	}
}
