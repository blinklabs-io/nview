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
	"context"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/blinklabs-io/nview/internal/config"
)

func TestGetEpochProgress(t *testing.T) {
	tests := []struct {
		name              string
		promMetrics       *PromMetrics
		shelleyTransEpoch int32
		epochLength       uint64
		expected          float32
	}{
		{
			name:              "nil promMetrics",
			promMetrics:       nil,
			shelleyTransEpoch: 100,
			epochLength:       432000,
			expected:          0.0,
		},
		{
			name: "before Shelley transition",
			promMetrics: &PromMetrics{
				EpochNum:    50,
				SlotInEpoch: 216000,
			},
			shelleyTransEpoch: 100,
			epochLength:       432000,
			expected:          50.0,
		},
		{
			name: "after Shelley transition",
			promMetrics: &PromMetrics{
				EpochNum:    150,
				SlotInEpoch: 216000,
			},
			shelleyTransEpoch: 100,
			epochLength:       432000,
			expected:          50.0,
		},
		{
			name: "zero epoch length",
			promMetrics: &PromMetrics{
				EpochNum:    150,
				SlotInEpoch: 216000,
			},
			shelleyTransEpoch: 100,
			epochLength:       0,
			expected:          0.0,
		},
		{
			name: "negative Shelley trans epoch",
			promMetrics: &PromMetrics{
				EpochNum:    50,
				SlotInEpoch: 216000,
			},
			shelleyTransEpoch: -1,
			epochLength:       432000,
			expected:          0.0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Set up config
			cfg := config.GetConfig()
			cfg.Node.ShelleyTransEpoch = tt.shelleyTransEpoch
			cfg.Node.ShelleyGenesis.EpochLength = tt.epochLength
			cfg.Node.ByronGenesis.EpochLength = tt.epochLength

			// Set global promMetrics
			originalPromMetrics := promMetrics
			promMetrics = tt.promMetrics
			defer func() { promMetrics = originalPromMetrics }()

			result := getEpochProgress()
			if result != tt.expected {
				t.Errorf("getEpochProgress() = %v, expected %v", result, tt.expected)
			}
		})
	}
}

func TestGetEpochText(t *testing.T) {
	tests := []struct {
		name        string
		promMetrics *PromMetrics
		currentEpoch uint64
		checkContains string
	}{
		{
			name: "normal case",
			promMetrics: &PromMetrics{
				EpochNum:    100,
				SlotInEpoch: 216000,
			},
			currentEpoch: 100,
			checkContains: "100",
		},
		{
			name:         "nil promMetrics",
			promMetrics:  nil,
			currentEpoch: 100,
			checkContains: "100",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Set up config
			cfg := config.GetConfig()
			cfg.Node.ShelleyTransEpoch = 50
			cfg.Node.ShelleyGenesis.EpochLength = 432000
			cfg.Node.ByronGenesis.EpochLength = 216000

			// Set globals
			originalPromMetrics := promMetrics
			originalCurrentEpoch := currentEpoch
			promMetrics = tt.promMetrics
			currentEpoch = tt.currentEpoch
			defer func() {
				promMetrics = originalPromMetrics
				currentEpoch = originalCurrentEpoch
			}()

			ctx := context.Background()
			result := getEpochText(ctx)
			if !strings.Contains(result, tt.checkContains) {
				t.Errorf("getEpochText() = %q, expected to contain %q", result, tt.checkContains)
			}
		})
	}
}

func TestTcpinfoRtt(t *testing.T) {
	tests := []struct {
		name     string
		address  string
		expected int
	}{
		{
			name:     "invalid address",
			address:  "invalid:1234",
			expected: RTTUnreachable,
		},
		{
			name:     "empty address",
			address:  "",
			expected: RTTUnreachable,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := tcpinfoRtt(tt.address)
			if result != tt.expected {
				t.Errorf("tcpinfoRtt(%q) = %v, expected %v", tt.address, result, tt.expected)
			}
		})
	}
}

func BenchmarkGetEpochProgress(b *testing.B) {
	// Set up config
	cfg := config.GetConfig()
	cfg.Node.ShelleyTransEpoch = 100
	cfg.Node.ShelleyGenesis.EpochLength = 432000
	cfg.Node.ByronGenesis.EpochLength = 216000

	// Set promMetrics
	promMetrics = &PromMetrics{
		EpochNum:    150,
		SlotInEpoch: 216000,
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		getEpochProgress()
	}
}

func TestBufferHandler_Handle(t *testing.T) {
	// Set up config
	cfg := config.GetConfig()
	cfg.App.LogBufferSize = 10

	// Create a bufferHandler with a text handler
	textHandler := slog.NewTextHandler(&strings.Builder{}, &slog.HandlerOptions{})
	bh := &bufferHandler{handler: textHandler}

	// Create a test record
	record := slog.Record{
		Time:    time.Now(),
		Level:   slog.LevelInfo,
		Message: "test message",
	}
	record.AddAttrs(slog.String("key", "value"))

	// Reset logBuffer
	logMutex.Lock()
	logBuffer = nil
	logMutex.Unlock()

	// Call Handle
	err := bh.Handle(context.Background(), record)
	if err != nil {
		t.Fatalf("Handle returned error: %v", err)
	}

	// Check logBuffer
	logMutex.Lock()
	defer logMutex.Unlock()
	if len(logBuffer) != 1 {
		t.Errorf("expected 1 log entry, got %d", len(logBuffer))
	}
	if len(logBuffer) > 0 {
		expectedParts := []string{"INFO", "test message", "key=value"}
		for _, part := range expectedParts {
			if !strings.Contains(logBuffer[0], part) {
				t.Errorf("logBuffer[0] does not contain %q: %q", part, logBuffer[0])
			}
		}
	}
}

func TestBufferHandler_Enabled(t *testing.T) {
	// Create a bufferHandler with a text handler
	textHandler := slog.NewTextHandler(&strings.Builder{}, &slog.HandlerOptions{Level: slog.LevelInfo})
	bh := &bufferHandler{handler: textHandler}

	// Test enabled level
	if !bh.Enabled(context.Background(), slog.LevelInfo) {
		t.Error("Expected LevelInfo to be enabled")
	}
	if !bh.Enabled(context.Background(), slog.LevelWarn) {
		t.Error("Expected LevelWarn to be enabled")
	}
	if bh.Enabled(context.Background(), slog.LevelDebug) {
		t.Error("Expected LevelDebug to be disabled")
	}
}

func TestBufferHandler_WithAttrs(t *testing.T) {
	// Create a bufferHandler with a text handler
	textHandler := slog.NewTextHandler(&strings.Builder{}, &slog.HandlerOptions{})
	bh := &bufferHandler{handler: textHandler}

	// Call WithAttrs
	attrs := []slog.Attr{slog.String("test", "value")}
	newHandler := bh.WithAttrs(attrs)

	// Check it's a bufferHandler
	newBh, ok := newHandler.(*bufferHandler)
	if !ok {
		t.Fatal("WithAttrs did not return a *bufferHandler")
	}

	// Check the handler is wrapped
	if newBh.handler == bh.handler {
		t.Error("WithAttrs should return a new handler")
	}
}

func TestBufferHandler_WithGroup(t *testing.T) {
	// Create a bufferHandler with a text handler
	textHandler := slog.NewTextHandler(&strings.Builder{}, &slog.HandlerOptions{})
	bh := &bufferHandler{handler: textHandler}

	// Call WithGroup
	newHandler := bh.WithGroup("testgroup")

	// Check it's a bufferHandler
	newBh, ok := newHandler.(*bufferHandler)
	if !ok {
		t.Fatal("WithGroup did not return a *bufferHandler")
	}

	// Check the handler is wrapped
	if newBh.handler == bh.handler {
		t.Error("WithGroup should return a new handler")
	}
}

func TestGetConnectionText(t *testing.T) {
	tests := []struct {
		name     string
		p2p      bool
		promMetrics *PromMetrics
		checkContains string
	}{
		{
			name: "P2P enabled with metrics",
			p2p:  true,
			promMetrics: &PromMetrics{
				ConnIncoming: 10,
				ConnOutgoing: 5,
				PeersCold:    2,
				PeersWarm:    3,
				PeersHot:     1,
				ConnUniDir:   4,
				ConnBiDir:    6,
				ConnDuplex:   8,
			},
			checkContains: "P2P        : enabled",
		},
		{
			name:     "P2P enabled nil metrics",
			p2p:      true,
			promMetrics: nil,
			checkContains: "", // returns connectionText, but since it's global, hard to test
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Set globals
			originalP2p := p2p
			originalPromMetrics := promMetrics
			p2p = tt.p2p
			promMetrics = tt.promMetrics
			defer func() {
				p2p = originalP2p
				promMetrics = originalPromMetrics
			}()

			ctx := context.Background()
			result := getConnectionText(ctx)
			if tt.checkContains != "" && !strings.Contains(result, tt.checkContains) {
				t.Errorf("getConnectionText() = %q, expected to contain %q", result, tt.checkContains)
			}
		})
	}
}

func TestGetCoreText(t *testing.T) {
	tests := []struct {
		name     string
		role     string
		promMetrics *PromMetrics
		checkContains string
	}{
		{
			name: "Core role with metrics",
			role: "Core",
			promMetrics: &PromMetrics{
				IsLeader:   1,
				Adopted:    1,
				DidntAdopt: 0,
			},
			checkContains: "Leader",
		},
		{
			name:     "Non-core role",
			role:     "Relay",
			promMetrics: &PromMetrics{},
			checkContains: "N/A",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Set globals
			originalRole := role
			originalPromMetrics := promMetrics
			role = tt.role
			promMetrics = tt.promMetrics
			defer func() {
				role = originalRole
				promMetrics = originalPromMetrics
			}()

			ctx := context.Background()
			result := getCoreText(ctx)
			if !strings.Contains(result, tt.checkContains) {
				t.Errorf("getCoreText() = %q, expected to contain %q", result, tt.checkContains)
			}
		})
	}
}
