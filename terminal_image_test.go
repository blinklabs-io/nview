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
	"image/png"
	"strings"
	"testing"

	"github.com/gdamore/tcell/v2"
)

func TestDashboardApplicationConfiguresImageRenderer(t *testing.T) {
	for _, protocol := range []string{"kitty", "iterm2", "sixel"} {
		t.Run(protocol, func(t *testing.T) {
			application := newDashboardApplication(
				terminalVisualCapabilities{ImageProtocol: protocol},
			)
			if application.GetAfterDrawFunc() == nil {
				t.Fatalf("%s renderer is not installed", protocol)
			}
		})
	}

	application := newDashboardApplication(
		terminalVisualCapabilities{ImageProtocol: "none"},
	)
	if application.GetAfterDrawFunc() != nil {
		t.Fatal("plain fallback unexpectedly installs an image renderer")
	}
}

func TestTerminalStatusBannerPayloads(t *testing.T) {
	severities := []uiSeverity{
		uiSeverityOK,
		uiSeverityWarn,
		uiSeverityCritical,
	}
	for _, protocol := range []string{"kitty", "iterm2", "sixel"} {
		t.Run(protocol, func(t *testing.T) {
			payload, err := terminalStatusBannerPayload(
				protocol,
				48,
				16,
				severities,
			)
			if err != nil {
				t.Fatalf("terminalStatusBannerPayload() error = %v", err)
			}
			if payload == "" {
				t.Fatal(
					"terminalStatusBannerPayload() returned an empty payload",
				)
			}
			switch protocol {
			case "kitty":
				if !strings.Contains(payload, "\x1b_Ga=T,f=100") {
					t.Fatalf(
						"kitty payload missing transmit command: %q",
						payload,
					)
				}
				encoded := payload[strings.LastIndex(payload, ";")+1:]
				encoded = strings.TrimSuffix(encoded, "\x1b\\")
				assertTerminalBannerPNG(t, encoded)
			case "iterm2":
				if !strings.HasPrefix(payload, "\x1b]1337;File=inline=1;") {
					t.Fatalf("iTerm2 payload has the wrong prefix: %q", payload)
				}
				encoded := payload[strings.LastIndex(payload, ":")+1:]
				encoded = strings.TrimSuffix(encoded, "\a")
				assertTerminalBannerPNG(t, encoded)
			case "sixel":
				if !strings.HasPrefix(payload, "\x1bP0;1;0q") ||
					!strings.HasSuffix(payload, "\x1b\\") ||
					!strings.Contains(payload, "\"1;1;48;16") {
					t.Fatalf("invalid sixel payload: %q", payload)
				}
			}
		})
	}
}

func assertTerminalBannerPNG(t *testing.T, encoded string) {
	t.Helper()
	data, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		t.Fatalf("decoding terminal banner: %v", err)
	}
	img, err := png.Decode(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("decoding terminal banner PNG: %v", err)
	}
	if got := img.Bounds().Dx(); got != 48 {
		t.Fatalf("banner width = %d, expected 48", got)
	}
	if got := img.Bounds().Dy(); got != 16 {
		t.Fatalf("banner height = %d, expected 16", got)
	}
}

func TestTerminalImageHeaderPrefix(t *testing.T) {
	if got := terminalImageHeaderPrefix("none"); got != "" {
		t.Fatalf("plain fallback prefix = %q, expected empty", got)
	}
	for _, protocol := range []string{"kitty", "iterm2", "sixel"} {
		if got := terminalImageHeaderPrefix(protocol); len(
			got,
		) != terminalStatusBannerCells {
			t.Fatalf(
				"%s prefix width = %d, expected %d",
				protocol,
				len(got),
				terminalStatusBannerCells,
			)
		}
	}
}

func TestTerminalStatusBannerCellSize(t *testing.T) {
	windowSize := tcell.WindowSize{
		Width:       80,
		Height:      24,
		PixelWidth:  800,
		PixelHeight: 480,
	}
	width, height, ok := terminalStatusBannerCellSize(
		"sixel",
		windowSize,
		nil,
	)
	if !ok || width != 10 || height != 20 {
		t.Fatalf("known cell size = %dx%d, %t", width, height, ok)
	}

	windowSize.PixelWidth = 0
	windowSize.PixelHeight = 0
	if _, _, ok := terminalStatusBannerCellSize(
		"sixel",
		windowSize,
		nil,
	); ok {
		t.Fatal("Sixel banner accepted unknown cell dimensions")
	}

	width, height, ok = terminalStatusBannerCellSize(
		"kitty",
		windowSize,
		nil,
	)
	if !ok || width != 8 || height != 16 {
		t.Fatalf("Kitty fallback size = %dx%d, %t", width, height, ok)
	}
}
