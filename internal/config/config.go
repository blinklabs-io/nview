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

package config

import (
	"errors"
	"fmt"
	"os"

	"github.com/kelseyhightower/envconfig"
	"gopkg.in/yaml.v2"
)

// Config represents the complete configuration structure for nview.
// It contains application settings, node configuration, and Prometheus settings.
type Config struct {
	App        AppConfig        `yaml:"app"`
	Node       NodeConfig       `yaml:"node"`
	Prometheus PrometheusConfig `yaml:"prometheus"`
}

// AppConfig contains application-level configuration options.
type AppConfig struct {
	NodeName      string `yaml:"nodeName"      envconfig:"NODE_NAME"`
	Network       string `yaml:"network"       envconfig:"NETWORK"`
	Refresh       uint32 `yaml:"refresh"       envconfig:"REFRESH"`
	Retries       uint32 `yaml:"retries"       envconfig:"RETRIES"`
	LogBufferSize uint32 `yaml:"logBufferSize" envconfig:"LOG_BUFFER_SIZE"`
}

// NodeConfig contains configuration specific to the Cardano node being monitored.
type NodeConfig struct {
	ByronGenesis      ByronGenesisConfig   `yaml:"byron"`
	Binary            string               `yaml:"binary"           envconfig:"CARDANO_NODE_BINARY"`
	Pid               int32                `yaml:"pid"              envconfig:"CARDANO_NODE_PID"`
	PidFile           string               `yaml:"pidFile"          envconfig:"CARDANO_NODE_PID_FILE"`
	Network           string               `yaml:"network"          envconfig:"CARDANO_NETWORK"`
	NetworkMagic      uint32               `yaml:"networkMagic"     envconfig:"CARDANO_NODE_NETWORK_MAGIC"`
	Port              uint32               `yaml:"port"             envconfig:"CARDANO_PORT"`
	ShelleyGenesis    ShelleyGenesisConfig `yaml:"shelley"`
	ShelleyTransEpoch int32                `yaml:"shellyTransEpoch" envconfig:"SHELLEY_TRANS_EPOCH"`
	BlockProducer     bool                 `yaml:"blockProducer"    envconfig:"CARDANO_BLOCK_PRODUCER"`
}

// PrometheusConfig contains settings for connecting to the node's Prometheus metrics endpoint.
type PrometheusConfig struct {
	Host    string `yaml:"host"    envconfig:"PROM_HOST"`
	Port    uint32 `yaml:"port"    envconfig:"PROM_PORT"`
	Refresh uint32 `yaml:"refresh" envconfig:"PROM_REFRESH"`
	Timeout uint32 `yaml:"timeout" envconfig:"PROM_TIMEOUT"`
}

// ByronGenesisConfig contains Byron-era genesis parameters.
type ByronGenesisConfig struct {
	StartTime   uint64 `yaml:"startTime"   envconfig:"BYRON_GENESIS_START_SEC"`
	EpochLength uint64 `yaml:"epochLength" envconfig:"BYRON_EPOCH_LENGTH"`
	K           uint64 `yaml:"k"           envconfig:"BYRON_K"`
	SlotLength  uint64 `yaml:"slotLength"  envconfig:"BYRON_SLOT_LENGTH"`
}

// ShelleyGenesisConfig contains Shelley-era genesis parameters.
type ShelleyGenesisConfig struct {
	EpochLength       uint64 `yaml:"epochLength"       envconfig:"SHELLEY_EPOCH_LENGTH"`
	SlotLength        uint64 `yaml:"slotLength"        envconfig:"SHELLEY_SLOT_LENGTH"`
	SlotsPerKESPeriod uint64 `yaml:"slotsPerKESPeriod" envconfig:"SHELLEY_SLOTS_PER_KES_PERIOD"`
}

// getDefaultConfig returns a new Config instance with default values
func getDefaultConfig() *Config {
	return &Config{
		App: AppConfig{
			NodeName:      "Cardano Node",
			Network:       "",
			Refresh:       1,
			Retries:       3,
			LogBufferSize: 1000,
		},
		Node: NodeConfig{
			Binary:            "cardano-node",
			Network:           "mainnet",
			Port:              3001,
			ShelleyTransEpoch: -1,
		},
		Prometheus: PrometheusConfig{
			Host:    "127.0.0.1",
			Port:    12798,
			Refresh: 3,
			Timeout: 3,
		},
	}
}

// Singleton config instance with default values
var globalConfig = getDefaultConfig()

// LoadConfig loads configuration from a YAML file and environment variables.
// Environment variables take precedence over file values. If configFile is empty,
// only environment variables and defaults are used.
func LoadConfig(configFile string) (*Config, error) {
	cfg := getDefaultConfig()
	// Load config file as YAML if provided
	if configFile != "" {
		buf, err := os.ReadFile(configFile)
		if err != nil {
			return nil, fmt.Errorf("error reading config file: %w", err)
		}
		err = yaml.Unmarshal(buf, cfg)
		if err != nil {
			return nil, fmt.Errorf("error parsing config file: %w", err)
		}
	}
	// Load config values from environment variables
	// We use "dummy" as the app name here to (mostly) prevent picking up env
	// vars that we hadn't explicitly specified in annotations above
	err := envconfig.Process("dummy", cfg)
	if err != nil {
		return nil, fmt.Errorf("error processing environment: %w", err)
	}
	// Populate NetworkMagic from named networks
	if err := cfg.populateNetworkMagic(); err != nil {
		return nil, err
	}
	// Populate ByronGenesis from named networks
	if err := cfg.populateByronGenesis(); err != nil {
		return nil, err
	}
	// Populate ShelleyGenesis from named networks
	if err := cfg.populateShelleyGenesis(); err != nil {
		return nil, err
	}
	// Populate ShelleyTransEpoch from named networks
	if err := cfg.populateShelleyTransEpoch(); err != nil {
		return nil, err
	}
	// Update global config for singleton access
	globalConfig = cfg
	return cfg, nil
}

// GetConfig returns the global config instance.
// This is a singleton accessor for the loaded configuration.
func GetConfig() *Config {
	return globalConfig
}

// Populates NetworkMagic from named networks
func (c *Config) populateNetworkMagic() error {
	if c.Node.NetworkMagic == 0 {
		if c.App.Network != "" {
			switch c.App.Network {
			case "mainnet":
				c.Node.NetworkMagic = 764824073
			case "preprod":
				c.Node.NetworkMagic = 1
			case "preview":
				c.Node.NetworkMagic = 2
			default:
				return errors.New("unknown network")
			}
			c.Node.Port = 3001
			return nil
		} else if c.Node.Network != "" {
			switch c.Node.Network {
			case "mainnet":
				c.Node.NetworkMagic = 764824073
			case "preprod":
				c.Node.NetworkMagic = 1
			case "preview":
				c.Node.NetworkMagic = 2
			default:
				return errors.New("unknown network")
			}
			return nil
		} else {
			// Default to mainnet if no network specified
			c.Node.Network = "mainnet"
			c.Node.NetworkMagic = 764824073
			c.Node.Port = 3001
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
	} else if c.Node.Network != "" {
		switch c.Node.Network {
		case "preprod":
			c.Node.ShelleyTransEpoch = 4
		case "mainnet":
			c.Node.ShelleyTransEpoch = 208
		}
	} else {
		return errors.New("unable to populate shelley transition epoch")
	}
	return nil
}

// Populates ByronGenesisConfig from named networks
func (c *Config) populateByronGenesis() error {
	if c.Node.ByronGenesis.StartTime != 0 {
		return nil
	}
	// Our slot length is always 20000 in supported networks
	c.Node.ByronGenesis.SlotLength = 20000
	// Our K is 2160, except preview, which we'll override below
	c.Node.ByronGenesis.K = 2160
	if c.App.Network != "" {
		switch c.App.Network {
		case "preview":
			c.Node.ByronGenesis.K = 432
			c.Node.ByronGenesis.StartTime = 1666656000
		case "preprod":
			c.Node.ByronGenesis.StartTime = 1654041600
		case "sancho":
			c.Node.ByronGenesis.K = 432
			c.Node.ByronGenesis.StartTime = 1686789000
		case "mainnet":
			c.Node.ByronGenesis.StartTime = 1506203091
		}
	} else if c.Node.Network != "" {
		switch c.Node.Network {
		case "preview":
			c.Node.ByronGenesis.K = 432
			c.Node.ByronGenesis.StartTime = 1666656000
		case "preprod":
			c.Node.ByronGenesis.StartTime = 1654041600
		case "sancho":
			c.Node.ByronGenesis.K = 432
			c.Node.ByronGenesis.StartTime = 1686789000
		case "mainnet":
			c.Node.ByronGenesis.StartTime = 1506203091
		}
	} else {
		return errors.New("unable to populate byron genesis config")
	}
	c.Node.ByronGenesis.EpochLength = (10 * c.Node.ByronGenesis.K)
	return nil
}

// Populates ShelleyGenesisConfig from named networks
func (c *Config) populateShelleyGenesis() error {
	if c.Node.ShelleyGenesis.EpochLength != 0 {
		return nil
	}
	// Our slot length is always 1000 in supported networks
	c.Node.ShelleyGenesis.SlotLength = 1000
	// Our slots per KES period is always 129600 in supported networks
	c.Node.ShelleyGenesis.SlotsPerKESPeriod = 129600
	// Our epoch length is 432000, except sanchonet/preview
	c.Node.ShelleyGenesis.EpochLength = 432000
	if c.App.Network != "" {
		switch c.App.Network {
		case "sancho":
			c.Node.ShelleyGenesis.EpochLength = 86400
		case "preview":
			c.Node.ShelleyGenesis.EpochLength = 86400
		}
	} else if c.Node.Network != "" {
		switch c.Node.Network {
		case "sancho":
			c.Node.ShelleyGenesis.EpochLength = 86400
		case "preview":
			c.Node.ShelleyGenesis.EpochLength = 86400
		}
	} else {
		return errors.New("unable to populate shelley genesis config")
	}
	return nil
}
