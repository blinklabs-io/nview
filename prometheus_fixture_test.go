package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// decodeFixture decodes a recorded scrape the way nview does when the node
// answers with OpenMetrics.
func decodeFixture(t *testing.T, name string) PromMetrics {
	t.Helper()

	b, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	out, err := prom2jsonWithContentType(b, "application/openmetrics-text; version=1.0.0; charset=utf-8")
	if err != nil {
		t.Fatalf("prom2jsonWithContentType() error = %v", err)
	}
	var metrics PromMetrics
	if err := json.Unmarshal(out, &metrics); err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}
	return metrics
}

// TestPromMetricsRecordedCardanoNode11Relay decodes a scrape recorded from a
// cardano-node 11.1.3 preview relay. It pins every PromMetrics field that
// differs between 11.x and earlier nodes to the value in the scrape, so a
// rename that leaves a field at zero fails here.
func TestPromMetricsRecordedCardanoNode11Relay(t *testing.T) {
	m := decodeFixture(t, "cardano-node-11.1.3-relay.om.txt")

	counts := []struct {
		name string
		got  uint64
		want uint64
	}{
		{"BlockNum", m.BlockNum, 4799016},
		{"EpochNum", m.EpochNum, 1463},
		{"SlotNum", m.SlotNum, 126461757},
		{"SlotInEpoch", m.SlotInEpoch, 58557},
		{"TxProcessed", m.TxProcessed, 1483},
		{"Forks", m.Forks, 27},
		{"BlocksServed", m.BlocksServed, 71},
		{"BlocksLate", m.BlocksLate, 3},
		{"ConnIncoming", m.ConnIncoming, 5},
		{"ConnOutgoing", m.ConnOutgoing, 40},
		{"ConnUniDir", m.ConnUniDir, 0},
		{"ConnBiDir", m.ConnBiDir, 41},
		{"ConnFullDuplex", m.ConnFullDuplex, 4},
		{"PeersKnown", m.PeersKnown, 150},
		{"PeersEstablished", m.PeersEstablished, 30},
		{"PeersActive", m.PeersActive, 20},
		{"PeersCold", m.PeersCold, 120},
		{"PeersWarm", m.PeersWarm, 10},
		{"PeersHot", m.PeersHot, 20},
		{"ForgingEnabled", m.ForgingEnabled, 0},
	}
	for _, tt := range counts {
		if tt.got != tt.want {
			t.Errorf("%s = %d, expected %d", tt.name, tt.got, tt.want)
		}
	}

	delays := []struct {
		name string
		got  float64
		want float64
	}{
		{"BlockDelay", m.BlockDelay, 0.254554213},
		{"BlocksW1s", m.BlocksW1s, 0.9907407407407407},
		{"BlocksW3s", m.BlocksW3s, 0.9935185185185185},
		{"BlocksW5s", m.BlocksW5s, 0.9990740740740741},
	}
	for _, tt := range delays {
		if tt.got != tt.want {
			t.Errorf("%s = %v, expected %v", tt.name, tt.got, tt.want)
		}
	}
}

// TestPromMetricsRecordedCardanoNode11BlockProducer decodes a scrape recorded
// from a forging cardano-node 11.1.3 block producer and checks the forging
// fields, which are the ones nview uses to report the Core role.
func TestPromMetricsRecordedCardanoNode11BlockProducer(t *testing.T) {
	m := decodeFixture(t, "cardano-node-11.1.3-block-producer.om.txt")

	counts := []struct {
		name string
		got  uint64
		want uint64
	}{
		{"ForgingEnabled", m.ForgingEnabled, 1},
		{"BlocksForged", m.BlocksForged, 40640},
		{"MissedSlots", m.MissedSlots, 0},
		{"IsLeader", m.IsLeader, 40640},
		{"AboutToLead", m.AboutToLead, 80763},
		{"Adopted", m.Adopted, 40640},
		{"EpochNum", m.EpochNum, 269},
		{"BlockNum", m.BlockNum, 40639},
	}
	for _, tt := range counts {
		if tt.got != tt.want {
			t.Errorf("%s = %d, expected %d", tt.name, tt.got, tt.want)
		}
	}
}

// decodeTextFixture decodes a recorded scrape the way nview does when the node
// answers in the Prometheus text format, as Dingo does.
func decodeTextFixture(t *testing.T, name string) PromMetrics {
	t.Helper()

	b, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	out, err := prom2jsonWithContentType(
		b,
		"text/plain; version=0.0.4; charset=utf-8; escaping=underscores",
	)
	if err != nil {
		t.Fatalf("prom2jsonWithContentType() error = %v", err)
	}
	var metrics PromMetrics
	if err := json.Unmarshal(out, &metrics); err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}
	return metrics
}

// TestPromMetricsRecordedDingo088Preview decodes a scrape recorded from a
// Dingo 0.88.1 preview relay with the histogram families removed. It pins the
// sync backlog, anomaly, hot-peer deficit and Mithril restore series to the
// recorded values, and checks the views render them as exported zeros rather
// than as unavailable.
func TestPromMetricsRecordedDingo088Preview(t *testing.T) {
	m := decodeTextFixture(t, "dingo-0.88.1-preview.prom.txt")

	counts := []struct {
		name string
		got  uint64
		want uint64
	}{
		{"header queue length", m.DingoHeaderQueueLength, 0},
		{"header queue capacity", m.DingoHeaderQueueCapacity, 10000},
		{"inflight blocks", m.DingoBlockfetchInflightBlocks, 0},
		{"inflight bytes", m.DingoBlockfetchInflightBytes, 0},
		{"stalled, summed over reasons", m.DingoChainselectionStalled, 11},
		{"gdd disconnects", m.DingoChainselectionGDDDisconnects, 0},
		{"plateau decisions, summed over outcomes", m.DingoChainsyncPlateauDecisions, 0},
		{"keepalive timeouts", m.DingoKeepaliveTimeouts, 29},
		{"hot set deficit", m.DingoHotSetDeficit, 0},
		{"mithril restore state", m.MithrilRestoreState, 0},
		{"mithril source decisions", m.MithrilSourceDecisions, 0},
		{"mithril fallbacks", m.MithrilFallbacks, 0},
	}
	for _, c := range counts {
		if c.got != c.want {
			t.Errorf("%s = %d, expected %d", c.name, c.got, c.want)
		}
	}
	for name := range presenceTrackedPromMetrics {
		if !m.exports(name) {
			t.Errorf("exports(%q) = false for a recorded Dingo 0.88.1 scrape", name)
		}
	}

	chain := dingoConsoleChainLane(&m, 120)
	for _, expected := range []string{"Backlog", "0/10000", "Inflight"} {
		if !strings.Contains(chain, expected) {
			t.Errorf("chain view of the recorded scrape missing %q:\n%s", expected, chain)
		}
	}
	for _, cell := range dingoAnomalyCells(&m, nil) {
		if strings.Contains(cell.Value, "n/a") {
			t.Errorf("anomaly %s reads n/a for a scrape that exports it", cell.Label)
		}
	}
	if label, _ := dingoHotSetDeficit(&m); label != "0" {
		t.Errorf("hot set deficit = %q, expected 0", label)
	}
}
