package common

import (
	"encoding/base64"
)

// Base64Encode encodes a string to base64, this may be required by some
// configurations.
func Base64Encode(data string) string {
	return base64.StdEncoding.EncodeToString([]byte(data))
}
