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
	// Check the IP address
	ip = strings.TrimSpace(ip)
	if ip == "" {
		return []HostInfo{}, fmt.Errorf("empty IP address")
	}

	// Create a context with a timeout per host
	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(cfg.Timeout)*time.Second)
	defer cancel()

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
	//scanner = nil // free the scanner

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
	options = append(options, nmap.WithVerbosity(2))
	options = append(options, nmap.WithDebugging(2))

	return options, nil
}

func parseScanResults(result *nmap.Run) []HostInfo {
	var hosts []HostInfo // This will hold all the host info structs we create

	// Log the raw results if we are in debug mode:
	jsonData, err := json.MarshalIndent(result, "", "  ")
	if err == nil {
		cmn.DebugMsg(cmn.DbgLvlDebug5, "ServiceScout debug: %s", string(jsonData))
	}

	for _, hostResult := range result.Hosts {
		hostInfo := HostInfo{} // Assuming HostInfo is a struct that contains IP, Hostname, and Ports fields

		// Collect scanned IP information
		collectIPInfo(&hostResult, &hostInfo)

		// Collect hostname information
		collectHostnameInfo(&hostResult, &hostInfo)

		// Collect port information
		hostInfo.Ports, hostInfo.Services = collectPortInfo(hostResult.Ports)
		collectExtraPortInfo(hostResult.ExtraPorts, &hostInfo.Ports)

		// Collect OS information
		hostInfo.OS = collectOSInfo(hostResult.OS)

		// Collect vulnerabilities from Nmap scripts
		hostInfo.Vulnerabilities = collectVulnerabilityInfo(&(hostResult.HostScripts))

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
func collectPortInfo(ports []nmap.Port) ([]PortInfo, []ServiceInfo) {
	var portInfoList []PortInfo
	var serviceInfoList []ServiceInfo

	if len(ports) > 0 {
		for _, port := range ports {
			portInfo := PortInfo{
				Port:     int(port.ID),
				Protocol: port.Protocol,
				State:    port.State.State,
				Service:  port.Service.Name,
			}
			portInfoList = append(portInfoList, portInfo)
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
				serviceInfoList = append(serviceInfoList, serviceInfo)
			}
		}
	}

	return portInfoList, serviceInfoList
}

func collectExtraPortInfo(ports []nmap.ExtraPort, portInfoList *[]PortInfo) {
	if len(ports) > 0 {
		for _, port := range ports {
			portInfo := PortInfo{
				Port:     int(port.Count),
				Protocol: "unknown",
				State:    port.State,
				Service:  "unknown",
			}
			*portInfoList = append(*portInfoList, portInfo)
		}
	}
}

func collectOSInfo(item nmap.OS) []OSInfo {
	var osInfoList []OSInfo

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
		osInfoList = append(osInfoList, OSMatch)
	}

	return osInfoList
}

func collectVulnerabilityInfo(scripts *[]nmap.Script) []VulnerabilityInfo {
	var vulnerabilityInfoList []VulnerabilityInfo

	for _, script := range *scripts {
		vulnerabilityInfo := collectVulnerabilityData(&script)
		vulnerabilityInfoList = append(vulnerabilityInfoList, vulnerabilityInfo)
	}

	return vulnerabilityInfoList
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
