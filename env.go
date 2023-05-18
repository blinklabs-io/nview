// Copyright 2023 Blink Labs, LLC.
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
	"io"
	"math"
	"net/http"
	"time"

	"github.com/blinklabs-io/gouroboros/protocol/localstatequery"
)

// Fetches the node metrics and return a byte array
func getNodeMetrics(ctx context.Context) ([]byte, int, error) {
	// Load our config and get host/port
	cfg := GetConfig()
	url := fmt.Sprintf(
		"http://%s:%d/metrics",
		cfg.Prometheus.Host,
		cfg.Prometheus.Port,
	)
	var respBodyBytes []byte
	// Setup request
	req, err := http.NewRequest(
		http.MethodGet,
		url,
		nil,
	)
	if err != nil {
		return respBodyBytes, http.StatusInternalServerError, err
	}
	// Set a 3 second timeout
	ctx, cancel := context.WithTimeout(ctx, time.Second*time.Duration(cfg.Prometheus.Timeout))
	defer cancel()
	req = req.WithContext(ctx)
	// Get metrics from the node
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return respBodyBytes, http.StatusInternalServerError, err
	}
	// Read the entire response body and close it to prevent a memory leak
	respBodyBytes, err = io.ReadAll(resp.Body)
	if err != nil {
		return respBodyBytes, http.StatusInternalServerError, err
	}
	defer resp.Body.Close()
	return respBodyBytes, resp.StatusCode, nil
}

// Calculate epoch from current second
func getEpoch() uint64 {
	cfg := GetConfig()
	currentTimeSec := uint64(time.Now().Unix() - 1)
	byronEndTime := cfg.Node.ByronGenesis.StartTime + ((uint64(cfg.Node.ShelleyTransEpoch) * cfg.Node.ByronGenesis.EpochLength * cfg.Node.ByronGenesis.SlotLength) / 1000)
	result := uint64(
		cfg.Node.ShelleyTransEpoch,
	) + ((currentTimeSec - byronEndTime) / cfg.Node.ByronGenesis.EpochLength / cfg.Node.ByronGenesis.SlotLength)
	return uint64(result)
}

// Calculate slot number
func getSlotTipRef(g *localstatequery.GenesisConfigResult) uint64 {
	cfg := GetConfig()
	currentTimeSec := uint64(time.Now().Unix() - 1)
	byronSlots := uint64(cfg.Node.ShelleyTransEpoch) * cfg.Node.ByronGenesis.EpochLength
	byronEndTime := cfg.Node.ByronGenesis.StartTime + ((uint64(cfg.Node.ShelleyTransEpoch) * cfg.Node.ByronGenesis.EpochLength * cfg.Node.ByronGenesis.SlotLength) / 1000)
	if currentTimeSec < byronEndTime {
		return ((currentTimeSec - cfg.Node.ByronGenesis.StartTime) * 1000) / cfg.Node.ByronGenesis.SlotLength
	}
	return byronSlots + ((currentTimeSec - byronEndTime) / uint64(g.SlotLength/1000000))
}

// Calculate expected interval between blocks
func slotInterval(g *localstatequery.GenesisConfigResult) uint64 {
	// g.SlotLength is nanoseconds
	// 0.05 is g.ActiveSlotsCoeff resolved
	// 0.5 is decentralisation (removed in babbage... so use default)
	result := (float64(g.SlotLength/1000000) / 0.05 / 0.5) + 0.5
	return uint64(result)
}

// Time is in seconds
func timeLeft(t uint64) string {
	d := t / 60 / 60 / 24
	h := math.Mod(float64(t/60/60), 24)
	m := math.Mod(float64(t/60), 60)
	s := math.Mod(float64(t), 60)
	var result string
	if d > 0 {
		result = fmt.Sprintf("%dd ", d)
	}
	return fmt.Sprintf("%s%02d:%02d:%02d", result, int(h), int(m), int(s))
}

func timeUntilNextEpoch() uint64 {
	cfg := GetConfig()
	currentTimeSec := uint64(time.Now().Unix() - 1)
	result := ((uint64(cfg.Node.ShelleyTransEpoch) * cfg.Node.ByronGenesis.EpochLength * cfg.Node.ByronGenesis.SlotLength) / 1000) + ((getEpoch() + uint64(1) - uint64(cfg.Node.ShelleyTransEpoch)) * cfg.Node.ByronGenesis.EpochLength * cfg.Node.ByronGenesis.SlotLength) - currentTimeSec + cfg.Node.ByronGenesis.StartTime
	return uint64(result)
}
