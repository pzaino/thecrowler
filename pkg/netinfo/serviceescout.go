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
	"context"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	nmap "github.com/Ullaakut/nmap"
	cmn "github.com/pzaino/thecrowler/pkg/common"
	cfg "github.com/pzaino/thecrowler/pkg/config"
	exi "github.com/pzaino/thecrowler/pkg/exprterpreter"
)

// GetNmapInfo returns the Nmap information for the provided URL
func (ni *NetInfo) GetServiceScoutInfo(scanCfg *cfg.ServiceScoutConfig) error {
	// Scan the hosts
	hosts, err := ni.scanHosts(scanCfg)
	if err != nil {
		return err
	}

	// Add the Nmap info to the NetInfo struct
	nmapInfo := &ServiceScoutInfo{
		Hosts: hosts,
	}
	ni.ServiceScout = *nmapInfo

	return nil
}

func (ni *NetInfo) scanHost(cfg *cfg.ServiceScoutConfig, ip string) ([]HostInfo, error) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(cfg.Timeout)*time.Second)
	defer cancel()

	// Check the IP address
	ip = strings.TrimSpace(ip)
	if ip == "" {
		return []HostInfo{}, fmt.Errorf("empty IP address")
	}

	// Build the Nmap options
	options, err := buildNmapOptions(cfg, ip, &ctx)
	if err != nil {
		return []HostInfo{}, fmt.Errorf("unable to build nmap options: %w", err)
	}

	// Create a new scanner
	scanner, err := nmap.NewScanner(options...)
	if err != nil {
		return []HostInfo{}, fmt.Errorf("unable to create nmap scanner: %w", err)
	}

	// Run the scan
	result, warnings, err := scanner.Run()
	if len(warnings) != 0 {
		for _, warning := range warnings {
			cmn.DebugMsg(cmn.DbgLvlDebug, "ServiceScout warning: %v", warning)
		}
	}
	// log the raw results if we are in debug mode:
	cmn.DebugMsg(cmn.DbgLvlDebug3, "ServiceScout raw results: %v", result)
	if err != nil {
		return []HostInfo{}, fmt.Errorf("ServiceScout scan failed: %w", err)
	}
	scanner = nil // free the scanner

	// Parse the scan result
	hosts := parseScanResults(result)

	return hosts, nil
}

func buildNmapOptions(cfg *cfg.ServiceScoutConfig, ip string,
	ctx *context.Context) ([]func(*nmap.Scanner), error) {
	var options []func(*nmap.Scanner)

	if cmn.CheckIPVersion(ip) == 6 {
		options = append(options, nmap.WithIPv6Scanning())
	}
	options = append(options, nmap.WithTargets(ip))
	options = append(options, nmap.WithContext(*ctx))

	// Add options based on the config fields
	if cfg.PingScan {
		options = append(options, nmap.WithPingScan())
	}
	if cfg.SynScan {
		options = append(options, nmap.WithSYNScan())
	}
	if cfg.ConnectScan {
		options = append(options, nmap.WithConnectScan())
	}
	if cfg.AggressiveScan {
		options = append(options, nmap.WithAggressiveScan())
	}
	// Prepare scripts to use for the scan
	if len(cfg.ScriptScan) == 0 {
		cfg.ScriptScan = []string{"default"}
	}
	if len(cfg.ScriptScan) != 0 {
		options = append(options, nmap.WithScripts(cfg.ScriptScan...))
	}
	if cfg.ServiceDetection {
		options = append(options, nmap.WithCustomArguments("-Pn"))
		options = append(options, nmap.WithPorts("1-"+strconv.Itoa(cfg.MaxPortNumber)))
		options = append(options, nmap.WithServiceInfo())
		//options = append(options, nmap.WithCustomArguments("--version-intensity 5"))
	}
	if cfg.OSFingerprinting {
		options = append(options, nmap.WithOSDetection())
	}
	if cfg.ScanDelay != "" {
		scDelay := exi.GetFloat(cfg.ScanDelay)
		if scDelay < 1 {
			scDelay += 1
		}
		options = append(options, nmap.WithScanDelay(time.Duration(scDelay)*time.Millisecond))
	}
	if cfg.TimingTemplate != "" {
		// transform cfg.Timing into int16
		timing, err := strconv.ParseInt(cfg.TimingTemplate, 10, 16)
		if err != nil {
			return nil, fmt.Errorf("unable to parse timing template: %w", err)
		}
		timingTemplate := nmap.Timing(int16(timing)) // Convert int to nmap.Timing
		options = append(options, nmap.WithTimingTemplate(timingTemplate))
	}
	if cfg.HostTimeout != "" {
		hostTimeout := exi.GetFloat(cfg.HostTimeout)
		options = append(options, nmap.WithHostTimeout(time.Duration(hostTimeout)*time.Second))
	}

	return options, nil
}

func parseScanResults(result *nmap.Run) []HostInfo {
	var hosts []HostInfo // This will hold all the host info structs we create

	for _, hostResult := range result.Hosts {
		hostInfo := HostInfo{} // Assuming HostInfo is a struct that contains IP, Hostname, and Ports fields

		// Assuming there's always at least one address, and it's the IP you want
		if len(hostResult.Addresses) > 0 {
			hostInfo.IP = hostResult.Addresses[0].Addr
		}

		if len(hostResult.Hostnames) > 0 {
			hostInfo.Hostname = hostResult.Hostnames[0].Name
		}

		if len(hostResult.Ports) > 0 {
			for _, port := range hostResult.Ports {
				portInfo := PortInfo{ // Assuming PortInfo is your struct for port details
					Port:     int(port.ID),
					Protocol: port.Protocol,
					State:    port.State.State,
					Service:  port.Service.Name,
				}
				hostInfo.Ports = append(hostInfo.Ports, portInfo)
			}
		}

		if len(hostResult.OS.Matches) > 0 {
			for _, match := range hostResult.OS.Matches {
				hostInfo.OS = append(hostInfo.OS, match.Name)
			}
		}

		hosts = append(hosts, hostInfo)
	}

	return hosts
}

// scanHosts scans the hosts using Nmap
func (ni *NetInfo) scanHosts(scanCfg *cfg.ServiceScoutConfig) ([]HostInfo, error) {
	// Get the IP addresses
	ips := ni.IPs.IP

	var fHosts []HostInfo // This will hold all the host info structs we create (fHosts = final hosts)
	var wg sync.WaitGroup
	var mu sync.Mutex

	for _, ip := range ips {
		wg.Add(1)
		go func(ip string) {
			defer wg.Done()

			hosts, err := ni.scanHost(scanCfg, ip)
			if err != nil {
				// Log error or handle it according to your error policy and skip this host
				cmn.DebugMsg(cmn.DbgLvlDebug, "error scanning host %s: %v", ip, err)
			}
			cmn.DebugMsg(cmn.DbgLvlDebug, "ServiceScout results: %v", hosts)
			// check if hosts is empty and if it is add an empty HostInfo struct to it
			if len(hosts) == 0 {
				hosts = append(hosts, HostInfo{
					IP: ip,
				})
			}
			mu.Lock()
			fHosts = append(fHosts, hosts...)
			mu.Unlock()
			cmn.DebugMsg(cmn.DbgLvlDebug, "ServiceScout results: %v", fHosts)
		}(ip)
	}

	wg.Wait()
	cmn.DebugMsg(cmn.DbgLvlDebug, "ServiceScout scan completed")

	return fHosts, nil
}

/*
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
*/
