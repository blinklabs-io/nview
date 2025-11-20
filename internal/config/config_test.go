package config

import (
	"os"
	"testing"
)

func TestLoadConfig(t *testing.T) {
	// Clear any dummy env vars that might affect the test
	envVars := []string{
		"DUMMY_NODE_NAME", "DUMMY_NETWORK", "DUMMY_REFRESH", "DUMMY_RETRIES",
		"DUMMY_CARDANO_NODE_BINARY", "DUMMY_CARDANO_NODE_PID_FILE", "DUMMY_CARDANO_NETWORK",
		"DUMMY_CARDANO_NODE_NETWORK_MAGIC", "DUMMY_CARDANO_PORT", "DUMMY_SHELLEY_TRANS_EPOCH",
		"DUMMY_CARDANO_BLOCK_PRODUCER", "DUMMY_PROM_HOST", "DUMMY_PROM_PORT",
		"DUMMY_PROM_REFRESH", "DUMMY_PROM_TIMEOUT", "DUMMY_BYRON_GENESIS_START_SEC",
		"DUMMY_BYRON_EPOCH_LENGTH", "DUMMY_BYRON_K", "DUMMY_BYRON_SLOT_LENGTH",
		"DUMMY_SHELLEY_EPOCH_LENGTH",
	}
	for _, v := range envVars {
		os.Unsetenv(v)
	}
	// Reset globalConfig to defaults
	globalConfig = getDefaultConfig()

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
	tests := []struct {
		name          string
		appNetwork    string
		nodeNetwork   string
		expectedMagic uint32
		expectError   bool
	}{
		{"mainnet app", "mainnet", "", 764824073, false},
		{"preprod app", "preprod", "", 1, false},
		{"preview app", "preview", "", 2, false},
		{"mainnet node", "", "mainnet", 764824073, false},
		{"unknown app", "unknown", "", 0, true},
		{"no network", "", "", 764824073, false}, // defaults to mainnet
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := &Config{
				App: AppConfig{
					Network: tt.appNetwork,
				},
				Node: NodeConfig{
					Network: tt.nodeNetwork,
				},
			}
			err := cfg.populateNetworkMagic()
			if tt.expectError && err == nil {
				t.Error("Expected error but got none")
			}
			if !tt.expectError && err != nil {
				t.Errorf("Unexpected error: %v", err)
			}
			if !tt.expectError && cfg.Node.NetworkMagic != tt.expectedMagic {
				t.Errorf(
					"Expected NetworkMagic %d, got %d",
					tt.expectedMagic,
					cfg.Node.NetworkMagic,
				)
			}
		})
	}
}

func TestPopulateByronGenesis(t *testing.T) {
	tests := []struct {
		name          string
		appNetwork    string
		nodeNetwork   string
		expectedStart uint64
		expectedK     uint64
	}{
		{"mainnet app", "mainnet", "", 1506203091, 2160},
		{"preprod app", "preprod", "", 1654041600, 2160},
		{"preview app", "preview", "", 1666656000, 432},
		{"sancho app", "sancho", "", 1686789000, 432},
		{"mainnet node", "", "mainnet", 1506203091, 2160},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := &Config{
				App: AppConfig{
					Network: tt.appNetwork,
				},
				Node: NodeConfig{
					Network: tt.nodeNetwork,
				},
			}
			err := cfg.populateByronGenesis()
			if err != nil {
				t.Errorf("Unexpected error: %v", err)
			}
			if cfg.Node.ByronGenesis.StartTime != tt.expectedStart {
				t.Errorf(
					"Expected StartTime %d, got %d",
					tt.expectedStart,
					cfg.Node.ByronGenesis.StartTime,
				)
			}
			if cfg.Node.ByronGenesis.K != tt.expectedK {
				t.Errorf(
					"Expected K %d, got %d",
					tt.expectedK,
					cfg.Node.ByronGenesis.K,
				)
			}
		})
	}
}

func TestPopulateShelleyGenesis(t *testing.T) {
	tests := []struct {
		name             string
		appNetwork       string
		nodeNetwork      string
		expectedEpochLen uint64
	}{
		{"mainnet app", "mainnet", "", 432000},
		{"preprod app", "preprod", "", 432000},
		{"preview app", "preview", "", 86400},
		{"sancho app", "sancho", "", 86400},
		{"mainnet node", "", "mainnet", 432000},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := &Config{
				App: AppConfig{
					Network: tt.appNetwork,
				},
				Node: NodeConfig{
					Network: tt.nodeNetwork,
				},
			}
			err := cfg.populateShelleyGenesis()
			if err != nil {
				t.Errorf("Unexpected error: %v", err)
			}
			if cfg.Node.ShelleyGenesis.EpochLength != tt.expectedEpochLen {
				t.Errorf(
					"Expected EpochLength %d, got %d",
					tt.expectedEpochLen,
					cfg.Node.ShelleyGenesis.EpochLength,
				)
			}
		})
	}
}

func TestPopulateShelleyTransEpoch(t *testing.T) {
	tests := []struct {
		name        string
		appNetwork  string
		nodeNetwork string
		expected    int32
	}{
		{"mainnet app", "mainnet", "", 208},
		{"preprod app", "preprod", "", 4},
		{"preview app", "preview", "", 0}, // not set, defaults to 0
		{"mainnet node", "", "mainnet", 208},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := &Config{
				App: AppConfig{
					Network: tt.appNetwork,
				},
				Node: NodeConfig{
					Network:           tt.nodeNetwork,
					ShelleyTransEpoch: -1, // Set to trigger population
				},
			}
			err := cfg.populateShelleyTransEpoch()
			if err != nil {
				t.Errorf("Unexpected error: %v", err)
			}
			if cfg.Node.ShelleyTransEpoch != tt.expected {
				t.Errorf(
					"Expected ShelleyTransEpoch %d, got %d",
					tt.expected,
					cfg.Node.ShelleyTransEpoch,
				)
			}
		})
	}
}

func TestLoadConfigWithNetwork(t *testing.T) {
	// Create a temporary config file
	configContent := `
app:
  network: preprod
node:
  binary: cardano-node
`
	tmpFile, err := os.CreateTemp("", "config*.yaml")
	if err != nil {
		t.Fatalf("Failed to create temp file: %v", err)
	}
	defer os.Remove(tmpFile.Name())

	_, err = tmpFile.WriteString(configContent)
	if err != nil {
		t.Fatalf("Failed to write to temp file: %v", err)
	}
	tmpFile.Close()

	cfg, err := LoadConfig(tmpFile.Name())
	if err != nil {
		t.Fatalf("Failed to load config: %v", err)
	}
	if cfg.App.Network != "preprod" {
		t.Errorf("Expected network 'preprod', got %s", cfg.App.Network)
	}
	if cfg.Node.NetworkMagic != 1 {
		t.Errorf(
			"Expected NetworkMagic 1 for preprod, got %d",
			cfg.Node.NetworkMagic,
		)
	}
}
