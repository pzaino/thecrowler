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
	"context"
	"crypto/tls"
	"errors"
	"log"
	"net"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"
)

const (
	errIPNotAllowed = "ip address is not allowed"
)

// InitLogger initializes the logger
func InitLogger(appName string) {
	log.SetOutput(os.Stdout)

	// Retrieve process PID
	pid := os.Getpid()

	// Retrieve process PPID
	ppid := os.Getppid()

	// Retrieve default network address
	hostname, err := os.Hostname()
	if err != nil {
		hostname = "localhost"
	}

	// create process instance name: <hostname>:<pid>:<ppid>
	processName := hostname + ":" + strconv.Itoa(pid) + ":" + strconv.Itoa(ppid)

	// Setting the log prefix
	loggerPrefix = appName + " [" + processName + "]: "

	// Setting the log flags (date, time, microseconds, short file name)
	log.SetFlags(log.LstdFlags | log.Ldate | log.Ltime | log.Lmicroseconds)
}

// UpdateLoggerConfig Updates the logger configuration
func UpdateLoggerConfig() {
	if debugLevel > 0 {
		log.SetFlags(log.LstdFlags | log.Ldate | log.Ltime | log.Lmicroseconds | log.Lshortfile)
	} else {
		log.SetFlags(log.LstdFlags | log.Ldate | log.Ltime | log.Lmicroseconds)
	}
}

// SetDebugLevel allows to set the current debug level
func SetDebugLevel(dbgLvl DbgLevel) {
	debugLevel = dbgLvl
}

// GetDebugLevel returns the value of the current debug level
func GetDebugLevel() DbgLevel {
	return debugLevel
}

// DebugMsg is a function that prints debug information
func DebugMsg(dbgLvl DbgLevel, msg string, args ...interface{}) {
	// For always-log messages (Info, Warning, Error, Fatal)
	if dbgLvl <= DbgLvlInfo {
		log.Printf(loggerPrefix+msg, args...)
		if dbgLvl == DbgLvlFatal {
			os.Exit(1)
		}
		return
	}
	if debugLevel >= dbgLvl {
		// For Debug messages, log only if the set debug level is equal or higher
		log.Printf(loggerPrefix+msg, args...)
	}
}

// IsDisallowedIP parses the ip to determine if we should allow the HTTP client to continue
func IsDisallowedIP(hostIP string, level int) bool {
	ip := net.ParseIP(hostIP)
	switch level {
	case 0: // Allow only public IPs
		return ip.IsMulticast() || ip.IsUnspecified() || ip.IsLoopback() || ip.IsPrivate()
	case 1: // Allow only private IPs
		return ip.IsMulticast() || ip.IsUnspecified() || ip.IsLoopback() || ip.IsGlobalUnicast()
	case 2: // Allow only loopback IPs
		return ip.IsMulticast() || ip.IsUnspecified() || ip.IsPrivate() || ip.IsGlobalUnicast()
	case 3: // Allow only valid IPs
		return ip.IsMulticast() || ip.IsUnspecified()
	default:
		return ip.IsMulticast() || ip.IsUnspecified() || ip.IsLoopback() || ip.IsPrivate()
	}
}

// Create a function that checks if a path is correct and if it exists
func IsPathCorrect(path string) bool {
	if _, err := os.Stat(path); os.IsNotExist(err) {
		return false
	}
	return true
}

func SafeTransport(timeout int, sslmode string) *http.Transport {
	sslmode = strings.ToLower(strings.TrimSpace(sslmode))
	if sslmode == "disable" || sslmode == "disabled" {
		return &http.Transport{
			DialContext: dialContextWithIPCheck(time.Second * time.Duration(timeout)),
		}
	}
	return &http.Transport{
		DialContext:         dialContextWithIPCheck(time.Second * time.Duration(timeout)),
		DialTLS:             dialTLSWithIPCheck(time.Second * time.Duration(timeout)),
		TLSHandshakeTimeout: time.Second * time.Duration(timeout),
	}
}

func dialContextWithIPCheck(timeout time.Duration) func(ctx context.Context, network, addr string) (net.Conn, error) {
	return func(ctx context.Context, network, addr string) (net.Conn, error) {
		c, err := net.DialTimeout(network, addr, timeout)
		if err != nil {
			return nil, err
		}
		ip, _, _ := net.SplitHostPort(c.RemoteAddr().String())
		if IsDisallowedIP(ip, 3) {
			return nil, errors.New(errIPNotAllowed)
		}
		return c, err
	}
}

func dialTLSWithIPCheck(timeout time.Duration) func(network, addr string) (net.Conn, error) {
	return func(network, addr string) (net.Conn, error) {
		dialer := &net.Dialer{Timeout: timeout}
		c, err := tls.DialWithDialer(dialer, network, addr, &tls.Config{MinVersion: tls.VersionTLS13})
		if err != nil {
			return nil, err
		}

		ip, _, _ := net.SplitHostPort(c.RemoteAddr().String())
		if IsDisallowedIP(ip, 3) {
			return nil, errors.New(errIPNotAllowed)
		}

		err = c.Handshake()
		if err != nil {
			return c, err
		}

		return c, c.Handshake()
	}
}
