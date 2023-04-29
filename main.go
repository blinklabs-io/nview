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

var flex = tview.NewFlex()
var text = tview.NewTextView().
	SetTextColor(tcell.ColorGreen).
	SetText("(r) to refresh prometheus (q) to quit")
var promText = tview.NewTextView()

func main() {
	// Load our config
	cfg := GetConfig()
	if err := cfg.LoadConfig(); err != nil {
		fmt.Printf("Failed to load config: %s", err)
		os.Exit(1)
	}

	// Fetch data from Prometheus
	promText.SetText(getPromText()).SetBorder(true)
	flex.SetDirection(tview.FlexRow).
		// Row 1 is a box
		AddItem(tview.NewBox(),
			0,
			1,
			false).
		// Row 2 is a box with a border
		AddItem(tview.NewBox().SetBorder(true),
			0,
			1,
			false).
		// Row 3 is a flex set of rows
		AddItem(tview.NewFlex().SetDirection(tview.FlexRow).
			// Row 1 - r3r1
			AddItem(promText,
				0,
				6,
				false).
			// Row 2 - r3r2
			AddItem(tview.NewBox().SetBorder(true),
				0,
				1,
				false),
			0,
			7,
			false).
		// Row 4 is our footer
		AddItem(text, 0, 1, false)

	// capture inputs
	flex.SetInputCapture(func(event *tcell.EventKey) *tcell.EventKey {
		if event.Rune() == 113 { // q
			app.Stop()
		} else if event.Rune() == 114 { // r
			promText.Clear()
			promText.SetText(getPromText())
		}
		return event
	})

	// Pages
	pages.AddPage("Main", flex, true, true)

	if err := app.SetRoot(pages, true).EnableMouse(false).Run(); err != nil {
		panic(err)
	}
}

func getPromText() string {
	var respBodyBytes []byte
	respBodyBytes, statusCode, err := getNodeMetrics()
	if err != nil {
		return fmt.Sprintf("Failed getNodeMetrics: %s", err)
	}
	if statusCode != http.StatusOK {
		return fmt.Sprintf("Failed HTTP: %d", statusCode)
	}

	b, err := prom2json(respBodyBytes)
	if err != nil {
		return fmt.Sprintf("Failed prom2json: %s", err)
	}

	var metrics *Metrics
	if err := json.Unmarshal(b, &metrics); err != nil {
		return fmt.Sprintf("Failed JSON unmarshal: %s", err)
	}

	// TODO: fetch uptime from node
	var uptimes uint64 = 0

	return fmt.Sprintf("Uptime: %s\nEpoch: %d\nBlock: %d\nSlot: %d\nSlot epoch: %d",
		timeLeft(uptimes),
		metrics.EpochNum,
		metrics.BlockNum,
		metrics.SlotNum,
		metrics.SlotInEpoch,
	)
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
