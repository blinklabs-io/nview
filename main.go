package main

import (
	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
)

var app = tview.NewApplication()
var pages = tview.NewPages()

var mainText = tview.NewTextView().
	SetTextColor(tcell.ColorGreen).
	SetText("(f) to go to next page (q) to quit")

var twoText = tview.NewTextView().
	SetTextColor(tcell.ColorGreen).
	SetText("(b) to go to main page (q) to quit")

func main() {
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
	pages.AddPage("Main", mainText, true, true)
	pages.AddPage("Two", twoText, true, false)

	if err := app.SetRoot(pages, true).EnableMouse(false).Run(); err != nil {
		panic(err)
	}
}
