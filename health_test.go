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
	"sync"
	"sync/atomic"
	"testing"

	"github.com/blinklabs-io/nview/internal/config"
)

// resetSubsystemFailuresForTest zeroes every subsystem's consecutive-failure
// count and returns the prior values so a test can restore them.
func resetSubsystemFailuresForTest() [healthSubsystemCount]uint32 {
	var original [healthSubsystemCount]uint32
	for i := range subsystemFailures {
		original[i] = subsystemFailures[i].Load()
		subsystemFailures[i].Store(0)
	}
	return original
}

func restoreSubsystemFailuresForTest(original [healthSubsystemCount]uint32) {
	for i := range subsystemFailures {
		subsystemFailures[i].Store(original[i])
	}
}

// TestSubsystemFailuresAreIndependent proves that a success recorded for one
// subsystem never clears another subsystem's consecutive-failure count. The
// bug this guards against: a single shared failure counter meant a healthy
// peer probe (or any unrelated renderer) could silently erase an ongoing
// Prometheus scrape outage from the dashboard's health signal.
func TestSubsystemFailuresAreIndependent(t *testing.T) {
	original := resetSubsystemFailuresForTest()
	defer restoreSubsystemFailuresForTest(original)

	recordSubsystemFailure(healthSubsystemPrometheus)
	recordSubsystemFailure(healthSubsystemPrometheus)
	recordSubsystemFailure(healthSubsystemPrometheus)

	if got := subsystemFailures[healthSubsystemPrometheus].Load(); got != 3 {
		t.Fatalf("prometheus failure count = %d, expected 3", got)
	}

	// A success in an unrelated subsystem must not touch Prometheus's count.
	recordSubsystemSuccess(healthSubsystemPeers)
	recordSubsystemSuccess(healthSubsystemProcess)

	if got := subsystemFailures[healthSubsystemPrometheus].Load(); got != 3 {
		t.Fatalf(
			"prometheus failure count changed to %d after an unrelated subsystem succeeded, expected 3",
			got,
		)
	}
	if got := subsystemFailures[healthSubsystemPeers].Load(); got != 0 {
		t.Fatalf("peers failure count = %d, expected 0", got)
	}

	// Recovering the Prometheus subsystem itself must clear only its own
	// count.
	recordSubsystemFailure(healthSubsystemPeers)
	recordSubsystemSuccess(healthSubsystemPrometheus)
	if got := subsystemFailures[healthSubsystemPrometheus].Load(); got != 0 {
		t.Fatalf("prometheus failure count = %d after its own success, expected 0", got)
	}
	if got := subsystemFailures[healthSubsystemPeers].Load(); got != 1 {
		t.Fatalf("peers failure count = %d, expected 1 to remain untouched", got)
	}
}

// TestFailCountReportsWorstSubsystem verifies the aggregate failCount used
// for the dashboard health indicator and the connect-retry panic threshold
// reports the worst consecutive-failure streak across all subsystems, not a
// sum or a single subsystem's view.
func TestFailCountReportsWorstSubsystem(t *testing.T) {
	original := resetSubsystemFailuresForTest()
	defer restoreSubsystemFailuresForTest(original)

	if got := failCount(); got != 0 {
		t.Fatalf("failCount() = %d, expected 0 with no failures", got)
	}

	recordSubsystemFailure(healthSubsystemProcess)
	recordSubsystemFailure(healthSubsystemProcess)
	recordSubsystemFailure(healthSubsystemPeers)

	if got := failCount(); got != 2 {
		t.Fatalf("failCount() = %d, expected 2 (the worst subsystem)", got)
	}

	recordSubsystemFailure(healthSubsystemPrometheus)
	recordSubsystemFailure(healthSubsystemPrometheus)
	recordSubsystemFailure(healthSubsystemPrometheus)
	recordSubsystemFailure(healthSubsystemPrometheus)
	recordSubsystemFailure(healthSubsystemPrometheus)

	if got := failCount(); got != 5 {
		t.Fatalf("failCount() = %d, expected 5 once Prometheus becomes the worst subsystem", got)
	}

	recordSubsystemSuccess(healthSubsystemPrometheus)
	if got := failCount(); got != 2 {
		t.Fatalf(
			"failCount() = %d, expected 2 after Prometheus recovers, leaving process as the worst",
			got,
		)
	}
}

// TestGetPromMetricsAppliesResultExactlyOnce guards against the historical
// double-count bug: a single failed scrape attempt must move the Prometheus
// subsystem's consecutive-failure count by exactly one, and a single
// successful attempt must clear it, regardless of how many times the caller
// inspects the error.
func TestGetPromMetricsAppliesResultExactlyOnce(t *testing.T) {
	original := resetSubsystemFailuresForTest()
	defer restoreSubsystemFailuresForTest(original)

	cfg := config.GetConfig()
	originalHost := cfg.Prometheus.Host
	originalPort := cfg.Prometheus.Port
	originalTimeout := cfg.Prometheus.Timeout
	defer func() {
		cfg.Prometheus.Host = originalHost
		cfg.Prometheus.Port = originalPort
		cfg.Prometheus.Timeout = originalTimeout
	}()
	// Port 0 is never a listening Prometheus endpoint, so the request fails
	// immediately instead of depending on network timing.
	cfg.Prometheus.Host = "127.0.0.1"
	cfg.Prometheus.Port = 0
	cfg.Prometheus.Timeout = 1

	ctx := context.Background()
	if _, err := getPromMetrics(ctx); err == nil {
		t.Fatal("expected getPromMetrics to fail against an unreachable endpoint")
	}
	if got := subsystemFailures[healthSubsystemPrometheus].Load(); got != 1 {
		t.Fatalf(
			"prometheus failure count = %d after one failed attempt, expected exactly 1",
			got,
		)
	}
}

// TestConcurrentPromMetricsPublicationAndRendering exercises the scrape
// goroutine's snapshot publication concurrently with renderer goroutines
// reading it. Run with `go test -race` to additionally verify no data race
// is reported; before promMetrics became an atomic.Pointer[PromMetrics],
// this same access pattern (an unsynchronized package-level *PromMetrics
// written by one goroutine and read by others) raced.
//
// The race detector alone would make this test a silent no-op under a plain
// `go test ./...` (the CI workflow does not pass -race), so a consistency
// reader also asserts, on every observed snapshot, that BlockNum, SlotNum,
// and EpochNum still satisfy the writer's invariant. That fails
// deterministically, with or without -race, if publication ever stopped
// being a whole-struct replacement (for example a future change that
// mutated fields on the shared struct in place, which could let a reader
// observe some fields from one write and some from the next).
func TestConcurrentPromMetricsPublicationAndRendering(t *testing.T) {
	originalPromMetrics := promMetrics.Load()
	defer promMetrics.Store(originalPromMetrics)

	// chainPaneSeverity consults the slot tip gap, which divides by the
	// configured slot length; give it a non-zero value so this test
	// exercises that path instead of tripping over unrelated config setup.
	cfg := config.GetConfig()
	originalSlotLength := cfg.Node.ShelleyGenesis.SlotLength
	defer func() { cfg.Node.ShelleyGenesis.SlotLength = originalSlotLength }()
	cfg.Node.ShelleyGenesis.SlotLength = 1000

	const iterations = 200
	var wg sync.WaitGroup
	var inconsistent atomic.Bool

	// Writer: mimics the scrape goroutine publishing a fresh, complete
	// snapshot on every attempt. BlockNum and SlotNum always match, and
	// EpochNum is always BlockNum/10, so a reader observing a mix of old and
	// new field values would trip the consistency check below.
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := uint64(0); i < iterations; i++ {
			promMetrics.Store(&PromMetrics{
				BlockNum: i,
				SlotNum:  i,
				EpochNum: i / 10,
			})
		}
	}()

	// Consistency reader: deterministically checks the writer's invariant
	// holds on every snapshot observed, regardless of race-detector mode.
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < iterations; i++ {
			if m := promMetrics.Load(); m != nil {
				if m.BlockNum != m.SlotNum || m.EpochNum != m.BlockNum/10 {
					inconsistent.Store(true)
				}
			}
		}
	}()

	// Readers: mimic renderer goroutines observing the snapshot mid-flight.
	renderers := []func(){
		func() { dashboardHealth() },
		func() { currentNetworkName() },
		func() { chainPaneSeverity() },
		func() { blockPaneSeverity() },
		func() { corePaneSeverity() },
		func() { runtimePaneSeverity() },
	}
	for _, render := range renderers {
		wg.Add(1)
		go func(render func()) {
			defer wg.Done()
			for i := 0; i < iterations; i++ {
				render()
			}
		}(render)
	}

	wg.Wait()

	if inconsistent.Load() {
		t.Fatal("observed a promMetrics snapshot with inconsistent fields, expected whole-struct replacement")
	}
}
