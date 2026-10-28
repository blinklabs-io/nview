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
	"fmt"
	"math"
	"strconv"
	"sync"
	"time"

	"github.com/blinklabs-io/nview/internal/config"
)

const (
	dingoCounterLeaderWon  = "leader_won"
	dingoCounterLeaderLost = "leader_not_won"
	dingoCounterMissed     = "missed"
	dingoCounterBattles    = "battles"
	dingoCounterOwnEquiv   = "own_equivocation"
	dingoCounterKESSuccess = "kes_sign_success"
	dingoCounterKESFailure = "kes_sign_failure"
)

var dingoForgeLeaderMetricNames = []string{
	"dingo_forge_credentials_valid",
	"dingo_forge_opcert_counter_loaded",
	"dingo_forge_opcert_counter_onchain",
	"dingo_forge_slots_per_kes_period",
	"cardano_node_metrics_currentKESPeriod_int",
	"cardano_node_metrics_remainingKESPeriods_int",
	"dingo_kes_agent_connected",
	"dingo_kes_agent_sign_success_total",
	"dingo_kes_agent_sign_failure_total",
	"dingo_metrics_leader_slot_won_total",
	"dingo_metrics_leader_slot_not_won_total",
	"dingo_forge_missed_leader_slots_total",
	"dingo_metrics_slotBattlesTotal_int",
	"dingo_metrics_leader_last_epoch_slots_checked_int",
	"dingo_metrics_leader_last_epoch_slots_won_int",
	"dingo_metrics_leader_last_epoch_slots_not_won_int",
	"dingo_metrics_leader_last_evaluated_epoch_int",
	"dingo_equivocation_total_self",
}

var dingoForgeLeaderMetricSet = func() map[string]struct{} {
	result := make(map[string]struct{}, len(dingoForgeLeaderMetricNames))
	for _, name := range dingoForgeLeaderMetricNames {
		result[name] = struct{}{}
	}
	return result
}()

func isDingoForgeLeaderMetric(name string) bool {
	_, ok := dingoForgeLeaderMetricSet[name]
	return ok
}

// DingoCounterWindow contains counter deltas between two consecutive scrapes.
// Available distinguishes a real zero delta from a missing or reset counter.
type DingoCounterWindow struct {
	Valid     bool
	Interval  time.Duration
	Deltas    map[string]uint64
	Available map[string]bool
}

type dingoForgeCounterHistory struct {
	mu       sync.Mutex
	previous map[string]uint64
	at       time.Time
}

var globalDingoForgeCounterHistory dingoForgeCounterHistory

var dingoCounterMetricNames = map[string]string{
	dingoCounterLeaderWon:  "dingo_metrics_leader_slot_won_total",
	dingoCounterLeaderLost: "dingo_metrics_leader_slot_not_won_total",
	dingoCounterMissed:     "dingo_forge_missed_leader_slots_total",
	dingoCounterBattles:    "dingo_metrics_slotBattlesTotal_int",
	dingoCounterOwnEquiv:   "dingo_equivocation_total_self",
	dingoCounterKESSuccess: "dingo_kes_agent_sign_success_total",
	dingoCounterKESFailure: "dingo_kes_agent_sign_failure_total",
}

func (h *dingoForgeCounterHistory) Apply(metrics *PromMetrics, at time.Time) {
	if metrics == nil {
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()

	window := DingoCounterWindow{
		Deltas:    make(map[string]uint64),
		Available: make(map[string]bool),
	}
	if !h.at.IsZero() && at.After(h.at) {
		window.Interval = at.Sub(h.at)
	}
	if h.previous == nil {
		h.previous = make(map[string]uint64)
	}

	current := dingoCounterValues(metrics)
	for key, metricName := range dingoCounterMetricNames {
		value, present := current[key]
		if !present || !metrics.PromMetricPresent[metricName] {
			delete(h.previous, key)
			continue
		}
		previous, hadPrevious := h.previous[key]
		if hadPrevious && window.Interval > 0 && value >= previous {
			window.Deltas[key] = value - previous
			window.Available[key] = true
		}
		h.previous[key] = value
	}

	window.Valid = window.Available[dingoCounterLeaderWon] &&
		window.Available[dingoCounterMissed] &&
		window.Available[dingoCounterBattles] &&
		window.Available[dingoCounterOwnEquiv]
	metrics.DingoCounterWindow = window
	h.at = at
}

func dingoCounterValues(metrics *PromMetrics) map[string]uint64 {
	return map[string]uint64{
		dingoCounterLeaderWon:  metrics.DingoLeaderWon,
		dingoCounterLeaderLost: metrics.DingoLeaderNotWon,
		dingoCounterMissed:     metrics.DingoMissedLeaderSlots,
		dingoCounterBattles:    metrics.DingoSlotBattles,
		dingoCounterOwnEquiv:   metrics.DingoOwnEquivocations,
		dingoCounterKESSuccess: metrics.DingoKESAgentSignSuccess,
		dingoCounterKESFailure: metrics.DingoKESAgentSignFailure,
	}
}

func dingoConsoleForgeLeaderBand(metrics *PromMetrics, width int) string {
	return dingoConsoleForgeLeaderBandAt(metrics, width, time.Now())
}

func dingoConsoleForgeLeaderBandAt(
	metrics *PromMetrics,
	width int,
	now time.Time,
) string {
	if metrics == nil || !hasDingoForgeLeaderMetrics(metrics) {
		return dingoPanel(
			"forge / leader",
			uiSeverityMuted,
			width,
			[]string{dingoMetricRowColumns(
				dingoPanelInnerWidth(width),
				2,
				dingoMetricSpan("Status", uiMuted("not exposed"), 2),
			)},
		)
	}
	if !dingoForgeSnapshotFresh(metrics, now) {
		return dingoPanel(
			"forge / leader",
			uiSeverityWarn,
			width,
			[]string{dingoMetricRowColumns(
				dingoPanelInnerWidth(width),
				2,
				dingoMetricStyledWide(
					"Status",
					uiSeverityValue("stale", uiSeverityWarn),
					uiSeverityWarn,
				),
			)},
		)
	}

	innerWidth := dingoPanelInnerWidth(width)
	credentials, credentialsSeverity := dingoCredentialStatus(metrics)
	opCert, opCertSeverity := dingoOpCertStatus(metrics)
	kesExpiry, kesExpirySeverity := dingoKESExpiryStatus(metrics)
	kesAgent, kesAgentSeverity := dingoKESAgentStatus(metrics)
	missRatio, missRatioSeverity := dingoMissRatio(metrics)
	lastEpoch, lastEpochSeverity := dingoLastEpochStatus(metrics)
	equivocationSeverity := uiSeverityOK
	if metrics.DingoOwnEquivocations > 0 {
		equivocationSeverity = uiSeverityCritical
	}
	if !metrics.PromMetricPresent["dingo_equivocation_total_self"] {
		equivocationSeverity = uiSeverityMuted
	}

	credentialsCell := dingoMetricStyled("Credentials", credentials, credentialsSeverity)
	opCertCell := dingoMetricStyled("OpCert", opCert, opCertSeverity)
	kesExpiryCell := dingoMetricStyledWide("KES Expiry", kesExpiry, kesExpirySeverity)
	kesAgentCell := dingoMetricStyled("KES Agent", kesAgent, kesAgentSeverity)
	kesSuccessCell := dingoMetric(
		"KES OK",
		dingoCounterDisplay(metrics, dingoCounterKESSuccess, metrics.DingoKESAgentSignSuccess),
	)
	kesFailureSeverity := dingoTrackedCounterSeverity(
		metrics,
		dingoCounterKESFailure,
		metrics.DingoKESAgentSignFailure,
	)
	kesFailureCell := dingoMetricStyled(
		"KES Fail",
		dingoCounterDisplay(metrics, dingoCounterKESFailure, metrics.DingoKESAgentSignFailure),
		kesFailureSeverity,
	)
	wonCell := dingoMetric(
		"Won",
		dingoCounterDisplay(metrics, dingoCounterLeaderWon, metrics.DingoLeaderWon),
	)
	notWonTotalCell := dingoMetric(
		"Not Won",
		dingoCounterDisplay(metrics, dingoCounterLeaderLost, metrics.DingoLeaderNotWon),
	)
	missedSeverity := dingoTrackedCounterSeverity(
		metrics,
		dingoCounterMissed,
		metrics.DingoMissedLeaderSlots,
	)
	missedCell := dingoMetricStyled(
		"Missed",
		dingoCounterDisplay(metrics, dingoCounterMissed, metrics.DingoMissedLeaderSlots),
		missedSeverity,
	)
	battlesSeverity := dingoTrackedCounterSeverity(
		metrics,
		dingoCounterBattles,
		metrics.DingoSlotBattles,
	)
	battlesCell := dingoMetricStyled(
		"Battles",
		dingoCounterDisplay(metrics, dingoCounterBattles, metrics.DingoSlotBattles),
		battlesSeverity,
	)
	missRatioCell := dingoMetricStyled("Miss Ratio", missRatio, missRatioSeverity)
	lastEpochCell := dingoMetricStyled("Last Epoch", lastEpoch, lastEpochSeverity)
	scheduleCell := dingoMetric("Schedule", dingoLastEpochSchedule(metrics))
	equivocationCell := dingoMetricStyled(
		"Own Equiv",
		dingoCounterDisplay(metrics, dingoCounterOwnEquiv, metrics.DingoOwnEquivocations),
		equivocationSeverity,
	)
	notWonCell := dingoMetric("Epoch Not Won", dingoLastEpochNotWon(metrics))

	lines := []string{
		dingoMetricRow(
			innerWidth,
			credentialsCell,
			opCertCell,
			kesExpiryCell,
		),
		dingoMetricRow(
			innerWidth,
			kesAgentCell,
			kesSuccessCell,
			kesFailureCell,
			missRatioCell,
		),
		dingoMetricRow(
			innerWidth,
			wonCell,
			notWonTotalCell,
			missedCell,
			battlesCell,
		),
		dingoMetricRowColumns(
			innerWidth,
			2,
			lastEpochCell,
			scheduleCell,
		),
		dingoMetricRowColumns(
			innerWidth,
			2,
			equivocationCell,
			notWonCell,
		),
	}
	if innerWidth < 88 {
		lines = []string{
			dingoMetricRowColumns(innerWidth, 2, credentialsCell, opCertCell),
			dingoMetricRowColumns(innerWidth, 2, kesExpiryCell),
			dingoMetricRowColumns(innerWidth, 2, kesAgentCell, kesSuccessCell),
			dingoMetricRowColumns(innerWidth, 2, kesFailureCell, missRatioCell),
			dingoMetricRowColumns(innerWidth, 2, wonCell, notWonTotalCell),
			dingoMetricRowColumns(innerWidth, 2, missedCell, battlesCell),
			dingoMetricRowColumns(innerWidth, 2, lastEpochCell, scheduleCell),
			dingoMetricRowColumns(innerWidth, 2, equivocationCell, notWonCell),
		}
	}

	return dingoPanel(
		"forge / leader",
		worstSeverity(
			credentialsSeverity,
			opCertSeverity,
			kesExpirySeverity,
			kesAgentSeverity,
			kesFailureSeverity,
			missedSeverity,
			battlesSeverity,
			missRatioSeverity,
			equivocationSeverity,
		),
		width,
		lines,
	)
}

func hasDingoForgeLeaderMetrics(metrics *PromMetrics) bool {
	if metrics == nil {
		return false
	}
	for _, name := range dingoForgeLeaderMetricNames {
		if metrics.PromMetricPresent[name] {
			return true
		}
	}
	return false
}

func dingoForgeSnapshotFresh(metrics *PromMetrics, now time.Time) bool {
	if metrics == nil || metrics.CollectedAt.IsZero() {
		return false
	}
	age := now.Sub(metrics.CollectedAt)
	if age < 0 {
		return false
	}
	maxAge := 30 * time.Second
	if refresh := config.GetConfig().Prometheus.Refresh; refresh > 0 {
		configured := 3 * time.Duration(refresh) * time.Second
		if configured > maxAge {
			maxAge = configured
		}
	}
	return age <= maxAge
}

func dingoCredentialStatus(metrics *PromMetrics) (string, uiSeverity) {
	if !metrics.PromMetricPresent["dingo_forge_credentials_valid"] {
		return uiMuted("n/a"), uiSeverityMuted
	}
	if metrics.DingoForgeCredentialsValid == 1 {
		return uiSeverityValue("valid", uiSeverityOK), uiSeverityOK
	}
	return uiSeverityValue("invalid", uiSeverityCritical), uiSeverityCritical
}

func dingoOpCertStatus(metrics *PromMetrics) (string, uiSeverity) {
	if !metrics.PromMetricPresent["dingo_forge_opcert_counter_loaded"] ||
		!metrics.PromMetricPresent["dingo_forge_opcert_counter_onchain"] {
		return uiMuted("n/a"), uiSeverityMuted
	}
	loaded := metrics.DingoForgeOpCertLoaded
	onChain := metrics.DingoForgeOpCertOnChain
	severity := uiSeverityOK
	if loaded != onChain && (onChain == math.MaxUint64 || loaded != onChain+1) {
		severity = uiSeverityWarn
	}
	drift := ""
	if loaded >= onChain {
		drift = "+" + strconv.FormatUint(loaded-onChain, 10)
	} else {
		drift = "-" + strconv.FormatUint(onChain-loaded, 10)
	}
	return uiSeverityValue(
		fmt.Sprintf("%d / %d", loaded, onChain),
		severity,
	) + uiUnit(" ("+drift+")"), severity
}

func dingoKESExpiryStatus(metrics *PromMetrics) (string, uiSeverity) {
	if !metrics.PromMetricPresent["dingo_forge_slots_per_kes_period"] ||
		!metrics.PromMetricPresent["cardano_node_metrics_remainingKESPeriods_int"] ||
		metrics.DingoForgeSlotsPerKESPeriod == 0 {
		return uiMuted("n/a"), uiSeverityMuted
	}
	if metrics.RemainingKesPeriods > math.MaxUint64/metrics.DingoForgeSlotsPerKESPeriod {
		return uiSeverityValue("invalid", uiSeverityCritical), uiSeverityCritical
	}
	slots := metrics.RemainingKesPeriods * metrics.DingoForgeSlotsPerKESPeriod
	slotLength := config.GetConfig().Node.ShelleyGenesis.SlotLength
	severity := uiSeverityOK
	if slots == 0 {
		severity = uiSeverityCritical
	}
	if slotLength == 0 {
		return uiSeverityValue(strconv.FormatUint(slots, 10), severity) +
			uiUnit(" slots / ") + uiMuted("n/a days"), severity
	}
	days := float64(slots) * float64(slotLength) / (1000 * 86400)
	if slots > 0 && days < 7 {
		severity = uiSeverityWarn
	}
	return uiSeverityValue(strconv.FormatUint(slots, 10), severity) +
		uiUnit(" slots / ") +
		uiSeverityValue(fmt.Sprintf("%.1f", days), severity) +
		uiUnit(" days"), severity
}

func dingoKESAgentStatus(metrics *PromMetrics) (string, uiSeverity) {
	if !metrics.PromMetricPresent["dingo_kes_agent_connected"] {
		return uiMuted("not exposed"), uiSeverityMuted
	}
	if metrics.DingoKESAgentConnected == 1 {
		return uiSeverityValue("connected", uiSeverityOK), uiSeverityOK
	}
	return uiSeverityValue("disconnected", uiSeverityCritical), uiSeverityCritical
}

func dingoCounterDisplay(metrics *PromMetrics, key string, total uint64) string {
	metricName, ok := dingoCounterMetricNames[key]
	if !ok || !metrics.PromMetricPresent[metricName] {
		return uiMuted("n/a")
	}
	value := uiValue(strconv.FormatUint(total, 10))
	if metrics.DingoCounterWindow.Available[key] {
		value += uiUnit(" (+") +
			uiValue(strconv.FormatUint(metrics.DingoCounterWindow.Deltas[key], 10)) +
			uiUnit(")")
	}
	return value
}

func dingoTrackedCounterSeverity(metrics *PromMetrics, key string, value uint64) uiSeverity {
	metricName, ok := dingoCounterMetricNames[key]
	if !ok || !metrics.PromMetricPresent[metricName] {
		return uiSeverityMuted
	}
	return dingoFailureCounterSeverity(value)
}

func dingoMissRatio(metrics *PromMetrics) (string, uiSeverity) {
	if !metrics.DingoCounterWindow.Valid {
		return uiMuted("n/a"), uiSeverityMuted
	}
	won := metrics.DingoCounterWindow.Deltas[dingoCounterLeaderWon]
	if won == 0 {
		return uiMuted("n/a"), uiSeverityMuted
	}
	missed := metrics.DingoCounterWindow.Deltas[dingoCounterMissed]
	ratio := float64(missed) * 100 / float64(won)
	severity := uiSeverityOK
	if missed > 0 {
		severity = uiSeverityWarn
	}
	return uiSeverityValue(fmt.Sprintf("%.1f%%", ratio), severity), severity
}

func dingoLastEpochStatus(metrics *PromMetrics) (string, uiSeverity) {
	required := []string{
		"dingo_metrics_leader_last_evaluated_epoch_int",
		"dingo_metrics_leader_last_epoch_slots_checked_int",
		"dingo_metrics_leader_last_epoch_slots_won_int",
		"dingo_metrics_leader_last_epoch_slots_not_won_int",
	}
	for _, name := range required {
		if !metrics.PromMetricPresent[name] {
			return uiMuted("n/a"), uiSeverityMuted
		}
	}
	severity := uiSeverityOK
	if metrics.DingoLeaderLastEpochWon+metrics.DingoLeaderLastEpochNotWon !=
		metrics.DingoLeaderLastEpochChecked {
		severity = uiSeverityWarn
	}
	return uiSeverityValue(
		strconv.FormatUint(metrics.DingoLeaderLastEpoch, 10),
		severity,
	), severity
}

func dingoLastEpochSchedule(metrics *PromMetrics) string {
	if !metrics.PromMetricPresent["dingo_metrics_leader_last_epoch_slots_checked_int"] ||
		!metrics.PromMetricPresent["dingo_metrics_leader_last_epoch_slots_won_int"] {
		return uiMuted("n/a")
	}
	return uiValue(strconv.FormatUint(metrics.DingoLeaderLastEpochWon, 10)) +
		uiUnit(" / ") +
		uiValue(strconv.FormatUint(metrics.DingoLeaderLastEpochChecked, 10)) +
		uiUnit(" won")
}

func dingoLastEpochNotWon(metrics *PromMetrics) string {
	if !metrics.PromMetricPresent["dingo_metrics_leader_last_epoch_slots_not_won_int"] {
		return uiMuted("n/a")
	}
	return uiValue(strconv.FormatUint(metrics.DingoLeaderLastEpochNotWon, 10))
}

func dingoFailureCounterSeverity(value uint64) uiSeverity {
	if value > 0 {
		return uiSeverityWarn
	}
	return uiSeverityOK
}
