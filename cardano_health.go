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
	"slices"
	"strconv"
	"time"

	"github.com/blinklabs-io/nview/internal/config"
)

const cardanoKESWarningWindow = 7 * 24 * time.Hour

func hasAnyPromMetric(metrics *PromMetrics, names ...string) bool {
	return slices.ContainsFunc(names, metrics.hasPromMetric)
}

func cardanoGSMStatus(metrics *PromMetrics) (string, uiSeverity, bool) {
	if !metrics.hasPromMetric("cardano_node_metrics_GSM_state_int") {
		return "", uiSeverityMuted, false
	}
	switch metrics.GSMState {
	case 0:
		return "pre-sync", uiSeverityWarn, true
	case 1:
		return "syncing", uiSeverityWarn, true
	case 2:
		return "caught up", uiSeverityOK, true
	default:
		return "unknown " + strconv.FormatUint(metrics.GSMState, 10),
			uiSeverityCritical,
			true
	}
}

func cardanoKESExpiryStatusAt(
	metrics *PromMetrics,
	now time.Time,
) (string, uiSeverity) {
	if !metrics.hasPromMetric(
		"cardano_node_metrics_operationalCertificateExpiryKESPeriod_int",
	) {
		return "n/a", uiSeverityMuted
	}
	slotsPerPeriod := config.GetConfig().Node.ShelleyGenesis.SlotsPerKESPeriod
	slotLengthMs := config.GetConfig().Node.ShelleyGenesis.SlotLength
	if slotsPerPeriod == 0 || slotLengthMs == 0 ||
		metrics.OpCertExpiryKES > math.MaxUint64/slotsPerPeriod {
		return "period " + strconv.FormatUint(metrics.OpCertExpiryKES, 10),
			uiSeverityMuted
	}
	expirySlot := metrics.OpCertExpiryKES * slotsPerPeriod
	currentSlot, ok := networkSlotAt(now)
	if !ok {
		return "period " + strconv.FormatUint(metrics.OpCertExpiryKES, 10),
			uiSeverityMuted
	}
	if expirySlot <= currentSlot {
		return "expired", uiSeverityCritical
	}
	remainingSlots := expirySlot - currentSlot
	maxDurationMillis := uint64(math.MaxInt64 / time.Millisecond)
	if remainingSlots > maxDurationMillis/slotLengthMs {
		return "period " + strconv.FormatUint(metrics.OpCertExpiryKES, 10),
			uiSeverityMuted
	}
	// #nosec G115 -- the millisecond product is bounded to a time.Duration.
	remaining := time.Duration(remainingSlots*slotLengthMs) * time.Millisecond
	severity := uiSeverityOK
	if remaining <= cardanoKESWarningWindow {
		severity = uiSeverityWarn
	}
	return fmt.Sprintf(
		"%s (%s)",
		now.Add(remaining).Format("2006-01-02"),
		formatCardanoDuration(remaining),
	), severity
}

func formatCardanoDuration(duration time.Duration) string {
	days := duration / (24 * time.Hour)
	if days > 0 {
		return fmt.Sprintf("%dd", days)
	}
	hours := duration / time.Hour
	return fmt.Sprintf("%dh", hours)
}

func cardanoKESWindow(metrics *PromMetrics) string {
	const startMetric = "cardano_node_metrics_operationalCertificateStartKESPeriod_int"
	const expiryMetric = "cardano_node_metrics_operationalCertificateExpiryKESPeriod_int"
	if !metrics.hasPromMetric(startMetric) &&
		!metrics.hasPromMetric(expiryMetric) {
		return "n/a"
	}
	start := "n/a"
	if metrics.hasPromMetric(startMetric) {
		start = strconv.FormatUint(metrics.OpCertStartKES, 10)
	}
	expiry := "n/a"
	if metrics.hasPromMetric(expiryMetric) {
		expiry = strconv.FormatUint(metrics.OpCertExpiryKES, 10)
	}
	return start + "-" + expiry
}

func cardanoKESStateUnavailable(metrics *PromMetrics) bool {
	if !hasAnyPromMetric(
		metrics,
		"cardano_node_metrics_currentKESPeriod_int",
		"cardano_node_metrics_remainingKESPeriods_int",
	) || metrics.KesPeriod != 0 || metrics.RemainingKesPeriods != 0 {
		return false
	}
	slotsPerPeriod := config.GetConfig().Node.ShelleyGenesis.SlotsPerKESPeriod
	return slotsPerPeriod > 0 && metrics.SlotNum/slotsPerPeriod > 0 &&
		metrics.OpCertExpiryKES > metrics.SlotNum/slotsPerPeriod
}

func cardanoLastForged(metrics *PromMetrics) string {
	if !metrics.hasPromMetric("cardano_node_metrics_forgedSlotLast_int") {
		return "n/a"
	}
	if metrics.ForgedSlotLast == 0 {
		return "never"
	}
	if metrics.ForgedSlotLast > metrics.SlotNum {
		return "n/a"
	}
	return strconv.FormatUint(
		metrics.SlotNum-metrics.ForgedSlotLast,
		10,
	) + " slots"
}

func cardanoMetricValue(
	metrics *PromMetrics,
	name string,
	value uint64,
) string {
	if !metrics.hasPromMetric(name) {
		return uiMuted("n/a")
	}
	return uiValue(strconv.FormatUint(value, 10))
}

func cardanoMetricSeverityCount(
	metrics *PromMetrics,
	name string,
	value uint64,
	severity uiSeverity,
) string {
	if !metrics.hasPromMetric(name) {
		return uiMuted("n/a")
	}
	return formatSeverityCount(value, severity)
}
