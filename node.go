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

	"github.com/blinklabs-io/gouroboros"
	"github.com/blinklabs-io/gouroboros/protocol/localstatequery"
)

func buildLocalStateQueryConfig() localstatequery.Config {
	return localstatequery.NewConfig()
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
		return conn
		fmt.Printf("ERROR: %s\n", err)
		os.Exit(1)
	}
	return conn
}

// Get Protocol Parameters from a running node using Ouroboros
func getProtocolParams(cfg *Config) string {
	// Get a connection and setup our error channels
	conn := createClientConnection(cfg)
	if conn == nil {
		return fmt.Sprintf("ERROR: cannot connect to node: %v", conn)
	}
	errorChan := make(chan error)
	go func() {
		for {
			err := <-errorChan
			fmt.Printf("ERROR: %s\n", err)
			os.Exit(1)
		}
	}()
	o, err := ouroboros.New(
		ouroboros.WithConnection(conn),
		ouroboros.WithNetworkMagic(uint32(cfg.Node.NetworkMagic)),
		ouroboros.WithErrorChan(errorChan),
		ouroboros.WithNodeToNode(false),
		ouroboros.WithKeepAlive(false),
		ouroboros.WithLocalStateQueryConfig(buildLocalStateQueryConfig()),
	)
	if err != nil {
		return fmt.Sprintf("ERROR: %s\n", err)
	}
	// Start our client
	o.LocalStateQuery().Client.Start()
	protoParams, err := o.LocalStateQuery().Client.GetCurrentProtocolParams()
	if err != nil {
		return fmt.Sprintf("ERROR: failure querying protocol params: %s\n", err)
		os.Exit(1)
	}
	return fmt.Sprintf("%v", *protoParams)
}
