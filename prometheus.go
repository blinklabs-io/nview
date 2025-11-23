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
	BlockNum            uint64  `json:"cardano_node_metrics_blockNum_int"`
	EpochNum            uint64  `json:"cardano_node_metrics_epoch_int"`
	SlotInEpoch         uint64  `json:"cardano_node_metrics_slotInEpoch_int"`
	SlotNum             uint64  `json:"cardano_node_metrics_slotNum_int"`
	Density             float64 `json:"cardano_node_metrics_density_real"`
	TxProcessed         uint64  `json:"cardano_node_metrics_txsProcessedNum_int"`
	MempoolTx           uint64  `json:"cardano_node_metrics_txsInMempool_int"`
	MempoolBytes        uint64  `json:"cardano_node_metrics_mempoolBytes_int"`
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
	ConnIncoming        uint64  `json:"cardano_node_metrics_connectionManager_incomingConns"`
	ConnOutgoing        uint64  `json:"cardano_node_metrics_connectionManager_outgoingConns"`
	ConnUniDir          uint64  `json:"cardano_node_metrics_connectionManager_unidirectionalConns"`
	ConnBiDir           uint64  `json:"cardano_node_metrics_connectionManager_duplexConns"`
	ConnDuplex          uint64  `json:"cardano_node_metrics_connectionManager_prunableConns"`
	// Go runtime metrics for Dingo
	GoMemAlloc  uint64 `json:"go_memstats_alloc_bytes"`
	GoHeapIdle  uint64 `json:"go_memstats_heap_idle_bytes"`
	GoHeapInuse uint64 `json:"go_memstats_heap_inuse_bytes"`
	GoHeapSys   uint64 `json:"go_memstats_heap_sys_bytes"`
	GoGcCount   uint64 `json:"go_gc_duration_seconds_count"`
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
			switch val.GetType() {
			case dto.MetricType_COUNTER:
				out[val.GetName()] = m.GetCounter().GetValue()
			case dto.MetricType_GAUGE:
				out[val.GetName()] = m.GetGauge().GetValue()
			case dto.MetricType_UNTYPED:
				out[val.GetName()] = m.GetUntyped().GetValue()
			case dto.MetricType_SUMMARY:
				// Extract count from SUMMARY metrics (e.g. go_gc_duration_seconds_count)
				out[val.GetName()+"_count"] = m.GetSummary().GetSampleCount()
			case dto.MetricType_HISTOGRAM,
				dto.MetricType_GAUGE_HISTOGRAM:
				// Skip unsupported metric types
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
