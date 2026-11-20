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
	"context"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/blinklabs-io/nview/internal/config"
)

var trendBase = time.Date(2026, 11, 20, 12, 0, 0, 0, time.UTC)

// trendTestMetrics observes one scrape per entry into a fresh history and
// returns the last scrape with that history attached, the way getPromMetrics
// does. A nil slice leaves that metric out of every scrape.
func trendTestMetrics(
	slots, mempool, dingoGap []float64,
	extraSlotSeries bool,
) *PromMetrics {
	history := NewMetricHistory(16, 12)
	scrapes := max(len(slots), len(mempool), len(dingoGap))
	var last *PromMetrics
	for i := range scrapes {
		m := &PromMetrics{}
		add := func(name string, values []float64, labels ...PromMetricLabel) {
			if i < len(values) {
				m.MetricSeries = append(m.MetricSeries, PromMetricSeries{
					Name: name, Value: values[i], Kind: PromMetricGauge, Labels: labels,
				})
			}
		}
		add(slotNumMetric, slots, PromMetricLabel{Name: "job", Value: "n1"})
		if extraSlotSeries {
			add(slotNumMetric, slots, PromMetricLabel{Name: "job", Value: "n2"})
		}
		add(mempoolTxMetric, mempool)
		add(
			dingoTipGapMetric,
			dingoGap,
			PromMetricLabel{Name: "network", Value: "preview"},
		)
		history.Observe(trendBase.Add(time.Duration(i)*3*time.Second), m)
		last = m
	}
	if last == nil {
		last = &PromMetrics{}
	}
	last.History = history
	return last
}

func TestTrendSamplesNeedsOneUnambiguousSeries(t *testing.T) {
	m := trendTestMetrics([]float64{10, 11, 12}, nil, nil, false)
	if got := sampleValues(m.trendSamples(slotNumMetric)); len(got) != 3 ||
		got[0] != 10 || got[2] != 12 {
		t.Fatalf("samples = %v, expected 10 11 12", got)
	}
	if got := m.trendSamples(mempoolTxMetric); got != nil {
		t.Fatalf("missing metric returned %v, expected nil", got)
	}

	ambiguous := trendTestMetrics([]float64{10, 11, 12}, nil, nil, true)
	if got := ambiguous.trendSamples(slotNumMetric); got != nil {
		t.Fatalf("two labelled series returned %v, expected nil", got)
	}

	var nilMetrics *PromMetrics
	if nilMetrics.trendSamples(slotNumMetric) != nil ||
		(&PromMetrics{}).trendSamples(slotNumMetric) != nil {
		t.Fatal("no metrics or history should give no samples")
	}
}

func TestTipGapTrendRebuildsTheGapFromSlots(t *testing.T) {
	// The tip moves one slot a second from slot 1000 at trendBase; each scrape
	// is three seconds apart, and the node lags by the listed gaps.
	slotAt := func(at time.Time) (uint64, bool) {
		return uint64(1000 + at.Sub(trendBase)/time.Second), true
	}
	lag := []float64{5, 3, 0, 8}
	slots := make([]float64, len(lag))
	for i, gap := range lag {
		slots[i] = float64(1000+i*3) - gap
	}
	m := trendTestMetrics(slots, nil, nil, false)

	got := tipGapTrend(m, CARDANO_BINARY, slotAt)
	if len(got) != len(lag) {
		t.Fatalf("trend = %v, expected %d points", got, len(lag))
	}
	for i := range lag {
		if got[i] != lag[i] {
			t.Errorf("point %d = %v, expected %v", i, got[i], lag[i])
		}
	}

	// A node ahead of the reference is not behind.
	ahead := trendTestMetrics([]float64{1100, 1103}, nil, nil, false)
	for i, gap := range tipGapTrend(ahead, CARDANO_BINARY, slotAt) {
		if gap != 0 {
			t.Errorf("ahead point %d = %v, expected 0", i, gap)
		}
	}

	// Samples with no known network time are skipped, not charted as zero.
	unknown := func(time.Time) (uint64, bool) { return 0, false }
	if got := tipGapTrend(m, CARDANO_BINARY, unknown); len(got) != 0 {
		t.Errorf("unknown network time gave %v, expected nothing", got)
	}
}

func TestTipGapTrendPrefersDingosOwnGap(t *testing.T) {
	slotAt := func(time.Time) (uint64, bool) { return 5000, true }
	m := trendTestMetrics([]float64{1, 2, 3}, nil, []float64{9, 7, 4}, false)

	got := tipGapTrend(m, DINGO_BINARY, slotAt)
	if len(got) != 3 || got[0] != 9 || got[2] != 4 {
		t.Fatalf("dingo trend = %v, expected its own gap 9 7 4", got)
	}
	if got := tipGapTrend(m, CARDANO_BINARY, slotAt); got[0] == 9 {
		t.Fatalf("cardano-node used the dingo gap: %v", got)
	}

	withoutGap := trendTestMetrics([]float64{4990, 4995}, nil, nil, false)
	got = tipGapTrend(withoutGap, DINGO_BINARY, slotAt)
	if len(got) != 2 || got[0] != 10 || got[1] != 5 {
		t.Fatalf("dingo without a gap series = %v, expected 10 5", got)
	}
}

func TestGetTrendText(t *testing.T) {
	setTestVisuals(t, "unicode", "none")

	if got := getTrendText(&PromMetrics{}, CARDANO_BINARY, uiSeverityOK, nil); got != "" {
		t.Fatalf("no history gave %q, expected nothing", got)
	}
	oneSample := trendTestMetrics([]float64{100}, []float64{3}, nil, false)
	if got := getTrendText(oneSample, CARDANO_BINARY, uiSeverityOK, nil); got != "" {
		t.Fatalf("one sample gave %q, expected nothing", got)
	}

	m := trendTestMetrics(
		[]float64{100, 101, 102},
		[]float64{0, 4, 2},
		nil,
		false,
	)
	plain := stripTviewTags(getTrendText(m, CARDANO_BINARY, uiSeverityOK, nil))
	for _, want := range []string{"Trend", "Tip Gap", "Mempool"} {
		if !strings.Contains(plain, want) {
			t.Errorf("trend text missing %q:\n%s", want, plain)
		}
	}
	if !strings.HasSuffix(plain, "\n") || strings.Count(plain, "\n") != 1 {
		t.Errorf("trend text is not exactly one line: %q", plain)
	}
	hasBraille := false
	for _, r := range plain {
		hasBraille = hasBraille || (r >= 0x2800 && r <= 0x28FF)
	}
	if !hasBraille {
		t.Errorf("unicode mode drew no braille cells: %q", plain)
	}

	withRTT := stripTviewTags(
		getTrendText(&PromMetrics{}, CARDANO_BINARY, uiSeverityOK,
			[]float64{20, 80, 40}),
	)
	if !strings.Contains(withRTT, "Peer RTT") ||
		strings.Contains(withRTT, "Tip Gap") {
		t.Errorf("rtt-only trend text = %q", withRTT)
	}
	// A fixed scale: a steady low RTT must not fill the chart.
	low := stripTviewTags(getTrendText(&PromMetrics{}, CARDANO_BINARY,
		uiSeverityOK, []float64{10, 10}))
	if !strings.Contains(low, "Peer RTT ⣀\n") {
		t.Errorf("low steady rtt = %q, expected a baseline chart", low)
	}
	if got := getTrendText(&PromMetrics{}, CARDANO_BINARY, uiSeverityOK,
		[]float64{20}); got != "" {
		t.Errorf("one rtt sample gave %q, expected nothing", got)
	}

	mempoolOnly := trendTestMetrics(nil, []float64{1, 2}, nil, false)
	text := stripTviewTags(
		getTrendText(mempoolOnly, CARDANO_BINARY, uiSeverityOK, nil),
	)
	if strings.Contains(text, "Tip Gap") || !strings.Contains(text, "Mempool") {
		t.Errorf("mempool-only trend text = %q", text)
	}
}

func TestGetTrendTextPlainModeUsesASCII(t *testing.T) {
	setTestVisuals(t, "plain", "none")

	m := trendTestMetrics(nil, []float64{0, 5, 10}, nil, false)
	text := stripTviewTags(getTrendText(m, CARDANO_BINARY, uiSeverityOK, nil))
	if !strings.Contains(text, "Mempool") {
		t.Fatalf("plain trend text = %q", text)
	}
	for _, r := range text {
		if r > 0x7f {
			t.Fatalf("plain mode wrote a non-ASCII rune %q in %q", r, text)
		}
	}
}

// The Command Deck has one spare row, so the trend row must be a single line
// and only the panel layout (not Dingo's own console) may show it.
func TestOverviewShowsTrendRowOnlyForThePanelLayout(t *testing.T) {
	setTestVisuals(t, "unicode", "none")
	prevBinary := detectedNodeBinary.Load()
	prevMetrics := promMetrics.Load()
	t.Cleanup(func() {
		promMetrics.Store(prevMetrics)
		if prevBinary != nil {
			detectedNodeBinary.Store(prevBinary)
		}
	})
	m := trendTestMetrics(
		[]float64{100, 101, 102},
		[]float64{0, 4, 2},
		nil,
		false,
	)
	m.SlotNum = 102
	promMetrics.Store(m)

	detectedNodeBinary.Store(CARDANO_BINARY)
	overview := stripTviewTags(getOverviewText(context.Background()))
	if !strings.Contains(overview, "Trend") {
		t.Fatalf("cardano-node overview has no trend row:\n%s", overview)
	}
	if lines := strings.Count(strings.TrimRight(overview, "\n"), "\n") + 1; lines > 4 {
		t.Fatalf(
			"overview is %d lines, the Command Deck holds 4:\n%s",
			lines,
			overview,
		)
	}

	detectedNodeBinary.Store(DINGO_BINARY)
	if got := stripTviewTags(getOverviewText(context.Background())); strings.Contains(
		got,
		"Trend",
	) {
		t.Fatalf("dingo overview shows the panel trend row:\n%s", got)
	}
}

func TestDingoTipGapValueAddsTheTrend(t *testing.T) {
	setTestVisuals(t, "unicode", "none")

	bare := stripTviewTags(dingoTipGapValue(&PromMetrics{}, 6, uiSeverityOK))
	if bare != "6 slots" {
		t.Fatalf("without history = %q, expected %q", bare, "6 slots")
	}

	m := trendTestMetrics(nil, nil, []float64{9, 7, 4}, false)
	got := stripTviewTags(dingoTipGapValue(m, 4, uiSeverityOK))
	if !strings.HasPrefix(got, "4 slots ") ||
		len([]rune(got)) <= len("4 slots ") {
		t.Fatalf("with history = %q, expected the slots then a chart", got)
	}
}

func hasBrailleCell(text string) bool {
	for _, r := range text {
		if r >= 0x2800 && r <= 0x28FF {
			return true
		}
	}
	return false
}

// The three Dingo views that show a tip gap must each draw its trend.
func TestDingoConsoleViewsShowTheTipGapTrend(t *testing.T) {
	setTestVisuals(t, "unicode", "none")
	prevBinary := detectedNodeBinary.Load()
	detectedNodeBinary.Store(DINGO_BINARY)
	t.Cleanup(func() {
		if prevBinary != nil {
			detectedNodeBinary.Store(prevBinary)
		}
	})
	m := trendTestMetrics(nil, nil, []float64{9, 7, 4, 2}, false)

	views := map[string]func(*PromMetrics, int) string{
		"chain lane":        dingoConsoleChainLane,
		"compact dashboard": dingoConsoleDashboardCompact,
		"compact chain":     dingoConsoleChainCompact,
	}
	for name, view := range views {
		if text := stripTviewTags(view(m, 100)); !hasBrailleCell(text) {
			t.Errorf("%s drew no tip gap trend:\n%s", name, text)
		}
		bare := &PromMetrics{}
		if text := stripTviewTags(view(bare, 100)); hasBrailleCell(text) {
			t.Errorf("%s drew a chart with no history:\n%s", name, text)
		}
	}
}

func TestRecordPeerRTTTrend(t *testing.T) {
	original := peerStats
	t.Cleanup(func() {
		peerStatsMu.Lock()
		peerStats = original
		peerStatsMu.Unlock()
	})
	peerStatsMu.Lock()
	peerStats = PeerStats{}

	// No answered peer: RTTAVG is zero but is not a measurement.
	peerStats.RTTresultsSlice = peerRTTresultsSlice{{RTT: RTTUnreachable}}
	peerStats.CNT0 = 1
	recordPeerRTTTrendLocked(trendBase)
	if len(peerStats.RTTTrend) != 0 {
		t.Fatalf("recorded %v with no reachable peer", peerStats.RTTTrend)
	}

	peerStats.RTTresultsSlice = peerRTTresultsSlice{{RTT: 30}, {RTT: 50}}
	peerStats.CNT0 = 0
	peerStats.RTTAVG = 40
	recordPeerRTTTrendLocked(trendBase)
	recordPeerRTTTrendLocked(trendBase.Add(peerRTTTrendEvery - time.Nanosecond))
	if len(peerStats.RTTTrend) != 1 || peerStats.RTTTrend[0] != 40 {
		t.Fatalf("throttled record = %v, expected [40]", peerStats.RTTTrend)
	}

	for i := 1; i <= peerRTTTrendSamples+3; i++ {
		peerStats.RTTAVG = 100 + i
		recordPeerRTTTrendLocked(
			trendBase.Add(time.Duration(i) * peerRTTTrendEvery),
		)
	}
	got := slices.Clone(peerStats.RTTTrend)
	peerStatsMu.Unlock()
	if len(got) != peerRTTTrendSamples ||
		got[len(got)-1] != float64(100+peerRTTTrendSamples+3) {
		t.Fatalf("capped trend = %v", got)
	}
	if copied := peerRTTTrend(); len(copied) != len(got) {
		t.Fatalf("peerRTTTrend() = %v, expected %v", copied, got)
	}

	resetPeers()
	if len(peerRTTTrend()) != 0 {
		t.Fatalf("reset kept %v", peerRTTTrend())
	}
}

func TestOverviewShowsPeerRTTTrend(t *testing.T) {
	setTestVisuals(t, "unicode", "none")
	prevBinary := detectedNodeBinary.Load()
	prevMetrics := promMetrics.Load()
	original := peerStats
	t.Cleanup(func() {
		promMetrics.Store(prevMetrics)
		if prevBinary != nil {
			detectedNodeBinary.Store(prevBinary)
		}
		peerStatsMu.Lock()
		peerStats = original
		peerStatsMu.Unlock()
	})
	promMetrics.Store(&PromMetrics{})
	detectedNodeBinary.Store(CARDANO_BINARY)
	peerStatsMu.Lock()
	peerStats = PeerStats{RTTTrend: []float64{20, 90, 40}}
	peerStatsMu.Unlock()

	if got := stripTviewTags(getOverviewText(context.Background())); !strings.Contains(
		got,
		"Peer RTT",
	) {
		t.Fatalf("overview has no peer rtt trend:\n%s", got)
	}
}

// pingPeers runs on every refresh tick, so it is where the RTT samples are
// taken: the chart must grow from there, once per spacing interval.
func TestPingPeersRecordsTheRTTTrend(t *testing.T) {
	cfg := config.GetConfig()
	originalBinary := cfg.Node.Binary
	originalDetected, _ := detectedNodeBinary.Load().(string)
	peerStatsMu.Lock()
	originalStats := peerStats
	peerStatsMu.Unlock()
	peersFilteredMu.Lock()
	originalFiltered := peersFiltered
	peersFilteredMu.Unlock()
	t.Cleanup(func() {
		cfg.Node.Binary = originalBinary
		detectedNodeBinary.Store(originalDetected)
		peerStatsMu.Lock()
		peerStats = originalStats
		peerStatsMu.Unlock()
		peersFilteredMu.Lock()
		peersFiltered = originalFiltered
		peersFilteredMu.Unlock()
	})

	cfg.Node.Binary = ""
	detectedNodeBinary.Store(DINGO_BINARY)
	peersFilteredMu.Lock()
	peersFiltered = []string{"198.51.100.10;3001;o"}
	peersFilteredMu.Unlock()
	peerStatsMu.Lock()
	peerStats = PeerStats{
		RTTresultsMap: peerRTTresultsMap{
			"198.51.100.10": {
				IP:        "198.51.100.10",
				Port:      3001,
				Direction: "o",
				RTT:       60,
				Location:  "Testville",
				UpdatedAt: time.Now(),
			},
		},
		InFlight: make(map[string]time.Time),
	}
	peerStatsMu.Unlock()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	pingPeers(ctx)
	pingPeers(ctx)

	if got := peerRTTTrend(); len(got) != 1 || got[0] != 60 {
		t.Fatalf("trend after two quick ticks = %v, expected [60]", got)
	}
}
