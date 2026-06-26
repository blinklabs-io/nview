// Copyright 2025 Blink Labs Software
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

package main

import (
	"context"
	_ "embed"
	"fmt"
	"net"
	"os/exec"
	"strings"
	"time"

	"github.com/oschwald/geoip2-golang"
)

func getNodeVersion() (version string, revision string, err error) {
	binary := getEffectiveNodeBinary()
	cmd := exec.Command(binary, "version") // #nosec G204
	stdout, err := cmd.Output()
	if err != nil {
		return "N/A", "N/A", fmt.Errorf(
			"failed to execute %s version command: %w",
			binary,
			err,
		)
	}
	return parseNodeVersionOutput(binary, strings.TrimSpace(string(stdout)))
}

func parseNodeVersionOutput(
	binary string,
	output string,
) (version string, revision string, err error) {
	// Handle Dingo format: "devel (commit 80ae952)" or "v0.17.0 (commit 1f54020)"
	if commitStart := strings.Index(output, "(commit "); commitStart >= 0 {
		version = strings.TrimSpace(output[:commitStart])
		hashStart := commitStart + len("(commit ")
		hashEnd := strings.Index(output[hashStart:], ")")
		if version != "" && hashEnd >= 0 {
			revision = strings.TrimSpace(output[hashStart : hashStart+hashEnd])
			if revision != "" {
				if len(revision) > 8 {
					revision = revision[0:8]
				}
				return version, revision, nil
			}
		}
	}

	// Handle cardano-node format (fallback)
	strArray := strings.Fields(output)
	if len(strArray) < 2 {
		return "N/A", "N/A", fmt.Errorf(
			"unexpected version format from %s: output has %d parts, expected at least 2",
			binary,
			len(strArray),
		)
	}
	version = strArray[1]
	for idx, part := range strArray {
		if part == "rev" && idx+1 < len(strArray) {
			revision = strArray[idx+1]
			break
		}
	}
	if revision == "" {
		return "N/A", "N/A", fmt.Errorf(
			"unexpected version format from %s: output is missing git revision",
			binary,
		)
	}
	if len(revision) > 8 {
		revision = revision[0:8]
	}
	return version, revision, nil
}

var publicIP *net.IP

func getPublicIP(ctx context.Context) (net.IP, error) {
	// First, check for external address using custom resolver so we can
	// use a given DNS server to resolve our public address
	r := &net.Resolver{
		PreferGo: true,
		Dial: func(ctx context.Context, network, address string) (net.Conn, error) {
			d := net.Dialer{
				Timeout: time.Second * time.Duration(3),
			}
			return d.DialContext(ctx, network, "resolver1.opendns.com:53")
		},
	}
	// Lookup special address to get our public IP
	ips, err := r.LookupIP(ctx, "ip4", "myip.opendns.com")
	if err != nil {
		return nil, err
	}
	if ips != nil {
		return ips[0], nil
	}
	return nil, nil
}

// MaxMind database, available from https://www.maxmind.com
//
//go:embed resources/GeoLite2-City.mmdb
var MaxmindDB []byte

func getGeoIP(ctx context.Context, address string) string {
	select {
	case <-ctx.Done():
		return "---"
	default:
	}

	db, err := geoip2.FromBytes(MaxmindDB)
	if err != nil {
		return "---"
	}
	defer db.Close()
	ip := net.ParseIP(address)
	record, err := db.City(ip)
	if err != nil {
		return "---"
	}
	if len(record.City.Names["en"]) == 0 {
		if len(record.Country.IsoCode) == 0 {
			return "---"
		} else {
			return record.Country.IsoCode
		}
	}
	return fmt.Sprintf(
		"%v, %v",
		record.City.Names["en"],
		record.Country.IsoCode,
	)
}
