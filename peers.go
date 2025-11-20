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
	"fmt"
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
	checkPeers  bool = true
	scrollPeers bool = false
)

func filterPeers(ctx context.Context) error {
	var peers []string
	if len(peerStats.RTTresultsSlice) != 0 &&
		len(peerStats.RTTresultsSlice) == len(peersFiltered) {
		return nil
	}

	if processMetrics == nil {
		return nil // TODO: what to do here
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

	var peersIn []string
	var peersOut []string

	// Loops each connection, looking for ESTABLISHED
	for _, c := range connections {
		if c.Status == "ESTABLISHED" {
			// If local port == node port, it's incoming (except P2P)
			if c.Laddr.Port == cfg.Node.Port {
				peersIn = append(
					peersIn,
					fmt.Sprintf("%s:%d", c.Raddr.IP, c.Raddr.Port),
				)
			}
			// If local port != node port, ekg port, or prometheus port, it's outgoing
			if c.Laddr.Port != cfg.Node.Port && c.Laddr.Port != uint32(12788) &&
				c.Laddr.Port != cfg.Prometheus.Port {
				peersOut = append(
					peersOut,
					fmt.Sprintf("%s:%d", c.Raddr.IP, c.Raddr.Port),
				)
			}
		}
	}

	// Skip everything if we have no peers
	if len(peersIn) == 0 && len(peersOut) == 0 {
		failCount.Store(0)
		return nil
	}

	// Process peersIn
	for _, peer := range peersIn {
		p := strings.Split(peer, ":")
		if p == nil {
			continue
		}
		peerIP := p[0]
		peerPORT := p[1]
		if strings.HasPrefix(peerIP, "[") { // IPv6
			peerIP = strings.TrimPrefix(strings.TrimSuffix(peerIP, "]"), "[")
		}

		if peerIP == "127.0.0.1" ||
			(publicIP != nil && peerIP == publicIP.String() && peerPORT == strconv.FormatUint(uint64(cfg.Node.Port), 10)) {
			// Do nothing
			continue
		} else {
			added := false
			for i, toCheck := range peers {
				checkIPArr := strings.Split(toCheck, ":")
				if checkIPArr == nil {
					continue
				}
				checkIP := checkIPArr[0]
				if checkIP == peerIP {
					if p[2] != "i" {
						// Remove and re-add as duplex (i+o)
						peers = slices.Delete(peers, i, i+1)
						peers = append(peers, fmt.Sprintf("%s;%s;i+o", peerIP, peerPORT))
						added = true
						break
					}
				}
			}
			if !added {
				peers = append(peers, fmt.Sprintf("%s;%s;i", peerIP, peerPORT))
			}
		}
	}

	// Process peersOut
	for _, peer := range peersOut {
		p := strings.Split(peer, ":")
		if p == nil {
			continue
		}
		peerIP := p[0]
		peerPORT := p[1]
		if strings.HasPrefix(peerIP, "[") { // IPv6
			peerIP = strings.TrimPrefix(strings.TrimSuffix(peerIP, "]"), "[")
		}

		if peerIP == "127.0.0.1" ||
			(publicIP != nil && peerIP == publicIP.String() && peerPORT == strconv.FormatUint(uint64(cfg.Node.Port), 10)) {
			// Do nothing
			continue
		} else {
			added := false
			for i, toCheck := range peers {
				checkIPArr := strings.Split(toCheck, ":")
				if checkIPArr == nil {
					continue
				}
				checkIP := checkIPArr[0]
				if checkIP == peerIP {
					if p[2] != "o" {
						// Remove and re-add as duplex (i+o)
						peers = slices.Delete(peers, i, i+1)
						peers = append(peers, fmt.Sprintf("%s;%s;i+o", peerIP, peerPORT))
						added = true
						break
					}
				}
			}
			if !added {
				peers = append(peers, fmt.Sprintf("%s;%s;o", peerIP, peerPORT))
			}
		}
	}
	// TODO: do this better than just a length check
	if len(peers) != len(peersFiltered) {
		peersFiltered = peers
	}
	return nil
}

func pingPeers(ctx context.Context) error {
	scrollPeers = false
	granularity := 68
	granularitySmall := granularity / 2
	if checkPeers {
		// counters, etc.
		peerCount := len(peersFiltered)
		var peerRTT int
		var wg sync.WaitGroup
		for _, v := range peersFiltered {
			// increment waitgroup counter
			wg.Add(1)

			go func() {
				defer wg.Done()
				peerArr := strings.Split(v, ";")
				if peerArr == nil {
					return
				}
				peerIP := peerArr[0]
				peerPORT := peerArr[1]
				peerDIR := peerArr[2]

				// Return early if we've been checked recently
				now := time.Now()
				expire := now.Add(-600 * time.Second)
				existing, ok := peerStats.RTTresultsMap[peerIP]
				if ok {
					if existing.UpdatedAt.After(expire) && existing.RTT != 0 {
						return
					}
					if existing.Location != "---" {
						return
					}
				}
				if len(peerStats.RTTresultsMap) == 0 {
					peerStats.RTTresultsMap = make(
						peerRTTresultsMap,
						len(peersFiltered),
					)
				}

				// Start RTT loop
				// for tool in ... return peerRTT
				peerRTT = tcpinfoRtt(fmt.Sprintf("%s:%s", peerIP, peerPORT))
				if peerRTT != 99999 {
					peerStats.RTTSUM = peerStats.RTTSUM + peerRTT
				}
				// Update counters
				if peerRTT < 50 {
					peerStats.CNT1 = peerStats.CNT1 + 1
				} else if peerRTT < 100 {
					peerStats.CNT2 = peerStats.CNT2 + 1
				} else if peerRTT < 200 {
					peerStats.CNT3 = peerStats.CNT3 + 1
				} else if peerRTT < 99999 {
					peerStats.CNT4 = peerStats.CNT4 + 1
				} else {
					peerStats.CNT0 = peerStats.CNT0 + 1
				}
				peerPort, err := strconv.Atoi(peerPORT)
				if err != nil {
					peerPort = 0
				}
				peerLocation := getGeoIP(ctx, peerIP)
				peer := &Peer{
					IP:        peerIP,
					Port:      peerPort,
					Direction: peerDIR,
					RTT:       peerRTT,
					Location:  peerLocation,
					UpdatedAt: time.Now(),
				}
				peerStats.RTTresultsMap[peerIP] = peer
				peerStats.RTTresultsSlice = append(
					peerStats.RTTresultsSlice,
					peer,
				)
				sort.Sort(peerStats.RTTresultsSlice)
			}()
			wg.Wait()
		}
		peerCNTreachable := peerCount - peerStats.CNT0
		if peerCNTreachable > 0 {
			peerStats.RTTAVG = peerStats.RTTSUM / peerCNTreachable
			peerStats.PCT1 = float32(
				peerStats.CNT1,
			) / float32(
				peerCNTreachable,
			) * 100
			peerStats.PCT1items = int(peerStats.PCT1) * granularitySmall / 100
			peerStats.PCT2 = float32(
				peerStats.CNT2,
			) / float32(
				peerCNTreachable,
			) * 100
			peerStats.PCT2items = int(peerStats.PCT2) * granularitySmall / 100
			peerStats.PCT3 = float32(
				peerStats.CNT3,
			) / float32(
				peerCNTreachable,
			) * 100
			peerStats.PCT3items = int(peerStats.PCT3) * granularitySmall / 100
			peerStats.PCT4 = float32(
				peerStats.CNT4,
			) / float32(
				peerCNTreachable,
			) * 100
			peerStats.PCT4items = int(peerStats.PCT4) * granularitySmall / 100
		}
		if len(peerStats.RTTresultsSlice) != 0 &&
			len(peerStats.RTTresultsSlice) >= peerCount {
			checkPeers = false
			scrollPeers = true
		}
	}
	failCount.Store(0)
	return nil
}

func resetPeers() {
	peerStats.CNT0 = 0
	peerStats.CNT1 = 0
	peerStats.CNT2 = 0
	peerStats.CNT3 = 0
	peerStats.CNT4 = 0
	peerStats.RTTSUM = 0
	peerStats.RTTresultsSlice = []*Peer{}
	for _, peerIP := range peerStats.RTTresultsMap {
		peerIP.RTT = 0
	}
	peersFiltered = []string{}
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

// Less is part of sort.Interface and we use RTT as the value to sort by
func (p peerRTTresultsSlice) Less(i, j int) bool {
	return p[i].RTT < p[j].RTT
}
