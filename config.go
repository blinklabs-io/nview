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
	"github.com/Bitrue-exchange/libada-go"
	"github.com/kelseyhightower/envconfig"
	"gopkg.in/yaml.v2"
	"os"
)

type Config struct {
	App        AppConfig        `yaml:"app"`
	Node       NodeConfig       `yaml:"node"`
	Prometheus PrometheusConfig `yaml:"prometheus"`
}

type AppConfig struct {
	NodeName string `yaml:"nodeName" envconfig:"NODE_NAME"`
	Network  string `yaml:"network" envconfig:"NETWORK"`
}

type NodeConfig struct {
	Binary       string // TODO: make this configurable
	Network      string `yaml:"network" envconfig:"CARDANO_NETWORK"`
	NetworkMagic uint32 `yaml:"networkMagic" envconfig:"CARDANO_NODE_NETWORK_MAGIC"`
	Port         uint   `yaml:"port" envconfig:"CARDANO_PORT"`
}

type PrometheusConfig struct {
	Host    string `yaml:"host" envconfig:"PROM_HOST"`
	Port    uint   `yaml:"port" envconfig:"PROM_PORT"`
	Timeout uint   `yaml:"timeout" envconfig:"PROM_TIMEOUT"`
}

// Singleton config instance with default values
var globalConfig = &Config{
	App: AppConfig{
		NodeName: "Cardano Node",
		Network:  "",
	},
	Node: NodeConfig{
		Binary:  "cardano-node",
		Network: "mainnet",
		Port:    3001,
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
	return globalConfig, nil
}

// GetConfig returns the global config instance
func GetConfig() *Config {
	return globalConfig
}

func (c *Config) populateNetworkMagic() error {
	if c.Node.NetworkMagic != uint32(0) {
		return nil
	}
	if c.App.Network != "" {
		switch c.App.Network {
		case "preview":
			c.Node.NetworkMagic = libada.Preview.ProtocolMagic()
		case "preprod":
			c.Node.NetworkMagic = libada.Preprod.ProtocolMagic()
		case "testnet":
			c.Node.NetworkMagic = libada.Testnet.ProtocolMagic()
		case "mainnet":
			c.Node.NetworkMagic = libada.Mainnet.ProtocolMagic()
		default:
			return fmt.Errorf("unknown network: %s", c.App.Network)
		}
	}
	if c.Node.Network != "" {
		switch c.Node.Network {
		case "preview":
			c.Node.NetworkMagic = libada.Preview.ProtocolMagic()
		case "preprod":
			c.Node.NetworkMagic = libada.Preprod.ProtocolMagic()
		case "testnet":
			c.Node.NetworkMagic = libada.Testnet.ProtocolMagic()
		case "mainnet":
			c.Node.NetworkMagic = libada.Mainnet.ProtocolMagic()
		default:
			return fmt.Errorf("unknown network: %s", c.Node.Network)
		}
	}
	return nil
}
