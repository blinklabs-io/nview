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
	"time"

	"github.com/blinklabs-io/nview/internal/config"
)

func refreshPrometheusMetrics(ctx context.Context) {
	prom, err := getPromMetrics(ctx)
	if err != nil || prom == nil {
		if err != nil && logger != nil {
			logger.Warn("Failed to fetch Prometheus metrics", "error", err)
		}
		if promMetrics.Load() == nil {
			promMetrics.Store(&PromMetrics{})
		}
		return
	}
	if getEffectiveNodeBinary() == DINGO_BINARY {
		if config.ApplyDingoGenesisOverride(
			prom.DingoShelleyStartTime,
			prom.DingoEpochLengthSlots,
		) && logger != nil {
			logger.Info(
				"genesis params overridden from Dingo metrics",
				"shelleyStart",
				prom.DingoShelleyStartTime,
				"epochLengthSlots",
				prom.DingoEpochLengthSlots,
			)
		}
	}
	applyDefaultSecondaryView()
	promMetrics.Store(prom)
	updateMithrilView()
}

func runDashboardRefreshLoop(ctx context.Context, refreshDelay time.Duration, refresh func()) {
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}
		refresh()
		select {
		case <-ctx.Done():
			return
		case <-time.After(refreshDelay):
		}
	}
}
