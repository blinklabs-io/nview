package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"

	dto "github.com/prometheus/client_model/go"
	"github.com/gdamore/tcell/v2"
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
		if event.Rune() == 113 {
			app.Stop()
		// p or right
		} else if event.Rune() == 112 || event.Key() == tcell.KeyRight {
			pages.SwitchToPage("Prometheus")
		// c or left
		} else if event.Rune() == 99 || event.Key() == tcell.KeyLeft {
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
				SetText(fmt.Sprintf("Failed JSON: %s", err)),
			0,
			2,
			true)
	}

	// Set up our text view with our response data
	promText := tview.NewTextView().
		SetTextColor(tcell.ColorGreen).
		SetText(string(b))

	// Configure a flexible box, split into 2 rows
	flex.SetDirection(tview.FlexRow).
		AddItem(promText, 0, 9, true).
		AddItem(text, 0, 1, false)
	return flex
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
