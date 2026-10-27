package main

import (
	"encoding/json"
	"os"
	"path/filepath"
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
