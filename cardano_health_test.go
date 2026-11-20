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
	"math"
	"strings"
	"testing"
	"time"

	"github.com/blinklabs-io/nview/internal/config"
	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
)

func configureCardanoKESClock(
	t *testing.T,
	start time.Time,
	slotLength uint64,
	slotsPerKESPeriod uint64,
) {
	t.Helper()
	cfg := config.GetConfig()
	originalByron := cfg.Node.ByronGenesis
	originalShelley := cfg.Node.ShelleyGenesis
	originalTransition := cfg.Node.ShelleyTransEpoch
	t.Cleanup(func() {
		cfg.Node.ByronGenesis = originalByron
		cfg.Node.ShelleyGenesis = originalShelley
		cfg.Node.ShelleyTransEpoch = originalTransition
	})
	cfg.Node.ByronGenesis.StartTime = uint64(start.Unix())
	cfg.Node.ByronGenesis.EpochLength = 1
	cfg.Node.ByronGenesis.SlotLength = 1000
	cfg.Node.ShelleyGenesis.SlotLength = slotLength
	cfg.Node.ShelleyGenesis.SlotsPerKESPeriod = slotsPerKESPeriod
	cfg.Node.ShelleyTransEpoch = 0
}

func TestCardanoNodeHealthViewsUseRecordedMetrics(t *testing.T) {
	cfg := config.GetConfig()
	originalBinary := cfg.Node.Binary
	originalDetectedBinary, _ := detectedNodeBinary.Load().(string)
	originalMetrics := promMetrics.Load()
	originalRole := role
	originalByron := cfg.Node.ByronGenesis
	originalShelley := cfg.Node.ShelleyGenesis
	originalTransition := cfg.Node.ShelleyTransEpoch
	defer func() {
		cfg.Node.Binary = originalBinary
		detectedNodeBinary.Store(originalDetectedBinary)
		promMetrics.Store(originalMetrics)
		role = originalRole
		cfg.Node.ByronGenesis = originalByron
		cfg.Node.ShelleyGenesis = originalShelley
		cfg.Node.ShelleyTransEpoch = originalTransition
	}()

	cfg.Node.Binary = ""
	cfg.Node.ByronGenesis.StartTime = uint64(
		time.Now().Add(-50 * 129_600 * time.Second).Unix(),
	)
	cfg.Node.ByronGenesis.EpochLength = 21_600
	cfg.Node.ByronGenesis.SlotLength = 20_000
	cfg.Node.ShelleyGenesis.EpochLength = 86_400
	cfg.Node.ShelleyGenesis.SlotLength = 1_000
	cfg.Node.ShelleyGenesis.SlotsPerKESPeriod = 129_600
	cfg.Node.ShelleyTransEpoch = 0
	detectedNodeBinary.Store(CARDANO_BINARY)
	role = "Core"
	producer := decodeFixture(t, "cardano-node-11.1.3-block-producer.om.txt")
	producer.CouldNotForge = 2
	producer.ForgedInvalid = 3
	producer.AdoptionThreadDied = 4
	producer.PromMetricPresent["cardano_node_metrics_Forge_could_not_forge_counter"] = true
	producer.PromMetricPresent["cardano_node_metrics_Forge_forged_invalid_counter"] = true
	producer.PromMetricPresent["cardano_node_metrics_Forge_adoption_thread_died_counter"] = true
	promMetrics.Store(&producer)
	expectedExpiry, _ := cardanoKESExpiryStatusAt(&producer, time.Now())
	if expectedExpiry == "expired" || strings.HasPrefix(expectedExpiry, "period ") {
		t.Fatalf("test fixture has no future KES countdown: %q", expectedExpiry)
	}

	for name, rendered := range map[string]string{
		"overview": getOverviewText(context.Background()),
		"core":     getCoreText(context.Background()),
		"peers":    getPeerText(context.Background()),
	} {
		for _, expected := range map[string][]string{
			"overview": {"sync", "syncing"},
			"core": {
				"Not leader", "40123", "Last forged", "0 slots",
				"Cannot", "2", "Bad", "3", "Adopt", "4",
				"KES window", "0-60", "KES expiry",
			},
			"peers": {"Local roots", "1", "active"},
		}[name] {
			if !strings.Contains(rendered, expected) {
				t.Errorf("%s view missing %q:\n%s", name, expected, rendered)
			}
		}
	}

	screen := tcell.NewSimulationScreen("UTF-8")
	if err := screen.Init(); err != nil {
		t.Fatalf("initialize simulation screen: %v", err)
	}
	t.Cleanup(screen.Fini)
	screen.SetSize(45, corePaneHeight)
	view := tview.NewTextView().SetDynamicColors(true)
	view.SetBorder(true)
	view.SetRect(0, 0, 45, corePaneHeight)
	view.SetText(getCoreText(context.Background()))
	view.Draw(screen)
	screen.Show()
	cells, _, _ := screen.GetContents()
	var visible strings.Builder
	for _, cell := range cells {
		visible.WriteString(string(cell.Runes))
	}
	for _, part := range strings.Fields(expectedExpiry) {
		if !strings.Contains(visible.String(), part) {
			t.Fatalf(
				"KES expiry part %q is clipped from the Forge pane:\n%s",
				part,
				visible.String(),
			)
		}
	}

	relay := decodeFixture(t, "cardano-node-11.1.3-relay.om.txt")
	relay.LocalInboundIdle = 1
	relay.LocalInboundCold = 2
	relay.LocalInboundWarm = 3
	relay.LocalInboundHot = 4
	relay.TxMempoolTimeoutSoft = 9
	for _, name := range []string{
		"cardano_node_metrics_localInboundGovernor_idle_int",
		"cardano_node_metrics_localInboundGovernor_cold_int",
		"cardano_node_metrics_localInboundGovernor_warm_int",
		"cardano_node_metrics_localInboundGovernor_hot_int",
		"cardano_node_metrics_txsMempoolTimeoutSoft_counter",
	} {
		relay.PromMetricPresent[name] = true
	}
	promMetrics.Store(&relay)
	for name, rendered := range map[string]string{
		"operator": getOperatorFocusText(context.Background()),
		"peers":    getPeerText(context.Background()),
	} {
		for _, expected := range map[string][]string{
			"operator": {
				"Replay", "99.995%", "Headers", "3425", "ChainSync", "6962",
				"Mempool Pressure", "Sync duration", "Soft timeout", "9",
			},
			"peers": {
				"Big known", "15", "Inbound idle", "cold", "36", "warm", "2",
				"hot", "3", "Local N2C idle", "1",
			},
		}[name] {
			if !strings.Contains(rendered, expected) {
				t.Errorf("%s view missing %q:\n%s", name, expected, rendered)
			}
		}
	}
}

func TestCardanoKESStateErrorIsNotReportedAsExpiry(t *testing.T) {
	now := time.Date(2026, 11, 19, 0, 0, 0, 0, time.UTC)
	configureCardanoKESClock(t, now.Add(-1000*time.Second), 1000, 100)
	metrics := &PromMetrics{
		SlotNum:         1_000,
		OpCertExpiryKES: 20,
		PromMetricPresent: map[string]bool{
			"cardano_node_metrics_currentKESPeriod_int":                      true,
			"cardano_node_metrics_remainingKESPeriods_int":                   true,
			"cardano_node_metrics_operationalCertificateExpiryKESPeriod_int": true,
		},
		CollectedAt: now,
	}
	if !cardanoKESStateUnavailable(metrics) {
		t.Fatal(
			"zeroed KES telemetry with a valid certificate was not detected",
		)
	}
	status, severity := cardanoKESExpiryStatusAt(
		metrics,
		now,
	)
	if status == "expired" || severity == uiSeverityCritical {
		t.Fatalf(
			"valid certificate status = %q, severity = %v",
			status,
			severity,
		)
	}
}

func TestCardanoKESExpiryUsesWallClockSlot(t *testing.T) {
	now := time.Date(2026, 11, 19, 0, 0, 0, 0, time.UTC)
	configureCardanoKESClock(t, now.Add(-1000*time.Second), 1000, 100)
	metrics := &PromMetrics{
		SlotNum:         500,
		OpCertExpiryKES: 20,
		PromMetricPresent: map[string]bool{
			"cardano_node_metrics_operationalCertificateExpiryKESPeriod_int": true,
		},
		CollectedAt: now.Add(-time.Hour),
	}

	status, _ := cardanoKESExpiryStatusAt(metrics, now)
	if status == "expired" {
		t.Fatalf("certificate expired too early: %q", status)
	}
	status, severity := cardanoKESExpiryStatusAt(
		metrics,
		now.Add(1001*time.Second),
	)
	if status != "expired" || severity != uiSeverityCritical {
		t.Fatalf("stale snapshot status = %q, severity = %v", status, severity)
	}
}

func TestCardanoKESWindowPreservesMissingBounds(t *testing.T) {
	tests := []struct {
		name    string
		metrics PromMetrics
		want    string
	}{
		{
			name: "start only",
			metrics: PromMetrics{
				OpCertStartKES: 1500,
				PromMetricPresent: map[string]bool{
					"cardano_node_metrics_operationalCertificateStartKESPeriod_int": true,
				},
			},
			want: "1500-n/a",
		},
		{
			name: "expiry only",
			metrics: PromMetrics{
				OpCertExpiryKES: 20,
				PromMetricPresent: map[string]bool{
					"cardano_node_metrics_operationalCertificateExpiryKESPeriod_int": true,
				},
			},
			want: "n/a-20",
		},
		{
			name: "explicit zero start",
			metrics: PromMetrics{
				OpCertExpiryKES: 20,
				PromMetricPresent: map[string]bool{
					"cardano_node_metrics_operationalCertificateStartKESPeriod_int":  true,
					"cardano_node_metrics_operationalCertificateExpiryKESPeriod_int": true,
				},
			},
			want: "0-20",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := cardanoKESWindow(&tt.metrics); got != tt.want {
				t.Fatalf("cardanoKESWindow() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestCardanoKESExpiryRejectsDurationOverflow(t *testing.T) {
	configureCardanoKESClock(
		t,
		time.Unix(1, 0),
		math.MaxUint32,
		math.MaxUint32,
	)
	metrics := &PromMetrics{
		OpCertExpiryKES: math.MaxUint32,
		PromMetricPresent: map[string]bool{
			"cardano_node_metrics_operationalCertificateExpiryKESPeriod_int": true,
		},
	}
	status, severity := cardanoKESExpiryStatusAt(metrics, time.Unix(2, 0))
	if !strings.HasPrefix(status, "period ") || severity != uiSeverityMuted {
		t.Fatalf("overflow status = %q, severity = %v", status, severity)
	}
}
