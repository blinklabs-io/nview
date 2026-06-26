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
	"errors"
	"fmt"
	"net"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/blinklabs-io/nview/internal/config"
	netutil "github.com/shirou/gopsutil/v3/net"
)

var peersFiltered []string

var (
	scrollPeers     bool = false
	peersFilteredMu sync.RWMutex
	peerStatsMu     sync.Mutex
)

const (
	peerProbeBatchSize = 8
	peerProbeFreshFor  = 10 * time.Minute
	peerProbeStaleFor  = 30 * time.Second
)

type peerProbe struct {
	ip          string
	port        string
	direction   string
	scheduledAt time.Time
}

func filterPeers(ctx context.Context) error {
	var peers []string
	if processMetrics == nil {
		return errors.New("process metrics not available for peer filtering")
	}
	cfg := config.GetConfig()

	// Get process in/out connections
	connections, err := netutil.ConnectionsPidWithContext(
		ctx,
		"tcp",
		processMetrics.Pid,
	)
	if err != nil {
		return err
	}

	// Loops each connection, looking for ESTABLISHED
	for _, c := range connections {
		if c.Status == "ESTABLISHED" {
			// If local port == node port, it's incoming (except P2P)
			if c.Laddr.Port == cfg.Node.Port {
				peers = appendFilteredPeer(peers, c.Raddr.IP, c.Raddr.Port, "i")
			}
			// If local port != node port, ekg port, or prometheus port, it's outgoing
			if c.Laddr.Port != cfg.Node.Port && c.Laddr.Port != uint32(12788) &&
				c.Laddr.Port != cfg.Prometheus.Port {
				peers = appendFilteredPeer(peers, c.Raddr.IP, c.Raddr.Port, "o")
			}
		}
	}

	// Skip everything if we have no peers
	if len(peers) == 0 {
		resetPeers()
		failCount.Store(0)
		return nil
	}

	// Early return if peers haven't changed
	peersFilteredMu.Lock()
	equal := slices.Equal(peers, peersFiltered)
	peerStatsMu.Lock()
	hasResults := len(peerStats.RTTresultsSlice) != 0
	peerStatsMu.Unlock()
	if equal && hasResults {
		peersFilteredMu.Unlock()
		return nil
	}

	// Update peersFiltered if changed
	peersFiltered = peers
	peersFilteredMu.Unlock()
	return nil
}

func appendFilteredPeer(peers []string, peerIP string, peerPort uint32, direction string) []string {
	cfg := config.GetConfig()
	peerPortText := strconv.FormatUint(uint64(peerPort), 10)
	if peerIP == "127.0.0.1" || peerIP == "::1" ||
		(publicIP != nil &&
			peerIP == publicIP.String() &&
			peerPortText == strconv.FormatUint(uint64(cfg.Node.Port), 10)) {
		return peers
	}

	for idx, existing := range peers {
		probe, ok := parsePeerProbe(existing)
		if !ok || probe.ip != peerIP {
			continue
		}
		nextDirection := mergePeerDirections(probe.direction, direction)
		nextPort := preferredPeerProbePort(probe, peerPortText, direction)
		if nextDirection == probe.direction && nextPort == probe.port {
			return peers
		}
		peers = slices.Delete(peers, idx, idx+1)
		return append(peers, fmt.Sprintf("%s;%s;%s", peerIP, nextPort, nextDirection))
	}
	return append(peers, fmt.Sprintf("%s;%s;%s", peerIP, peerPortText, direction))
}

func preferredPeerProbePort(existing peerProbe, nextPort, nextDirection string) string {
	if directionHasOutgoing(nextDirection) {
		return nextPort
	}
	if directionHasOutgoing(existing.direction) {
		return existing.port
	}
	return nextPort
}

func directionHasOutgoing(direction string) bool {
	return direction == "o" || direction == "i+o"
}

func mergePeerDirections(existing, next string) string {
	if existing == next || existing == "i+o" {
		return existing
	}
	if (existing == "i" && next == "o") || (existing == "o" && next == "i") {
		return "i+o"
	}
	return next
}

func pingPeers(ctx context.Context) {
	if !dashboardShowsPeers() && getActiveSecondaryView() != viewPeers {
		failCount.Store(0)
		return
	}

	scrollPeers = false
	peersFilteredMu.RLock()
	peers := slices.Clone(peersFiltered)
	peersFilteredMu.RUnlock()
	if len(peers) == 0 {
		return
	}

	now := time.Now()
	freshAfter := now.Add(-peerProbeFreshFor)
	inFlightAfter := now.Add(-peerProbeStaleFor)
	probes := make([]peerProbe, 0, peerProbeBatchSize)

	peerStatsMu.Lock()
	if peerStats.RTTresultsMap == nil {
		peerStats.RTTresultsMap = make(peerRTTresultsMap, len(peers))
	}
	if peerStats.InFlight == nil {
		peerStats.InFlight = make(map[string]time.Time, len(peers))
	}
	prunePeerStatsLocked(peers)
	for _, peer := range peers {
		probe, ok := parsePeerProbe(peer)
		if !ok {
			continue
		}
		existing, exists := peerStats.RTTresultsMap[probe.ip]
		if exists &&
			existing.UpdatedAt.After(freshAfter) &&
			existing.RTT != 0 &&
			existing.Location != "---" {
			continue
		}
		if startedAt, inFlight := peerStats.InFlight[probe.ip]; inFlight &&
			startedAt.After(inFlightAfter) {
			continue
		}
		probe = schedulePeerProbeLocked(probe, now)
		probes = append(probes, probe)
		if len(probes) >= peerProbeBatchSize {
			break
		}
	}
	recomputePeerStatsLocked()
	peerStatsMu.Unlock()

	for _, probe := range probes {
		go probePeer(ctx, probe)
	}
	failCount.Store(0)
}

func schedulePeerProbeLocked(probe peerProbe, scheduledAt time.Time) peerProbe {
	probe.scheduledAt = scheduledAt
	peerStats.InFlight[probe.ip] = scheduledAt
	return probe
}

func parsePeerProbe(peer string) (peerProbe, bool) {
	peerArr := strings.Split(peer, ";")
	if len(peerArr) < 3 {
		return peerProbe{}, false
	}
	return peerProbe{
		ip:        peerArr[0],
		port:      peerArr[1],
		direction: peerArr[2],
	}, true
}

func probePeer(ctx context.Context, probe peerProbe) {
	select {
	case <-ctx.Done():
		return
	default:
	}

	peerRTT := tcpinfoRtt(net.JoinHostPort(probe.ip, probe.port))
	peerPort, err := strconv.Atoi(probe.port)
	if err != nil {
		peerPort = 0
	}
	peerLocation := getGeoIP(ctx, probe.ip)
	peer := &Peer{
		IP:        probe.ip,
		Port:      peerPort,
		Direction: probe.direction,
		RTT:       peerRTT,
		Location:  peerLocation,
		UpdatedAt: time.Now(),
	}

	completePeerProbe(probe, peer)
}

func completePeerProbe(probe peerProbe, peer *Peer) bool {
	peerStatsMu.Lock()
	defer peerStatsMu.Unlock()
	startedAt, inFlight := peerStats.InFlight[probe.ip]
	if !inFlight || !startedAt.Equal(probe.scheduledAt) {
		return false
	}
	if peerStats.RTTresultsMap == nil {
		peerStats.RTTresultsMap = make(peerRTTresultsMap)
	}
	peerStats.RTTresultsMap[probe.ip] = peer
	delete(peerStats.InFlight, probe.ip)
	recomputePeerStatsLocked()
	return true
}

func prunePeerStatsLocked(peers []string) {
	active := make(map[string]struct{}, len(peers))
	for _, peer := range peers {
		probe, ok := parsePeerProbe(peer)
		if !ok {
			continue
		}
		active[probe.ip] = struct{}{}
	}
	for peerIP := range peerStats.RTTresultsMap {
		if _, ok := active[peerIP]; !ok {
			delete(peerStats.RTTresultsMap, peerIP)
		}
	}
	for peerIP := range peerStats.InFlight {
		if _, ok := active[peerIP]; !ok {
			delete(peerStats.InFlight, peerIP)
		}
	}
}

func recomputePeerStatsLocked() {
	peerStats.CNT0 = 0
	peerStats.CNT1 = 0
	peerStats.CNT2 = 0
	peerStats.CNT3 = 0
	peerStats.CNT4 = 0
	peerStats.RTTSUM = 0
	peerStats.RTTAVG = 0
	peerStats.PCT1 = 0
	peerStats.PCT2 = 0
	peerStats.PCT3 = 0
	peerStats.PCT4 = 0
	peerStats.PCT1items = 0
	peerStats.PCT2items = 0
	peerStats.PCT3items = 0
	peerStats.PCT4items = 0
	peerStats.RTTresultsSlice = peerStats.RTTresultsSlice[:0]

	for _, peer := range peerStats.RTTresultsMap {
		peerStats.RTTresultsSlice = append(peerStats.RTTresultsSlice, peer)
		if peer.RTT < RTTThreshold1 {
			peerStats.RTTSUM += peer.RTT
			peerStats.CNT1++
		} else if peer.RTT < RTTThreshold2 {
			peerStats.RTTSUM += peer.RTT
			peerStats.CNT2++
		} else if peer.RTT < RTTThreshold3 {
			peerStats.RTTSUM += peer.RTT
			peerStats.CNT3++
		} else if peer.RTT < RTTUnreachable {
			peerStats.RTTSUM += peer.RTT
			peerStats.CNT4++
		} else {
			peerStats.CNT0++
		}
	}
	sort.Sort(peerStats.RTTresultsSlice)

	peerCNTReachable := len(peerStats.RTTresultsSlice) - peerStats.CNT0
	if peerCNTReachable <= 0 {
		return
	}
	peerStats.RTTAVG = peerStats.RTTSUM / peerCNTReachable
	granularitySmall := ProgressBarGranularity / 2
	peerStats.PCT1 = float32(peerStats.CNT1) / float32(peerCNTReachable) * 100
	peerStats.PCT1items = int(peerStats.PCT1) * granularitySmall / 100
	peerStats.PCT2 = float32(peerStats.CNT2) / float32(peerCNTReachable) * 100
	peerStats.PCT2items = int(peerStats.PCT2) * granularitySmall / 100
	peerStats.PCT3 = float32(peerStats.CNT3) / float32(peerCNTReachable) * 100
	peerStats.PCT3items = int(peerStats.PCT3) * granularitySmall / 100
	peerStats.PCT4 = float32(peerStats.CNT4) / float32(peerCNTReachable) * 100
	peerStats.PCT4items = int(peerStats.PCT4) * granularitySmall / 100
}

func resetPeers() {
	peersFilteredMu.Lock()
	peerStatsMu.Lock()
	peersFiltered = []string{}
	resetPeerStatsLocked()
	peerStatsMu.Unlock()
	peersFilteredMu.Unlock()
}

func resetPeerStatsLocked() {
	peerStats = PeerStats{
		RTTresultsMap:   make(peerRTTresultsMap),
		RTTresultsSlice: peerRTTresultsSlice{},
		InFlight:        make(map[string]time.Time),
	}
}

var peerStats PeerStats

type PeerStats struct {
	RTTSUM          int
	RTTAVG          int
	CNT0            int
	CNT1            int
	CNT2            int
	CNT3            int
	CNT4            int
	PCT1            float32
	PCT2            float32
	PCT3            float32
	PCT4            float32
	PCT1items       int
	PCT2items       int
	PCT3items       int
	PCT4items       int
	RTTresultsMap   peerRTTresultsMap
	RTTresultsSlice peerRTTresultsSlice
	InFlight        map[string]time.Time
}

type Peer struct {
	Direction string
	IP        string
	RTT       int
	Port      int
	Location  string
	UpdatedAt time.Time
}

type (
	peerRTTresultsMap   map[string]*Peer
	peerRTTresultsSlice []*Peer
)

// Len is part of sort.Interface
func (p peerRTTresultsSlice) Len() int {
	return len(p)
}

// Swap is part of sort.Interface
func (p peerRTTresultsSlice) Swap(i, j int) {
	p[i], p[j] = p[j], p[i]
}

// Less is part of sort.Interface. RTT remains the primary key, but endpoint
// details make the order deterministic when RTT values are tied or unavailable.
func (p peerRTTresultsSlice) Less(i, j int) bool {
	left := p[i]
	right := p[j]
	if left == nil || right == nil {
		return right != nil
	}
	if left.RTT != right.RTT {
		return left.RTT < right.RTT
	}
	if left.IP != right.IP {
		return left.IP < right.IP
	}
	if left.Port != right.Port {
		return left.Port < right.Port
	}
	if left.Direction != right.Direction {
		return left.Direction < right.Direction
	}
	return left.Location < right.Location
}
