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

package main

import (
	"encoding/json"
	"testing"

	"github.com/blinklabs-io/nview/internal/config"
)

func TestDetectNodeType(t *testing.T) {
	tests := []struct {
		name             string
		metrics          *PromMetrics
		initialBinary    string
		initialNodeName  string
		expectedBinary   string
		expectedNodeName string
	}{
		{
			name: "Dingo detection with default node name",
			metrics: &PromMetrics{
				GoMemAlloc: 1000,
				MemLive:    0,
			},
			initialBinary:    "",
			initialNodeName:  "Cardano Node",
			expectedBinary:   "dingo",
			expectedNodeName: "Dingo",
		},
		{
			name: "Dingo detection with custom node name",
			metrics: &PromMetrics{
				GoMemAlloc: 1000,
				MemLive:    0,
			},
			initialBinary:    "",
			initialNodeName:  "My Custom Node",
			expectedBinary:   "dingo",
			expectedNodeName: "My Custom Node",
		},
		{
			name: "Cardano-node detection",
			metrics: &PromMetrics{
				GoMemAlloc: 0,
				MemLive:    500,
			},
			initialBinary:    "",
			initialNodeName:  "Cardano Node",
			expectedBinary:   CARDANO_BINARY,
			expectedNodeName: "Cardano Node",
		},
		{
			name: "Cardano-node detection with MemLive > 0",
			metrics: &PromMetrics{
				GoMemAlloc: 1000,
				MemLive:    500,
			},
			initialBinary:    "",
			initialNodeName:  "Cardano Node",
			expectedBinary:   CARDANO_BINARY,
			expectedNodeName: "Cardano Node",
		},
		{
			name: "Edge case: GoMemAlloc = 0, MemLive = 0",
			metrics: &PromMetrics{
				GoMemAlloc: 0,
				MemLive:    0,
			},
			initialBinary:    "",
			initialNodeName:  "Cardano Node",
			expectedBinary:   CARDANO_BINARY,
			expectedNodeName: "Cardano Node",
		},
		{
			name: "User provided binary - don't override",
			metrics: &PromMetrics{
				GoMemAlloc: 1000,
				MemLive:    0,
			},
			initialBinary:    "custom-node",
			initialNodeName:  "Cardano Node",
			expectedBinary:   "custom-node",
			expectedNodeName: "Cardano Node",
		},
		{
			name: "Amaru binary with default node name",
			metrics: &PromMetrics{
				GoMemAlloc: 1000,
				MemLive:    0,
			},
			initialBinary:    "amaru",
			initialNodeName:  "Cardano Node",
			expectedBinary:   "amaru",
			expectedNodeName: "Amaru",
		},
		{
			name: "Amaru binary with custom node name",
			metrics: &PromMetrics{
				GoMemAlloc: 1000,
				MemLive:    0,
			},
			initialBinary:    "amaru",
			initialNodeName:  "My Custom Node",
			expectedBinary:   "amaru",
			expectedNodeName: "My Custom Node",
		},
		{
			name:             "Nil metrics - early return",
			metrics:          nil,
			initialBinary:    "",
			initialNodeName:  "Cardano Node",
			expectedBinary:   "",
			expectedNodeName: "Cardano Node",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Reset config and detected binary for each test
			cfg := config.GetConfig()
			cfg.Node.Binary = tt.initialBinary
			cfg.App.NodeName = tt.initialNodeName
			detectedNodeBinary.Store("") // Reset detected binary

			// Call the function
			detectNodeType(tt.metrics)

			// Check results
			if getEffectiveNodeBinary() != tt.expectedBinary {
				t.Errorf(
					"expected Binary %q, got %q",
					tt.expectedBinary,
					getEffectiveNodeBinary(),
				)
			}
			if getEffectiveNodeName() != tt.expectedNodeName {
				t.Errorf(
					"expected NodeName %q, got %q",
					tt.expectedNodeName,
					getEffectiveNodeName(),
				)
			}
		})
	}
}

func TestPromMetricsConnectionManagerGauges(t *testing.T) {
	prom := []byte(`
cardano_node_metrics_connectionManager_unidirectionalConns 4
cardano_node_metrics_connectionManager_duplexConns 6
cardano_node_metrics_connectionManager_fullDuplexConns 8
cardano_node_metrics_connectionManager_prunableConns 2
`)

	metrics := decodePromMetrics(t, prom)

	if metrics.ConnUniDir != 4 {
		t.Errorf("ConnUniDir = %d, expected 4", metrics.ConnUniDir)
	}
	if metrics.ConnBiDir != 6 {
		t.Errorf("ConnBiDir = %d, expected 6", metrics.ConnBiDir)
	}
	if metrics.ConnFullDuplex != 8 {
		t.Errorf("ConnFullDuplex = %d, expected 8", metrics.ConnFullDuplex)
	}
	if metrics.ConnPrunable != 2 {
		t.Errorf("ConnPrunable = %d, expected 2", metrics.ConnPrunable)
	}
}

func TestPromMetricsMissingFullDuplexConns(t *testing.T) {
	prom := []byte(`
cardano_node_metrics_connectionManager_unidirectionalConns 4
cardano_node_metrics_connectionManager_duplexConns 6
cardano_node_metrics_connectionManager_prunableConns 2
`)

	metrics := decodePromMetrics(t, prom)

	if metrics.ConnFullDuplex != 0 {
		t.Errorf("ConnFullDuplex = %d, expected 0", metrics.ConnFullDuplex)
	}
	if metrics.ConnPrunable != 2 {
		t.Errorf("ConnPrunable = %d, expected 2", metrics.ConnPrunable)
	}
}

func TestPromMetricsDingoGenesisFields(t *testing.T) {
	prom := []byte(`
dingo_shelley_start_time 1700000000
dingo_epoch_length_slots 1000
`)

	metrics := decodePromMetrics(t, prom)

	if metrics.DingoShelleyStartTime != 1700000000 {
		t.Errorf(
			"DingoShelleyStartTime = %d, expected 1700000000",
			metrics.DingoShelleyStartTime,
		)
	}
	if metrics.DingoEpochLengthSlots != 1000 {
		t.Errorf(
			"DingoEpochLengthSlots = %d, expected 1000",
			metrics.DingoEpochLengthSlots,
		)
	}
}

// TestPromMetricsPopulatesAllDingoDiagnosticsFields verifies every new Dingo
// and event-bus Prometheus metric is decoded into the PromMetrics struct.
func TestPromMetricsPopulatesAllDingoDiagnosticsFields(t *testing.T) {
	prom := []byte(`
dingo_database_size_bytes{plugin="ledger"} 1337
dingo_chain_manager_cached_blocks 8192
dingo_tip_gap_slots 2
dingo_forge_tip_gap_slots 3
dingo_ledger_slot_clock_fallback_total 4
dingo_forge_slot_clock_errors_total 5
dingo_forge_sync_skip_total 6
dingo_cbor_cache_utxo_hot_hits_total 100
dingo_cbor_cache_utxo_hot_misses_total 10
dingo_cbor_cache_tx_hot_hits_total 200
dingo_cbor_cache_tx_hot_misses_total 20
dingo_cbor_cache_block_lru_hits_total 300
dingo_cbor_cache_block_lru_misses_total 30
dingo_cbor_cache_cold_extractions_total 40
event_total{type="block"} 20
event_total{type="tx"} 30
event_subscribers{topic="block"} 25
event_subscribers{topic="tx"} 35
event_delivery_errors_total{topic="block"} 30
event_delivery_errors_total{topic="tx"} 40
event_delivery_timeouts_total{topic="block"} 35
event_delivery_timeouts_total{topic="tx"} 45
`)

	metrics := decodePromMetrics(t, prom)

	tests := []struct {
		name string
		got  uint64
		want uint64
	}{
		{"DingoDbSizeBytes", metrics.DingoDbSizeBytes, 1337},
		{"DingoChainCachedBlocks", metrics.DingoChainCachedBlocks, 8192},
		{"DingoTipGapSlots", metrics.DingoTipGapSlots, 2},
		{"DingoForgeTipGapSlots", metrics.DingoForgeTipGapSlots, 3},
		{"DingoSlotClockFallback", metrics.DingoSlotClockFallback, 4},
		{"DingoForgeSlotClockErr", metrics.DingoForgeSlotClockErr, 5},
		{"DingoForgeSyncSkip", metrics.DingoForgeSyncSkip, 6},
		{"DingoCacheUtxoHotHits", metrics.DingoCacheUtxoHotHits, 100},
		{"DingoCacheUtxoHotMiss", metrics.DingoCacheUtxoHotMiss, 10},
		{"DingoCacheTxHotHits", metrics.DingoCacheTxHotHits, 200},
		{"DingoCacheTxHotMiss", metrics.DingoCacheTxHotMiss, 20},
		{"DingoCacheBlockLruHits", metrics.DingoCacheBlockLruHits, 300},
		{"DingoCacheBlockLruMiss", metrics.DingoCacheBlockLruMiss, 30},
		{"DingoCacheColdExtract", metrics.DingoCacheColdExtract, 40},
		{"EventTotal", metrics.EventTotal, 50},
		{"EventSubscribers", metrics.EventSubscribers, 60},
		{"EventDeliveryErrors", metrics.EventDeliveryErrors, 70},
		{"EventDeliveryTimeouts", metrics.EventDeliveryTimeouts, 80},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.got != tt.want {
				t.Errorf("%s = %d, expected %d", tt.name, tt.got, tt.want)
			}
		})
	}
}

func TestPromMetricsMithrilSyncFields(t *testing.T) {
	prom := []byte(`
dingo_mithril_sync_completed 0
dingo_mithril_sync_started_at_seconds 1700000000
dingo_mithril_sync_errors_total 1
dingo_mithril_sync_download_bytes 1073741824
dingo_mithril_sync_download_total_bytes 2147483648
dingo_mithril_sync_download_percent 45.5
dingo_mithril_sync_download_bytes_per_second 1048576
dingo_mithril_sync_snapshot_size_bytes 2147483648
dingo_mithril_sync_snapshot_epoch 500
dingo_mithril_sync_ledger_import_current{stage="utxo"} 12345
dingo_mithril_sync_ledger_import_total{stage="utxo"} 18230
dingo_mithril_sync_ledger_import_percent{stage="utxo"} 67.7
dingo_mithril_sync_immutable_blocks_copied 1234
dingo_mithril_sync_immutable_blocks_per_second 56.0
dingo_mithril_sync_immutable_copy_percent 23.1
dingo_mithril_sync_gap_blocks 1200
dingo_mithril_sync_phase_active{phase="bootstrap"} 0
dingo_mithril_sync_phase_active{phase="immutable_copy"} 1
dingo_mithril_sync_phase_active{phase="ledger_import"} 0
dingo_mithril_sync_phase_active{phase="gap_blocks"} 0
dingo_mithril_sync_phase_active{phase="backfill"} 0
dingo_mithril_sync_phase_active{phase="post_ledger_state"} 0
dingo_governance_proposal_decode_failures_total 3
`)

	metrics := decodePromMetrics(t, prom)

	uintTests := []struct {
		name string
		got  uint64
		want uint64
	}{
		{"MithrilSyncCompleted", metrics.MithrilSyncCompleted, 0},
		{"MithrilSyncErrorsTotal", metrics.MithrilSyncErrorsTotal, 1},
		{"MithrilSyncDownloadBytes", metrics.MithrilSyncDownloadBytes, 1073741824},
		{"MithrilSyncDownloadTotalBytes", metrics.MithrilSyncDownloadTotalBytes, 2147483648},
		{"MithrilSyncSnapshotSize", metrics.MithrilSyncSnapshotSize, 2147483648},
		{"MithrilSyncSnapshotEpoch", metrics.MithrilSyncSnapshotEpoch, 500},
		{"MithrilSyncLedgerImportCurrent", metrics.MithrilSyncLedgerImportCurrent, 12345},
		{"MithrilSyncLedgerImportTotal", metrics.MithrilSyncLedgerImportTotal, 18230},
		{"MithrilSyncImmutableBlocksCopied", metrics.MithrilSyncImmutableBlocksCopied, 1234},
		{"MithrilSyncGapBlocks", metrics.MithrilSyncGapBlocks, 1200},
		{"DingoGovernanceDecodeFailures", metrics.DingoGovernanceDecodeFailures, 3},
	}
	for _, tt := range uintTests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.got != tt.want {
				t.Errorf("%s = %d, expected %d", tt.name, tt.got, tt.want)
			}
		})
	}

	floatTests := []struct {
		name string
		got  float64
		want float64
	}{
		{"MithrilSyncStartedAt", metrics.MithrilSyncStartedAt, 1700000000},
		{"MithrilSyncDownloadPercent", metrics.MithrilSyncDownloadPercent, 45.5},
		{"MithrilSyncDownloadRate", metrics.MithrilSyncDownloadRate, 1048576},
		{"MithrilSyncLedgerImportPercent", metrics.MithrilSyncLedgerImportPercent, 67.7},
		{"MithrilSyncImmutableCopyPerSecond", metrics.MithrilSyncImmutableCopyPerSecond, 56.0},
		{"MithrilSyncImmutableCopyPercent", metrics.MithrilSyncImmutableCopyPercent, 23.1},
		{"MithrilPhaseBootstrap", metrics.MithrilPhaseBootstrap, 0},
		{"MithrilPhaseImmutable", metrics.MithrilPhaseImmutable, 1},
		{"MithrilPhaseLedger", metrics.MithrilPhaseLedger, 0},
		{"MithrilPhaseGapBlocks", metrics.MithrilPhaseGapBlocks, 0},
		{"MithrilPhaseBackfill", metrics.MithrilPhaseBackfill, 0},
		{"MithrilPhasePostLedger", metrics.MithrilPhasePostLedger, 0},
	}
	for _, tt := range floatTests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.got != tt.want {
				t.Errorf("%s = %f, expected %f", tt.name, tt.got, tt.want)
			}
		})
	}
}

func decodePromMetrics(t *testing.T, prom []byte) PromMetrics {
	t.Helper()

	b, err := prom2json(prom)
	if err != nil {
		t.Fatalf("prom2json() error = %v", err)
	}

	var metrics PromMetrics
	if err := json.Unmarshal(b, &metrics); err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}

	return metrics
}

func TestGetEffectiveNodeBinary(t *testing.T) {
	tests := []struct {
		name           string
		detectedBinary string
		configBinary   string
		expected       string
	}{
		{
			name:           "Detected binary available",
			detectedBinary: "dingo",
			configBinary:   "cardano-node",
			expected:       "dingo",
		},
		{
			name:           "No detected binary, use config",
			detectedBinary: "",
			configBinary:   "cardano-node",
			expected:       "cardano-node",
		},
		{
			name:           "Empty detected binary, use config",
			detectedBinary: "",
			configBinary:   "amaru",
			expected:       "amaru",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Set detected binary
			if tt.detectedBinary != "" {
				detectedNodeBinary.Store(tt.detectedBinary)
			} else {
				detectedNodeBinary.Store("")
			}

			// Set config
			cfg := config.GetConfig()
			origBinary := cfg.Node.Binary
			cfg.Node.Binary = tt.configBinary
			defer func() { cfg.Node.Binary = origBinary }()

			result := getEffectiveNodeBinary()
			if result != tt.expected {
				t.Errorf(
					"getEffectiveNodeBinary() = %q, expected %q",
					result,
					tt.expected,
				)
			}
		})
	}
}

func TestGetEffectiveNodeName(t *testing.T) {
	tests := []struct {
		name           string
		detectedBinary string
		configBinary   string
		configName     string
		expected       string
	}{
		{
			name:           "Dingo with default name",
			detectedBinary: "dingo",
			configBinary:   "",
			configName:     "Cardano Node",
			expected:       "Dingo",
		},
		{
			name:           "Dingo with custom name",
			detectedBinary: "dingo",
			configBinary:   "",
			configName:     "My Node",
			expected:       "My Node",
		},
		{
			name:           "Amaru with default name",
			detectedBinary: "amaru",
			configBinary:   "",
			configName:     "Cardano Node",
			expected:       "Amaru",
		},
		{
			name:           "Amaru with custom name",
			detectedBinary: "amaru",
			configBinary:   "",
			configName:     "My Node",
			expected:       "My Node",
		},
		{
			name:           "Cardano with default name",
			detectedBinary: "cardano-node",
			configBinary:   "",
			configName:     "Cardano Node",
			expected:       "Cardano Node",
		},
		{
			name:           "Cardano with custom name",
			detectedBinary: "cardano-node",
			configBinary:   "",
			configName:     "My Node",
			expected:       "My Node",
		},
		{
			name:           "Config-driven Amaru with default name",
			detectedBinary: "",
			configBinary:   "amaru",
			configName:     "Cardano Node",
			expected:       "Amaru",
		},
		{
			name:           "Config-driven Amaru with custom name",
			detectedBinary: "",
			configBinary:   "amaru",
			configName:     "My Custom Node",
			expected:       "My Custom Node",
		},
		{
			name:           "Config-driven Dingo with default name",
			detectedBinary: "",
			configBinary:   "dingo",
			configName:     "Cardano Node",
			expected:       "Dingo",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Set detected binary
			detectedNodeBinary.Store(tt.detectedBinary)

			// Set config
			cfg := config.GetConfig()
			origBinary := cfg.Node.Binary
			origName := cfg.App.NodeName
			cfg.Node.Binary = tt.configBinary
			cfg.App.NodeName = tt.configName
			defer func() {
				cfg.Node.Binary = origBinary
				cfg.App.NodeName = origName
			}()

			result := getEffectiveNodeName()
			if result != tt.expected {
				t.Errorf(
					"getEffectiveNodeName() = %q, expected %q",
					result,
					tt.expected,
				)
			}
		})
	}
}
