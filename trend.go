// Copyright 2026 Blink Labs Software
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
	"strconv"
	"strings"
	"time"
)

const (
	// trendMinSamples is the fewest samples worth charting: one point has no
	// direction.
	trendMinSamples = 2

	slotNumMetric     = "cardano_node_metrics_slotNum_int"
	mempoolTxMetric   = "cardano_node_metrics_txsInMempool_int"
	dingoTipGapMetric = "dingo_tip_gap_slots"
)

// trendSamples returns the retained samples of a metric family, oldest first.
// It returns nothing unless the family has exactly one series: charting one of
// several labelled series would quietly pick the wrong one.
func (m *PromMetrics) trendSamples(name string) []PromMetricSample {
	if m == nil || m.History == nil {
		return nil
	}
	series := m.SeriesByName(name)
	if len(series) != 1 {
		return nil
	}
	labels := make(map[string]string, len(series[0].Labels))
	for _, label := range series[0].Labels {
		labels[label.Name] = label.Value
	}
	return m.History.Samples(name, labels)
}

func sampleValues(samples []PromMetricSample) []float64 {
	values := make([]float64, 0, len(samples))
	for _, sample := range samples {
		values = append(values, sample.Value)
	}
	return values
}

// tipGapTrend returns how far behind the tip the node was at each retained
// scrape. Dingo reports its own gap, which is used when it has history. The
// other nodes have no gap metric, so it is rebuilt from the slot each scrape
// reported, measured against where the network tip was at that scrape's time.
func tipGapTrend(
	metrics *PromMetrics,
	binary string,
	slotAt func(time.Time) (uint64, bool),
) []float64 {
	if binary == DINGO_BINARY {
		if samples := metrics.trendSamples(dingoTipGapMetric); len(
			samples,
		) > 0 {
			return sampleValues(samples)
		}
	}
	samples := metrics.trendSamples(slotNumMetric)
	values := make([]float64, 0, len(samples))
	for _, sample := range samples {
		tip, ok := slotAt(sample.At)
		if !ok {
			continue
		}
		gap := 0.0
		if slot := uint64(sample.Value); tip > slot {
			gap = float64(tip - slot)
		}
		values = append(values, gap)
	}
	return values
}

func mempoolTrend(metrics *PromMetrics) []float64 {
	return sampleValues(metrics.trendSamples(mempoolTxMetric))
}

// getTrendText is the Command Deck row of mini-charts. It is empty until at
// least one metric has enough history to show a direction.
func getTrendText(
	metrics *PromMetrics,
	binary string,
	tipSeverity uiSeverity,
	peerRTT []float64,
) string {
	var parts []string
	if gap := tipGapTrend(metrics, binary, slotTipRefAt); len(
		gap,
	) >= trendMinSamples {
		parts = append(
			parts,
			uiKV("Tip Gap", uiBrailleChart(gap, 0, tipSeverity)),
		)
	}
	if mempool := mempoolTrend(metrics); len(mempool) >= trendMinSamples {
		parts = append(
			parts,
			uiKV("Mempool", uiBrailleChart(mempool, 0, uiSeverityNeutral)),
		)
	}
	if len(peerRTT) >= trendMinSamples {
		parts = append(
			parts,
			uiKV("Peer RTT", uiBrailleChart(peerRTT, RTTThreshold3, uiSeverityNeutral)),
		)
	}
	if len(parts) == 0 {
		return ""
	}
	return " " + uiMuted("Trend") + "   " + strings.Join(parts, "   ") + "\n"
}

// dingoTipGapValue is the Tip Gap cell value in the Dingo console, with the
// gap's trend after it once there is one.
func dingoTipGapValue(
	metrics *PromMetrics,
	tipGap uint64,
	tipSeverity uiSeverity,
) string {
	value := uiSeverityValue(
		strconv.FormatUint(tipGap, 10),
		tipSeverity,
	) + uiUnit(
		" slots",
	)
	if gap := tipGapTrend(metrics, DINGO_BINARY, slotTipRefAt); len(
		gap,
	) >= trendMinSamples {
		value += " " + uiBrailleChart(gap, 0, tipSeverity)
	}
	return value
}
