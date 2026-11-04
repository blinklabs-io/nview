package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/blinklabs-io/nview/internal/config"
	"github.com/prometheus/common/expfmt"
)

// decodeAmaruFixture decodes a scrape recorded from an Amaru 10.11.0 relay
// behind an OpenTelemetry Collector, which answers in text format.
func decodeAmaruFixture(t *testing.T) *PromMetrics {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", "amaru-10.11.0-relay.prom.txt"))
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	out, err := prom2jsonWithContentType(b, string(expfmt.NewFormat(expfmt.TypeTextPlain)))
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
	}
	for _, tt := range counts {
		if tt.got != tt.want {
			t.Errorf("%s = %d, expected %d", tt.name, tt.got, tt.want)
		}
	}
	if m.AmaruProcessCPU < 0.41 || m.AmaruProcessCPU > 0.42 {
		t.Errorf("AmaruProcessCPU = %v, expected about 0.415", m.AmaruProcessCPU)
	}
	if m.AmaruBuildVersion != "10.11.0" || m.AmaruBuildRevision != "d182f86" {
		t.Errorf("build = %q @ %q, expected 10.11.0 @ d182f86", m.AmaruBuildVersion, m.AmaruBuildRevision)
	}
}

func withAmaruState(t *testing.T) {
	t.Helper()
	cfg := config.GetConfig()
	originalBinary := cfg.Node.Binary
	originalDetected, _ := detectedNodeBinary.Load().(string)
	originalProm := promMetrics.Load()
	originalProc := processMetrics
	t.Cleanup(func() {
		cfg.Node.Binary = originalBinary
		detectedNodeBinary.Store(originalDetected)
		promMetrics.Store(originalProm)
		processMetrics = originalProc
		resetPeers()
		recordSubsystemSuccess(healthSubsystemProcess)
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
		t.Fatalf("binary for an empty scrape = %q, expected %q", got, CARDANO_BINARY)
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
		t.Errorf("version = %q @ %q, expected 10.11.0 @ d182f86", version, revision)
	}

	text := getResourceText(context.Background())
	for _, expected := range []string{"CPU", "0.41%", "RSS", "459.4M", "Host Mem", "939.8M / 7.6G"} {
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
		failed := failCount() > 0
		if binary == AMARU_BINARY && failed {
			t.Errorf("%s: %d failures recorded, expected none", binary, failCount())
		}
		if binary == CARDANO_BINARY && !failed {
			t.Errorf("%s: a missing node process must still count as a failure", binary)
		}
	}

	detectedNodeBinary.Store(AMARU_BINARY)
	if err := filterPeers(context.Background()); err != nil {
		t.Errorf("filterPeers() with no local process = %v, expected nil", err)
	}
	detectedNodeBinary.Store(CARDANO_BINARY)
	if err := filterPeers(context.Background()); err == nil {
		t.Error("filterPeers() without a process must still fail for cardano-node")
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
	bare := &PromMetrics{DingoPresentMetrics: map[string]bool{"process_runtime": true}}
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
	if m.NetworkExported {
		t.Fatal("the recorded Amaru scrape has no connection or peer series")
	}
	promMetrics.Store(m)
	detectNodeType(m)
	text := getConnectionText(context.Background())
	if strings.Count(text, "n/a") < 2 || strings.Contains(text, "Incoming") {
		t.Errorf("connection text should read n/a without counts:\n%s", text)
	}

	exported := &PromMetrics{ConnIncoming: 3, NetworkExported: true}
	promMetrics.Store(exported)
	text = getConnectionText(context.Background())
	if !strings.Contains(text, "Incoming") || strings.Contains(text, "n/a") {
		t.Errorf("exported counts should be shown:\n%s", text)
	}
}

func TestPromMetricsNetworkExportedFollowsSeries(t *testing.T) {
	m := decodePromMetrics(t, []byte("cardano_node_metrics_connectionManager_inboundConns_int 2\n"))
	if !m.NetworkExported {
		t.Error("NetworkExported = false for a scrape with a connectionManager series")
	}
	m = decodePromMetrics(t, []byte("cardano_node_metrics_slotNum_int 5\n"))
	if m.NetworkExported {
		t.Error("NetworkExported = true for a scrape with no connection or peer series")
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
		t.Errorf("displayEpoch() = %d without an epoch in the scrape, expected the cached 12", got)
	}
	if got := displayEpoch(nil); got != 12 {
		t.Errorf("displayEpoch(nil) = %d, expected the cached 12", got)
	}
}
