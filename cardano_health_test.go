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
	"strings"
	"testing"
	"time"

	"github.com/blinklabs-io/nview/internal/config"
)

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
	cfg.Node.ByronGenesis.StartTime = 1_506_203_091
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

	for name, rendered := range map[string]string{
		"overview": getOverviewText(context.Background()),
		"core":     getCoreText(context.Background()),
		"peers":    getPeerText(context.Background()),
	} {
		for _, expected := range map[string][]string{
			"overview": {"sync", "syncing"},
			"core": {
				"Not leader", "40123", "Last forged", "0 slots",
				"Cannot", "2", "Bad forge", "3", "Adopt died", "4",
				"KES window", "0-60", "KES expiry",
			},
			"peers": {"Local roots", "1", "active"},
		}[name] {
			if !strings.Contains(rendered, expected) {
				t.Errorf("%s view missing %q:\n%s", name, expected, rendered)
			}
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
	cfg := config.GetConfig()
	originalSlotsPerPeriod := cfg.Node.ShelleyGenesis.SlotsPerKESPeriod
	originalSlotLength := cfg.Node.ShelleyGenesis.SlotLength
	defer func() {
		cfg.Node.ShelleyGenesis.SlotsPerKESPeriod = originalSlotsPerPeriod
		cfg.Node.ShelleyGenesis.SlotLength = originalSlotLength
	}()

	cfg.Node.ShelleyGenesis.SlotsPerKESPeriod = 100
	cfg.Node.ShelleyGenesis.SlotLength = 1000
	metrics := &PromMetrics{
		SlotNum:         1_000,
		OpCertExpiryKES: 20,
		PromMetricPresent: map[string]bool{
			"cardano_node_metrics_currentKESPeriod_int":                      true,
			"cardano_node_metrics_remainingKESPeriods_int":                   true,
			"cardano_node_metrics_operationalCertificateExpiryKESPeriod_int": true,
		},
		CollectedAt: time.Date(2026, 11, 19, 0, 0, 0, 0, time.UTC),
	}
	if !cardanoKESStateUnavailable(metrics) {
		t.Fatal(
			"zeroed KES telemetry with a valid certificate was not detected",
		)
	}
	status, severity := cardanoKESExpiryStatusAt(
		metrics,
		time.Date(2026, 11, 19, 0, 0, 0, 0, time.UTC),
	)
	if status == "expired" || severity == uiSeverityCritical {
		t.Fatalf(
			"valid certificate status = %q, severity = %v",
			status,
			severity,
		)
	}
}
