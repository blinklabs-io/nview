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
	dto "github.com/prometheus/client_model/go"
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

func TestPromMetricsCollectsLeiosMetrics(t *testing.T) {
	prom := []byte(`
# TYPE dingo_leios_input_blocks_total counter
dingo_leios_input_blocks_total{network="preview",stage="diffuse"} 3
# TYPE cardano_node_metrics_leios_vote_duration_seconds histogram
cardano_node_metrics_leios_vote_duration_seconds_bucket{network="preview",le="0.1"} 1
cardano_node_metrics_leios_vote_duration_seconds_bucket{network="preview",le="+Inf"} 2
cardano_node_metrics_leios_vote_duration_seconds_sum{network="preview"} 0.25
cardano_node_metrics_leios_vote_duration_seconds_count{network="preview"} 2
`)

	metrics := decodePromMetrics(t, prom)

	if got := metrics.LeiosMetrics["dingo_leios_input_blocks_total{stage=diffuse}"]; got != 3 {
		t.Fatalf("Leios counter = %v, expected 3", got)
	}
	if got := metrics.LeiosMetrics["cardano_node_metrics_leios_vote_duration_seconds_count"]; got != 2 {
		t.Fatalf("Leios histogram count = %v, expected 2", got)
	}
	if got := metrics.LeiosMetrics["cardano_node_metrics_leios_vote_duration_seconds_sum"]; got != 0.25 {
		t.Fatalf("Leios histogram sum = %v, expected 0.25", got)
	}
}

// TestPromMetricsPopulatesAllDingoDiagnosticsFields verifies every new Dingo
// and event-bus Prometheus metric is decoded into the PromMetrics struct.
func TestPromMetricsPopulatesAllDingoDiagnosticsFields(t *testing.T) {
	prom := []byte(`
cardano_node_metrics_blocksForgedNum_int{network="preview"} 12
cardano_node_metrics_forging_enabled 1
cardano_node_metrics_nodeStartTime_int 1700000001
cardano_node_metrics_peerSelection_KnownPeers 19
cardano_node_metrics_peerSelection_EstablishedPeers 7
cardano_node_metrics_peerSelection_ActivePeers 3
cardano_node_metrics_peerSelection_WarmPeersPromotions 11
cardano_node_metrics_peerSelection_WarmPeersDemotions 2
cardano_node_metrics_peerSelection_churn_IncreasedKnownPeers 125
cardano_node_metrics_peerSelection_churn_DecreasedKnownPeers 5
dingo_database_size_bytes{store="blob"} 1000
dingo_database_size_bytes{store="metadata"} 337
dingo_build_info{commit="62809b98",goversion="go1.26.1",network="preview",version="v0.57.0 (commit 62809b98)"} 1
dingo_chain_manager_cached_blocks 8192
dingo_chainsync_seen_headers 15
dingo_tip_gap_slots 2
dingo_forge_tip_gap_slots 3
dingo_ledger_slot_clock_fallback_total 4
dingo_forge_slot_clock_errors_total 5
dingo_forge_sync_skip_total 6
dingo_metrics_txsEvictedNum_int 7
dingo_metrics_txsExpiredNum_int 8
dingo_metrics_peerSelection_InboundArrivalsTotal 9
dingo_metrics_peerSelection_InboundDuplexHeld 10
dingo_metrics_peerSelection_InboundHotHeld 11
dingo_metrics_peerSelection_InboundHotQuota 12
dingo_metrics_peerSelection_InboundHotQuotaUsage 0.5
dingo_metrics_peerSelection_InboundPruned 13
dingo_metrics_peerSelection_InboundTopologyMatched 14
dingo_metrics_peerSelection_InboundWarmHeld 15
dingo_metrics_peerSelection_InboundWarmTarget 16
dingo_metrics_peerSelection_InboundWarmTargetOccupancy 0.25
dingo_metrics_peerSelection_peers_by_source{source="ledger",state="cold"} 18
dingo_metrics_peerSelection_peers_by_source{source="ledger",state="hot"} 1
dingo_metrics_peerSelection_peers_by_source{source="inbound",state="warm"} 2
dingo_metrics_peerSelection_peers_by_source{source="gossip",state="cold"} 3
dingo_metrics_peerSelection_churn_promotions_by_source{source="ledger"} 4
dingo_metrics_peerSelection_churn_demotions_by_source{source="ledger"} 5
dingo_cbor_cache_utxo_hot_hits_total 100
dingo_cbor_cache_utxo_hot_misses_total 10
dingo_cbor_cache_tx_hot_hits_total 200
dingo_cbor_cache_tx_hot_misses_total 20
dingo_cbor_cache_block_lru_hits_total 300
dingo_cbor_cache_block_lru_misses_total 30
dingo_cbor_cache_cold_extractions_total 40
dingo_protocol_messages_received_total{network="preview",outcome="success",protocol="blockfetch"} 5669
dingo_protocol_messages_received_total{network="preview",outcome="success",protocol="chainsync"} 356904
dingo_protocol_messages_received_total{network="preview",outcome="success",protocol="keepalive"} 6254
dingo_protocol_messages_received_total{network="preview",outcome="success",protocol="txsubmission"} 1
dingo_blockfetch_shadow_gate_decisions_total{cutoff="fallback",network="preview",path="dispatched"} 2
dingo_blockfetch_shadow_gate_decisions_total{cutoff="fallback",network="preview",path="skipped_fast"} 2472
dingo_blockfetch_shadow_gate_decisions_total{cutoff="no_sample",network="preview",path="dispatched"} 10
dingo_blockfetch_shadow_gate_decisions_total{cutoff="no_sample",network="preview",path="skipped_no_peer"} 30
# TYPE dingo_protocol_message_duration_seconds histogram
dingo_protocol_message_duration_seconds_bucket{network="preview",outcome="success",protocol="blockfetch",le="+Inf"} 10
dingo_protocol_message_duration_seconds_sum{network="preview",outcome="success",protocol="blockfetch"} 0.02
dingo_protocol_message_duration_seconds_count{network="preview",outcome="success",protocol="blockfetch"} 10
dingo_protocol_message_duration_seconds_bucket{network="preview",outcome="success",protocol="chainsync",le="+Inf"} 20
dingo_protocol_message_duration_seconds_sum{network="preview",outcome="success",protocol="chainsync"} 0.04
dingo_protocol_message_duration_seconds_count{network="preview",outcome="success",protocol="chainsync"} 20
dingo_metrics_stake_snapshot_capture_success_total 2
dingo_metrics_stake_snapshot_capture_failure_total 1
dingo_metrics_stake_snapshot_last_successful_epoch_int 1340
dingo_metrics_stake_snapshot_pool_count_int 658
dingo_metrics_stake_snapshot_total_active_stake_lovelace 1496610126371652
go_goroutines 452
go_threads 22
process_open_fds 965
process_max_fds 122880
# TYPE dingo_metrics_blockForgingLatency_seconds histogram
dingo_metrics_blockForgingLatency_seconds_bucket{le="1"} 2
dingo_metrics_blockForgingLatency_seconds_bucket{le="+Inf"} 2
dingo_metrics_blockForgingLatency_seconds_sum 1.25
dingo_metrics_blockForgingLatency_seconds_count 2
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

	if metrics.Network != "preview" {
		t.Fatalf("Network = %q, expected preview", metrics.Network)
	}

	tests := []struct {
		name string
		got  uint64
		want uint64
	}{
		{"BlocksForged", metrics.BlocksForged, 12},
		{"ForgingEnabled", metrics.ForgingEnabled, 1},
		{"NodeStartTime", metrics.NodeStartTime, 1700000001},
		{"PeersKnown", metrics.PeersKnown, 19},
		{"PeersEstablished", metrics.PeersEstablished, 7},
		{"PeersActive", metrics.PeersActive, 3},
		{"PeerWarmPromotions", metrics.PeerWarmPromotions, 11},
		{"PeerWarmDemotions", metrics.PeerWarmDemotions, 2},
		{"PeerChurnKnownUp", metrics.PeerChurnKnownUp, 125},
		{"PeerChurnKnownDown", metrics.PeerChurnKnownDown, 5},
		{"DingoDbSizeBytes", metrics.DingoDbSizeBytes, 1337},
		{"DingoDbBlobSizeBytes", metrics.DingoDbBlobSizeBytes, 1000},
		{"DingoDbMetadataSizeBytes", metrics.DingoDbMetadataSizeBytes, 337},
		{"DingoChainCachedBlocks", metrics.DingoChainCachedBlocks, 8192},
		{"DingoChainsyncSeenHeaders", metrics.DingoChainsyncSeenHeaders, 15},
		{"DingoTipGapSlots", metrics.DingoTipGapSlots, 2},
		{"DingoForgeTipGapSlots", metrics.DingoForgeTipGapSlots, 3},
		{"DingoSlotClockFallback", metrics.DingoSlotClockFallback, 4},
		{"DingoForgeSlotClockErr", metrics.DingoForgeSlotClockErr, 5},
		{"DingoForgeSyncSkip", metrics.DingoForgeSyncSkip, 6},
		{"DingoTxsEvicted", metrics.DingoTxsEvicted, 7},
		{"DingoTxsExpired", metrics.DingoTxsExpired, 8},
		{"DingoInboundArrivalsTotal", metrics.DingoInboundArrivalsTotal, 9},
		{"DingoInboundDuplexHeld", metrics.DingoInboundDuplexHeld, 10},
		{"DingoInboundHotHeld", metrics.DingoInboundHotHeld, 11},
		{"DingoInboundHotQuota", metrics.DingoInboundHotQuota, 12},
		{"DingoInboundPruned", metrics.DingoInboundPruned, 13},
		{"DingoInboundTopologyMatch", metrics.DingoInboundTopologyMatch, 14},
		{"DingoInboundWarmHeld", metrics.DingoInboundWarmHeld, 15},
		{"DingoInboundWarmTarget", metrics.DingoInboundWarmTarget, 16},
		{"DingoPeersBySourceLedger", metrics.DingoPeersBySourceLedger, 19},
		{"DingoPeersBySourceInbound", metrics.DingoPeersBySourceInbound, 2},
		{"DingoPeersBySourceGossip", metrics.DingoPeersBySourceGossip, 3},
		{"DingoPeerPromotionsLedger", metrics.DingoPeerPromotionsLedger, 4},
		{"DingoPeerDemotionsLedger", metrics.DingoPeerDemotionsLedger, 5},
		{"DingoCacheUtxoHotHits", metrics.DingoCacheUtxoHotHits, 100},
		{"DingoCacheUtxoHotMiss", metrics.DingoCacheUtxoHotMiss, 10},
		{"DingoCacheTxHotHits", metrics.DingoCacheTxHotHits, 200},
		{"DingoCacheTxHotMiss", metrics.DingoCacheTxHotMiss, 20},
		{"DingoCacheBlockLruHits", metrics.DingoCacheBlockLruHits, 300},
		{"DingoCacheBlockLruMiss", metrics.DingoCacheBlockLruMiss, 30},
		{"DingoCacheColdExtract", metrics.DingoCacheColdExtract, 40},
		{"DingoProtocolBlockfetchMessages", metrics.DingoProtocolBlockfetchMessages, 5669},
		{"DingoProtocolChainsyncMessages", metrics.DingoProtocolChainsyncMessages, 356904},
		{"DingoProtocolKeepaliveMessages", metrics.DingoProtocolKeepaliveMessages, 6254},
		{"DingoProtocolTxSubmitMessages", metrics.DingoProtocolTxSubmitMessages, 1},
		{"DingoProtocolBlockfetchCount", metrics.DingoProtocolBlockfetchCount, 10},
		{"DingoProtocolChainsyncCount", metrics.DingoProtocolChainsyncCount, 20},
		{"DingoBlockfetchGateDispatched", metrics.DingoBlockfetchGateDispatched, 12},
		{"DingoBlockfetchGateSkippedFast", metrics.DingoBlockfetchGateSkippedFast, 2472},
		{"DingoBlockfetchGateSkippedPeer", metrics.DingoBlockfetchGateSkippedPeer, 30},
		{"DingoBlockForgingLatencyN", metrics.DingoBlockForgingLatencyN, 2},
		{"DingoStakeSnapshotSuccess", metrics.DingoStakeSnapshotSuccess, 2},
		{"DingoStakeSnapshotFailure", metrics.DingoStakeSnapshotFailure, 1},
		{"DingoStakeSnapshotLastEpoch", metrics.DingoStakeSnapshotLastEpoch, 1340},
		{"DingoStakeSnapshotPoolCount", metrics.DingoStakeSnapshotPoolCount, 658},
		{"DingoStakeSnapshotActiveStake", metrics.DingoStakeSnapshotActiveStake, 1496610126371652},
		{"GoRoutines", metrics.GoRoutines, 452},
		{"GoThreads", metrics.GoThreads, 22},
		{"ProcessOpenFDs", metrics.ProcessOpenFDs, 965},
		{"ProcessMaxFDs", metrics.ProcessMaxFDs, 122880},
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

	if metrics.DingoInboundHotQuotaUsage != 0.5 {
		t.Errorf(
			"DingoInboundHotQuotaUsage = %f, expected 0.5",
			metrics.DingoInboundHotQuotaUsage,
		)
	}
	if metrics.DingoInboundWarmOccupancy != 0.25 {
		t.Errorf(
			"DingoInboundWarmOccupancy = %f, expected 0.25",
			metrics.DingoInboundWarmOccupancy,
		)
	}
	if metrics.DingoBlockForgingLatencyS != 1.25 {
		t.Errorf(
			"DingoBlockForgingLatencyS = %f, expected 1.25",
			metrics.DingoBlockForgingLatencyS,
		)
	}
	if metrics.DingoProtocolBlockfetchSum != 0.02 {
		t.Errorf("DingoProtocolBlockfetchSum = %f, expected 0.02", metrics.DingoProtocolBlockfetchSum)
	}
	if metrics.DingoProtocolChainsyncSum != 0.04 {
		t.Errorf("DingoProtocolChainsyncSum = %f, expected 0.04", metrics.DingoProtocolChainsyncSum)
	}
	if metrics.DingoBuildVersion != "v0.57.0 (commit 62809b98)" {
		t.Errorf("DingoBuildVersion = %q", metrics.DingoBuildVersion)
	}
	if metrics.DingoBuildCommit != "62809b98" {
		t.Errorf("DingoBuildCommit = %q", metrics.DingoBuildCommit)
	}
	if metrics.DingoBuildGoVersion != "go1.26.1" {
		t.Errorf("DingoBuildGoVersion = %q", metrics.DingoBuildGoVersion)
	}
}

func TestHistogramSampleCountPrefersFloatCount(t *testing.T) {
	sampleCount := uint64(7)
	sampleCountFloat := 2.5
	histogram := &dto.Histogram{
		SampleCount:      &sampleCount,
		SampleCountFloat: &sampleCountFloat,
	}
	if got := histogramSampleCount(histogram); got != sampleCountFloat {
		t.Fatalf("histogramSampleCount() = %f, expected %f", got, sampleCountFloat)
	}

	histogram.SampleCountFloat = nil
	if got := histogramSampleCount(histogram); got != float64(sampleCount) {
		t.Fatalf("histogramSampleCount() fallback = %f, expected %d", got, sampleCount)
	}
}

func TestBucketByLabelPreservesTotalOption(t *testing.T) {
	labels := []*dto.LabelPair{
		{
			Name:  stringPtr("source"),
			Value: stringPtr("ledger"),
		},
	}

	withTotal := map[string]any{}
	bucketByLabel(withTotal, "metric_total", 3, labels, "source", true)
	if got := withTotal["metric_total_ledger"]; got != float64(3) {
		t.Fatalf("suffixed metric = %#v, expected 3", got)
	}
	if got := withTotal["metric_total"]; got != float64(3) {
		t.Fatalf("total metric = %#v, expected 3", got)
	}

	withoutTotal := map[string]any{}
	bucketByLabel(withoutTotal, "metric_total", 5, labels, "source", false)
	if got := withoutTotal["metric_total_ledger"]; got != float64(5) {
		t.Fatalf("suffixed-only metric = %#v, expected 5", got)
	}
	if _, ok := withoutTotal["metric_total"]; ok {
		t.Fatal("bucketByLabel() emitted total when includeTotal=false")
	}
}

func stringPtr(value string) *string {
	return &value
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
dingo_mithril_sync_snapshot_ancillary_size_bytes 482418384
dingo_mithril_sync_snapshot_immutable_file_number 26153
dingo_mithril_sync_ledger_import_current{stage="accounts"} 89720
dingo_mithril_sync_ledger_import_current{stage="utxo"} 12345
dingo_mithril_sync_ledger_import_total{stage="accounts"} 89720
dingo_mithril_sync_ledger_import_total{stage="utxo"} 18230
dingo_mithril_sync_ledger_import_percent{stage="accounts"} 100
dingo_mithril_sync_ledger_import_percent{stage="utxo"} 67.7
dingo_mithril_sync_ledger_state_slot 112986212
dingo_mithril_sync_immutable_blocks_copied 1234
dingo_mithril_sync_immutable_blocks_per_second 56.0
dingo_mithril_sync_immutable_copy_percent 23.1
dingo_mithril_sync_immutable_current_slot 112985271
dingo_mithril_sync_immutable_tip_slot 112985271
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
		{"MithrilSyncSnapshotAncillarySize", metrics.MithrilSyncSnapshotAncillarySize, 482418384},
		{"MithrilSyncSnapshotImmutableFile", metrics.MithrilSyncSnapshotImmutableFile, 26153},
		{"MithrilSyncLedgerImportCurrent", metrics.MithrilSyncLedgerImportCurrent, 0},
		{"MithrilSyncLedgerImportTotal", metrics.MithrilSyncLedgerImportTotal, 0},
		{"MithrilSyncLedgerStateSlot", metrics.MithrilSyncLedgerStateSlot, 112986212},
		{"MithrilSyncImmutableBlocksCopied", metrics.MithrilSyncImmutableBlocksCopied, 1234},
		{"MithrilSyncImmutableCurrentSlot", metrics.MithrilSyncImmutableCurrentSlot, 112985271},
		{"MithrilSyncImmutableTipSlot", metrics.MithrilSyncImmutableTipSlot, 112985271},
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
		{"MithrilSyncLedgerImportPercent", metrics.MithrilSyncLedgerImportPercent, 0},
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

	stageTests := []struct {
		stage string
		want  MithrilLedgerImportStage
	}{
		{"accounts", MithrilLedgerImportStage{Current: 89720, Total: 89720, Percent: 100}},
		{"utxo", MithrilLedgerImportStage{Current: 12345, Total: 18230, Percent: 67.7}},
	}
	for _, tt := range stageTests {
		t.Run("stage "+tt.stage, func(t *testing.T) {
			got, ok := metrics.MithrilSyncLedgerImportStages[tt.stage]
			if !ok {
				t.Fatalf("missing ledger import stage %q", tt.stage)
			}
			if got != tt.want {
				t.Errorf("ledger import stage %q = %+v, expected %+v", tt.stage, got, tt.want)
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
