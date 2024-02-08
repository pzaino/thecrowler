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
	"net"
	"sync"
	"time"

	cmn "github.com/pzaino/thecrowler/pkg/common"
)

// GetIPs returns the IP addresses of the provided URL
func (ni *NetInfo) GetIPs() error {
	host := urlToDomain(ni.URL)

	// retrieve ni.URL IP addresses
	ips, err := net.LookupIP(host)
	if err != nil {
		return fmt.Errorf("error looking up IP addresses: %v", err)
	}
	ipsStr := ipsToString(ips)

	// Append only new IPs to the existing list
	ipList := ni.IPs.IP
	asnList := ni.IPs.ASN
	cidrList := ni.IPs.CIDR
	ntRangeList := ni.IPs.NetRange
	ntNameList := ni.IPs.NetName
	ntHandleList := ni.IPs.NetHandle
	ntParentList := ni.IPs.NetParent
	ntTypeList := ni.IPs.NetType
	countryList := ni.IPs.Country

	for _, newIP := range ipsStr {
		isNew := true
		// Check if the IP is already in the list
		for _, ip := range ipList {
			if ip == newIP {
				isNew = false
				break
			}
		}
		if isNew {
			ipList = append(ipList, newIP)
			// Get network information for the new IP
			entity, err := getIPInfo(ni, newIP)
			if err == nil {
				asnList = append(asnList, defaultNA(entity.ASN))
				cidrList = append(cidrList, defaultNA(entity.CIDR))
				ntRangeList = append(ntRangeList, defaultNA(entity.NetRange))
				ntNameList = append(ntNameList, defaultNA(entity.NetName))
				ntHandleList = append(ntHandleList, defaultNA(entity.NetHandle))
				ntParentList = append(ntParentList, defaultNA(entity.NetParent))
				ntTypeList = append(ntTypeList, defaultNA(entity.NetType))
				countryList = append(countryList, defaultNA(entity.Country))
			} else {
				asnList = append(asnList, "N/A")
				cidrList = append(cidrList, "N/A")
				ntRangeList = append(ntRangeList, "")
				ntNameList = append(ntNameList, "")
				ntHandleList = append(ntHandleList, "")
				ntParentList = append(ntParentList, "")
				ntTypeList = append(ntTypeList, "")
				countryList = append(countryList, "")
			}
			time.Sleep(time.Duration(ni.Config.WHOIS.RateLimit) * time.Second)
		}
	}

	// Update all IP fields at once
	ni.IPs = IPData{
		IP:        ipList,
		ASN:       asnList,
		CIDR:      cidrList,
		NetRange:  ntRangeList,
		NetName:   ntNameList,
		NetHandle: ntHandleList,
		NetParent: ntParentList,
		NetType:   ntTypeList,
		Country:   countryList,
	}

	return nil
}

// ipsToString converts []net.IP to []string
func ipsToString(ips []net.IP) []string {
	var ipStrs []string
	for _, ip := range ips {
		ipStrs = append(ipStrs, ip.String())
	}
	return ipStrs
}

// GetHostsFromIPs returns the hostnames of the provided IP addresses
func (ni *NetInfo) GetHostsFromIPs() error {
	var hosts []string
	var wg sync.WaitGroup
	var mu sync.Mutex

	// Create a channel to receive results
	results := make(chan []string, len(ni.IPs.IP))

	for _, ip := range ni.IPs.IP {
		wg.Add(1)

		// Perform reverse DNS lookup in a goroutine
		go func(ip string) {
			defer wg.Done()

			hostnames, err := net.LookupAddr(ip)
			if err == nil {
				results <- hostnames
			} else {
				cmn.DebugMsg(cmn.DbgLvlError, "error looking up hostnames for IP %s: %v", ip, err)
			}
		}(ip)
	}

	// Wait for all goroutines to finish
	go func() {
		wg.Wait()
		close(results)
	}()

	// Collect and deduplicate hostnames
	for result := range results {
		mu.Lock()
		for _, hostname := range result {
			hosts = appendIfNotExists(hosts, hostname)
		}
		mu.Unlock()
	}

	ni.Hosts = HostData{Host: hosts}

	return nil
}

// appendIfNotExists appends an item to a slice if it doesn't already exist in the slice.
func appendIfNotExists(slice []string, item string) []string {
	for _, existingItem := range slice {
		if existingItem == item {
			return slice
		}
	}
	return append(slice, item)
}

// GetHosts returns the hostnames of the provided URL
func (ni *NetInfo) GetHosts() error {
	host := urlToDomain(ni.URL)
	ips, err := net.LookupHost(host)
	if err != nil {
		return fmt.Errorf("error looking up hostnames: %v", err)
	}

	// Append only new IPs to the existing list
	ipList := ni.IPs.IP
	for _, newIP := range ips {
		isNew := true
		// Check if the IP is already in the list
		for _, ip := range ipList {
			if ip == newIP {
				isNew = false
			}
		}
		if isNew {
			ipList = append(ipList, newIP)
		}
	}
	ni.IPs = IPData{IP: ipList}

	// Append the new host to the existing list
	hostList := ni.Hosts.Host
	isNew := true
	for _, h := range hostList {
		if h == host {
			isNew = false
		}
	}
	if isNew {
		hostList = append(hostList, host)
	}
	ni.Hosts = HostData{Host: hostList}

	return nil
}

// GetIPsFromHosts returns the IP addresses of the provided hostnames
func (ni *NetInfo) GetIPsFromHosts() error {
	var ips []string
	for _, host := range ni.Hosts.Host {
		ipAddrs, err := net.LookupIP(host)
		if err != nil {
			return fmt.Errorf("error looking up IP addresses: %v", err)
		}
		ips = append(ips, ipsToString(ipAddrs)...)
	}

	ni.IPs = IPData{IP: ips}

	return nil
}

func getEntities(ni *NetInfo) error {
	// Get IP addresses
	err := ni.GetIPs()
	if err != nil {
		return err
	}
	if len(ni.IPs.IP) == 0 {
		err = ni.GetHosts()
		if err != nil {
			return err
		}
		err = ni.GetIPsFromHosts()
		if err != nil {
			return err
		}
	} else {
		err = ni.GetHostsFromIPs()
		if err != nil {
			return err
		}
	}
	return nil
}

// GetNetInfo returns the IP addresses and hostnames of the provided URL
func (ni *NetInfo) GetNetInfo(url string) error {
	ni.URL = url

	// Get IP addresses and hostnames
	err := getEntities(ni)
	if err != nil {
		return err
	}

	// Check if host0 has been removed from the list
	if len(ni.Hosts.Host) == 0 {
		host0 := urlToDomain(url)
		ni.Hosts.Host = append(ni.Hosts.Host, host0)
	}

	// Get WHOIS information for all collected IPs
	if ni.Config.WHOIS.Enabled {
		err = ni.GetWHOISData()
		if err != nil {
			return err
		}
	}

	// Get DNS information for all collected hosts
	if ni.Config.DNS.Enabled {
		err = ni.GetDNSInfo()
		if err != nil {
			return err
		}
	}

	return nil
}
