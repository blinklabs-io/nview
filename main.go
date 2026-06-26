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
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"math"
	"net"
	"os"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/blinklabs-io/nview/internal/config"
	"github.com/blinklabs-io/nview/internal/version"
	"github.com/gdamore/tcell/v2"
	"github.com/mikioh/tcp"
	"github.com/mikioh/tcpinfo"
	"github.com/rivo/tview"
	netutil "github.com/shirou/gopsutil/v3/net"
	"github.com/shirou/gopsutil/v3/process"
	terminal "golang.org/x/term"
)

const (
	AMARU_BINARY   = "amaru"
	DINGO_BINARY   = "dingo"
	CARDANO_BINARY = "cardano-node"

	// Default node names
	DefaultNodeName = "Cardano Node"
	DingoNodeName   = "Dingo"
	AmaruNodeName   = "Amaru"

	// Conversion constants
	BytesInGigabyte       = 1073741824
	MillisecondsToSeconds = 1000

	// RTT thresholds in milliseconds
	RTTThreshold1 = 50  // 0-50ms: good
	RTTThreshold2 = 100 // 50-100ms: acceptable
	RTTThreshold3 = 200 // 100-200ms: slow

	// Sync status thresholds (slot difference)
	SyncThresholdGood = 20  // Within 20 slots: good sync
	SyncThresholdSlow = 600 // Within 600 slots: slow sync

	// Peer RTT sentinel values
	RTTUnreachable = 99999 // Sentinel value for unreachable peers

	// UI layout constants
	TerminalWidthDefault    = 71
	DashboardWidthDefault   = 118
	DashboardHeightDefault  = 30
	ProgressBarGranularity  = 68
	ProcessDiscoveryRefresh = 30 * time.Second
	mithrilOverlayPageName  = "mithril-overlay"
	peerOverlayPageName     = "peer-overlay"
)

var (
	logBuffer []string
	logMutex  sync.Mutex
)

type bufferHandler struct {
	handler slog.Handler
}

func (h *bufferHandler) Enabled(ctx context.Context, level slog.Level) bool {
	return h.handler.Enabled(ctx, level)
}

func (h *bufferHandler) Handle(ctx context.Context, r slog.Record) error {
	logMutex.Lock()
	defer logMutex.Unlock()
	var buf strings.Builder
	if err := h.handler.Handle(ctx, r); err != nil {
		return err
	}
	// Also append to buffer
	buf.WriteString(r.Time.Format(time.RFC3339))
	buf.WriteString(" ")
	buf.WriteString(r.Level.String())
	buf.WriteString(" ")
	buf.WriteString(r.Message)
	r.Attrs(func(a slog.Attr) bool {
		buf.WriteString(" ")
		buf.WriteString(a.Key)
		buf.WriteString("=")
		buf.WriteString(a.Value.String())
		return true
	})
	buf.WriteString("\n")
	logBuffer = append(logBuffer, buf.String())
	cfg := config.GetConfig()
	if len(logBuffer) > int(cfg.App.LogBufferSize) {
		logBuffer = logBuffer[1:] // Remove oldest
	}
	return nil
}

func (h *bufferHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return &bufferHandler{handler: h.handler.WithAttrs(attrs)}
}

func (h *bufferHandler) WithGroup(name string) slog.Handler {
	return &bufferHandler{handler: h.handler.WithGroup(name)}
}

var (
	logger                      *slog.Logger
	dingoProcessSelectionLogged atomic.Bool
	dingoProcessAmbiguityLogged atomic.Bool
	peerSortSelection           atomic.Int32
)

type secondaryView int

const (
	viewNone secondaryView = iota
	viewPeers
	viewDingo
	viewMithril
)

const (
	viewNoneValue  int32 = int32(viewNone)
	viewPeersValue int32 = int32(viewPeers)
	viewDingoValue int32 = int32(viewDingo)
)

type peerSortMode int32

const (
	peerSortRTT peerSortMode = iota
	peerSortName
	peerSortLocation
)

var (
	activeSecondary       atomic.Int32
	secondaryDefaultSet   atomic.Bool
	mithrilViewAutoActive atomic.Bool
	lastDingoSample       *PromMetrics
	lastDingoSampleAt     time.Time
	lastDingoRateBase     *PromMetrics
	lastDingoRateBaseAt   time.Time
	lastDingoSampleSrc    *PromMetrics
	lastDingoSampleMu     sync.Mutex
	compactDingoPage      atomic.Int32
	peerOverlayActive     atomic.Bool
)

// Global command line flags
var cmdlineFlags struct {
	configFile string
}

// Global tview application and pages
var (
	app   = tview.NewApplication()
	pages = tview.NewPages()
)

// Main viewport - flexible box
var (
	flex           = tview.NewFlex()
	dashboardPages = tview.NewPages()
)

// Our text views
var blockTextView = tview.NewTextView().
	SetDynamicColors(true)

var chainTextView = tview.NewTextView().
	SetDynamicColors(true).
	SetTextColor(tcell.ColorGreen).
	SetChangedFunc(func() {
		// Redraw the screen on a change
		app.Draw()
	})

var connectionTextView = tview.NewTextView().
	SetDynamicColors(true).
	SetTextColor(tcell.ColorGreen).
	SetChangedFunc(func() {
		app.Draw()
	})

var dingoTextView = tview.NewTextView().
	SetDynamicColors(true).
	SetChangedFunc(func() {
		app.Draw()
	})

var dingoConsoleTextView = tview.NewTextView().
	SetDynamicColors(true).
	SetWrap(false).
	SetChangedFunc(func() {
		app.Draw()
	})

var peerOverlayTextView = tview.NewTextView().
	SetDynamicColors(true).
	SetWrap(false).
	SetChangedFunc(func() {
		app.Draw()
	})

var mithrilOverlayTextView = tview.NewTextView().
	SetDynamicColors(true).
	SetWrap(false).
	SetChangedFunc(func() {
		app.Draw()
	})

var coreTextView = tview.NewTextView().
	SetDynamicColors(true).
	SetTextColor(tcell.ColorGreen).
	SetChangedFunc(func() {
		app.Draw()
	})

var footerTextView = tview.NewTextView().
	SetDynamicColors(true).
	SetTextColor(tcell.ColorGreen).
	SetWrap(false)

var headerTextView = tview.NewTextView().
	SetDynamicColors(true).
	SetTextColor(tcell.ColorGreen)

var overviewTextView = tview.NewTextView().
	SetDynamicColors(true).
	SetChangedFunc(func() {
		app.Draw()
	})

var nodeTextView = tview.NewTextView().
	SetDynamicColors(true).
	SetTextColor(tcell.ColorGreen).
	SetChangedFunc(func() {
		app.Draw()
	})

var peerTextView = tview.NewTextView().
	SetDynamicColors(true).
	SetChangedFunc(func() {
		app.Draw()
	})

var resourceTextView = tview.NewTextView().
	SetDynamicColors(true).
	SetTextColor(tcell.ColorGreen).
	SetChangedFunc(func() {
		app.Draw()
	})

// Text strings
var blockText, chainText, coreText, connectionText, dingoConsoleText, dingoText, mithrilOverlayText, nodeText, overviewText, peerOverlayText, peerText, resourceText string

// Metrics variables
var processMetrics *process.Process

// Track our failures
var failCount atomic.Uint32

func getActiveSecondaryView() secondaryView {
	return secondaryView(activeSecondary.Load())
}

func setActiveSecondaryView(view secondaryView) {
	switch view {
	case viewNone:
		activeSecondary.Store(viewNoneValue)
	case viewPeers:
		activeSecondary.Store(viewPeersValue)
	case viewDingo:
		activeSecondary.Store(viewDingoValue)
	case viewMithril:
		activeSecondary.Store(int32(viewMithril))
	}
}

func applyDefaultSecondaryView() {
	if !secondaryDefaultSet.CompareAndSwap(false, true) {
		return
	}

	switch strings.ToLower(strings.TrimSpace(os.Getenv("NVIEW_DEFAULT_VIEW"))) {
	case "peers":
		setActiveSecondaryView(viewPeers)
	case "dingo":
		if getEffectiveNodeBinary() == DINGO_BINARY {
			setActiveSecondaryView(viewDingo)
		} else {
			setActiveSecondaryView(viewNone)
			if logger != nil {
				logger.Debug(
					"ignoring dingo default view for non-dingo node",
					"binary",
					getEffectiveNodeBinary(),
				)
			}
		}
	case "none":
		setActiveSecondaryView(viewNone)
	case "":
		if getEffectiveNodeBinary() == DINGO_BINARY {
			setActiveSecondaryView(viewDingo)
		} else {
			setActiveSecondaryView(viewNone)
		}
	default:
		setActiveSecondaryView(viewNone)
		if logger != nil {
			logger.Debug(
				"ignoring invalid NVIEW_DEFAULT_VIEW",
				"value",
				os.Getenv("NVIEW_DEFAULT_VIEW"),
			)
		}
	}
}

func dashboardShowsPeers() bool {
	return getEffectiveNodeBinary() == DINGO_BINARY || peerOverlayActive.Load()
}

func dashboardShowsDingoSystems() bool {
	return getEffectiveNodeBinary() == DINGO_BINARY
}

func styleDashboardPane(view *tview.TextView, title string, severity uiSeverity) {
	view.SetDynamicColors(true)
	view.SetBorder(true)
	view.SetTitle(uiPanelTitle(title, severity))
	view.SetTitleColor(uiTCellColor(severity))
	view.SetBorderColor(uiTCellColor(severity))
}

func newMithrilOverlayPrimitive(width int) tview.Primitive {
	return tview.NewGrid().
		SetRows(0, 18, 0).
		SetColumns(0, dingoPanelRenderedWidth(width), 0).
		AddItem(mithrilOverlayTextView, 1, 1, 1, 1, 0, 0, false)
}

func newPeerOverlayPrimitive(width, rows int) tview.Primitive {
	height := rows - 4
	if height < 12 {
		height = 12
	}
	if height > 28 {
		height = 28
	}
	return tview.NewGrid().
		SetRows(0, height, 0).
		SetColumns(0, dingoPanelRenderedWidth(width), 0).
		AddItem(peerOverlayTextView, 1, 1, 1, 1, 0, 0, true)
}

func currentNetworkName() string {
	if getEffectiveNodeBinary() == DINGO_BINARY && promMetrics != nil {
		if network := strings.TrimSpace(promMetrics.Network); network != "" {
			return formatNetworkName(network)
		}
	}
	cfg := config.GetConfig()
	network := cfg.Node.Network
	if cfg.App.Network != "" {
		network = cfg.App.Network
	}
	return formatNetworkName(network)
}

func formatNetworkName(network string) string {
	network = strings.TrimSpace(network)
	if network == "" {
		return "unknown"
	}
	return strings.ToUpper(network[:1]) + network[1:]
}

func visualModeName(mode terminalVisualMode) string {
	switch mode {
	case terminalVisualPlain:
		return "plain"
	case terminalVisualUnicode:
		return "unicode"
	case terminalVisualNerd:
		return "nerd"
	default:
		return "unicode"
	}
}

func dashboardHealth() (string, uiSeverity) {
	failures := failCount.Load()
	if failures >= 5 {
		return "degraded", uiSeverityCritical
	}
	if failures > 0 {
		return "retrying", uiSeverityWarn
	}
	if promMetrics == nil {
		return "waiting", uiSeverityMuted
	}
	return "online", uiSeverityOK
}

func getHeaderText() string {
	healthLabel, healthSeverity := dashboardHealth()
	visuals := currentTerminalVisuals()
	return dashboardChromeText(fmt.Sprintf(
		" %s %s  %s  %s  %s  %s",
		uiInlineSection("nview"),
		uiMuted(version.GetVersionString()),
		uiPill("node", getEffectiveNodeName(), uiSeverityNeutral),
		uiPill("net", currentNetworkName(), uiSeverityNeutral),
		uiPill("health", healthLabel, healthSeverity),
		uiPill("visual", visualModeName(visuals.Mode), uiSeverityMuted),
	))
}

func getFooterText() string {
	if peerOverlayActive.Load() {
		return dashboardChromeText(" " + strings.Join([]string{
			uiKey("esc", "Close"),
			uiKey("q", "Quit"),
			uiKey("p", "Close Peers"),
			uiKey("s", "Sort:"+peerSortModeLabel()),
			uiKey("↑/↓", "Scroll"),
			uiKey("r", "Refresh"),
		}, " | "))
	}
	parts := []string{
		uiKey("esc/q", "Quit"),
		uiKey("r", "Refresh"),
		uiKey("p", "Peers"),
	}
	if getEffectiveNodeBinary() == DINGO_BINARY {
		cols, rows := dashboardTerminalSize()
		if dingoConsoleCompact(cols, rows) {
			if compactDingoPageIndex() == 2 {
				parts = append(parts, uiKey("s", "Sort:"+peerSortModeLabel()))
			}
			parts = append(
				parts,
				uiKey(
					fmt.Sprintf("%d/%d", compactDingoPageIndex()+1, compactDingoPageCount),
					"Panel",
				),
			)
			parts = append(parts, uiKey("←/→", "Change"))
		} else {
			parts = append(parts, uiKey("↑/↓", "Scroll"))
		}
	}
	return dashboardChromeText(" " + strings.Join(parts, " | "))
}

func dashboardChromeText(text string) string {
	if getEffectiveNodeBinary() != DINGO_BINARY {
		return text
	}
	width := dashboardContentWidth()
	return dingoPanelMargin(width) + dingoPadTaggedRight(text, dingoPanelRenderedWidth(width))
}

func getMinimalFooterText() string {
	return dashboardChromeText(" " + uiKey("esc/q", "Quit"))
}

func refreshChrome() {
	headerTextView.SetText(getHeaderText())
	footerTextView.SetText(getFooterText())
	dingoConsoleTextView.SetBorder(false)
	dingoConsoleTextView.SetTextColor(tcell.ColorWhite)
	styleDashboardPane(overviewTextView, "Command Deck", overviewPaneSeverity())
	styleDashboardPane(nodeTextView, "Identity", dashboardHealthSeverity())
	styleDashboardPane(resourceTextView, "Runtime", runtimePaneSeverity())
	styleDashboardPane(connectionTextView, "Network", connectionPaneSeverity())
	styleDashboardPane(coreTextView, "Forge", corePaneSeverity())
	styleDashboardPane(chainTextView, "Ledger", chainPaneSeverity())
	styleDashboardPane(blockTextView, activityPanelTitle(), activityPaneSeverity())
	styleDashboardPane(dingoTextView, dingoPanelTitle(), dingoPaneSeverity())
	styleDashboardPane(peerTextView, "Peer Radar", peerPaneSeverity())
	styleDashboardPane(peerOverlayTextView, "Peer Radar", peerPaneSeverity())
	styleDashboardPane(mithrilOverlayTextView, "Mithril Sync", activityPaneSeverity())
	peerOverlayTextView.SetTextColor(tcell.ColorWhite)
	mithrilOverlayTextView.SetTextColor(tcell.ColorWhite)
}

func setTextIfChanged(view *tview.TextView, current *string, next string) {
	if next == "" {
		return
	}
	if next == *current {
		return
	}
	*current = next
	view.Clear()
	view.SetText(next)
}

func refreshDashboardText(ctx context.Context) {
	refreshChrome()
	if getEffectiveNodeBinary() == DINGO_BINARY {
		dashboardPages.SwitchToPage("dingo")
		setTextIfChanged(dingoConsoleTextView, &dingoConsoleText, getDingoConsoleText(ctx))
		updateMithrilView()
		updatePeerOverlay(ctx)
		if scrollPeers {
			scrollPeers = false
			dingoConsoleTextView.ScrollToBeginning()
		}
		return
	}

	dashboardPages.SwitchToPage("panels")
	setTextIfChanged(overviewTextView, &overviewText, getOverviewText(ctx))
	setTextIfChanged(nodeTextView, &nodeText, getNodeText(ctx))
	setTextIfChanged(resourceTextView, &resourceText, getResourceText(ctx))
	setTextIfChanged(connectionTextView, &connectionText, getConnectionText(ctx))
	setTextIfChanged(coreTextView, &coreText, getCoreText(ctx))
	setTextIfChanged(chainTextView, &chainText, getLedgerText(ctx))
	setTextIfChanged(blockTextView, &blockText, getActivityText(ctx))
	setTextIfChanged(dingoTextView, &dingoText, getDingoSystemsText(ctx))
	setTextIfChanged(peerTextView, &peerText, getPeerText(ctx))
	if scrollPeers {
		scrollPeers = false
		peerTextView.ScrollToBeginning()
	}
	updateMithrilView()
	updatePeerOverlay(ctx)
}

func togglePeerOverlay(ctx context.Context) {
	if peerOverlayActive.Load() {
		hidePeerOverlay()
		refreshChrome()
		return
	}
	peerOverlayActive.Store(true)
	if err := filterPeers(ctx); err != nil && logger != nil {
		logger.Debug("peer overlay refresh skipped", "error", err)
	}
	pingPeers(ctx)
	peerOverlayTextView.ScrollToBeginning()
	updatePeerOverlay(ctx)
	refreshChrome()
}

func hidePeerOverlay() {
	peerOverlayActive.Store(false)
	dashboardPages.HidePage(peerOverlayPageName)
}

func updatePeerOverlay(ctx context.Context) {
	if !peerOverlayActive.Load() || isMithrilSyncActive() {
		dashboardPages.HidePage(peerOverlayPageName)
		return
	}
	width := dashboardContentWidth()
	_, rows := dashboardTerminalSize()
	setTextIfChanged(peerOverlayTextView, &peerOverlayText, getPeerText(ctx))
	dashboardPages.AddPage(
		peerOverlayPageName,
		newPeerOverlayPrimitive(width, rows),
		true,
		true,
	)
}

func refreshDashboardNow(ctx context.Context) {
	setRole()
	p2p = getP2P(ctx, processMetrics)
	resetPeers()
	if err := filterPeers(ctx); err != nil && logger != nil {
		logger.Debug("peer refresh skipped", "error", err)
	}
	pingPeers(ctx)
	refreshDashboardText(ctx)
}

func worstSeverity(values ...uiSeverity) uiSeverity {
	worst := uiSeverityMuted
	for _, severity := range values {
		if severity == uiSeverityCritical {
			return uiSeverityCritical
		}
		if severity == uiSeverityWarn {
			worst = uiSeverityWarn
		} else if severity == uiSeverityNeutral && worst == uiSeverityMuted {
			worst = uiSeverityNeutral
		} else if severity == uiSeverityOK && worst == uiSeverityMuted {
			worst = uiSeverityOK
		}
	}
	return worst
}

func overviewPaneSeverity() uiSeverity {
	return worstSeverity(
		dashboardHealthSeverity(),
		chainPaneSeverity(),
		corePaneSeverity(),
		activityPaneSeverity(),
		peerPaneSeverity(),
	)
}

func dashboardHealthSeverity() uiSeverity {
	_, severity := dashboardHealth()
	return severity
}

func chainPaneSeverity() uiSeverity {
	if promMetrics == nil {
		return uiSeverityMuted
	}
	if promMetrics.SlotNum == 0 {
		return uiSeverityMuted
	}
	_, severity := currentTipGap()
	return severity
}

func blockPaneSeverity() uiSeverity {
	if promMetrics == nil {
		return uiSeverityMuted
	}
	if promMetrics.BlockDelay >= 5 || promMetrics.BlocksLate > 0 {
		return uiSeverityCritical
	}
	if promMetrics.BlockDelay >= 3 {
		return uiSeverityWarn
	}
	return uiSeverityOK
}

func activityPaneSeverity() uiSeverity {
	if isMithrilSyncActive() {
		if promMetrics == nil {
			return uiSeverityMuted
		}
		if promMetrics.MithrilSyncErrorsTotal > 0 {
			return uiSeverityCritical
		}
		if promMetrics.MithrilSyncGapBlocks > 0 {
			return uiSeverityWarn
		}
		return uiSeverityNeutral
	}
	return blockPaneSeverity()
}

func corePaneSeverity() uiSeverity {
	if promMetrics == nil || !hasForgeMetrics(promMetrics) {
		return uiSeverityMuted
	}
	if getEffectiveNodeBinary() == DINGO_BINARY && promMetrics.ForgingEnabled == 0 {
		if promMetrics.DidntAdopt > 0 {
			return uiSeverityCritical
		}
		if promMetrics.MissedSlots > 0 {
			return uiSeverityWarn
		}
		return uiSeverityMuted
	}
	if promMetrics.DidntAdopt > 0 {
		return uiSeverityCritical
	}
	if promMetrics.IsLeader != promMetrics.Adopted || promMetrics.MissedSlots > 0 {
		return uiSeverityWarn
	}
	return uiSeverityOK
}

func runtimePaneSeverity() uiSeverity {
	if promMetrics == nil {
		return uiSeverityMuted
	}
	fdUsage := 0.0
	if promMetrics.ProcessMaxFDs > 0 {
		fdUsage = float64(promMetrics.ProcessOpenFDs) / float64(promMetrics.ProcessMaxFDs) * 100
	}
	if fdUsage >= 90 {
		return uiSeverityCritical
	}
	if fdUsage >= 75 {
		return uiSeverityWarn
	}
	return dashboardHealthSeverity()
}

func connectionPaneSeverity() uiSeverity {
	if p2p {
		return uiSeverityOK
	}
	return uiSeverityWarn
}

func dingoPaneSeverity() uiSeverity {
	if !dashboardShowsDingoSystems() {
		return dashboardHealthSeverity()
	}
	if promMetrics == nil {
		return uiSeverityMuted
	}
	if promMetrics.DingoGovernanceDecodeFailures > 0 ||
		promMetrics.DingoForgeSlotClockErr > 0 ||
		promMetrics.EventDeliveryErrors > 0 ||
		promMetrics.EventDeliveryTimeouts > 0 {
		return uiSeverityCritical
	}
	return worstSeverity(
		dingoTipGapSeverity(promMetrics.DingoTipGapSlots),
		dingoTipGapSeverity(promMetrics.DingoForgeTipGapSlots),
	)
}

func peerPaneSeverity() uiSeverity {
	peersFilteredMu.RLock()
	peerCount := len(peersFiltered)
	peersFilteredMu.RUnlock()

	peerStatsMu.Lock()
	rttCount := len(peerStats.RTTresultsSlice)
	cnt0 := peerStats.CNT0
	rttAvg := peerStats.RTTAVG
	inFlight := len(peerStats.InFlight)
	peerStatsMu.Unlock()

	if peerCount == 0 && promMetrics == nil {
		return uiSeverityMuted
	}
	if cnt0 > 0 || rttAvg >= RTTThreshold2 {
		return uiSeverityWarn
	}
	if peerCount > 0 && (rttCount < peerCount || inFlight > 0) {
		return uiSeverityNeutral
	}
	return uiSeverityOK
}

func activityPanelTitle() string {
	if isMithrilSyncActive() {
		return "Mithril Sync"
	}
	return "Propagation"
}

func dingoPanelTitle() string {
	switch getEffectiveNodeBinary() {
	case DINGO_BINARY:
		return "Dingo Diagnostics"
	case AMARU_BINARY:
		return "Amaru Focus"
	default:
		return "Operator Focus"
	}
}

func currentTipGap() (uint64, uiSeverity) {
	if promMetrics == nil || promMetrics.SlotNum == 0 {
		return 0, uiSeverityMuted
	}
	tipRef := getSlotTipRef()
	if tipRef < promMetrics.SlotNum {
		return 0, uiSeverityOK
	}
	gap := tipRef - promMetrics.SlotNum
	return gap, dingoTipGapSeverity(gap)
}

func hasForgeMetrics(metrics *PromMetrics) bool {
	if metrics == nil {
		return false
	}
	if role == "Core" || getEffectiveNodeBinary() == DINGO_BINARY {
		return true
	}
	return metrics.ForgingEnabled > 0 ||
		metrics.BlocksForged > 0 ||
		metrics.IsLeader > 0 ||
		metrics.Adopted > 0 ||
		metrics.DidntAdopt > 0 ||
		metrics.AboutToLead > 0 ||
		metrics.MissedSlots > 0 ||
		metrics.KesPeriod > 0 ||
		metrics.RemainingKesPeriods > 0 ||
		metrics.DingoBlockForgingLatencyN > 0
}

func getLedgerText(ctx context.Context) string {
	return fmt.Sprintf("%s%s", getEpochText(ctx), getChainText(ctx))
}

func getActivityText(ctx context.Context) string {
	if isMithrilSyncActive() {
		return getMithrilStats()
	}
	return getBlockText(ctx)
}

func getDingoSystemsText(ctx context.Context) string {
	if dashboardShowsDingoSystems() {
		return getDingoStats()
	}
	return getOperatorFocusText(ctx)
}

func getOverviewText(ctx context.Context) string {
	select {
	case <-ctx.Done():
		return ""
	default:
	}

	healthLabel, healthSeverity := dashboardHealth()
	var sb strings.Builder
	fmt.Fprintf(&sb, " %s   %s   %s   %s\n",
		uiPill("impl", getEffectiveNodeBinary(), uiSeverityNeutral),
		uiPill("net", currentNetworkName(), uiSeverityNeutral),
		uiPill("role", role, corePaneSeverity()),
		uiPill("health", healthLabel, healthSeverity),
	)
	if promMetrics == nil {
		fmt.Fprintf(&sb, " %s %s\n",
			uiStatusGlyph(uiSeverityMuted),
			uiMuted("waiting for the first Prometheus scrape"),
		)
		return sb.String()
	}

	tipGap, tipSeverity := currentTipGap()
	epochProgress := float64(getEpochProgress())
	fmt.Fprintf(&sb, " %s   %s   %s\n",
		uiKV("Block", uiValue(strconv.FormatUint(promMetrics.BlockNum, 10))),
		uiKV("Slot", uiValue(strconv.FormatUint(promMetrics.SlotNum, 10))),
		uiKV(
			"Tip Gap",
			uiSeverityValue(strconv.FormatUint(tipGap, 10), tipSeverity)+
				uiUnit(" slots"),
		),
	)
	fmt.Fprintf(&sb, " %s   %s   %s\n",
		uiPercentBar("Epoch", epochProgress, mithrilProgressSeverity(epochProgress), 20),
		uiKVAligned(9, "Remaining", uiValue(formatEpochRemaining())),
		uiKVAligned(9, "Peers", peerStateSegmentBar(promMetrics, 12)),
	)
	if getEffectiveNodeBinary() == DINGO_BINARY {
		forgeLabel := "disabled"
		forgeSeverity := uiSeverityMuted
		if promMetrics.ForgingEnabled > 0 {
			forgeLabel = "enabled"
			forgeSeverity = uiSeverityOK
		}
		fmt.Fprintf(&sb, " %s   %s   %s   %s\n",
			uiPill("forge", forgeLabel, forgeSeverity),
			uiKV("Forged", uiValue(strconv.FormatUint(promMetrics.BlocksForged, 10))),
			uiKV("DB", uiValue(formatDingoBytes(dingoDbSize(promMetrics)))),
			uiKV("FD", uiValue(formatFDUsage(promMetrics))),
		)
	}
	return sb.String()
}

func getOperatorFocusText(ctx context.Context) string {
	select {
	case <-ctx.Done():
		return ""
	default:
	}
	if promMetrics == nil {
		return " " + uiMuted("waiting for metrics") + "\n"
	}

	var sb strings.Builder
	sb.WriteString(uiSection("Node Signals"))
	tipGap, tipSeverity := currentTipGap()
	fmt.Fprintf(&sb, " %s   %s\n",
		uiKV("Health", uiSeverityValue(dashboardHealthLabel(), dashboardHealthSeverity())),
		uiKV(
			"Tip Gap",
			uiSeverityValue(strconv.FormatUint(tipGap, 10), tipSeverity)+uiUnit(" slots"),
		),
	)
	fmt.Fprintf(&sb, " %s   %s\n",
		uiKV("Mempool", uiValue(strconv.FormatUint(promMetrics.MempoolTx, 10))),
		uiKV("Forks", uiValue(strconv.FormatUint(promMetrics.Forks, 10))),
	)
	sb.WriteString(uiSection("Block Service"))
	lateSeverity := dingoCounterSeverity(promMetrics.BlocksLate, uiSeverityWarn)
	fmt.Fprintf(&sb, " %s   %s\n",
		uiKV("Served", uiValue(strconv.FormatUint(promMetrics.BlocksServed, 10))),
		uiKV("Late", formatSeverityCount(promMetrics.BlocksLate, lateSeverity)),
	)
	return sb.String()
}

func dashboardHealthLabel() string {
	label, _ := dashboardHealth()
	return label
}

func formatFDUsage(metrics *PromMetrics) string {
	if metrics == nil || metrics.ProcessMaxFDs == 0 {
		if metrics != nil && metrics.ProcessOpenFDs > 0 {
			return strconv.FormatUint(metrics.ProcessOpenFDs, 10)
		}
		return "n/a"
	}
	return fmt.Sprintf(
		"%d/%d",
		metrics.ProcessOpenFDs,
		metrics.ProcessMaxFDs,
	)
}

func formatDingoADAFromLovelace(lovelace uint64) string {
	if lovelace == 0 {
		return "0 ADA"
	}
	ada := float64(lovelace) / 1_000_000
	switch {
	case ada >= 1_000_000_000:
		return fmt.Sprintf("%.2fB ADA", ada/1_000_000_000)
	case ada >= 1_000_000:
		return fmt.Sprintf("%.2fM ADA", ada/1_000_000)
	case ada >= 1_000:
		return fmt.Sprintf("%.2fK ADA", ada/1_000)
	default:
		return fmt.Sprintf("%.0f ADA", ada)
	}
}

func dingoDbSize(metrics *PromMetrics) uint64 {
	if metrics == nil {
		return 0
	}
	if metrics.DingoDbSizeBytes > 0 {
		return metrics.DingoDbSizeBytes
	}
	return metrics.DingoDbBlobSizeBytes + metrics.DingoDbMetadataSizeBytes
}

func peerStateSegmentBar(metrics *PromMetrics, width int) string {
	if metrics == nil {
		return uiSegmentBar(nil, width)
	}
	return uiSegmentBar([]uiSegment{
		{Label: "hot", Value: float64(metrics.PeersHot), Severity: uiSeverityOK},
		{Label: "warm", Value: float64(metrics.PeersWarm), Severity: uiSeverityWarn},
		{Label: "cold", Value: float64(metrics.PeersCold), Severity: uiSeverityMuted},
	}, width)
}

func dashboardContentWidth() int {
	cols, _ := dashboardTerminalSize()
	if cols > 180 {
		return 180
	}
	return cols
}

func dashboardTerminalSize() (int, int) {
	return terminalSizeWithFallback(DashboardWidthDefault)
}

func terminalSizeWithFallback(defaultWidth int) (int, int) {
	if defaultWidth <= 0 {
		defaultWidth = DashboardWidthDefault
	}
	fd := os.Stdout.Fd()
	if fd > math.MaxInt {
		return defaultWidth, DashboardHeightDefault
	}
	cols, rows, err := terminal.GetSize(int(fd)) //nolint:gosec // fd validated above
	if err != nil || cols <= 0 || rows <= 0 {
		return defaultWidth, DashboardHeightDefault
	}
	return cols, rows
}

type dingoMetricCell struct {
	Label         string
	Value         string
	LabelSeverity uiSeverity
	Span          int
}

func dingoPanel(title string, severity uiSeverity, width int, lines []string) string {
	panelWidth := dingoPanelWidth(width)
	innerWidth := dingoPanelInnerWidth(width)
	margin := dingoPanelMargin(width)

	top := dingoPanelTop(title, severity, panelWidth)
	bottom := uiMuted(" " + dingoBorderLeftBottom() + strings.Repeat(dingoBorderHorizontal(), panelWidth-2) + dingoBorderRightBottom())
	var sb strings.Builder
	sb.WriteString("\n")
	sb.WriteString(margin)
	sb.WriteString(top)
	sb.WriteString("\n")
	for _, line := range lines {
		fmt.Fprintf(
			&sb,
			"%s %s %s %s\n",
			margin,
			uiMuted(dingoBorderVertical()),
			dingoPadTaggedRight(line, innerWidth),
			uiMuted(dingoBorderVertical()),
		)
	}
	sb.WriteString(margin)
	sb.WriteString(bottom)
	sb.WriteString("\n")
	return sb.String()
}

func dingoPanelWidth(width int) int {
	panelWidth := width - 2
	if panelWidth < 64 {
		panelWidth = 64
	}
	if panelWidth > 178 {
		panelWidth = 178
	}
	return panelWidth
}

func dingoPanelInnerWidth(width int) int {
	panelWidth := dingoPanelWidth(width)
	innerWidth := panelWidth - 4
	if innerWidth < 1 {
		innerWidth = 1
	}
	return innerWidth
}

func dingoPanelRenderedWidth(width int) int {
	return dingoPanelWidth(width) + 1
}

func dingoPanelMargin(width int) string {
	cols, _ := terminalSizeWithFallback(width)
	renderedWidth := dingoPanelRenderedWidth(width)
	if cols <= renderedWidth {
		return ""
	}
	return strings.Repeat(" ", (cols-renderedWidth)/2)
}

func dingoPanelTop(title string, severity uiSeverity, width int) string {
	title = strings.ToUpper(strings.TrimSpace(title))
	prefix := " " + dingoBorderLeftTop() + dingoBorderHorizontal() + " "
	titleText := uiSeverityValue(title, severity)
	suffixPrefix := " "
	used := tview.TaggedStringWidth(prefix) +
		tview.TaggedStringWidth(titleText) +
		tview.TaggedStringWidth(suffixPrefix) +
		1
	fill := width + 1 - used
	if fill < 1 {
		fill = 1
	}
	return uiMuted(prefix) +
		titleText +
		uiMuted(suffixPrefix+strings.Repeat(dingoBorderHorizontal(), fill)+dingoBorderRightTop())
}

func dingoBorderLeftTop() string {
	if currentTerminalVisuals().Mode == terminalVisualPlain {
		return "+"
	}
	return "┌"
}

func dingoBorderRightTop() string {
	if currentTerminalVisuals().Mode == terminalVisualPlain {
		return "+"
	}
	return "┐"
}

func dingoBorderLeftBottom() string {
	if currentTerminalVisuals().Mode == terminalVisualPlain {
		return "+"
	}
	return "└"
}

func dingoBorderRightBottom() string {
	if currentTerminalVisuals().Mode == terminalVisualPlain {
		return "+"
	}
	return "┘"
}

func dingoBorderHorizontal() string {
	if currentTerminalVisuals().Mode == terminalVisualPlain {
		return "-"
	}
	return "─"
}

func dingoBorderVertical() string {
	if currentTerminalVisuals().Mode == terminalVisualPlain {
		return "|"
	}
	return "│"
}

func dingoPadTaggedRight(text string, width int) string {
	padding := width - tview.TaggedStringWidth(text)
	if padding <= 0 {
		return text
	}
	return text + strings.Repeat(" ", padding)
}

func dingoMetricRow(innerWidth int, cells ...dingoMetricCell) string {
	return dingoMetricRowColumns(innerWidth, 4, cells...)
}

func dingoMetricRowColumns(innerWidth, columns int, cells ...dingoMetricCell) string {
	if len(cells) == 0 {
		return ""
	}
	if columns <= 0 {
		columns = 1
	}
	separator := dingoMetricSeparator()
	separatorWidth := tview.TaggedStringWidth(separator)
	available := innerWidth - separatorWidth*(columns-1) - 1
	if available < columns {
		available = columns
	}
	baseWidth := available / columns
	remainder := available % columns
	columnWidths := make([]int, columns)
	for idx := range columnWidths {
		columnWidths[idx] = baseWidth
		if idx < remainder {
			columnWidths[idx]++
		}
	}

	parts := make([]string, 0, len(cells))
	usedColumns := 0
	for _, cell := range cells {
		span := cell.Span
		if span <= 0 {
			if len(cells) == 1 {
				span = columns
			} else {
				span = 1
			}
		}
		if remaining := columns - usedColumns; span > remaining {
			span = remaining
		}
		if span <= 0 {
			break
		}
		cellWidth := 0
		for idx := 0; idx < span; idx++ {
			cellWidth += columnWidths[usedColumns+idx]
		}
		cellWidth += separatorWidth * (span - 1)
		parts = append(parts, dingoMetricCellText(cell, cellWidth))
		usedColumns += span
	}
	for usedColumns < columns {
		parts = append(parts, dingoMetricCellText(dingoMetricCell{}, columnWidths[usedColumns]))
		usedColumns++
	}
	return strings.Join(parts, uiMuted(separator))
}

func dingoMetricSeparator() string {
	if currentTerminalVisuals().Mode == terminalVisualPlain {
		return " | "
	}
	return " │ "
}

func dingoMetricCellText(cell dingoMetricCell, width int) string {
	if cell.Label == "" && cell.Value == "" {
		return strings.Repeat(" ", width)
	}
	labelWidth := 12
	if width < 26 {
		labelWidth = 10
	}
	if width < 22 {
		labelWidth = 8
	}
	if width < 18 {
		labelWidth = 6
	}
	label := cell.Label
	if len(label) > labelWidth {
		label = shortenString(label, labelWidth)
	}
	labelSeverity := cell.LabelSeverity
	if labelSeverity == 0 {
		labelSeverity = uiSeverityNeutral
	}
	text := uiLabelSeverity(fmt.Sprintf("%-*s", labelWidth, label), labelSeverity) +
		" " +
		cell.Value
	return dingoPadTaggedRight(text, width)
}

func dingoMetric(label, value string) dingoMetricCell {
	return dingoMetricCell{
		Label:         label,
		Value:         value,
		LabelSeverity: uiSeverityNeutral,
	}
}

func dingoMetricStyled(label, value string, severity uiSeverity) dingoMetricCell {
	return dingoMetricCell{
		Label:         label,
		Value:         value,
		LabelSeverity: severity,
	}
}

func dingoMetricSpan(label, value string, span int) dingoMetricCell {
	cell := dingoMetric(label, value)
	cell.Span = span
	return cell
}

func dingoMetricStyledSpan(label, value string, severity uiSeverity, span int) dingoMetricCell {
	cell := dingoMetricStyled(label, value, severity)
	cell.Span = span
	return cell
}

func dingoConsoleTipGap(metrics *PromMetrics) (uint64, uiSeverity) {
	if metrics == nil || metrics.SlotNum == 0 {
		return 0, uiSeverityMuted
	}
	if metrics.DingoTipGapSlots > 0 ||
		metrics.DingoShelleyStartTime > 0 ||
		metrics.DingoEpochLengthSlots > 0 {
		return metrics.DingoTipGapSlots, dingoTipGapSeverity(metrics.DingoTipGapSlots)
	}
	return currentTipGap()
}

func formatEpochSwitchTime() string {
	switchTime, ok := currentEpochSwitchTime()
	if !ok {
		return "n/a"
	}
	return switchTime.Format("2006-01-02 15:04:05 MST")
}

func formatEpochSwitchClock() string {
	switchTime, ok := currentEpochSwitchTime()
	if !ok {
		return "n/a"
	}
	return switchTime.Format("15:04:05")
}

func getDingoConsoleText(ctx context.Context) string {
	select {
	case <-ctx.Done():
		return ""
	default:
	}

	cols, rows := dashboardTerminalSize()
	width := dashboardContentWidth()
	if promMetrics == nil {
		return fmt.Sprintf(
			"\n %s  %s\n",
			uiInlineSection("Dingo"),
			uiMuted("waiting for the first Prometheus scrape"),
		)
	}

	m := promMetrics
	if dingoConsoleCompact(cols, rows) {
		return getDingoConsoleCompactText(m, width)
	}

	var sb strings.Builder
	sb.WriteString(dingoConsoleHero(m, width))
	sb.WriteString(dingoConsoleChainLane(m, width))
	sb.WriteString(dingoConsolePeerLane(m, width))
	sb.WriteString(dingoConsoleFlowLane(m, width))
	sb.WriteString(dingoConsoleSystemsBand(m, width))
	return sb.String()
}

func dingoConsoleCompact(cols, rows int) bool {
	return cols <= 92 || rows <= 28
}

const compactDingoPageCount = 4

func compactDingoPageIndex() int {
	if compactDingoPageCount <= 0 {
		return 0
	}
	idx := int(compactDingoPage.Load()) % compactDingoPageCount
	if idx < 0 {
		idx += compactDingoPageCount
	}
	return idx
}

func shiftCompactDingoPage(delta int) {
	if delta == 0 {
		return
	}
	next := (compactDingoPageIndex() + delta) % compactDingoPageCount
	if next < 0 {
		next += compactDingoPageCount
	}
	compactDingoPage.Store(int32(next))
}

func getDingoConsoleCompactText(metrics *PromMetrics, width int) string {
	var sb strings.Builder
	switch compactDingoPageIndex() {
	case 1:
		sb.WriteString(dingoConsoleChainCompact(metrics, width))
		sb.WriteString(dingoConsoleFlowCompact(metrics, width))
	case 2:
		sb.WriteString(dingoConsolePeerCompact(metrics, width))
	case 3:
		sb.WriteString(dingoConsoleOperationsCompact(metrics, width))
	default:
		sb.WriteString(dingoConsoleDashboardCompact(metrics, width))
	}
	return sb.String()
}

func dingoConsoleHero(metrics *PromMetrics, width int) string {
	healthLabel, healthSeverity := dashboardHealth()
	nodeVersion, nodeRevision := currentNodeVersionInfo()
	if nodeRevision == "" {
		nodeRevision = "n/a"
	}

	innerWidth := dingoPanelInnerWidth(width)
	return dingoPanel(
		"operator control",
		healthSeverity,
		width,
		[]string{
			dingoMetricRow(
				innerWidth,
				dingoMetric("Node", uiValue("Dingo")),
				dingoMetric("Network", uiValue(currentNetworkName())),
				dingoMetric("Role", uiValue(role)),
				dingoMetricStyled("Health", uiSeverityValue(healthLabel, healthSeverity), healthSeverity),
			),
			dingoMetricRow(
				innerWidth,
				dingoMetric("Version", uiValue(shortenString(nodeVersion, 18))),
				dingoMetric("Commit", uiValue(shortenString(nodeRevision, 12))),
				dingoMetric(
					"Peers",
					uiValue(strconv.FormatUint(metrics.PeersActive, 10))+
						uiUnit("/")+
						uiValue(strconv.FormatUint(metrics.PeersEstablished, 10)),
				),
				dingoMetric("Remaining", uiValue(formatEpochRemaining())),
			),
		},
	)
}

func dingoConsoleDashboardCompact(metrics *PromMetrics, width int) string {
	healthLabel, healthSeverity := dashboardHealth()
	tipGap, tipSeverity := dingoConsoleTipGap(metrics)
	epoch := metrics.EpochNum
	if epoch == 0 {
		epoch = currentEpoch
	}
	forgeSeverity := uiSeverityMuted
	forgeLabel := "off"
	if metrics.ForgingEnabled > 0 {
		forgeSeverity = uiSeverityOK
		forgeLabel = "on"
	} else if metrics.BlocksForged > 0 || metrics.DingoBlockForgingLatencyN > 0 {
		forgeSeverity = uiSeverityNeutral
	}
	peerValue := uiValue(strconv.FormatUint(metrics.PeersActive, 10)) +
		uiUnit("/") +
		uiValue(strconv.FormatUint(metrics.PeersEstablished, 10)) +
		uiUnit(" active/est")
	mempoolValue := uiValue(strconv.FormatUint(metrics.MempoolTx, 10)) +
		uiUnit("/") +
		uiValue(formatDingoBytes(metrics.MempoolBytes))

	innerWidth := dingoPanelInnerWidth(width)
	return dingoPanel(
		"dashboard",
		healthSeverity,
		width,
		[]string{
			dingoMetricRowColumns(
				innerWidth,
				2,
				dingoMetric("Node", uiValue("Dingo")),
				dingoMetric("Network", uiValue(currentNetworkName())),
			),
			dingoMetricRowColumns(
				innerWidth,
				2,
				dingoMetricStyled("Health", uiSeverityValue(healthLabel, healthSeverity), healthSeverity),
				dingoMetric("Role", uiValue(role)),
			),
			dingoMetricRowColumns(
				innerWidth,
				2,
				dingoMetric("Block", uiValue(strconv.FormatUint(metrics.BlockNum, 10))),
				dingoMetric("Slot", uiValue(strconv.FormatUint(metrics.SlotNum, 10))),
			),
			dingoMetricRowColumns(
				innerWidth,
				2,
				dingoMetricStyled(
					"Tip Gap",
					uiSeverityValue(strconv.FormatUint(tipGap, 10), tipSeverity)+uiUnit(" slots"),
					tipSeverity,
				),
				dingoMetric("Remaining", uiValue(formatEpochRemaining())),
			),
			dingoMetricRowColumns(
				innerWidth,
				2,
				dingoMetric("Epoch", uiValue(strconv.FormatUint(epoch, 10))),
				dingoMetric("Peers", peerValue),
			),
			dingoMetricRowColumns(
				innerWidth,
				2,
				dingoMetricStyled("Forge", uiSeverityValue(forgeLabel, forgeSeverity), forgeSeverity),
				dingoMetric("Mempool", mempoolValue),
			),
			dingoMetricRowColumns(
				innerWidth,
				2,
				dingoMetric("Heap", uiValue(formatMemoryBytes(metrics.GoHeapInuse))),
				dingoMetric("DB", uiValue(formatDingoBytes(dingoDbSize(metrics)))),
			),
		},
	)
}

func dingoConsoleSystemsBand(metrics *PromMetrics, width int) string {
	forgeDisabled := metrics.ForgingEnabled == 0
	forgeSeverity := uiSeverityMuted
	forgeLabel := "disabled"
	if metrics.ForgingEnabled > 0 {
		forgeSeverity = uiSeverityOK
		forgeLabel = "enabled"
	} else if metrics.BlocksForged > 0 || metrics.DingoBlockForgingLatencyN > 0 {
		forgeSeverity = uiSeverityNeutral
	}
	adoptedSeverity, invalidSeverity, missedSeverity := dingoForgeCounterSeverities(metrics)

	innerWidth := dingoPanelInnerWidth(width)
	utxoCache, utxoSeverity := dingoConsoleCacheValue(metrics.DingoCacheUtxoHotHits, metrics.DingoCacheUtxoHotMiss)
	blockCache, blockSeverity := dingoConsoleCacheValue(metrics.DingoCacheBlockLruHits, metrics.DingoCacheBlockLruMiss)
	lines := make([]string, 0, 9)
	lines = append(
		lines,
		dingoMetricRow(
			innerWidth,
			dingoMetric("DB", uiValue(formatDingoBytes(dingoDbSize(metrics)))),
			dingoMetric("Heap", uiValue(formatMemoryBytes(metrics.GoHeapInuse))),
			dingoMetric("FD", uiValue(formatFDUsage(metrics))),
			dingoMetric("Goroutines", uiValue(strconv.FormatUint(metrics.GoRoutines, 10))),
		),
		dingoMetricRow(
			innerWidth,
			dingoMetricStyledSpan("UTxO Cache", utxoCache, utxoSeverity, 2),
			dingoMetricStyledSpan("Block Cache", blockCache, blockSeverity, 2),
		),
		dingoMetricRow(
			innerWidth,
			dingoMetric("Cold CBOR", uiValue(strconv.FormatUint(metrics.DingoCacheColdExtract, 10))),
			dingoMetric("Chain Cache", uiValue(strconv.FormatUint(metrics.DingoChainCachedBlocks, 10))),
		),
		dingoMetricRow(
			innerWidth,
			dingoMetricStyled("Forge", uiSeverityValue(forgeLabel, forgeSeverity), forgeSeverity),
			dingoMetricStyled("Forged", uiSeverityValue(strconv.FormatUint(metrics.BlocksForged, 10), forgeSeverity), forgeSeverity),
			dingoMetricStyled("Latency", uiSeverityValue(formatDingoProtocolLatency(metrics.DingoBlockForgingLatencyS, metrics.DingoBlockForgingLatencyN), forgeSeverity), forgeSeverity),
			dingoMetricStyled("Missed", uiSeverityValue(strconv.FormatUint(metrics.MissedSlots, 10), missedSeverity), missedSeverity),
		),
		dingoMetricRow(
			innerWidth,
			dingoMetricStyled("Adopted", formatSeverityCount(metrics.Adopted, adoptedSeverity), adoptedSeverity),
			dingoMetricStyled("Invalid", formatSeverityCount(metrics.DidntAdopt, invalidSeverity), invalidSeverity),
			dingoMetricStyled("Leader", uiSeverityValue(strconv.FormatUint(metrics.IsLeader, 10), forgeSeverity), forgeSeverity),
		),
	)
	lines = append(lines, dingoConsoleSignalRows(metrics, forgeDisabled, innerWidth)...)
	lines = append(lines, dingoConsoleLeiosRows(metrics, innerWidth)...)
	return dingoPanel(
		"dingo systems",
		worstSeverity(corePaneSeverity(), dingoPaneSeverity(), runtimePaneSeverity()),
		width,
		lines,
	)
}

func dingoForgeCounterSeverities(metrics *PromMetrics) (uiSeverity, uiSeverity, uiSeverity) {
	forgeDisabled := metrics.ForgingEnabled == 0
	adoptedSeverity := uiSeverityOK
	if forgeDisabled && metrics.Adopted == 0 {
		adoptedSeverity = uiSeverityMuted
	}
	if metrics.IsLeader != metrics.Adopted {
		adoptedSeverity = uiSeverityWarn
	}
	invalidSeverity := uiSeverityOK
	if forgeDisabled && metrics.DidntAdopt == 0 {
		invalidSeverity = uiSeverityMuted
	}
	if metrics.DidntAdopt > 0 {
		invalidSeverity = uiSeverityCritical
	}
	missedSeverity := uiSeverityOK
	if metrics.MissedSlots > 0 {
		missedSeverity = uiSeverityWarn
	} else if forgeDisabled {
		missedSeverity = uiSeverityMuted
	}
	return adoptedSeverity, invalidSeverity, missedSeverity
}

func dingoConsoleCacheValue(hits, misses uint64) (string, uiSeverity) {
	percent, ok := dingoLifetimeHitRatio(hits, misses)
	severity := dingoCacheSeverity(percent, ok)
	if !ok {
		return uiMuted("n/a") + " " + uiProgressBar(0, 10, severity), severity
	}
	return uiSeverityValue(fmt.Sprintf("%5.1f%%", percent), severity) +
		" " +
		uiProgressBar(percent, 10, severity), severity
}

func dingoConsoleSignalRows(
	metrics *PromMetrics,
	forgeDisabled bool,
	innerWidth int,
) []string {
	cells := make([]dingoMetricCell, 0, 5)
	addSignal := func(label string, value uint64, severity uiSeverity) {
		if value == 0 {
			return
		}
		cells = append(cells, dingoMetricStyled(label, formatSeverityCount(value, severity), severity))
	}
	addSignal("Clock", metrics.DingoSlotClockFallback, uiSeverityWarn)
	addSignal("Forge Err", metrics.DingoForgeSlotClockErr, uiSeverityWarn)
	addSignal("Sync Skip", metrics.DingoForgeSyncSkip, uiSeverityWarn)
	addSignal("Gov", metrics.DingoGovernanceDecodeFailures, uiSeverityCritical)
	addSignal("Stake Fail", metrics.DingoStakeSnapshotFailure, uiSeverityCritical)
	if len(cells) == 0 {
		status := uiMuted("quiet")
		statusSeverity := uiSeverityMuted
		if !forgeDisabled || metrics.DingoStakeSnapshotSuccess > 0 {
			status = uiSeverityValue("quiet", uiSeverityOK)
			statusSeverity = uiSeverityOK
		}
		stake := uiMuted("inactive")
		stakeSeverity := uiSeverityMuted
		if metrics.DingoStakeSnapshotPoolCount > 0 || metrics.DingoStakeSnapshotActiveStake > 0 {
			stakeSeverity = uiSeverityOK
			stake = uiSeverityValue(
				strconv.FormatUint(metrics.DingoStakeSnapshotPoolCount, 10),
				stakeSeverity,
			) + uiUnit(" pools")
		}
		return []string{
			dingoMetricRow(
				innerWidth,
				dingoMetricStyled("Signals", status, statusSeverity),
				dingoMetricStyled("Stake", stake, stakeSeverity),
			),
		}
	}

	rows := make([]string, 0, (len(cells)+2)/3)
	for len(cells) > 0 {
		n := len(cells)
		if n > 3 {
			n = 3
		}
		rows = append(rows, dingoMetricRow(innerWidth, cells[:n]...))
		cells = cells[n:]
	}
	return rows
}

func dingoConsoleLeiosRows(metrics *PromMetrics, innerWidth int) []string {
	if !hasLeiosMetrics(metrics) {
		return []string{
			dingoMetricRow(
				innerWidth,
				dingoMetricStyled("Leios", uiMuted("not exposed by this metrics endpoint"), uiSeverityMuted),
			),
		}
	}

	keys := make([]string, 0, len(metrics.LeiosMetrics))
	for key := range metrics.LeiosMetrics {
		keys = append(keys, key)
	}
	slices.Sort(keys)
	if len(keys) > 4 {
		keys = keys[:4]
	}

	cells := make([]dingoMetricCell, 0, 1+len(keys))
	cells = append(cells,
		dingoMetricStyled("Leios", uiSeverityValue("exposed", uiSeverityOK), uiSeverityOK),
	)
	for _, key := range keys {
		cells = append(
			cells,
			dingoMetric(
				shortenString(formatLeiosMetricName(key), 12),
				uiValue(formatLeiosMetricValue(metrics.LeiosMetrics[key])),
			),
		)
	}

	rows := make([]string, 0, (len(cells)+2)/3)
	for len(cells) > 0 {
		n := len(cells)
		if n > 3 {
			n = 3
		}
		rows = append(rows, dingoMetricRow(innerWidth, cells[:n]...))
		cells = cells[n:]
	}
	return rows
}

func dingoConsoleMithrilProgressLines(
	metrics *PromMetrics,
	innerWidth,
	barWidth int,
	includeBytes bool,
) []string {
	dlPct := derivedProgressPercent(
		metrics.MithrilSyncDownloadBytes,
		metrics.MithrilSyncDownloadTotalBytes,
		metrics.MithrilSyncDownloadPercent,
	)
	ledgerPct := derivedProgressPercent(
		metrics.MithrilSyncLedgerImportCurrent,
		metrics.MithrilSyncLedgerImportTotal,
		metrics.MithrilSyncLedgerImportPercent,
	)
	errSeverity := dingoCounterSeverity(metrics.MithrilSyncErrorsTotal, uiSeverityCritical)
	dlSeverity := mithrilProgressSeverity(dlPct)
	ledgerSeverity := mithrilProgressSeverity(ledgerPct)

	lines := make([]string, 0, 3)
	lines = append(lines,
		dingoMetricRow(
			innerWidth,
			dingoMetric("Phase", uiSeverityValue(mithrilPhaseName(metrics), uiSeverityNeutral)),
			dingoMetric("Snapshot", uiValue(formatDingoBytes(metrics.MithrilSyncSnapshotSize))),
			dingoMetric("Errors", formatSeverityCount(metrics.MithrilSyncErrorsTotal, errSeverity)),
		),
	)

	downloadCells := []dingoMetricCell{
		dingoMetricSpan(
			"Download",
			uiSeverityValue(fmt.Sprintf("%5.1f%%", dlPct), dlSeverity)+
				" "+
				uiProgressBar(dlPct, barWidth, dlSeverity),
			3,
		),
	}
	if includeBytes {
		downloadCells = append(
			downloadCells,
			dingoMetric(
				"Bytes",
				uiValue(formatDingoBytes(metrics.MithrilSyncDownloadBytes))+
					uiUnit("/")+
					uiValue(formatDingoBytes(metrics.MithrilSyncDownloadTotalBytes)),
			),
		)
	}
	lines = append(lines, dingoMetricRow(innerWidth, downloadCells...))

	ledgerCells := []dingoMetricCell{
		dingoMetricSpan(
			"Ledger",
			uiSeverityValue(fmt.Sprintf("%5.1f%%", ledgerPct), ledgerSeverity)+
				" "+
				uiProgressBar(ledgerPct, barWidth, ledgerSeverity),
			3,
		),
	}
	if includeBytes {
		ledgerCells = append(
			ledgerCells,
			dingoMetric(
				"Items",
				uiValue(strconv.FormatUint(metrics.MithrilSyncLedgerImportCurrent, 10))+
					uiUnit("/")+
					uiValue(strconv.FormatUint(metrics.MithrilSyncLedgerImportTotal, 10)),
			),
		)
	}
	lines = append(lines, dingoMetricRow(innerWidth, ledgerCells...))
	return lines
}

func dingoConsoleChainLane(metrics *PromMetrics, width int) string {
	tipGap, tipSeverity := dingoConsoleTipGap(metrics)
	epoch := metrics.EpochNum
	if epoch == 0 {
		epoch = currentEpoch
	}
	mempoolKBytes := metrics.MempoolBytes / 1024
	epochProgress := float64(getEpochProgress())
	progressSeverity := mithrilProgressSeverity(epochProgress)
	innerWidth := dingoPanelInnerWidth(width)

	return dingoPanel(
		"chain timeline",
		chainPaneSeverity(),
		width,
		[]string{
			dingoMetricRow(
				innerWidth,
				dingoMetric(
					"Epoch",
					uiSeverityValue(fmt.Sprintf("%5.1f%%", epochProgress), progressSeverity)+
						" "+
						uiProgressBar(epochProgress, 52, progressSeverity),
				),
			),
			dingoMetricRow(
				innerWidth,
				dingoMetric("Block", uiValue(strconv.FormatUint(metrics.BlockNum, 10))),
				dingoMetric("Slot", uiValue(strconv.FormatUint(metrics.SlotNum, 10))),
				dingoMetricStyled(
					"Tip Gap",
					uiSeverityValue(strconv.FormatUint(tipGap, 10), tipSeverity)+uiUnit(" slots"),
					tipSeverity,
				),
			),
			dingoMetricRow(
				innerWidth,
				dingoMetric("Epoch No.", uiValue(strconv.FormatUint(epoch, 10))),
				dingoMetric("Remaining", uiValue(formatEpochRemaining())),
				dingoMetricSpan("Switch", uiValue(formatEpochSwitchTime()), 2),
			),
			dingoMetricRow(
				innerWidth,
				dingoMetric("Slot/Epoch", uiValue(strconv.FormatUint(metrics.SlotInEpoch, 10))),
				dingoMetric(
					"Mempool",
					uiValue(strconv.FormatUint(metrics.MempoolTx, 10))+
						uiUnit(" tx / ")+
						uiValue(strconv.FormatUint(mempoolKBytes, 10))+
						uiUnit("K"),
				),
				dingoMetric("Forks", uiValue(strconv.FormatUint(metrics.Forks, 10))),
				dingoMetric("Density", uiValue(fmt.Sprintf("%3.5f%%", metrics.Density*100))),
			),
		},
	)
}

func dingoConsoleChainCompact(metrics *PromMetrics, width int) string {
	tipGap, tipSeverity := dingoConsoleTipGap(metrics)
	epoch := metrics.EpochNum
	if epoch == 0 {
		epoch = currentEpoch
	}
	mempoolKBytes := metrics.MempoolBytes / 1024
	innerWidth := dingoPanelInnerWidth(width)
	return dingoPanel(
		"chain",
		chainPaneSeverity(),
		width,
		[]string{
			dingoMetricRowColumns(
				innerWidth,
				2,
				dingoMetric("Block", uiValue(strconv.FormatUint(metrics.BlockNum, 10))),
				dingoMetric("Slot", uiValue(strconv.FormatUint(metrics.SlotNum, 10))),
			),
			dingoMetricRowColumns(
				innerWidth,
				2,
				dingoMetricStyled(
					"Tip Gap",
					uiSeverityValue(strconv.FormatUint(tipGap, 10), tipSeverity)+uiUnit(" slots"),
					tipSeverity,
				),
				dingoMetric("Epoch", uiValue(strconv.FormatUint(epoch, 10))),
			),
			dingoMetricRowColumns(
				innerWidth,
				2,
				dingoMetric("Remain", uiValue(formatEpochRemaining())),
				dingoMetric(
					"Mempool",
					uiValue(strconv.FormatUint(metrics.MempoolTx, 10))+
						uiUnit("/")+
						uiValue(strconv.FormatUint(mempoolKBytes, 10))+
						uiUnit("K"),
				),
			),
			dingoMetricRowColumns(
				innerWidth,
				2,
				dingoMetric("Forks", uiValue(strconv.FormatUint(metrics.Forks, 10))),
				dingoMetric("Density", uiValue(fmt.Sprintf("%3.3f%%", metrics.Density*100))),
			),
		},
	)
}

func dingoConsolePeerCompact(metrics *PromMetrics, width int) string {
	peersFilteredMu.RLock()
	peerCount := len(peersFiltered)
	peersFilteredMu.RUnlock()

	peerStatsMu.Lock()
	rttCount := len(peerStats.RTTresultsSlice)
	cnt0 := peerStats.CNT0
	cnt1 := peerStats.CNT1
	cnt2 := peerStats.CNT2
	cnt3 := peerStats.CNT3
	cnt4 := peerStats.CNT4
	pct1 := peerStats.PCT1
	pct2 := peerStats.PCT2
	pct3 := peerStats.PCT3
	pct4 := peerStats.PCT4
	rttAvg := peerStats.RTTAVG
	inFlight := len(peerStats.InFlight)
	peers := slices.Clone(peerStats.RTTresultsSlice)
	peerStatsMu.Unlock()

	scanSeverity := uiSeverityMuted
	if peerCount > 0 && rttCount < peerCount {
		scanSeverity = uiSeverityNeutral
	} else if peerCount > 0 {
		scanSeverity = uiSeverityOK
	}
	avgSeverity := uiSeverityMuted
	if rttCount > 0 {
		avgSeverity = uiSeverityOK
		if rttAvg >= RTTThreshold2 {
			avgSeverity = uiSeverityCritical
		} else if rttAvg >= RTTThreshold1 {
			avgSeverity = uiSeverityWarn
		}
	}

	innerWidth := dingoPanelInnerWidth(width)
	lines := make([]string, 0, 10+len(peers))
	lines = append(
		lines,
		dingoMetricRowColumns(
			innerWidth,
			2,
			dingoMetric("Known", uiValue(strconv.FormatUint(metrics.PeersKnown, 10))),
			dingoMetric("Active", uiValue(strconv.FormatUint(metrics.PeersActive, 10))),
		),
		dingoMetricRowColumns(
			innerWidth,
			2,
			dingoMetric(
				"Hot/Warm",
				uiValue(strconv.FormatUint(metrics.PeersHot, 10))+
					uiUnit("/")+
					uiValue(strconv.FormatUint(metrics.PeersWarm, 10)),
			),
			dingoMetric("Cold", uiValue(strconv.FormatUint(metrics.PeersCold, 10))),
		),
		dingoMetricRowColumns(
			innerWidth,
			2,
			dingoMetricStyled("RTT", uiSeverityValue(fmt.Sprintf("%d/%d", rttCount, peerCount), scanSeverity), scanSeverity),
			dingoMetric("Probing", uiValue(strconv.Itoa(inFlight))),
		),
		dingoMetricRowColumns(
			innerWidth,
			2,
			dingoMetricStyled("Avg", uiSeverityValue(strconv.Itoa(rttAvg), avgSeverity)+uiUnit(" ms"), avgSeverity),
			dingoMetric("Unknown", formatSeverityIntCount(cnt0, dingoIntCounterSeverity(cnt0, uiSeverityWarn))),
		),
		dingoMetricRowColumns(
			innerWidth,
			2,
			dingoMetricSpan(
				"Latency",
				uiSegmentBar([]uiSegment{
					{Label: "0-50", Value: float64(cnt1), Severity: uiSeverityOK},
					{Label: "50-100", Value: float64(cnt2), Severity: uiSeverityWarn},
					{Label: "100-200", Value: float64(cnt3), Severity: uiSeverityCritical},
					{Label: ">200", Value: float64(cnt4), Severity: uiSeverityCritical},
					{Label: "unknown", Value: float64(cnt0), Severity: uiSeverityMuted},
				}, 28),
				2,
			),
		),
		dingoMetricRowColumns(innerWidth, 2, dingoMetric("0-50ms", peerLatencyBar(cnt1, pct1, uiSeverityOK))),
		dingoMetricRowColumns(innerWidth, 2, dingoMetric("50-100", peerLatencyBar(cnt2, pct2, uiSeverityWarn))),
		dingoMetricRowColumns(innerWidth, 2, dingoMetric("100-200", peerLatencyBar(cnt3, pct3, uiSeverityCritical))),
		dingoMetricRowColumns(innerWidth, 2, dingoMetric(">200ms", peerLatencyBar(cnt4, pct4, uiSeverityCritical))),
		dingoMetricRowColumns(
			innerWidth,
			2,
			dingoMetric("Sort", uiValue(peerSortModeLabel())),
			dingoMetric("Samples", uiValue(strconv.Itoa(rttCount))),
		),
	)
	lines = append(lines, dingoConsoleTopPeerLines(peers, len(peers), innerWidth)...)
	return dingoPanel("peers", peerPaneSeverity(), width, lines)
}

func dingoConsoleOperationsCompact(metrics *PromMetrics, width int) string {
	forgeSeverity := uiSeverityMuted
	forgeLabel := "disabled"
	if metrics.ForgingEnabled > 0 {
		forgeSeverity = uiSeverityOK
		forgeLabel = "enabled"
	} else if metrics.BlocksForged > 0 || metrics.DingoBlockForgingLatencyN > 0 {
		forgeSeverity = uiSeverityNeutral
	}
	_, invalidSeverity, missedSeverity := dingoForgeCounterSeverities(metrics)

	protocolValue := uiMuted("not exposed")
	if hasDingoPropagationMetrics(metrics) {
		protocolValue = uiValue(strconv.FormatUint(metrics.DingoProtocolBlockfetchMessages, 10)) +
			uiUnit(" bf / ") +
			uiValue(strconv.FormatUint(metrics.DingoProtocolChainsyncMessages, 10)) +
			uiUnit(" cs")
	}

	innerWidth := dingoPanelInnerWidth(width)
	return dingoPanel(
		"operations",
		worstSeverity(corePaneSeverity(), activityPaneSeverity(), runtimePaneSeverity()),
		width,
		[]string{
			dingoMetricRowColumns(
				innerWidth,
				2,
				dingoMetricStyled("Forge", uiSeverityValue(forgeLabel, forgeSeverity), forgeSeverity),
				dingoMetricStyled("Forged", uiSeverityValue(strconv.FormatUint(metrics.BlocksForged, 10), forgeSeverity), forgeSeverity),
			),
			dingoMetricRowColumns(
				innerWidth,
				2,
				dingoMetricStyled("Invalid", formatSeverityCount(metrics.DidntAdopt, invalidSeverity), invalidSeverity),
				dingoMetricStyled("Missed", uiSeverityValue(strconv.FormatUint(metrics.MissedSlots, 10), missedSeverity), missedSeverity),
			),
			dingoMetricRowColumns(
				innerWidth,
				2,
				dingoMetricSpan("Protocols", protocolValue, 2),
			),
			dingoMetricRowColumns(
				innerWidth,
				2,
				dingoMetric("DB", uiValue(formatDingoBytes(dingoDbSize(metrics)))),
				dingoMetric("Heap", uiValue(formatMemoryBytes(metrics.GoHeapInuse))),
			),
			dingoMetricRowColumns(
				innerWidth,
				2,
				dingoMetric("FD", uiValue(formatFDUsage(metrics))),
				dingoMetric("Goroutines", uiValue(strconv.FormatUint(metrics.GoRoutines, 10))),
			),
		},
	)
}

func dingoConsolePeerLane(metrics *PromMetrics, width int) string {
	peersFilteredMu.RLock()
	peerCount := len(peersFiltered)
	peersFilteredMu.RUnlock()

	peerStatsMu.Lock()
	rttCount := len(peerStats.RTTresultsSlice)
	cnt0 := peerStats.CNT0
	cnt1 := peerStats.CNT1
	cnt2 := peerStats.CNT2
	cnt3 := peerStats.CNT3
	cnt4 := peerStats.CNT4
	rttAvg := peerStats.RTTAVG
	inFlight := len(peerStats.InFlight)
	peerStatsMu.Unlock()

	innerWidth := dingoPanelInnerWidth(width)
	lines := make([]string, 0, 7)
	lines = append(lines,
		dingoMetricRow(
			innerWidth,
			dingoMetric("Known", uiValue(strconv.FormatUint(metrics.PeersKnown, 10))),
			dingoMetric("Established", uiValue(strconv.FormatUint(metrics.PeersEstablished, 10))),
			dingoMetric("Active", uiValue(strconv.FormatUint(metrics.PeersActive, 10))),
			dingoMetric(
				"Hot/Warm/Cold",
				uiValue(strconv.FormatUint(metrics.PeersHot, 10))+
					uiUnit("/")+
					uiValue(strconv.FormatUint(metrics.PeersWarm, 10))+
					uiUnit("/")+
					uiValue(strconv.FormatUint(metrics.PeersCold, 10)),
			),
		),
	)
	stateMixRow := dingoMetricRow(
		innerWidth,
		dingoMetric("State Mix", peerStateSegmentBar(metrics, 58)),
	)
	if metrics.DingoPeersBySourceLedger > 0 ||
		metrics.DingoPeersBySourceInbound > 0 ||
		metrics.DingoPeersBySourceGossip > 0 {
		lines = append(
			lines,
			dingoMetricRow(
				innerWidth,
				dingoMetric("Ledger", uiValue(strconv.FormatUint(metrics.DingoPeersBySourceLedger, 10))),
				dingoMetric("Inbound", uiValue(strconv.FormatUint(metrics.DingoPeersBySourceInbound, 10))),
				dingoMetric("Gossip", uiValue(strconv.FormatUint(metrics.DingoPeersBySourceGossip, 10))),
			),
			stateMixRow,
			dingoMetricRow(
				innerWidth,
				dingoMetric(
					"Source Mix",
					uiSegmentBar([]uiSegment{
						{Label: "ledger", Value: float64(metrics.DingoPeersBySourceLedger), Severity: uiSeverityOK},
						{Label: "inbound", Value: float64(metrics.DingoPeersBySourceInbound), Severity: uiSeverityNeutral},
						{Label: "gossip", Value: float64(metrics.DingoPeersBySourceGossip), Severity: uiSeverityWarn},
					}, 58),
				),
			),
		)
	} else {
		lines = append(lines, stateMixRow)
	}
	if metrics.DingoInboundHotQuota > 0 || metrics.DingoInboundWarmTarget > 0 {
		hotUsage := metrics.DingoInboundHotQuotaUsage * 100
		warmUsage := metrics.DingoInboundWarmOccupancy * 100
		hotSeverity := dingoQuotaSeverity(hotUsage)
		warmSeverity := dingoQuotaSeverity(warmUsage)
		lines = append(
			lines,
			dingoMetricRow(
				innerWidth,
				dingoMetric(
					"In Hot",
					uiSeverityValue(fmt.Sprintf("%5.1f%%", hotUsage), hotSeverity)+
						" "+
						uiProgressBar(hotUsage, 14, hotSeverity),
				),
				dingoMetric(
					"In Warm",
					uiSeverityValue(fmt.Sprintf("%5.1f%%", warmUsage), warmSeverity)+
						" "+
						uiProgressBar(warmUsage, 14, warmSeverity),
				),
				dingoMetric("Arrivals", uiValue(strconv.FormatUint(metrics.DingoInboundArrivalsTotal, 10))),
			),
		)
	} else {
		lines = append(
			lines,
			dingoMetricRow(
				innerWidth,
				dingoMetricStyled("Inbound", uiMuted("quota metrics not exposed"), uiSeverityMuted),
			),
		)
	}

	scanSeverity := uiSeverityMuted
	if peerCount > 0 && rttCount < peerCount {
		scanSeverity = uiSeverityNeutral
	} else if peerCount > 0 {
		scanSeverity = uiSeverityOK
	}
	avgSeverity := uiSeverityMuted
	if rttCount > 0 {
		avgSeverity = uiSeverityOK
		if rttAvg >= RTTThreshold2 {
			avgSeverity = uiSeverityCritical
		} else if rttAvg >= RTTThreshold1 {
			avgSeverity = uiSeverityWarn
		}
	}
	lines = append(
		lines,
		dingoMetricRow(
			innerWidth,
			dingoMetricStyled(
				"RTT Scan",
				uiSeverityValue(fmt.Sprintf("%d/%d", rttCount, peerCount), scanSeverity),
				scanSeverity,
			),
			dingoMetric("Probing", uiValue(strconv.Itoa(inFlight))),
			dingoMetricStyled(
				"Average",
				uiSeverityValue(strconv.Itoa(rttAvg), avgSeverity)+uiUnit(" ms"),
				avgSeverity,
			),
		),
		dingoMetricRow(
			innerWidth,
			dingoMetricSpan(
				"Latency",
				uiSegmentBar([]uiSegment{
					{Label: "0-50", Value: float64(cnt1), Severity: uiSeverityOK},
					{Label: "50-100", Value: float64(cnt2), Severity: uiSeverityWarn},
					{Label: "100-200", Value: float64(cnt3), Severity: uiSeverityCritical},
					{Label: ">200", Value: float64(cnt4), Severity: uiSeverityCritical},
					{Label: "unknown", Value: float64(cnt0), Severity: uiSeverityMuted},
				}, 58),
				3,
			),
			dingoMetric("Unknown", formatSeverityIntCount(cnt0, dingoIntCounterSeverity(cnt0, uiSeverityWarn))),
		),
	)
	return dingoPanel("peer fabric", peerPaneSeverity(), width, lines)
}

func dingoConsoleFlowCompact(metrics *PromMetrics, width int) string {
	innerWidth := dingoPanelInnerWidth(width)
	lines := make([]string, 0, 5)
	if hasDingoPropagationMetrics(metrics) {
		blockfetchLatency := formatDingoProtocolLatency(
			metrics.DingoProtocolBlockfetchSum,
			metrics.DingoProtocolBlockfetchCount,
		)
		chainsyncLatency := formatDingoProtocolLatency(
			metrics.DingoProtocolChainsyncSum,
			metrics.DingoProtocolChainsyncCount,
		)
		lines = append(
			lines,
			dingoMetricRowColumns(
				innerWidth,
				2,
				dingoMetric("Blockfetch", uiValue(strconv.FormatUint(metrics.DingoProtocolBlockfetchMessages, 10))),
				dingoMetric("Chainsync", uiValue(strconv.FormatUint(metrics.DingoProtocolChainsyncMessages, 10))),
			),
			dingoMetricRowColumns(
				innerWidth,
				2,
				dingoMetric("BF Avg", uiValue(blockfetchLatency)),
				dingoMetric("CS Avg", uiValue(chainsyncLatency)),
			),
		)
		if metrics.DingoBlockfetchGateDispatched > 0 ||
			metrics.DingoBlockfetchGateSkippedFast > 0 ||
			metrics.DingoBlockfetchGateSkippedPeer > 0 {
			lines = append(
				lines,
				dingoMetricRowColumns(
					innerWidth,
					2,
					dingoMetric("Dispatch", uiValue(strconv.FormatUint(metrics.DingoBlockfetchGateDispatched, 10))),
					dingoMetric("Skip Fast", uiValue(strconv.FormatUint(metrics.DingoBlockfetchGateSkippedFast, 10))),
				),
				dingoMetricRowColumns(
					innerWidth,
					2,
					dingoMetric("No Peer", uiValue(strconv.FormatUint(metrics.DingoBlockfetchGateSkippedPeer, 10))),
				),
			)
		}
	} else {
		lines = append(
			lines,
			dingoMetricRowColumns(
				innerWidth,
				2,
				dingoMetricStyled("Protocols", uiMuted("not exposed"), uiSeverityMuted),
			),
		)
	}

	if metrics.BlockDelay > 0 ||
		metrics.BlocksW1s > 0 ||
		metrics.BlocksW3s > 0 ||
		metrics.BlocksW5s > 0 ||
		metrics.BlocksServed > 0 ||
		metrics.BlocksLate > 0 {
		delaySeverity := uiSeverityOK
		if metrics.BlockDelay >= 5 {
			delaySeverity = uiSeverityCritical
		} else if metrics.BlockDelay >= 3 {
			delaySeverity = uiSeverityWarn
		}
		lateSeverity := dingoCounterSeverity(metrics.BlocksLate, uiSeverityWarn)
		lines = append(
			lines,
			dingoMetricRowColumns(
				innerWidth,
				2,
				dingoMetricStyled("Last Fetch", uiSeverityValue(fmt.Sprintf("%.2fs", metrics.BlockDelay), delaySeverity), delaySeverity),
				dingoMetric("Late >5s", formatSeverityCount(metrics.BlocksLate, lateSeverity)),
			),
		)
	}
	return dingoPanel("protocol flow", activityPaneSeverity(), width, lines)
}

func dingoConsoleFlowLane(metrics *PromMetrics, width int) string {
	innerWidth := dingoPanelInnerWidth(width)
	lines := make([]string, 0, 4)
	if hasDingoPropagationMetrics(metrics) {
		blockfetchLatency := formatDingoProtocolLatency(
			metrics.DingoProtocolBlockfetchSum,
			metrics.DingoProtocolBlockfetchCount,
		)
		chainsyncLatency := formatDingoProtocolLatency(
			metrics.DingoProtocolChainsyncSum,
			metrics.DingoProtocolChainsyncCount,
		)
		lines = append(
			lines,
			dingoMetricRow(
				innerWidth,
				dingoMetric(
					"Traffic",
					uiSegmentBar([]uiSegment{
						{Label: "blockfetch", Value: float64(metrics.DingoProtocolBlockfetchMessages), Severity: uiSeverityOK},
						{Label: "chainsync", Value: float64(metrics.DingoProtocolChainsyncMessages), Severity: uiSeverityNeutral},
						{Label: "keepalive", Value: float64(metrics.DingoProtocolKeepaliveMessages), Severity: uiSeverityWarn},
						{Label: "txsubmit", Value: float64(metrics.DingoProtocolTxSubmitMessages), Severity: uiSeverityMuted},
					}, 58),
				),
			),
			dingoMetricRow(
				innerWidth,
				dingoMetric("Blockfetch", uiValue(strconv.FormatUint(metrics.DingoProtocolBlockfetchMessages, 10))),
				dingoMetric("Chainsync", uiValue(strconv.FormatUint(metrics.DingoProtocolChainsyncMessages, 10))),
				dingoMetric("Keepalive", uiValue(strconv.FormatUint(metrics.DingoProtocolKeepaliveMessages, 10))),
				dingoMetric("TxSubmit", uiValue(strconv.FormatUint(metrics.DingoProtocolTxSubmitMessages, 10))),
			),
			dingoMetricRow(
				innerWidth,
				dingoMetric("BF Avg", uiValue(blockfetchLatency)),
				dingoMetric("CS Avg", uiValue(chainsyncLatency)),
			),
		)
		if metrics.DingoBlockfetchGateDispatched > 0 ||
			metrics.DingoBlockfetchGateSkippedFast > 0 ||
			metrics.DingoBlockfetchGateSkippedPeer > 0 {
			lines = append(
				lines,
				dingoMetricRow(
					innerWidth,
					dingoMetric(
						"Gate",
						uiSegmentBar([]uiSegment{
							{Label: "dispatch", Value: float64(metrics.DingoBlockfetchGateDispatched), Severity: uiSeverityOK},
							{Label: "fast", Value: float64(metrics.DingoBlockfetchGateSkippedFast), Severity: uiSeverityMuted},
							{Label: "no_peer", Value: float64(metrics.DingoBlockfetchGateSkippedPeer), Severity: uiSeverityWarn},
						}, 58),
					),
				),
				dingoMetricRow(
					innerWidth,
					dingoMetric("Dispatched", uiValue(strconv.FormatUint(metrics.DingoBlockfetchGateDispatched, 10))),
					dingoMetric("Skip Fast", uiValue(strconv.FormatUint(metrics.DingoBlockfetchGateSkippedFast, 10))),
					dingoMetric("No Peer", uiValue(strconv.FormatUint(metrics.DingoBlockfetchGateSkippedPeer, 10))),
				),
			)
		}
	} else {
		lines = append(
			lines,
			dingoMetricRow(
				innerWidth,
				dingoMetricStyled("Protocols", uiMuted("mini-protocol metrics not exposed"), uiSeverityMuted),
			),
		)
	}

	if metrics.BlockDelay > 0 ||
		metrics.BlocksW1s > 0 ||
		metrics.BlocksW3s > 0 ||
		metrics.BlocksW5s > 0 ||
		metrics.BlocksServed > 0 ||
		metrics.BlocksLate > 0 {
		blk1s := formatBlockPropagationPercent(metrics.BlocksW1s)
		blk3s := formatBlockPropagationPercent(metrics.BlocksW3s)
		blk5s := formatBlockPropagationPercent(metrics.BlocksW5s)
		blk1Pct, _ := strconv.ParseFloat(blk1s, 64)
		blk3Pct, _ := strconv.ParseFloat(blk3s, 64)
		blk5Pct, _ := strconv.ParseFloat(blk5s, 64)
		delaySeverity := uiSeverityOK
		if metrics.BlockDelay >= 5 {
			delaySeverity = uiSeverityCritical
		} else if metrics.BlockDelay >= 3 {
			delaySeverity = uiSeverityWarn
		}
		lateSeverity := dingoCounterSeverity(metrics.BlocksLate, uiSeverityWarn)
		lines = append(
			lines,
			dingoMetricRow(
				innerWidth,
				dingoMetricStyled(
					"Last Fetch",
					uiSeverityValue(fmt.Sprintf("%.2fs", metrics.BlockDelay), delaySeverity),
					delaySeverity,
				),
				dingoMetric("Served", uiValue(strconv.FormatUint(metrics.BlocksServed, 10))),
				dingoMetric("Late >5s", formatSeverityCount(metrics.BlocksLate, lateSeverity)),
				dingoMetric(
					"CDF",
					uiSparkline([]float64{blk1Pct, blk3Pct, blk5Pct}, 100, dingoCacheSeverity(blk5Pct, true))+
						uiUnit(" 1s 3s 5s"),
				),
			),
		)
	}
	return dingoPanel("protocol flow", activityPaneSeverity(), width, lines)
}

func currentPeerSortMode() peerSortMode {
	mode := peerSortMode(peerSortSelection.Load())
	switch mode {
	case peerSortRTT, peerSortName, peerSortLocation:
		return mode
	default:
		return peerSortRTT
	}
}

func cyclePeerSortMode() {
	next := (int32(currentPeerSortMode()) + 1) % 3
	peerSortSelection.Store(next)
}

func peerSortModeLabel() string {
	switch currentPeerSortMode() {
	case peerSortRTT:
		return "rtt"
	case peerSortName:
		return "name"
	case peerSortLocation:
		return "location"
	default:
		return "rtt"
	}
}

func sortedPeersForDisplay(peers []*Peer) []*Peer {
	peers = slices.Clone(peers)
	slices.SortFunc(peers, func(left, right *Peer) int {
		switch currentPeerSortMode() {
		case peerSortRTT:
			return comparePeerRTT(left, right)
		case peerSortName:
			if cmp := compareStrings(formatPeerEndpoint(left), formatPeerEndpoint(right)); cmp != 0 {
				return cmp
			}
			return comparePeerRTT(left, right)
		case peerSortLocation:
			if cmp := compareStrings(peerLocation(left), peerLocation(right)); cmp != 0 {
				return cmp
			}
			if cmp := compareStrings(formatPeerEndpoint(left), formatPeerEndpoint(right)); cmp != 0 {
				return cmp
			}
			return comparePeerRTT(left, right)
		default:
			return comparePeerRTT(left, right)
		}
	})
	return peers
}

func comparePeerRTT(left, right *Peer) int {
	if left == nil || right == nil {
		if left == right {
			return 0
		}
		if left == nil {
			return 1
		}
		return -1
	}
	if left.RTT != right.RTT {
		return compareInts(left.RTT, right.RTT)
	}
	if cmp := compareStrings(formatPeerEndpoint(left), formatPeerEndpoint(right)); cmp != 0 {
		return cmp
	}
	if left.Port != right.Port {
		return compareInts(left.Port, right.Port)
	}
	return compareStrings(left.Direction, right.Direction)
}

func compareInts(left, right int) int {
	if left < right {
		return -1
	}
	if left > right {
		return 1
	}
	return 0
}

func compareStrings(left, right string) int {
	left = strings.ToLower(left)
	right = strings.ToLower(right)
	if left < right {
		return -1
	}
	if left > right {
		return 1
	}
	return 0
}

func peerLocation(peer *Peer) string {
	if peer == nil {
		return ""
	}
	return peer.Location
}

func dingoConsoleTopPeerLines(peers []*Peer, limit, innerWidth int) []string {
	peers = sortedPeersForDisplay(peers)
	if len(peers) == 0 {
		return []string{
			dingoMetricRow(
				innerWidth,
				dingoMetricStyled("Peers", uiMuted("RTT collection pending"), uiSeverityMuted),
			),
		}
	}
	if limit > len(peers) {
		limit = len(peers)
	}

	sampleWidth := 6
	dirWidth := 3
	rttWidth := 4
	locationWidth := 20
	remoteWidth := innerWidth - sampleWidth - dirWidth - rttWidth - locationWidth - 5
	if remoteWidth < 22 {
		locationWidth = 0
		remoteWidth = innerWidth - sampleWidth - dirWidth - rttWidth - 3
	}
	if remoteWidth < 12 {
		remoteWidth = 12
	}
	if remoteWidth > 48 {
		remoteWidth = 48
	}

	headerFormat := "%-*s %-*s %-*s %*s"
	peerFormat := "%s %s %s %s"
	if locationWidth > 0 {
		headerFormat += "  %-*s"
		peerFormat += "  %s"
	}
	lines := make([]string, 0, limit+1)
	lines = append(
		lines,
		uiMuted(formatPeerTableHeader(headerFormat, sampleWidth, remoteWidth, dirWidth, rttWidth, locationWidth)),
	)
	for idx, peer := range peers[:limit] {
		severity := uiSeverityMuted
		rtt := "---"
		if peer.RTT < RTTUnreachable {
			rtt = strconv.Itoa(peer.RTT)
			severity = uiSeverityOK
			if peer.RTT >= RTTThreshold2 {
				severity = uiSeverityCritical
			} else if peer.RTT >= RTTThreshold1 {
				severity = uiSeverityWarn
			}
		}
		values := []any{
			uiMuted(fmt.Sprintf("%-*s", sampleWidth, fmt.Sprintf("#%02d", idx+1))),
			uiValue(fmt.Sprintf("%-*s", remoteWidth, shortenString(formatPeerEndpoint(peer), remoteWidth))),
			uiMuted(fmt.Sprintf("%-*s", dirWidth, shortenString(peer.Direction, dirWidth))),
			uiSeverityValue(fmt.Sprintf("%*s", rttWidth, rtt), severity),
		}
		if locationWidth > 0 {
			values = append(
				values,
				uiMuted(fmt.Sprintf("%-*s", locationWidth, shortenString(peer.Location, locationWidth))),
			)
		}
		lines = append(lines, fmt.Sprintf(peerFormat, values...))
	}
	return lines
}

func formatPeerTableHeader(
	format string,
	sampleWidth,
	remoteWidth,
	dirWidth,
	rttWidth,
	locationWidth int,
) string {
	values := []any{
		sampleWidth,
		"sample",
		remoteWidth,
		"remote peer",
		dirWidth,
		"dir",
		rttWidth,
		"rtt",
	}
	if locationWidth > 0 {
		values = append(
			values,
			locationWidth,
			"location",
		)
	}
	return fmt.Sprintf(format, values...)
}

func formatPeerEndpoint(peer *Peer) string {
	if peer == nil {
		return "unknown"
	}
	if peer.Port <= 0 {
		return peer.IP
	}
	return net.JoinHostPort(peer.IP, strconv.Itoa(peer.Port))
}

func shortenString(value string, maxLen int) string {
	if maxLen <= 0 || len(value) <= maxLen {
		return value
	}
	if maxLen <= 3 {
		return value[:maxLen]
	}
	return value[:maxLen-3] + "..."
}

func dingoLifetimeHitRatio(hits, misses uint64) (float64, bool) {
	total := hits + misses
	if total == 0 {
		return 0, false
	}
	return float64(hits) / float64(total) * 100, true
}

func mithrilPhaseName(metrics *PromMetrics) string {
	switch {
	case metrics == nil:
		return "n/a"
	case metrics.MithrilPhaseBootstrap > 0:
		return "bootstrap"
	case metrics.MithrilPhaseLedger > 0:
		return "ledger_import"
	case metrics.MithrilPhaseImmutable > 0:
		return "immutable_copy"
	case metrics.MithrilPhaseGapBlocks > 0:
		return "gap_blocks"
	case metrics.MithrilPhaseBackfill > 0:
		return "backfill"
	case metrics.MithrilPhasePostLedger > 0:
		return "post_ledger"
	default:
		return "n/a"
	}
}

func hasLeiosMetrics(metrics *PromMetrics) bool {
	return metrics != nil && len(metrics.LeiosMetrics) > 0
}

func formatLeiosMetricName(name string) string {
	labelSuffix := ""
	if labelStart := strings.Index(name, "{"); labelStart >= 0 {
		labelSuffix = name[labelStart:]
		name = name[:labelStart]
	}
	name = strings.TrimPrefix(name, "cardano_node_metrics_")
	name = strings.TrimPrefix(name, "dingo_")
	name = strings.TrimPrefix(name, "leios_")
	name = strings.TrimSuffix(name, "_total")
	name = strings.ReplaceAll(name, "_", " ")
	if labelSuffix != "" {
		name += strings.ReplaceAll(labelSuffix, "_", " ")
	}
	return name
}

func formatLeiosMetricValue(value float64) string {
	if math.IsNaN(value) || math.IsInf(value, 0) {
		return "n/a"
	}
	if math.Abs(value) >= 1000 || value == math.Trunc(value) {
		return strconv.FormatFloat(value, 'f', 0, 64)
	}
	return strconv.FormatFloat(value, 'f', 2, 64)
}

func main() {
	// Check if any command line flags are given
	flag.StringVar(
		&cmdlineFlags.configFile,
		"config",
		"",
		"path to config file to load",
	)
	flag.Parse()

	// Load config
	cfg, err := config.LoadConfig(cmdlineFlags.configFile)
	if err != nil {
		fmt.Printf("Failed to load config: %s\n", err)
		os.Exit(1)
	}

	// Initialize logger with buffer
	baseHandler := slog.NewTextHandler(
		io.Discard,
		&slog.HandlerOptions{Level: slog.LevelInfo},
	)
	bufferedHandler := &bufferHandler{handler: baseHandler}
	logger = slog.New(bufferedHandler)

	// Create a background context
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Exit if NODE_NAME is > 19 characters
	if len([]rune(getEffectiveNodeName())) > 19 {
		fmt.Println(
			"Please keep node name at or below 19 characters in length!",
		)
		os.Exit(1)
	}

	// Determine if we're P2P
	p2p = getP2P(ctx, processMetrics)
	// Set role
	setRole()
	// Get public IP
	ip, err := getPublicIP(ctx)
	if err == nil {
		publicIP = &ip
	}
	// Fetch data from Prometheus
	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			default:
			}
			prom, err := getPromMetrics(ctx)
			if err != nil && prom != nil {
				logger.Warn("Failed to fetch Prometheus metrics", "error", err)
				failCount.Add(1)
				select {
				case <-ctx.Done():
					return
				case <-time.After(time.Second * time.Duration(cfg.Prometheus.Refresh)):
				}
				continue
			} else if prom == nil {
				if promMetrics == nil {
					promMetrics = &PromMetrics{}
				}
				failCount.Add(1)
				select {
				case <-ctx.Done():
					return
				case <-time.After(time.Second * time.Duration(cfg.Prometheus.Refresh)):
				}
				continue
			}
			if getEffectiveNodeBinary() == DINGO_BINARY && prom != nil {
				if config.ApplyDingoGenesisOverride(
					prom.DingoShelleyStartTime,
					prom.DingoEpochLengthSlots,
				) {
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
			promMetrics = prom
			updateMithrilView()
			select {
			case <-ctx.Done():
				return
			case <-time.After(time.Second * time.Duration(cfg.Prometheus.Refresh)):
			}
		}
	}()

	// Set Epoch
	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			default:
			}
			setCurrentEpoch()
			if currentEpoch != 0 {
				select {
				case <-ctx.Done():
					return
				case <-time.After(time.Second * 20):
				}
			}
		}
	}()

	// Update Process metrics
	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			default:
			}
			proc, err := getProcessMetrics(ctx)
			if err != nil {
				failCount.Add(1)
				select {
				case <-ctx.Done():
					return
				case <-time.After(time.Second * 1):
				}
				continue
			}
			processMetrics = proc
			select {
			case <-ctx.Done():
				return
			case <-time.After(ProcessDiscoveryRefresh):
			}
		}
	}()

	// Set uptimes
	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			default:
			}
			uptime := getUptimes(ctx, processMetrics)
			if uptime != 0 {
				uptimes = uptime
			}
			select {
			case <-ctx.Done():
				return
			case <-time.After(time.Second * 1):
			}
		}
	}()

	// Filter peers
	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			default:
			}
			err := filterPeers(ctx)
			if err != nil {
				logger.Warn("Failed to filter peers", "error", err)
				failCount.Add(1)
				select {
				case <-ctx.Done():
					return
				case <-time.After(time.Second * 1):
				}
				continue
			}
			select {
			case <-ctx.Done():
				return
			case <-time.After(time.Second * 1):
			}
		}
	}()

	// Ping peers
	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			default:
			}
			pingPeers(ctx)
			select {
			case <-ctx.Done():
				return
			case <-time.After(time.Second * 10):
			}
		}
	}()

	// Add content to our flex box. Dingo gets its own console surface while
	// cardano-node and Amaru keep the traditional operator panel grid.
	layout := tview.NewFlex()
	leftColumn := tview.NewFlex().SetDirection(tview.FlexRow)
	centerColumn := tview.NewFlex().SetDirection(tview.FlexRow)
	rightColumn := tview.NewFlex().SetDirection(tview.FlexRow)
	panelBody := tview.NewFlex().SetDirection(tview.FlexRow)
	leftColumn.
		AddItem(nodeTextView, 7, 0, false).
		AddItem(resourceTextView, 8, 0, false).
		AddItem(connectionTextView, 0, 1, false)
	centerColumn.
		AddItem(chainTextView, 10, 0, false).
		AddItem(coreTextView, 8, 0, false).
		AddItem(blockTextView, 0, 1, false)
	rightColumn.
		AddItem(dingoTextView, 0, 1, false).
		AddItem(peerTextView, 0, 1, true)
	layout.
		AddItem(leftColumn, 0, 7, false).
		AddItem(centerColumn, 0, 10, true).
		AddItem(rightColumn, 0, 9, false)
	panelBody.
		AddItem(overviewTextView, 6, 0, false).
		AddItem(layout, 0, 1, true)
	dashboardPages.AddPage("panels", panelBody, true, true)
	dashboardPages.AddPage("dingo", dingoConsoleTextView, true, false)
	dashboardPages.AddPage(
		mithrilOverlayPageName,
		newMithrilOverlayPrimitive(dashboardContentWidth()),
		true,
		false,
	)
	dashboardPages.AddPage(
		peerOverlayPageName,
		newPeerOverlayPrimitive(dashboardContentWidth(), DashboardHeightDefault),
		true,
		false,
	)
	flex.SetDirection(tview.FlexRow).
		AddItem(headerTextView, 1, 0, false).
		AddItem(dashboardPages, 0, 1, true).
		AddItem(footerTextView, 1, 0, false)

	peerStats.RTTresultsMap = make(map[string]*Peer)
	peerStats.RTTresultsSlice = []*Peer{}
	peerStats.InFlight = make(map[string]time.Time)

	// capture inputs
	pages.SetInputCapture(func(event *tcell.EventKey) *tcell.EventKey {
		if peerOverlayActive.Load() && event.Key() == tcell.KeyEscape {
			hidePeerOverlay()
			refreshChrome()
			return nil
		}
		if event.Rune() == 112 { // p
			togglePeerOverlay(ctx)
			return nil
		}
		if event.Rune() == 115 { // s
			cyclePeerSortMode()
			if peerOverlayActive.Load() {
				peerOverlayTextView.ScrollToBeginning()
			} else {
				dingoConsoleTextView.ScrollToBeginning()
			}
			refreshDashboardText(ctx)
			return nil
		}
		if !peerOverlayActive.Load() && getEffectiveNodeBinary() == DINGO_BINARY {
			cols, rows := dashboardTerminalSize()
			if dingoConsoleCompact(cols, rows) {
				if event.Key() == tcell.KeyLeft {
					shiftCompactDingoPage(-1)
					dingoConsoleTextView.ScrollToBeginning()
					refreshDashboardText(ctx)
					return nil
				}
				if event.Key() == tcell.KeyRight {
					shiftCompactDingoPage(1)
					dingoConsoleTextView.ScrollToBeginning()
					refreshDashboardText(ctx)
					return nil
				}
			}
		}
		if event.Rune() == 114 { // r
			refreshDashboardNow(ctx)
		}
		if event.Rune() == 113 || event.Key() == tcell.KeyEscape { // q
			logMutex.Lock()
			if len(logBuffer) > 0 {
				fmt.Println("\n--- Application Logs ---")
				for _, log := range logBuffer {
					fmt.Print(log)
				}
			}
			logMutex.Unlock()
			app.Stop()
		}
		return event
	})

	// Pages
	pages.AddPage("Main", flex, true, true)
	refreshDashboardText(ctx)

	// Start our background refresh timer
	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			default:
			}
			if failCount.Load() >= cfg.App.Retries {
				panic(
					fmt.Errorf(
						"COULD NOT CONNECT TO A RUNNING INSTANCE, %d FAILED ATTEMPTS IN A ROW",
						failCount.Load(),
					),
				)
			}

			// Refresh all the things
			setRole()
			refreshDashboardText(ctx)
			select {
			case <-ctx.Done():
				return
			case <-time.After(time.Second * time.Duration(cfg.App.Refresh)):
			}
		}
	}()

	if err := app.SetRoot(pages, true).EnableMouse(false).Run(); err != nil {
		panic(err)
	}
}

var uptimes uint64

func getUptimes(ctx context.Context, processMetrics *process.Process) uint64 {
	if processMetrics == nil {
		return uptimes
	}
	// Calculate uptime
	createTime, err := processMetrics.CreateTimeWithContext(ctx)
	if err != nil {
		return uptimes
	}
	// createTime is milliseconds since UNIX epoch, convert to seconds
	createTimeSeconds := createTime / MillisecondsToSeconds
	currentTimeSeconds := time.Now().Unix()
	if currentTimeSeconds > createTimeSeconds {
		// #nosec G115 - Safe subtraction: currentTimeSeconds > createTimeSeconds prevents negative result
		uptimes = uint64(currentTimeSeconds - createTimeSeconds)
	} else {
		uptimes = 0 // Handle clock skew by showing 0 uptime
	}
	return uptimes
}

func formatDingoBytes(bytes uint64) string {
	const unit = 1024
	if bytes < unit {
		return fmt.Sprintf("%dB", bytes)
	}
	div := float64(unit)
	exp := 0
	for n := bytes / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f%c", float64(bytes)/div, "KMGTPE"[exp])
}

func formatMemoryBytes(bytes uint64) string {
	return formatDingoBytes(bytes)
}

func dingoCounterDelta(curr, prev uint64) uint64 {
	if curr < prev {
		return curr
	}
	return curr - prev
}

func dingoHitRatioPercent(
	currHits,
	currMiss,
	prevHits,
	prevMiss uint64,
) (float64, bool) {
	hits := dingoCounterDelta(currHits, prevHits)
	misses := dingoCounterDelta(currMiss, prevMiss)
	total := hits + misses
	if total == 0 {
		return 0, false
	}
	return float64(hits) / float64(total) * 100, true
}

func formatDingoRate(curr, prev uint64, dt time.Duration) string {
	if dt <= 0 {
		return "n/a"
	}
	rate := float64(dingoCounterDelta(curr, prev)) / dt.Seconds()
	return fmt.Sprintf("%.0f", math.Round(rate))
}

func dingoTipGapSeverity(slots uint64) uiSeverity {
	if slots <= SyncThresholdGood {
		return uiSeverityOK
	}
	if slots <= SyncThresholdSlow {
		return uiSeverityWarn
	}
	return uiSeverityCritical
}

func dingoCacheSeverity(percent float64, ok bool) uiSeverity {
	if !ok {
		return uiSeverityMuted
	}
	switch {
	case percent >= 95:
		return uiSeverityOK
	case percent >= 85:
		return uiSeverityWarn
	default:
		return uiSeverityCritical
	}
}

func dingoQuotaSeverity(percent float64) uiSeverity {
	switch {
	case percent <= 100:
		return uiSeverityOK
	case percent <= 125:
		return uiSeverityWarn
	default:
		return uiSeverityCritical
	}
}

func dingoRateSeverity(rate string, nonZeroSeverity uiSeverity) uiSeverity {
	if rate == "n/a" {
		return uiSeverityMuted
	}
	value, err := strconv.ParseFloat(rate, 64)
	if err != nil {
		return uiSeverityMuted
	}
	if value <= 0 {
		return uiSeverityOK
	}
	return nonZeroSeverity
}

func dingoCounterSeverity(value uint64, nonZeroSeverity uiSeverity) uiSeverity {
	if value == 0 {
		return uiSeverityOK
	}
	return nonZeroSeverity
}

func dingoIntCounterSeverity(value int, nonZeroSeverity uiSeverity) uiSeverity {
	if value <= 0 {
		return uiSeverityOK
	}
	return nonZeroSeverity
}

func formatDingoCacheMetric(label string, percent float64, ok bool) string {
	severity := dingoCacheSeverity(percent, ok)
	if !ok {
		return fmt.Sprintf(
			"%s %s %s",
			uiLabel(label),
			uiMuted("n/a"),
			uiProgressBar(0, 8, severity),
		)
	}
	return fmt.Sprintf(
		"%s %s %s",
		uiLabel(label),
		uiSeverityValue(fmt.Sprintf("%5.1f%%", percent), severity),
		uiProgressBar(percent, 8, severity),
	)
}

func formatSeverityCount(value uint64, severity uiSeverity) string {
	return fmt.Sprintf(
		"%s %s",
		uiStatusGlyph(severity),
		uiSeverityValue(strconv.FormatUint(value, 10), severity),
	)
}

func formatSeverityIntCount(value int, severity uiSeverity) string {
	if value < 0 {
		value = 0
	}
	return fmt.Sprintf(
		"%s %s",
		uiStatusGlyph(severity),
		uiSeverityValue(strconv.Itoa(value), severity),
	)
}

func peerLatencyBar(count int, percent float32, severity uiSeverity) string {
	return uiSeverityValue(strconv.Itoa(count), severity) +
		uiUnit(fmt.Sprintf(" %.0f%% ", percent)) +
		uiProgressBar(float64(percent), 14, severity)
}

func formatSeverityPercentRatio(value uint64, percent float32, severity uiSeverity) string {
	separator := uiUnit(" / ")
	if severity == uiSeverityMuted {
		separator = uiMuted(" / ")
	}
	return uiSeverityValue(strconv.FormatUint(value, 10), severity) +
		separator +
		uiSeverityValue(fmt.Sprintf("%.2f%%", percent), severity)
}

func getDingoStats() string {
	if promMetrics == nil {
		return ""
	}

	curr := *promMetrics
	currSrc := promMetrics
	now := time.Now()

	lastDingoSampleMu.Lock()
	if currSrc != lastDingoSampleSrc {
		lastDingoRateBase = lastDingoSample
		lastDingoRateBaseAt = lastDingoSampleAt
		lastDingoSample = &curr
		lastDingoSampleAt = now
		lastDingoSampleSrc = currSrc
	}
	prev := lastDingoRateBase
	prevAt := lastDingoRateBaseAt
	currAt := lastDingoSampleAt
	lastDingoSampleMu.Unlock()

	prevMetrics := &PromMetrics{}
	dt := time.Duration(0)
	if prev != nil {
		prevMetrics = prev
		dt = currAt.Sub(prevAt)
	}

	utxoRatio := 0.0
	txRatio := 0.0
	blockRatio := 0.0
	utxoRatioOK := false
	txRatioOK := false
	blockRatioOK := false
	coldExtractRate := "n/a"
	eventErrorRate := "n/a"
	eventTimeoutRate := "n/a"
	if prev != nil {
		utxoRatio, utxoRatioOK = dingoHitRatioPercent(
			curr.DingoCacheUtxoHotHits,
			curr.DingoCacheUtxoHotMiss,
			prevMetrics.DingoCacheUtxoHotHits,
			prevMetrics.DingoCacheUtxoHotMiss,
		)
		txRatio, txRatioOK = dingoHitRatioPercent(
			curr.DingoCacheTxHotHits,
			curr.DingoCacheTxHotMiss,
			prevMetrics.DingoCacheTxHotHits,
			prevMetrics.DingoCacheTxHotMiss,
		)
		blockRatio, blockRatioOK = dingoHitRatioPercent(
			curr.DingoCacheBlockLruHits,
			curr.DingoCacheBlockLruMiss,
			prevMetrics.DingoCacheBlockLruHits,
			prevMetrics.DingoCacheBlockLruMiss,
		)
		coldExtractRate = formatDingoRate(
			curr.DingoCacheColdExtract,
			prevMetrics.DingoCacheColdExtract,
			dt,
		)
		eventErrorRate = formatDingoRate(
			curr.EventDeliveryErrors,
			prevMetrics.EventDeliveryErrors,
			dt,
		)
		eventTimeoutRate = formatDingoRate(
			curr.EventDeliveryTimeouts,
			prevMetrics.EventDeliveryTimeouts,
			dt,
		)
	}

	var sb strings.Builder
	sb.WriteString(uiSection("Storage / Chain"))
	fmt.Fprintf(&sb, " %s   %s\n",
		uiKV("DB Size", uiValue(formatDingoBytes(dingoDbSize(&curr)))),
		uiKV(
			"Cached Blocks",
			uiValue(strconv.FormatUint(curr.DingoChainCachedBlocks, 10))+
				uiUnit(" blocks"),
		),
	)
	if curr.DingoDbBlobSizeBytes > 0 || curr.DingoDbMetadataSizeBytes > 0 {
		fmt.Fprintf(&sb, " %s   %s\n",
			uiKV("Blob", uiValue(formatDingoBytes(curr.DingoDbBlobSizeBytes))),
			uiKV("Metadata", uiValue(formatDingoBytes(curr.DingoDbMetadataSizeBytes))),
		)
	}
	tipSeverity := dingoTipGapSeverity(curr.DingoTipGapSlots)
	forgeSeverity := dingoTipGapSeverity(curr.DingoForgeTipGapSlots)
	fmt.Fprintf(&sb, " %s %s %s%s   %s %s %s%s\n",
		uiLabel("Tip Gap"),
		uiStatusGlyph(tipSeverity),
		uiSeverityValue(strconv.FormatUint(curr.DingoTipGapSlots, 10), tipSeverity),
		uiUnit(" slots"),
		uiLabel("Forge Gap"),
		uiStatusGlyph(forgeSeverity),
		uiSeverityValue(strconv.FormatUint(curr.DingoForgeTipGapSlots, 10), forgeSeverity),
		uiUnit(" slots"),
	)
	fmt.Fprintf(&sb, " %s   %s   %s\n",
		uiKV("Seen Headers", uiValue(strconv.FormatUint(curr.DingoChainsyncSeenHeaders, 10))),
		uiKV("Tx Evicted", uiValue(strconv.FormatUint(curr.DingoTxsEvicted, 10))),
		uiKV("Tx Expired", uiValue(strconv.FormatUint(curr.DingoTxsExpired, 10))),
	)

	sb.WriteString(uiSection("CBOR Cache"))
	fmt.Fprintf(&sb, " %s  %s  %s\n",
		formatDingoCacheMetric("utxo", utxoRatio, utxoRatioOK),
		formatDingoCacheMetric("tx", txRatio, txRatioOK),
		formatDingoCacheMetric("blk", blockRatio, blockRatioOK),
	)
	coldExtractSeverity := dingoRateSeverity(coldExtractRate, uiSeverityNeutral)
	fmt.Fprintf(&sb, " %s %s%s\n",
		uiLabel("Cold Extract"),
		uiSeverityValue(coldExtractRate, coldExtractSeverity),
		uiUnit("/s"),
	)

	sb.WriteString(uiSection("Event Bus / Slot Clock"))
	eventErrorSeverity := dingoRateSeverity(eventErrorRate, uiSeverityCritical)
	eventTimeoutSeverity := dingoRateSeverity(eventTimeoutRate, uiSeverityCritical)
	fmt.Fprintf(&sb, " %s   %s   %s %s%s   %s %s%s\n",
		uiKV("Subscribers", uiValue(strconv.FormatUint(curr.EventSubscribers, 10))),
		uiKV("Events", uiValue(strconv.FormatUint(curr.EventTotal, 10))),
		uiLabel("Err"),
		uiSeverityValue(eventErrorRate, eventErrorSeverity),
		uiUnit("/s"),
		uiLabel("Timeout"),
		uiSeverityValue(eventTimeoutRate, eventTimeoutSeverity),
		uiUnit("/s"),
	)
	fallbackSeverity := dingoCounterSeverity(curr.DingoSlotClockFallback, uiSeverityWarn)
	forgeErrSeverity := dingoCounterSeverity(curr.DingoForgeSlotClockErr, uiSeverityWarn)
	syncSkipSeverity := dingoCounterSeverity(curr.DingoForgeSyncSkip, uiSeverityWarn)
	govSeverity := dingoCounterSeverity(curr.DingoGovernanceDecodeFailures, uiSeverityCritical)
	fmt.Fprintf(&sb, " %s %s   %s %s   %s %s\n",
		uiLabel("fallback"),
		formatSeverityCount(curr.DingoSlotClockFallback, fallbackSeverity),
		uiLabel("forgeErr"),
		formatSeverityCount(curr.DingoForgeSlotClockErr, forgeErrSeverity),
		uiLabel("syncSkip"),
		formatSeverityCount(curr.DingoForgeSyncSkip, syncSkipSeverity),
	)
	fmt.Fprintf(&sb, " %s %s %s\n",
		uiLabel("Gov Failures"),
		uiStatusGlyph(govSeverity),
		uiSeverityValue(
			strconv.FormatUint(curr.DingoGovernanceDecodeFailures, 10),
			govSeverity,
		),
	)
	sb.WriteString(uiSection("Stake / Runtime"))
	stakeFailureSeverity := dingoCounterSeverity(curr.DingoStakeSnapshotFailure, uiSeverityCritical)
	fmt.Fprintf(&sb, " %s   %s   %s\n",
		uiKV("Snap Epoch", uiValue(strconv.FormatUint(curr.DingoStakeSnapshotLastEpoch, 10))),
		uiKV("Pools", uiValue(strconv.FormatUint(curr.DingoStakeSnapshotPoolCount, 10))),
		uiKV("Failures", formatSeverityCount(curr.DingoStakeSnapshotFailure, stakeFailureSeverity)),
	)
	if curr.DingoStakeSnapshotActiveStake > 0 {
		fmt.Fprintf(&sb, " %s\n",
			uiKV("Active Stake", uiValue(formatDingoADAFromLovelace(curr.DingoStakeSnapshotActiveStake))),
		)
	}
	fmt.Fprintf(&sb, " %s   %s   %s\n",
		uiKV("Goroutines", uiValue(strconv.FormatUint(curr.GoRoutines, 10))),
		uiKV("Threads", uiValue(strconv.FormatUint(curr.GoThreads, 10))),
		uiKV("FD", uiValue(formatFDUsage(&curr))),
	)
	return sb.String()
}

func isMithrilSyncActive() bool {
	if promMetrics == nil {
		return false
	}
	if promMetrics.MithrilSyncCompleted == 1 {
		return false
	}
	for _, stage := range promMetrics.MithrilSyncLedgerImportStages {
		if stage.Current > 0 || stage.Total > 0 || stage.Percent > 0 {
			return true
		}
	}
	return promMetrics.MithrilSyncStartedAt > 0 ||
		promMetrics.MithrilSyncErrorsTotal > 0 ||
		promMetrics.MithrilSyncDownloadBytes > 0 ||
		promMetrics.MithrilSyncDownloadTotalBytes > 0 ||
		promMetrics.MithrilSyncDownloadPercent > 0 ||
		promMetrics.MithrilSyncDownloadRate > 0 ||
		promMetrics.MithrilSyncSnapshotSize > 0 ||
		promMetrics.MithrilSyncSnapshotEpoch > 0 ||
		promMetrics.MithrilSyncSnapshotAncillarySize > 0 ||
		promMetrics.MithrilSyncSnapshotImmutableFile > 0 ||
		promMetrics.MithrilSyncLedgerImportCurrent > 0 ||
		promMetrics.MithrilSyncLedgerImportTotal > 0 ||
		promMetrics.MithrilSyncLedgerImportPercent > 0 ||
		promMetrics.MithrilSyncLedgerStateSlot > 0 ||
		promMetrics.MithrilSyncImmutableBlocksCopied > 0 ||
		promMetrics.MithrilSyncImmutableCopyPerSecond > 0 ||
		promMetrics.MithrilSyncImmutableCopyPercent > 0 ||
		promMetrics.MithrilSyncImmutableCurrentSlot > 0 ||
		promMetrics.MithrilSyncImmutableTipSlot > 0 ||
		promMetrics.MithrilSyncGapBlocks > 0 ||
		promMetrics.MithrilPhaseBootstrap > 0 ||
		promMetrics.MithrilPhaseLedger > 0 ||
		promMetrics.MithrilPhaseImmutable > 0 ||
		promMetrics.MithrilPhaseGapBlocks > 0 ||
		promMetrics.MithrilPhaseBackfill > 0 ||
		promMetrics.MithrilPhasePostLedger > 0
}

// updateMithrilView presents Mithril sync as an overlay over the active dashboard
// instead of moving the operator away from the normal Dingo view.
func updateMithrilView() {
	if getEffectiveNodeBinary() != DINGO_BINARY || !isMithrilSyncActive() {
		mithrilOverlayText = ""
		mithrilViewAutoActive.Store(false)
		dashboardPages.HidePage(mithrilOverlayPageName)
		return
	}

	width := dashboardContentWidth()
	setTextIfChanged(
		mithrilOverlayTextView,
		&mithrilOverlayText,
		getMithrilOverlayText(width),
	)
	mithrilViewAutoActive.Store(true)
	dashboardPages.AddPage(
		mithrilOverlayPageName,
		newMithrilOverlayPrimitive(width),
		true,
		true,
	)
}

func getMithrilOverlayText(width int) string {
	if promMetrics == nil {
		return ""
	}
	m := promMetrics
	innerWidth := dingoPanelInnerWidth(width)
	tipGap, tipSeverity := dingoConsoleTipGap(m)
	epochProgress := float64(getEpochProgress())
	progressSeverity := mithrilProgressSeverity(epochProgress)

	startedAgo := "n/a"
	if m.MithrilSyncStartedAt > 0 {
		startedAgo = time.Since(time.Unix(int64(m.MithrilSyncStartedAt), 0)).
			Truncate(time.Second).
			String() + " ago"
	}

	lines := make([]string, 0, 6)
	lines = append(lines,
		dingoMetricRow(
			innerWidth,
			dingoMetricStyled("MITHRIL SYNC", uiSeverityValue(mithrilPhaseName(m), uiSeverityNeutral), activityPaneSeverity()),
			dingoMetric("Started", uiValue(startedAgo)),
			dingoMetric("Snapshot", uiValue(formatDingoBytes(m.MithrilSyncSnapshotSize))),
		),
	)
	lines = append(lines, dingoConsoleMithrilProgressLines(m, innerWidth, 34, true)...)
	lines = append(
		lines,
		dingoMetricRow(
			innerWidth,
			dingoMetricSpan(
				"Immutable",
				uiSeverityValue(fmt.Sprintf("%5.1f%%", m.MithrilSyncImmutableCopyPercent), mithrilProgressSeverity(m.MithrilSyncImmutableCopyPercent))+
					" "+
					uiProgressBar(m.MithrilSyncImmutableCopyPercent, 34, mithrilProgressSeverity(m.MithrilSyncImmutableCopyPercent)),
				3,
			),
			dingoMetric(
				"Copied",
				uiValue(strconv.FormatUint(m.MithrilSyncImmutableBlocksCopied, 10))+
					uiUnit(" blocks"),
			),
		),
		dingoMetricRow(
			innerWidth,
			dingoMetric("Block", uiValue(strconv.FormatUint(m.BlockNum, 10))),
			dingoMetric("Slot", uiValue(strconv.FormatUint(m.SlotNum, 10))),
			dingoMetricStyled(
				"Tip Gap",
				uiSeverityValue(strconv.FormatUint(tipGap, 10), tipSeverity)+uiUnit(" slots"),
				tipSeverity,
			),
		),
		dingoMetricRow(
			innerWidth,
			dingoMetricSpan(
				"Epoch",
				uiSeverityValue(fmt.Sprintf("%5.1f%%", epochProgress), progressSeverity)+
					" "+
					uiProgressBar(epochProgress, 34, progressSeverity),
				2,
			),
			dingoMetric("Remaining", uiValue(formatEpochRemaining())),
			dingoMetric("Switch", uiValue(formatEpochSwitchClock())),
		),
	)

	var sb strings.Builder
	sb.WriteString("\n")
	for _, line := range lines {
		fmt.Fprintf(&sb, " %s\n", dingoPadTaggedRight(line, innerWidth))
	}
	return sb.String()
}

func mithrilProgressSeverity(percent float64) uiSeverity {
	if percent >= 100 {
		return uiSeverityOK
	}
	if percent <= 0 {
		return uiSeverityMuted
	}
	return uiSeverityNeutral
}

func derivedProgressPercent(current, total uint64, fallback float64) float64 {
	if total == 0 {
		return fallback
	}
	percent := float64(current) / float64(total) * 100
	if percent < 0 {
		return 0
	}
	if percent > 100 {
		return 100
	}
	return percent
}

func formatMithrilProgressLine(
	label string,
	percent float64,
	detail string,
	severity uiSeverity,
) string {
	return fmt.Sprintf(
		" %s %s  %s  %s\n",
		uiLabel(fmt.Sprintf("%-13s", label)),
		uiSeverityValue(fmt.Sprintf("%5.1f%%", percent), severity),
		uiProgressBar(percent, 18, severity),
		detail,
	)
}

func getMithrilStats() string {
	if promMetrics == nil {
		return ""
	}
	m := promMetrics

	startedAgo := "n/a"
	if m.MithrilSyncStartedAt > 0 {
		elapsed := time.Since(time.Unix(int64(m.MithrilSyncStartedAt), 0)).Truncate(time.Second)
		startedAgo = elapsed.String() + " ago"
	}

	phaseStr := "n/a"
	switch {
	case m.MithrilPhaseBootstrap > 0:
		phaseStr = "bootstrap"
	case m.MithrilPhaseLedger > 0:
		phaseStr = "ledger_import"
	case m.MithrilPhaseImmutable > 0:
		phaseStr = "immutable_copy"
	case m.MithrilPhaseGapBlocks > 0:
		phaseStr = "gap_blocks"
	case m.MithrilPhaseBackfill > 0:
		phaseStr = "backfill"
	case m.MithrilPhasePostLedger > 0:
		phaseStr = "post_ledger"
	}

	snapshotStr := "n/a"
	if m.MithrilSyncSnapshotSize > 0 {
		snapshotStr = formatDingoBytes(m.MithrilSyncSnapshotSize)
	}

	var sb strings.Builder
	sb.WriteString(uiSection("Sync State"))
	fmt.Fprintf(&sb, " %s   %s\n",
		uiKV("Phase", uiSeverityValue(phaseStr, uiSeverityNeutral)),
		uiKV("Started", uiValue(startedAgo)),
	)

	errSeverity := uiSeverityOK
	if m.MithrilSyncErrorsTotal > 0 {
		errSeverity = uiSeverityCritical
	}
	fmt.Fprintf(&sb, " %s   %s   %s %s\n",
		uiKV("Snapshot", uiValue(snapshotStr)),
		uiKV("Epoch", uiValue(strconv.FormatUint(m.MithrilSyncSnapshotEpoch, 10))),
		uiLabel("Errors"),
		formatSeverityCount(m.MithrilSyncErrorsTotal, errSeverity),
	)
	fmt.Fprintf(&sb, " %s %s   %s\n",
		uiLabel("Snapshot Meta"),
		uiKV(
			"file",
			uiValue(strconv.FormatUint(m.MithrilSyncSnapshotImmutableFile, 10)),
		),
		uiKV("Ancillary", uiValue(formatDingoBytes(m.MithrilSyncSnapshotAncillarySize))),
	)

	sb.WriteString(uiSection("Progress"))
	dlPct := derivedProgressPercent(
		m.MithrilSyncDownloadBytes,
		m.MithrilSyncDownloadTotalBytes,
		m.MithrilSyncDownloadPercent,
	)
	dlSuffix := formatDingoBytes(m.MithrilSyncDownloadBytes)
	if m.MithrilSyncDownloadTotalBytes > 0 {
		dlSuffix = fmt.Sprintf(
			"%s%s%s",
			uiValue(formatDingoBytes(m.MithrilSyncDownloadBytes)),
			uiUnit("/"),
			uiValue(formatDingoBytes(m.MithrilSyncDownloadTotalBytes)),
		)
	}
	sb.WriteString(formatMithrilProgressLine(
		"Download",
		dlPct,
		fmt.Sprintf(
			"%s   %s %s%s",
			dlSuffix,
			uiLabel("rate"),
			uiValue(formatDingoBytes(uint64(m.MithrilSyncDownloadRate))),
			uiUnit("/s"),
		),
		mithrilProgressSeverity(dlPct),
	))

	ldgPct := derivedProgressPercent(
		m.MithrilSyncLedgerImportCurrent,
		m.MithrilSyncLedgerImportTotal,
		m.MithrilSyncLedgerImportPercent,
	)
	ldgSuffix := uiValue(strconv.FormatUint(m.MithrilSyncLedgerImportCurrent, 10))
	if m.MithrilSyncLedgerImportTotal > 0 {
		ldgSuffix = fmt.Sprintf(
			"%s%s%s%s",
			uiValue(strconv.FormatUint(m.MithrilSyncLedgerImportCurrent, 10)),
			uiUnit("/"),
			uiValue(strconv.FormatUint(m.MithrilSyncLedgerImportTotal, 10)),
			uiUnit(" items"),
		)
	}
	sb.WriteString(formatMithrilProgressLine(
		"Ledger Import",
		ldgPct,
		ldgSuffix,
		mithrilProgressSeverity(ldgPct),
	))
	if len(m.MithrilSyncLedgerImportStages) > 0 {
		stages := make([]string, 0, len(m.MithrilSyncLedgerImportStages))
		for stage := range m.MithrilSyncLedgerImportStages {
			stages = append(stages, stage)
		}
		slices.Sort(stages)
		for _, stage := range stages {
			stageMetrics := m.MithrilSyncLedgerImportStages[stage]
			stageSuffix := uiValue(strconv.FormatUint(stageMetrics.Current, 10))
			if stageMetrics.Total > 0 {
				stageSuffix = fmt.Sprintf(
					"%s%s%s%s",
					uiValue(strconv.FormatUint(stageMetrics.Current, 10)),
					uiUnit("/"),
					uiValue(strconv.FormatUint(stageMetrics.Total, 10)),
					uiUnit(" items"),
				)
			}
			sb.WriteString(formatMithrilProgressLine(
				stage,
				stageMetrics.Percent,
				stageSuffix,
				mithrilProgressSeverity(stageMetrics.Percent),
			))
		}
	}

	immPct := m.MithrilSyncImmutableCopyPercent
	sb.WriteString(formatMithrilProgressLine(
		"Immutable",
		immPct,
		fmt.Sprintf(
			"%s%s   %s%s",
			uiValue(strconv.FormatUint(m.MithrilSyncImmutableBlocksCopied, 10)),
			uiUnit(" blocks"),
			uiValue(fmt.Sprintf("%.0f", m.MithrilSyncImmutableCopyPerSecond)),
			uiUnit(" blk/s"),
		),
		mithrilProgressSeverity(immPct),
	))

	sb.WriteString(uiSection("Slots / Gap"))
	fmt.Fprintf(&sb, " %s   %s\n",
		uiKV("Ledger Slot", uiValue(strconv.FormatUint(m.MithrilSyncLedgerStateSlot, 10))),
		uiKV(
			"Immutable Slot",
			uiValue(strconv.FormatUint(m.MithrilSyncImmutableCurrentSlot, 10))+
				uiUnit("/")+
				uiValue(strconv.FormatUint(m.MithrilSyncImmutableTipSlot, 10)),
		),
	)
	gapSeverity := uiSeverityOK
	if m.MithrilSyncGapBlocks > 0 {
		gapSeverity = uiSeverityWarn
	}
	fmt.Fprintf(&sb, " %s %s %s%s\n",
		uiLabel("Gap Blocks"),
		uiStatusGlyph(gapSeverity),
		uiSeverityValue(strconv.FormatUint(m.MithrilSyncGapBlocks, 10), gapSeverity),
		uiUnit(" blocks remaining"),
	)

	return sb.String()
}

func getEpochProgress() float32 {
	cfg := config.GetConfig()
	if cfg.Node.ShelleyTransEpoch < 0 {
		return float32(0.0)
	}
	var epochProgress float32
	if promMetrics == nil {
		epochProgress = float32(0.0)
		// #nosec G115
	} else if promMetrics.EpochNum >= uint64(cfg.Node.ShelleyTransEpoch) {
		if cfg.Node.ShelleyGenesis.EpochLength == 0 {
			epochProgress = 0.0
		} else {
			epochProgress = float32(
				(float32(promMetrics.SlotInEpoch) / float32(cfg.Node.ShelleyGenesis.EpochLength)) * 100,
			)
		}
	} else {
		if cfg.Node.ByronGenesis.EpochLength == 0 {
			epochProgress = 0.0
		} else {
			epochProgress = float32(
				(float32(promMetrics.SlotInEpoch) / float32(cfg.Node.ByronGenesis.EpochLength)) * 100,
			)
		}
	}
	return epochProgress
}

func currentEpochTiming() (epochLengthSlots uint64, slotLengthMs uint64, ok bool) {
	cfg := config.GetConfig()
	if promMetrics == nil {
		return 0, 0, false
	}
	if cfg.Node.ShelleyTransEpoch >= 0 &&
		promMetrics.EpochNum >= uint64(cfg.Node.ShelleyTransEpoch) {
		return cfg.Node.ShelleyGenesis.EpochLength, cfg.Node.ShelleyGenesis.SlotLength, cfg.Node.ShelleyGenesis.EpochLength > 0 && cfg.Node.ShelleyGenesis.SlotLength > 0
	}
	return cfg.Node.ByronGenesis.EpochLength, cfg.Node.ByronGenesis.SlotLength, cfg.Node.ByronGenesis.EpochLength > 0 && cfg.Node.ByronGenesis.SlotLength > 0
}

func currentEpochSwitchTime() (time.Time, bool) {
	cfg := config.GetConfig()
	if promMetrics == nil || cfg.Node.ByronGenesis.StartTime == 0 {
		return time.Time{}, false
	}

	epoch := promMetrics.EpochNum
	if cfg.Node.ShelleyTransEpoch >= 0 &&
		epoch >= uint64(cfg.Node.ShelleyTransEpoch) {
		if cfg.Node.ByronGenesis.EpochLength == 0 ||
			cfg.Node.ByronGenesis.SlotLength == 0 ||
			cfg.Node.ShelleyGenesis.EpochLength == 0 ||
			cfg.Node.ShelleyGenesis.SlotLength == 0 {
			return time.Time{}, false
		}
		shelleyStart := cfg.Node.ByronGenesis.StartTime +
			(uint64(cfg.Node.ShelleyTransEpoch)*
				cfg.Node.ByronGenesis.EpochLength*
				cfg.Node.ByronGenesis.SlotLength)/1000
		epochsSinceShelley := epoch - uint64(cfg.Node.ShelleyTransEpoch) + 1
		switchUnix := shelleyStart +
			(epochsSinceShelley*
				cfg.Node.ShelleyGenesis.EpochLength*
				cfg.Node.ShelleyGenesis.SlotLength)/1000
		return unixTimeFromUint64(switchUnix)
	}

	if cfg.Node.ByronGenesis.EpochLength == 0 || cfg.Node.ByronGenesis.SlotLength == 0 {
		return time.Time{}, false
	}
	switchUnix := cfg.Node.ByronGenesis.StartTime +
		((epoch + 1) * cfg.Node.ByronGenesis.EpochLength * cfg.Node.ByronGenesis.SlotLength / 1000)
	return unixTimeFromUint64(switchUnix)
}

func unixTimeFromUint64(seconds uint64) (time.Time, bool) {
	const maxUnixSeconds = uint64(1<<63 - 1)
	if seconds > maxUnixSeconds {
		return time.Time{}, false
	}
	return time.Unix(int64(seconds), 0), true //nolint:gosec // bounds checked above
}

func getEpochRemainingSeconds() (uint64, bool) {
	if switchTime, ok := currentEpochSwitchTime(); ok {
		remaining := time.Until(switchTime)
		if remaining <= 0 {
			return 0, true
		}
		return uint64(remaining / time.Second), true
	}

	epochLength, slotLengthMs, ok := currentEpochTiming()
	if !ok || promMetrics == nil {
		return 0, false
	}
	if promMetrics.SlotInEpoch >= epochLength {
		return 0, true
	}
	remainingSlots := epochLength - promMetrics.SlotInEpoch
	return (remainingSlots * slotLengthMs) / 1000, true
}

func formatEpochRemaining() string {
	remaining, ok := getEpochRemainingSeconds()
	if !ok {
		return "n/a"
	}
	return timeFromSeconds(remaining)
}

func getEpochText(ctx context.Context) string {
	select {
	case <-ctx.Done():
		return ""
	default:
	}

	var sb strings.Builder

	epochProgress := getEpochProgress()
	severity := mithrilProgressSeverity(float64(epochProgress))

	fmt.Fprintf(&sb, " %s   %s\n",
		uiKV("Epoch", uiValue(strconv.FormatUint(currentEpoch, 10))),
		uiKV("Remaining", uiValue(formatEpochRemaining())),
	)
	fmt.Fprintf(&sb, " %s\n",
		uiPercentBar("Progress", float64(epochProgress), severity, 50),
	)
	return sb.String()
}

func getChainText(ctx context.Context) string {
	select {
	case <-ctx.Done():
		return ""
	default:
	}

	if promMetrics == nil {
		return chainText
	}
	var sb strings.Builder

	mempoolTxKBytes := promMetrics.MempoolBytes / 1024

	tipRef := getSlotTipRef()
	var tipDiff uint64
	if tipRef < promMetrics.SlotNum {
		tipDiff = 0
	} else {
		tipDiff = tipRef - promMetrics.SlotNum
	}

	tipSeverity := dingoTipGapSeverity(tipDiff)
	tipStatus := "synced"
	if promMetrics.SlotNum == 0 {
		tipSeverity = uiSeverityMuted
		tipStatus = "starting"
	} else if tipSeverity == uiSeverityWarn {
		tipStatus = "lagging"
	} else if tipSeverity == uiSeverityCritical {
		tipStatus = "syncing"
	}
	fmt.Fprintf(&sb, " %s   %s   %s\n",
		uiKV("Block", uiValue(strconv.FormatUint(promMetrics.BlockNum, 10))),
		uiKV("Slot", uiValue(strconv.FormatUint(promMetrics.SlotNum, 10))),
		uiKV("Tip Ref", uiValue(strconv.FormatUint(tipRef, 10))),
	)
	fmt.Fprintf(&sb, " %s %s %s%s   %s   %s\n",
		uiLabel("Tip Gap"),
		uiStatusGlyph(tipSeverity),
		uiSeverityValue(strconv.FormatUint(tipDiff, 10), tipSeverity),
		uiUnit(" slots"),
		uiPill("status", tipStatus, tipSeverity),
		uiKV("Forks", uiValue(strconv.FormatUint(promMetrics.Forks, 10))),
	)
	fmt.Fprintf(&sb, " %s   %s   %s\n",
		uiKV("Total Tx", uiValue(strconv.FormatUint(promMetrics.TxProcessed, 10))),
		uiKV("Slot Epoch", uiValue(strconv.FormatUint(promMetrics.SlotInEpoch, 10))),
		uiKV(
			"Mempool",
			uiValue(strconv.FormatUint(promMetrics.MempoolTx, 10))+
				uiUnit("/")+
				uiValue(strconv.FormatUint(mempoolTxKBytes, 10))+
				uiUnit("K"),
		),
	)
	fmt.Fprintf(&sb, " %s\n",
		uiKV("Density", uiValue(fmt.Sprintf("%3.5f%%", promMetrics.Density*100))),
	)
	return sb.String()
}

func getConnectionText(ctx context.Context) string {
	cfg := config.GetConfig()
	var sb strings.Builder

	if p2p {
		if promMetrics == nil {
			return connectionText
		}
		fmt.Fprintf(&sb, " %s\n", uiPill("p2p", "enabled", uiSeverityOK))
		fmt.Fprintf(&sb, " %s   %s\n",
			uiKV("Incoming", uiValue(strconv.FormatUint(promMetrics.ConnIncoming, 10))),
			uiKV("Outgoing", uiValue(strconv.FormatUint(promMetrics.ConnOutgoing, 10))),
		)
		fmt.Fprintf(&sb, " %s   %s   %s\n",
			uiKV("Cold", uiValue(strconv.FormatUint(promMetrics.PeersCold, 10))),
			uiKV("Warm", uiValue(strconv.FormatUint(promMetrics.PeersWarm, 10))),
			uiKV("Hot", uiValue(strconv.FormatUint(promMetrics.PeersHot, 10))),
		)
		fmt.Fprintf(&sb, " %s   %s\n",
			uiKV("Uni", uiValue(strconv.FormatUint(promMetrics.ConnUniDir, 10))),
			uiKV("Bi", uiValue(strconv.FormatUint(promMetrics.ConnBiDir, 10))),
		)
		fmt.Fprintf(&sb, " %s\n",
			uiKV("FullDuplex", uiValue(strconv.FormatUint(promMetrics.ConnFullDuplex, 10))),
		)
	} else {
		if processMetrics == nil {
			return connectionText
		}
		// Get process in/out connections
		connections, err := netutil.ConnectionsPidWithContext(ctx, "tcp", processMetrics.Pid)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Failed to get processes: %v\n", err)
			return connectionText
		}

		var peersIn []string
		var peersOut []string

		// Loops each connection, looking for ESTABLISHED
		for _, c := range connections {
			if c.Status == "ESTABLISHED" {
				// If local port == node port, it's incoming
				if c.Laddr.Port == cfg.Node.Port {
					peersIn = append(peersIn, fmt.Sprintf("%s:%d", c.Raddr.IP, c.Raddr.Port))
				}
				// If local port != node port, ekg port, or prometheus port, it's outgoing
				if c.Laddr.Port != cfg.Node.Port && c.Laddr.Port != uint32(12788) && c.Laddr.Port != cfg.Prometheus.Port {
					peersOut = append(peersOut, fmt.Sprintf("%s:%d", c.Raddr.IP, c.Raddr.Port))
				}
			}
		}

		fmt.Fprintf(&sb, " %s\n", uiPill("p2p", "disabled", uiSeverityWarn))
		fmt.Fprintf(&sb, " %s   %s\n",
			uiKV("Incoming", uiValue(strconv.Itoa(len(peersIn)))),
			uiKV("Outgoing", uiValue(strconv.Itoa(len(peersOut)))),
		)
	}
	return sb.String()
}

func getCoreText(ctx context.Context) string {
	select {
	case <-ctx.Done():
		return ""
	default:
	}

	if promMetrics == nil {
		return coreText
	}

	var sb strings.Builder

	if hasForgeMetrics(promMetrics) {
		forgeDisabled := getEffectiveNodeBinary() == DINGO_BINARY &&
			promMetrics.ForgingEnabled == 0
		adoptedSeverity := uiSeverityOK
		if forgeDisabled && promMetrics.Adopted == 0 {
			adoptedSeverity = uiSeverityMuted
		}
		invalidSeverity := uiSeverityOK
		if forgeDisabled && promMetrics.DidntAdopt == 0 {
			invalidSeverity = uiSeverityMuted
		}
		if promMetrics.IsLeader != promMetrics.Adopted {
			adoptedSeverity = uiSeverityWarn
		}
		if promMetrics.DidntAdopt != 0 {
			invalidSeverity = uiSeverityCritical
		}
		var missedSlotsPct float32
		if promMetrics.AboutToLead > 0 {
			missedSlotsPct = float32(
				promMetrics.MissedSlots,
			) / (float32(promMetrics.AboutToLead + promMetrics.MissedSlots)) * 100
		}
		missedSeverity := uiSeverityOK
		if promMetrics.MissedSlots > 0 {
			missedSeverity = uiSeverityWarn
		} else if forgeDisabled {
			missedSeverity = uiSeverityMuted
		} else if missedSlotsPct > 0 {
			missedSeverity = uiSeverityWarn
		}
		forgeMode := "observing"
		forgeModeSeverity := uiSeverityMuted
		if role == "Core" {
			forgeMode = "core"
			forgeModeSeverity = uiSeverityOK
		}
		if getEffectiveNodeBinary() == DINGO_BINARY {
			forgeMode = "disabled"
			if promMetrics.ForgingEnabled > 0 {
				forgeMode = "enabled"
				forgeModeSeverity = uiSeverityOK
			}
		}
		forgeRowSeverity := uiSeverityNeutral
		if forgeDisabled {
			forgeRowSeverity = uiSeverityMuted
		}
		forgedSeverity := forgeRowSeverity
		if promMetrics.BlocksForged > 0 {
			forgedSeverity = uiSeverityNeutral
		}
		fmt.Fprintf(&sb, " %s   %s\n",
			uiPill("mode", forgeMode, forgeModeSeverity),
			uiKVSeverityAligned(8, "Forged", strconv.FormatUint(promMetrics.BlocksForged, 10), forgedSeverity),
		)
		fmt.Fprintf(&sb, " %s   %s\n",
			uiKVSeverityAligned(8, "Leader", strconv.FormatUint(promMetrics.IsLeader, 10), forgeRowSeverity),
			uiKVStyledAligned(8, "Adopted", formatSeverityCount(promMetrics.Adopted, adoptedSeverity), adoptedSeverity),
		)
		fmt.Fprintf(&sb, " %s   %s\n",
			uiKVStyledAligned(8, "Invalid", formatSeverityCount(promMetrics.DidntAdopt, invalidSeverity), invalidSeverity),
			uiKVStyledAligned(
				8,
				"Missed",
				formatSeverityPercentRatio(promMetrics.MissedSlots, missedSlotsPct, missedSeverity),
				missedSeverity,
			),
		)
		fmt.Fprintf(&sb, " %s   %s\n",
			uiKVSeverityAligned(10, "KES period", strconv.FormatUint(promMetrics.KesPeriod, 10), forgeRowSeverity),
			uiKVSeverityAligned(10, "KES remain", strconv.FormatUint(promMetrics.RemainingKesPeriods, 10), forgeRowSeverity),
		)
		if promMetrics.DingoBlockForgingLatencyN > 0 {
			avg := promMetrics.DingoBlockForgingLatencyS /
				float64(promMetrics.DingoBlockForgingLatencyN)
			fmt.Fprintf(&sb, " %s   %s\n",
				uiKVAligned(12, "Latency Avg", uiValue(fmt.Sprintf("%.3fs", avg))),
				uiKVAligned(12, "Samples", uiValue(strconv.FormatUint(promMetrics.DingoBlockForgingLatencyN, 10))),
			)
		} else if getEffectiveNodeBinary() == DINGO_BINARY {
			fmt.Fprintf(&sb, " %s\n", uiKVStyledAligned(12, "Latency Avg", uiMuted("n/a"), forgeRowSeverity))
		}
	} else {
		fmt.Fprintf(&sb, " %s\n", uiMuted("observing only"))
	}
	failCount.Store(0)
	return sb.String()
}

func getBlockText(ctx context.Context) string {
	select {
	case <-ctx.Done():
		return ""
	default:
	}

	if promMetrics == nil {
		return blockText
	}
	if getEffectiveNodeBinary() == DINGO_BINARY && hasDingoPropagationMetrics(promMetrics) {
		failCount.Store(0)
		return getDingoPropagationText()
	}

	// Style / UI
	width := TerminalWidthDefault

	// Get our terminal size
	fd := os.Stdout.Fd()
	if fd > math.MaxInt {
		failCount.Add(1)
		return "ERROR: invalid file descriptor"
	}
	tcols, tlines, err := terminal.GetSize(int(fd)) //nolint:gosec // fd validated above
	if err != nil {
		failCount.Add(1)
		return fmt.Sprintf("ERROR: %v", err)
	}
	// Validate size
	if width >= tcols {
		footerTextView.Clear()
		footerTextView.SetText(getMinimalFooterText())
		return fmt.Sprintf(
			"\n [red]Terminal width too small![white]\n Please increase by [yellow]%d[white] columns\n",
			width-tcols+1,
		)
	}
	// Track the number of lines drawn, using left column as reference
	line := 10
	if line >= (tlines - 1) {
		footerTextView.Clear()
		footerTextView.SetText(getMinimalFooterText())
		return fmt.Sprintf(
			"\n [red]Terminal height too small![white]\n Please increase by [yellow]%d[white] lines\n",
			line-tlines+2,
		)
	}

	var sb strings.Builder

	blk1s := formatBlockPropagationPercent(promMetrics.BlocksW1s)
	blk3s := formatBlockPropagationPercent(promMetrics.BlocksW3s)
	blk5s := formatBlockPropagationPercent(promMetrics.BlocksW5s)
	delay := fmt.Sprintf("%.2f", promMetrics.BlockDelay)
	delaySeverity := uiSeverityOK
	if promMetrics.BlockDelay >= 5 {
		delaySeverity = uiSeverityCritical
	} else if promMetrics.BlockDelay >= 3 {
		delaySeverity = uiSeverityWarn
	}
	lateSeverity := dingoCounterSeverity(promMetrics.BlocksLate, uiSeverityWarn)
	blk1Pct, _ := strconv.ParseFloat(blk1s, 64)
	blk3Pct, _ := strconv.ParseFloat(blk3s, 64)
	blk5Pct, _ := strconv.ParseFloat(blk5s, 64)

	fmt.Fprintf(&sb, " %s %s%s   %s   %s %s\n",
		uiLabel("Last Delay"),
		uiSeverityValue(delay, delaySeverity),
		uiUnit("s"),
		uiKV("Served", uiValue(strconv.FormatUint(promMetrics.BlocksServed, 10))),
		uiLabel("Late >5s"),
		formatSeverityCount(promMetrics.BlocksLate, lateSeverity),
	)
	fmt.Fprintf(&sb, " %s   %s   %s\n",
		uiPercentBar("<=1s", blk1Pct, dingoCacheSeverity(blk1Pct, true), 8),
		uiPercentBar("<=3s", blk3Pct, dingoCacheSeverity(blk3Pct, true), 8),
		uiPercentBar("<=5s", blk5Pct, dingoCacheSeverity(blk5Pct, true), 8),
	)
	fmt.Fprintf(&sb, " %s %s   %s\n",
		uiLabel("CDF Shape"),
		uiSparkline([]float64{blk1Pct, blk3Pct, blk5Pct}, 100, dingoCacheSeverity(blk5Pct, true)),
		uiMuted("1s 3s 5s"),
	)

	failCount.Store(0)
	return sb.String()
}

func hasDingoPropagationMetrics(metrics *PromMetrics) bool {
	if metrics == nil {
		return false
	}
	return metrics.DingoProtocolBlockfetchMessages > 0 ||
		metrics.DingoProtocolChainsyncMessages > 0 ||
		metrics.DingoProtocolKeepaliveMessages > 0 ||
		metrics.DingoProtocolTxSubmitMessages > 0 ||
		metrics.DingoProtocolBlockfetchCount > 0 ||
		metrics.DingoProtocolChainsyncCount > 0 ||
		metrics.DingoBlockfetchGateDispatched > 0 ||
		metrics.DingoBlockfetchGateSkippedFast > 0 ||
		metrics.DingoBlockfetchGateSkippedPeer > 0
}

func getDingoPropagationText() string {
	if promMetrics == nil {
		return ""
	}
	m := promMetrics

	blockfetchLatency := formatDingoProtocolLatency(
		m.DingoProtocolBlockfetchSum,
		m.DingoProtocolBlockfetchCount,
	)
	chainsyncLatency := formatDingoProtocolLatency(
		m.DingoProtocolChainsyncSum,
		m.DingoProtocolChainsyncCount,
	)

	var sb strings.Builder
	sb.WriteString(uiSection("Mini Protocols"))
	fmt.Fprintf(&sb, " %s   %s\n",
		uiKVAligned(
			10,
			"Blockfetch",
			uiValue(strconv.FormatUint(m.DingoProtocolBlockfetchMessages, 10))+
				uiUnit(" msg"),
		),
		uiKVAligned(10, "Avg", uiValue(blockfetchLatency)),
	)
	fmt.Fprintf(&sb, " %s   %s\n",
		uiKVAligned(
			10,
			"Chainsync",
			uiValue(strconv.FormatUint(m.DingoProtocolChainsyncMessages, 10))+
				uiUnit(" msg"),
		),
		uiKVAligned(10, "Avg", uiValue(chainsyncLatency)),
	)
	fmt.Fprintf(&sb, " %s   %s\n",
		uiKVAligned(
			10,
			"Keepalive",
			uiValue(strconv.FormatUint(m.DingoProtocolKeepaliveMessages, 10))+
				uiUnit(" msg"),
		),
		uiKVAligned(
			10,
			"TxSubmit",
			uiValue(strconv.FormatUint(m.DingoProtocolTxSubmitMessages, 10))+
				uiUnit(" msg"),
		),
	)
	fmt.Fprintf(&sb, " %s %s\n",
		uiLabel("Traffic Mix"),
		uiSegmentBar([]uiSegment{
			{Label: "blockfetch", Value: float64(m.DingoProtocolBlockfetchMessages), Severity: uiSeverityOK},
			{Label: "chainsync", Value: float64(m.DingoProtocolChainsyncMessages), Severity: uiSeverityNeutral},
			{Label: "keepalive", Value: float64(m.DingoProtocolKeepaliveMessages), Severity: uiSeverityWarn},
			{Label: "txsubmit", Value: float64(m.DingoProtocolTxSubmitMessages), Severity: uiSeverityMuted},
		}, 24),
	)

	if m.DingoBlockfetchGateDispatched > 0 ||
		m.DingoBlockfetchGateSkippedFast > 0 ||
		m.DingoBlockfetchGateSkippedPeer > 0 {
		sb.WriteString(uiSection("Blockfetch Gate"))
		fmt.Fprintf(&sb, " %s   %s   %s\n",
			uiKVAligned(11, "Dispatched", uiValue(strconv.FormatUint(m.DingoBlockfetchGateDispatched, 10))),
			uiKVAligned(11, "Skip Fast", uiValue(strconv.FormatUint(m.DingoBlockfetchGateSkippedFast, 10))),
			uiKVAligned(11, "No Peer", uiValue(strconv.FormatUint(m.DingoBlockfetchGateSkippedPeer, 10))),
		)
		fmt.Fprintf(&sb, " %s %s\n",
			uiLabel("Gate Mix"),
			uiSegmentBar([]uiSegment{
				{Label: "dispatch", Value: float64(m.DingoBlockfetchGateDispatched), Severity: uiSeverityOK},
				{Label: "fast", Value: float64(m.DingoBlockfetchGateSkippedFast), Severity: uiSeverityMuted},
				{Label: "no_peer", Value: float64(m.DingoBlockfetchGateSkippedPeer), Severity: uiSeverityWarn},
			}, 24),
		)
	}

	if m.BlockDelay > 0 || m.BlocksW1s > 0 || m.BlocksW3s > 0 || m.BlocksW5s > 0 ||
		m.BlocksServed > 0 || m.BlocksLate > 0 {
		sb.WriteString(uiSection("Fetch Timing"))
		blk1s := formatBlockPropagationPercent(m.BlocksW1s)
		blk3s := formatBlockPropagationPercent(m.BlocksW3s)
		blk5s := formatBlockPropagationPercent(m.BlocksW5s)
		blk1Pct, _ := strconv.ParseFloat(blk1s, 64)
		blk3Pct, _ := strconv.ParseFloat(blk3s, 64)
		blk5Pct, _ := strconv.ParseFloat(blk5s, 64)
		delaySeverity := uiSeverityOK
		if m.BlockDelay >= 5 {
			delaySeverity = uiSeverityCritical
		} else if m.BlockDelay >= 3 {
			delaySeverity = uiSeverityWarn
		}
		lateSeverity := dingoCounterSeverity(m.BlocksLate, uiSeverityWarn)
		fmt.Fprintf(&sb, " %s   %s   %s\n",
			uiKVAligned(
				10,
				"Last",
				uiSeverityValue(fmt.Sprintf("%.2fs", m.BlockDelay), delaySeverity),
			),
			uiKVAligned(10, "Served", uiValue(strconv.FormatUint(m.BlocksServed, 10))),
			uiKVAligned(10, "Late >5s", formatSeverityCount(m.BlocksLate, lateSeverity)),
		)
		fmt.Fprintf(&sb, " %s   %s   %s\n",
			uiPercentBar("<=1s", blk1Pct, dingoCacheSeverity(blk1Pct, true), 8),
			uiPercentBar("<=3s", blk3Pct, dingoCacheSeverity(blk3Pct, true), 8),
			uiPercentBar("<=5s", blk5Pct, dingoCacheSeverity(blk5Pct, true), 8),
		)
		fmt.Fprintf(&sb, " %s %s   %s\n",
			uiLabel("CDF Shape"),
			uiSparkline(
				[]float64{blk1Pct, blk3Pct, blk5Pct},
				100,
				dingoCacheSeverity(blk5Pct, true),
			),
			uiMuted("1s 3s 5s"),
		)
	}
	return sb.String()
}

func formatDingoProtocolLatency(sum float64, count uint64) string {
	if count == 0 {
		return "n/a"
	}
	latencyMs := sum / float64(count) * 1000
	if latencyMs < 1 {
		return fmt.Sprintf("%.2fms", latencyMs)
	}
	if latencyMs < 100 {
		return fmt.Sprintf("%.1fms", latencyMs)
	}
	return fmt.Sprintf("%.0fms", latencyMs)
}

func formatBlockPropagationPercent(value float64) string {
	if getEffectiveNodeBinary() != DINGO_BINARY && value <= 1 {
		value *= 100
	}
	return fmt.Sprintf("%.2f", value)
}

func currentNodeVersionInfo() (string, string) {
	if promMetrics != nil && getEffectiveNodeBinary() == DINGO_BINARY {
		version := strings.TrimSpace(promMetrics.DingoBuildVersion)
		revision := strings.TrimSpace(promMetrics.DingoBuildCommit)
		if version != "" {
			version, revision = normalizeDingoBuildInfo(version, revision)
			if revision == "" {
				revision = "metrics"
			}
			if len(revision) > 8 {
				revision = revision[:8]
			}
			return version, revision
		}
	}
	nodeVersion, nodeRevision, _ := getNodeVersion()
	return nodeVersion, nodeRevision
}

func normalizeDingoBuildInfo(version, revision string) (string, string) {
	version = strings.TrimSpace(version)
	revision = strings.TrimSpace(revision)
	commitStart := strings.Index(version, "(commit ")
	if commitStart < 0 {
		return version, revision
	}
	commitText := version[commitStart+len("(commit "):]
	if commitEnd := strings.Index(commitText, ")"); commitEnd >= 0 {
		commitText = commitText[:commitEnd]
	}
	if revision == "" {
		revision = strings.TrimSpace(commitText)
	}
	version = strings.TrimSpace(version[:commitStart])
	return version, revision
}

func getNodeText(ctx context.Context) string {
	select {
	case <-ctx.Done():
		return ""
	default:
	}

	nodeVersion, nodeRevision := currentNodeVersionInfo()
	var sb strings.Builder
	fmt.Fprintf(&sb, " %s\n", uiPill("name", getEffectiveNodeName(), uiSeverityNeutral))
	fmt.Fprintf(&sb, " %s   %s\n",
		uiKV("Role", uiValue(role)),
		uiKV("Network", uiValue(currentNetworkName())),
	)
	fmt.Fprintf(&sb, " %s %s%s%s\n",
		uiLabel("Version"),
		uiValue(nodeVersion),
		uiUnit(" @ "),
		uiMuted(nodeRevision),
	)
	if publicIP != nil {
		fmt.Fprintf(&sb, " %s\n", uiKV("Public IP", uiValue(publicIP.String())))
	} else {
		fmt.Fprintln(&sb)
	}
	fmt.Fprintf(&sb, " %s\n", uiKV("Uptime", uiValue(timeFromSeconds(uptimes))))
	return sb.String()
}

func getPeerText(ctx context.Context) string {
	select {
	case <-ctx.Done():
		return ""
	default:
	}

	var sb strings.Builder

	// Style / UI
	width := 52

	peersFilteredMu.RLock()
	peerCount := len(peersFiltered)
	peersFilteredMu.RUnlock()

	peerStatsMu.Lock()
	rttCount := len(peerStats.RTTresultsSlice)
	cnt0 := peerStats.CNT0
	cnt1 := peerStats.CNT1
	cnt2 := peerStats.CNT2
	cnt3 := peerStats.CNT3
	cnt4 := peerStats.CNT4
	pct1 := peerStats.PCT1
	pct2 := peerStats.PCT2
	pct3 := peerStats.PCT3
	pct4 := peerStats.PCT4
	rttAvg := peerStats.RTTAVG
	inFlight := len(peerStats.InFlight)
	peers := slices.Clone(peerStats.RTTresultsSlice)
	peerStatsMu.Unlock()
	peers = sortedPeersForDisplay(peers)

	if promMetrics != nil {
		sb.WriteString(uiSection("Selection"))
		fmt.Fprintf(&sb, " %s   %s   %s\n",
			uiKV("Known", uiValue(strconv.FormatUint(promMetrics.PeersKnown, 10))),
			uiKV("Established", uiValue(strconv.FormatUint(promMetrics.PeersEstablished, 10))),
			uiKV("Active", uiValue(strconv.FormatUint(promMetrics.PeersActive, 10))),
		)
		fmt.Fprintf(&sb, " %s   %s   %s\n",
			uiKV("Hot", uiValue(strconv.FormatUint(promMetrics.PeersHot, 10))),
			uiKV("Warm", uiValue(strconv.FormatUint(promMetrics.PeersWarm, 10))),
			uiKV("Cold", uiValue(strconv.FormatUint(promMetrics.PeersCold, 10))),
		)
		if promMetrics.DingoPeerPromotionsLedger > 0 ||
			promMetrics.DingoPeerDemotionsLedger > 0 ||
			promMetrics.PeerWarmPromotions > 0 ||
			promMetrics.PeerWarmDemotions > 0 {
			fmt.Fprintf(&sb, " %s   %s\n",
				uiKV("Promoted", uiValue(strconv.FormatUint(
					promMetrics.DingoPeerPromotionsLedger+promMetrics.PeerWarmPromotions,
					10,
				))),
				uiKV("Demoted", uiValue(strconv.FormatUint(
					promMetrics.DingoPeerDemotionsLedger+promMetrics.PeerWarmDemotions,
					10,
				))),
			)
		}
	}

	if peerCount == 0 {
		fmt.Fprintf(&sb, " %s %s%s%s\n",
			uiStatusGlyph(uiSeverityWarn),
			uiLabel("RTT scan"),
			uiValue(strconv.Itoa(rttCount)),
			uiUnit("/"+strconv.Itoa(peerCount)+" scanned"),
		)
		scrollPeers = false
		return sb.String()
	}
	if rttCount < peerCount {
		fmt.Fprintf(&sb, " %s %s%s%s\n",
			uiStatusGlyph(uiSeverityWarn),
			uiLabel("RTT scan"),
			uiValue(strconv.Itoa(rttCount)),
			uiUnit("/"+strconv.Itoa(peerCount)+" scanned, "+
				strconv.Itoa(inFlight)+" probing"),
		)
		if rttCount == 0 {
			scrollPeers = false
			return sb.String()
		}
	}

	sb.WriteString(uiSection("RTT Distribution"))
	fmt.Fprintf(&sb, " %s %s\n",
		uiLabel("Latency Mix"),
		uiSegmentBar([]uiSegment{
			{Label: "0-50", Value: float64(cnt1), Severity: uiSeverityOK},
			{Label: "50-100", Value: float64(cnt2), Severity: uiSeverityWarn},
			{Label: "100-200", Value: float64(cnt3), Severity: uiSeverityCritical},
			{Label: ">200", Value: float64(cnt4), Severity: uiSeverityCritical},
			{Label: "unknown", Value: float64(cnt0), Severity: uiSeverityMuted},
		}, 22),
	)
	fmt.Fprintf(&sb, " %s %5s  %s\n",
		uiPercentBar("0-50ms", float64(pct1), uiSeverityOK, 22),
		strconv.Itoa(cnt1),
		uiUnit("peers"),
	)
	fmt.Fprintf(&sb, " %s %5s  %s\n",
		uiPercentBar("50-100ms", float64(pct2), uiSeverityWarn, 22),
		strconv.Itoa(cnt2),
		uiUnit("peers"),
	)
	fmt.Fprintf(&sb, " %s %5s  %s\n",
		uiPercentBar("100-200ms", float64(pct3), uiSeverityCritical, 22),
		strconv.Itoa(cnt3),
		uiUnit("peers"),
	)
	fmt.Fprintf(&sb, " %s %5s  %s\n",
		uiPercentBar(">200ms", float64(pct4), uiSeverityCritical, 22),
		strconv.Itoa(cnt4),
		uiUnit("peers"),
	)

	// Divider
	sb.WriteString(uiMuted(strings.Repeat("-", width-1)) + "\n")

	avgSeverity := uiSeverityOK
	if rttAvg >= RTTThreshold3 {
		avgSeverity = uiSeverityCritical
	} else if rttAvg >= RTTThreshold2 {
		avgSeverity = uiSeverityCritical
	} else if rttAvg >= RTTThreshold1 {
		avgSeverity = uiSeverityWarn
	}
	unknownSeverity := dingoIntCounterSeverity(cnt0, uiSeverityWarn)
	fmt.Fprintf(&sb, " %s   %s   %s %s\n",
		uiKV("Total", uiValue(strconv.Itoa(peerCount))),
		uiKV("Unknown", formatSeverityIntCount(cnt0, unknownSeverity)),
		uiLabel("Average RTT"),
		uiSeverityValue(strconv.Itoa(rttAvg), avgSeverity)+uiUnit(" ms"),
	)
	fmt.Fprintf(&sb, " %s\n", uiKV("Sort", uiValue(peerSortModeLabel())))

	// Divider
	sb.WriteString(uiMuted(strings.Repeat("-", width-1)) + "\n")

	fmt.Fprintf(&sb, "   %s %24s  I/O RTT   Geolocation\n", uiLabel("#"), "REMOTE PEER")
	// peerLocationWidth := width - 41
	for peerNbr, peer := range peers {
		peerNbr++
		peerRTT := peer.RTT
		peerDIR := peer.Direction
		peerEndpoint := shortenString(formatPeerEndpoint(peer), 25)
		peerLocationFmt := peer.Location

		// Set color
		color := "fuchsia"
		if peerRTT < RTTThreshold1 {
			color = "green"
		} else if peerRTT < RTTThreshold2 {
			color = "yellow"
		} else if peerRTT < RTTThreshold3 {
			color = "red"
		}
		if peerRTT < RTTUnreachable {
			fmt.Fprintf(&sb, " %3d %25s %-3s ["+color+"]%-5d[white] %s\n",
				peerNbr,
				peerEndpoint,
				peerDIR,
				peerRTT,
				peerLocationFmt)
		} else {
			fmt.Fprintf(&sb, " %3d %25s %-3s [fuchsia]%-5s[white] %s\n",
				peerNbr,
				peerEndpoint,
				peerDIR,
				"---",
				peerLocationFmt)
		}
	}
	sb.WriteString("[white]\n")

	failCount.Store(0)
	return sb.String()
}

func getResourceText(ctx context.Context) string {
	if processMetrics == nil || promMetrics == nil {
		return resourceText
	}

	var sb strings.Builder

	cpuPercent := 0.0
	var rss uint64 = 0
	var err error
	var processMemory *process.MemoryInfoStat
	if processMetrics != nil && processMetrics.Pid != 0 {
		cpuPercent, err = processMetrics.CPUPercentWithContext(ctx)
		if err != nil {
			failCount.Add(1)
			return fmt.Sprintf("cannot parse CPU usage: %s", err)
		}
		processMemory, err = processMetrics.MemoryInfoWithContext(ctx)
		if err != nil {
			failCount.Add(1)
			return fmt.Sprintf("cannot parse memory usage: %s", err)
		}
		rss = processMemory.RSS
	}

	memRss := formatMemoryBytes(rss)

	var memLiveStr, memHeapStr string
	if getEffectiveNodeBinary() == DINGO_BINARY {
		memLiveStr = formatMemoryBytes(promMetrics.GoHeapInuse)
		memHeapStr = formatMemoryBytes(promMetrics.GoHeapSys)
	} else {
		memLiveStr = formatMemoryBytes(promMetrics.MemLive)
		memHeapStr = formatMemoryBytes(promMetrics.MemHeap)
	}

	cpuSeverity := uiSeverityOK
	if cpuPercent >= 90 {
		cpuSeverity = uiSeverityCritical
	} else if cpuPercent >= 70 {
		cpuSeverity = uiSeverityWarn
	}
	fmt.Fprintf(&sb, " %s %s   %s\n",
		uiLabel("CPU"),
		uiSeverityValue(fmt.Sprintf("%.2f%%", cpuPercent), cpuSeverity),
		uiProgressBar(cpuPercent, 14, cpuSeverity),
	)
	fmt.Fprintf(&sb, " %s   %s\n",
		uiKV("Mem Live", uiValue(memLiveStr)),
		uiKV("RSS", uiValue(memRss)),
	)
	fmt.Fprintf(&sb, " %s\n",
		uiKV("Heap", uiValue(memHeapStr)),
	)
	var gcMinor, gcMajor uint64
	if getEffectiveNodeBinary() == DINGO_BINARY {
		gcMinor = 0
		gcMajor = promMetrics.GoGcCount
	} else {
		gcMinor = promMetrics.GcMinor
		gcMajor = promMetrics.GcMajor
	}

	fmt.Fprintf(&sb, " %s   %s\n",
		uiKV("GC Minor", uiValue(strconv.FormatUint(gcMinor, 10))),
		uiKV("GC Major", uiValue(strconv.FormatUint(gcMajor, 10))),
	)
	return sb.String()
}

func getProcessMetrics(ctx context.Context) (*process.Process, error) {
	cfg := config.GetConfig()

	// If CARDANO_NODE_PID is specified, use it directly for all node types
	if cfg.Node.Pid > 0 && getEffectiveNodeBinary() != DINGO_BINARY {
		return getProcessMetricsByPid(ctx, cfg.Node.Pid)
	}

	switch getEffectiveNodeBinary() {
	case AMARU_BINARY:
		return getProcessMetricsByPidFile(ctx, cfg)
	case DINGO_BINARY:
		return findDingoProcess(ctx, cfg, defaultProcLookups())
	default:
		return getProcessMetricsByNameAndPort(ctx, cfg)
	}
}

type procLookups struct {
	socketOwner func(ctx context.Context, host string, port uint32) (int32, error)
	listProcs   func(ctx context.Context, name string) ([]*process.Process, error)
}

func defaultProcLookups() procLookups {
	return procLookups{
		socketOwner: findTCPSocketOwner,
		listProcs:   listProcessesByName,
	}
}

func findDingoProcess(
	ctx context.Context,
	cfg *config.Config,
	lookups procLookups,
) (*process.Process, error) {
	if cfg.Node.Pid > 0 {
		proc, err := getProcessMetricsByPid(ctx, cfg.Node.Pid)
		if err == nil {
			logDingoProcessSelection(proc.Pid, "explicit-pid")
		}
		return proc, err
	}

	// First try the process listening on the Prometheus metrics port, because
	// that is the process nview is reading metrics from.
	if lookups.socketOwner != nil {
		pid, err := lookups.socketOwner(
			ctx,
			cfg.Prometheus.Host,
			cfg.Prometheus.Port,
		)
		if err == nil && pid > 0 {
			proc, err := getProcessMetricsByPid(ctx, pid)
			if err == nil && processNameContains(ctx, proc, DINGO_BINARY) {
				logDingoProcessSelection(proc.Pid, "socket-owner")
				return proc, nil
			}
		}
	}

	// If socket ownership is unavailable, inspect named Dingo processes and
	// select the one that declares the configured metrics port.
	if lookups.listProcs != nil {
		processes, err := lookups.listProcs(ctx, DINGO_BINARY)
		if err == nil && len(processes) > 0 {
			candidates := inspectDingoCandidates(ctx, processes)
			portMatches := dingoCandidatesByMetricsPort(
				candidates,
				cfg.Prometheus.Port,
			)
			if len(portMatches) == 1 {
				proc := portMatches[0].proc
				logDingoProcessSelection(proc.Pid, "port-match")
				return proc, nil
			}
			if len(portMatches) > 1 {
				selected, ok := lowestPIDDingoCandidate(portMatches)
				if !ok {
					return nil, errors.New("no dingo process found")
				}
				logDingoCandidateAmbiguity(
					fmt.Sprintf(
						"multiple dingo processes declared metrics-port=%d, picked lowest pid=%d",
						cfg.Prometheus.Port,
						selected.proc.Pid,
					),
					selected,
					portMatches,
					cfg.Prometheus.Port,
				)
				logDingoProcessSelection(selected.proc.Pid, "port-match")
				return selected.proc, nil
			}

			// If no process declares the scrape port, use a deterministic
			// name-only fallback and warn when there is real ambiguity.
			selected, ok := lowestPIDDingoCandidate(candidates)
			if !ok {
				return nil, errors.New("no dingo process found")
			}
			if len(candidates) > 1 {
				logDingoCandidateAmbiguity(
					fmt.Sprintf(
						"multiple dingo processes found, none declared metrics-port=%d, picked lowest pid=%d",
						cfg.Prometheus.Port,
						selected.proc.Pid,
					),
					selected,
					candidates,
					cfg.Prometheus.Port,
				)
			}
			logDingoProcessSelection(selected.proc.Pid, "name-only")
			return selected.proc, nil
		}
	}

	// As a final fallback, try PID 1 for containers where Dingo is the only
	// process but process listing did not find it.
	proc, err := getProcessMetricsByPid(ctx, 1)
	if err == nil {
		logDingoProcessSelection(proc.Pid, "pid-1-fallback")
	}
	return proc, err
}

type dingoCandidate struct {
	proc        *process.Process
	metricsPort string
	dataDir     string
}

// inspectDingoCandidates reads process metadata used to disambiguate multiple
// Dingo instances without failing when cmdline or env access is denied.
func inspectDingoCandidates(
	ctx context.Context,
	processes []*process.Process,
) []dingoCandidate {
	candidates := make([]dingoCandidate, 0, len(processes))
	for _, proc := range processes {
		candidate := dingoCandidate{
			proc:        proc,
			metricsPort: "-",
			dataDir:     "-",
		}

		if env, err := proc.EnvironWithContext(ctx); err == nil {
			if metricsPort := valueFromEnv(env, "DINGO_METRICS_PORT"); metricsPort != "" {
				candidate.metricsPort = metricsPort
			}
		}

		if args, err := proc.CmdlineSliceWithContext(ctx); err == nil {
			if metricsPort := valueFromArgs(args, "--metrics-port"); metricsPort != "" {
				candidate.metricsPort = metricsPort
			}
			if dataDir := valueFromArgs(args, "--data-dir"); dataDir != "" {
				candidate.dataDir = dataDir
			}
		}

		candidates = append(candidates, candidate)
	}
	return candidates
}

func dingoCandidatesByMetricsPort(
	candidates []dingoCandidate,
	port uint32,
) []dingoCandidate {
	expected := strconv.FormatUint(uint64(port), 10)
	matches := make([]dingoCandidate, 0, len(candidates))
	for _, candidate := range candidates {
		if candidate.metricsPort == expected {
			matches = append(matches, candidate)
		}
	}
	return matches
}

func lowestPIDDingoCandidate(candidates []dingoCandidate) (dingoCandidate, bool) {
	if len(candidates) == 0 {
		return dingoCandidate{}, false
	}
	selected := candidates[0]
	for _, candidate := range candidates[1:] {
		if candidate.proc.Pid < selected.proc.Pid {
			selected = candidate
		}
	}
	return selected, true
}

func processNameContains(
	ctx context.Context,
	proc *process.Process,
	name string,
) bool {
	if procName, err := proc.NameWithContext(ctx); err == nil &&
		strings.Contains(procName, name) {
		return true
	}
	args, err := proc.CmdlineSliceWithContext(ctx)
	if err != nil {
		return false
	}
	for _, arg := range args {
		if strings.Contains(arg, name) {
			return true
		}
	}
	return false
}

func valueFromArgs(args []string, flag string) string {
	result := ""
	for i, arg := range args {
		if arg == flag && i+1 < len(args) {
			result = args[i+1]
		} else if strings.HasPrefix(arg, flag+"=") {
			result = strings.TrimPrefix(arg, flag+"=")
		}
	}
	return result
}

func valueFromEnv(env []string, name string) string {
	prefix := name + "="
	for _, item := range env {
		if strings.HasPrefix(item, prefix) {
			return strings.TrimPrefix(item, prefix)
		}
	}
	return ""
}

func logDingoProcessSelection(pid int32, method string) {
	if logger == nil {
		return
	}
	if !dingoProcessSelectionLogged.CompareAndSwap(false, true) {
		return
	}
	logger.Info(
		"selected dingo process",
		"pid",
		pid,
		"method",
		method,
	)
}

func logDingoCandidateAmbiguity(
	message string,
	selected dingoCandidate,
	candidates []dingoCandidate,
	port uint32,
) {
	if logger == nil {
		return
	}
	if !dingoProcessAmbiguityLogged.CompareAndSwap(false, true) {
		return
	}
	logger.Warn(
		message,
		"metrics-port",
		port,
		"picked-pid",
		selected.proc.Pid,
		"candidates",
		formatDingoCandidates(candidates),
	)
}

func formatDingoCandidates(candidates []dingoCandidate) string {
	parts := make([]string, 0, len(candidates))
	for _, candidate := range candidates {
		parts = append(
			parts,
			fmt.Sprintf(
				"pid=%d metrics-port=%s data-dir=%s",
				candidate.proc.Pid,
				candidate.metricsPort,
				candidate.dataDir,
			),
		)
	}
	return "[" + strings.Join(parts, "] [") + "]"
}

// findTCPSocketOwner returns the PID that owns the configured Prometheus TCP
// listener, if the OS exposes that socket ownership information.
func findTCPSocketOwner(
	ctx context.Context,
	host string,
	port uint32,
) (int32, error) {
	connections, err := netutil.ConnectionsWithContext(ctx, "tcp")
	if err != nil {
		return 0, err
	}
	for _, conn := range connections {
		if conn.Status != "LISTEN" || conn.Laddr.Port != port || conn.Pid <= 0 {
			continue
		}
		if tcpListenAddrMatches(conn.Laddr.IP, host) {
			return conn.Pid, nil
		}
	}
	return 0, fmt.Errorf("no tcp listener found on %s:%d", host, port)
}

// tcpListenAddrMatches treats wildcard listener addresses as matching any
// scrape host, while still supporting exact and parsed IP comparisons. It does
// not resolve arbitrary hostnames; gopsutil normally reports listener addresses
// as IP literals.
func tcpListenAddrMatches(listenHost, scrapeHost string) bool {
	listenIP := net.ParseIP(listenHost)
	if listenIP != nil && listenIP.IsUnspecified() {
		return true
	}
	if listenHost == scrapeHost {
		return true
	}
	scrapeIP := net.ParseIP(scrapeHost)
	if listenIP == nil || scrapeIP == nil {
		if scrapeHost == "localhost" && listenIP != nil {
			return listenIP.IsLoopback()
		}
		return false
	}
	return listenIP.Equal(scrapeIP)
}

func listProcessesByName(
	ctx context.Context,
	name string,
) ([]*process.Process, error) {
	processes, err := process.ProcessesWithContext(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get processes: %w", err)
	}
	matches := []*process.Process{}
	for _, proc := range processes {
		procName, err := proc.NameWithContext(ctx)
		if err != nil {
			continue
		}
		if strings.Contains(procName, name) {
			matches = append(matches, proc)
		}
	}
	return matches, nil
}

func getProcessMetricsByPid(
	ctx context.Context,
	pid int32,
) (*process.Process, error) {
	proc, err := process.NewProcessWithContext(ctx, pid)
	if err != nil {
		return nil, fmt.Errorf("failed to get process %d: %w", pid, err)
	}

	exists, err := proc.IsRunning()
	if err != nil {
		return nil, fmt.Errorf(
			"failed to check if process %d is running: %w",
			pid,
			err,
		)
	}

	if !exists {
		return nil, fmt.Errorf("process %d is not running", pid)
	}

	return proc, nil
}

func getProcessMetricsByPidFile(
	ctx context.Context,
	cfg *config.Config,
) (*process.Process, error) {
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	default:
	}

	data, err := os.ReadFile(cfg.Node.PidFile)
	if err != nil {
		return nil, fmt.Errorf("failed to read pid file: %w", err)
	}
	pidStr := strings.TrimSpace(string(data))
	pid, err := strconv.Atoi(pidStr)
	if err != nil {
		return nil, fmt.Errorf("invalid pid in pid file: %w", err)
	}
	if pid <= 0 || pid > math.MaxInt32 {
		return nil, fmt.Errorf("invalid pid %d: out of int32 range", pid)
	}
	//nolint:gosec
	proc, err := process.NewProcessWithContext(ctx, int32(pid))
	if err != nil {
		return nil, fmt.Errorf("failed to get process %d: %w", pid, err)
	}

	exists, err := proc.IsRunning()
	if err != nil {
		return nil, fmt.Errorf(
			"failed to check if process %d is running: %w",
			pid,
			err,
		)
	}

	if !exists {
		return nil, fmt.Errorf("process %d is not running", pid)
	}

	return proc, nil
}

func getProcessMetricsByNameAndPort(
	ctx context.Context,
	cfg *config.Config,
) (*process.Process, error) {
	r, _ := process.NewProcessWithContext(ctx, 0)
	processes, err := process.ProcessesWithContext(ctx)
	if err != nil {
		return r, fmt.Errorf("failed to get processes: %w", err)
	}
	nodePort := strconv.FormatUint(uint64(cfg.Node.Port), 10)
	for _, p := range processes {
		n, err := p.NameWithContext(ctx)
		if err != nil {
			continue
		}

		c, err := p.CmdlineWithContext(ctx)
		if err != nil {
			continue
		}

		if strings.Contains(n, getEffectiveNodeBinary()) &&
			strings.Contains(c, nodePort) {
			r = p
		}
	}

	return r, nil
}

func tcpinfoRtt(address string) int {
	result := RTTUnreachable
	// Get a connection and setup our error channels
	conn, err := net.DialTimeout("tcp", address, 3*time.Second)
	if err != nil {
		return result
	}
	if conn == nil {
		return result
	}
	defer conn.Close()
	tc, err := tcp.NewConn(conn)
	if err != nil {
		return result
	}
	var o tcpinfo.Info
	var b [256]byte
	i, err := tc.Option(o.Level(), o.Name(), b[:])
	if err != nil {
		return result
	}
	txt, err := json.Marshal(i)
	if err != nil {
		return result
	}
	q := &tcpinfo.Info{}
	if err := json.Unmarshal(txt, &q); err != nil {
		return result
	}
	result = int(q.RTT.Seconds() * 1000)
	return result
}
