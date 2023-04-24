package main

import (
	"fmt"
	"os"
	"strconv"

	"github.com/gdamore/tcell/v2"
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
		// f or right
		} else if event.Rune() == 102 || event.Key() == tcell.KeyRight {
			pages.SwitchToPage("Two")
		// b or left
		} else if event.Rune() == 98 || event.Key() == tcell.KeyLeft {
			pages.SwitchToPage("Main")
		}
		return event
	})

	// Pages
	pages.AddPage("Main", layout("main"), true, true)
	pages.AddPage("Two", layout("two"), true, false)

	if err := app.SetRoot(pages, true).EnableMouse(false).Run(); err != nil {
		panic(err)
	}
}

func layout(p string) *tview.Flex {
	// Load our config
	cfg := GetConfig()
	text := tview.NewTextView().SetTextColor(tcell.ColorGreen)
	promText := tview.NewTextView().SetTextColor(tcell.ColorGreen).
		SetText("Prometheus: http://" + cfg.Prometheus.Host + ":" + strconv.FormatUint(uint64(cfg.Prometheus.Port), 10) + "/metrics")
	switch p {
	case "two":
		text.SetText("(b) to go to main page (q) to quit")
	default:
		text.SetText("(f) to go to next page (q) to quit")
	}
	flex := tview.NewFlex()
	// Configure a flexible box, split into 3 rows
	flex.SetDirection(tview.FlexRow).
		// Row 1 is a box
		AddItem(promText,
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
