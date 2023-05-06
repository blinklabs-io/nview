package main

import (
	"fmt"
	"github.com/kelseyhightower/envconfig"
)

type Config struct {
	App        AppConfig
	Node       NodeConfig
	Prometheus PrometheusConfig
}

type AppConfig struct {
	NodeName string `envconfig:"NODE_NAME"`
	Network  string `envconfig:"NETWORK"`
}

type NodeConfig struct {
	Binary string // TODO: make this configurable
	Port   uint   `envconfig:"CARDANO_PORT"`
}

type PrometheusConfig struct {
	Host    string `envconfig:"PROM_HOST"`
	Port    uint   `envconfig:"PROM_PORT"`
	Timeout uint   `envconfig:"PROM_TIMEOUT"`
}

// Singleton config instance with default values
var globalConfig = &Config{
	App: AppConfig{
		NodeName: "Cardano Node",
		Network:  "Mainnet",
	},
	Node: NodeConfig{
		Binary: "cardano-node",
		Port:   3001,
	},
	Prometheus: PrometheusConfig{
		Host:    "127.0.0.1",
		Port:    12798,
		Timeout: 3,
	},
}

func (c *Config) LoadConfig() error {
	// Load config values from environment variables
	// We use "dummy" as the app name here to (mostly) prevent picking up env
	// vars that we hadn't explicitly specified in annotations above
	err := envconfig.Process("dummy", c)
	if err != nil {
		return fmt.Errorf("error processing environment: %s", err)
	}
	return nil
}

// GetConfig returns the global config instance
func GetConfig() *Config {
	return globalConfig
}
