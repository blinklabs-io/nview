package config

import (
	"testing"
)

func TestLoadConfig(t *testing.T) {
	// Test loading default config
	cfg, err := LoadConfig("")
	if err != nil {
		t.Fatalf("Failed to load default config: %v", err)
	}
	if cfg == nil {
		t.Fatal("Config is nil")
	}
	if cfg.App.NodeName != "Cardano Node" {
		t.Errorf("Expected NodeName 'Cardano Node', got %s", cfg.App.NodeName)
	}
}

func TestPopulateNetworkMagic(t *testing.T) {
	cfg := &Config{
		App: AppConfig{
			Network: "mainnet",
		},
	}
	err := cfg.populateNetworkMagic()
	if err != nil {
		t.Fatalf("Failed to populate network magic: %v", err)
	}
	if cfg.Node.NetworkMagic != 764824073 {
		t.Errorf("Expected NetworkMagic 764824073, got %d", cfg.Node.NetworkMagic)
	}
}