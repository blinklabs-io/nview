package main

import (
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"os"
	"strings"

	"github.com/gdamore/tcell/v2"
	dto "github.com/prometheus/client_model/go"
	"github.com/prometheus/common/expfmt"
	"github.com/rivo/tview"
)

var app = tview.NewApplication()
var pages = tview.NewPages()

func main() {
	// Load our config
	cfg := GetConfig()
	if err := cfg.LoadConfig(); err != nil {
		fmt.Printf("Failed to load config: %s", err)
		os.Exit(1)
	}
	// capture inputs
	app.SetInputCapture(func(event *tcell.EventKey) *tcell.EventKey {
		// q
		if event.Rune() == 113 { // q
			app.Stop()
		} else if event.Rune() == 112 || event.Key() == tcell.KeyRight { // p or right
			pages.SwitchToPage("Prometheus")
		} else if event.Rune() == 99 || event.Key() == tcell.KeyLeft { // c or left
			pages.SwitchToPage("Main")
		}
		return event
	})

	// Pages
	pages.AddPage("Main", mainLayout(), true, true)
	pages.AddPage("Prometheus", prometheusLayout(), true, false)

	if err := app.SetRoot(pages, true).EnableMouse(false).Run(); err != nil {
		panic(err)
	}
}

// Main page layout
func mainLayout() *tview.Flex {
	text := tview.NewTextView().
		SetTextColor(tcell.ColorGreen).
		SetText("(p) to open prometheus (q) to quit")
	flex := tview.NewFlex()
	// Configure a flexible box, split into 3 rows
	flex.SetDirection(tview.FlexRow).
		// Row 1 is a box
		AddItem(tview.NewBox().SetBorder(true),
			0,
			2,
			false).
		// Row 2 is a flex set of rows
		AddItem(tview.NewFlex().SetDirection(tview.FlexRow).
			// Row 1 is a flex set of columns
			AddItem(tview.NewFlex().
				// Column 1 - r2r1c1
				AddItem(tview.NewBox().SetBorder(true),
					0,
					1,
					true).
				// Column 2 - r2r1c2
				AddItem(tview.NewBox().SetBorder(true),
					0,
					4,
					false),
				0,
				6,
				false).
			// Row 2 - r2r2
			AddItem(tview.NewBox().SetBorder(true),
				0,
				1,
				false),
			0,
			6,
			false).
		// Row 3
		AddItem(text, 0, 1, false)
	return flex
}

func prometheusLayout() *tview.Flex {
	text := tview.NewTextView().
		SetTextColor(tcell.ColorGreen).
		SetText("(c) to close this page (q) to quit")
	flex := tview.NewFlex()

	var respBodyBytes []byte
	respBodyBytes, statusCode, err := getNodeMetrics()
	if err != nil {
		return flex.AddItem(
			tview.NewTextView().
				SetTextColor(tcell.ColorGreen).
				SetText(fmt.Sprintf("Failed getNodeMetrics: %s", err)),
			0,
			2,
			false)
	}
	if statusCode != http.StatusOK {
		return flex.AddItem(
			tview.NewTextView().
				SetTextColor(tcell.ColorGreen).
				SetText(fmt.Sprintf("Failed HTTP: %d", statusCode)),
			0,
			1,
			false)
	}

	b, err := prom2json(respBodyBytes)
	if err != nil {
		return flex.AddItem(
			tview.NewTextView().
				SetTextColor(tcell.ColorGreen).
				SetText(fmt.Sprintf("Failed prom2json: %s", err)),
			0,
			2,
			true)
	}

	var metrics *Metrics
	if err := json.Unmarshal(b, &metrics); err != nil {
		return flex.AddItem(
			tview.NewTextView().
				SetTextColor(tcell.ColorGreen).
				SetText(fmt.Sprintf("Failed JSON unmarshal: %s", err)),
			0,
			2,
			true)
	}

	// TODO: fetch uptime from node
	var uptimes uint64 = 0

	// Set up our text view with our response data
	promText := tview.NewTextView().
		SetTextColor(tcell.ColorGreen).
		SetText(fmt.Sprintf("Uptime: %s\nEpoch: %d", timeLeft(uptimes), metrics.EpochNum))

	// Configure a flexible box, split into 2 rows
	flex.SetDirection(tview.FlexRow).
		AddItem(promText, 0, 9, true).
		AddItem(text, 0, 1, false)
	return flex
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
	BlockServed         uint64  `json:"cardano_node_metrics_served_block_count_int"`
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

func getNodeMetrics() ([]byte, int, error) {
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
