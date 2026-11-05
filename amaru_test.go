package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/blinklabs-io/nview/internal/config"
	"github.com/prometheus/common/expfmt"
)

// decodeAmaruFixture decodes a scrape recorded from an Amaru 10.11.0 relay
// behind an OpenTelemetry Collector, which answers in text format.
func decodeAmaruFixture(t *testing.T) *PromMetrics {
	t.Helper()
	b, err := os.ReadFile(
		filepath.Join("testdata", "amaru-10.11.0-relay.prom.txt"),
	)
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	out, err := prom2jsonWithContentType(
		b,
		string(expfmt.NewFormat(expfmt.TypeTextPlain)),
	)
	if err != nil {
		t.Fatalf("prom2jsonWithContentType() error = %v", err)
	}
	var m PromMetrics
	if err := json.Unmarshal(out, &m); err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}
	return &m
}

func TestPromMetricsRecordedAmaruRelay(t *testing.T) {
	m := decodeAmaruFixture(t)

	counts := []struct {
		name string
		got  uint64
		want uint64
	}{
		{"EpochNum", m.EpochNum, 665},
		{"BlockNum", m.BlockNum, 14156709},
		{"SlotInEpoch", m.SlotInEpoch, 308299},
		{"AmaruProcessRuntime", m.AmaruProcessRuntime, 12808},
		{"AmaruMemoryResident", m.AmaruMemoryResident, 481697792},
		{"AmaruMemoryFootprint", m.AmaruMemoryFootprint, 481705984},
		{"AmaruHostMemoryTotal", m.AmaruHostMemoryTotal, 8123699200},
		{"AmaruHostMemoryUsed", m.AmaruHostMemoryUsed, 985423872},
		{"AmaruHeadersTotal", m.AmaruHeadersTotal, 649},
		{"AmaruHeadersValid", m.AmaruHeadersValid, 649},
		{"AmaruMempoolAccepted", m.AmaruMempoolAccepted, 3257},
		{"AmaruMempoolDuplicate", m.AmaruMempoolDuplicate, 17158},
		{"AmaruMempoolInvalid", m.AmaruMempoolInvalid, 6841},
		{"AmaruBlockFetchCount", m.AmaruBlockFetchCount, 649},
		{"AmaruBlockFetchWaitCount", m.AmaruBlockFetchWaitCount, 649},
		{"AmaruHeaderForwardCount", m.AmaruHeaderForwardCount, 649},
		{"AmaruSlotToHeaderCount", m.AmaruSlotToHeaderCount, 646},
		{"AmaruForkSwitchCount", m.AmaruForkSwitchCount, 14},
	}
	for _, tt := range counts {
		if tt.got != tt.want {
			t.Errorf("%s = %d, expected %d", tt.name, tt.got, tt.want)
		}
	}
	if m.AmaruProcessCPU < 0.41 || m.AmaruProcessCPU > 0.42 {
		t.Errorf(
			"AmaruProcessCPU = %v, expected about 0.415",
			m.AmaruProcessCPU,
		)
	}
	if m.AmaruBuildVersion != "10.11.0" || m.AmaruBuildRevision != "d182f86" {
		t.Errorf(
			"build = %q @ %q, expected 10.11.0 @ d182f86",
			m.AmaruBuildVersion,
			m.AmaruBuildRevision,
		)
	}
}

func withAmaruState(t *testing.T) {
	t.Helper()
	cfg := config.GetConfig()
	originalBinary := cfg.Node.Binary
	originalDetected, _ := detectedNodeBinary.Load().(string)
	originalProm := promMetrics.Load()
	originalProc := processMetrics
	originalFailures := subsystemFailures[healthSubsystemProcess].Load()
	peersFilteredMu.Lock()
	peerStatsMu.Lock()
	originalPeers := slices.Clone(peersFiltered)
	originalPeerStats := peerStats
	peerStatsMu.Unlock()
	peersFilteredMu.Unlock()
	t.Cleanup(func() {
		cfg.Node.Binary = originalBinary
		detectedNodeBinary.Store(originalDetected)
		promMetrics.Store(originalProm)
		processMetrics = originalProc
		subsystemFailures[healthSubsystemProcess].Store(originalFailures)
		peersFilteredMu.Lock()
		peerStatsMu.Lock()
		peersFiltered = originalPeers
		peerStats = originalPeerStats
		peerStatsMu.Unlock()
		peersFilteredMu.Unlock()
	})
	cfg.Node.Binary = ""
	processMetrics = nil
	recordSubsystemSuccess(healthSubsystemProcess)
}

func TestDetectNodeTypeRecognisesAmaru(t *testing.T) {
	withAmaruState(t)

	detectNodeType(decodeAmaruFixture(t))
	if got := getEffectiveNodeBinary(); got != AMARU_BINARY {
		t.Fatalf("binary = %q, expected %q", got, AMARU_BINARY)
	}

	detectNodeType(&PromMetrics{})
	if got := getEffectiveNodeBinary(); got != CARDANO_BINARY {
		t.Fatalf(
			"binary for an empty scrape = %q, expected %q",
			got,
			CARDANO_BINARY,
		)
	}

	config.GetConfig().Node.Binary = DINGO_BINARY
	detectNodeType(decodeAmaruFixture(t))
	if got := getEffectiveNodeBinary(); got != DINGO_BINARY {
		t.Fatalf("configured binary overridden: got %q", got)
	}
}

func TestAmaruShowsVersionUptimeAndResourcesFromScrape(t *testing.T) {
	withAmaruState(t)
	m := decodeAmaruFixture(t)
	promMetrics.Store(m)
	detectNodeType(m)

	version, revision := currentNodeVersionInfo()
	if version != "10.11.0" || revision != "d182f86" {
		t.Errorf(
			"version = %q @ %q, expected 10.11.0 @ d182f86",
			version,
			revision,
		)
	}

	text := getResourceText(context.Background())
	for _, expected := range []string{"CPU", "0.41%", "RSS", "459.4M", "Host Mem", "939.8M", "7.6G"} {
		if !strings.Contains(text, expected) {
			t.Errorf("resource text missing %q:\n%s", expected, text)
		}
	}
}

// TestAmaruWithoutLocalProcessIsNotAFailure checks the crash where a Collector
// scrape with no local node process counted as failed attempts until nview
// panicked with "could not connect to a running instance".
func TestAmaruWithoutLocalProcessIsNotAFailure(t *testing.T) {
	withAmaruState(t)

	for _, binary := range []string{AMARU_BINARY, CARDANO_BINARY} {
		detectedNodeBinary.Store(binary)
		recordSubsystemSuccess(healthSubsystemProcess)
		for range 5 {
			if recordProcessLookup(nil, nil) {
				t.Fatalf("%s: lookup of no process reported found", binary)
			}
		}
		failures := subsystemFailures[healthSubsystemProcess].Load()
		failed := failures > 0
		if binary == AMARU_BINARY && failed {
			t.Errorf(
				"%s: %d failures recorded, expected none",
				binary,
				failures,
			)
		}
		if binary == CARDANO_BINARY && !failed {
			t.Errorf(
				"%s: a missing node process must still count as a failure",
				binary,
			)
		}
	}

	detectedNodeBinary.Store(AMARU_BINARY)
	if err := filterPeers(context.Background()); err != nil {
		t.Errorf("filterPeers() with no local process = %v, expected nil", err)
	}
	detectedNodeBinary.Store(CARDANO_BINARY)
	if err := filterPeers(context.Background()); err == nil {
		t.Error(
			"filterPeers() without a process must still fail for cardano-node",
		)
	}
}

// TestDefaultConfigAutoDetectsNodeType checks that an unset binary leaves
// detection on, as the README documents. A default of "cardano-node" counted as
// an explicit choice and disabled it for every node.
func TestDefaultConfigAutoDetectsNodeType(t *testing.T) {
	if got := config.GetConfig().Node.Binary; got != "" {
		t.Fatalf("default Node.Binary = %q, expected it unset", got)
	}
}

func TestAmaruIntakeShowsHeaderAndMempoolOutcomes(t *testing.T) {
	withAmaruState(t)
	m := decodeAmaruFixture(t)
	promMetrics.Store(m)
	detectNodeType(m)

	text := amaruIntakeText(m)
	for _, expected := range []string{"Intake", "Headers", "649", "Rejected", "Tx OK", "3257", "Tx Dup", "17158", "Tx Invalid", "6841"} {
		if !strings.Contains(text, expected) {
			t.Errorf("focus text missing %q:\n%s", expected, text)
		}
	}

	// A scrape without the series must not read as zero.
	bare := &PromMetrics{
		DingoPresentMetrics: map[string]bool{"process_runtime": true},
	}
	promMetrics.Store(bare)
	text = amaruIntakeText(bare)
	if strings.Count(text, "n/a") < 4 {
		t.Errorf("unexported intake series should read n/a:\n%s", text)
	}
}

// TestAmaruWithoutNetworkSeriesReadsNotAvailable checks that a scrape with no
// connection or peer series shows n/a, because zeros would claim no peers.
func TestAmaruWithoutNetworkSeriesReadsNotAvailable(t *testing.T) {
	withAmaruState(t)
	originalP2P := p2p
	p2p = true
	t.Cleanup(func() { p2p = originalP2P })

	m := decodeAmaruFixture(t)
	if m.ConnectionsExported || m.PeerSelectionExported {
		t.Fatal("the recorded Amaru scrape has no connection or peer series")
	}
	promMetrics.Store(m)
	detectNodeType(m)
	text := getConnectionText(context.Background())
	if strings.Count(text, "n/a") != 8 {
		t.Errorf(
			"all eight connection and peer rows should read n/a:\n%s",
			text,
		)
	}

	exported := &PromMetrics{
		ConnIncoming:          3,
		ConnectionsExported:   true,
		PeerSelectionExported: true,
	}
	promMetrics.Store(exported)
	text = getConnectionText(context.Background())
	if !strings.Contains(text, "Incoming") || !strings.Contains(text, "3") ||
		strings.Contains(text, "n/a") {
		t.Errorf("exported counts should be shown:\n%s", text)
	}
}

func TestPromMetricsNetworkExportedFollowsSeries(t *testing.T) {
	m := decodePromMetrics(
		t,
		[]byte("cardano_node_metrics_connectionManager_inboundConns_int 2\n"),
	)
	if !m.ConnectionsExported || m.PeerSelectionExported {
		t.Errorf(
			"connection series: connections=%v peers=%v, expected true/false",
			m.ConnectionsExported,
			m.PeerSelectionExported,
		)
	}
	m = decodePromMetrics(
		t,
		[]byte("cardano_node_metrics_peerSelection_KnownPeers_int 9\n"),
	)
	if m.ConnectionsExported || !m.PeerSelectionExported {
		t.Errorf(
			"peer series: connections=%v peers=%v, expected false/true",
			m.ConnectionsExported,
			m.PeerSelectionExported,
		)
	}
	m = decodePromMetrics(t, []byte("cardano_node_metrics_slotNum_int 5\n"))
	if m.ConnectionsExported || m.PeerSelectionExported {
		t.Error("a scrape with no connection or peer series reads as exported")
	}
}

// TestDisplayEpochUsesTheScrapeBeingDrawn checks the Epoch row does not wait
// for the timer-refreshed cache, which reads 0 until its first update.
func TestDisplayEpochUsesTheScrapeBeingDrawn(t *testing.T) {
	original := currentEpoch
	t.Cleanup(func() { currentEpoch = original })

	currentEpoch = 0
	if got := displayEpoch(&PromMetrics{EpochNum: 665}); got != 665 {
		t.Errorf("displayEpoch() = %d with a stale cache, expected 665", got)
	}
	currentEpoch = 12
	if got := displayEpoch(&PromMetrics{}); got != 12 {
		t.Errorf(
			"displayEpoch() = %d without an epoch in the scrape, expected the cached 12",
			got,
		)
	}
	if got := displayEpoch(nil); got != 12 {
		t.Errorf("displayEpoch(nil) = %d, expected the cached 12", got)
	}
}

func TestAmaruLatencyShowsAverageAndRecent(t *testing.T) {
	m := decodeAmaruFixture(t)
	if m.AmaruBlockFetchSum != 128088283 ||
		m.AmaruSlotToHeaderSum != 128388335 {
		t.Fatalf("sums = %v, %v", m.AmaruBlockFetchSum, m.AmaruSlotToHeaderSum)
	}

	text := amaruLatencyText(m, nil)
	for _, expected := range []string{"Latency", "Fetch", "197ms", "Fork Sw", "251ms", "Slot>Hdr", "199ms"} {
		if !strings.Contains(text, expected) {
			t.Errorf("latency text missing %q:\n%s", expected, text)
		}
	}
	if strings.Contains(text, "n/a") {
		t.Errorf("a scrape with samples must not read n/a:\n%s", text)
	}

	// 49 more fetches adding 49 s of total time: the recent average is 1 s.
	prev := *m
	prev.AmaruBlockFetchCount -= 49
	prev.AmaruBlockFetchSum -= 49_000_000
	row := amaruLatencyRows(m, &prev)[0]
	if got, ok := row.recent(); !ok || got != 1_000_000 {
		t.Errorf("recent fetch = %v, %v, expected 1000000 us", got, ok)
	}
	if text := amaruLatencyText(m, &prev); !strings.Contains(text, "1.00s") {
		t.Errorf("latency text missing the recent 1.00s:\n%s", text)
	}

	if avg, ok := amaruLatencyRows(m, nil)[0].average(); !ok ||
		avg != 128088283.0/649.0 {
		t.Errorf(
			"average fetch = %v, %v, expected the sum over the count",
			avg,
			ok,
		)
	}

	// No new samples, or counters that went backwards after a restart.
	if _, ok := amaruLatencyRows(m, m)[0].recent(); ok {
		t.Error("recent reported with no new samples")
	}
	restarted := *m
	restarted.AmaruBlockFetchCount = 1000
	restarted.AmaruBlockFetchSum = 1e12
	if _, ok := amaruLatencyRows(m, &restarted)[0].recent(); ok {
		t.Error("recent reported after the counters went backwards")
	}
	sumBackwards := *m
	sumBackwards.AmaruBlockFetchCount--
	sumBackwards.AmaruBlockFetchSum++
	if _, ok := amaruLatencyRows(m, &sumBackwards)[0].recent(); ok {
		t.Error("recent reported after the sum went backwards")
	}
}

func TestAmaruLatencyWithoutSamplesReadsNotAvailable(t *testing.T) {
	text := amaruLatencyText(&PromMetrics{}, nil)
	if strings.Count(text, "n/a") != 5 {
		t.Errorf("each histogram without samples should read n/a:\n%s", text)
	}
}

func TestPromMetricsAmaruLatencySumsOutcomes(t *testing.T) {
	m := decodePromMetrics(
		t,
		[]byte(
			`# TYPE amaru_consensus_block_fetch_duration_microseconds histogram
amaru_consensus_block_fetch_duration_microseconds_sum{outcome="valid"} 100
amaru_consensus_block_fetch_duration_microseconds_count{outcome="valid"} 4
amaru_consensus_block_fetch_duration_microseconds_sum{outcome="invalid"} 50
amaru_consensus_block_fetch_duration_microseconds_count{outcome="invalid"} 1
`,
		),
	)
	if m.AmaruBlockFetchSum != 150 || m.AmaruBlockFetchCount != 5 {
		t.Errorf(
			"fetch = %v over %d, expected 150 over 5",
			m.AmaruBlockFetchSum,
			m.AmaruBlockFetchCount,
		)
	}
}

// TestAmaruFocusPaneShowsLatency checks the latency rows reach the pane Amaru
// draws, with the previous scrape as the baseline for the recent figure.
func TestAmaruFocusPaneShowsLatency(t *testing.T) {
	withAmaruState(t)
	withDingoSampleState(t)
	cfg := config.GetConfig()
	originalSlotLength := cfg.Node.ShelleyGenesis.SlotLength
	cfg.Node.ShelleyGenesis.SlotLength = 1000
	t.Cleanup(
		func() { cfg.Node.ShelleyGenesis.SlotLength = originalSlotLength },
	)

	first := decodeAmaruFixture(t)
	second := *first
	second.AmaruBlockFetchCount += 10
	second.AmaruBlockFetchSum += 10 * 2_000_000
	detectNodeType(first)
	recordDingoSample(first)
	promMetrics.Store(&second)
	recordDingoSample(&second)

	text := getOperatorFocusText(context.Background())
	for _, expected := range []string{"Latency", "Fetch", "225ms", "2.00s"} {
		if !strings.Contains(text, expected) {
			t.Errorf("focus pane missing %q:\n%s", expected, text)
		}
	}
}

// TestAmaruPartialNetworkSeriesAreJudgedSeparately checks that exporting only
// connection series does not turn the peer rows into zeros, and the reverse.
func TestAmaruPartialNetworkSeriesAreJudgedSeparately(t *testing.T) {
	withAmaruState(t)
	originalP2P := p2p
	p2p = true
	t.Cleanup(func() { p2p = originalP2P })
	detectedNodeBinary.Store(AMARU_BINARY)

	promMetrics.Store(&PromMetrics{ConnIncoming: 7, ConnectionsExported: true})
	text := getConnectionText(context.Background())
	if !strings.Contains(text, "7") || strings.Count(text, "n/a") != 3 {
		t.Errorf(
			"connections only: expected counts for connections and n/a for Cold, Warm and Hot:\n%s",
			text,
		)
	}
	if peers := getPeerText(context.Background()); !strings.Contains(
		peers,
		"Peers",
	) ||
		!strings.Contains(peers, "n/a") {
		t.Errorf("connections only: peer selection should read n/a:\n%s", peers)
	}

	promMetrics.Store(&PromMetrics{PeersHot: 5, PeerSelectionExported: true})
	text = getConnectionText(context.Background())
	if strings.Count(text, "n/a") != 5 {
		t.Errorf(
			"peers only: expected n/a for the five connection rows:\n%s",
			text,
		)
	}
}

func TestAmaruResourcesReadNotAvailablePerSeries(t *testing.T) {
	withAmaruState(t)
	detectedNodeBinary.Store(AMARU_BINARY)

	// Identified by process_runtime alone, with no CPU series.
	m := &PromMetrics{
		AmaruMemoryResident: 1 << 30,
		DingoPresentMetrics: map[string]bool{
			"process_runtime":              true,
			"process_memory_live_resident": true,
		},
	}
	promMetrics.Store(m)
	text := getResourceText(context.Background())
	if !strings.Contains(text, "1.0G") {
		t.Errorf("exported RSS missing:\n%s", text)
	}
	if strings.Count(text, "n/a") != 4 {
		t.Errorf(
			"CPU, footprint and both host memory values should read n/a:\n%s",
			text,
		)
	}
}

// TestProcessLookupIsNotAFailureBeforeDetection checks that a lookup made before
// the first scrape identified the node, when the binary is only the default,
// cannot count toward giving up on an Amaru node that is slow to answer.
func TestProcessLookupIsNotAFailureBeforeDetection(t *testing.T) {
	withAmaruState(t)
	detectedNodeBinary.Store("")

	if recordProcessLookup(nil, nil) {
		t.Fatal("lookup of no process reported found")
	}
	if got := subsystemFailures[healthSubsystemProcess].Load(); got != 0 {
		t.Errorf("failures before detection = %d, expected none", got)
	}
	if err := filterPeers(context.Background()); err != nil {
		t.Errorf("filterPeers() before detection = %v, expected nil", err)
	}

	detectedNodeBinary.Store(CARDANO_BINARY)
	recordProcessLookup(nil, nil)
	if got := subsystemFailures[healthSubsystemProcess].Load(); got != 1 {
		t.Errorf("failures after detection = %d, expected 1", got)
	}
}

// TestPromMetricsAmaruResourcePresenceFollowsSeries checks the parser marks each
// resource series as exported, so an absent one can read n/a.
func TestPromMetricsAmaruResourcePresenceFollowsSeries(t *testing.T) {
	m := decodeAmaruFixture(t)
	for _, name := range []string{
		"process_runtime", "process_cpu_live", "process_memory_live_resident",
		"process_memory_footprint", "host_memory_total", "host_memory_used",
	} {
		if !m.exports(name) {
			t.Errorf("exports(%q) = false for the recorded Amaru scrape", name)
		}
	}
	bare := decodePromMetrics(t, []byte("process_runtime 5\n"))
	for _, name := range []string{"process_memory_live_resident", "host_memory_total"} {
		if bare.exports(name) {
			t.Errorf("exports(%q) = true for a scrape without it", name)
		}
	}
}
