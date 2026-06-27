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
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"sync/atomic"

	"github.com/blinklabs-io/nview/internal/config"
	dto "github.com/prometheus/client_model/go"
	"github.com/prometheus/common/expfmt"
	"github.com/prometheus/common/model"
)

// Track current epoch
var currentEpoch uint64 = 0

// Thread-safe node type detection
var detectedNodeBinary atomic.Value // stores string

func setCurrentEpoch() {
	if promMetrics != nil {
		currentEpoch = promMetrics.EpochNum
	}
}

var promMetrics *PromMetrics

// PromMetrics holds all the Prometheus metrics collected from a Cardano node.
// It includes metrics for blocks, epochs, slots, memory, connections, and more.
// The struct fields are tagged with JSON names corresponding to Prometheus metric names.
type PromMetrics struct {
	Network             string  `json:"network"`
	BlockNum            uint64  `json:"cardano_node_metrics_blockNum_int"`
	EpochNum            uint64  `json:"cardano_node_metrics_epoch_int"`
	SlotInEpoch         uint64  `json:"cardano_node_metrics_slotInEpoch_int"`
	SlotNum             uint64  `json:"cardano_node_metrics_slotNum_int"`
	Density             float64 `json:"cardano_node_metrics_density_real"`
	TxProcessed         uint64  `json:"cardano_node_metrics_txsProcessedNum_int"`
	MempoolTx           uint64  `json:"cardano_node_metrics_txsInMempool_int"`
	MempoolBytes        uint64  `json:"cardano_node_metrics_mempoolBytes_int"`
	BlocksForged        uint64  `json:"cardano_node_metrics_blocksForgedNum_int"`
	ForgingEnabled      uint64  `json:"cardano_node_metrics_forging_enabled"`
	NodeStartTime       uint64  `json:"cardano_node_metrics_nodeStartTime_int"`
	KesPeriod           uint64  `json:"cardano_node_metrics_currentKESPeriod_int"`
	RemainingKesPeriods uint64  `json:"cardano_node_metrics_remainingKESPeriods_int"`
	IsLeader            uint64  `json:"cardano_node_metrics_Forge_node_is_leader_int"`
	Adopted             uint64  `json:"cardano_node_metrics_Forge_adopted_int"`
	DidntAdopt          uint64  `json:"cardano_node_metrics_Forge_didnt_adopt_int"`
	AboutToLead         uint64  `json:"cardano_node_metrics_Forge_forge_about_to_lead_int"`
	MissedSlots         uint64  `json:"cardano_node_metrics_slotsMissedNum_int"`
	MemLive             uint64  `json:"cardano_node_metrics_RTS_gcLiveBytes_int"`
	MemHeap             uint64  `json:"cardano_node_metrics_RTS_gcHeapBytes_int"`
	GcMinor             uint64  `json:"cardano_node_metrics_RTS_gcMinorNum_int"`
	GcMajor             uint64  `json:"cardano_node_metrics_RTS_gcMajorNum_int"`
	Forks               uint64  `json:"cardano_node_metrics_forks_int"`
	BlockDelay          float64 `json:"cardano_node_metrics_blockfetchclient_blockdelay_s"`
	BlocksServed        uint64  `json:"cardano_node_metrics_served_block_count_int"`
	BlocksLate          uint64  `json:"cardano_node_metrics_blockfetchclient_lateblocks"`
	BlocksW1s           float64 `json:"cardano_node_metrics_blockfetchclient_blockdelay_cdfOne"`
	BlocksW3s           float64 `json:"cardano_node_metrics_blockfetchclient_blockdelay_cdfThree"`
	BlocksW5s           float64 `json:"cardano_node_metrics_blockfetchclient_blockdelay_cdfFive"`
	PeersCold           uint64  `json:"cardano_node_metrics_peerSelection_cold"`
	PeersWarm           uint64  `json:"cardano_node_metrics_peerSelection_warm"`
	PeersHot            uint64  `json:"cardano_node_metrics_peerSelection_hot"`
	PeersKnown          uint64  `json:"cardano_node_metrics_peerSelection_KnownPeers"`
	PeersEstablished    uint64  `json:"cardano_node_metrics_peerSelection_EstablishedPeers"`
	PeersActive         uint64  `json:"cardano_node_metrics_peerSelection_ActivePeers"`
	PeerWarmPromotions  uint64  `json:"cardano_node_metrics_peerSelection_WarmPeersPromotions"`
	PeerWarmDemotions   uint64  `json:"cardano_node_metrics_peerSelection_WarmPeersDemotions"`
	PeerChurnKnownUp    uint64  `json:"cardano_node_metrics_peerSelection_churn_IncreasedKnownPeers"`
	PeerChurnKnownDown  uint64  `json:"cardano_node_metrics_peerSelection_churn_DecreasedKnownPeers"`
	ConnIncoming        uint64  `json:"cardano_node_metrics_connectionManager_incomingConns"`
	ConnOutgoing        uint64  `json:"cardano_node_metrics_connectionManager_outgoingConns"`
	ConnUniDir          uint64  `json:"cardano_node_metrics_connectionManager_unidirectionalConns"`
	ConnBiDir           uint64  `json:"cardano_node_metrics_connectionManager_duplexConns"`
	ConnFullDuplex      uint64  `json:"cardano_node_metrics_connectionManager_fullDuplexConns"`
	ConnPrunable        uint64  `json:"cardano_node_metrics_connectionManager_prunableConns"`
	// Go runtime metrics for Dingo
	GoMemAlloc            uint64 `json:"go_memstats_alloc_bytes"`
	GoHeapIdle            uint64 `json:"go_memstats_heap_idle_bytes"`
	GoHeapInuse           uint64 `json:"go_memstats_heap_inuse_bytes"`
	GoHeapSys             uint64 `json:"go_memstats_heap_sys_bytes"`
	GoGcCount             uint64 `json:"go_gc_duration_seconds_count"`
	GoRoutines            uint64 `json:"go_goroutines"`
	GoThreads             uint64 `json:"go_threads"`
	ProcessOpenFDs        uint64 `json:"process_open_fds"`
	ProcessMaxFDs         uint64 `json:"process_max_fds"`
	DingoShelleyStartTime uint64 `json:"dingo_shelley_start_time"`
	DingoEpochLengthSlots uint64 `json:"dingo_epoch_length_slots"`
	DingoBuildVersion     string `json:"dingo_build_info_version"`
	DingoBuildCommit      string `json:"dingo_build_info_commit"`
	DingoBuildGoVersion   string `json:"dingo_build_info_goversion"`

	// Dingo-native metrics
	DingoDbSizeBytes          uint64  `json:"dingo_database_size_bytes"`
	DingoDbBlobSizeBytes      uint64  `json:"dingo_database_size_bytes_blob"`
	DingoDbMetadataSizeBytes  uint64  `json:"dingo_database_size_bytes_metadata"`
	DingoChainCachedBlocks    uint64  `json:"dingo_chain_manager_cached_blocks"`
	DingoChainsyncSeenHeaders uint64  `json:"dingo_chainsync_seen_headers"`
	DingoTipGapSlots          uint64  `json:"dingo_tip_gap_slots"`
	DingoForgeTipGapSlots     uint64  `json:"dingo_forge_tip_gap_slots"`
	DingoSlotClockFallback    uint64  `json:"dingo_ledger_slot_clock_fallback_total"`
	DingoForgeSlotClockErr    uint64  `json:"dingo_forge_slot_clock_errors_total"`
	DingoForgeSyncSkip        uint64  `json:"dingo_forge_sync_skip_total"`
	DingoTxsEvicted           uint64  `json:"dingo_metrics_txsEvictedNum_int"`
	DingoTxsExpired           uint64  `json:"dingo_metrics_txsExpiredNum_int"`
	DingoBlockForgingLatencyN uint64  `json:"dingo_metrics_blockForgingLatency_seconds_count"`
	DingoBlockForgingLatencyS float64 `json:"dingo_metrics_blockForgingLatency_seconds_sum"`
	DingoInboundArrivalsTotal uint64  `json:"dingo_metrics_peerSelection_InboundArrivalsTotal"`
	DingoInboundDuplexHeld    uint64  `json:"dingo_metrics_peerSelection_InboundDuplexHeld"`
	DingoInboundHotHeld       uint64  `json:"dingo_metrics_peerSelection_InboundHotHeld"`
	DingoInboundHotQuota      uint64  `json:"dingo_metrics_peerSelection_InboundHotQuota"`
	DingoInboundHotQuotaUsage float64 `json:"dingo_metrics_peerSelection_InboundHotQuotaUsage"`
	DingoInboundPruned        uint64  `json:"dingo_metrics_peerSelection_InboundPruned"`
	DingoInboundTopologyMatch uint64  `json:"dingo_metrics_peerSelection_InboundTopologyMatched"`
	DingoInboundWarmHeld      uint64  `json:"dingo_metrics_peerSelection_InboundWarmHeld"`
	DingoInboundWarmTarget    uint64  `json:"dingo_metrics_peerSelection_InboundWarmTarget"`
	DingoInboundWarmOccupancy float64 `json:"dingo_metrics_peerSelection_InboundWarmTargetOccupancy"`
	DingoPeersBySourceLedger  uint64  `json:"dingo_metrics_peerSelection_peers_by_source_ledger"`
	DingoPeersBySourceInbound uint64  `json:"dingo_metrics_peerSelection_peers_by_source_inbound"`
	DingoPeersBySourceGossip  uint64  `json:"dingo_metrics_peerSelection_peers_by_source_gossip"`
	DingoPeerPromotionsLedger uint64  `json:"dingo_metrics_peerSelection_churn_promotions_by_source_ledger"`
	DingoPeerDemotionsLedger  uint64  `json:"dingo_metrics_peerSelection_churn_demotions_by_source_ledger"`

	// CBOR cache counters (cumulative)
	DingoCacheUtxoHotHits  uint64 `json:"dingo_cbor_cache_utxo_hot_hits_total"`
	DingoCacheUtxoHotMiss  uint64 `json:"dingo_cbor_cache_utxo_hot_misses_total"`
	DingoCacheTxHotHits    uint64 `json:"dingo_cbor_cache_tx_hot_hits_total"`
	DingoCacheTxHotMiss    uint64 `json:"dingo_cbor_cache_tx_hot_misses_total"`
	DingoCacheBlockLruHits uint64 `json:"dingo_cbor_cache_block_lru_hits_total"`
	DingoCacheBlockLruMiss uint64 `json:"dingo_cbor_cache_block_lru_misses_total"`
	DingoCacheColdExtract  uint64 `json:"dingo_cbor_cache_cold_extractions_total"`

	// Mini-protocol activity
	DingoProtocolBlockfetchMessages uint64  `json:"dingo_protocol_messages_received_total_blockfetch"`
	DingoProtocolChainsyncMessages  uint64  `json:"dingo_protocol_messages_received_total_chainsync"`
	DingoProtocolKeepaliveMessages  uint64  `json:"dingo_protocol_messages_received_total_keepalive"`
	DingoProtocolTxSubmitMessages   uint64  `json:"dingo_protocol_messages_received_total_txsubmission"`
	DingoProtocolBlockfetchCount    uint64  `json:"dingo_protocol_message_duration_seconds_blockfetch_count"`
	DingoProtocolBlockfetchSum      float64 `json:"dingo_protocol_message_duration_seconds_blockfetch_sum"`
	DingoProtocolChainsyncCount     uint64  `json:"dingo_protocol_message_duration_seconds_chainsync_count"`
	DingoProtocolChainsyncSum       float64 `json:"dingo_protocol_message_duration_seconds_chainsync_sum"`
	DingoBlockfetchGateDispatched   uint64  `json:"dingo_blockfetch_shadow_gate_decisions_total_dispatched"`
	DingoBlockfetchGateSkippedFast  uint64  `json:"dingo_blockfetch_shadow_gate_decisions_total_skipped_fast"`
	DingoBlockfetchGateSkippedPeer  uint64  `json:"dingo_blockfetch_shadow_gate_decisions_total_skipped_no_peer"`

	// Event bus
	EventTotal            uint64 `json:"event_total"`
	EventSubscribers      uint64 `json:"event_subscribers"`
	EventDeliveryErrors   uint64 `json:"event_delivery_errors_total"`
	EventDeliveryTimeouts uint64 `json:"event_delivery_timeouts_total"`

	// Mithril bootstrap sync metrics
	MithrilSyncCompleted              uint64                              `json:"dingo_mithril_sync_completed"`
	MithrilSyncStartedAt              float64                             `json:"dingo_mithril_sync_started_at_seconds"`
	MithrilSyncErrorsTotal            uint64                              `json:"dingo_mithril_sync_errors_total"`
	MithrilSyncDownloadBytes          uint64                              `json:"dingo_mithril_sync_download_bytes"`
	MithrilSyncDownloadTotalBytes     uint64                              `json:"dingo_mithril_sync_download_total_bytes"`
	MithrilSyncDownloadPercent        float64                             `json:"dingo_mithril_sync_download_percent"`
	MithrilSyncDownloadRate           float64                             `json:"dingo_mithril_sync_download_bytes_per_second"`
	MithrilSyncSnapshotSize           uint64                              `json:"dingo_mithril_sync_snapshot_size_bytes"`
	MithrilSyncSnapshotEpoch          uint64                              `json:"dingo_mithril_sync_snapshot_epoch"`
	MithrilSyncSnapshotAncillarySize  uint64                              `json:"dingo_mithril_sync_snapshot_ancillary_size_bytes"`
	MithrilSyncSnapshotImmutableFile  uint64                              `json:"dingo_mithril_sync_snapshot_immutable_file_number"`
	MithrilSyncLedgerImportCurrent    uint64                              `json:"dingo_mithril_sync_ledger_import_current"`
	MithrilSyncLedgerImportTotal      uint64                              `json:"dingo_mithril_sync_ledger_import_total"`
	MithrilSyncLedgerImportPercent    float64                             `json:"dingo_mithril_sync_ledger_import_percent"`
	MithrilSyncLedgerImportStages     map[string]MithrilLedgerImportStage `json:"dingo_mithril_sync_ledger_import_stages"`
	MithrilSyncLedgerStateSlot        uint64                              `json:"dingo_mithril_sync_ledger_state_slot"`
	MithrilSyncImmutableBlocksCopied  uint64                              `json:"dingo_mithril_sync_immutable_blocks_copied"`
	MithrilSyncImmutableCopyPerSecond float64                             `json:"dingo_mithril_sync_immutable_blocks_per_second"`
	MithrilSyncImmutableCopyPercent   float64                             `json:"dingo_mithril_sync_immutable_copy_percent"`
	MithrilSyncImmutableCurrentSlot   uint64                              `json:"dingo_mithril_sync_immutable_current_slot"`
	MithrilSyncImmutableTipSlot       uint64                              `json:"dingo_mithril_sync_immutable_tip_slot"`
	MithrilSyncGapBlocks              uint64                              `json:"dingo_mithril_sync_gap_blocks"`
	// Phase active flags — populated from dingo_mithril_sync_phase_active{phase="..."} labels
	MithrilPhaseBootstrap  float64 `json:"dingo_mithril_sync_phase_active_bootstrap"`
	MithrilPhaseImmutable  float64 `json:"dingo_mithril_sync_phase_active_immutable_copy"`
	MithrilPhaseLedger     float64 `json:"dingo_mithril_sync_phase_active_ledger_import"`
	MithrilPhaseGapBlocks  float64 `json:"dingo_mithril_sync_phase_active_gap_blocks"`
	MithrilPhaseBackfill   float64 `json:"dingo_mithril_sync_phase_active_backfill"`
	MithrilPhasePostLedger float64 `json:"dingo_mithril_sync_phase_active_post_ledger_state"`

	// Governance metrics
	DingoGovernanceDecodeFailures uint64 `json:"dingo_governance_proposal_decode_failures_total"`
	DingoStakeSnapshotSuccess     uint64 `json:"dingo_metrics_stake_snapshot_capture_success_total"`
	DingoStakeSnapshotFailure     uint64 `json:"dingo_metrics_stake_snapshot_capture_failure_total"`
	DingoStakeSnapshotLastEpoch   uint64 `json:"dingo_metrics_stake_snapshot_last_successful_epoch_int"`
	DingoStakeSnapshotPoolCount   uint64 `json:"dingo_metrics_stake_snapshot_pool_count_int"`
	DingoStakeSnapshotActiveStake uint64 `json:"dingo_metrics_stake_snapshot_total_active_stake_lovelace"`

	// Generic future protocol metrics. Current Dingo scrapes do not expose
	// Leios, but any metric family containing "leios" is collected here.
	LeiosMetrics map[string]float64 `json:"leios_metrics"`
}

type MithrilLedgerImportStage struct {
	Current uint64  `json:"current"`
	Total   uint64  `json:"total"`
	Percent float64 `json:"percent"`
}

// Gets metrics from prometheus and return a PromMetrics instance
func getPromMetrics(ctx context.Context) (*PromMetrics, error) {
	var metrics *PromMetrics
	var respBodyBytes []byte
	respBodyBytes, statusCode, err := getNodeMetrics(ctx)
	if err != nil {
		failCount.Add(1)
		return metrics, fmt.Errorf("failed getNodeMetrics: %w", err)
	}
	if statusCode != http.StatusOK {
		failCount.Add(1)
		return metrics, fmt.Errorf("failed HTTP: %d", statusCode)
	}

	b, err := prom2json(respBodyBytes)
	if err != nil {
		failCount.Add(1)
		return metrics, fmt.Errorf("failed prom2json: %w", err)
	}

	if err := json.Unmarshal(b, &metrics); err != nil {
		failCount.Add(1)
		return metrics, fmt.Errorf("failed JSON unmarshal: %w", err)
	}

	// Detect node type based on metrics
	detectNodeType(metrics)

	// panic(string(b))
	failCount.Store(0)
	return metrics, nil
}

// detectNodeType determines the node binary from metrics (honoring
// cfg.Node.Binary) and stores the result in detectedNodeBinary
func detectNodeType(metrics *PromMetrics) {
	if metrics == nil {
		return
	}

	cfg := config.GetConfig()
	// Don't override if user has already set a binary
	if cfg.Node.Binary != "" {
		detectedNodeBinary.Store(cfg.Node.Binary)
		return
	}

	if metrics.GoMemAlloc > 0 && metrics.MemLive == 0 {
		detectedNodeBinary.Store(DINGO_BINARY)
	} else {
		detectedNodeBinary.Store(CARDANO_BINARY)
	}
}

// getEffectiveNodeBinary returns the detected node binary if available, otherwise the configured one
func getEffectiveNodeBinary() string {
	if val := detectedNodeBinary.Load(); val != nil {
		if s, ok := val.(string); ok && s != "" {
			return s
		}
	}
	cfg := config.GetConfig()
	return cfg.Node.Binary
}

// getEffectiveNodeName returns the appropriate node name based on the detected binary
func getEffectiveNodeName() string {
	binary := getEffectiveNodeBinary()
	cfg := config.GetConfig()
	if binary == DINGO_BINARY && cfg.App.NodeName == DefaultNodeName {
		return DingoNodeName
	}
	if binary == AMARU_BINARY && cfg.App.NodeName == DefaultNodeName {
		return AmaruNodeName
	}
	return cfg.App.NodeName
} // Converts a prometheus http response byte array into a JSON byte array
func prom2json(prom []byte) ([]byte, error) {
	// {"name": 0}
	out := make(map[string]any)
	b := []byte{}
	parser := expfmt.NewTextParser(model.UTF8Validation)
	families, err := parser.TextToMetricFamilies(
		strings.NewReader(string(prom)),
	)
	if err != nil {
		return b, err
	}
	for _, val := range families {
		for _, m := range val.GetMetric() {
			name := val.GetName()
			switch val.GetType() {
			case dto.MetricType_COUNTER:
				setPromMetricValueWithLabels(out, name, m.GetCounter().GetValue(), m.GetLabel())
			case dto.MetricType_GAUGE:
				setPromMetricValueWithLabels(out, name, m.GetGauge().GetValue(), m.GetLabel())
			case dto.MetricType_UNTYPED:
				setPromMetricValueWithLabels(out, name, m.GetUntyped().GetValue(), m.GetLabel())
			case dto.MetricType_SUMMARY:
				// Extract count from SUMMARY metrics (e.g. go_gc_duration_seconds_count)
				setPromMetricValue(out, name+"_count", float64(m.GetSummary().GetSampleCount()))
			case dto.MetricType_HISTOGRAM,
				dto.MetricType_GAUGE_HISTOGRAM:
				setPromHistogramValueWithLabels(
					out,
					name,
					histogramSampleCount(m.GetHistogram()),
					m.GetHistogram().GetSampleSum(),
					m.GetLabel(),
				)
			default:
				// Skip unknown types
			}
		}
	}
	b, err = json.MarshalIndent(out, "", "    ")
	if err != nil {
		return b, err
	}
	return b, nil
}

func setPromMetricValue(out map[string]any, name string, value float64) {
	if shouldAggregatePromMetric(name) {
		if prev, ok := out[name].(float64); ok {
			value += prev
		}
	}
	out[name] = value
}

func setPromMetricValueWithLabels(out map[string]any, name string, value float64, labels []*dto.LabelPair) {
	if network := strings.TrimSpace(labelValue(labels, "network")); network != "" {
		if existing, ok := out["network"].(string); !ok || existing == "" {
			out["network"] = network
		}
	}
	if isLeiosPromMetric(name) {
		setNamedMetricValue(out, "leios_metrics", name, value, labels)
	}
	if name == "dingo_build_info" {
		for _, label := range []string{"version", "commit", "goversion"} {
			if value := strings.TrimSpace(labelValue(labels, label)); value != "" {
				out[name+"_"+label] = value
			}
		}
		setPromMetricValue(out, name, value)
		return
	}
	if name == "dingo_protocol_messages_received_total" {
		bucketByLabel(out, name, value, labels, "protocol", true)
		return
	}
	if name == "dingo_blockfetch_shadow_gate_decisions_total" {
		bucketByLabel(out, name, value, labels, "path", true)
		return
	}
	if name == "dingo_database_size_bytes" {
		bucketByLabel(out, name, value, labels, "store", true)
		return
	}
	if name == "dingo_metrics_peerSelection_peers_by_source" {
		bucketByLabel(out, name, value, labels, "source", false)
		return
	}
	if name == "dingo_metrics_peerSelection_churn_promotions_by_source" ||
		name == "dingo_metrics_peerSelection_churn_demotions_by_source" {
		bucketByLabel(out, name, value, labels, "source", true)
		return
	}
	if name == "dingo_mithril_sync_phase_active" {
		if phase := labelValue(labels, "phase"); phase != "" {
			setPromMetricValue(out, name+"_"+phase, value)
			return
		}
	}
	if strings.HasPrefix(name, "dingo_mithril_sync_ledger_import_") {
		if stage := labelValue(labels, "stage"); stage != "" {
			setMithrilLedgerImportStageValue(out, name, stage, value)
			return
		}
	}
	setPromMetricValue(out, name, value)
}

func histogramSampleCount(histogram *dto.Histogram) float64 {
	if histogram == nil {
		return 0
	}
	if count := histogram.GetSampleCountFloat(); count > 0 {
		return count
	}
	return float64(histogram.GetSampleCount())
}

func bucketByLabel(
	out map[string]any,
	name string,
	value float64,
	labels []*dto.LabelPair,
	labelName string,
	includeTotal bool,
) {
	if label := normalizedLabelValue(labels, labelName); label != "" {
		setPromMetricValue(out, name+"_"+label, value)
	}
	if includeTotal {
		setPromMetricValue(out, name, value)
	}
}

func setPromHistogramValueWithLabels(
	out map[string]any,
	name string,
	count float64,
	sum float64,
	labels []*dto.LabelPair,
) {
	if isLeiosPromMetric(name) {
		setNamedMetricValue(out, "leios_metrics", name+"_count", count, labels)
		setNamedMetricValue(out, "leios_metrics", name+"_sum", sum, labels)
	}
	if name == "dingo_protocol_message_duration_seconds" {
		if protocol := normalizedLabelValue(labels, "protocol"); protocol != "" {
			setPromMetricValue(out, name+"_"+protocol+"_count", count)
			setPromMetricValue(out, name+"_"+protocol+"_sum", sum)
		}
	}
	setPromMetricValue(out, name+"_count", count)
	setPromMetricValue(out, name+"_sum", sum)
}

func isLeiosPromMetric(name string) bool {
	return strings.Contains(strings.ToLower(name), "leios")
}

func setNamedMetricValue(
	out map[string]any,
	bucket string,
	name string,
	value float64,
	labels []*dto.LabelPair,
) {
	metrics, _ := out[bucket].(map[string]float64)
	if metrics == nil {
		metrics = make(map[string]float64)
		out[bucket] = metrics
	}
	metrics[name+metricLabelSuffix(labels)] += value
}

func metricLabelSuffix(labels []*dto.LabelPair) string {
	parts := make([]string, 0, len(labels))
	for _, label := range labels {
		labelName := strings.TrimSpace(label.GetName())
		if labelName == "" || labelName == "network" {
			continue
		}
		labelValue := strings.TrimSpace(label.GetValue())
		if labelValue == "" {
			continue
		}
		parts = append(parts, labelName+"="+normalizedMetricLabelPart(labelValue))
	}
	if len(parts) == 0 {
		return ""
	}
	slices.Sort(parts)
	return "{" + strings.Join(parts, ",") + "}"
}

func normalizedMetricLabelPart(value string) string {
	replacer := strings.NewReplacer(" ", "_", "-", "_", ".", "_", "/", "_")
	return strings.ToLower(replacer.Replace(value))
}

func shouldAggregatePromMetric(name string) bool {
	return strings.HasPrefix(name, "event_") ||
		name == "dingo_database_size_bytes" ||
		name == "dingo_protocol_messages_received_total" ||
		name == "dingo_blockfetch_shadow_gate_decisions_total" ||
		name == "dingo_protocol_message_duration_seconds_count" ||
		name == "dingo_protocol_message_duration_seconds_sum" ||
		name == "dingo_metrics_peerSelection_churn_promotions_by_source" ||
		name == "dingo_metrics_peerSelection_churn_demotions_by_source" ||
		strings.HasPrefix(name, "dingo_metrics_peerSelection_peers_by_source_") ||
		strings.HasPrefix(name, "dingo_protocol_messages_received_total_") ||
		strings.HasPrefix(name, "dingo_blockfetch_shadow_gate_decisions_total_") ||
		strings.HasPrefix(name, "dingo_protocol_message_duration_seconds_")
}

func labelValue(labels []*dto.LabelPair, name string) string {
	for _, lp := range labels {
		if lp.GetName() == name {
			return lp.GetValue()
		}
	}
	return ""
}

func normalizedLabelValue(labels []*dto.LabelPair, name string) string {
	value := labelValue(labels, name)
	value = strings.ToLower(strings.TrimSpace(value))
	value = strings.NewReplacer("-", "_", ".", "_", " ", "_").Replace(value)
	return value
}

func setMithrilLedgerImportStageValue(out map[string]any, name, stage string, value float64) {
	const key = "dingo_mithril_sync_ledger_import_stages"
	stages, ok := out[key].(map[string]any)
	if !ok {
		stages = make(map[string]any)
		out[key] = stages
	}
	stageValues, ok := stages[stage].(map[string]any)
	if !ok {
		stageValues = make(map[string]any)
		stages[stage] = stageValues
	}
	switch name {
	case "dingo_mithril_sync_ledger_import_current":
		stageValues["current"] = value
	case "dingo_mithril_sync_ledger_import_total":
		stageValues["total"] = value
	case "dingo_mithril_sync_ledger_import_percent":
		stageValues["percent"] = value
	}
}
