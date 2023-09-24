// Copyright 2023 Blink Labs, LLC.
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
	"net"
	"os/exec"
	"strings"
	"time"

	"github.com/blinklabs-io/nview/internal/config"
)

func getNodeVersion() (version string, revision string, err error) {
	cfg := config.GetConfig()
	cmd := exec.Command(cfg.Node.Binary, "version")
	stdout, err := cmd.Output()
	if err != nil {
		return "N/A", "N/A", err
	}
	strArray := strings.Split(string(stdout), string(' '))
	version = strArray[1]
	revision = strArray[7]
	if len(revision) > 8 {
		revision = revision[0:8]
	}
	return version, revision, nil
}

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
