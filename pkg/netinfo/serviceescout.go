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
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	nmap "github.com/Ullaakut/nmap/v3"
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
	// Check the IP address
	ip = strings.TrimSpace(ip)
	if ip == "" {
		return []HostInfo{}, fmt.Errorf("empty IP address")
	}

	// Create a context with a timeout per host
	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(cfg.Timeout)*time.Second)
	defer cancel()

	// Build the Nmap options
	options, err := buildNmapOptions(cfg, ip, &ni.Config.HostPlatform)
	if err != nil {
		return []HostInfo{}, fmt.Errorf("unable to build nmap options: %w", err)
	}

	// Create a new scanner
	scanner, err := nmap.NewScanner(ctx, options...)
	if err != nil {
		return []HostInfo{}, fmt.Errorf("unable to create nmap scanner: %w", err)
	}

	// Prepare parsed results container
	var hosts []HostInfo

	// Run the scan
	result, warnings, err := scanner.Run()
	if result != nil {
		// Parse the scan result
		hosts = parseScanResults(result)
	}
	if err != nil {
		output := "ServiceScout scan failed:\n"
		output += fmt.Sprintf("    Error: %v\n", err)
		output += fmt.Sprintf("     Args: %v\n", scanner.Args())
		if len((*warnings)) != 0 {
			for i, warning := range *warnings {
				output += fmt.Sprintf("Warning %d: %s\n", i, warning)
			}
		}
		if result != nil {
			output += fmt.Sprintf("Result's Hosts: %v\n", result.Hosts)
			output += fmt.Sprintf("Result's Args: %v\n", result.Args)
			output += fmt.Sprintf("Result's Errors: %v\n", result.NmapErrors)
		}
		return hosts, fmt.Errorf("%s", output)
	} else {
		// log the raw results if we are in debug mode:
		output := "ServiceScout scan completed:\n"
		if len(*warnings) != 0 {
			for _, warning := range *warnings {
				output += fmt.Sprintf("%s\n", warning)
			}
		}
		cmn.DebugMsg(cmn.DbgLvlInfo, output)
		cmn.DebugMsg(cmn.DbgLvlDebug3, "ServiceScout raw results: %v", result)
	}
	scanner = nil // free the scanner

	return hosts, nil
}

func buildNmapOptions(cfg *cfg.ServiceScoutConfig,
	ip string, platform *cfg.PlatformInfo) ([]nmap.Option, error) {
	//var options []func(*nmap.Scanner)
	var options []nmap.Option

	// Set the IP address
	if cmn.CheckIPVersion(ip) == 6 {
		options = append(options, nmap.WithIPv6Scanning())
	}
	options = append(options, nmap.WithTargets(ip))
	//options = append(options, nmap.WithContext(*ctx))

	// Scan types:
	options = appendScanTypes(options, cfg)

	// DNS Options
	options = appendDNSOptions(options, cfg, platform)

	// Prepare scripts to use for the scan
	options = appendScripts(options, cfg)

	// Service detection
	options = appendServiceDetection(options, cfg, platform)

	// OS detection
	options = appendOSDetection(options, cfg)

	// Timing options
	options = appendTimingOptions(options, cfg)

	// Low-Nosing options
	options = appendLowNosingOptions(options, cfg, platform)

	// Info gathering options
	options = append(options, nmap.WithVerbosity(2))
	options = append(options, nmap.WithDebugging(2))

	// Privileged mode
	if platform.OSName != "darwin" {
		options = append(options, nmap.WithPrivileged())
	}

	return options, nil
}

func appendScanTypes(options []nmap.Option, cfg *cfg.ServiceScoutConfig) []nmap.Option {
	if cfg.UDPScan {
		options = append(options, nmap.WithUDPScan())
	}
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
	return options
}

func appendDNSOptions(options []nmap.Option, cfg *cfg.ServiceScoutConfig,
	platform *cfg.PlatformInfo) []nmap.Option {
	if platform.OSName != "darwin" {
		if len(cfg.DNSServers) > 0 {
			options = append(options, nmap.WithCustomDNSServers(cfg.DNSServers...))
		} else {
			options = append(options, nmap.WithSystemDNS())
		}
	}
	if cfg.NoDNSResolution {
		options = append(options, nmap.WithDisabledDNSResolution())
	}
	return options
}

func appendScripts(options []nmap.Option, cfg *cfg.ServiceScoutConfig) []nmap.Option {
	if len(cfg.ScriptScan) == 0 {
		cfg.ScriptScan = []string{"default"}
	} else {
		options = append(options, nmap.WithScripts(cfg.ScriptScan...))
	}
	return options
}

func appendServiceDetection(options []nmap.Option, cfg *cfg.ServiceScoutConfig,
	_ *cfg.PlatformInfo) []nmap.Option {
	if cfg.ServiceDetection {
		options = append(options, nmap.WithSkipHostDiscovery())
		options = append(options, nmap.WithPorts("1-"+strconv.Itoa(cfg.MaxPortNumber)))
		options = append(options, nmap.WithServiceInfo())
	}
	return options
}

func appendOSDetection(options []nmap.Option, cfg *cfg.ServiceScoutConfig) []nmap.Option {
	if cfg.OSFingerprinting {
		options = append(options, nmap.WithOSDetection())
	}
	return options
}

func appendTimingOptions(options []nmap.Option, cfg *cfg.ServiceScoutConfig) []nmap.Option {
	if cfg.HostTimeout != "" {
		hostTimeout := exi.GetFloat(cfg.HostTimeout)
		options = append(options, nmap.WithHostTimeout(time.Duration(hostTimeout)*time.Second))
	}
	if cfg.TimingTemplate != "" {
		timing, err := strconv.ParseInt(cfg.TimingTemplate, 10, 16)
		if err != nil {
			// If the timing template is not a number, just stop here and return the options
			return options
		}
		timingTemplate := nmap.Timing(int16(timing)) // Convert int to nmap.Timing
		options = append(options, nmap.WithTimingTemplate(timingTemplate))
	}
	if cfg.ScanDelay != "" {
		scDelay := exi.GetFloat(cfg.ScanDelay)
		if scDelay < 1 {
			scDelay += 1
		}
		options = append(options, nmap.WithScanDelay(time.Duration(scDelay)*time.Millisecond))
	}
	return options
}

func appendLowNosingOptions(options []nmap.Option, cfg *cfg.ServiceScoutConfig,
	platform *cfg.PlatformInfo) []nmap.Option {
	if cfg.MaxRetries > 0 {
		maxRetries := cfg.MaxRetries
		options = append(options, nmap.WithMaxRetries(int(maxRetries)))
	}
	usingSS := false
	if platform.OSName != "darwin" {
		if cfg.IPFragment {
			options = append(options, nmap.WithFragmentPackets())
			if cfg.UDPScan {
				options = append(options, nmap.WithSYNScan())
				usingSS = true
			}
		}
	}

	// Syn scan
	if cfg.SynScan {
		if !usingSS {
			options = append(options, nmap.WithSYNScan())
		}
	}
	if cfg.PingScan || cfg.SynScan || usingSS {
		//options = append(options, nmap.WithCustomArguments("-PO6,17"))
		//options = append(options, nmap.WithIPOptions("PO6,17"))
		options = append(options, nmap.WithIPProtocolScan())
		// need to improve these two:
		//options = append(options, nmap.WithCustomArguments("--defeat-rst-ratelimit"))
		//options = append(options, nmap.WithCustomArguments("--defeat-icmp-ratelimit"))
	}

	// need to improve this one
	//options = append(options, nmap.WithCustomArguments("--disable-arp-ping"))

	// Idle scan
	if cfg.IdleScan.ZombieHost != "" {
		options = append(options, nmap.WithIdleScan(cfg.IdleScan.ZombieHost, cfg.IdleScan.ZombiePort))
	}

	// Proxies
	if len(cfg.Proxies) > 0 {
		options = append(options, nmap.WithProxies(cfg.Proxies...))
	}

	// Traceroute
	options = append(options, nmap.WithTraceRoute())

	return options
}

func parseScanResults(result *nmap.Run) []HostInfo {
	var hosts []HostInfo // This will hold all the host info structs we create
	if result == nil {
		return hosts
	}

	// Log the raw results if we are in debug mode:
	jsonData, err := json.MarshalIndent(result, "", "  ")
	if err == nil {
		cmn.DebugMsg(cmn.DbgLvlDebug5, "ServiceScout debug: %s", string(jsonData))
	}

	for _, hostResult := range result.Hosts {
		hostInfo := HostInfo{} // This will hold the host info struct we create

		// Collect scanned IP information
		collectIPInfo(&hostResult, &hostInfo)

		// Collect hostname information
		collectHostnameInfo(&hostResult, &hostInfo)

		// Collect port information
		collectPortInfo(&hostResult, &hostInfo)
		collectExtraPortInfo(&hostResult, &hostInfo)

		// Collect OS information
		collectOSInfo(&hostResult, &hostInfo)

		// Collect vulnerabilities from Nmap scripts
		collectVulnerabilityInfo(&hostResult, &hostInfo)

		// Append the host to the hosts slice
		hosts = append(hosts, hostInfo)
	}

	return hosts
}

func collectIPInfo(hostResult *nmap.Host, hostInfo *HostInfo) {
	if len(hostResult.Addresses) > 0 {
		for _, addr := range hostResult.Addresses {
			if strings.TrimSpace(addr.AddrType) == "" || strings.ToLower(strings.TrimSpace(addr.AddrType)) == "unknown" {
				if cmn.CheckIPVersion(addr.Addr) == 6 {
					addr.AddrType = "ipv6"
				} else {
					addr.AddrType = "ipv4"
				}
			}
			AddrInfo := IPInfoDetails{
				Address: addr.Addr,
				Type:    addr.AddrType,
				Vendor:  addr.Vendor,
			}
			hostInfo.IP = append(hostInfo.IP, AddrInfo)
		}
	}
}

func collectHostnameInfo(hostResult *nmap.Host, hostInfo *HostInfo) {
	if len(hostResult.Hostnames) > 0 {
		for _, hostname := range hostResult.Hostnames {
			hostnameInfo := HostNameDetails{
				Name: hostname.Name,
				Type: hostname.Type,
			}
			hostInfo.Hostname = append(hostInfo.Hostname, hostnameInfo)
		}
	}
}

// collectPortInfo collects the port and service information
func collectPortInfo(hostResult *nmap.Host, hostInfo *HostInfo) {
	ports := hostResult.Ports

	if len(ports) > 0 {
		for _, port := range ports {
			portInfo := PortInfo{
				Port:     int(port.ID),
				Protocol: port.Protocol,
				State:    port.State.State,
				Service:  port.Service.Name,
			}
			hostInfo.Ports = append(hostInfo.Ports, portInfo)
			if port.Service.String() != "" {
				serviceInfo := ServiceInfo{
					Name:       port.Service.Name,
					Product:    port.Service.Product,
					Version:    port.Service.Version,
					ExtraInfo:  port.Service.ExtraInfo,
					DeviceType: port.Service.DeviceType,
					OSType:     port.Service.OSType,
					Hostname:   port.Service.Hostname,
					Method:     port.Service.Method,
					Proto:      port.Service.Proto,
					RPCNum:     port.Service.RPCNum,
					ServiceFP:  port.Service.ServiceFP,
					Tunnel:     port.Service.Tunnel,
				}
				if len(port.Scripts) > 0 {
					serviceInfo.Scripts = make([]ScriptInfo, 0)
					for _, script := range port.Scripts {
						scriptInfo := ScriptInfo{
							ID:     script.ID,
							Output: script.Output,
						}
						if len(script.Elements) > 0 {
							for _, elem := range script.Elements {
								scriptInfo.Elements = append(scriptInfo.Elements, ScriptElement{
									Key:   elem.Key,
									Value: elem.Value,
								})
							}
						}
						if len(script.Tables) > 0 {
							for _, table := range script.Tables {
								scriptTable := ScriptTable{
									Key: table.Key,
								}
								if len(table.Elements) > 0 {
									for _, elem := range table.Elements {
										scriptTable.Elements = append(scriptTable.Elements, ScriptElement{
											Key:   elem.Key,
											Value: elem.Value,
										})
									}
								}
								scriptInfo.Tables = append(scriptInfo.Tables, scriptTable)
							}
						}
						serviceInfo.Scripts = append(serviceInfo.Scripts, scriptInfo)
					}
				}
				hostInfo.Services = append(hostInfo.Services, serviceInfo)
			}
		}
	}
}

func collectExtraPortInfo(hostResult *nmap.Host, hostInfo *HostInfo) {
	ports := hostResult.ExtraPorts
	if len(ports) > 0 {
		for _, port := range ports {
			portInfo := PortInfo{
				Port:     int(port.Count),
				Protocol: "unknown",
				State:    port.State,
				Service:  "unknown",
			}
			hostInfo.Ports = append(hostInfo.Ports, portInfo)
		}
	}
}

func collectOSInfo(hostResult *nmap.Host, hostInfo *HostInfo) {
	item := hostResult.OS

	for _, match := range item.Matches {
		OSMatch := OSInfo{
			Name:     match.Name,
			Accuracy: match.Accuracy,
			Classes:  make([]OSCLass, 0),
			Line:     match.Line,
		}
		for _, class := range match.Classes {
			OSMatch.Classes = append(OSMatch.Classes, OSCLass{
				Type:     class.Type,
				Vendor:   class.Vendor,
				OSFamily: class.Family,
				OSGen:    class.OSGeneration,
				Accuracy: class.Accuracy,
			})
		}
		hostInfo.OS = append(hostInfo.OS, OSMatch)
	}
}

func collectVulnerabilityInfo(hostResult *nmap.Host, hostInfo *HostInfo) {
	scripts := &(hostResult.HostScripts)

	for _, script := range *scripts {
		vulnerabilityInfo := collectVulnerabilityData(&script)
		hostInfo.Vulnerabilities = append(hostInfo.Vulnerabilities, vulnerabilityInfo)
	}
}

func collectVulnerabilityData(script *nmap.Script) VulnerabilityInfo {
	vulnerabilityInfo := VulnerabilityInfo{
		ID:       script.ID,
		Name:     script.ID,
		Severity: "unknown",
		Output:   script.Output,
	}
	for _, elem := range script.Elements {
		switch elem.Key {
		case "severity":
			vulnerabilityInfo.Severity = elem.Value
		case "title":
			vulnerabilityInfo.Name = elem.Value
		case "reference":
			vulnerabilityInfo.Reference = elem.Value
		case "description":
			vulnerabilityInfo.Description = elem.Value
		case "state":
			vulnerabilityInfo.State = elem.Value
		}
		vulnerabilityInfo.Elements = append(vulnerabilityInfo.Elements, ScriptElement{
			Key:   elem.Key,
			Value: elem.Value,
		})
	}
	return vulnerabilityInfo
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

			// Scan the host
			hosts, err := ni.scanHost(scanCfg, ip)
			if err != nil {
				cmn.DebugMsg(cmn.DbgLvlDebug, "error scanning host %s: %v", ip, err)
			}

			// check if hosts is empty, and if it is add an empty HostInfo struct to it
			if len(hosts) == 0 {
				IPInfoItem := []IPInfoDetails{{
					Address: ip,
					Vendor:  "unknown",
				}}
				if cmn.CheckIPVersion(ip) == 6 {
					IPInfoItem[0].Type = "ipv6"
				} else {
					IPInfoItem[0].Type = "ipv4"
				}
				hosts = append(hosts, HostInfo{
					IP: IPInfoItem,
				})
			}

			// Append the host info to the final hosts slice
			mu.Lock()
			fHosts = append(fHosts, hosts...)
			mu.Unlock()

			// Log the raw results if we are in debug mode:
			jsonData, err := json.MarshalIndent(fHosts, "", "  ")
			if err == nil {
				cmn.DebugMsg(cmn.DbgLvlDebug3, "ServiceScout results: %s", string(jsonData))
			}
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
