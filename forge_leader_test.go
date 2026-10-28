// Copyright 2026 Blink Labs Software
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
// http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package main

import (
	"encoding/json"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/rivo/tview"
)

func TestPromMetricsDecodesDingoForgeLeaderMetrics(t *testing.T) {
	prometheusText := []byte(`# TYPE dingo_forge_credentials_valid gauge
dingo_forge_credentials_valid 1
dingo_forge_opcert_counter_loaded 7
dingo_forge_opcert_counter_onchain 6
dingo_forge_slots_per_kes_period 129600
cardano_node_metrics_currentKESPeriod_int 141
cardano_node_metrics_remainingKESPeriods_int 4
dingo_kes_agent_connected 1
dingo_kes_agent_sign_success_total 23
dingo_kes_agent_sign_failure_total 2
dingo_metrics_leader_slot_won_total 12
dingo_metrics_leader_slot_not_won_total 88
dingo_forge_missed_leader_slots_total 3
dingo_metrics_slotBattlesTotal_int 4
dingo_metrics_leader_last_epoch_slots_checked_int 100
dingo_metrics_leader_last_epoch_slots_won_int 12
dingo_metrics_leader_last_epoch_slots_not_won_int 88
dingo_metrics_leader_last_evaluated_epoch_int 550
dingo_equivocation_total{pool_id="own",self_key="true"} 2
dingo_equivocation_total{pool_id="other",self_key="false"} 9
`)

	openMetrics := append([]byte{}, prometheusText...)
	openMetrics = append(openMetrics, []byte("# EOF\n")...)
	tests := []struct {
		name        string
		contentType string
		input       []byte
	}{
		{name: "Prometheus", input: prometheusText},
		{
			name:        "OpenMetrics",
			contentType: "application/openmetrics-text; version=1.0.0; charset=utf-8",
			input:       openMetrics,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var (
				encoded []byte
				err     error
			)
			if tt.contentType == "" {
				encoded, err = prometheusText2json(tt.input)
			} else {
				encoded, err = prom2jsonWithContentType(tt.input, tt.contentType)
			}
			if err != nil {
				t.Fatalf("decode metrics: %v", err)
			}
			var got PromMetrics
			if err := json.Unmarshal(encoded, &got); err != nil {
				t.Fatalf("unmarshal metrics: %v", err)
			}
			checks := map[string]uint64{
				"credentials":        got.DingoForgeCredentialsValid,
				"opcert loaded":      got.DingoForgeOpCertLoaded,
				"opcert on-chain":    got.DingoForgeOpCertOnChain,
				"slots/KES":          got.DingoForgeSlotsPerKESPeriod,
				"KES connected":      got.DingoKESAgentConnected,
				"KES sign successes": got.DingoKESAgentSignSuccess,
				"KES sign failures":  got.DingoKESAgentSignFailure,
				"leader won":         got.DingoLeaderWon,
				"leader not won":     got.DingoLeaderNotWon,
				"missed":             got.DingoMissedLeaderSlots,
				"battles":            got.DingoSlotBattles,
				"last checked":       got.DingoLeaderLastEpochChecked,
				"last won":           got.DingoLeaderLastEpochWon,
				"last not won":       got.DingoLeaderLastEpochNotWon,
				"last epoch":         got.DingoLeaderLastEpoch,
				"own equivocation":   got.DingoOwnEquivocations,
			}
			want := map[string]uint64{
				"credentials": 1, "opcert loaded": 7, "opcert on-chain": 6,
				"slots/KES": 129600, "KES connected": 1,
				"KES sign successes": 23, "KES sign failures": 2,
				"leader won": 12, "leader not won": 88, "missed": 3,
				"battles": 4, "last checked": 100, "last won": 12,
				"last not won": 88, "last epoch": 550, "own equivocation": 2,
			}
			for name, value := range checks {
				if value != want[name] {
					t.Errorf("%s = %d, want %d", name, value, want[name])
				}
			}
			for _, name := range dingoForgeLeaderMetricNames {
				if !got.PromMetricPresent[name] {
					t.Errorf("metric %q not marked present", name)
				}
			}
		})
	}
}

func TestPromMetricsSkipsNonFiniteOpCertCounter(t *testing.T) {
	encoded, err := prometheusText2json([]byte(
		"dingo_forge_opcert_counter_onchain NaN\n",
	))
	if err != nil {
		t.Fatalf("decode metrics: %v", err)
	}
	var got PromMetrics
	if err := json.Unmarshal(encoded, &got); err != nil {
		t.Fatalf("unmarshal metrics: %v", err)
	}
	if got.PromMetricPresent["dingo_forge_opcert_counter_onchain"] {
		t.Fatal("non-finite on-chain counter must not be marked usable")
	}
}

func TestDingoForgeCounterHistoryHandlesResetAndMissingSamples(t *testing.T) {
	var history dingoForgeCounterHistory
	base := time.Unix(1000, 0)
	first := forgeCounterFixture(10, 2, 3, 4)
	history.Apply(first, base)
	if first.DingoCounterWindow.Valid {
		t.Fatal("first scrape must establish a baseline, not a delta")
	}

	second := forgeCounterFixture(14, 3, 5, 4)
	history.Apply(second, base.Add(5*time.Second))
	if !second.DingoCounterWindow.Valid {
		t.Fatal("second monotonic scrape should produce a valid window")
	}
	want := map[string]uint64{
		dingoCounterLeaderWon: 4,
		dingoCounterMissed:    1,
		dingoCounterBattles:   2,
		dingoCounterOwnEquiv:  0,
	}
	for name, value := range want {
		if got := second.DingoCounterWindow.Deltas[name]; got != value {
			t.Errorf("delta %s = %d, want %d", name, got, value)
		}
	}

	reset := forgeCounterFixture(1, 0, 0, 0)
	history.Apply(reset, base.Add(10*time.Second))
	if reset.DingoCounterWindow.Valid {
		t.Fatal("counter reset must invalidate the window")
	}

	missing := forgeCounterFixture(2, 1, 1, 1)
	delete(missing.PromMetricPresent, "dingo_metrics_slotBattlesTotal_int")
	history.Apply(missing, base.Add(15*time.Second))
	if missing.DingoCounterWindow.Valid {
		t.Fatal("missing counter must invalidate the window")
	}
}

func TestDingoForgeLeaderPanelShowsProducerHealth(t *testing.T) {
	now := time.Unix(2000, 0)
	metrics := forgeCounterFixture(12, 3, 4, 2)
	metrics.CollectedAt = now
	metrics.DingoForgeCredentialsValid = 1
	metrics.DingoForgeOpCertLoaded = 7
	metrics.DingoForgeOpCertOnChain = 6
	metrics.DingoForgeSlotsPerKESPeriod = 129600
	metrics.RemainingKesPeriods = 4
	metrics.DingoKESAgentConnected = 1
	metrics.DingoKESAgentSignSuccess = 23
	metrics.DingoKESAgentSignFailure = 2
	metrics.DingoLeaderLastEpoch = 550
	metrics.DingoLeaderNotWon = 88
	metrics.DingoLeaderLastEpochChecked = 100
	metrics.DingoLeaderLastEpochWon = 12
	metrics.DingoLeaderLastEpochNotWon = 88
	for _, name := range dingoForgeLeaderMetricNames {
		metrics.PromMetricPresent[name] = true
	}
	metrics.DingoCounterWindow = DingoCounterWindow{
		Valid:    true,
		Interval: 5 * time.Second,
		Deltas: map[string]uint64{
			dingoCounterLeaderWon:  4,
			dingoCounterLeaderLost: 0,
			dingoCounterMissed:     1,
			dingoCounterBattles:    2,
			dingoCounterOwnEquiv:   0,
			dingoCounterKESSuccess: 3,
			dingoCounterKESFailure: 1,
		},
		Available: map[string]bool{
			dingoCounterLeaderWon:  true,
			dingoCounterLeaderLost: true,
			dingoCounterMissed:     true,
			dingoCounterBattles:    true,
			dingoCounterOwnEquiv:   true,
			dingoCounterKESSuccess: true,
			dingoCounterKESFailure: true,
		},
	}

	panel := stripTviewTags(dingoConsoleForgeLeaderBandAt(metrics, 118, now))
	for _, token := range []string{
		"FORGE / LEADER", "Credentials", "valid", "OpCert", "7 / 6",
		"KES Expiry", "518400 slots", "6.0 days", "KES Agent", "connected",
		"Won", "12 (+4)", "Not Won", "88 (+0)", "Missed", "3 (+1)", "Battles", "4 (+2)",
		"Miss Ratio", "25.0%", "Last Epoch", "550", "12 / 100 won",
		"Own Equiv", "2",
	} {
		if !strings.Contains(panel, token) {
			t.Errorf("panel missing %q:\n%s", token, panel)
		}
	}
}

var tviewTagPattern = regexp.MustCompile(`\[[^]]*\]`)

func stripTviewTags(value string) string {
	return tviewTagPattern.ReplaceAllString(value, "")
}

func TestDingoForgeLeaderPanelFailsClosedForStaleOrAbsentData(t *testing.T) {
	now := time.Unix(3000, 0)
	stale := forgeCounterFixture(10, 0, 0, 0)
	stale.CollectedAt = now.Add(-time.Hour)
	stale.DingoForgeCredentialsValid = 1
	stale.PromMetricPresent["dingo_forge_credentials_valid"] = true
	panel := dingoConsoleForgeLeaderBandAt(stale, 118, now)
	if !strings.Contains(panel, "stale") || strings.Contains(panel, "Credentials[white] valid") {
		t.Fatalf("stale panel must not claim healthy credentials:\n%s", panel)
	}

	absent := &PromMetrics{CollectedAt: now}
	panel = dingoConsoleForgeLeaderBandAt(absent, 118, now)
	if !strings.Contains(panel, "not exposed") {
		t.Fatalf("absent producer metrics must be explicit:\n%s", panel)
	}
}

func TestDingoForgeLeaderPanelFitsCompactViewport(t *testing.T) {
	now := time.Unix(4000, 0)
	metrics := forgeCounterFixture(12, 3, 4, 0)
	metrics.CollectedAt = now
	metrics.DingoForgeCredentialsValid = 1
	metrics.DingoForgeOpCertLoaded = 7
	metrics.DingoForgeOpCertOnChain = 6
	metrics.DingoForgeSlotsPerKESPeriod = 129600
	metrics.RemainingKesPeriods = 4
	for _, name := range dingoForgeLeaderMetricNames {
		metrics.PromMetricPresent[name] = true
	}

	panel := dingoConsoleForgeLeaderBandAt(metrics, 71, now)
	wantWidth := tview.TaggedStringWidth(strings.Split(panel, "\n")[1])
	for lineNumber, line := range strings.Split(strings.TrimSuffix(panel, "\n"), "\n") {
		if got := tview.TaggedStringWidth(line); got > wantWidth {
			t.Errorf("line %d width = %d, panel width = %d:\n%s", lineNumber, got, wantWidth, panel)
		}
	}
	for _, token := range []string{"Credentials", "KES Expiry", "Miss Ratio", "Own Equiv"} {
		if !strings.Contains(panel, token) {
			t.Errorf("compact panel missing %q:\n%s", token, panel)
		}
	}
}

func forgeCounterFixture(won, missed, battles, ownEquiv uint64) *PromMetrics {
	return &PromMetrics{
		DingoLeaderWon:           won,
		DingoLeaderNotWon:        88,
		DingoMissedLeaderSlots:   missed,
		DingoSlotBattles:         battles,
		DingoOwnEquivocations:    ownEquiv,
		DingoKESAgentSignSuccess: 10,
		DingoKESAgentSignFailure: 1,
		PromMetricPresent: map[string]bool{
			"dingo_metrics_leader_slot_won_total":     true,
			"dingo_metrics_leader_slot_not_won_total": true,
			"dingo_forge_missed_leader_slots_total":   true,
			"dingo_metrics_slotBattlesTotal_int":      true,
			"dingo_equivocation_total_self":           true,
			"dingo_kes_agent_sign_success_total":      true,
			"dingo_kes_agent_sign_failure_total":      true,
		},
	}
}
