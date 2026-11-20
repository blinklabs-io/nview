// Copyright 2026 Blink Labs Software
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
	"bytes"
	"encoding/base64"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"io"
	"os"
	"strings"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
)

const terminalStatusBannerCells = 6

var severityBannerColors = map[uiSeverity]color.RGBA{
	uiSeverityNeutral:  {R: 0x42, G: 0x85, B: 0xf4, A: 0xff},
	uiSeverityOK:       {R: 0x34, G: 0xa8, B: 0x53, A: 0xff},
	uiSeverityWarn:     {R: 0xfb, G: 0xbc, B: 0x04, A: 0xff},
	uiSeverityCritical: {R: 0xea, G: 0x43, B: 0x35, A: 0xff},
	uiSeverityMuted:    {R: 0x80, G: 0x80, B: 0x80, A: 0xff},
}

type terminalStatusBanner struct {
	protocol      string
	lastSignature string
	lastScreen    tcell.Screen
}

func newDashboardApplication(
	visuals terminalVisualCapabilities,
) *tview.Application {
	application := tview.NewApplication()
	if banner := newTerminalStatusBanner(visuals.ImageProtocol); banner != nil {
		application.SetAfterDrawFunc(banner.Draw)
	}
	return application
}

func newTerminalStatusBanner(protocol string) *terminalStatusBanner {
	if !terminalImageProtocolSupported(protocol) {
		return nil
	}
	return &terminalStatusBanner{protocol: protocol}
}

func terminalImageProtocolSupported(protocol string) bool {
	switch protocol {
	case "kitty", "iterm2", "sixel":
		return true
	default:
		return false
	}
}

func terminalImageHeaderPrefix(protocol string) string {
	if !terminalImageProtocolSupported(protocol) {
		return ""
	}
	return strings.Repeat(" ", terminalStatusBannerCells)
}

func terminalStatusSeverities() []uiSeverity {
	return []uiSeverity{
		dashboardHealthSeverity(),
		connectionPaneSeverity(),
		corePaneSeverity(),
	}
}

func (b *terminalStatusBanner) Draw(screen tcell.Screen) {
	tty, ok := screen.Tty()
	if !ok {
		return
	}
	terminfo, err := tcell.LookupTerminfo(os.Getenv("TERM"))
	if err != nil {
		return
	}

	cellWidth, cellHeight := 8, 16
	if windowSize, sizeErr := tty.WindowSize(); sizeErr == nil {
		if width, height := windowSize.CellDimensions(); width > 0 &&
			height > 0 {
			cellWidth, cellHeight = width, height
		}
	}
	severities := terminalStatusSeverities()
	cols, rows := screen.Size()
	signature := fmt.Sprintf(
		"%s:%d:%d:%d:%d:%v",
		b.protocol,
		cols,
		rows,
		cellWidth,
		cellHeight,
		severities,
	)

	screen.LockRegion(0, 0, terminalStatusBannerCells, 1, true)
	if b.lastScreen == screen && b.lastSignature == signature {
		return
	}
	payload, err := terminalStatusBannerPayload(
		b.protocol,
		cellWidth*terminalStatusBannerCells,
		cellHeight,
		severities,
	)
	if err != nil {
		screen.LockRegion(0, 0, terminalStatusBannerCells, 1, false)
		return
	}

	output := "\x1b7" + terminfo.TGoto(0, 0) + payload + "\x1b8"
	written, err := io.WriteString(tty, output)
	if err != nil || written != len(output) {
		screen.LockRegion(0, 0, terminalStatusBannerCells, 1, false)
		return
	}
	b.lastScreen = screen
	b.lastSignature = signature
}

func terminalStatusBannerPayload(
	protocol string,
	width, height int,
	severities []uiSeverity,
) (string, error) {
	img := terminalStatusBannerImage(width, height, severities)
	switch protocol {
	case "kitty":
		encoded, err := terminalStatusBannerPNG(img)
		if err != nil {
			return "", err
		}
		return fmt.Sprintf(
			"\x1b_Ga=d,d=i,i=13111,q=2;\x1b\\"+
				"\x1b_Ga=T,f=100,t=d,q=2,i=13111,c=%d,r=1;%s\x1b\\",
			terminalStatusBannerCells,
			base64.StdEncoding.EncodeToString(encoded),
		), nil
	case "iterm2":
		encoded, err := terminalStatusBannerPNG(img)
		if err != nil {
			return "", err
		}
		return fmt.Sprintf(
			"\x1b]1337;File=inline=1;width=%d;height=1;"+
				"preserveAspectRatio=0:%s\a",
			terminalStatusBannerCells,
			base64.StdEncoding.EncodeToString(encoded),
		), nil
	case "sixel":
		return encodeSixel(img), nil
	default:
		return "", fmt.Errorf(
			"unsupported terminal image protocol %q",
			protocol,
		)
	}
}

func terminalStatusBannerImage(
	width, height int,
	severities []uiSeverity,
) *image.RGBA {
	if width < terminalStatusBannerCells {
		width = terminalStatusBannerCells
	}
	if height < 1 {
		height = 1
	}
	img := image.NewRGBA(image.Rect(0, 0, width, height))
	if len(severities) == 0 {
		return img
	}

	badgeWidth := width / len(severities)
	gap := max(1, badgeWidth/5)
	verticalGap := max(1, height/5)
	for index, severity := range severities {
		badgeColor, ok := severityBannerColors[severity]
		if !ok {
			badgeColor = severityBannerColors[uiSeverityMuted]
		}
		startX := index*badgeWidth + gap
		endX := min((index+1)*badgeWidth-gap, width)
		for y := verticalGap; y < height-verticalGap; y++ {
			for x := startX; x < endX; x++ {
				img.SetRGBA(x, y, badgeColor)
			}
		}
	}
	return img
}

func terminalStatusBannerPNG(img image.Image) ([]byte, error) {
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		return nil, fmt.Errorf("encoding terminal status banner: %w", err)
	}
	return buf.Bytes(), nil
}

func encodeSixel(img image.Image) string {
	bounds := img.Bounds()
	palette, indexed := sixelPalette(img)
	var output strings.Builder
	output.WriteString("\x1bP0;1;0q")
	fmt.Fprintf(
		&output,
		"\"1;1;%d;%d",
		bounds.Dx(),
		bounds.Dy(),
	)
	for index, value := range palette {
		fmt.Fprintf(
			&output,
			"#%d;2;%d;%d;%d",
			index,
			int(value.R)*100/255,
			int(value.G)*100/255,
			int(value.B)*100/255,
		)
	}
	for bandY := 0; bandY < bounds.Dy(); bandY += 6 {
		if bandY > 0 {
			output.WriteByte('-')
		}
		for paletteIndex := range palette {
			if paletteIndex > 0 {
				output.WriteByte('$')
			}
			fmt.Fprintf(&output, "#%d", paletteIndex)
			for x := 0; x < bounds.Dx(); x++ {
				bits := byte(0)
				for bit := 0; bit < 6 && bandY+bit < bounds.Dy(); bit++ {
					if indexed[(bandY+bit)*bounds.Dx()+x] == paletteIndex {
						bits |= 1 << bit
					}
				}
				output.WriteByte(63 + bits)
			}
		}
	}
	output.WriteString("\x1b\\")
	return output.String()
}

func sixelPalette(img image.Image) ([]color.RGBA, []int) {
	bounds := img.Bounds()
	palette := make([]color.RGBA, 0, len(severityBannerColors))
	indexes := make(map[color.RGBA]int)
	indexed := make([]int, bounds.Dx()*bounds.Dy())
	for y := 0; y < bounds.Dy(); y++ {
		for x := 0; x < bounds.Dx(); x++ {
			value := color.RGBAModel.Convert(img.At(bounds.Min.X+x, bounds.Min.Y+y)).(color.RGBA)
			if value.A == 0 {
				indexed[y*bounds.Dx()+x] = -1
				continue
			}
			index, ok := indexes[value]
			if !ok {
				index = len(palette)
				indexes[value] = index
				palette = append(palette, value)
			}
			indexed[y*bounds.Dx()+x] = index
		}
	}
	return palette, indexed
}
