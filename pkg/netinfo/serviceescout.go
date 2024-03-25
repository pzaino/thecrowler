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

// Package netinfo provides functionality to extract network information
package netinfo

import (
	"fmt"
	"sync"

	"github.com/Ullaakut/nmap"
	cmn "github.com/pzaino/thecrowler/pkg/common"
)

// GetNmapInfo returns the Nmap information for the provided URL
func (ni *NetInfo) GetNmapInfo() (*NmapInfo, error) {
	hosts, err := ni.scanHosts()
	if err != nil {
		return nil, err
	}

	nmapInfo := &NmapInfo{
		Hosts: hosts,
	}

	return nmapInfo, nil
}

// scanHosts scans the hosts using Nmap
func (ni *NetInfo) scanHosts() ([]HostInfo, error) {
	ips := ni.IPs.IP

	var hosts []HostInfo
	var wg sync.WaitGroup
	var mu sync.Mutex

	for _, ip := range ips {
		wg.Add(1)
		go func(ip string) {
			defer wg.Done()

			host, err := ni.scanHost(ip)
			if err != nil {
				// Log error or handle it according to your error policy
				cmn.DebugMsg(cmn.DbgLvlDebug, "error scanning host %s: %v", ip, err)
				return
			}

			mu.Lock()
			hosts = append(hosts, host)
			mu.Unlock()
		}(ip)
	}

	wg.Wait()

	return hosts, nil
}

// scanHost scans a single host using Nmap
func (ni *NetInfo) scanHost(ip string) (HostInfo, error) {
	host := HostInfo{
		IP: ip,
	}

	// Create a new scanner
	scanner, err := nmap.NewScanner(
		nmap.WithTargets(ip),
		nmap.WithPorts("1-1024"),
		nmap.WithTimingTemplate(nmap.TimingAggressive),
	)
	if err != nil {
		return host, fmt.Errorf("error creating Nmap scanner: %v", err)
	}

	// Run the scan
	result, warnings, err := scanner.Run()
	if err != nil {
		return host, fmt.Errorf("error running Nmap scan: %v", err)
	}

	// Log warnings
	for _, warning := range warnings {
		cmn.DebugMsg(cmn.DbgLvlDebug, "Nmap warning: %v", warning)
	}

	// Parse the scan result
	for _, hostResult := range result.Hosts {
		host.Hostname = hostResult.Hostnames[0].Name
		for _, port := range hostResult.Ports {
			portInfo := PortInfo{
				Port:     int(port.ID),
				Protocol: port.Protocol,
				State:    port.State.State,
				Service:  port.Service.Name,
			}
			host.Ports = append(host.Ports, portInfo)
		}
	}

	return host, nil
}
