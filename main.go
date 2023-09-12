// Copyright 2023 Blink Labs, LLC.
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
	"net"
	"os"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/gdamore/tcell/v2"
	"github.com/mikioh/tcp"
	"github.com/mikioh/tcpinfo"
	"github.com/rivo/tview"
	"github.com/shirou/gopsutil/v3/process"
	terminal "golang.org/x/term"

	"github.com/blinklabs-io/nview/internal/version"
)

// Global command line flags
var cmdlineFlags struct {
	configFile string
}

// Global tview application and pages
var app = tview.NewApplication()
var pages = tview.NewPages()

// Main viewport - flexible box
var flex = tview.NewFlex()

// Our text views: footer, header, and main section
var footerText = tview.NewTextView().
	SetDynamicColors(true).
	SetTextColor(tcell.ColorGreen)
var headerText = tview.NewTextView().
	SetDynamicColors(true)
var text = tview.NewTextView().
	SetDynamicColors(true).
	SetChangedFunc(func() {
		// Redraw the screen on a change
		app.Draw()
	})

// Track which page is active
var active string = "main"

// Track our failures
var failCount uint32 = 0

// Track our role
var role string = "Relay"

// Track current epoch
var currentEpoch uint32 = 0

func main() {
	// Check if any command line flags are given
	flag.StringVar(&cmdlineFlags.configFile, "config", "", "path to config file to load")
	flag.Parse()

	// Load config
	cfg, err := LoadConfig(cmdlineFlags.configFile)
	if err != nil {
		fmt.Printf("Failed to load config: %s", err)
		os.Exit(1)
	}

	// Create a background context
	ctx := context.Background()

	// Exit if NODE_NAME is > 19 characters
	if len([]rune(cfg.App.NodeName)) > 19 {
		fmt.Println("Please keep node name at or below 19 characters in length!")
		os.Exit(1)
	}

	// Fetch data from Prometheus
	metrics, err := getPromMetrics(ctx)
	if err != nil {
		text.SetText(
			fmt.Sprintf(" [red]Cannot get metrics from node![white]\n [red]ERROR[white]: %s", err),
		)
	}
	// Set current epoch from Prometheus metrics
	currentEpoch = uint32(metrics.EpochNum)
	// TODO: temp hack to use currentEpoch
	if currentEpoch > 0 {
		// Do something non-useful
		text.SetText(fmt.Sprintf("%d", currentEpoch))
	}

	// Populate initial text from metrics
	text.SetText(getHomeText(ctx, metrics)).SetBorder(true)

	// Set our header
	var width int = 71
	var network string
	if cfg.App.Network != "" {
		network = strings.ToUpper(cfg.App.Network[:1]) + cfg.App.Network[1:]
	} else {
		network = strings.ToUpper(cfg.Node.Network[:1]) + cfg.Node.Network[1:]
	}
	nodeVersion, nodeRevision, _ := getNodeVersion()
	var headerLength int
	var headerPadding int
	headerLength = len([]rune(cfg.App.NodeName)) + len(role) + len(nodeVersion) + len(nodeRevision) + len(network) + 19
	if headerLength >= width {
		headerPadding = 0
	} else {
		headerPadding = (width - headerLength) / 2
	}
	defaultHeaderText := fmt.Sprintf(
		"%"+strconv.Itoa(headerPadding)+"s > [green]%s[white] - [yellow](%s - %s)[white] : [blue]%s[white] [[blue]%s[white]] <",
		"",
		cfg.App.NodeName,
		role,
		network,
		nodeVersion,
		nodeRevision,
	)
	headerText.SetText(defaultHeaderText)

	// Set our footer
	defaultFooterText := " [yellow](esc/q) Quit[white] | [yellow](i) Info[white] | [yellow](p) Peer Analysis"
	footerText.SetText(defaultFooterText)

	// Add content to our flex box
	flex.SetDirection(tview.FlexRow).
		// Row 1 is our application header
		AddItem(headerText,
			1,
			1,
			false).
		// Row 2 is our main text section
		AddItem(text,
			0,
			6,
			true).
		// Row 3 is our footer
		AddItem(footerText, 2, 0, false)

	// capture inputs
	flex.SetInputCapture(func(event *tcell.EventKey) *tcell.EventKey {
		if event.Rune() == 104 || event.Rune() == 114 { // h or r
			active = "main"
			showPeers = false
			text.Clear()
			footerText.Clear()
			footerText.SetText(defaultFooterText)
			metrics, err = getPromMetrics(ctx)
			if err != nil {
				text.SetText(
					fmt.Sprintf(
						" [red]Cannot get metrics from node![white]\n [red]ERROR[white]: %s",
						err,
					),
				)
			}
			text.SetText(getHomeText(ctx, metrics))
		}
		if event.Rune() == 105 { // i
			active = "info"
			text.Clear()
			footerText.Clear()
			footerText.SetText(" [yellow](esc/q) Quit[white] | [yellow](h) Return home")
			text.SetText(getInfoText(ctx))
		}
		if event.Rune() == 112 { // p
			active = "peer"
			checkPeers = true
			pingPeers = false
			showPeers = false
			text.Clear()
			footerText.Clear()
			footerText.SetText(" [yellow](esc/q) Quit[white] | [yellow](h) Return home")
			text.SetText(getPeerText(ctx))
		}
		if event.Rune() == 113 || event.Key() == tcell.KeyEscape { // q
			app.Stop()
		}
		if event.Rune() == 116 { // t
			active = "test"
			text.Clear()
			footerText.Clear()
			footerText.SetText(" [yellow](esc/q) Quit[white] | [yellow](h) Return home")
			metrics, err = getPromMetrics(ctx)
			if err != nil {
				text.SetText(
					fmt.Sprintf(
						" [red]Cannot get metrics from node![white]\n [red]ERROR[white]: %s",
						err,
					),
				)
			}
			text.SetText(getTestText(ctx, metrics))
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
						"COULD NOT CONNECT TO A RUNNING INSTANCE, %d FAILED ATTEMPTS IN A ROW!",
						failCount,
					),
				)
			}
			if active == "main" {
				text.Clear()
				metrics, err = getPromMetrics(ctx)
				if err != nil {
					text.SetText(
						fmt.Sprintf(
							" [red]Cannot get metrics from node![white]\n [red]ERROR[white]: %s",
							err,
						),
					)
				}
				text.SetText(getHomeText(ctx, metrics))
			}
			if active == "peer" {
				if checkPeers {
					checkPeers = false
					pingPeers = true
					text.Clear()
					text.SetText(getPeerText(ctx))
				} else {
					text.Clear()
					text.SetText(getPeerText(ctx))
				}
			}
			if active == "test" {
				text.Clear()
				metrics, err = getPromMetrics(ctx)
				if err != nil {
					text.SetText(
						fmt.Sprintf(
							" [red]Cannot get metrics from node![white]\n [red]ERROR[white]: %s",
							err,
						),
					)
				}
				text.SetText(getTestText(ctx, metrics))
			}
			time.Sleep(time.Second * 2)
		}
	}()

	if err := app.SetRoot(pages, true).EnableMouse(false).Run(); err != nil {
		panic(err)
	}
}

var uptimes uint64

// Track size of epoch items
var epochItemsLast = 0

func getTestText(ctx context.Context, promMetrics *PromMetrics) string {
	cfg := GetConfig()
	// Refresh process metrics from host
	processMetrics, err := getProcessMetrics(ctx)
	if err != nil {
		uptimes = 0
	} else {
		// Calculate uptime for our process
		createTime, err := processMetrics.CreateTimeWithContext(ctx)
		if err == nil {
			// createTime is milliseconds since UNIX epoch, convert to seconds
			uptimes = uint64(time.Now().Unix() - (createTime / 1000))
		}
	}

	var sb strings.Builder

	// Style / UI
	var width = 71

	var twoColWidth int = (width - 3) / 2
	var twoColSecond int = twoColWidth + 2

	// Main section
	uptime := timeLeft(uptimes)
	sb.WriteString(fmt.Sprintf(" Uptime: [blue]%-"+strconv.Itoa(twoColSecond-9-len(uptime))+"s[white]", uptime))
	sb.WriteString(fmt.Sprintf(" nview Version: [blue]%-"+strconv.Itoa(twoColWidth)+"s[white]\n", version.GetVersionString()))
	sb.WriteString(fmt.Sprintf("%s\n", strings.Repeat("-", width+1)))

	// Epoch progress
	var epochProgress float32
	genesisConfig := getGenesisConfig(cfg)
	if promMetrics.EpochNum >= uint64(cfg.Node.ShelleyTransEpoch) {
		epochProgress = float32(
			(float32(promMetrics.SlotInEpoch) / float32(genesisConfig.EpochLength)) * 100,
		)
	} else {
		epochProgress = float32((float32(promMetrics.SlotInEpoch) / float32(cfg.Node.ByronGenesis.EpochLength)) * 100)
	}
	epochProgress1dec := fmt.Sprintf("%.1f", epochProgress)
	epochTimeLeft := timeLeft(timeUntilNextEpoch())

	// Epoch
	sb.WriteString(
		fmt.Sprintf(
			" Epoch [blue]%d[white] [[blue]%s%%[white]], [blue]%s[white] %-12s\n\n",
			promMetrics.EpochNum,
			epochProgress1dec,
			epochTimeLeft,
			"remaining",
		),
	)

	// Epoch Debug
	sb.WriteString(fmt.Sprintf(" Epoch Debug%s\n", ""))
	currentTimeSec := uint64(time.Now().Unix() - 1)
	sb.WriteString(fmt.Sprintf("currentTimeSec    = %d\n", currentTimeSec))
	sb.WriteString(fmt.Sprintf("startTime         = %d\n", cfg.Node.ByronGenesis.StartTime))
	sb.WriteString(fmt.Sprintf("shellyTransEpoch  = %d\n", cfg.Node.ShelleyTransEpoch))
	sb.WriteString(fmt.Sprintf("byron length      = %d\n", ((uint64(
		cfg.Node.ShelleyTransEpoch,
	) * cfg.Node.ByronGenesis.EpochLength * cfg.Node.ByronGenesis.SlotLength) / 1000)))
	sb.WriteString(
		fmt.Sprintf(
			"rhs               = %d\n",
			(uint64(cfg.Node.ShelleyTransEpoch)*cfg.Node.ByronGenesis.EpochLength*cfg.Node.ByronGenesis.SlotLength)/1000,
		),
	)
	byronEndTime := uint64(
		cfg.Node.ByronGenesis.StartTime + ((uint64(cfg.Node.ShelleyTransEpoch) * cfg.Node.ByronGenesis.EpochLength * cfg.Node.ByronGenesis.SlotLength) / 1000),
	)
	sb.WriteString(fmt.Sprintf("byronEndTime      = %d\n", byronEndTime))
	sb.WriteString(fmt.Sprintf("byron EpochLength = %d\n", cfg.Node.ByronGenesis.EpochLength))
	sb.WriteString(fmt.Sprintf("byron SlotLength  = %d\n", cfg.Node.ByronGenesis.SlotLength))
	sb.WriteString(
		fmt.Sprintf("currentTimeSec-byronEndTime = %d\n", (currentTimeSec - byronEndTime)),
	)
	sb.WriteString(
		fmt.Sprintf(
			"byron EpochLength*SlotLength = %d\n",
			(cfg.Node.ByronGenesis.EpochLength * cfg.Node.ByronGenesis.SlotLength),
		),
	)
	sb.WriteString(fmt.Sprintf("slotInterval      = %d\n", slotInterval(genesisConfig)))
	sb.WriteString(fmt.Sprintf("ActiveSlotsCoeff  = %#v\n", genesisConfig.ActiveSlotsCoeff))

	result := uint64(
		cfg.Node.ShelleyTransEpoch,
	) + ((currentTimeSec - byronEndTime) / cfg.Node.ByronGenesis.EpochLength / cfg.Node.ByronGenesis.SlotLength)
	sb.WriteString(fmt.Sprintf("result=%d\n", result))

	sb.WriteString(fmt.Sprintf(" Epoch getEpoch: %d\n", getEpoch()))
	sb.WriteString(fmt.Sprintf(" Epoch timeUntilNextEpoch: %d\n", timeUntilNextEpoch()))
	sb.WriteString(
		fmt.Sprintf(
			"   timeLeft now: %s\n\n\n",
			timeLeft(
				((uint64(cfg.Node.ShelleyTransEpoch)*cfg.Node.ByronGenesis.EpochLength*cfg.Node.ByronGenesis.SlotLength)/1000)+((promMetrics.EpochNum+1-uint64(cfg.Node.ShelleyTransEpoch))*cfg.Node.ByronGenesis.EpochLength*cfg.Node.ByronGenesis.SlotLength)-currentTimeSec+cfg.Node.ByronGenesis.StartTime,
			),
		),
	)

	// Genesis Config
	sb.WriteString(fmt.Sprintf(" Genesis Config: %#v\n\n", genesisConfig))

	// Application config
	sb.WriteString(fmt.Sprintf(" Application config: %#v\n\n", cfg))

	// PromMetrics
	sb.WriteString(fmt.Sprintf(" Prometheus metrics: %#v\n\n", promMetrics))

	failCount = 0
	return fmt.Sprint(sb.String())
}

func getHomeText(ctx context.Context, promMetrics *PromMetrics) string {
	cfg := GetConfig()
	processMetrics, err := getProcessMetrics(ctx)
	if err != nil {
		uptimes = 0
	} else {
		// Calculate uptime for our process
		createTime, err := processMetrics.CreateTimeWithContext(ctx)
		if err == nil {
			// createTime is milliseconds since UNIX epoch, convert to seconds
			uptimes = uint64(time.Now().Unix() - (createTime / 1000))
		}
	}

	// Set role
	if cfg.Node.BlockProducer {
		if role != "Core" {
			role = "Core"
		}
	} else if promMetrics.AboutToLead > 0 {
		if role != "Core" {
			role = "Core"
		}
	} else if role != "Relay" {
		role = "Relay"
	}

	// Style / UI
	var width = 71

	var twoColWidth int = (width - 3) / 2
	var twoColSecond int = twoColWidth + 2
	var threeColWidth = (width - 5) / 3
	//var threeCol2Start = threeColWidth+3
	//var threeCol3Start = threeColWidth*2+4
	var threeCol1ValueWidth = threeColWidth - 12
	var threeCol2ValueWidth = threeColWidth - 12
	var threeCol3ValueWidth = threeColWidth - 12

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
	granularity := width - 3

	// Get our terminal size
	tcols, tlines, err := terminal.GetSize(int(os.Stdout.Fd()))
	if err != nil {
		failCount++
		return fmt.Sprintf("ERROR: %v", err)
	}
	// Validate size
	if width >= tcols {
		footerText.Clear()
		footerText.SetText(" [yellow](esc/q) Quit\n")
		return fmt.Sprintf(
			"\n [red]Terminal width too small![white]\n Please increase by [yellow]%d[white] columns\n",
			width-tcols+1,
		)
	}
	// TODO: populate lines
	line := 10
	if line >= (tlines - 1) {
		footerText.Clear()
		footerText.SetText(" [yellow](esc/q) Quit\n")
		return fmt.Sprintf(
			"\n [red]Terminal height too small![white]\n Please increase by [yellow]%d[white] lines\n",
			line-tlines+2,
		)
	}

	var sb strings.Builder

	// Main section
	uptime := timeLeft(uptimes)
	sb.WriteString(fmt.Sprintf(" Uptime: [blue]%-"+strconv.Itoa(twoColSecond-9-len(uptime))+"s[white]", uptime))
	sb.WriteString(fmt.Sprintf(" nview Version: [blue]%-"+strconv.Itoa(twoColWidth)+"s[white]\n", version.GetVersionString()))
	sb.WriteString(fmt.Sprintf("%s\n", strings.Repeat("-", width+1)))

	// Epoch progress
	var epochProgress float32
	genesisConfig := getGenesisConfig(cfg)
	if promMetrics.EpochNum >= uint64(cfg.Node.ShelleyTransEpoch) {
		epochProgress = float32(
			(float32(promMetrics.SlotInEpoch) / float32(genesisConfig.EpochLength)) * 100,
		)
	} else {
		epochProgress = float32(
			(float32(promMetrics.SlotInEpoch) / float32(cfg.Node.ByronGenesis.EpochLength)) * 100,
		)
	}
	epochProgress1dec := fmt.Sprintf("%.1f", epochProgress)
	// epochTimeLeft := timeLeft(timeUntilNextEpoch())

	// Epoch
	sb.WriteString(
		fmt.Sprintf(
			" Epoch [blue]%d[white] [[blue]%s%%[white]], [blue]%s[white] %-12s\n",
			promMetrics.EpochNum,
			epochProgress1dec,
			"N/A",
			"remaining",
		),
	)

	// Epoch progress bar
	var epochBar string
	epochItems := int(epochProgress) * granularity / 100
	if epochBar == "" || epochItems != epochItemsLast {
		epochBar = ""
		epochItemsLast = epochItems
		for i := 0; i <= granularity-1; i++ {
			if i < epochItems {
				epochBar += fmt.Sprintf("[blue]%s", charMarked)
			} else {
				epochBar += fmt.Sprintf("[white]%s", charUnmarked)
			}
		}
	}
	sb.WriteString(fmt.Sprintf(" [blue]%s[white]\n\n", epochBar))

	// Blocks / Slots / Tx

	mempoolTxKBytes := promMetrics.MempoolBytes / 1024
	kWidth := strconv.Itoa(threeCol3ValueWidth -
		len(strconv.FormatUint(promMetrics.MempoolTx, 10)) -
		len(strconv.FormatUint(mempoolTxKBytes, 10)))

	tipRef := getSlotTipRef(genesisConfig)
	tipDiff := (tipRef - promMetrics.SlotNum)

	// Row 1
	sb.WriteString(fmt.Sprintf(
		" Block      : [blue]%-"+strconv.Itoa(threeCol1ValueWidth)+"s[white]",
		strconv.FormatUint(promMetrics.BlockNum, 10),
	))
	sb.WriteString(fmt.Sprintf(
		" Tip (ref)  : [blue]%-"+strconv.Itoa(threeCol2ValueWidth)+"s[white]",
		strconv.FormatUint(tipRef, 10),
	))
	sb.WriteString(fmt.Sprintf(
		" Forks      : [blue]%-"+strconv.Itoa(threeCol3ValueWidth)+"s[white]\n",
		strconv.FormatUint(promMetrics.Forks, 10),
	))
	// Row 2
	sb.WriteString(fmt.Sprintf(
		" Slot       : [blue]%-"+strconv.Itoa(threeCol1ValueWidth)+"s[white]",
		strconv.FormatUint(promMetrics.SlotNum, 10),
	))
	if promMetrics.SlotNum == 0 {
		sb.WriteString(fmt.Sprintf(
			" Status     : [blue]%-"+strconv.Itoa(threeCol2ValueWidth)+"s[white]",
			"starting",
		))
	} else if tipDiff <= slotInterval(genesisConfig) {
		sb.WriteString(fmt.Sprintf(
			" Tip (diff) : [green]%-"+strconv.Itoa(threeCol2ValueWidth)+"s[white]",
			fmt.Sprintf("%s :)", strconv.FormatUint(tipDiff, 10)),
		))
	} else if tipDiff <= 600 {
		sb.WriteString(fmt.Sprintf(
			" Tip (diff) : [yellow]%-"+strconv.Itoa(threeCol2ValueWidth)+"s[white]",
			fmt.Sprintf("%s :|", strconv.FormatUint(tipDiff, 10)),
		))
	} else {
		syncProgress := float32((float32(promMetrics.SlotNum) / float32(tipRef)) * 100)
		sb.WriteString(fmt.Sprintf(
			" Syncing    : [yellow]%-"+strconv.Itoa(threeCol2ValueWidth)+"s[white]",
			fmt.Sprintf("%2.1f", syncProgress),
		))
	}
	sb.WriteString(fmt.Sprintf(
		" Total Tx   : [blue]%-"+strconv.Itoa(threeCol3ValueWidth)+"s[white]\n",
		strconv.FormatUint(promMetrics.TxProcessed, 10),
	))
	// Row 3
	sb.WriteString(fmt.Sprintf(
		" Slot epoch : [blue]%-"+strconv.Itoa(threeCol1ValueWidth)+"s[white]",
		strconv.FormatUint(promMetrics.SlotInEpoch, 10),
	))
	sb.WriteString(fmt.Sprintf(
		" Density    : [blue]%-"+strconv.Itoa(threeCol2ValueWidth)+"s[white]",
		fmt.Sprintf("%3.5f", promMetrics.Density*100/1),
	))
	sb.WriteString(fmt.Sprintf(
		" Pending Tx : [blue]%d[white]/[blue]%d[white]%-"+kWidth+"s\n",
		promMetrics.MempoolTx,
		mempoolTxKBytes,
		"K",
	))

	// CONNECTIONS Divider
	sb.WriteString(fmt.Sprintf("- [yellow]CONNECTIONS[white] %s\n",
		strings.Repeat("-", width-13),
	))

	// TODO: actually check for p2p
	p2p := true
	if p2p {
		// Row 1
		sb.WriteString(fmt.Sprintf(
			" P2P        : [green]%-"+strconv.Itoa(threeCol1ValueWidth)+"s[white]",
			"enabled",
		))
		sb.WriteString(fmt.Sprintf(
			" Cold Peers : [blue]%-"+strconv.Itoa(threeCol2ValueWidth)+"s[white]",
			strconv.FormatUint(promMetrics.PeersCold, 10),
		))
		sb.WriteString(fmt.Sprintf(
			" Uni-Dir    : [blue]%-"+strconv.Itoa(threeCol3ValueWidth)+"s[white]\n",
			strconv.FormatUint(promMetrics.ConnUniDir, 10),
		))
		// Row 2
		sb.WriteString(fmt.Sprintf(
			" Incoming   : [blue]%-"+strconv.Itoa(threeCol1ValueWidth)+"s[white]",
			strconv.FormatUint(promMetrics.ConnIncoming, 10),
		))
		sb.WriteString(fmt.Sprintf(
			" Warm Peers : [blue]%-"+strconv.Itoa(threeCol2ValueWidth)+"s[white]",
			strconv.FormatUint(promMetrics.PeersWarm, 10),
		))
		sb.WriteString(fmt.Sprintf(
			" Bi-Dir     : [blue]%-"+strconv.Itoa(threeCol3ValueWidth)+"s[white]\n",
			strconv.FormatUint(promMetrics.ConnBiDir, 10),
		))
		// Row 3
		sb.WriteString(fmt.Sprintf(
			" Outgoing   : [blue]%-"+strconv.Itoa(threeCol1ValueWidth)+"s[white]",
			strconv.FormatUint(promMetrics.ConnOutgoing, 10),
		))
		sb.WriteString(fmt.Sprintf(
			" Hot Peers  : [blue]%-"+strconv.Itoa(threeCol2ValueWidth)+"s[white]",
			strconv.FormatUint(promMetrics.PeersHot, 10),
		))
		sb.WriteString(fmt.Sprintf(
			" Duplex     : [blue]%-"+strconv.Itoa(threeCol3ValueWidth)+"s[white]\n",
			strconv.FormatUint(promMetrics.ConnDuplex, 10),
		))
	} else {
		// Get process in/out connections
		connections, err := processMetrics.ConnectionsWithContext(ctx)
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

		sb.WriteString(fmt.Sprintf(
			" P2P        : [yellow]%-"+strconv.Itoa(threeCol1ValueWidth)+"s[white]",
			"disabled",
		))
		sb.WriteString(fmt.Sprintf(
			" Incoming   : [blue]%-"+strconv.Itoa(threeCol2ValueWidth)+"s[white]",
			strconv.Itoa(len(peersIn)),
		))
		sb.WriteString(fmt.Sprintf(
			" Outgoing   : [blue]%-"+strconv.Itoa(threeCol3ValueWidth)+"s[white]\n",
			strconv.Itoa(len(peersOut)),
		))
	}

	// BLOCK PROPAGATION Divider
	sb.WriteString(fmt.Sprintf("- [yellow]BLOCK PROPAGATION[white] %s\n",
		strings.Repeat("-", width-19),
	))

	blk1s := fmt.Sprintf("%.2f", promMetrics.BlocksW1s*100)
	blk3s := fmt.Sprintf("%.2f", promMetrics.BlocksW3s*100)
	blk5s := fmt.Sprintf("%.2f", promMetrics.BlocksW5s*100)
	delay := fmt.Sprintf("%.2f", promMetrics.BlockDelay)

	// Row 1
	sb.WriteString(fmt.Sprintf(
		" Last Delay : [blue]%s[white]%-"+strconv.Itoa(threeCol1ValueWidth-len(delay))+"s",
		delay,
		"s",
	))
	sb.WriteString(fmt.Sprintf(
		" Served     : [blue]%-"+strconv.Itoa(threeCol2ValueWidth)+"s[white]",
		strconv.FormatUint(promMetrics.BlocksServed, 10),
	))
	sb.WriteString(fmt.Sprintf(
		" Late (>5s) : [blue]%-"+strconv.Itoa(threeCol3ValueWidth)+"s[white]\n",
		strconv.FormatUint(promMetrics.BlocksLate, 10),
	))
	// Row 2
	sb.WriteString(fmt.Sprintf(
		" Within 1s  : [blue]%s[white]%-"+strconv.Itoa(threeCol1ValueWidth-len(blk1s))+"s",
		blk1s,
		"%",
	))
	sb.WriteString(fmt.Sprintf(
		" Within 3s  : [blue]%s[white]%-"+strconv.Itoa(threeCol2ValueWidth-len(blk3s))+"s",
		blk3s,
		"%",
	))
	sb.WriteString(fmt.Sprintf(
		" Within 5s  : [blue]%s[white]%-"+strconv.Itoa(threeCol3ValueWidth-len(blk5s))+"s\n",
		blk5s,
		"%",
	))

	// NODE RESOURCE USAGE Divider
	sb.WriteString(fmt.Sprintf("- [yellow]NODE RESOURCE USAGE[white] %s\n",
		strings.Repeat("-", width-21),
	))

	var cpuPercent float64 = 0.0
	var rss uint64 = 0
	var processMemory *process.MemoryInfoStat
	if processMetrics.Pid != 0 {
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
	cWidth := strconv.Itoa(threeCol1ValueWidth - len(fmt.Sprintf("%.2f", cpuPercent)))

	memRss := fmt.Sprintf("%.1f", float64(rss)/float64(1073741824))
	memLive := fmt.Sprintf("%.1f", float64(promMetrics.MemLive)/float64(1073741824))
	memHeap := fmt.Sprintf("%.1f", float64(promMetrics.MemHeap)/float64(1073741824))

	// Row 1
	sb.WriteString(fmt.Sprintf(
		" CPU (sys)  : [blue]%s[white]%-"+cWidth+"s",
		fmt.Sprintf("%.2f", cpuPercent),
		"%",
	))
	sb.WriteString(fmt.Sprintf(
		" Mem (Live) : [blue]%s[white]%-"+strconv.Itoa(threeCol2ValueWidth-len(memLive))+"s",
		memLive,
		"G",
	))
	sb.WriteString(fmt.Sprintf(
		" GC Minor   : [blue]%-"+strconv.Itoa(threeCol3ValueWidth)+"s[white]\n",
		strconv.FormatUint(promMetrics.GcMinor, 10),
	))
	// Row 2
	sb.WriteString(fmt.Sprintf(
		" Mem (RSS)  : [blue]%s[white]%-"+strconv.Itoa(threeCol1ValueWidth-len(memRss))+"s",
		memRss,
		"G",
	))
	sb.WriteString(fmt.Sprintf(
		" Mem (Heap) : [blue]%s[white]%-"+strconv.Itoa(threeCol2ValueWidth-len(memHeap))+"s",
		memHeap,
		"G",
	))
	sb.WriteString(fmt.Sprintf(
		" GC Major   : [blue]%-"+strconv.Itoa(threeCol3ValueWidth)+"s[white]\n",
		strconv.FormatUint(promMetrics.GcMajor, 10),
	))

	// Core section
	if role == "Core" {
		// Core Divider
		sb.WriteString(fmt.Sprintf("- [yellow]CORE[white] %s\n",
			strings.Repeat("-", width-6),
		))

		// Row 1
		sb.WriteString(fmt.Sprintf(" KES current/remaining %"+strconv.Itoa(twoColSecond-1-22)+"s: ",
			" ",
		))
		sb.WriteString(fmt.Sprintf("[blue]%d[white] / ", promMetrics.KesPeriod))
		if promMetrics.RemainingKesPeriods <= 0 {
			sb.WriteString(fmt.Sprintf("[fuchsia]%d[white]\n", promMetrics.RemainingKesPeriods))
		} else if promMetrics.RemainingKesPeriods <= 8 {
			sb.WriteString(fmt.Sprintf("[red]%d[white]\n", promMetrics.RemainingKesPeriods))
		} else {
			sb.WriteString(fmt.Sprintf("[blue]%d[white]\n", promMetrics.RemainingKesPeriods))
		}
		// Row 2
		sb.WriteString(fmt.Sprintf(" KES expiration date %"+strconv.Itoa(twoColSecond-1-20)+"s: ",
			" ",
		))
		kesString := strings.Replace(strings.Replace(kesExpiration(genesisConfig, promMetrics).Format(time.RFC3339), "Z", " ", 1), "T", " ", 1)
		sb.WriteString(fmt.Sprintf("[blue]%-"+strconv.Itoa(twoColWidth)+"s[white]\n", kesString))
		// Row 3
		sb.WriteString(fmt.Sprintf(" Missed slot leader checks %"+strconv.Itoa(twoColSecond-1-26)+"s: ",
			" ",
		))
		var missedSlotsPct float32
		if promMetrics.AboutToLead > 0 {
			missedSlotsPct = float32(promMetrics.MissedSlots) / (float32(promMetrics.AboutToLead + promMetrics.MissedSlots)) * 100
		}
		sb.WriteString(fmt.Sprintf("[blue]%s[white] ([blue]%s[white] %%)\n",
			strconv.FormatUint(promMetrics.MissedSlots, 10),
			fmt.Sprintf("%.4f", missedSlotsPct),
		))

		// BLOCK PRODUCTION Divider
		sb.WriteString(fmt.Sprintf("- [yellow]BLOCK PRODUCTION[white] %s\n",
			strings.Repeat("-", width-18),
		))

		// TODO: block log functionality
		var adoptedFmt string = "green"
		var invalidFmt string = "blue"
		if promMetrics.IsLeader != promMetrics.Adopted {
			adoptedFmt = "yellow"
		}
		if promMetrics.DidntAdopt != 0 {
			invalidFmt = "red"
		}
		leader := strconv.FormatUint(promMetrics.IsLeader, 10)
		sb.WriteString(fmt.Sprintf(
			" Leader : [blue]%-"+strconv.Itoa(threeCol1ValueWidth-len(leader))+"s[white] ",
			leader,
		))
		sb.WriteString("     ") // 5 spaces extra
		adopted := strconv.FormatUint(promMetrics.Adopted, 10)
		sb.WriteString(fmt.Sprintf(
			"Adopted : ["+adoptedFmt+"]%-"+strconv.Itoa(threeCol2ValueWidth-len(adopted))+"s[white] ",
			adopted,
		))
		sb.WriteString("    ") // 4 spaces extra
		invalid := strconv.FormatUint(promMetrics.DidntAdopt, 10)
		sb.WriteString(fmt.Sprintf(
			"Invalid : ["+invalidFmt+"]%-"+strconv.Itoa(threeCol3ValueWidth-len(invalid))+"s[white] ",
			invalid,
		))
	}

	failCount = 0
	return fmt.Sprint(sb.String())
}

func getInfoText(ctx context.Context) string {
	// Refresh metrics from host
	processMetrics, err := getProcessMetrics(ctx)
	if err != nil {
		uptimes = 0
	} else {
		// Calculate uptime for our process
		createTime, err := processMetrics.CreateTimeWithContext(ctx)
		if err == nil {
			// createTime is milliseconds since UNIX epoch, convert to seconds
			uptimes = uint64(time.Now().Unix() - (createTime / 1000))
		}
	}

	var sb strings.Builder

	// Style / UI
	var width = 71

	var twoColWidth int = (width - 3) / 2
	var twoColSecond int = twoColWidth + 2

	// Main section
	uptime := timeLeft(uptimes)
	sb.WriteString(fmt.Sprintf(" Uptime: [blue]%-"+strconv.Itoa(twoColSecond-9-len(uptime))+"s[white]", uptime))
	sb.WriteString(fmt.Sprintf(" nview Version: [blue]%-"+strconv.Itoa(twoColWidth)+"s[white]\n", version.GetVersionString()))
	sb.WriteString(fmt.Sprintf("%s\n", strings.Repeat("-", width+1)))

	if showPeers {
		sb.WriteString(fmt.Sprintf(
			"[white:black:r] INFO [white:-:-] One-shot peer analysis last run at [blue]%s\n\n",
			time.Unix(int64(peerAnalysisDate), 0),
		))

		sb.WriteString(" Runs a latency test on connections to the node.\n")
		sb.WriteString(" Once the analysis is finished, RTTs(Round Trip Time) for each peer\n")
		sb.WriteString(" is display and grouped in ranges of 0-50, 50-100, 100-200, 200<.\n\n")
	} else {
		sb.WriteString(
			"[white:black:r] INFO [white:-:-] Displays live metrics gathered from node Prometheus endpoint\n\n",
		)

		sb.WriteString(" [green]Main Section[white]\n")
		sb.WriteString(" Epoch number is live from the node.\n\n")
		sb.WriteString(" Tip reference and diff are not yet available.\n\n")
		sb.WriteString(" Forks is how many times the blockchain branched off in a different\n")
		sb.WriteString(" direction since node start (and discarded blocks by doing so).\n\n")
		sb.WriteString(" P2P Connections shows how many peers the node pushes to/pulls from.\n\n")
		sb.WriteString(" Block propagation metrics are discussed in the documentation.\n\n")
		sb.WriteString(" RSS/Live/Heap shows the memory utilization of RSS/live/heap data.\n")
	}

	failCount = 0
	return fmt.Sprint(sb.String())
}

var peerAnalysisDate uint64

var checkPeers bool = false
var pingPeers bool = false
var showPeers bool = false

//nolint:unused
func getPeerText(ctx context.Context) string {
	cfg := GetConfig()
	// Refresh metrics from host
	processMetrics, err := getProcessMetrics(ctx)
	if err != nil {
		uptimes = 0
		failCount++
		return fmt.Sprintf(" [red]Could not get process metrics![white]%s\n", "")
	} else {
		// Calculate uptime for our process
		createTime, err := processMetrics.CreateTimeWithContext(ctx)
		if err == nil {
			// createTime is milliseconds since UNIX epoch, convert to seconds
			uptimes = uint64(time.Now().Unix() - (createTime / 1000))
		}
	}

	var sb strings.Builder

	// Style / UI
	var width = 71

	var twoColWidth int = (width - 3) / 2
	var twoColSecond int = twoColWidth + 2

	// Main section
	uptime := timeLeft(uptimes)
	sb.WriteString(fmt.Sprintf(" Uptime: [blue]%-"+strconv.Itoa(twoColSecond-9-len(uptime))+"s[white]", uptime))
	sb.WriteString(fmt.Sprintf(" nview Version: [blue]%-"+strconv.Itoa(twoColWidth)+"s[white]\n", version.GetVersionString()))
	sb.WriteString(fmt.Sprintf("%s\n", strings.Repeat("-", width+1)))

	// bail on FreeBSD due to missing connections support
	if runtime.GOOS == "freebsd" {
		sb.WriteString(fmt.Sprintf(" [yellow]%s[white]\n",
			"FreeBSD peer analysis is currently unsupported",
		))
		return fmt.Sprint(sb.String())
	}

	// Get process in/out connections
	connections, err := processMetrics.ConnectionsWithContext(ctx)
	if err != nil {
		sb.WriteString(fmt.Sprintf(" [red]Failed to get processes[white]: %v", err))
		return fmt.Sprint(sb.String())
	}

	var peersIn []string
	var peersOut []string

	// Loops each connection, looking for ESTABLISHED
	for _, c := range connections {
		if c.Status == "ESTABLISHED" {
			// If local port == node port, it's incoming (except P2P)
			if c.Laddr.Port == cfg.Node.Port {
				peersIn = append(peersIn, fmt.Sprintf("%s:%d", c.Raddr.IP, c.Raddr.Port))
			}
			// If local port != node port, ekg port, or prometheus port, it's outgoing
			if c.Laddr.Port != cfg.Node.Port && c.Laddr.Port != uint32(12788) &&
				c.Laddr.Port != cfg.Prometheus.Port {
				peersOut = append(peersOut, fmt.Sprintf("%s:%d", c.Raddr.IP, c.Raddr.Port))
			}
		}
	}

	// Start "checkPeers"
	var peersFiltered []string

	ip, _ := getPublicIP(ctx)
	if ip != nil {
		if !checkPeers && !pingPeers {
			sb.WriteString(fmt.Sprintf(" Public IP : %s\n", ip))
		}
	}

	// Skip everything if we have no peers
	if len(peersIn) == 0 && len(peersOut) == 0 {
		sb.WriteString(fmt.Sprintf("%s\n",
			" [yellow]No peers found[white]",
		))
		failCount = 0
		return fmt.Sprint(sb.String())
	}

	// Process peersIn
	for _, peer := range peersIn {
		p := strings.Split(peer, ":")
		peerIP := p[0]
		peerPORT := p[1]
		if strings.HasPrefix(peerIP, "[") { // IPv6
			peerIP = strings.TrimPrefix(strings.TrimSuffix(peerIP, "]"), "[")
		}

		if peerIP == "127.0.0.1" ||
			(peerIP == ip.String() && peerPORT == strconv.FormatUint(uint64(cfg.Node.Port), 10)) {
			// Do nothing
			continue
		} else {
			// TODO: filter duplicates
			peersFiltered = append(peersFiltered, fmt.Sprintf("%s;%s;i", peerIP, peerPORT))
		}
	}

	// Process peersOut
	for _, peer := range peersOut {
		p := strings.Split(peer, ":")
		peerIP := p[0]
		peerPORT := p[1]
		if strings.HasPrefix(peerIP, "[") { // IPv6
			peerIP = strings.TrimPrefix(strings.TrimSuffix(peerIP, "]"), "[")
		}

		if peerIP == "127.0.0.1" ||
			(peerIP == ip.String() && peerPORT == strconv.FormatUint(uint64(cfg.Node.Port), 10)) {
			// Do nothing
			continue
		} else {
			// TODO: filter duplicates
			peersFiltered = append(peersFiltered, fmt.Sprintf("%s;%s;o", peerIP, peerPORT))
		}
	}

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
	granularity := width - 3
	granularitySmall := granularity / 2
	if checkPeers {
		sb.WriteString(fmt.Sprintf(" [yellow]%-"+strconv.Itoa(width-3)+"s[white]\n",
			"Peer analysis started... please wait!",
		))

		checkPeers = false
		pingPeers = true
		// sb.WriteString(fmt.Sprintf("checkPeers=%v, pingPeers=%v, showPeers=%v\n", checkPeers, pingPeers, showPeers))
		failCount = 0
		return sb.String()
	} else if pingPeers {
		pingPeers = false
		peerCount := len(peersFiltered)
		printStart := width - (peerCount * 2) - 2
		sb.WriteString(fmt.Sprintf("%"+strconv.Itoa(printStart-1)+"s [blue]%"+strconv.Itoa(peerCount)+"s[white]/[green]%d[white]\n",
			" ", "0", peerCount,
		))
		//var index int
		var lastpeerIP string
		// counters, etc.
		var peerRTT int
		for _, v := range peersFiltered {
			peerArr := strings.Split(v, ";")
			peerIP := peerArr[0]
			peerPORT := peerArr[1]
			peerDIR := peerArr[2]

			// TODO: geolocation

			if peerIP == lastpeerIP && peerRTT != 99999 {
				peerStats.RTTSUM = peerStats.RTTSUM + peerRTT // skip RTT check and reuse old peerRTT
			} else {
				// Start RTT loop
				// for tool in ... return peerRTT
				sb.WriteString(fmt.Sprintf(" Getting RTT for: %s:%s\n", peerIP, peerPORT))
				peerRTT = tcpinfoRtt(fmt.Sprintf("%s:%s", peerIP, peerPORT))
				if peerRTT != 99999 {
					peerStats.RTTSUM = peerStats.RTTSUM + peerRTT
				}

			}
			lastpeerIP = peerIP
			// Update counters
			if peerRTT < 50 {
				peerStats.CNT1 = peerStats.CNT1 + 1
			} else if peerRTT < 100 {
				peerStats.CNT2 = peerStats.CNT2 + 1
			} else if peerRTT < 200 {
				peerStats.CNT3 = peerStats.CNT3 + 1
			} else if peerRTT < 99999 {
				peerStats.CNT4 = peerStats.CNT4 + 1
			} else {
				peerStats.CNT0 = peerStats.CNT0 + 1
			}
			peerPort, err := strconv.Atoi(peerPORT)
			if err != nil {
				return fmt.Sprintf(" [red]%s[white]", "Unable to convert port to string!")
			}
			peerStats.RTTresults = append(peerStats.RTTresults, Peer{
				IP:        peerIP,
				Port:      peerPort,
				Direction: peerDIR,
				RTT:       peerRTT,
			})
			sort.SliceStable(peerStats.RTTresults, func(i, j int) bool {
				return peerStats.RTTresults[i].RTT < peerStats.RTTresults[j].RTT
			})
		}
		peerCNTreachable := peerCount - peerStats.CNT0
		if peerCNTreachable > 0 {
			peerStats.RTTAVG = peerStats.RTTSUM / peerCNTreachable
			peerStats.PCT1 = float32(peerStats.CNT1) / float32(peerCNTreachable) * 100
			peerStats.PCT1items = int(peerStats.PCT1) * granularitySmall / 100
			peerStats.PCT2 = float32(peerStats.CNT2) / float32(peerCNTreachable) * 100
			peerStats.PCT2items = int(peerStats.PCT2) * granularitySmall / 100
			peerStats.PCT3 = float32(peerStats.CNT3) / float32(peerCNTreachable) * 100
			peerStats.PCT3items = int(peerStats.PCT3) * granularitySmall / 100
			peerStats.PCT4 = float32(peerStats.CNT4) / float32(peerCNTreachable) * 100
			peerStats.PCT4items = int(peerStats.PCT4) * granularitySmall / 100
		}
		// TODO: lookup geoIP data
		sb.WriteString(fmt.Sprintf(" [yellow]%-46s[white]\n", "Peer analysis done!"))
		peerAnalysisDate = uint64(time.Now().Unix() - 1)
		checkPeers = false
		showPeers = true
		// sb.WriteString(fmt.Sprintf("checkPeers=%v, pingPeers=%v, showPeers=%v\n", checkPeers, pingPeers, showPeers))
		failCount = 0
		return sb.String()
	} else if showPeers {
		peerCount := len(peersFiltered)
		sb.WriteString("       RTT : Peers / Percent\n")
		sb.WriteString(fmt.Sprintf(
			"    0-50ms : [blue]%5s[white]   [blue]%.f[white]%%",
			strconv.Itoa(peerStats.CNT1),
			peerStats.PCT1,
		))
		sb.WriteString(fmt.Sprintf(
			"%"+strconv.Itoa(10-len(fmt.Sprintf("%.f", peerStats.PCT1)))+"s",
			" ",
		))
		for i := 0; i < granularitySmall; i++ {
			if i < int(peerStats.PCT1) {
				sb.WriteString(fmt.Sprintf("[green]%s", charMarked))
			} else {
				sb.WriteString(fmt.Sprintf("[white]%s", charUnmarked))
			}
		}
		sb.WriteString("[white]\n") // closeRow
		sb.WriteString(fmt.Sprintf(
			"  50-100ms : [blue]%5s[white]   [blue]%.f[white]%%",
			strconv.Itoa(peerStats.CNT2),
			peerStats.PCT2,
		))
		sb.WriteString(fmt.Sprintf(
			"%"+strconv.Itoa(10-len(fmt.Sprintf("%.f", peerStats.PCT2)))+"s",
			"",
		))
		for i := 0; i < granularitySmall; i++ {
			if i < int(peerStats.PCT2) {
				sb.WriteString(fmt.Sprintf("[yellow]%s", charMarked))
			} else {
				sb.WriteString(fmt.Sprintf("[white]%s", charUnmarked))
			}
		}
		sb.WriteString("[white]\n") // closeRow
		sb.WriteString(fmt.Sprintf(
			" 100-200ms : [blue]%5s[white]   [blue]%.f[white]%%",
			strconv.Itoa(peerStats.CNT3),
			peerStats.PCT3,
		))
		sb.WriteString(fmt.Sprintf(
			"%"+strconv.Itoa(10-len(fmt.Sprintf("%.f", peerStats.PCT3)))+"s",
			"",
		))
		for i := 0; i < granularitySmall; i++ {
			if i < int(peerStats.PCT3) {
				sb.WriteString(fmt.Sprintf("[red]%s", charMarked))
			} else {
				sb.WriteString(fmt.Sprintf("[white]%s", charUnmarked))
			}
		}
		sb.WriteString("[white]\n") // closeRow
		sb.WriteString(fmt.Sprintf(
			"   200ms < : [blue]%5s[white]   [blue]%.f[white]%%",
			strconv.Itoa(peerStats.CNT4),
			peerStats.PCT4,
		))
		sb.WriteString(fmt.Sprintf(
			"%"+strconv.Itoa(10-len(fmt.Sprintf("%.f", peerStats.PCT4)))+"s",
			"",
		))
		for i := 0; i < granularitySmall; i++ {
			if i < int(peerStats.PCT4) {
				sb.WriteString(fmt.Sprintf("[fuchsia]%s", charMarked))
			} else {
				sb.WriteString(fmt.Sprintf("[white]%s", charUnmarked))
			}
		}
		sb.WriteString("[white]\n") // closeRow

		// Divider
		sb.WriteString(fmt.Sprintf("%s\n", strings.Repeat("-", width-1)))

		sb.WriteString(fmt.Sprintf(" Total / Undetermined : [blue]%d[white] / ", peerCount))
		if peerStats.CNT0 == 0 {
			sb.WriteString("[blue]0[white]")
		} else {
			sb.WriteString(fmt.Sprintf("[fuchsia]%d[white]", peerStats.CNT0))
		}
		// TODO: figure out spacing here
		if peerStats.RTTAVG >= 200 {
			sb.WriteString(fmt.Sprintf(" Average RTT : [fuchsia]%d[white] ms\n", peerStats.RTTAVG))
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
		sb.WriteString(fmt.Sprintf("%s\n", strings.Repeat("-", width-1)))

		sb.WriteString(fmt.Sprintf("[blue]   # %24s  I/O RTT   Geolocation[white] ([green]Coming soon![white])\n", "REMOTE PEER"))
		peerNbrStart := 1
		// peerLocationWidth := width - 41
		for peerNbr, peer := range peerStats.RTTresults {
			if peerNbr < peerNbrStart {
				continue
			}
			// sb.WriteString(fmt.Sprintf(" DEBUG: peer=%#v\n", peer))
			peerRTT := peer.RTT
			peerPORT := peer.Port
			peerDIR := peer.Direction
			peerIP := peer.IP
			if strings.Contains(peer.IP, ":") {
				if len(strings.Split(peer.IP, ":")) > 3 {
					splitIP := strings.Split(peer.IP, ":")
					peerIP = fmt.Sprintf("%s...%s:%s",
						splitIP[0],
						splitIP[:len(splitIP)-2],
						splitIP[:len(splitIP)-1],
					)
				}
			}
			// TODO: geolocation
			peerLocationFmt := "---"

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
		// sb.WriteString(fmt.Sprintf("checkPeers=%v, pingPeers=%v, showPeers=%v\n", checkPeers, pingPeers, showPeers))
	}
	sb.WriteString("[white]\n")

	// Display progress
	//sb.WriteString(fmt.Sprintf(" Incoming peers: %v\n", peersIn))
	//sb.WriteString(fmt.Sprintf(" Outgoing peers: %v\n", peersOut))
	//sb.WriteString(fmt.Sprintf(" Filtered peers: %v\n\n", peersFiltered))
	//sb.WriteString(fmt.Sprintf(" PeerStats:      %#v\n\n", peerStats))

	failCount = 0
	return fmt.Sprint(sb.String())
}

var peerStats PeerStats

type PeerStats struct {
	RTTSUM     int
	RTTAVG     int
	CNT0       int
	CNT1       int
	CNT2       int
	CNT3       int
	CNT4       int
	PCT1       float32
	PCT2       float32
	PCT3       float32
	PCT4       float32
	PCT1items  int
	PCT2items  int
	PCT3items  int
	PCT4items  int
	RTTresults []Peer
}

type Peer struct {
	Direction string
	IP        string
	RTT       int
	Port      int
}

func getProcessMetrics(ctx context.Context) (*process.Process, error) {
	cfg := GetConfig()
	r, _ := process.NewProcessWithContext(ctx, 0)
	processes, err := process.ProcessesWithContext(ctx)
	if err != nil {
		return r, fmt.Errorf("failed to get processes: %s", err)
	}
	for _, p := range processes {
		n, err := p.NameWithContext(ctx)
		if err != nil {
			return r, fmt.Errorf("failed to get process name: %s", err)
		}
		c, err := p.CmdlineWithContext(ctx)
		if err != nil {
			return r, fmt.Errorf("failed to get process cmdline: %s", err)
		}
		if strings.Contains(n, cfg.Node.Binary) &&
			strings.Contains(c, strconv.FormatUint(uint64(cfg.Node.Port), 10)) {
			r = p
		}
	}
	return r, nil
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
	var result int = 99999
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
	var q *tcpinfo.Info
	if err := json.Unmarshal(txt, &q); err != nil {
		result = int(q.RTT.Seconds() * 1000)
	}
	return result
}
