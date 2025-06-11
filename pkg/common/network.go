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
	"fmt"
	"net"
	"os"
)

// DetectLocalNetwork finds the local network the machine is connected to
func DetectLocalNetwork() (string, error) {
	interfaces, err := net.Interfaces()
	if err != nil {
		return "", fmt.Errorf("failed to get network interfaces: %w", err)
	}

	for _, iface := range interfaces {
		// Skip interfaces that are down
		if iface.Flags&net.FlagUp == 0 {
			continue
		}

		addrs, err := getAddrs(iface)
		if err != nil {
			continue
		}

		for _, addr := range addrs {
			ipNet, ok := addr.(*net.IPNet)
			if ok && ipNet.IP.To4() != nil {
				if isPrivateIP(ipNet.IP) {
					// Calculate the network address
					networkIP := ipNet.IP.Mask(ipNet.Mask)
					return fmt.Sprintf("%s/%d", networkIP.String(), maskToCIDR(ipNet.Mask)), nil
				}
			}
		}
	}

	return "", fmt.Errorf("no local network detected")
}

func isPrivateIP(ip net.IP) bool {
	privateBlocks := []string{
		"10.0.0.0/8",
		"172.16.0.0/12",
		"192.168.0.0/16",
	}

	for _, block := range privateBlocks {
		_, cidr, _ := net.ParseCIDR(block)
		if cidr.Contains(ip) {
			return true
		}
	}

	return false
}

// maskToCIDR converts a net.IPMask to its CIDR prefix length
func maskToCIDR(mask net.IPMask) int {
	ones, _ := mask.Size()
	return ones
}

// getAddrs is the default implementation for fetching interface addresses
func getAddrs(iface net.Interface) ([]net.Addr, error) {
	return iface.Addrs()
}

// GetHostName returns the hostname of the machine
func GetHostName() string {
	const unknown = "unknown"
	hostname, err := os.Hostname()
	if err != nil {
		hostnames, err := net.LookupHost("localhost")
		if err != nil {
			return unknown
		}
		if len(hostnames) == 0 {
			return unknown
		}
		return hostnames[0]
	}
	if len(hostname) == 0 {
		return unknown
	}
	return hostname
}
