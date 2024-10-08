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

	cmn "github.com/pzaino/thecrowler/pkg/common"
)

// GetIPs returns the IP addresses of the provided URL
func (ni *NetInfo) GetIPs() error {
	host := urlToHost(ni.URL)

	// Get IP addresses
	ips, err := net.LookupIP(host)
	if err != nil {
		return fmt.Errorf("error looking up host IP addresses: %v", err)
	}
	ipsStr := ipsToString(ips)

	ipMap := make(map[string]bool)
	for _, ip := range ipsStr {
		ipMap[ip] = true
	}

	// Get IP addresses for the domain
	host = urlToDomain(ni.URL)
	ips, err = net.LookupIP(host)
	if err != nil {
		return fmt.Errorf("error looking up domain IP addresses: %v", err)
	}
	for _, ip := range ips {
		ipMap[ip.String()] = true
	}

	ipsStr = make([]string, 0, len(ipMap))
	for ip := range ipMap {
		ipsStr = append(ipsStr, ip)
	}

	var newIPData []IPInfo

	for _, newIP := range ipsStr {
		if ni.isIPNew(newIP) {
			ipInfo, err := ni.processNewIP(newIP)
			if err != nil {
				// Log error or handle it according to your error policy
				cmn.DebugMsg(cmn.DbgLvlDebug, "error processing new IP %s: %v", newIP, err)
				continue
			}
			newIPData = append(newIPData, ipInfo)
		}
	}

	ni.updateIPData(newIPData) // Update ni.IPs with newIPData

	return nil
}

// isIPNew checks if the provided IP address is new in the current NetInfo instance
func (ni *NetInfo) isIPNew(ip string) bool {
	for _, existingIP := range ni.IPs.IP {
		if existingIP == ip {
			return false
		}
	}
	return true
}

// processNewIP processes the new IP address to get geolocation and network information
func (ni *NetInfo) processNewIP(ip string) (IPInfo, error) {
	ipInfo := IPInfo{IP: ip} // Assume IPInfo is a struct that matches your IPData structure

	// Get geolocation info
	if ni.Config.Geolocation.Enabled {
		geoLocation, err := DetectLocation(ip, ni.Config.Geolocation)
		if err != nil {
			return ipInfo, fmt.Errorf("error detecting geolocation for IP %s: %v", ip, err)
		}
		ipInfo.UpdateWithGeoLocation(geoLocation)
	}

	// Get network information
	entity, err := getIPInfo(ni, ip)
	if err == nil {
		ipInfo.UpdateWithIPInfo(&entity)
	} else {
		ipInfo.SetDefaults()
	}

	return ipInfo, nil
}

// updateIPData updates the IPData fields of the NetInfo instance with the provided newIPData
func (ni *NetInfo) updateIPData(newIPData []IPInfo) {
	// Iterate over newIPData to update ni.IPs fields
	for _, ipInfo := range newIPData {
		ni.IPs.IP = append(ni.IPs.IP, ipInfo.IP)
		ni.IPs.ASN = append(ni.IPs.ASN, ipInfo.ASN)
		ni.IPs.CIDR = append(ni.IPs.CIDR, ipInfo.CIDR)
		ni.IPs.NetRange = append(ni.IPs.NetRange, ipInfo.NetRange)
		ni.IPs.NetName = append(ni.IPs.NetName, ipInfo.NetName)
		ni.IPs.NetHandle = append(ni.IPs.NetHandle, ipInfo.NetHandle)
		ni.IPs.NetParent = append(ni.IPs.NetParent, ipInfo.NetParent)
		ni.IPs.NetType = append(ni.IPs.NetType, ipInfo.NetType)
		ni.IPs.Country = append(ni.IPs.Country, ipInfo.Country)
		ni.IPs.CountryCode = append(ni.IPs.CountryCode, ipInfo.CountryCode)
		ni.IPs.City = append(ni.IPs.City, ipInfo.City)
		ni.IPs.Latitude = append(ni.IPs.Latitude, ipInfo.Latitude)
		ni.IPs.Longitude = append(ni.IPs.Longitude, ipInfo.Longitude)
	}
}

// UpdateWithGeoLocation updates the IPInfo fields with the provided geolocation data
func (info *IPInfo) UpdateWithGeoLocation(geoLocation *DetectedLocation) {
	// Assuming DetectedLocation has similar fields to what we need
	info.Country = geoLocation.CountryName
	info.CountryCode = geoLocation.CountryCode
	info.City = geoLocation.City
	info.Latitude = geoLocation.Latitude
	info.Longitude = geoLocation.Longitude
}

// UpdateWithIPInfo updates the IPInfo fields with the provided network data
func (info *IPInfo) UpdateWithIPInfo(entity *ipExtraData) {
	// NetworkInfo is assumed to be the struct with network data
	info.ASN = defaultNA(entity.ASN)
	info.CIDR = defaultNA(entity.CIDR)
	info.NetRange = defaultNA(entity.NetRange)
	info.NetName = defaultNA(entity.NetName)
	info.NetHandle = defaultNA(entity.NetHandle)
	info.NetParent = defaultNA(entity.NetParent)
	info.NetType = defaultNA(entity.NetType)
	// Only update Country and City if they were not set by geolocation
	if info.Country == "" {
		info.Country = defaultNA(entity.Country)
	}
	if info.City == "" {
		info.City = defaultNA(entity.City)
	}
	info.CountryCode = defaultNA(entity.CountryCode)
	// Latitude and Longitude are set only if not already provided by geolocation
	if info.Latitude == 0 {
		info.Latitude = entity.Latitude
	}
	if info.Longitude == 0 {
		info.Longitude = entity.Longitude
	}
}

// SetDefaults sets the default values for all IPInfo fields
func (info *IPInfo) SetDefaults() {
	info.ASN = "N/A"
	info.CIDR = "N/A"
	info.NetRange = "N/A"
	info.NetName = "N/A"
	info.NetHandle = "N/A"
	info.NetParent = "N/A"
	info.NetType = "N/A"
	info.Country = "N/A"
	info.CountryCode = "N/A"
	info.City = "N/A"
	// Assuming 0 is the default/unset value for Latitude and Longitude
	info.Latitude = 0
	info.Longitude = 0
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
				cmn.DebugMsg(cmn.DbgLvlError, "looking up hostnames for IP %s: %v", ip, err)
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
	var err error
	// Check if url is an IP address
	if net.ParseIP(ni.URL) != nil {
		ni.IPs.IP = append(ni.IPs.IP, ni.URL)
	} else {
		// Get IP addresses
		err = ni.GetIPs()
		if err != nil {
			return err
		}
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
	var err error

	if ni.Config.NetLookup.Enabled {
		// Get IP addresses and hostnames
		err = getEntities(ni)
		if err != nil {
			return err
		}

		// Check if host0 has been removed from the list
		if len(ni.Hosts.Host) == 0 {
			host0 := urlToDomain(url)
			ni.Hosts.Host = append(ni.Hosts.Host, host0)
		}
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

	if ni.Config.ServiceScout.Enabled {
		err = ni.GetServiceScoutInfo(&ni.Config.ServiceScout)
		if err != nil {
			return err
		}
	}

	return nil
}
