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
	"fmt"
	"net"
	"os"

	ouroboros "github.com/blinklabs-io/gouroboros"
	"github.com/blinklabs-io/gouroboros/protocol/chainsync"
	"github.com/blinklabs-io/gouroboros/protocol/localstatequery"
)

func buildLocalStateQueryConfig() localstatequery.Config {
	return localstatequery.NewConfig()
}

//nolint:unused
func buildChainSyncConfig() chainsync.Config {
	return chainsync.NewConfig()
}

func createClientConnection(cfg *Config) net.Conn {
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
func getGenesisConfig(cfg *Config) *localstatequery.GenesisConfigResult {
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

// Get Protocol Parameters from a running node using Ouroboros NtC
func getProtocolParams(cfg *Config) *localstatequery.CurrentProtocolParamsResult {
	var result *localstatequery.CurrentProtocolParamsResult
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
	result, err = oConn.LocalStateQuery().Client.GetCurrentProtocolParams()
	if err != nil {
		return result
	}
	return result
}

// Get remote tip
//
//nolint:unused
func getRemoteTip(cfg *Config, address string) *chainsync.Tip {
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
