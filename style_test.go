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
	"testing"

	"github.com/rivo/tview"
)

func TestTerminalVisualsFromEnv(t *testing.T) {
	tests := []struct {
		name          string
		env           map[string]string
		expectedMode  terminalVisualMode
		expectedImage string
	}{
		{
			name: "explicit plain",
			env: map[string]string{
				"NVIEW_VISUAL_MODE": "plain",
			},
			expectedMode:  terminalVisualPlain,
			expectedImage: "none",
		},
		{
			name: "explicit nerd font",
			env: map[string]string{
				"NVIEW_VISUAL_MODE": "nerd",
			},
			expectedMode:  terminalVisualNerd,
			expectedImage: "none",
		},
		{
			name: "auto dumb terminal",
			env: map[string]string{
				"TERM": "dumb",
			},
			expectedMode:  terminalVisualPlain,
			expectedImage: "none",
		},
		{
			name: "auto unicode locale",
			env: map[string]string{
				"LANG": "en_US.UTF-8",
			},
			expectedMode:  terminalVisualUnicode,
			expectedImage: "none",
		},
		{
			name: "kitty image detection",
			env: map[string]string{
				"LANG":            "en_US.UTF-8",
				"KITTY_WINDOW_ID": "1",
			},
			expectedMode:  terminalVisualUnicode,
			expectedImage: "kitty",
		},
		{
			name: "explicit image protocol off",
			env: map[string]string{
				"LANG":                 "en_US.UTF-8",
				"KITTY_WINDOW_ID":      "1",
				"NVIEW_IMAGE_PROTOCOL": "none",
			},
			expectedMode:  terminalVisualUnicode,
			expectedImage: "none",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := terminalVisualsFromEnv(func(key string) string {
				return tt.env[key]
			})
			if got.Mode != tt.expectedMode {
				t.Errorf("Mode = %d, expected %d", got.Mode, tt.expectedMode)
			}
			if got.ImageProtocol != tt.expectedImage {
				t.Errorf(
					"ImageProtocol = %q, expected %q",
					got.ImageProtocol,
					tt.expectedImage,
				)
			}
		})
	}
}

func TestUIProgressBarPlainFallback(t *testing.T) {
	t.Setenv("NVIEW_VISUAL_MODE", "plain")
	t.Setenv("NVIEW_IMAGE_PROTOCOL", "none")

	got := uiProgressBar(50, 4, uiSeverityOK)
	expected := "[green]##[gray]--[white]"
	if got != expected {
		t.Errorf("uiProgressBar() = %q, expected %q", got, expected)
	}
}

func TestUIProgressBarUnicode(t *testing.T) {
	t.Setenv("NVIEW_VISUAL_MODE", "unicode")
	t.Setenv("NVIEW_IMAGE_PROTOCOL", "none")

	got := uiProgressBar(50, 4, uiSeverityWarn)
	expected := "[yellow]██[gray]░░[white]"
	if got != expected {
		t.Errorf("uiProgressBar() = %q, expected %q", got, expected)
	}
}

func TestUIWrapEscapesTagDelimiters(t *testing.T) {
	got := uiValue("node[relay]")
	expected := "[white]" + tview.Escape("node[relay]") + "[white]"
	if got != expected {
		t.Errorf("uiValue() = %q, expected %q", got, expected)
	}

	got = uiPill("node[type]", "relay[1]", uiSeverityNeutral)
	expected = "[gray] " + tview.Escape("node[type]") +
		" [white]" + tview.Escape("relay[1]") + "[white]"
	if got != expected {
		t.Errorf("uiPill() = %q, expected %q", got, expected)
	}
}

func TestUISegmentBarPlainFallback(t *testing.T) {
	t.Setenv("NVIEW_VISUAL_MODE", "plain")
	t.Setenv("NVIEW_IMAGE_PROTOCOL", "none")

	got := uiSegmentBar([]uiSegment{
		{Value: 3, Severity: uiSeverityOK},
		{Value: 1, Severity: uiSeverityWarn},
	}, 4)
	expected := "[green]###[white][yellow]#[white]"
	if got != expected {
		t.Errorf("uiSegmentBar() = %q, expected %q", got, expected)
	}
}

func TestUISegmentBarRemainderUsesLastPositiveSegment(t *testing.T) {
	t.Setenv("NVIEW_VISUAL_MODE", "plain")
	t.Setenv("NVIEW_IMAGE_PROTOCOL", "none")

	got := uiSegmentBar([]uiSegment{
		{Value: 1, Severity: uiSeverityOK},
		{Value: 1, Severity: uiSeverityWarn},
		{Value: 1, Severity: uiSeverityCritical},
		{Value: 0, Severity: uiSeverityMuted},
	}, 4)
	expected := "[green]#[white][yellow]#[white][red]##[white]"
	if got != expected {
		t.Errorf("uiSegmentBar() = %q, expected %q", got, expected)
	}
}

func TestUISparklinePlainFallback(t *testing.T) {
	t.Setenv("NVIEW_VISUAL_MODE", "plain")
	t.Setenv("NVIEW_IMAGE_PROTOCOL", "none")

	got := uiSparkline([]float64{0, 50, 100}, 100, uiSeverityOK)
	expected := "[green].+@[white]"
	if got != expected {
		t.Errorf("uiSparkline() = %q, expected %q", got, expected)
	}
}

func TestUIKVAlignedPadsBeforeColor(t *testing.T) {
	got := uiKVAligned(8, "Hot", uiValue("1"))
	expected := "[green]Hot     [white] [white]1[white]"
	if got != expected {
		t.Errorf("uiKVAligned() = %q, expected %q", got, expected)
	}
}
