package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/gdamore/tcell/v2"
	dto "github.com/prometheus/client_model/go"
	"github.com/prometheus/common/expfmt"
	"github.com/rivo/tview"
)

var app = tview.NewApplication()
var pages = tview.NewPages()

var flex = tview.NewFlex()
var text = tview.NewTextView().
	SetDynamicColors(true).
	SetChangedFunc(func() {
		app.Draw()
	})
var headerText = tview.NewTextView()
var footerText = tview.NewTextView().
	SetDynamicColors(true).
	SetTextColor(tcell.ColorGreen).
	SetText(" [yellow](esc/q) Quit[white] | [yellow](r) Refresh Prometheus")

func main() {
	// Create a background context
	ctx := context.Background()
	// Load our config
	cfg := GetConfig()
	if err := cfg.LoadConfig(); err != nil {
		fmt.Printf("Failed to load config: %s", err)
		os.Exit(1)
	}

	// Exit if NODE_NAME is > 19 characters
	if len([]rune(cfg.App.NodeName)) > 19 {
		fmt.Println("Please keep node name at or below 19 characters in length!")
		os.Exit(1)
	}

	// Fetch data from Prometheus
	text.SetText(getPromText(ctx)).SetBorder(true)
	// Set our header
	headerText.SetText(fmt.Sprintf("> [green]%s[white] <", cfg.App.NodeName)).
		SetDynamicColors(true).
		SetTextAlign(tview.AlignCenter)
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
			false).
		// Row 3 is our footer
		AddItem(footerText, 0, 1, false)

	// capture inputs
	flex.SetInputCapture(func(event *tcell.EventKey) *tcell.EventKey {
		if event.Rune() == 113 || event.Key() == tcell.KeyEscape { // q
			app.Stop()
		}
		if event.Rune() == 114 { // r
			text.Clear()
			text.SetText(getPromText(ctx))
		}
		return event
	})

	// Pages
	pages.AddPage("Main", flex, true, true)

	// Start our background refresh timer
	go func() {
		for {
			text.Clear()
			text.SetText(getPromText(ctx))
			time.Sleep(time.Second * 5)
		}
	}()

	if err := app.SetRoot(pages, true).EnableMouse(false).Run(); err != nil {
		panic(err)
	}
}

func getPromMetrics(ctx context.Context) *Metrics {
	var respBodyBytes []byte
	respBodyBytes, statusCode, err := getNodeMetrics(ctx)
	if err != nil {
		fmt.Printf("Failed getNodeMetrics: %s\n", err)
		os.Exit(1)
	}
	if statusCode != http.StatusOK {
		fmt.Printf("Failed HTTP: %d\n", statusCode)
		os.Exit(1)
	}

	b, err := prom2json(respBodyBytes)
	if err != nil {
		fmt.Printf("Failed prom2json: %s\n", err)
		os.Exit(1)
	}

	var metrics *Metrics
	if err := json.Unmarshal(b, &metrics); err != nil {
		fmt.Printf("Failed JSON unmarshal: %s\n", err)
		os.Exit(1)
	}
	return metrics
}

func getPromText(ctx context.Context) string {
	metrics := getPromMetrics(ctx)
	// TODO: fetch uptime from node
	var uptimes uint64 = 0

	//var nc = tview.TranslateANSI("\e[0m")
	//var standout = tview.TranslateANSI("\e[7m")
	//var bold = tview.TranslateANSI("\e[1m")

	// Style / UI
	var width = 71
	var threeColWidth = (width - 3) / 2
	//var threeCol2Start = threeColWidth+3
	//var threeCol3Start = threeColWidth*2+4
	var threeCol1ValueWidth = threeColWidth - 12
	var threeCol2ValueWidth = threeColWidth - 12
	var threeCol3ValueWidth = threeColWidth - 12

	var sb strings.Builder

	// Main section
	sb.WriteString(fmt.Sprintf(" Uptime: [blue]%s[white]\n", timeLeft(uptimes)))
	sb.WriteString(fmt.Sprintf("%s\n", strings.Repeat("- ", 10)))

	// Epoch
	sb.WriteString(fmt.Sprintf(" Epoch [blue]%d[white]\n", metrics.EpochNum))
	sb.WriteString(fmt.Sprintf("\n"))

	// Blocks / Slots / Tx

	// Row 1
	sb.WriteString(fmt.Sprintf(" Block      : [blue]%-"+strconv.Itoa(threeCol1ValueWidth)+"s[white]", strconv.FormatUint(metrics.BlockNum, 10)))
	sb.WriteString(fmt.Sprintf(" Tip (ref)  : [blue]%-"+strconv.Itoa(threeCol2ValueWidth)+"s[white]", "N/A"))
	sb.WriteString(fmt.Sprintf(" Forks      : [blue]%-"+strconv.Itoa(threeCol3ValueWidth)+"s[white]\n", strconv.FormatUint(metrics.Forks, 10)))
	// Row 2
	sb.WriteString(fmt.Sprintf(" Slot       : [blue]%-"+strconv.Itoa(threeCol1ValueWidth)+"s[white]", strconv.FormatUint(metrics.SlotNum, 10)))
	sb.WriteString(fmt.Sprintf(" Tip (diff) : [blue]%-"+strconv.Itoa(threeCol2ValueWidth)+"s[white]", "N/A"))
	sb.WriteString(fmt.Sprintf(" Total Tx   : [blue]%-"+strconv.Itoa(threeCol3ValueWidth)+"s[white]\n", strconv.FormatUint(metrics.TxProcessed, 10)))
	// Row 3
	sb.WriteString(fmt.Sprintf(" Slot epoch : [blue]%-"+strconv.Itoa(threeCol1ValueWidth)+"s[white]", strconv.FormatUint(metrics.SlotInEpoch, 10)))
	sb.WriteString(fmt.Sprintf(" Density    : [blue]%-"+strconv.Itoa(threeCol2ValueWidth)+"s[white]", fmt.Sprintf("%3.5f", metrics.Density*100/1)))
	mempoolTxKBytes := metrics.MempoolBytes / 1024
	kWidth := strconv.Itoa(threeCol1ValueWidth -
		len(strconv.FormatUint(metrics.MempoolTx, 10)) -
		len(strconv.FormatUint(mempoolTxKBytes, 10)))
	sb.WriteString(fmt.Sprintf(" Pending Tx : [blue]%d[white]/[blue]%d[white]%-"+kWidth+"s\n",
		metrics.MempoolTx,
		mempoolTxKBytes,
		"K",
	))

	// CONNECTIONS Divider
	sb.WriteString(fmt.Sprintf("- [yellow]CONNECTIONS[white] %s\n", strings.Repeat("- ", width-13)))

	// TODO: actually check for p2p
	p2p := true
	if p2p {
		// Row 1
		sb.WriteString(fmt.Sprintf(" P2P        : [green]%-"+strconv.Itoa(threeCol1ValueWidth)+"s[white]", "enabled"))
		sb.WriteString(fmt.Sprintf(" Cold Peers : [blue]%-"+strconv.Itoa(threeCol2ValueWidth)+"s[white]", strconv.FormatUint(metrics.PeersCold, 10)))
		sb.WriteString(fmt.Sprintf(" Uni-Dir    : [blue]%-"+strconv.Itoa(threeCol3ValueWidth)+"s[white]\n", strconv.FormatUint(metrics.ConnUniDir, 10)))
		// Row 2
		sb.WriteString(fmt.Sprintf(" Incoming   : [blue]%-"+strconv.Itoa(threeCol1ValueWidth)+"s[white]", strconv.FormatUint(metrics.ConnIncoming, 10)))
		sb.WriteString(fmt.Sprintf(" Warm Peers : [blue]%-"+strconv.Itoa(threeCol2ValueWidth)+"s[white]", strconv.FormatUint(metrics.PeersWarm, 10)))
		sb.WriteString(fmt.Sprintf(" Bi-Dir     : [blue]%-"+strconv.Itoa(threeCol3ValueWidth)+"s[white]\n", strconv.FormatUint(metrics.ConnBiDir, 10)))
		// Row 3
		sb.WriteString(fmt.Sprintf(" Outgoing   : [blue]%-"+strconv.Itoa(threeCol1ValueWidth)+"s[white]", strconv.FormatUint(metrics.ConnOutgoing, 10)))
		sb.WriteString(fmt.Sprintf(" Hot Peers  : [blue]%-"+strconv.Itoa(threeCol2ValueWidth)+"s[white]", strconv.FormatUint(metrics.PeersHot, 10)))
		sb.WriteString(fmt.Sprintf(" Duplex     : [blue]%-"+strconv.Itoa(threeCol3ValueWidth)+"s[white]\n", strconv.FormatUint(metrics.ConnDuplex, 10)))
	} else {
		sb.WriteString(fmt.Sprintf(" P2P        : [yellow]%-"+strconv.Itoa(threeCol1ValueWidth)+"s[white]\n", "disabled"))
	}

	// BLOCK PROPAGATION Divider
	sb.WriteString(fmt.Sprintf("- [yellow]BLOCK PROPAGATION[white] %s\n", strings.Repeat("- ", width-16)))

	// Row 1
	sb.WriteString(fmt.Sprintf(" Last Delay : [blue]%s[white]%-18s", fmt.Sprintf("%.2f", metrics.BlockDelay), "s"))
	sb.WriteString(fmt.Sprintf(" Served     : [blue]%-22s[white]", strconv.FormatUint(metrics.BlocksServed, 10)))
	sb.WriteString(fmt.Sprintf(" Late (>5s) : [blue]%-22s[white]\n", strconv.FormatUint(metrics.BlocksLate, 10)))
	// Row 2
	blk1s := fmt.Sprintf("%.2f", metrics.BlocksW1s*100)
	sb.WriteString(fmt.Sprintf(" Within 1s  : [blue]%s[white]%-"+strconv.Itoa(threeCol1ValueWidth - len(blk1s))+"s", blk1s, "%"))
	blk3s := fmt.Sprintf("%.2f", metrics.BlocksW3s*100)
	sb.WriteString(fmt.Sprintf(" Within 3s  : [blue]%s[white]%-"+strconv.Itoa(threeCol2ValueWidth - len(blk3s))+"s", blk3s, "%"))
	blk5s := fmt.Sprintf("%.2f", metrics.BlocksW5s*100)
	sb.WriteString(fmt.Sprintf(" Within 5s  : [blue]%s[white]%-"+strconv.Itoa(threeCol3ValueWidth - len(blk5s))+"s\n", blk5s, "%"))

	// NODE RESOURCE USAGE Divider
	sb.WriteString(fmt.Sprintf("- [yellow]NODE RESOURCE USAGE[white] %s\n", strings.Repeat("- ", width-17)))

	// Row 1
	sb.WriteString(fmt.Sprintf(" CPU (sys)  : [blue]%s[white]%-18s", fmt.Sprintf("%.1f", 99.0), "%"))
	memLive := fmt.Sprintf("%.1f", float64(metrics.MemLive)/float64(1073741824))
	sb.WriteString(fmt.Sprintf(" Mem (Live) : [blue]%s[white]%-"+strconv.Itoa(threeCol1ValueWidth - len(memLive))+"s", memLive, "G"))
	sb.WriteString(fmt.Sprintf(" GC Minor   : [blue]%-"+strconv.Itoa(threeCol3ValueWidth)+"s[white]\n", strconv.FormatUint(metrics.GcMinor, 10)))
	// Row 2
	sb.WriteString(fmt.Sprintf(" Mem (RSS)  : [blue]%s[white]%-19s", fmt.Sprintf("%.1f", 1.2), "G"))
	memHeap := fmt.Sprintf("%.1f", float64(metrics.MemHeap)/float64(1073741824))
	sb.WriteString(fmt.Sprintf(" Mem (Heap) : [blue]%s[white]%-"+strconv.Itoa(threeCol1ValueWidth - len(memHeap))+"s", memHeap, "G"))
	sb.WriteString(fmt.Sprintf(" GC Major   : [blue]%-"+strconv.Itoa(threeCol3ValueWidth)+"s[white]\n", strconv.FormatUint(metrics.GcMajor, 10)))

	return fmt.Sprint(sb.String())
}

type Metrics struct {
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

func getNodeMetrics(ctx context.Context) ([]byte, int, error) {
	// Load our config and get host/port
	cfg := GetConfig()
	url := fmt.Sprintf(
		"http://%s:%d/metrics",
		cfg.Prometheus.Host,
		cfg.Prometheus.Port,
	)
	var respBodyBytes []byte
	// Setup request
	req, err := http.NewRequest(
		http.MethodGet,
		url,
		nil,
	)
	if err != nil {
		return respBodyBytes, http.StatusInternalServerError, err
	}
	// Set a 3 second timeout
	ctx, cancel := context.WithTimeout(ctx, time.Duration(time.Second*3))
	defer cancel()
	req.WithContext(ctx)
	// Get metrics from the node
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return respBodyBytes, http.StatusInternalServerError, err
	}
	// Read the entire response body and close it to prevent a memory leak
	respBodyBytes, err = io.ReadAll(resp.Body)
	if err != nil {
		return respBodyBytes, http.StatusInternalServerError, err
	}
	defer resp.Body.Close()
	return respBodyBytes, resp.StatusCode, nil
}

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

func timeLeft(t uint64) string {
	d := t / 60 / 60 / 24
	h := math.Mod(float64(t/60/60), 24)
	m := math.Mod(float64(t/60), 60)
	s := math.Mod(float64(t), 60)
	var result string
	if d > 0 {
		result = fmt.Sprintf("%dd ", d)
	}
	return fmt.Sprintf("%s%02d:%02d:%02d", result, int(h), int(m), int(s))
}
