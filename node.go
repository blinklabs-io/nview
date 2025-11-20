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
	"os"
	"strings"

	"github.com/blinklabs-io/nview/internal/config"
	"github.com/shirou/gopsutil/v3/process"
)

var (
	p2p  bool   = true
	role string = "Relay"
)

func setRole() {
	cfg := config.GetConfig()
	r := "Relay"
	if cfg.Node.BlockProducer {
		r = "Core"
	} else if promMetrics != nil && promMetrics.AboutToLead > 0 {
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
				cmdArray := strings.Split(cmd, " ")
				for p, arg := range cmdArray {
					if arg == "--config" {
						nodeConfigFile := cmdArray[p+1]
						buf, err := os.ReadFile(nodeConfigFile)
						if err == nil {
							type nodeConfig struct {
								EnableP2P bool `json:"EnableP2P"`
							}
							var nc nodeConfig
							err = json.Unmarshal(buf, &nc)
							if err != nil {
								p2p = false
							} else {
								p2p = nc.EnableP2P
							}
						}
					}
				}
			}
		}
	}
	return p2p
}
