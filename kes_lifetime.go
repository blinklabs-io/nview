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
	"fmt"
	"math"
	"strconv"
	"strings"

	"github.com/blinklabs-io/nview/internal/config"
)

const (
	kesCurrentMetric   = "cardano_node_metrics_currentKESPeriod_int"
	kesStartMetric     = "cardano_node_metrics_operationalCertificateStartKESPeriod_int"
	kesExpiryMetric    = "cardano_node_metrics_operationalCertificateExpiryKESPeriod_int"
	kesRemainingMetric = "cardano_node_metrics_remainingKESPeriods_int"
)

func kesLifetimeStatus(
	metrics *PromMetrics,
	barWidth int,
) (string, uiSeverity) {
	if metrics == nil || !metrics.PromMetricPresent[kesRemainingMetric] {
		return uiMuted("n/a"), uiSeverityMuted
	}
	percent, progressKnown := kesRemainingPercent(metrics)
	if metrics.RemainingKesPeriods == 0 && !progressKnown {
		return uiMuted("n/a"), uiSeverityMuted
	}

	severity := uiSeverityOK
	if metrics.RemainingKesPeriods == 0 {
		severity = uiSeverityCritical
	}
	parts := []string{uiSeverityValue(
		formatKESPeriods(metrics.RemainingKesPeriods),
		severity,
	)}

	slotsPerPeriod := config.GetConfig().Node.ShelleyGenesis.SlotsPerKESPeriod
	if metrics.PromMetricPresent["dingo_forge_slots_per_kes_period"] &&
		metrics.DingoForgeSlotsPerKESPeriod > 0 {
		slotsPerPeriod = metrics.DingoForgeSlotsPerKESPeriod
	}
	days, daysKnown := kesRemainingDays(
		metrics.RemainingKesPeriods,
		slotsPerPeriod,
		config.GetConfig().Node.ShelleyGenesis.SlotLength,
	)
	if daysKnown {
		if metrics.RemainingKesPeriods > 0 && days < 7 {
			severity = uiSeverityWarn
			parts[0] = uiSeverityValue(
				formatKESPeriods(metrics.RemainingKesPeriods),
				severity,
			)
		}
		parts = append(
			parts,
			uiSeverityValue(fmt.Sprintf("%.1fd", days), severity),
		)
	} else {
		parts = append(parts, uiMuted("n/a days"))
	}

	result := strings.Join(parts, uiUnit(" / "))
	if progressKnown {
		result += "  " +
			uiSeverityValue(fmt.Sprintf("%.1f%%", percent), severity) +
			" " + uiProgressBar(percent, barWidth, severity)
	}
	return result, severity
}

func formatKESPeriods(periods uint64) string {
	unit := " periods"
	if periods == 1 {
		unit = " period"
	}
	return strconv.FormatUint(periods, 10) + unit
}

func kesRemainingDays(
	remainingPeriods uint64,
	slotsPerPeriod uint64,
	slotLengthMilliseconds uint64,
) (float64, bool) {
	if slotsPerPeriod == 0 || slotLengthMilliseconds == 0 ||
		remainingPeriods > math.MaxUint64/slotsPerPeriod {
		return 0, false
	}
	remainingSlots := remainingPeriods * slotsPerPeriod
	return float64(remainingSlots) * float64(slotLengthMilliseconds) /
		(1000 * 86400), true
}

func kesRemainingPercent(metrics *PromMetrics) (float64, bool) {
	if metrics == nil || !metrics.PromMetricPresent[kesCurrentMetric] ||
		!metrics.PromMetricPresent[kesStartMetric] ||
		!metrics.PromMetricPresent[kesExpiryMetric] ||
		metrics.OpCertExpiryKES <= metrics.OpCertStartKES ||
		metrics.KesPeriod < metrics.OpCertStartKES ||
		metrics.KesPeriod > metrics.OpCertExpiryKES ||
		metrics.RemainingKesPeriods != metrics.OpCertExpiryKES-metrics.KesPeriod {
		return 0, false
	}
	lifetime := metrics.OpCertExpiryKES - metrics.OpCertStartKES
	return float64(metrics.RemainingKesPeriods) / float64(lifetime) * 100, true
}
