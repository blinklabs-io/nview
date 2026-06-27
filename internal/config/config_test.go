package config

import (
	"os"
	"testing"
)

func resetConfigEnv(t *testing.T) {
	t.Helper()
	envVars := []string{
		"BYRON_GENESIS_START_SEC", "BYRON_EPOCH_LENGTH", "BYRON_SLOT_LENGTH",
		"SHELLEY_EPOCH_LENGTH", "SHELLEY_SLOT_LENGTH", "SHELLEY_TRANS_EPOCH",
		"DUMMY_NODE_NAME", "DUMMY_NETWORK", "DUMMY_REFRESH", "DUMMY_RETRIES",
		"DUMMY_CARDANO_NODE_BINARY", "DUMMY_CARDANO_NODE_PID_FILE", "DUMMY_CARDANO_NETWORK",
		"DUMMY_CARDANO_NODE_NETWORK_MAGIC", "DUMMY_CARDANO_PORT", "DUMMY_SHELLEY_TRANS_EPOCH",
		"DUMMY_CARDANO_BLOCK_PRODUCER", "DUMMY_PROM_HOST", "DUMMY_PROM_PORT",
		"DUMMY_PROM_REFRESH", "DUMMY_PROM_TIMEOUT", "DUMMY_BYRON_GENESIS_START_SEC",
		"DUMMY_BYRON_EPOCH_LENGTH", "DUMMY_BYRON_K", "DUMMY_BYRON_SLOT_LENGTH",
		"DUMMY_SHELLEY_EPOCH_LENGTH", "DUMMY_SHELLEY_SLOT_LENGTH",
	}
	for _, v := range envVars {
		os.Unsetenv(v)
	}
	// Reset globalConfig to defaults
	globalConfig.Store(getDefaultConfig())
	defaultsForCurrentNetwork.Store(nil)
}

func TestLoadConfig(t *testing.T) {
	resetConfigEnv(t)

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
		{"musashi app", "musashi", "", 164, false},
		{"mainnet node", "", "mainnet", 764824073, false},
		{"musashi node", "", "musashi", 164, false},
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
		{"musashi app", "musashi", "", 1780012800, 432},
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

func TestPopulateByronGenesisPreservesPinnedK(t *testing.T) {
	cfg := &Config{
		App: AppConfig{
			Network: "preview",
		},
		Node: NodeConfig{
			ByronGenesis: ByronGenesisConfig{
				K: 2160,
			},
		},
	}

	err := cfg.populateByronGenesis()
	if err != nil {
		t.Errorf("Unexpected error: %v", err)
	}
	if cfg.Node.ByronGenesis.K != 2160 {
		t.Errorf("Expected pinned K 2160, got %d", cfg.Node.ByronGenesis.K)
	}
	if cfg.Node.ByronGenesis.EpochLength != 21600 {
		t.Errorf(
			"Expected EpochLength 21600 from pinned K, got %d",
			cfg.Node.ByronGenesis.EpochLength,
		)
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
		{"musashi app", "musashi", "", 86400},
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
		{"musashi app", "musashi", "", 0}, // all eras fork at epoch 0
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
	resetConfigEnv(t)

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

// Verifies Dingo metrics override hardcoded defaults once when both metrics exist.
func TestApplyDingoGenesisOverride(t *testing.T) {
	resetConfigEnv(t)

	_, err := LoadConfig("")
	if err != nil {
		t.Fatalf("Failed to load config: %v", err)
	}

	if !ApplyDingoGenesisOverride(1666656000, 86400) {
		t.Fatal("Expected Dingo genesis override to apply")
	}
	cfg := GetConfig()
	if cfg.Node.ByronGenesis.StartTime != 1666656000 {
		t.Errorf(
			"Expected Byron start time 1666656000, got %d",
			cfg.Node.ByronGenesis.StartTime,
		)
	}
	if cfg.Node.ShelleyGenesis.EpochLength != 86400 {
		t.Errorf(
			"Expected Shelley epoch length 86400, got %d",
			cfg.Node.ShelleyGenesis.EpochLength,
		)
	}
	if cfg.Node.ShelleyTransEpoch != 0 {
		t.Errorf(
			"Expected Shelley transition epoch 0, got %d",
			cfg.Node.ShelleyTransEpoch,
		)
	}
	if ApplyDingoGenesisOverride(1666656000, 86400) {
		t.Fatal("Expected second Dingo genesis override to be skipped")
	}
}

// Verifies missing Dingo genesis metrics leave config unchanged.
func TestApplyDingoGenesisOverrideZeroMetrics(t *testing.T) {
	tests := []struct {
		name          string
		shelleyStart  uint64
		epochLenSlots uint64
	}{
		{"zero Shelley start", 0, 86400},
		{"zero epoch length", 1666656000, 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resetConfigEnv(t)

			cfg, err := LoadConfig("")
			if err != nil {
				t.Fatalf("Failed to load config: %v", err)
			}
			start := cfg.Node.ByronGenesis.StartTime
			epochLen := cfg.Node.ShelleyGenesis.EpochLength

			if ApplyDingoGenesisOverride(tt.shelleyStart, tt.epochLenSlots) {
				t.Fatal("Expected Dingo genesis override to be skipped")
			}
			if cfg.Node.ByronGenesis.StartTime != start {
				t.Errorf(
					"Expected Byron start time %d, got %d",
					start,
					cfg.Node.ByronGenesis.StartTime,
				)
			}
			if cfg.Node.ShelleyGenesis.EpochLength != epochLen {
				t.Errorf(
					"Expected Shelley epoch length %d, got %d",
					epochLen,
					cfg.Node.ShelleyGenesis.EpochLength,
				)
			}
		})
	}
}

// Verifies explicit user genesis config takes priority over Dingo metrics.
func TestApplyDingoGenesisOverrideSkipsPinnedGenesisValues(t *testing.T) {
	tests := []struct {
		name string
		env  string
		val  string
	}{
		{"Byron start pinned", "BYRON_GENESIS_START_SEC", "1700000001"},
		{"Byron epoch length pinned", "BYRON_EPOCH_LENGTH", "999"},
		{"Byron slot length pinned", "BYRON_SLOT_LENGTH", "999"},
		{"Shelley epoch length pinned", "SHELLEY_EPOCH_LENGTH", "999"},
		{"Shelley slot length pinned", "SHELLEY_SLOT_LENGTH", "2"},
		{"Shelley transition epoch pinned", "SHELLEY_TRANS_EPOCH", "1"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resetConfigEnv(t)
			os.Setenv(tt.env, tt.val)
			defer os.Unsetenv(tt.env)

			cfg, err := LoadConfig("")
			if err != nil {
				t.Fatalf("Failed to load config: %v", err)
			}
			start := cfg.Node.ByronGenesis.StartTime
			byronEpochLen := cfg.Node.ByronGenesis.EpochLength
			byronSlotLen := cfg.Node.ByronGenesis.SlotLength
			shelleyEpochLen := cfg.Node.ShelleyGenesis.EpochLength
			shelleySlotLen := cfg.Node.ShelleyGenesis.SlotLength
			transEpoch := cfg.Node.ShelleyTransEpoch

			if ApplyDingoGenesisOverride(1666656000, 86400) {
				t.Fatal("Expected Dingo genesis override to be skipped")
			}
			if cfg.Node.ByronGenesis.StartTime != start ||
				cfg.Node.ByronGenesis.EpochLength != byronEpochLen ||
				cfg.Node.ByronGenesis.SlotLength != byronSlotLen ||
				cfg.Node.ShelleyGenesis.EpochLength != shelleyEpochLen ||
				cfg.Node.ShelleyGenesis.SlotLength != shelleySlotLen ||
				cfg.Node.ShelleyTransEpoch != transEpoch {
				t.Fatal("Expected pinned config to remain unchanged")
			}
		})
	}
}
