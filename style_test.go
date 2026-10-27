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
	"os"
	"path/filepath"
	"testing"

	"github.com/blinklabs-io/nview/internal/config"
	"github.com/rivo/tview"
)

// clearVisualEnv unsets the variables that LoadConfig reads for the visual
// settings, including the prefixed names envconfig checks first, so an
// inherited value cannot change what a test loads. The values are restored
// when the test ends.
func clearVisualEnv(t *testing.T) {
	t.Helper()

	for _, name := range []string{
		"NVIEW_VISUAL_MODE", "NVIEW_IMAGE_PROTOCOL",
		"DUMMY_APP_NVIEW_VISUAL_MODE", "DUMMY_APP_NVIEW_IMAGE_PROTOCOL",
	} {
		// An empty value would still count as set and override the file,
		// so register the restore with Setenv and then unset.
		t.Setenv(name, "")
		os.Unsetenv(name)
	}
}

// setTestVisuals loads the config with the given visual mode and image
// protocol, as NVIEW_VISUAL_MODE and NVIEW_IMAGE_PROTOCOL would, and reloads
// the defaults when the test ends. The reload is registered before the
// environment is set so that it runs after the environment is restored.
func setTestVisuals(t *testing.T, visualMode, imageProtocol string) {
	t.Helper()

	t.Cleanup(func() {
		if _, err := config.LoadConfig(""); err != nil {
			t.Errorf("config.LoadConfig() error = %v", err)
		}
	})
	clearVisualEnv(t)
	t.Setenv("NVIEW_VISUAL_MODE", visualMode)
	t.Setenv("NVIEW_IMAGE_PROTOCOL", imageProtocol)
	if _, err := config.LoadConfig(""); err != nil {
		t.Fatalf("config.LoadConfig() error = %v", err)
	}
}

// TestTerminalVisuals checks how the configured visual mode and image protocol
// combine with the terminal environment (TERM, locale, Kitty and iTerm
// variables) into the glyph set and image protocol nview uses.
func TestTerminalVisuals(t *testing.T) {
	tests := []struct {
		name          string
		visualMode    string
		imageProtocol string
		env           map[string]string
		expectedMode  terminalVisualMode
		expectedImage string
	}{
		{
			name:          "explicit plain",
			visualMode:    "plain",
			expectedMode:  terminalVisualPlain,
			expectedImage: "none",
		},
		{
			name:          "explicit nerd font",
			visualMode:    "nerd",
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
			name:          "explicit image protocol off",
			imageProtocol: "none",
			env: map[string]string{
				"LANG":            "en_US.UTF-8",
				"KITTY_WINDOW_ID": "1",
			},
			expectedMode:  terminalVisualUnicode,
			expectedImage: "none",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := terminalVisuals(tt.visualMode, tt.imageProtocol, func(key string) string {
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
	setTestVisuals(t, "plain", "none")

	got := uiProgressBar(50, 4, uiSeverityOK)
	expected := "[green]##[gray]--[white]"
	if got != expected {
		t.Errorf("uiProgressBar() = %q, expected %q", got, expected)
	}
}

func TestUIProgressBarUnicode(t *testing.T) {
	setTestVisuals(t, "unicode", "none")

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
	setTestVisuals(t, "plain", "none")

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
	setTestVisuals(t, "plain", "none")

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
	setTestVisuals(t, "plain", "none")

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

// TestCurrentTerminalVisualsReadsYAMLConfig checks that app.visualMode and
// app.imageProtocol in the YAML file reach the TUI, and that the environment
// variables still override them.
func TestCurrentTerminalVisualsReadsYAMLConfig(t *testing.T) {
	t.Cleanup(func() {
		if _, err := config.LoadConfig(""); err != nil {
			t.Errorf("config.LoadConfig() error = %v", err)
		}
	})
	clearVisualEnv(t)
	t.Setenv("LANG", "en_US.UTF-8")
	// Image detection would pick kitty, so "none" can only come from the file.
	t.Setenv("KITTY_WINDOW_ID", "1")

	file := filepath.Join(t.TempDir(), "config.yaml")
	yaml := "app:\n  visualMode: plain\n  imageProtocol: none\n"
	if err := os.WriteFile(file, []byte(yaml), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	if _, err := config.LoadConfig(file); err != nil {
		t.Fatalf("config.LoadConfig() error = %v", err)
	}
	got := currentTerminalVisuals()
	if got.Mode != terminalVisualPlain || got.ImageProtocol != "none" {
		t.Errorf("from YAML: Mode = %d, ImageProtocol = %q, expected plain and none",
			got.Mode, got.ImageProtocol)
	}

	t.Setenv("NVIEW_VISUAL_MODE", "nerd")
	if _, err := config.LoadConfig(file); err != nil {
		t.Fatalf("config.LoadConfig() error = %v", err)
	}
	if got := currentTerminalVisuals(); got.Mode != terminalVisualNerd {
		t.Errorf("env override: Mode = %d, expected %d", got.Mode, terminalVisualNerd)
	}
}
