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
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/blinklabs-io/nview/internal/config"
)

func TestDashboardKeepsRefreshingThroughSubsystemFailures(t *testing.T) {
	for _, subsystem := range []healthSubsystem{healthSubsystemPrometheus, healthSubsystemProcess, healthSubsystemPeers} {
		t.Run(fmt.Sprint(subsystem), func(t *testing.T) {
			original := resetSubsystemFailuresForTest()
			defer restoreSubsystemFailuresForTest(original)
			originalMetrics := promMetrics.Load()
			defer promMetrics.Store(originalMetrics)
			promMetrics.Store(&PromMetrics{BlockNum: 42})
			cfg := config.GetConfig()
			oldRetries := cfg.App.Retries
			defer func() { cfg.App.Retries = oldRetries }()
			cfg.App.Retries = 3
			subsystemFailures[subsystem].Store(6)
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			defer func() {
				if r := recover(); r != nil {
					t.Fatalf("dashboard panicked instead of allowing recovery: %v", r)
				}
			}()
			calls := 0
			runDashboardRefreshLoop(ctx, time.Millisecond, func() {
				calls++
				if calls == 1 {
					if label, severity := dashboardHealth(); label != "degraded" || severity != uiSeverityCritical {
						t.Errorf("unhealthy dashboard = %q/%d", label, severity)
					}
					recordSubsystemSuccess(subsystem)
				} else {
					if label, _ := dashboardHealth(); label != "online" {
						t.Errorf("recovered dashboard = %q", label)
					}
					cancel()
				}
			})
			if calls != 2 {
				t.Fatalf("refresh calls = %d, want unhealthy and recovered refresh", calls)
			}
		})
	}
}

func TestPrometheusRefreshReportsErrorsAndRecovers(t *testing.T) {
	fixture, err := os.ReadFile("testdata/cardano-node-11.1.2.prom")
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name   string
		body   string
		status int
		cause  string
	}{
		{"unsupported type", "# TYPE unexpected future_type\nunexpected 1\n", 200, "unknown metric type"},
		{"malformed sample", "cardano_node_metrics_blockNum_int not-a-number\n", 200, "parsing error"},
		{"invalid integer", "cardano_node_metrics_blockNum_int 1.5\n", 200, "JSON unmarshal"},
		{"nonfinite sample", "unexpected NaN\n", 200, "unsupported value"},
		{"HTTP error", "unavailable", 503, "HTTP: 503"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			original := resetSubsystemFailuresForTest()
			defer restoreSubsystemFailuresForTest(original)
			oldMetrics := promMetrics.Load()
			defer promMetrics.Store(oldMetrics)
			oldBinary := getEffectiveNodeBinary()
			defer detectedNodeBinary.Store(oldBinary)
			oldLogger := logger
			defer func() { logger = oldLogger }()
			var logs bytes.Buffer
			logger = slog.New(slog.NewTextHandler(&logs, nil))
			var body atomic.Value
			body.Store(tc.body)
			var status atomic.Int32
			status.Store(int32(tc.status))
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/metrics" {
					t.Errorf("unexpected scrape path %q", r.URL.Path)
				}
				w.WriteHeader(int(status.Load()))
				fmt.Fprint(w, body.Load().(string))
			}))
			defer server.Close()
			pointMetricsClientAtTestServer(t, server, 1)
			// The first failed attempt must work without a previous snapshot.
			promMetrics.Store(nil)
			refreshPrometheusMetrics(context.Background())
			if promMetrics.Load() == nil {
				t.Fatal("first failed scrape left the dashboard without a snapshot")
			}
			lastGood := &PromMetrics{BlockNum: 42, EpochNum: 1}
			promMetrics.Store(lastGood)
			for i := 2; i <= 6; i++ {
				refreshPrometheusMetrics(context.Background())
				if got := subsystemFailures[healthSubsystemPrometheus].Load(); got != uint32(i) {
					t.Fatalf("attempt %d failure count = %d", i, got)
				}
				if promMetrics.Load() != lastGood {
					t.Fatal("failed scrape replaced the last good snapshot")
				}
			}
			if !strings.Contains(logs.String(), tc.cause) {
				t.Errorf("scrape cause %q missing from logs: %s", tc.cause, &logs)
			}
			recordSubsystemFailure(healthSubsystemPeers)
			body.Store(string(fixture))
			status.Store(200)
			refreshPrometheusMetrics(context.Background())
			if got := promMetrics.Load(); got == nil || got.BlockNum != 14004704 || got.PeersKnown != 150 {
				t.Fatalf("scrape did not recover: %+v", got)
			}
			if got := subsystemFailures[healthSubsystemPrometheus].Load(); got != 0 {
				t.Fatalf("successful scrape retained %d failures", got)
			}
			if got := subsystemFailures[healthSubsystemPeers].Load(); got != 1 {
				t.Fatalf("successful scrape changed unrelated peer failures to %d", got)
			}
		})
	}
}

func TestDashboardRetriesSetsDegradedThreshold(t *testing.T) {
	original := resetSubsystemFailuresForTest()
	defer restoreSubsystemFailuresForTest(original)
	cfg := config.GetConfig()
	oldRetries := cfg.App.Retries
	defer func() { cfg.App.Retries = oldRetries }()
	cfg.App.Retries = 3
	subsystemFailures[healthSubsystemPeers].Store(2)
	if label, _ := dashboardHealth(); label != "retrying" {
		t.Fatalf("before threshold: health = %q", label)
	}
	subsystemFailures[healthSubsystemPeers].Store(3)
	if label, _ := dashboardHealth(); label != "degraded" {
		t.Fatalf("at threshold: health = %q", label)
	}
}
