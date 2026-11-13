// Command hashpw prints CROWler password hashes for passwords read from stdin.
// It is a test-only helper and never logs or persists the plaintext passwords.
package main

import (
	"bufio"
	"fmt"
	"os"
	"strings"

	auth "github.com/pzaino/thecrowler/pkg/auth"
)

func main() {
	scanner := bufio.NewScanner(os.Stdin)
	hashes := make([]string, 0, 3)
	for scanner.Scan() {
		password := strings.TrimSpace(scanner.Text())
		if password == "" {
			continue
		}
		hash, err := auth.HashPassword(password)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		hashes = append(hashes, hash)
	}
	if err := scanner.Err(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	for _, hash := range hashes {
		fmt.Println(hash)
	}
}
