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
	"fmt"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/blinklabs-io/nview/internal/config"
)

func TestParsePeerProbe(t *testing.T) {
	probe, ok := parsePeerProbe("198.51.100.10;3001;i+o")
	if !ok {
		t.Fatal("parsePeerProbe() returned false")
	}
	if probe.ip != "198.51.100.10" || probe.port != "3001" || probe.direction != "i+o" {
		t.Fatalf("parsePeerProbe() = %#v", probe)
	}
	if _, ok := parsePeerProbe("198.51.100.10:3001"); ok {
		t.Fatal("parsePeerProbe() accepted malformed peer")
	}
}

func TestFormatPeerEndpointIPv6(t *testing.T) {
	got := formatPeerEndpoint(&Peer{IP: "2001:db8::1", Port: 3001})
	if got != "[2001:db8::1]:3001" {
		t.Fatalf("formatPeerEndpoint() = %q", got)
	}
}

func TestAppendFilteredPeerPrefersOutboundPort(t *testing.T) {
	peers := appendFilteredPeer(nil, "198.51.100.10", 49152, "i")
	peers = appendFilteredPeer(peers, "198.51.100.10", 3001, "o")
	if len(peers) != 1 {
		t.Fatalf("peers = %#v, expected one merged peer", peers)
	}
	probe, ok := parsePeerProbe(peers[0])
	if !ok {
		t.Fatalf("merged peer %q did not parse", peers[0])
	}
	if probe.port != "3001" || probe.direction != "i+o" {
		t.Fatalf("merged peer = %#v, expected outbound port and i+o direction", probe)
	}
}

func TestPeerRTTResultsSortStableWhenRTTUnavailable(t *testing.T) {
	peers := peerRTTresultsSlice{
		{IP: "2001:db8::2", Port: 3001, Direction: "o", RTT: RTTUnreachable},
		{IP: "198.51.100.10", Port: 3001, Direction: "i", RTT: RTTUnreachable},
		{IP: "2001:db8::1", Port: 3001, Direction: "o", RTT: RTTUnreachable},
	}

	sort.Sort(peers)

	got := []string{
		formatPeerEndpoint(peers[0]),
		formatPeerEndpoint(peers[1]),
		formatPeerEndpoint(peers[2]),
	}
	want := []string{
		"198.51.100.10:3001",
		"[2001:db8::1]:3001",
		"[2001:db8::2]:3001",
	}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("sorted peers = %#v, expected %#v", got, want)
	}
}

func TestGetPeerTextRendersPartialLazyResults(t *testing.T) {
	originalPromMetrics := promMetrics
	originalPeerText := peerText
	peerStatsMu.Lock()
	originalPeerStats := peerStats
	peerStatsMu.Unlock()
	peersFilteredMu.Lock()
	originalPeersFiltered := peersFiltered
	peersFilteredMu.Unlock()
	defer func() {
		promMetrics = originalPromMetrics
		peerText = originalPeerText
		peerStatsMu.Lock()
		peerStats = originalPeerStats
		peerStatsMu.Unlock()
		peersFilteredMu.Lock()
		peersFiltered = originalPeersFiltered
		peersFilteredMu.Unlock()
	}()

	promMetrics = &PromMetrics{
		PeersKnown:       2,
		PeersEstablished: 1,
		PeersActive:      1,
		PeersHot:         1,
		PeersCold:        1,
	}
	peersFilteredMu.Lock()
	peersFiltered = []string{
		"198.51.100.10;3001;o",
		"203.0.113.20;3001;i",
	}
	peersFilteredMu.Unlock()
	peerStatsMu.Lock()
	peerStats = PeerStats{
		RTTresultsMap: make(peerRTTresultsMap),
		InFlight: map[string]time.Time{
			"203.0.113.20": time.Now(),
		},
	}
	peerStats.RTTresultsMap["198.51.100.10"] = &Peer{
		IP:        "198.51.100.10",
		Port:      3001,
		Direction: "o",
		RTT:       42,
		Location:  "Testville",
		UpdatedAt: time.Now(),
	}
	recomputePeerStatsLocked()
	peerStatsMu.Unlock()

	result := getPeerText(context.Background())
	for _, expected := range []string{
		"Selection",
		"RTT scan",
		"/2 scanned",
		"1 probing",
		"RTT Distribution",
		"198.51.100.10",
	} {
		if !strings.Contains(result, expected) {
			t.Fatalf("getPeerText() missing %q in:\n%s", expected, result)
		}
	}
}

func TestPingPeersStartsLazyBatchWithoutBlocking(t *testing.T) {
	cfg := config.GetConfig()
	originalBinary := cfg.Node.Binary
	originalDetectedBinary, _ := detectedNodeBinary.Load().(string)
	peerStatsMu.Lock()
	originalPeerStats := peerStats
	peerStatsMu.Unlock()
	peersFilteredMu.Lock()
	originalPeersFiltered := peersFiltered
	peersFilteredMu.Unlock()
	defer func() {
		cfg.Node.Binary = originalBinary
		detectedNodeBinary.Store(originalDetectedBinary)
		peerStatsMu.Lock()
		peerStats = originalPeerStats
		peerStatsMu.Unlock()
		peersFilteredMu.Lock()
		peersFiltered = originalPeersFiltered
		peersFilteredMu.Unlock()
	}()

	cfg.Node.Binary = ""
	detectedNodeBinary.Store(DINGO_BINARY)
	peers := make([]string, peerProbeBatchSize+4)
	for i := range peers {
		peers[i] = fmt.Sprintf("198.51.100.%d;3001;o", i+1)
	}
	peersFilteredMu.Lock()
	peersFiltered = peers
	peersFilteredMu.Unlock()
	peerStatsMu.Lock()
	peerStats = PeerStats{
		RTTresultsMap: make(peerRTTresultsMap),
		InFlight:      make(map[string]time.Time),
	}
	peerStatsMu.Unlock()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	pingPeers(ctx)

	peerStatsMu.Lock()
	inFlight := len(peerStats.InFlight)
	results := len(peerStats.RTTresultsSlice)
	peerStatsMu.Unlock()
	if inFlight != peerProbeBatchSize {
		t.Fatalf("in-flight probes = %d, expected %d", inFlight, peerProbeBatchSize)
	}
	if results != 0 {
		t.Fatalf("completed probe results = %d, expected 0", results)
	}
}

func TestCompletePeerProbeIgnoresSupersededProbe(t *testing.T) {
	peerStatsMu.Lock()
	originalPeerStats := peerStats
	peerStats = PeerStats{
		RTTresultsMap: make(peerRTTresultsMap),
		InFlight:      make(map[string]time.Time),
	}
	peerStatsMu.Unlock()
	defer func() {
		peerStatsMu.Lock()
		peerStats = originalPeerStats
		peerStatsMu.Unlock()
	}()

	oldScheduledAt := time.Now().Add(-time.Minute)
	newScheduledAt := time.Now()
	ip := "198.51.100.10"

	peerStatsMu.Lock()
	peerStats.InFlight[ip] = newScheduledAt
	peerStatsMu.Unlock()

	staleProbe := peerProbe{
		ip:          ip,
		port:        "3001",
		direction:   "o",
		scheduledAt: oldScheduledAt,
	}
	stalePeer := &Peer{
		IP:        ip,
		Port:      3001,
		Direction: "o",
		RTT:       42,
		Location:  "old",
		UpdatedAt: oldScheduledAt,
	}
	if completePeerProbe(staleProbe, stalePeer) {
		t.Fatal("completePeerProbe() completed a superseded probe")
	}

	peerStatsMu.Lock()
	if _, ok := peerStats.RTTresultsMap[ip]; ok {
		t.Fatal("stale probe wrote RTT result")
	}
	if got := peerStats.InFlight[ip]; !got.Equal(newScheduledAt) {
		t.Fatalf("in-flight timestamp = %s, expected %s", got, newScheduledAt)
	}
	peerStatsMu.Unlock()

	currentProbe := staleProbe
	currentProbe.scheduledAt = newScheduledAt
	currentPeer := *stalePeer
	currentPeer.Location = "new"
	currentPeer.UpdatedAt = newScheduledAt
	if !completePeerProbe(currentProbe, &currentPeer) {
		t.Fatal("completePeerProbe() rejected the current probe")
	}

	peerStatsMu.Lock()
	defer peerStatsMu.Unlock()
	if _, ok := peerStats.InFlight[ip]; ok {
		t.Fatal("current probe left in-flight entry behind")
	}
	result := peerStats.RTTresultsMap[ip]
	if result == nil || result.Location != "new" {
		t.Fatalf("RTT result = %#v, expected current peer", result)
	}
}

func TestResetPeersClearsFilteredAndStats(t *testing.T) {
	peersFilteredMu.Lock()
	originalPeersFiltered := peersFiltered
	peerStatsMu.Lock()
	originalPeerStats := peerStats
	peersFiltered = []string{"198.51.100.10;3001;o"}
	peerStats = PeerStats{
		RTTSUM:          42,
		RTTAVG:          42,
		CNT1:            1,
		PCT1:            100,
		PCT1items:       34,
		RTTresultsMap:   make(peerRTTresultsMap),
		RTTresultsSlice: peerRTTresultsSlice{{IP: "198.51.100.10", RTT: 42}},
		InFlight: map[string]time.Time{
			"198.51.100.20": time.Now(),
		},
	}
	peerStats.RTTresultsMap["198.51.100.10"] = &Peer{IP: "198.51.100.10", RTT: 42}
	peerStatsMu.Unlock()
	peersFilteredMu.Unlock()
	defer func() {
		peersFilteredMu.Lock()
		peerStatsMu.Lock()
		peersFiltered = originalPeersFiltered
		peerStats = originalPeerStats
		peerStatsMu.Unlock()
		peersFilteredMu.Unlock()
	}()

	resetPeers()

	peersFilteredMu.RLock()
	filteredCount := len(peersFiltered)
	peersFilteredMu.RUnlock()
	if filteredCount != 0 {
		t.Fatalf("peersFiltered count = %d, expected 0", filteredCount)
	}

	peerStatsMu.Lock()
	defer peerStatsMu.Unlock()
	if peerStats.RTTSUM != 0 ||
		peerStats.RTTAVG != 0 ||
		peerStats.CNT1 != 0 ||
		peerStats.PCT1 != 0 ||
		peerStats.PCT1items != 0 {
		t.Fatalf("peerStats counters were not reset: %#v", peerStats)
	}
	if len(peerStats.RTTresultsMap) != 0 ||
		len(peerStats.RTTresultsSlice) != 0 ||
		len(peerStats.InFlight) != 0 {
		t.Fatalf("peerStats collections were not reset: %#v", peerStats)
	}
}
