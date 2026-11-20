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
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/rivo/tview"
)

const kesLifetimeMetrics = `
cardano_node_metrics_forging_enabled 1
cardano_node_metrics_currentKESPeriod_int 110
cardano_node_metrics_operationalCertificateStartKESPeriod_int 100
cardano_node_metrics_operationalCertificateExpiryKESPeriod_int 160
cardano_node_metrics_remainingKESPeriods_int 50
dingo_forge_credentials_valid 1
dingo_forge_slots_per_kes_period 129600
`

func decodeKESLifetimeMetrics(t *testing.T) *PromMetrics {
	t.Helper()
	encoded, err := prometheusText2json([]byte(kesLifetimeMetrics))
	if err != nil {
		t.Fatalf("prometheusText2json() error = %v", err)
	}
	var metrics PromMetrics
	if err := json.Unmarshal(encoded, &metrics); err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}
	return &metrics
}

func TestCoreTextShowsKESLifetimeBar(t *testing.T) {
	setTestVisuals(t, "plain", "none")
	originalMetrics := promMetrics.Load()
	originalRole := role
	originalBinary, _ := detectedNodeBinary.Load().(string)
	t.Cleanup(func() {
		promMetrics.Store(originalMetrics)
		role = originalRole
		detectedNodeBinary.Store(originalBinary)
	})

	role = "Core"
	detectedNodeBinary.Store(CARDANO_BINARY)
	promMetrics.Store(decodeKESLifetimeMetrics(t))
	text := stripTviewTags(getCoreText(context.Background()))
	for _, expected := range []string{
		"KES left", "50 periods", "75.0d", "83.3%", "#",
	} {
		if !strings.Contains(text, expected) {
			t.Errorf("forge view missing KES lifetime %q:\n%s", expected, text)
		}
	}
	for _, line := range strings.Split(getCoreText(context.Background()), "\n") {
		if strings.Contains(line, "KES left") &&
			tview.TaggedStringWidth(line) > 43 {
			t.Errorf(
				"KES lifetime row is wider than the default Forge pane: %q",
				line,
			)
		}
	}
}

func TestDingoForgePanelShowsKESLifetimeBar(t *testing.T) {
	setTestVisuals(t, "plain", "none")
	metrics := decodeKESLifetimeMetrics(t)
	metrics.CollectedAt = time.Unix(2000, 0)
	panel := stripTviewTags(
		dingoConsoleForgeLeaderBandAt(metrics, 118, metrics.CollectedAt),
	)
	for _, expected := range []string{
		"KES Life", "50 periods", "75.0d", "83.3%", "#",
	} {
		if !strings.Contains(panel, expected) {
			t.Errorf(
				"Dingo forge panel missing KES lifetime %q:\n%s",
				expected,
				panel,
			)
		}
	}
}

func TestAmaruRelayDoesNotClaimKESLifetime(t *testing.T) {
	setTestVisuals(t, "plain", "none")
	originalMetrics := promMetrics.Load()
	originalRole := role
	originalBinary, _ := detectedNodeBinary.Load().(string)
	t.Cleanup(func() {
		promMetrics.Store(originalMetrics)
		role = originalRole
		detectedNodeBinary.Store(originalBinary)
	})

	metrics := decodeAmaruFixture(t)
	role = "Relay"
	detectedNodeBinary.Store(AMARU_BINARY)
	promMetrics.Store(metrics)
	text := stripTviewTags(getCoreText(context.Background()))
	if strings.Contains(text, "KES") ||
		!strings.Contains(text, "observing only") {
		t.Fatalf(
			"Amaru relay forge view claims an unavailable KES lifetime:\n%s",
			text,
		)
	}
}

func TestKESLifetimeStatusDoesNotInventProgress(t *testing.T) {
	setTestVisuals(t, "plain", "none")
	metrics := &PromMetrics{
		RemainingKesPeriods: 4,
		PromMetricPresent: map[string]bool{
			kesRemainingMetric: true,
		},
	}
	value, severity := kesLifetimeStatus(metrics, 8)
	plain := stripTviewTags(value)
	if severity != uiSeverityWarn ||
		!strings.Contains(plain, "4 periods / 6.0d") {
		t.Fatalf(
			"partial KES lifetime = %q, %d, expected periods and days",
			plain,
			severity,
		)
	}
	if strings.Contains(plain, "%") || strings.Contains(plain, "#") {
		t.Fatalf("partial KES lifetime invents progress: %q", plain)
	}

	value, severity = kesLifetimeStatus(&PromMetrics{}, 8)
	if stripTviewTags(value) != "n/a" || severity != uiSeverityMuted {
		t.Fatalf("missing KES lifetime = %q, %d, expected n/a", value, severity)
	}

	metrics.RemainingKesPeriods = 0
	value, severity = kesLifetimeStatus(metrics, 8)
	if stripTviewTags(value) != "n/a" || severity != uiSeverityMuted {
		t.Fatalf(
			"partial zero KES lifetime = %q, %d, expected n/a",
			value,
			severity,
		)
	}
}

func TestKESLifetimeRender(t *testing.T) {
	setTestVisuals(t, "plain", "none")
	metrics := decodeKESLifetimeMetrics(t)
	cardano, _ := kesLifetimeStatus(metrics, 7)
	dingo, _ := kesLifetimeStatus(metrics, 12)
	got := fmt.Sprintf(
		"cardano-node Forge\n KES left %s\n\n"+
			"Dingo Forge / Leader\n KES Life %s\n",
		stripTviewTags(cardano),
		stripTviewTags(dingo),
	)
	want, err := os.ReadFile(
		filepath.Join("testdata", "kes-lifetime-render.txt"),
	)
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	if got != string(want) {
		t.Errorf("KES lifetime render mismatch:\n%s", got)
	}
}

func TestKESLifetimeCompleteWindow(t *testing.T) {
	setTestVisuals(t, "plain", "none")
	for _, tc := range []struct {
		name               string
		current, remaining uint64
		severity           uiSeverity
		text               string
		bar                bool
	}{
		{"activation", 100, 60, uiSeverityOK, "60 periods / 90.0d", true},
		{"active", 110, 50, uiSeverityOK, "50 periods / 75.0d", true},
		{"expiry", 160, 0, uiSeverityCritical, "0 periods / 0.0d", true},
		{"expired", 161, 0, uiSeverityCritical, "0 periods / 0.0d", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			metrics := decodeKESLifetimeMetrics(t)
			metrics.KesPeriod, metrics.RemainingKesPeriods = tc.current, tc.remaining
			value, severity := kesLifetimeStatus(metrics, 7)
			plain := stripTviewTags(value)
			if severity != tc.severity || !strings.Contains(plain, tc.text) {
				t.Fatalf("KES lifetime = %q, severity %d; want %q, severity %d", plain, severity, tc.text, tc.severity)
			}
			if strings.Contains(plain, "%") != tc.bar {
				t.Fatalf("KES lifetime progress = %q, expected known %v", plain, tc.bar)
			}
			row := " " + uiKVStyledAligned(8, "KES left", value, severity)
			if width := tview.TaggedStringWidth(row); width > 43 {
				t.Fatalf("KES row has %d columns, want at most 43: %q", width, plain)
			}
		})
	}
}
