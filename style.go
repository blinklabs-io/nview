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
	"fmt"
	"math"
	"os"
	"strings"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
)

const (
	uiColorLabel    = "green"
	uiColorValue    = "white"
	uiColorUnit     = "blue"
	uiColorMuted    = "gray"
	uiColorAccent   = "fuchsia"
	uiColorOK       = "green"
	uiColorWarn     = "yellow"
	uiColorCritical = "red"
)

type uiSeverity int

const (
	uiSeverityNeutral uiSeverity = iota
	uiSeverityOK
	uiSeverityWarn
	uiSeverityCritical
	uiSeverityMuted
)

type terminalVisualMode int

const (
	terminalVisualPlain terminalVisualMode = iota
	terminalVisualUnicode
	terminalVisualNerd
)

type terminalVisualCapabilities struct {
	Mode          terminalVisualMode
	ImageProtocol string
}

type uiGlyphSet struct {
	Section  string
	OK       string
	Warn     string
	Critical string
	Info     string
	BarFull  string
	BarEmpty string
}

type uiSegment struct {
	Label    string
	Value    float64
	Severity uiSeverity
}

func currentTerminalVisuals() terminalVisualCapabilities {
	return terminalVisualsFromEnv(os.Getenv)
}

func terminalVisualsFromEnv(getenv func(string) string) terminalVisualCapabilities {
	if getenv == nil {
		getenv = func(string) string { return "" }
	}

	mode := terminalVisualUnicode
	switch strings.ToLower(strings.TrimSpace(getenv("NVIEW_VISUAL_MODE"))) {
	case "plain", "ascii", "basic":
		mode = terminalVisualPlain
	case "nerd", "nerdfont", "powerline":
		mode = terminalVisualNerd
	case "unicode", "utf8":
		mode = terminalVisualUnicode
	case "", "auto":
		if !terminalSupportsUnicode(getenv) {
			mode = terminalVisualPlain
		}
	default:
		if !terminalSupportsUnicode(getenv) {
			mode = terminalVisualPlain
		}
	}

	return terminalVisualCapabilities{
		Mode:          mode,
		ImageProtocol: terminalImageProtocolFromEnv(getenv),
	}
}

func terminalSupportsUnicode(getenv func(string) string) bool {
	if strings.EqualFold(strings.TrimSpace(getenv("TERM")), "dumb") {
		return false
	}
	locale := getenv("LC_ALL")
	if locale == "" {
		locale = getenv("LC_CTYPE")
	}
	if locale == "" {
		locale = getenv("LANG")
	}
	if locale == "" {
		return true
	}
	locale = strings.ToUpper(locale)
	return strings.Contains(locale, "UTF-8") || strings.Contains(locale, "UTF8")
}

func terminalImageProtocolFromEnv(getenv func(string) string) string {
	switch strings.ToLower(strings.TrimSpace(getenv("NVIEW_IMAGE_PROTOCOL"))) {
	case "none", "off", "plain":
		return "none"
	case "kitty", "iterm2", "sixel":
		return strings.ToLower(strings.TrimSpace(getenv("NVIEW_IMAGE_PROTOCOL")))
	case "", "auto":
	default:
		return "none"
	}

	term := strings.ToLower(getenv("TERM"))
	switch {
	case getenv("KITTY_WINDOW_ID") != "" || strings.Contains(term, "kitty"):
		return "kitty"
	case strings.EqualFold(getenv("TERM_PROGRAM"), "iTerm.app"):
		return "iterm2"
	case strings.Contains(term, "sixel") ||
		strings.Contains(strings.ToLower(getenv("TERM_FEATURES")), "sixel"):
		return "sixel"
	default:
		return "none"
	}
}

func uiGlyphs() uiGlyphSet {
	return uiGlyphsForVisuals(currentTerminalVisuals())
}

func uiGlyphsForVisuals(capabilities terminalVisualCapabilities) uiGlyphSet {
	switch capabilities.Mode {
	case terminalVisualPlain:
		return uiGlyphSet{
			Section:  "*",
			OK:       "OK",
			Warn:     "!!",
			Critical: "!!",
			Info:     ">",
			BarFull:  "#",
			BarEmpty: "-",
		}
	case terminalVisualNerd:
		return uiGlyphSet{
			Section:  "",
			OK:       "",
			Warn:     "",
			Critical: "",
			Info:     "",
			BarFull:  "█",
			BarEmpty: "░",
		}
	case terminalVisualUnicode:
		return uiGlyphSet{
			Section:  "◆",
			OK:       "●",
			Warn:     "▲",
			Critical: "■",
			Info:     "•",
			BarFull:  "█",
			BarEmpty: "░",
		}
	default:
		return uiGlyphSet{
			Section:  "◆",
			OK:       "●",
			Warn:     "▲",
			Critical: "■",
			Info:     "•",
			BarFull:  "█",
			BarEmpty: "░",
		}
	}
}

func uiColorForSeverity(severity uiSeverity) string {
	switch severity {
	case uiSeverityNeutral:
		return uiColorValue
	case uiSeverityOK:
		return uiColorOK
	case uiSeverityWarn:
		return uiColorWarn
	case uiSeverityCritical:
		return uiColorCritical
	case uiSeverityMuted:
		return uiColorMuted
	default:
		return uiColorValue
	}
}

func uiWrap(color, value string) string {
	return "[" + color + "]" + tview.Escape(value) + "[" + uiColorValue + "]"
}

func uiLabel(value string) string {
	return uiWrap(uiColorLabel, value)
}

func uiLabelSeverity(value string, severity uiSeverity) string {
	if severity == uiSeverityMuted {
		return uiMuted(value)
	}
	return uiLabel(value)
}

func uiValue(value string) string {
	return uiWrap(uiColorValue, value)
}

func uiUnit(value string) string {
	return uiWrap(uiColorUnit, value)
}

func uiMuted(value string) string {
	return uiWrap(uiColorMuted, value)
}

func uiSeverityValue(value string, severity uiSeverity) string {
	return uiWrap(uiColorForSeverity(severity), value)
}

func uiSection(title string) string {
	return uiSectionSeverity(title, uiSeverityNeutral)
}

func uiSectionSeverity(title string, severity uiSeverity) string {
	glyphs := uiGlyphs()
	color := uiColorAccent
	if severity == uiSeverityMuted {
		color = uiColorMuted
	}
	return fmt.Sprintf(" [%s]%s %s[%s]\n", color, glyphs.Section, tview.Escape(title), uiColorValue)
}

func uiInlineSection(title string) string {
	glyphs := uiGlyphs()
	return fmt.Sprintf("[%s]%s %s[%s]", uiColorAccent, glyphs.Section, tview.Escape(title), uiColorValue)
}

func uiStatusGlyph(severity uiSeverity) string {
	glyphs := uiGlyphs()
	switch severity {
	case uiSeverityNeutral:
		return uiSeverityValue(glyphs.Info, uiSeverityMuted)
	case uiSeverityOK:
		return uiSeverityValue(glyphs.OK, severity)
	case uiSeverityWarn:
		return uiSeverityValue(glyphs.Warn, severity)
	case uiSeverityCritical:
		return uiSeverityValue(glyphs.Critical, severity)
	case uiSeverityMuted:
		return uiSeverityValue(glyphs.Info, severity)
	default:
		return uiSeverityValue(glyphs.Info, uiSeverityMuted)
	}
}

func uiProgressBar(percent float64, width int, severity uiSeverity) string {
	if width <= 0 {
		return ""
	}
	if math.IsNaN(percent) || math.IsInf(percent, 0) {
		percent = 0
	}
	if percent < 0 {
		percent = 0
	}
	if percent > 100 {
		percent = 100
	}

	filled := int(math.Round(percent * float64(width) / 100))
	if filled < 0 {
		filled = 0
	}
	if filled > width {
		filled = width
	}

	glyphs := uiGlyphs()
	filledPart := strings.Repeat(glyphs.BarFull, filled)
	emptyPart := strings.Repeat(glyphs.BarEmpty, width-filled)
	if filledPart == "" {
		return uiMuted(emptyPart)
	}
	return fmt.Sprintf(
		"[%s]%s[%s]%s[%s]",
		uiColorForSeverity(severity),
		filledPart,
		uiColorMuted,
		emptyPart,
		uiColorValue,
	)
}

func uiKV(label, value string) string {
	return fmt.Sprintf("%s %s", uiLabel(label), value)
}

func uiKVStyledAligned(labelWidth int, label, value string, labelSeverity uiSeverity) string {
	if labelWidth < len(label) {
		labelWidth = len(label)
	}
	return fmt.Sprintf(
		"%s %s",
		uiLabelSeverity(fmt.Sprintf("%-*s", labelWidth, label), labelSeverity),
		value,
	)
}

func uiKVAligned(labelWidth int, label, value string) string {
	return uiKVStyledAligned(labelWidth, label, value, uiSeverityNeutral)
}

func uiKVSeverityAligned(labelWidth int, label, value string, severity uiSeverity) string {
	return uiKVStyledAligned(
		labelWidth,
		label,
		uiSeverityValue(value, severity),
		severity,
	)
}

func uiPill(label, value string, severity uiSeverity) string {
	return fmt.Sprintf(
		"[%s] %s [%s]%s[%s]",
		uiColorMuted,
		tview.Escape(label),
		uiColorForSeverity(severity),
		tview.Escape(value),
		uiColorValue,
	)
}

func uiKey(key, label string) string {
	return fmt.Sprintf(
		"[%s](%s)[%s] %s",
		uiColorWarn,
		tview.Escape(key),
		uiColorValue,
		tview.Escape(label),
	)
}

func uiPercentBar(label string, percent float64, severity uiSeverity, width int) string {
	return fmt.Sprintf(
		"%s %s %s",
		uiLabel(label),
		uiSeverityValue(fmt.Sprintf("%5.1f%%", percent), severity),
		uiProgressBar(percent, width, severity),
	)
}

func uiSegmentBar(segments []uiSegment, width int) string {
	if width <= 0 {
		return ""
	}
	total := 0.0
	for _, segment := range segments {
		if segment.Value > 0 {
			total += segment.Value
		}
	}
	if total <= 0 {
		return uiMuted(strings.Repeat(uiGlyphs().BarEmpty, width))
	}

	glyphs := uiGlyphs()
	var sb strings.Builder
	used := 0
	lastPositiveIdx := -1
	for idx, segment := range segments {
		if segment.Value > 0 {
			lastPositiveIdx = idx
		}
	}
	for idx, segment := range segments {
		if segment.Value <= 0 {
			continue
		}
		count := int(math.Round(segment.Value / total * float64(width)))
		if idx == lastPositiveIdx {
			count = width - used
		}
		if count <= 0 && used < width {
			count = 1
		}
		if used+count > width {
			count = width - used
		}
		if count <= 0 {
			continue
		}
		sb.WriteString(uiWrap(
			uiColorForSeverity(segment.Severity),
			strings.Repeat(glyphs.BarFull, count),
		))
		used += count
	}
	if used < width {
		sb.WriteString(uiMuted(strings.Repeat(glyphs.BarEmpty, width-used)))
	}
	return sb.String()
}

func uiSparkline(values []float64, maxValue float64, severity uiSeverity) string {
	if len(values) == 0 {
		return ""
	}
	if maxValue <= 0 {
		for _, value := range values {
			if value > maxValue {
				maxValue = value
			}
		}
	}
	if maxValue <= 0 {
		return uiMuted(strings.Repeat(".", len(values)))
	}

	levels := []string{"▁", "▂", "▃", "▄", "▅", "▆", "▇", "█"}
	if currentTerminalVisuals().Mode == terminalVisualPlain {
		levels = []string{".", ":", "-", "=", "+", "*", "#", "@"}
	}
	var sb strings.Builder
	for _, value := range values {
		if math.IsNaN(value) || math.IsInf(value, 0) || value < 0 {
			value = 0
		}
		if value > maxValue {
			value = maxValue
		}
		idx := int(math.Round(value / maxValue * float64(len(levels)-1)))
		if idx < 0 {
			idx = 0
		}
		if idx >= len(levels) {
			idx = len(levels) - 1
		}
		sb.WriteString(levels[idx])
	}
	return uiSeverityValue(sb.String(), severity)
}

func uiPanelTitle(title string, severity uiSeverity) string {
	return fmt.Sprintf(
		" %s %s ",
		uiStatusGlyph(severity),
		uiSeverityValue(title, severity),
	)
}

func uiTCellColor(severity uiSeverity) tcell.Color {
	switch severity {
	case uiSeverityNeutral:
		return tcell.ColorDarkCyan
	case uiSeverityOK:
		return tcell.ColorGreen
	case uiSeverityWarn:
		return tcell.ColorYellow
	case uiSeverityCritical:
		return tcell.ColorRed
	case uiSeverityMuted:
		return tcell.ColorGray
	default:
		return tcell.ColorDarkCyan
	}
}
