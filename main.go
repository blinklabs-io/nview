// Copyright 2023 Blink Labs Software
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
	"flag"
	"fmt"
	"math"
	"net"
	"os"
	"strconv"
	"strings"
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
	CARADNO_NODE_BINARY = "cardano-node"
	AMARU_BINARY        = "amaru"
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
	SetTextColor(tcell.ColorGreen)

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
var failCount uint32 = 0

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
		fmt.Printf("Failed to load config: %s", err)
		os.Exit(1)
	}

	// Create a background context
	ctx := context.Background()

	// Exit if NODE_NAME is > 19 characters
	if len([]rune(cfg.App.NodeName)) > 19 {
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
	checkPeers = true

	// Fetch data from Prometheus
	go func() {
		for {
			prom, err := getPromMetrics(ctx)
			if err != nil && prom != nil {
				failCount++
				time.Sleep(time.Second * time.Duration(cfg.Prometheus.Refresh))
				continue
			}
			promMetrics = prom
			time.Sleep(time.Second * time.Duration(cfg.Prometheus.Refresh))
		}
	}()

	// Set Epoch
	go func() {
		for {
			setCurrentEpoch()
			if currentEpoch != 0 {
				time.Sleep(time.Second * 20)
			}
		}
	}()

	// Update Process metrics
	go func() {
		for {
			proc, err := getProcessMetrics(ctx)
			if err != nil {
				failCount++
				time.Sleep(time.Second * 1)
				continue
			}
			processMetrics = proc
			time.Sleep(time.Second * 1)
		}
	}()

	// Set uptimes
	go func() {
		for {
			uptime := getUptimes(ctx, processMetrics)
			if uptime != 0 {
				uptimes = uptime
			}
			time.Sleep(time.Second * 1)
		}
	}()

	// Filter peers
	go func() {
		for {
			err := filterPeers(ctx)
			if err != nil {
				failCount++
				time.Sleep(time.Second * 1)
				continue
			}
			time.Sleep(time.Second * 1)
		}
	}()

	// Ping peers
	go func() {
		for {
			err := pingPeers(ctx)
			if err != nil {
				failCount++
				time.Sleep(time.Second * 10)
				continue
			}
			time.Sleep(time.Second * 10)
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

	peerText = getPeerText(ctx)
	peerTextView.SetText(peerText).SetTitle("Peers").SetBorder(true)

	// Set our footer
	defaultFooterText := " [yellow](esc/q)[white] Quit | [yellow](p)[white] Peer Analysis"
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
			checkPeers = true
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
			// Peers are last since they take time to process
			tmpText = getPeerText(ctx)
			if tmpText != "" && tmpText != peerText {
				peerText = tmpText
				peerTextView.Clear()
				peerTextView.SetText(peerText)
				// Scroll to the top only once
				if scrollPeers {
					scrollPeers = false
					peerTextView.ScrollToBeginning()
				}
			}
		}
		if event.Rune() == 112 { // p
			resetPeers()
			checkPeers = true
			scrollPeers = false
		}
		if event.Rune() == 113 || event.Key() == tcell.KeyEscape { // q
			app.Stop()
		}
		return event
	})

	// Pages
	pages.AddPage("Main", flex, true, true)

	// Start our background refresh timer
	go func() {
		for {
			if failCount >= cfg.App.Retries {
				panic(
					fmt.Errorf(
						"COULD NOT CONNECT TO A RUNNING INSTANCE, %d FAILED ATTEMPTS IN A ROW",
						failCount,
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
			tmpText = getPeerText(ctx)
			if tmpText != "" && tmpText != peerText {
				peerText = tmpText
				peerTextView.Clear()
				peerTextView.SetText(peerText)
				// Scroll to the top only once
				if scrollPeers {
					scrollPeers = false
					peerTextView.ScrollToBeginning()
				}
			}
			time.Sleep(time.Second * time.Duration(cfg.App.Refresh))
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
	// #nosec G115
	uptimes = uint64(time.Now().Unix() - (createTime / 1000))
	return uptimes
}

// Track size of epoch items
var epochItemsLast = 0

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
		epochProgress = float32(
			(float32(promMetrics.SlotInEpoch) / float32(cfg.Node.ShelleyGenesis.EpochLength)) * 100,
		)
	} else {
		epochProgress = float32(
			(float32(promMetrics.SlotInEpoch) / float32(cfg.Node.ByronGenesis.EpochLength)) * 100,
		)
	}
	return epochProgress
}

func getEpochText(ctx context.Context) string {
	var sb strings.Builder

	epochProgress := getEpochProgress()
	epochProgress1dec := fmt.Sprintf("%.1f", epochProgress)

	sb.WriteString(
		fmt.Sprintf(
			// `" Epoch [blue]%d[white] [[blue]%s%%[white]], [blue]%s[white] %-12s\n",
			" [green]Epoch: [white]%d[blue] [[white]%s%%[blue]]\n",
			currentEpoch,
			epochProgress1dec,
			// epochTimeLeft,
			// "remaining",
		),
	)

	// Epoch progress bar
	var epochBar string
	granularity := 68
	var charMarked string
	var charUnmarked string
	// TODO: legacy mode vs new
	if false {
		charMarked = string('#')
		charUnmarked = string('.')
	} else {
		charMarked = string('▌')
		charUnmarked = string('▖')
	}

	epochItems := int(epochProgress) * granularity / 100
	if epochBar == "" || epochItems != epochItemsLast {
		epochBar = ""
		epochItemsLast = epochItems
		for i := 0; i <= granularity-1; i++ {
			if i < epochItems {
				epochBar += "[blue]" + charMarked
			} else {
				epochBar += "[white]" + charUnmarked
			}
		}
	}
	sb.WriteString(fmt.Sprintf(" [blue]%s[green]\n", epochBar))
	return sb.String()
}

func getChainText(ctx context.Context) string {
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
	tipDiff := (tipRef - promMetrics.SlotNum)

	// Row 1
	sb.WriteString(fmt.Sprintf(
		" Block      : [white]%-"+strconv.Itoa(10)+"s[green]",
		strconv.FormatUint(promMetrics.BlockNum, 10),
	))
	sb.WriteString(fmt.Sprintf(
		" Tip (ref)  : [white]%-"+strconv.Itoa(10)+"s[green]",
		strconv.FormatUint(tipRef, 10),
	))
	sb.WriteString(fmt.Sprintf(
		" Forks      : [white]%-"+strconv.Itoa(10)+"s[green]\n",
		strconv.FormatUint(promMetrics.Forks, 10),
	))
	// Row 2
	sb.WriteString(fmt.Sprintf(
		" Slot       : [white]%-"+strconv.Itoa(10)+"s[green]",
		strconv.FormatUint(promMetrics.SlotNum, 10),
	))
	if promMetrics.SlotNum == 0 {
		sb.WriteString(fmt.Sprintf(
			" Status     : [white]%-"+strconv.Itoa(
				10,
			)+"s[green]",
			"starting",
		))
	} else if tipDiff <= 20 {
		sb.WriteString(fmt.Sprintf(
			" Tip (diff) : [white]%-"+strconv.Itoa(9)+"s[green]",
			strconv.FormatUint(tipDiff, 10)+" 😀",
		))
	} else if tipDiff <= 600 {
		sb.WriteString(fmt.Sprintf(
			" Tip (diff) : [yellow]%-"+strconv.Itoa(9)+"s[green]",
			strconv.FormatUint(tipDiff, 10)+" 😐",
		))
	} else {
		syncProgress := float32((float32(promMetrics.SlotNum) / float32(tipRef)) * 100)
		sb.WriteString(fmt.Sprintf(
			" Syncing    : [yellow]%-"+strconv.Itoa(10)+"s[green]",
			fmt.Sprintf("%2.1f", syncProgress),
		))
	}
	sb.WriteString(fmt.Sprintf(
		" Total Tx   : [white]%-"+strconv.Itoa(10)+"s[green]\n",
		strconv.FormatUint(promMetrics.TxProcessed, 10),
	))
	// Row 3
	sb.WriteString(fmt.Sprintf(
		" Slot epoch : [white]%-"+strconv.Itoa(10)+"s[green]",
		strconv.FormatUint(promMetrics.SlotInEpoch, 10),
	))
	sb.WriteString(fmt.Sprintf(
		" Density    : [white]%-"+strconv.Itoa(10)+"s[green]",
		fmt.Sprintf("%3.5f", promMetrics.Density*100/1),
	))
	sb.WriteString(fmt.Sprintf(
		" Pending Tx : [white]%d[blue]/[white]%d[blue]%-"+kWidth+"s\n",
		promMetrics.MempoolTx,
		mempoolTxKBytes,
		"K",
	))
	return sb.String()
}

func getConnectionText(ctx context.Context) string {
	cfg := config.GetConfig()
	var sb strings.Builder

	if p2p {
		if promMetrics == nil {
			return connectionText
		}
		sb.WriteString(fmt.Sprintf(" [green]P2P        : %s\n",
			"enabled",
		))
		sb.WriteString(fmt.Sprintf(" [green]Incoming   : [white]%s\n",
			strconv.FormatUint(promMetrics.ConnIncoming, 10),
		))
		sb.WriteString(fmt.Sprintf(" [green]Outgoing   : [white]%s\n",
			strconv.FormatUint(promMetrics.ConnOutgoing, 10),
		))
		sb.WriteString(fmt.Sprintf(" [green]Cold Peers : [white]%s\n",
			strconv.FormatUint(promMetrics.PeersCold, 10),
		))
		sb.WriteString(fmt.Sprintf(" [green]Warm Peers : [white]%s\n",
			strconv.FormatUint(promMetrics.PeersWarm, 10),
		))
		sb.WriteString(fmt.Sprintf(" [green]Hot Peers  : [white]%s\n",
			strconv.FormatUint(promMetrics.PeersHot, 10),
		))
		sb.WriteString(fmt.Sprintf(" [green]Uni-Dir    : [white]%s\n",
			strconv.FormatUint(promMetrics.ConnUniDir, 10),
		))
		sb.WriteString(fmt.Sprintf(" [green]Bi-Dir     : [white]%s\n",
			strconv.FormatUint(promMetrics.ConnBiDir, 10),
		))
		sb.WriteString(fmt.Sprintf(" [green]Duplex     : [white]%s\n",
			strconv.FormatUint(promMetrics.ConnDuplex, 10),
		))
	} else {
		if processMetrics == nil {
			return connectionText
		}
		// Get process in/out connections
		connections, err := netutil.ConnectionsPidWithContext(ctx, "tcp", processMetrics.Pid)
		if err != nil {
			sb.WriteString(fmt.Sprintf("Failed to get processes: %v", err))
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

		sb.WriteString(fmt.Sprintf(" [green]P2P        : [yellow]%s\n",
			"disabled",
		))
		sb.WriteString(fmt.Sprintf(" [green]Incoming   : [white]%s\n",
			strconv.Itoa(len(peersIn)),
		))
		sb.WriteString(fmt.Sprintf(" [green]Outgoing   : [white]%s\n",
			strconv.Itoa(len(peersOut)),
		))
	}
	return sb.String()
}

func getCoreText(ctx context.Context) string {
	if promMetrics == nil {
		return coreText
	}

	var sb strings.Builder

	// Core section
	if role == "Core" {
		// TODO: block log functionality
		adoptedFmt := "white"
		invalidFmt := "white"
		if promMetrics.IsLeader != promMetrics.Adopted {
			adoptedFmt = "yellow"
		}
		if promMetrics.DidntAdopt != 0 {
			invalidFmt = "red"
		}
		leader := strconv.FormatUint(promMetrics.IsLeader, 10)
		sb.WriteString(fmt.Sprintf(" [green]Leader     : [white]%s\n",
			leader,
		))
		adopted := strconv.FormatUint(promMetrics.Adopted, 10)
		sb.WriteString(fmt.Sprintf(" [green]Adopted    : ["+adoptedFmt+"]%s\n",
			adopted,
		))
		invalid := strconv.FormatUint(promMetrics.DidntAdopt, 10)
		sb.WriteString(fmt.Sprintf(" [green]Invalid    : ["+invalidFmt+"]%s\n",
			invalid,
		))
		sb.WriteString(" [green]Missed     : ")
		var missedSlotsPct float32
		if promMetrics.AboutToLead > 0 {
			missedSlotsPct = float32(
				promMetrics.MissedSlots,
			) / (float32(promMetrics.AboutToLead + promMetrics.MissedSlots)) * 100
		}
		sb.WriteString(fmt.Sprintf("[white]%s [blue]([white]%s %%[blue])\n",
			strconv.FormatUint(promMetrics.MissedSlots, 10),
			fmt.Sprintf("%.2f", missedSlotsPct),
		))

		sb.WriteString("\n")

		// KES
		sb.WriteString(fmt.Sprintf(" [green]KES period : [white]%d\n",
			promMetrics.KesPeriod,
		))
		sb.WriteString(fmt.Sprintf(" [green]KES remain : [white]%d\n",
			promMetrics.RemainingKesPeriods,
		))
	} else {
		sb.WriteString(fmt.Sprintf("%18s\n",
			"N/A",
		))
	}
	failCount = 0
	return sb.String()
}

func getBlockText(ctx context.Context) string {
	if promMetrics == nil {
		return blockText
	}

	// Style / UI
	width := 71

	// Get our terminal size
	tcols, tlines, err := terminal.GetSize(int(os.Stdout.Fd()))
	if err != nil {
		failCount++
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
	// TODO: populate lines
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
	sb.WriteString(
		fmt.Sprintf(
			" [green]Last Delay : [white]%s[blue]%-"+strconv.Itoa(
				10-len(delay),
			)+"s",
			delay,
			"s",
		),
	)
	sb.WriteString(
		fmt.Sprintf(" [green]Served     : [white]%-"+strconv.Itoa(10)+"s",
			strconv.FormatUint(promMetrics.BlocksServed, 10),
		),
	)
	sb.WriteString(
		fmt.Sprintf(" [green]Late (>5s) : [white]%-"+strconv.Itoa(10)+"s\n",
			strconv.FormatUint(promMetrics.BlocksLate, 10),
		),
	)
	// Row 2
	sb.WriteString(
		fmt.Sprintf(
			" [green]Within 1s  : [white]%s%-"+strconv.Itoa(10-len(blk1s))+"s",
			blk1s,
			"%",
		),
	)
	sb.WriteString(
		fmt.Sprintf(
			" [green]Within 3s  : [white]%s%-"+strconv.Itoa(10-len(blk3s))+"s",
			blk3s,
			"%",
		),
	)
	sb.WriteString(
		fmt.Sprintf(
			" [green]Within 5s  : [white]%s%-"+strconv.Itoa(
				10-len(blk5s),
			)+"s\n",
			blk5s,
			"%",
		),
	)

	failCount = 0
	return sb.String()
}

func getNodeText(ctx context.Context) string {
	cfg := config.GetConfig()
	var network string
	if cfg.App.Network != "" {
		network = strings.ToUpper(cfg.App.Network[:1]) + cfg.App.Network[1:]
	} else {
		network = strings.ToUpper(cfg.Node.Network[:1]) + cfg.Node.Network[1:]
	}
	nodeVersion, nodeRevision, _ := getNodeVersion()
	var sb strings.Builder
	sb.WriteString(
		fmt.Sprintf(" [green]Name       : [white]%s\n", cfg.App.NodeName),
	)
	sb.WriteString(fmt.Sprintf(" [green]Role       : [white]%s\n", role))
	sb.WriteString(fmt.Sprintf(" [green]Network    : [white]%s\n", network))
	sb.WriteString(fmt.Sprintf(
		" [green]Version    : [white]%s\n",
		fmt.Sprintf(
			"[white]%s[blue] [[white]%s[blue]]",
			nodeVersion,
			nodeRevision,
		),
	))
	if publicIP != nil {
		sb.WriteString(
			fmt.Sprintf(" [green]Public IP  : [white]%s\n", publicIP),
		)
	} else {
		sb.WriteString(fmt.Sprintln())
	}
	sb.WriteString(fmt.Sprintf(" [green]Uptime     : [white]%s\n",
		timeFromSeconds(uptimes),
	))
	return sb.String()
}

func getPeerText(ctx context.Context) string {
	if processMetrics == nil {
		return peerText
	}
	var sb strings.Builder

	// Style / UI
	width := 71

	var charMarked string
	var charUnmarked string
	// TODO: legacy mode vs new
	if false {
		charMarked = string('#')
		charUnmarked = string('.')
	} else {
		charMarked = string('▌')
		charUnmarked = string('▖')
	}
	granularity := 68
	granularitySmall := granularity / 2
	if checkPeers {
		peerCount := len(peersFiltered)
		sb.WriteString(
			fmt.Sprintf(" [yellow]%s [blue]%d[white]/[green]%d[white]\n",
				"Peer analysis started... please wait!",
				len(peerStats.RTTresultsSlice),
				peerCount,
			),
		)
		scrollPeers = false
		return sb.String()
	}

	peerCount := len(peersFiltered)
	sb.WriteString("       [green]RTT : Peers / Percent\n")
	sb.WriteString(fmt.Sprintf(
		"    [green]0-50ms : [white]%5s   %.f%%",
		strconv.Itoa(peerStats.CNT1),
		peerStats.PCT1,
	))
	sb.WriteString(fmt.Sprintf(
		"%"+strconv.Itoa(10-len(fmt.Sprintf("%.f", peerStats.PCT1)))+"s",
		" ",
	))
	for i := range granularitySmall {
		if i < int(peerStats.PCT1) {
			sb.WriteString("[green]" + charMarked)
		} else {
			sb.WriteString("[white]" + charUnmarked)
		}
	}
	sb.WriteString("[white]\n") // closeRow
	sb.WriteString(fmt.Sprintf(
		"  [green]50-100ms : [white]%5s   %.f%%",
		strconv.Itoa(peerStats.CNT2),
		peerStats.PCT2,
	))
	sb.WriteString(fmt.Sprintf(
		"%"+strconv.Itoa(10-len(fmt.Sprintf("%.f", peerStats.PCT2)))+"s",
		"",
	))
	for i := range granularitySmall {
		if i < int(peerStats.PCT2) {
			sb.WriteString("[yellow]" + charMarked)
		} else {
			sb.WriteString("[white]" + charUnmarked)
		}
	}
	sb.WriteString("[white]\n") // closeRow
	sb.WriteString(fmt.Sprintf(
		" [green]100-200ms : [white]%5s   %.f%%",
		strconv.Itoa(peerStats.CNT3),
		peerStats.PCT3,
	))
	sb.WriteString(fmt.Sprintf(
		"%"+strconv.Itoa(10-len(fmt.Sprintf("%.f", peerStats.PCT3)))+"s",
		"",
	))
	for i := range granularitySmall {
		if i < int(peerStats.PCT3) {
			sb.WriteString("[red]" + charMarked)
		} else {
			sb.WriteString("[white]" + charUnmarked)
		}
	}
	sb.WriteString("[white]\n") // closeRow
	sb.WriteString(fmt.Sprintf(
		"   [green]200ms < : [white]%5s   %.f%%",
		strconv.Itoa(peerStats.CNT4),
		peerStats.PCT4,
	))
	sb.WriteString(fmt.Sprintf(
		"%"+strconv.Itoa(10-len(fmt.Sprintf("%.f", peerStats.PCT4)))+"s",
		"",
	))
	for i := range granularitySmall {
		if i < int(peerStats.PCT4) {
			sb.WriteString("[fuchsia]" + charMarked)
		} else {
			sb.WriteString("[white]" + charUnmarked)
		}
	}
	sb.WriteString("[white]\n") // closeRow

	// Divider
	sb.WriteString(strings.Repeat("-", width-1) + "\n")

	sb.WriteString(
		fmt.Sprintf(
			" [green]Total / Undetermined : [white]%d[white] / ",
			peerCount,
		),
	)
	if peerStats.CNT0 == 0 {
		sb.WriteString("[blue]0[white]")
	} else {
		sb.WriteString(fmt.Sprintf("[fuchsia]%d[white]", peerStats.CNT0))
	}
	// TODO: figure out spacing here
	if peerStats.RTTAVG >= 200 {
		sb.WriteString(
			fmt.Sprintf(
				" Average RTT : [fuchsia]%d[white] ms\n",
				peerStats.RTTAVG,
			),
		)
	} else if peerStats.RTTAVG >= 100 {
		sb.WriteString(fmt.Sprintf(" Average RTT : [red]%d[white] ms\n", peerStats.RTTAVG))
	} else if peerStats.RTTAVG >= 50 {
		sb.WriteString(fmt.Sprintf(" Average RTT : [yellow]%d[white] ms\n", peerStats.RTTAVG))
	} else if peerStats.RTTAVG >= 0 {
		sb.WriteString(fmt.Sprintf(" Average RTT : [green]%d[white] ms\n", peerStats.RTTAVG))
	} else {
		sb.WriteString(fmt.Sprintf(" Average RTT : [red]%s[white] ms\n", "---"))
	}

	// Divider
	sb.WriteString(strings.Repeat("-", width-1) + "\n")

	sb.WriteString(
		fmt.Sprintf("   [green]# %24s  I/O RTT   Geolocation\n", "REMOTE PEER"),
	)
	// peerLocationWidth := width - 41
	for peerNbr, peer := range peerStats.RTTresultsSlice {
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
		if peerRTT < 50 {
			color = "green"
		} else if peerRTT < 100 {
			color = "yellow"
		} else if peerRTT < 200 {
			color = "red"
		}
		if peerRTT < 99999 {
			sb.WriteString(fmt.Sprintf(
				" %3d %19s:%-5d %-3s ["+color+"]%-5d[white] %s\n",
				peerNbr,
				peerIP,
				peerPORT,
				peerDIR,
				peerRTT,
				peerLocationFmt,
			))
		} else {
			sb.WriteString(fmt.Sprintf(
				" %3d %19s:%-5d %-3s [fuchsia]%-5s[white] %s\n",
				peerNbr,
				peerIP,
				peerPORT,
				peerDIR,
				"---",
				peerLocationFmt,
			))
		}
	}
	sb.WriteString("[white]\n")

	failCount = 0
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
			failCount++
			return fmt.Sprintf("cannot parse CPU usage: %s", err)
		}
		processMemory, err = processMetrics.MemoryInfoWithContext(ctx)
		if err != nil {
			failCount++
			return fmt.Sprintf("cannot parse memory usage: %s", err)
		}
		rss = processMemory.RSS
	}

	memRss := fmt.Sprintf("%.1f", float64(rss)/float64(1073741824))
	memLive := fmt.Sprintf(
		"%.1f",
		float64(promMetrics.MemLive)/float64(1073741824),
	)
	memHeap := fmt.Sprintf(
		"%.1f",
		float64(promMetrics.MemHeap)/float64(1073741824),
	)

	sb.WriteString(
		fmt.Sprintf(
			" [green]CPU (sys)  : [white]%s%%\n",
			fmt.Sprintf("%.2f", cpuPercent),
		),
	)
	sb.WriteString(
		fmt.Sprintf(" [green]Mem (Live) : [white]%s[blue]G\n", memLive),
	)
	sb.WriteString(
		fmt.Sprintf(" [green]Mem (RSS)  : [white]%s[blue]G\n", memRss),
	)
	sb.WriteString(
		fmt.Sprintf(" [green]Mem (Heap) : [white]%s[blue]G\n", memHeap),
	)
	sb.WriteString(
		fmt.Sprintf(
			" [green]GC Minor   : [white]%s\n",
			strconv.FormatUint(promMetrics.GcMinor, 10),
		),
	)
	sb.WriteString(
		fmt.Sprintf(
			" [green]GC Major   : [white]%s\n",
			strconv.FormatUint(promMetrics.GcMajor, 10),
		),
	)
	return sb.String()
}

func getProcessMetrics(ctx context.Context) (*process.Process, error) {
	cfg := config.GetConfig()

	if cfg.Node.Binary == AMARU_BINARY {
		return getProcessMetricsByPidFile(cfg, ctx)
	} else {
		return getProcessMetricsByNameAndPort(cfg, ctx)
	}
}

func getProcessMetricsByPidFile(cfg *config.Config, ctx context.Context) (*process.Process, error) {
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

	// the overflow is checked above
	//nolint:gosec
	proc, err := process.NewProcessWithContext(ctx, int32(pid))
	if err != nil {
		return nil, fmt.Errorf("failed to get process %d: %w", pid, err)
	}

	exists, err := proc.IsRunning()
	if err != nil {
		return nil, fmt.Errorf("failed to check if process %d is running: %w", pid, err)
	}

	if !exists {
		return nil, fmt.Errorf("process %d is not running", pid)
	}

	return proc, nil
}

func getProcessMetricsByNameAndPort(cfg *config.Config, ctx context.Context) (*process.Process, error) {
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

		if strings.Contains(n, cfg.Node.Binary) &&
			strings.Contains(c, strconv.FormatUint(uint64(cfg.Node.Port), 10)) {
			// TODO: linter thinks r = p here is ineffective, which it's not...
			//nolint:ineffassign
			r = p
		}
	}

	return nil, fmt.Errorf("process containing binary '%s' and port '%d' not found", cfg.Node.Binary, cfg.Node.Port)
}

//nolint:unused
func createRemoteClientConnection(address string) net.Conn {
	var err error
	var conn net.Conn
	var dialProto string
	var dialAddress string
	if address != "" {
		dialProto = "tcp"
		dialAddress = address
	} else {
		return conn
	}

	conn, err = net.DialTimeout(dialProto, dialAddress, 10*time.Second)
	if err != nil {
		fmt.Printf("ERROR: %s\n", err)
		os.Exit(1)
	}
	return conn
}

func tcpinfoRtt(address string) int {
	result := 99999
	// Get a connection and setup our error channels
	conn, err := net.DialTimeout("tcp", address, 3*time.Second)
	if err != nil {
		return result
	}
	if conn == nil {
		return result
	}
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
		result = int(q.RTT.Seconds() * 1000)
	}
	return result
}
