// Copyright 2023 Blink Labs Software
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
	"encoding/json"
	"errors"
	"os"
	"strings"

	"github.com/blinklabs-io/nview/internal/config"
	"github.com/shirou/gopsutil/v3/process"
)

// errMissingConfigValue is a stable, comparable error returned when a
// "--config" argument in a monitored process's command line has no
// following value.
var errMissingConfigValue = errors.New("--config flag has no value")

var (
	p2p  bool   = true
	role string = "Relay"
)

func setRole() {
	cfg := config.GetConfig()
	r := "Relay"
	if cfg.Node.BlockProducer {
		r = "Core"
	} else if m := promMetrics.Load(); m != nil && m.AboutToLead > 0 {
		r = "Core"
	}
	if role != r {
		role = r
	}
}

func getP2P(ctx context.Context, processMetrics *process.Process) bool {
	cfg := config.GetConfig()

	// Dingo and Amaru are always P2P
	bin := getEffectiveNodeBinary()
	if bin == DINGO_BINARY || bin == AMARU_BINARY {
		return true
	}

	if cfg.Node.Network == "mainnet" {
		if processMetrics == nil {
			return p2p
		}
		cmd, err := processMetrics.CmdlineWithContext(ctx)
		if err == nil {
			if !strings.Contains(cmd, "p2p") &&
				strings.Contains(cmd, "--config") {
				newP2P, err := p2pFromNodeConfigCmdline(cmd, p2p)
				if err != nil && logger != nil {
					logger.Warn(
						"could not determine P2P setting from node config",
						"error", err,
					)
				}
				p2p = newP2P
			}
		}
	}
	return p2p
}

// p2pFromNodeConfigCmdline inspects a cardano-node command line for a
// "--config <path>" argument and returns the EnableP2P setting from that
// config file. It validates that "--config" has a following value before
// indexing the argument list, returning errMissingConfigValue instead of
// panicking on a trailing "--config". The last "--config" occurrence is
// authoritative: current is returned when it is missing its value or names
// an unreadable file, even if an earlier occurrence parsed successfully.
func p2pFromNodeConfigCmdline(cmd string, current bool) (bool, error) {
	cmdArray := strings.Split(cmd, " ")
	result := current
	var err error
	for p, arg := range cmdArray {
		if arg != "--config" {
			continue
		}
		if p+1 >= len(cmdArray) {
			result = current
			err = errMissingConfigValue
			continue
		}
		nodeConfigFile := cmdArray[p+1]
		buf, readErr := os.ReadFile(nodeConfigFile)
		if readErr != nil {
			result = current
			err = readErr
			continue
		}
		type nodeConfig struct {
			EnableP2P bool `json:"EnableP2P"`
		}
		var nc nodeConfig
		if unmarshalErr := json.Unmarshal(buf, &nc); unmarshalErr != nil {
			result = false
			err = unmarshalErr
		} else {
			result = nc.EnableP2P
			err = nil
		}
	}
	return result, err
}
