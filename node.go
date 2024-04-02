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
	"fmt"
	"net"
	"os"
	"strings"

	ouroboros "github.com/blinklabs-io/gouroboros"
	"github.com/blinklabs-io/gouroboros/protocol/chainsync"
	"github.com/blinklabs-io/gouroboros/protocol/localstatequery"
	"github.com/shirou/gopsutil/v3/process"

	"github.com/blinklabs-io/nview/internal/config"
)

var (
	genesisConfig *localstatequery.GenesisConfigResult
	p2p           bool   = true
	role          string = "Relay"
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
	if cfg.Node.Network == "mainnet" {
		if processMetrics == nil {
			return p2p
		}
		cmd, err := processMetrics.CmdlineWithContext(ctx)
		if err == nil {
			if !strings.Contains(cmd, "p2p") && strings.Contains(cmd, "--config") {
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

func buildLocalStateQueryConfig() localstatequery.Config {
	return localstatequery.NewConfig()
}

//nolint:unused
func buildChainSyncConfig() chainsync.Config {
	return chainsync.NewConfig()
}

func createClientConnection(cfg *config.Config) net.Conn {
	var err error
	var conn net.Conn
	var dialProto string
	var dialAddress string
	if cfg.Node.SocketPath != "" {
		dialProto = "unix"
		dialAddress = cfg.Node.SocketPath
	} else {
		return conn
	}

	conn, err = net.Dial(dialProto, dialAddress)
	if err != nil {
		fmt.Printf("ERROR: %s\n", err)
		os.Exit(1)
	}
	return conn
}

// Get Genesis Config from a running node using Ouroboros NtC
func getGenesisConfig(cfg *config.Config) *localstatequery.GenesisConfigResult {
	var result *localstatequery.GenesisConfigResult
	// Get a connection and setup our error channels
	conn := createClientConnection(cfg)
	if conn == nil {
		return result
	}
	errorChan := make(chan error)
	go func() {
		for {
			err := <-errorChan
			fmt.Printf("ERROR: %s\n", err)
			os.Exit(1)
		}
	}()
	// Configure our Ouroboros connection
	oConn, err := ouroboros.NewConnection(
		ouroboros.WithConnection(conn),
		ouroboros.WithNetworkMagic(uint32(cfg.Node.NetworkMagic)),
		ouroboros.WithErrorChan(errorChan),
		ouroboros.WithNodeToNode(false),
		ouroboros.WithKeepAlive(false),
		ouroboros.WithLocalStateQueryConfig(buildLocalStateQueryConfig()),
	)
	if err != nil {
		return result
	}
	// Query our client
	oConn.LocalStateQuery().Client.Start()
	result, err = oConn.LocalStateQuery().Client.GetGenesisConfig()
	if err != nil {
		return result
	}
	return result
}

// Get remote tip
//
//nolint:unused
func getRemoteTip(cfg *config.Config, address string) *chainsync.Tip {
	var result *chainsync.Tip
	// Get a connection and setup our error channels
	conn := createRemoteClientConnection(address)
	if conn == nil {
		return result
	}
	errorChan := make(chan error)
	go func() {
		for {
			err := <-errorChan
			fmt.Printf("ERROR: %s\n", err)
			os.Exit(1)
		}
	}()
	oConn, err := ouroboros.NewConnection(
		ouroboros.WithConnection(conn),
		ouroboros.WithNetworkMagic(uint32(cfg.Node.NetworkMagic)),
		ouroboros.WithErrorChan(errorChan),
		ouroboros.WithNodeToNode(true),
		ouroboros.WithKeepAlive(false),
		ouroboros.WithChainSyncConfig(buildChainSyncConfig()),
	)
	if err != nil {
		return result
	}
	// Query our client
	oConn.ChainSync().Client.Start()
	result, err = oConn.ChainSync().Client.GetCurrentTip()
	if err != nil {
		return result
	}
	return result
}
