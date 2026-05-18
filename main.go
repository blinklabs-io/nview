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
	TerminalWidthDefault   = 71
	ProgressBarGranularity = 68
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
)

type secondaryView int

const (
	viewNone secondaryView = iota
	viewPeers
	viewDingo
)

const (
	viewNoneValue  int32 = int32(viewNone)
	viewPeersValue int32 = int32(viewPeers)
	viewDingoValue int32 = int32(viewDingo)
)

var (
	activeSecondary     atomic.Int32
	secondaryDefaultSet atomic.Bool
	lastDingoSample     *PromMetrics
	lastDingoSampleAt   time.Time
	lastDingoRateBase   *PromMetrics
	lastDingoRateBaseAt time.Time
	lastDingoSampleSrc  *PromMetrics
	lastDingoSampleMu   sync.Mutex
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
var flex = tview.NewFlex()

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
	SetTextColor(tcell.ColorGreen)

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
var blockText, chainText, coreText, connectionText, nodeText, peerText, resourceText string

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

func getSecondaryViewText(ctx context.Context) (string, string) {
	switch getActiveSecondaryView() {
	case viewPeers:
		return "Peers", getPeerText(ctx)
	case viewDingo:
		return "Dingo Diagnostics", getDingoStats()
	case viewNone:
		return "Secondary", ""
	default:
		return "Secondary", ""
	}
}

func updateSecondaryText(ctx context.Context) {
	title, tmpText := getSecondaryViewText(ctx)
	peerTextView.SetTitle(title)
	if tmpText != peerText {
		peerText = tmpText
		peerTextView.Clear()
		peerTextView.SetText(peerText)
		if scrollPeers {
			scrollPeers = false
			peerTextView.ScrollToBeginning()
		}
	}
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
			case <-time.After(time.Second * 1):
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
			err := pingPeers(ctx)
			if err != nil {
				logger.Warn("Failed to ping peers", "error", err)
				failCount.Add(1)
				select {
				case <-ctx.Done():
					return
				case <-time.After(time.Second * 10):
				}
				continue
			}
			select {
			case <-ctx.Done():
				return
			case <-time.After(time.Second * 10):
			}
		}
	}()

	// Populate initial text from metrics
	nodeText = getNodeText(ctx)
	nodeTextView.SetText(nodeText).SetTitle("Node").SetBorder(true)

	resourceText = getResourceText(ctx)
	resourceTextView.SetText(resourceText).SetTitle("Resources").SetBorder(true)

	connectionText = getConnectionText(ctx)
	connectionTextView.SetText(connectionText).
		SetTitle("Connections").
		SetBorder(true)

	coreText = getCoreText(ctx)
	coreTextView.SetText(coreText).SetTitle("Core").SetBorder(true)

	chainText = fmt.Sprintf("%s%s", getEpochText(ctx), getChainText(ctx))
	chainTextView.SetText(chainText).SetTitle("Chain").SetBorder(true)

	blockText = getBlockText(ctx)
	blockTextView.SetText(blockText).
		SetTitle("Block Propagation").
		SetBorder(true)

	peerTitle, peerTextValue := getSecondaryViewText(ctx)
	peerText = peerTextValue
	peerTextView.SetText(peerText).SetTitle(peerTitle).SetBorder(true)

	// Set our footer
	defaultFooterText := " [yellow](esc/q)[white] Quit | [yellow](p)[white] Peers | [yellow](d)[white] Dingo"
	footerTextView.SetText(defaultFooterText)

	// Add content to our flex box
	layout := tview.NewFlex()
	leftSide := tview.NewFlex()
	middleSide := tview.NewFlex()
	flex.SetDirection(tview.FlexRow).
		// Row 1 is our application header
		AddItem(headerTextView.SetText(fmt.Sprintln(" > nview -", version.GetVersionString())),
			1,
			1,
			false).

		// Row 2 is our main text section, and its own flex
		AddItem(layout.
			AddItem(leftSide.SetDirection(tview.FlexRow).
				// Node
				AddItem(nodeTextView,
					8,
					0,
					false).
				// Resources
				AddItem(resourceTextView,
					8,
					0,
					false).
				// Connections
				AddItem(connectionTextView,
					11,
					0,
					false),
				37,
				1,
				false).
			AddItem(middleSide.SetDirection(tview.FlexRow).
				// Chain
				AddItem(chainTextView,
					8,
					1,
					false).
				// Block
				AddItem(blockTextView,
					4,
					0,
					false).
				// Peers
				AddItem(peerTextView,
					0,
					3,
					true),
				74,
				2,
				true),
			0,
			6,
			true).
		// Row 3 is our footer
		AddItem(footerTextView, 2, 0, false)

	// Core
	if role == "Core" {
		leftSide.AddItem(coreTextView, 0, 1, false)
	} else {
		leftSide.AddItem(nil, 0, 1, false)
	}
	// TODO: another section + data
	// layout.AddItem(tview.NewBox().SetBorder(true).SetTitle("Coming Soon"), 22, 1, false)
	peerStats.RTTresultsMap = make(map[string]*Peer)
	peerStats.RTTresultsSlice = []*Peer{}

	// capture inputs
	flex.SetInputCapture(func(event *tcell.EventKey) *tcell.EventKey {
		if event.Rune() == 104 || event.Rune() == 114 { // h or r
			setRole()
			resetPeers()
			footerTextView.Clear()
			footerTextView.SetText(defaultFooterText)
			var tmpText string
			tmpText = getNodeText(ctx)
			if tmpText != "" && tmpText != nodeText {
				nodeText = tmpText
				nodeTextView.Clear()
				nodeTextView.SetText(nodeText)
			}
			tmpText = getResourceText(ctx)
			if tmpText != "" && tmpText != resourceText {
				resourceText = tmpText
				resourceTextView.Clear()
				resourceTextView.SetText(resourceText)
			}
			tmpText = getConnectionText(ctx)
			if tmpText != "" && tmpText != connectionText {
				connectionText = tmpText
				connectionTextView.Clear()
				connectionTextView.SetText(connectionText)
			}
			tmpText = getCoreText(ctx)
			if tmpText != "" && tmpText != coreText {
				coreText = tmpText
				coreTextView.Clear()
				coreTextView.SetText(coreText)
			}
			tmpText = fmt.Sprintf(
				"%s\n%s",
				getEpochText(ctx),
				getChainText(ctx),
			)
			if tmpText != "" && tmpText != chainText {
				chainText = tmpText
				chainTextView.Clear()
				chainTextView.SetText(chainText)
			}
			tmpText = getBlockText(ctx)
			if tmpText != "" && tmpText != blockText {
				blockText = tmpText
				blockTextView.Clear()
				blockTextView.SetText(blockText)
			}
			updateSecondaryText(ctx)
		}
		if event.Rune() == 112 { // p
			resetPeers()
			if getActiveSecondaryView() == viewPeers {
				setActiveSecondaryView(viewNone)
			} else {
				setActiveSecondaryView(viewPeers)
			}
			scrollPeers = false
			updateSecondaryText(ctx)
		}
		if event.Rune() == 100 { // d
			if getEffectiveNodeBinary() != DINGO_BINARY {
				if logger != nil {
					logger.Debug(
						"ignoring dingo diagnostics keypress for non-dingo node",
						"binary",
						getEffectiveNodeBinary(),
					)
				}
			} else if getActiveSecondaryView() == viewDingo {
				setActiveSecondaryView(viewNone)
			} else {
				setActiveSecondaryView(viewDingo)
			}
			updateSecondaryText(ctx)
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
			var tmpText string
			tmpText = getNodeText(ctx)
			if tmpText != "" && tmpText != nodeText {
				nodeText = tmpText
				nodeTextView.Clear()
				nodeTextView.SetText(nodeText)
			}
			tmpText = getResourceText(ctx)
			if tmpText != "" && tmpText != resourceText {
				resourceText = tmpText
				resourceTextView.Clear()
				resourceTextView.SetText(resourceText)
			}
			tmpText = getConnectionText(ctx)
			if tmpText != "" && tmpText != connectionText {
				connectionText = tmpText
				connectionTextView.Clear()
				connectionTextView.SetText(connectionText)
			}
			tmpText = getCoreText(ctx)
			if tmpText != "" && tmpText != coreText {
				coreText = tmpText
				coreTextView.Clear()
				coreTextView.SetText(coreText)
			}
			tmpText = fmt.Sprintf(
				"%s\n%s",
				getEpochText(ctx),
				getChainText(ctx),
			)
			if tmpText != "" && tmpText != chainText {
				chainText = tmpText
				chainTextView.Clear()
				chainTextView.SetText(chainText)
			}
			tmpText = getBlockText(ctx)
			if tmpText != "" && tmpText != blockText {
				blockText = tmpText
				blockTextView.Clear()
				blockTextView.SetText(blockText)
			}
			updateSecondaryText(ctx)
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

func dingoCounterDelta(curr, prev uint64) uint64 {
	if curr < prev {
		return curr
	}
	return curr - prev
}

func formatDingoHitRatio(currHits, currMiss, prevHits, prevMiss uint64) string {
	hits := dingoCounterDelta(currHits, prevHits)
	misses := dingoCounterDelta(currMiss, prevMiss)
	total := hits + misses
	if total == 0 {
		return "n/a"
	}
	return fmt.Sprintf("%.1f%%", float64(hits)/float64(total)*100)
}

func formatDingoRate(curr, prev uint64, dt time.Duration) string {
	if dt <= 0 {
		return "n/a"
	}
	rate := float64(dingoCounterDelta(curr, prev)) / dt.Seconds()
	return fmt.Sprintf("%.0f", math.Round(rate))
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

	utxoRatio := "n/a"
	txRatio := "n/a"
	blockRatio := "n/a"
	coldExtractRate := "n/a"
	eventErrorRate := "n/a"
	eventTimeoutRate := "n/a"
	if prev != nil {
		utxoRatio = formatDingoHitRatio(
			curr.DingoCacheUtxoHotHits,
			curr.DingoCacheUtxoHotMiss,
			prevMetrics.DingoCacheUtxoHotHits,
			prevMetrics.DingoCacheUtxoHotMiss,
		)
		txRatio = formatDingoHitRatio(
			curr.DingoCacheTxHotHits,
			curr.DingoCacheTxHotMiss,
			prevMetrics.DingoCacheTxHotHits,
			prevMetrics.DingoCacheTxHotMiss,
		)
		blockRatio = formatDingoHitRatio(
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
	fmt.Fprintf(&sb, " [green]DB Size      : [white]%s\n",
		formatDingoBytes(curr.DingoDbSizeBytes))
	fmt.Fprintf(&sb, " [green]Cached Blocks: [white]%d\n",
		curr.DingoChainCachedBlocks)
	fmt.Fprintf(&sb, " [green]Tip Gap      : [white]%d[blue] slots[white]         [green]Forge Gap: [white]%d\n",
		curr.DingoTipGapSlots,
		curr.DingoForgeTipGapSlots)
	fmt.Fprintf(&sb, " [green]CBOR Cache   : [white]utxo %s  tx %s  blk %s\n",
		utxoRatio,
		txRatio,
		blockRatio)
	fmt.Fprintf(&sb, " [green]Cold Extract : [white]%s[blue] / sec\n",
		coldExtractRate)
	fmt.Fprintf(&sb, " [green]Events       : [white]%d[blue] subs[white]   [green]total [white]%d[white]   [green]err [white]%s[blue]/s[white]   [green]timeout [white]%s[blue]/s\n",
		curr.EventSubscribers,
		curr.EventTotal,
		eventErrorRate,
		eventTimeoutRate)
	fmt.Fprintf(&sb, " [green]Slot Clock   : [white]fallback %d   forgeErr %d   syncSkip %d\n",
		curr.DingoSlotClockFallback,
		curr.DingoForgeSlotClockErr,
		curr.DingoForgeSyncSkip)
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

func getEpochText(ctx context.Context) string {
	select {
	case <-ctx.Done():
		return ""
	default:
	}

	var sb strings.Builder

	epochProgress := getEpochProgress()
	epochProgress1dec := fmt.Sprintf("%.1f", epochProgress)

	fmt.Fprintf(&sb, " [green]Epoch: [white]%d[blue] [[white]%s%%[blue]]\n",
		currentEpoch,
		epochProgress1dec)

	// Epoch progress bar
	var epochBar strings.Builder
	granularity := ProgressBarGranularity
	charMarked := string('▌')
	charUnmarked := string('▖')

	epochItems := int(epochProgress) * granularity / 100
	for i := 0; i <= granularity-1; i++ {
		if i < epochItems {
			epochBar.WriteString("[blue]" + charMarked)
		} else {
			epochBar.WriteString("[white]" + charUnmarked)
		}
	}
	fmt.Fprintf(&sb, " [blue]%s[green]\n", epochBar.String())
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

	// Blocks / Slots / Tx

	mempoolTxKBytes := promMetrics.MempoolBytes / 1024
	kWidth := strconv.Itoa(10 -
		len(strconv.FormatUint(promMetrics.MempoolTx, 10)) -
		len(strconv.FormatUint(mempoolTxKBytes, 10)))

	tipRef := getSlotTipRef()
	var tipDiff uint64
	if tipRef < promMetrics.SlotNum {
		tipDiff = 0
	} else {
		tipDiff = tipRef - promMetrics.SlotNum
	}

	// Row 1
	fmt.Fprintf(&sb, " Block      : [white]%-"+strconv.Itoa(10)+"s[green]",
		strconv.FormatUint(promMetrics.BlockNum, 10))
	fmt.Fprintf(&sb, " Tip (ref)  : [white]%-"+strconv.Itoa(10)+"s[green]",
		strconv.FormatUint(tipRef, 10))
	fmt.Fprintf(&sb, " Forks      : [white]%-"+strconv.Itoa(10)+"s[green]\n",
		strconv.FormatUint(promMetrics.Forks, 10))
	// Row 2
	fmt.Fprintf(&sb, " Slot       : [white]%-"+strconv.Itoa(10)+"s[green]",
		strconv.FormatUint(promMetrics.SlotNum, 10))
	if promMetrics.SlotNum == 0 {
		fmt.Fprintf(&sb, " Status     : [white]%-"+strconv.Itoa(
			10,
		)+"s[green]",
			"starting")
	} else if tipDiff <= SyncThresholdGood {
		fmt.Fprintf(&sb, " Tip (diff) : [white]%-"+strconv.Itoa(9)+"s[green]",
			strconv.FormatUint(tipDiff, 10)+" 😀")
	} else if tipDiff <= SyncThresholdSlow {
		fmt.Fprintf(&sb, " Tip (diff) : [yellow]%-"+strconv.Itoa(9)+"s[green]",
			strconv.FormatUint(tipDiff, 10)+" 😐")
	} else {
		var syncProgress float32
		if tipRef == 0 {
			syncProgress = 0.0
		} else {
			syncProgress = float32((float32(promMetrics.SlotNum) / float32(tipRef)) * 100)
		}
		fmt.Fprintf(&sb, " Syncing    : [yellow]%-"+strconv.Itoa(10)+"s[green]",
			fmt.Sprintf("%2.1f", syncProgress))
	}
	fmt.Fprintf(&sb, " Total Tx   : [white]%-"+strconv.Itoa(10)+"s[green]\n",
		strconv.FormatUint(promMetrics.TxProcessed, 10))
	// Row 3
	fmt.Fprintf(&sb, " Slot epoch : [white]%-"+strconv.Itoa(10)+"s[green]",
		strconv.FormatUint(promMetrics.SlotInEpoch, 10))
	fmt.Fprintf(&sb, " Density    : [white]%-"+strconv.Itoa(10)+"s[green]",
		fmt.Sprintf("%3.5f", promMetrics.Density*100/1))
	fmt.Fprintf(&sb, " Pending Tx : [white]%d[blue]/[white]%d[blue]%-"+kWidth+"s\n",
		promMetrics.MempoolTx,
		mempoolTxKBytes,
		"K")
	return sb.String()
}

func getConnectionText(ctx context.Context) string {
	cfg := config.GetConfig()
	var sb strings.Builder

	if p2p {
		if promMetrics == nil {
			return connectionText
		}
		fmt.Fprintf(&sb, " [green]P2P        : %s\n",
			"enabled")
		fmt.Fprintf(&sb, " [green]Incoming   : [white]%s\n",
			strconv.FormatUint(promMetrics.ConnIncoming, 10))
		fmt.Fprintf(&sb, " [green]Outgoing   : [white]%s\n",
			strconv.FormatUint(promMetrics.ConnOutgoing, 10))
		fmt.Fprintf(&sb, " [green]Cold Peers : [white]%s\n",
			strconv.FormatUint(promMetrics.PeersCold, 10))
		fmt.Fprintf(&sb, " [green]Warm Peers : [white]%s\n",
			strconv.FormatUint(promMetrics.PeersWarm, 10))
		fmt.Fprintf(&sb, " [green]Hot Peers  : [white]%s\n",
			strconv.FormatUint(promMetrics.PeersHot, 10))
		fmt.Fprintf(&sb, " [green]Uni-Dir    : [white]%s\n",
			strconv.FormatUint(promMetrics.ConnUniDir, 10))
		fmt.Fprintf(&sb, " [green]Bi-Dir     : [white]%s\n",
			strconv.FormatUint(promMetrics.ConnBiDir, 10))
		fmt.Fprintf(&sb, " [green]FullDuplex : [white]%s\n",
			strconv.FormatUint(promMetrics.ConnFullDuplex, 10))
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

		fmt.Fprintf(&sb, " [green]P2P        : [yellow]%s\n",
			"disabled")
		fmt.Fprintf(&sb, " [green]Incoming   : [white]%s\n",
			strconv.Itoa(len(peersIn)))
		fmt.Fprintf(&sb, " [green]Outgoing   : [white]%s\n",
			strconv.Itoa(len(peersOut)))
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

	// Core section
	if role == "Core" {
		adoptedFmt := "white"
		invalidFmt := "white"
		if promMetrics.IsLeader != promMetrics.Adopted {
			adoptedFmt = "yellow"
		}
		if promMetrics.DidntAdopt != 0 {
			invalidFmt = "red"
		}
		leader := strconv.FormatUint(promMetrics.IsLeader, 10)
		fmt.Fprintf(&sb, " [green]Leader     : [white]%s\n",
			leader)
		adopted := strconv.FormatUint(promMetrics.Adopted, 10)
		fmt.Fprintf(&sb, " [green]Adopted    : ["+adoptedFmt+"]%s\n",
			adopted)
		invalid := strconv.FormatUint(promMetrics.DidntAdopt, 10)
		fmt.Fprintf(&sb, " [green]Invalid    : ["+invalidFmt+"]%s\n",
			invalid)
		sb.WriteString(" [green]Missed     : ")
		var missedSlotsPct float32
		if promMetrics.AboutToLead > 0 {
			missedSlotsPct = float32(
				promMetrics.MissedSlots,
			) / (float32(promMetrics.AboutToLead + promMetrics.MissedSlots)) * 100
		}
		fmt.Fprintf(&sb, "[white]%s [blue]([white]%s %%[blue])\n",
			strconv.FormatUint(promMetrics.MissedSlots, 10),
			fmt.Sprintf("%.2f", missedSlotsPct))

		sb.WriteString("\n")

		// KES
		fmt.Fprintf(&sb, " [green]KES period : [white]%d\n",
			promMetrics.KesPeriod)
		fmt.Fprintf(&sb, " [green]KES remain : [white]%d\n",
			promMetrics.RemainingKesPeriods)
	} else {
		fmt.Fprintf(&sb, "%18s\n",
			"N/A")
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
		footerTextView.SetText(" [yellow](esc/q) Quit\n")
		return fmt.Sprintf(
			"\n [red]Terminal width too small![white]\n Please increase by [yellow]%d[white] columns\n",
			width-tcols+1,
		)
	}
	// Track the number of lines drawn, using left column as reference
	line := 10
	if line >= (tlines - 1) {
		footerTextView.Clear()
		footerTextView.SetText(" [yellow](esc/q) Quit\n")
		return fmt.Sprintf(
			"\n [red]Terminal height too small![white]\n Please increase by [yellow]%d[white] lines\n",
			line-tlines+2,
		)
	}

	var sb strings.Builder

	blk1s := fmt.Sprintf("%.2f", promMetrics.BlocksW1s*100)
	blk3s := fmt.Sprintf("%.2f", promMetrics.BlocksW3s*100)
	blk5s := fmt.Sprintf("%.2f", promMetrics.BlocksW5s*100)
	delay := fmt.Sprintf("%.2f", promMetrics.BlockDelay)

	// Row 1
	fmt.Fprintf(&sb, " [green]Last Delay : [white]%s[blue]%-"+strconv.Itoa(
		10-len(delay),
	)+"s",
		delay,
		"s")
	fmt.Fprintf(&sb, " [green]Served     : [white]%-"+strconv.Itoa(10)+"s",
		strconv.FormatUint(promMetrics.BlocksServed, 10))
	fmt.Fprintf(&sb, " [green]Late (>5s) : [white]%-"+strconv.Itoa(10)+"s\n",
		strconv.FormatUint(promMetrics.BlocksLate, 10))
	// Row 2
	fmt.Fprintf(&sb, " [green]Within 1s  : [white]%s%-"+strconv.Itoa(10-len(blk1s))+"s",
		blk1s,
		"%")
	fmt.Fprintf(&sb, " [green]Within 3s  : [white]%s%-"+strconv.Itoa(10-len(blk3s))+"s",
		blk3s,
		"%")
	fmt.Fprintf(&sb, " [green]Within 5s  : [white]%s%-"+strconv.Itoa(
		10-len(blk5s),
	)+"s\n",
		blk5s,
		"%")

	failCount.Store(0)
	return sb.String()
}

func getNodeText(ctx context.Context) string {
	select {
	case <-ctx.Done():
		return ""
	default:
	}

	cfg := config.GetConfig()
	var network string
	if cfg.App.Network != "" {
		network = strings.ToUpper(cfg.App.Network[:1]) + cfg.App.Network[1:]
	} else {
		network = strings.ToUpper(cfg.Node.Network[:1]) + cfg.Node.Network[1:]
	}
	nodeVersion, nodeRevision, _ := getNodeVersion()
	var sb strings.Builder
	fmt.Fprintf(&sb, " [green]Name       : [white]%s\n", getEffectiveNodeName())
	fmt.Fprintf(&sb, " [green]Role       : [white]%s\n", role)
	fmt.Fprintf(&sb, " [green]Network    : [white]%s\n", network)
	fmt.Fprintf(&sb, " [green]Version    : [white]%s\n",
		fmt.Sprintf(
			"[white]%s[blue] [[white]%s[blue]]",
			nodeVersion,
			nodeRevision,
		))
	if publicIP != nil {
		fmt.Fprintf(&sb, " [green]Public IP  : [white]%s\n", publicIP)
	} else {
		fmt.Fprintln(&sb)
	}
	fmt.Fprintf(&sb, " [green]Uptime     : [white]%s\n",
		timeFromSeconds(uptimes))
	return sb.String()
}

func getPeerText(ctx context.Context) string {
	select {
	case <-ctx.Done():
		return ""
	default:
	}

	if processMetrics == nil {
		return peerText
	}
	var sb strings.Builder

	// Style / UI
	width := TerminalWidthDefault

	var charMarked string
	var charUnmarked string
	charMarked = string('▌')
	charUnmarked = string('▖')
	granularity := ProgressBarGranularity
	granularitySmall := granularity / 2

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
	peers := slices.Clone(peerStats.RTTresultsSlice)
	peerStatsMu.Unlock()

	if peerCount == 0 || rttCount < peerCount {
		fmt.Fprintf(&sb, " [yellow]%s [blue]%d[white]/[green]%d[white]\n",
			"Peer analysis started... please wait!",
			rttCount,
			peerCount)
		scrollPeers = false
		return sb.String()
	}

	sb.WriteString("       [green]RTT : Peers / Percent\n")
	fmt.Fprintf(&sb, "    [green]0-50ms : [white]%5s   %.f%%",
		strconv.Itoa(cnt1),
		pct1)
	fmt.Fprintf(&sb, "%"+strconv.Itoa(10-len(fmt.Sprintf("%.f", pct1)))+"s",
		" ")
	for i := range granularitySmall {
		if i < int(pct1) {
			sb.WriteString("[green]" + charMarked)
		} else {
			sb.WriteString("[white]" + charUnmarked)
		}
	}
	sb.WriteString("[white]\n") // closeRow
	fmt.Fprintf(&sb, "  [green]50-100ms : [white]%5s   %.f%%",
		strconv.Itoa(cnt2),
		pct2)
	fmt.Fprintf(&sb, "%"+strconv.Itoa(10-len(fmt.Sprintf("%.f", pct2)))+"s",
		"")
	for i := range granularitySmall {
		if i < int(pct2) {
			sb.WriteString("[yellow]" + charMarked)
		} else {
			sb.WriteString("[white]" + charUnmarked)
		}
	}
	sb.WriteString("[white]\n") // closeRow
	fmt.Fprintf(&sb, " [green]100-200ms : [white]%5s   %.f%%",
		strconv.Itoa(cnt3),
		pct3)
	fmt.Fprintf(&sb, "%"+strconv.Itoa(10-len(fmt.Sprintf("%.f", pct3)))+"s",
		"")
	for i := range granularitySmall {
		if i < int(pct3) {
			sb.WriteString("[red]" + charMarked)
		} else {
			sb.WriteString("[white]" + charUnmarked)
		}
	}
	sb.WriteString("[white]\n") // closeRow
	fmt.Fprintf(&sb, "   [green]200ms < : [white]%5s   %.f%%",
		strconv.Itoa(cnt4),
		pct4)
	fmt.Fprintf(&sb, "%"+strconv.Itoa(10-len(fmt.Sprintf("%.f", pct4)))+"s",
		"")
	for i := range granularitySmall {
		if i < int(pct4) {
			sb.WriteString("[fuchsia]" + charMarked)
		} else {
			sb.WriteString("[white]" + charUnmarked)
		}
	}
	sb.WriteString("[white]\n") // closeRow

	// Divider
	sb.WriteString(strings.Repeat("-", width-1) + "\n")

	fmt.Fprintf(&sb, " [green]Total / Undetermined : [white]%d[white] / ",
		peerCount)
	if cnt0 == 0 {
		sb.WriteString("[blue]0[white]")
	} else {
		fmt.Fprintf(&sb, "[fuchsia]%d[white]", cnt0)
	}
	if rttAvg >= RTTThreshold3 {
		fmt.Fprintf(&sb, " Average RTT : [fuchsia]%d[white] ms\n",
			rttAvg)
	} else if rttAvg >= RTTThreshold2 {
		fmt.Fprintf(&sb, " Average RTT : [red]%d[white] ms\n", rttAvg)
	} else if rttAvg >= RTTThreshold1 {
		fmt.Fprintf(&sb, " Average RTT : [yellow]%d[white] ms\n", rttAvg)
	} else if rttAvg >= 0 {
		fmt.Fprintf(&sb, " Average RTT : [green]%d[white] ms\n", rttAvg)
	} else {
		fmt.Fprintf(&sb, " Average RTT : [red]%s[white] ms\n", "---")
	}

	// Divider
	sb.WriteString(strings.Repeat("-", width-1) + "\n")

	fmt.Fprintf(&sb, "   [green]# %24s  I/O RTT   Geolocation\n", "REMOTE PEER")
	// peerLocationWidth := width - 41
	for peerNbr, peer := range peers {
		peerNbr++
		peerRTT := peer.RTT
		peerPORT := peer.Port
		peerDIR := peer.Direction
		peerIP := peer.IP
		if strings.Contains(peer.IP, ":") {
			if len(strings.Split(peer.IP, ":")) > 3 {
				splitIP := strings.Split(peer.IP, ":")
				if splitIP == nil {
					continue
				}
				peerIP = fmt.Sprintf("%s...%s:%s",
					splitIP[0],
					splitIP[:len(splitIP)-2],
					splitIP[:len(splitIP)-1],
				)
			}
		}
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
			fmt.Fprintf(&sb, " %3d %19s:%-5d %-3s ["+color+"]%-5d[white] %s\n",
				peerNbr,
				peerIP,
				peerPORT,
				peerDIR,
				peerRTT,
				peerLocationFmt)
		} else {
			fmt.Fprintf(&sb, " %3d %19s:%-5d %-3s [fuchsia]%-5s[white] %s\n",
				peerNbr,
				peerIP,
				peerPORT,
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

	memRss := fmt.Sprintf("%.1f", float64(rss)/float64(BytesInGigabyte))

	var memLiveStr, memHeapStr string
	if getEffectiveNodeBinary() == DINGO_BINARY {
		memLiveStr = fmt.Sprintf(
			"%.1f",
			float64(promMetrics.GoHeapInuse)/float64(BytesInGigabyte),
		)
		memHeapStr = fmt.Sprintf(
			"%.1f",
			float64(promMetrics.GoHeapSys)/float64(BytesInGigabyte),
		)
	} else {
		memLiveStr = fmt.Sprintf("%.1f", float64(promMetrics.MemLive)/float64(BytesInGigabyte))
		memHeapStr = fmt.Sprintf("%.1f", float64(promMetrics.MemHeap)/float64(BytesInGigabyte))
	}

	fmt.Fprintf(&sb, " [green]CPU (sys)  : [white]%s%%\n",
		fmt.Sprintf("%.2f", cpuPercent))
	fmt.Fprintf(&sb, " [green]Mem (Live) : [white]%s[blue]G\n", memLiveStr)
	fmt.Fprintf(&sb, " [green]Mem (RSS)  : [white]%s[blue]G\n", memRss)
	fmt.Fprintf(&sb, " [green]Mem (Heap) : [white]%s[blue]G\n", memHeapStr)
	var gcMinor, gcMajor uint64
	if getEffectiveNodeBinary() == DINGO_BINARY {
		gcMinor = 0
		gcMajor = promMetrics.GoGcCount
	} else {
		gcMinor = promMetrics.GcMinor
		gcMajor = promMetrics.GcMajor
	}

	fmt.Fprintf(&sb, " [green]GC Minor   : [white]%s\n",
		strconv.FormatUint(gcMinor, 10))
	fmt.Fprintf(&sb, " [green]GC Major   : [white]%s\n",
		strconv.FormatUint(gcMajor, 10))
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
	matches := []dingoCandidate{}
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
			strings.Contains(c, strconv.FormatUint(uint64(cfg.Node.Port), 10)) {
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
