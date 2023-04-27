package main

import (
	"fmt"
	"github.com/kelseyhightower/envconfig"
)

type Config struct {
	Prometheus PrometheusConfig
}

type PrometheusConfig struct {
	Host string `envconfig:"PROM_HOST"`
	Port uint   `envconfig:"PROM_PORT"`
}

// Singleton config instance with default values
var globalConfig = &Config{
	Prometheus: PrometheusConfig{
		Host: "127.0.0.1",
		Port: 12798,
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
