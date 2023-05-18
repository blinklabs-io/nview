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
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/gdamore/tcell/v2"
	dto "github.com/prometheus/client_model/go"
	"github.com/prometheus/common/expfmt"
	"github.com/rivo/tview"
	"github.com/shirou/gopsutil/v3/process"
	terminal "golang.org/x/term"
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
	SetDynamicColors(true).
	SetTextAlign(tview.AlignCenter)
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
		text.SetText(fmt.Sprintf(" [red]Cannot get metrics from node![white]\n [red]ERROR[white]: %s", err))
	}
	// Set current epoch from Prometheus metrics
	currentEpoch = uint32(metrics.EpochNum)
	// TODO: temp hack to use currentEpoch
	if currentEpoch > 0 {
		// Do something non-useful
		text.SetText(fmt.Sprintf("%d", currentEpoch))
	}

	// Populate initial text from metrics
	text.SetText(getPromText(ctx, metrics)).SetBorder(true)

	// Set our header
	var network string
	if cfg.App.Network != "" {
		network = strings.ToUpper(cfg.App.Network[:1]) + cfg.App.Network[1:]
	} else {
		network = strings.ToUpper(cfg.Node.Network[:1]) + cfg.Node.Network[1:]
	}
	defaultHeaderText := fmt.Sprintf("> [green]%s[white] - [yellow](%s - %s)[white] : [blue]%s[white] [[blue]%s[white]] <",
		cfg.App.NodeName,
		role,
		network,
		"1.35.7",   // TODO: get the real Version
		"abcd1234", // TODO: get the real Revision
	)
	headerText.SetText(defaultHeaderText)

	// Set our footer
	defaultFooterText := " [yellow](esc/q) Quit[white] | [yellow](i) Info[white] | [yellow](r) Refresh Prometheus"
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
			5,
			true).
		// Row 3 is our footer
		AddItem(footerText, 0, 1, false)

	// capture inputs
	flex.SetInputCapture(func(event *tcell.EventKey) *tcell.EventKey {
		if event.Rune() == 104 || event.Rune() == 114 { // h or r
			active = "main"
			text.Clear()
			footerText.Clear()
			footerText.SetText(defaultFooterText)
			metrics, err = getPromMetrics(ctx)
			if err != nil {
				text.SetText(fmt.Sprintf(" [red]Cannot get metrics from node![white]\n [red]ERROR[white]: %s", err))
			}
			text.SetText(getPromText(ctx, metrics))
		}
		if event.Rune() == 105 { // i
			active = "info"
			text.Clear()
			footerText.Clear()
			footerText.SetText(" [yellow](esc/q) Quit[white] | [yellow](h) Return home")
			text.SetText(getInfoText(ctx))
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
				text.SetText(fmt.Sprintf(" [red]Cannot get metrics from node![white]\n [red]ERROR[white]: %s", err))
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
				panic(fmt.Errorf("COULD NOT CONNECT TO A RUNNING INSTANCE, %d FAILED ATTEMPTS IN A ROW!", failCount))
			}
			if active == "main" {
				text.Clear()
				metrics, err = getPromMetrics(ctx)
				if err != nil {
					text.SetText(fmt.Sprintf(" [red]Cannot get metrics from node![white]\n [red]ERROR[white]: %s", err))
				}
				text.SetText(getPromText(ctx, metrics))
			}
			if active == "test" {
				text.Clear()
				metrics, err = getPromMetrics(ctx)
				if err != nil {
					text.SetText(fmt.Sprintf(" [red]Cannot get metrics from node![white]\n [red]ERROR[white]: %s", err))
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

// TODO: Track size of epoch items
// var epochItemsLast uint32 = 0

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

	// Main section
	uptime := timeLeft(uptimes)
	sb.WriteString(fmt.Sprintf(" Uptime: [blue]%s[white]\n", uptime))
	sb.WriteString(fmt.Sprintf("%s\n", strings.Repeat("-", 20)))

	// Epoch progress
	var epochProgress float32
	genesisConfig := getGenesisConfig(cfg)
	if promMetrics.EpochNum >= uint64(cfg.Node.ShelleyTransEpoch) {
		epochProgress = float32((float32(promMetrics.SlotInEpoch) / float32(genesisConfig.EpochLength)) * 100)
	} else {
		epochProgress = float32((float32(promMetrics.SlotInEpoch) / float32(cfg.Node.ByronGenesis.EpochLength)) * 100)
	}
	epochProgress1dec := fmt.Sprintf("%.1f", epochProgress)
	epochTimeLeft := timeLeft(timeUntilNextEpoch())

	// Epoch
	sb.WriteString(fmt.Sprintf(" Epoch [blue]%d[white] [[blue]%s%%[white]], [blue]%s[white] %-12s\n\n", promMetrics.EpochNum, epochProgress1dec, epochTimeLeft, "remaining"))

	// Epoch Debug
	sb.WriteString(fmt.Sprintf(" Epoch Debug%s\n", ""))
	currentTimeSec := uint64(time.Now().Unix() - 1)
	sb.WriteString(fmt.Sprintf("currentTimeSec    = %d\n", currentTimeSec))
	sb.WriteString(fmt.Sprintf("startTime         = %d\n", cfg.Node.ByronGenesis.StartTime))
	sb.WriteString(fmt.Sprintf("rhs               = %d\n", (uint64(cfg.Node.ShelleyTransEpoch)*cfg.Node.ByronGenesis.EpochLength*cfg.Node.ByronGenesis.SlotLength)/1000))
	byronEndTime := uint64(cfg.Node.ByronGenesis.StartTime + ((uint64(cfg.Node.ShelleyTransEpoch) * cfg.Node.ByronGenesis.EpochLength * cfg.Node.ByronGenesis.SlotLength) / 1000))
	sb.WriteString(fmt.Sprintf("byronEndTime      = %d\n", byronEndTime))
	sb.WriteString(fmt.Sprintf("math, currentTimeSec - byronEndTime = %d\n", (currentTimeSec - byronEndTime)))

	result := uint64(cfg.Node.ShelleyTransEpoch) + ((currentTimeSec - byronEndTime) / cfg.Node.ByronGenesis.EpochLength / cfg.Node.ByronGenesis.SlotLength)
	sb.WriteString(fmt.Sprintf("result=%d\n", result))

	sb.WriteString(fmt.Sprintf(" Epoch getEpoch: %d\n", getEpoch()))
	sb.WriteString(fmt.Sprintf(" Epoch timeUntilNextEpoch: %d\n",
		((uint64(cfg.Node.ShelleyTransEpoch)*cfg.Node.ByronGenesis.EpochLength*cfg.Node.ByronGenesis.SlotLength)/1000)+((promMetrics.EpochNum+1-uint64(cfg.Node.ShelleyTransEpoch))*cfg.Node.ByronGenesis.EpochLength*cfg.Node.ByronGenesis.SlotLength)-currentTimeSec+cfg.Node.ByronGenesis.StartTime))
	sb.WriteString(fmt.Sprintf("   timeLeft now: %s\n\n\n", timeLeft(((uint64(cfg.Node.ShelleyTransEpoch)*cfg.Node.ByronGenesis.EpochLength*cfg.Node.ByronGenesis.SlotLength)/1000)+((promMetrics.EpochNum+1-uint64(cfg.Node.ShelleyTransEpoch))*cfg.Node.ByronGenesis.EpochLength*cfg.Node.ByronGenesis.SlotLength)-currentTimeSec+cfg.Node.ByronGenesis.StartTime)))

	// Genesis Config
	sb.WriteString(fmt.Sprintf(" Genesis Config: %#v\n\n", genesisConfig))

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
			// If local port == node port, it's incoming (except P2P)
			if c.Laddr.Port == cfg.Node.Port {
				peersIn = append(peersIn, fmt.Sprintf("%s:%d", c.Raddr.IP, c.Raddr.Port))
			}
			// If local port != node port, ekg port, or prometheus port, it's outgoing
			if c.Laddr.Port != cfg.Node.Port && c.Laddr.Port != uint32(12788) && c.Laddr.Port != cfg.Prometheus.Port {
				peersOut = append(peersOut, fmt.Sprintf("%s:%d", c.Raddr.IP, c.Raddr.Port))
			}
		}
	}

	// Start "checkPeers"
	var peersFiltered []string

	// First, check for external address using custom resolver so we can
	// use a given DNS server to resolve our public address
	r := &net.Resolver{
		PreferGo: true,
		Dial: func(ctx context.Context, network, address string) (net.Conn, error) {
			d := net.Dialer{
				Timeout: time.Second * time.Duration(3),
			}
			return d.DialContext(ctx, network, "resolver1.opendns.com:53")
		},
	}
	// Lookup special address to get our public IP
	ips, _ := r.LookupIP(ctx, "ip4", "myip.opendns.com")
	var ip net.IP
	if ips != nil {
		ip = ips[0]
		sb.WriteString(fmt.Sprintf(" Public IP info: %s\n", ip))
	}

	// Process peersIn
	for _, peer := range peersIn {
		p := strings.Split(peer, ":")
		peerIP := p[0]
		peerPORT := p[1]
		if strings.HasPrefix(peerIP, "[") { // IPv6
			peerIP = strings.TrimPrefix(strings.TrimSuffix(peerIP, "]"), "[")
		}

		if peerIP == "127.0.0.1" || (peerIP == ip.String() && peerPORT == strconv.FormatUint(uint64(cfg.Node.Port), 10)) {
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

		if peerIP == "127.0.0.1" || (peerIP == ip.String() && peerPORT == strconv.FormatUint(uint64(cfg.Node.Port), 10)) {
			// Do nothing
			continue
		} else {
			// TODO: filter duplicates
			peersFiltered = append(peersFiltered, fmt.Sprintf("%s;%s;o", peerIP, peerPORT))
		}
	}

	// Display progress
	sb.WriteString(fmt.Sprintf(" Incoming peers: %v\n", peersIn))
	sb.WriteString(fmt.Sprintf(" Outgoing peers: %v\n", peersOut))
	sb.WriteString(fmt.Sprintf(" Filtered peers: %v\n\n", peersFiltered))

	// Some Debugging
	sb.WriteString(fmt.Sprintf(" Application config: %#v\n\n", cfg))

	// Get protocol parameters
	protoParams := getProtocolParams(cfg)
	sb.WriteString(fmt.Sprintf(" Protocol params: %#v\n\n", protoParams))

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

	// Main section
	uptime := timeLeft(uptimes)
	sb.WriteString(fmt.Sprintf(" Uptime: [blue]%s[white]\n", uptime))
	sb.WriteString(fmt.Sprintf("%s\n", strings.Repeat("-", 20)))

	sb.WriteString("[white:black:r] INFO [white:-:-] Displays live metrics gathered from node Prometheus endpoint\n\n")

	sb.WriteString(" [green]Main Section[white]\n")
	sb.WriteString(" Epoch number is live from the node.\n\n")
	sb.WriteString(" Tip reference and diff are not yet available.\n\n")
	sb.WriteString(" Forks is how many times the blockchain branched off in a different\n")
	sb.WriteString(" direction since node start (and discarded blocks by doing so).\n\n")
	sb.WriteString(" P2P Connections shows how many peers the node pushes to/pulls from.\n\n")
	sb.WriteString(" Block propagation metrics are discussed in the documentation.\n\n")
	sb.WriteString(" RSS/Live/Heap shows the memory utilization of RSS/live/heap data.\n")

	failCount = 0
	return fmt.Sprint(sb.String())
}

func getPromText(ctx context.Context, promMetrics *PromMetrics) string {
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
	if promMetrics.AboutToLead > 0 {
		if role != "Core" {
			role = "Core"
		}
	} else if role != "Relay" {
		role = "Relay"
	}

	// Style / UI
	var width = 71
	var threeColWidth = (width - 3) / 2
	//var threeCol2Start = threeColWidth+3
	//var threeCol3Start = threeColWidth*2+4
	var threeCol1ValueWidth = threeColWidth - 12
	var threeCol2ValueWidth = threeColWidth - 12
	var threeCol3ValueWidth = threeColWidth - 12

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
	sb.WriteString(fmt.Sprintf(" Uptime: [blue]%s[white]\n", uptime))
	sb.WriteString(fmt.Sprintf("%s\n", strings.Repeat("-", 20)))

	// Epoch progress
	var epochProgress float32
	genesisConfig := getGenesisConfig(cfg)
	if promMetrics.EpochNum >= uint64(cfg.Node.ShelleyTransEpoch) {
		epochProgress = float32((float32(promMetrics.SlotInEpoch) / float32(genesisConfig.EpochLength)) * 100)
	} // TODO: support Byron epochs: else { epochProgress = float32((float32(promMetrics.SlotInEpoch) / float32(BYRON_EPOCH_LENGTH)) * 100)
	epochProgress1dec := fmt.Sprintf("%.1f", epochProgress)
	// epochTimeLeft := timeLeft(timeUntilNextEpoch())

	// Epoch
	sb.WriteString(fmt.Sprintf(" Epoch [blue]%d[white] [[blue]%s%%[white]], [blue]%s[white] %-12s\n\n", promMetrics.EpochNum, epochProgress1dec, "N/A", "remaining"))

	// Blocks / Slots / Tx

	mempoolTxKBytes := promMetrics.MempoolBytes / 1024
	kWidth := strconv.Itoa(threeCol3ValueWidth -
		len(strconv.FormatUint(promMetrics.MempoolTx, 10)) -
		len(strconv.FormatUint(mempoolTxKBytes, 10)))

	tipRef := getSlotTipRef(genesisConfig)
	tipDiff := tipRef - promMetrics.SlotNum

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
	} else if tipDiff >= slotInterval(genesisConfig) {
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
		syncProgress := float32(promMetrics.SlotNum / tipRef * 1000)
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
		strings.Repeat("- ", width-13),
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
		strings.Repeat("- ", width-16),
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
		strings.Repeat("- ", width-17),
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
		" Mem (Live) : [blue]%s[white]%-"+strconv.Itoa(threeCol1ValueWidth-len(memLive))+"s",
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
		" Mem (Heap) : [blue]%s[white]%-"+strconv.Itoa(threeCol1ValueWidth-len(memHeap))+"s",
		memHeap,
		"G",
	))
	sb.WriteString(fmt.Sprintf(
		" GC Major   : [blue]%-"+strconv.Itoa(threeCol3ValueWidth)+"s[white]\n",
		strconv.FormatUint(promMetrics.GcMajor, 10),
	))

	failCount = 0
	return fmt.Sprint(sb.String())
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
		if strings.Contains(n, cfg.Node.Binary) && strings.Contains(c, strconv.FormatUint(uint64(cfg.Node.Port), 10)) {
			r = p
		}
	}
	return r, nil
}

type PromMetrics struct {
	BlockNum            uint64  `json:"cardano_node_metrics_blockNum_int"`
	EpochNum            uint64  `json:"cardano_node_metrics_epoch_int"`
	SlotInEpoch         uint64  `json:"cardano_node_metrics_slotInEpoch_int"`
	SlotNum             uint64  `json:"cardano_node_metrics_slotNum_int"`
	Density             float64 `json:"cardano_node_metrics_density_real"`
	TxProcessed         uint64  `json:"cardano_node_metrics_txsProcessedNum_int"`
	MempoolTx           uint64  `json:"cardano_node_metrics_txsInMempool_int"`
	MempoolBytes        uint64  `json:"cardano_node_metrics_mempoolBytes_int"`
	KesPeriod           uint64  `json:"cardano_node_metrics_currentKESPeriod_int"`
	RemainingKesPeriods uint64  `json:"cardano_node_metrics_remainingKESPeriods_int"`
	IsLeader            uint64  `json:"cardano_node_metrics_Forge_node_is_leader_int"`
	Adopted             uint64  `json:"cardano_node_metrics_Forge_adopted_int"`
	DidntAdopt          uint64  `json:"cardano_node_metrics_Forge_didnt_adopt_int"`
	AboutToLead         uint64  `json:"cardano_node_metrics_Forge_forge_about_to_lead_int"`
	MissedSlots         uint64  `json:"cardano_node_metrics_slotsMissedNum_int"`
	MemLive             uint64  `json:"cardano_node_metrics_RTS_gcLiveBytes_int"`
	MemHeap             uint64  `json:"cardano_node_metrics_RTS_gcHeapBytes_int"`
	GcMinor             uint64  `json:"cardano_node_metrics_RTS_gcMinorNum_int"`
	GcMajor             uint64  `json:"cardano_node_metrics_RTS_gcMajorNum_int"`
	Forks               uint64  `json:"cardano_node_metrics_forks_int"`
	BlockDelay          float64 `json:"cardano_node_metrics_blockfetchclient_blockdelay_s"`
	BlocksServed        uint64  `json:"cardano_node_metrics_served_block_count_int"`
	BlocksLate          uint64  `json:"cardano_node_metrics_blockfetchclient_lateblocks"`
	BlocksW1s           float64 `json:"cardano_node_metrics_blockfetchclient_blockdelay_cdfOne"`
	BlocksW3s           float64 `json:"cardano_node_metrics_blockfetchclient_blockdelay_cdfThree"`
	BlocksW5s           float64 `json:"cardano_node_metrics_blockfetchclient_blockdelay_cdfFive"`
	PeersCold           uint64  `json:"cardano_node_metrics_peerSelection_cold"`
	PeersWarm           uint64  `json:"cardano_node_metrics_peerSelection_warm"`
	PeersHot            uint64  `json:"cardano_node_metrics_peerSelection_hot"`
	ConnIncoming        uint64  `json:"cardano_node_metrics_connectionManager_incomingConns"`
	ConnOutgoing        uint64  `json:"cardano_node_metrics_connectionManager_outgoingConns"`
	ConnUniDir          uint64  `json:"cardano_node_metrics_connectionManager_unidirectionalConns"`
	ConnBiDir           uint64  `json:"cardano_node_metrics_connectionManager_duplexConns"`
	ConnDuplex          uint64  `json:"cardano_node_metrics_connectionManager_prunableConns"`
}

func getPromMetrics(ctx context.Context) (*PromMetrics, error) {
	var metrics *PromMetrics
	var respBodyBytes []byte
	respBodyBytes, statusCode, err := getNodeMetrics(ctx)
	if err != nil {
		failCount++
		return metrics, fmt.Errorf("Failed getNodeMetrics: %s\n", err)
	}
	if statusCode != http.StatusOK {
		failCount++
		return metrics, fmt.Errorf("Failed HTTP: %d\n", statusCode)
	}

	b, err := prom2json(respBodyBytes)
	if err != nil {
		failCount++
		return metrics, fmt.Errorf("Failed prom2json: %s\n", err)
	}

	if err := json.Unmarshal(b, &metrics); err != nil {
		failCount++
		return metrics, fmt.Errorf("Failed JSON unmarshal: %s\n", err)
	}
	failCount = 0
	return metrics, nil
}

// Converts a prometheus http response byte array into a JSON byte array
func prom2json(prom []byte) ([]byte, error) {
	// {"name": 0}
	out := make(map[string]interface{})
	b := []byte{}
	parser := &expfmt.TextParser{}
	families, err := parser.TextToMetricFamilies(strings.NewReader(string(prom)))
	if err != nil {
		return b, err
	}
	for _, val := range families {
		for _, m := range val.GetMetric() {
			switch val.GetType() {
			case dto.MetricType_COUNTER:
				out[val.GetName()] = m.GetCounter().GetValue()
			case dto.MetricType_GAUGE:
				out[val.GetName()] = m.GetGauge().GetValue()
			case dto.MetricType_UNTYPED:
				out[val.GetName()] = m.GetUntyped().GetValue()
			default:
				return b, err
			}
		}
	}
	b, err = json.MarshalIndent(out, "", "    ")
	if err != nil {
		return b, err
	}
	return b, nil
}
