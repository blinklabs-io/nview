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
	"os"

	"github.com/blinklabs-io/gouroboros"
	"github.com/kelseyhightower/envconfig"
	"gopkg.in/yaml.v2"
)

type Config struct {
	App        AppConfig        `yaml:"app"`
	Node       NodeConfig       `yaml:"node"`
	Prometheus PrometheusConfig `yaml:"prometheus"`
}

type AppConfig struct {
	NodeName string `yaml:"nodeName" envconfig:"NODE_NAME"`
	Network  string `yaml:"network" envconfig:"NETWORK"`
	Retries  uint32 `yaml:"retries" envconfig:"RETRIES"`
}

type NodeConfig struct {
	Binary            string // TODO: make this configurable
	Network           string `yaml:"network" envconfig:"CARDANO_NETWORK"`
	NetworkMagic      uint32 `yaml:"networkMagic" envconfig:"CARDANO_NODE_NETWORK_MAGIC"`
	Port              uint32 `yaml:"port" envconfig:"CARDANO_PORT"`
	ShelleyTransEpoch int32  `yaml:"shellyTransEpoch" envconfig:"SHELLEY_TRANS_EPOCH"`
	SocketPath        string `yaml:"socketPath" envconfig:"CARDANO_NODE_SOCKET_PATH"`
}

type PrometheusConfig struct {
	Host    string `yaml:"host" envconfig:"PROM_HOST"`
	Port    uint32 `yaml:"port" envconfig:"PROM_PORT"`
	Timeout uint32 `yaml:"timeout" envconfig:"PROM_TIMEOUT"`
}

// Singleton config instance with default values
var globalConfig = &Config{
	App: AppConfig{
		NodeName: "Cardano Node",
		Network:  "",
		Retries:  3,
	},
	Node: NodeConfig{
		Binary:     "cardano-node",
		Network:    "mainnet",
		Port:       3001,
		SocketPath: "/opt/cardano/ipc/socket",
	},
	Prometheus: PrometheusConfig{
		Host:    "127.0.0.1",
		Port:    12798,
		Timeout: 3,
	},
}

func LoadConfig(configFile string) (*Config, error) {
	// Load config file as YAML if provided
	if configFile != "" {
		buf, err := os.ReadFile(configFile)
		if err != nil {
			return nil, fmt.Errorf("error reading config file: %s", err)
		}
		err = yaml.Unmarshal(buf, globalConfig)
		if err != nil {
			return nil, fmt.Errorf("error parsing config file: %s", err)
		}
	}
	// Load config values from environment variables
	// We use "dummy" as the app name here to (mostly) prevent picking up env
	// vars that we hadn't explicitly specified in annotations above
	err := envconfig.Process("dummy", globalConfig)
	if err != nil {
		return nil, fmt.Errorf("error processing environment: %s", err)
	}
	// Populate NetworkMagic from named networks
	if err := globalConfig.populateNetworkMagic(); err != nil {
		return nil, err
	}
	// Populate ShelleyTransEpoch from named networks
	if err := globalConfig.populateShelleyTransEpoch(); err != nil {
		return nil, err
	}
	return globalConfig, nil
}

// GetConfig returns the global config instance
func GetConfig() *Config {
	return globalConfig
}

// Populates NetworkMagic from named networks
func (c *Config) populateNetworkMagic() error {
	if c.Node.NetworkMagic == 0 {
		if c.App.Network != "" {
			network := ouroboros.NetworkByName(c.App.Network)
			if network == ouroboros.NetworkInvalid {
				return fmt.Errorf("unknown network: %s", c.App.Network)
			}
			// Set Node's network, networkMagic, port, and socketPath
			c.Node.Network = c.App.Network
			c.Node.NetworkMagic = uint32(network.NetworkMagic)
			c.Node.Port = uint32(3001)
			c.Node.SocketPath = "/ipc/node.socket"
			return nil
		}
		if c.Node.Network != "" {
			network := ouroboros.NetworkByName(c.Node.Network)
			if network == ouroboros.NetworkInvalid {
				return fmt.Errorf("unknown network: %s", c.Node.Network)
			}
			c.Node.NetworkMagic = uint32(network.NetworkMagic)
			return nil
		}
	}
	return nil
}

// Populates ShelleyTransEpoch from named networks
func (c *Config) populateShelleyTransEpoch() error {
	if c.Node.ShelleyTransEpoch != int32(-1) {
		return nil
	}
	c.Node.ShelleyTransEpoch = 0
	if c.App.Network != "" {
		switch c.App.Network {
		case "preprod":
			c.Node.ShelleyTransEpoch = 4
		case "mainnet":
			c.Node.ShelleyTransEpoch = 208
		}
	}
	if c.Node.Network != "" {
		switch c.Node.Network {
		case "preprod":
			c.Node.ShelleyTransEpoch = 4
		case "mainnet":
			c.Node.ShelleyTransEpoch = 208
		}
	}
	return nil
}
