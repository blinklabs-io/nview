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
	"errors"
	"log/slog"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/blinklabs-io/nview/internal/config"
	"github.com/shirou/gopsutil/v3/process"
)

// TestDingoProcessHelper is a subprocess target used by tests that need a real
// process with readable cmdline and environment metadata.
func TestDingoProcessHelper(t *testing.T) {
	if os.Getenv("GO_WANT_DINGO_PROCESS_HELPER") != "1" {
		return
	}
	time.Sleep(time.Minute)
	os.Exit(0)
}

func startDingoProcessHelper(
	t *testing.T,
	env []string,
	args ...string,
) *process.Process {
	t.Helper()

	executable, err := os.Executable()
	if err != nil {
		t.Fatalf("failed to find test executable: %v", err)
	}
	cmdArgs := append([]string{"-test.run=TestDingoProcessHelper", "--"}, args...)
	cmd := exec.Command(executable, cmdArgs...)
	cmd.Env = append(os.Environ(), "GO_WANT_DINGO_PROCESS_HELPER=1")
	cmd.Env = append(cmd.Env, env...)
	if err := cmd.Start(); err != nil {
		t.Fatalf("failed to start helper process: %v", err)
	}
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
	})

	proc, err := process.NewProcessWithContext(
		context.Background(),
		int32(cmd.Process.Pid),
	)
	if err != nil {
		t.Fatalf("failed to inspect helper process: %v", err)
	}

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := proc.CmdlineSliceWithContext(context.Background()); err == nil {
			return proc
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("helper process cmdline was not readable")
	return nil
}

// TestFindDingoProcessUsesExplicitPID verifies that an operator-provided PID
// wins before any automatic discovery is attempted.
func TestFindDingoProcessUsesExplicitPID(t *testing.T) {
	cfg := config.GetConfig()
	originalNodePid := cfg.Node.Pid
	defer func() {
		cfg.Node.Pid = originalNodePid
	}()

	cfg.Node.Pid = int32(os.Getpid())

	proc, err := findDingoProcess(context.Background(), cfg, procLookups{
		socketOwner: func(context.Context, string, uint32) (int32, error) {
			t.Fatal("socketOwner should not be called when explicit PID is set")
			return 0, nil
		},
		listProcs: func(context.Context, string) ([]*process.Process, error) {
			t.Fatal("listProcs should not be called when explicit PID is set")
			return nil, nil
		},
	})
	if err != nil {
		t.Fatalf("findDingoProcess() returned error: %v", err)
	}
	if proc.Pid != cfg.Node.Pid {
		t.Fatalf("findDingoProcess() returned pid %d, expected %d", proc.Pid, cfg.Node.Pid)
	}
}

// TestFindDingoProcessUsesSocketOwnerBeforePID1Fallback verifies that the
// process serving the Prometheus scrape socket wins over the PID 1 fallback.
func TestFindDingoProcessUsesSocketOwnerBeforePID1Fallback(t *testing.T) {
	cfg := config.GetConfig()
	originalNodeBinary := cfg.Node.Binary
	originalNodePid := cfg.Node.Pid
	originalPromHost := cfg.Prometheus.Host
	originalPromPort := cfg.Prometheus.Port
	originalLogBufferSize := cfg.App.LogBufferSize
	originalDetectedBinary, _ := detectedNodeBinary.Load().(string)
	originalLogger := logger
	originalSelectionLogged := dingoProcessSelectionLogged.Load()
	originalAmbiguityLogged := dingoProcessAmbiguityLogged.Load()
	defer func() {
		cfg.Node.Binary = originalNodeBinary
		cfg.Node.Pid = originalNodePid
		cfg.Prometheus.Host = originalPromHost
		cfg.Prometheus.Port = originalPromPort
		cfg.App.LogBufferSize = originalLogBufferSize
		detectedNodeBinary.Store(originalDetectedBinary)
		logger = originalLogger
		dingoProcessSelectionLogged.Store(originalSelectionLogged)
		dingoProcessAmbiguityLogged.Store(originalAmbiguityLogged)
		logMutex.Lock()
		logBuffer = nil
		logMutex.Unlock()
	}()

	cfg.Node.Binary = DINGO_BINARY
	cfg.Node.Pid = 0
	cfg.Prometheus.Host = "127.0.0.1"
	cfg.Prometheus.Port = 12798
	cfg.App.LogBufferSize = 10
	detectedNodeBinary.Store(DINGO_BINARY)
	dingoProcessSelectionLogged.Store(false)
	dingoProcessAmbiguityLogged.Store(false)
	logger = slog.New(&bufferHandler{
		handler: slog.NewTextHandler(&strings.Builder{}, &slog.HandlerOptions{}),
	})
	logMutex.Lock()
	logBuffer = nil
	logMutex.Unlock()

	socketOwnerPID := int32(os.Getpid())
	proc, err := findDingoProcess(context.Background(), cfg, procLookups{
		socketOwner: func(
			_ context.Context,
			host string,
			port uint32,
		) (int32, error) {
			if host != cfg.Prometheus.Host {
				t.Fatalf("socketOwner host = %q, expected %q", host, cfg.Prometheus.Host)
			}
			if port != cfg.Prometheus.Port {
				t.Fatalf("socketOwner port = %d, expected %d", port, cfg.Prometheus.Port)
			}
			return socketOwnerPID, nil
		},
	})
	if err != nil {
		t.Fatalf("findDingoProcess() returned error: %v", err)
	}

	if proc.Pid != socketOwnerPID {
		t.Fatalf("findDingoProcess() returned pid %d, expected socket owner pid %d", proc.Pid, socketOwnerPID)
	}

	logMutex.Lock()
	defer logMutex.Unlock()
	joinedLogs := strings.Join(logBuffer, "")
	for _, expected := range []string{
		"INFO",
		"selected dingo process",
		"pid=",
		"method=socket-owner",
	} {
		if !strings.Contains(joinedLogs, expected) {
			t.Fatalf("expected log buffer to contain %q, got %q", expected, joinedLogs)
		}
	}
}

// TestFindDingoProcessFallsBackFromSocketErrorToCmdlineMatch verifies that
// socket lookup failures fall through to --metrics-port matching.
func TestFindDingoProcessFallsBackFromSocketErrorToCmdlineMatch(t *testing.T) {
	cfg := config.GetConfig()
	originalNodePid := cfg.Node.Pid
	originalPromPort := cfg.Prometheus.Port
	defer func() {
		cfg.Node.Pid = originalNodePid
		cfg.Prometheus.Port = originalPromPort
	}()

	cfg.Node.Pid = 0
	cfg.Prometheus.Port = 12798
	matchingProc := startDingoProcessHelper(
		t,
		nil,
		"--metrics-port=12798",
		"--data-dir=/tmp/dingo-mainnet",
	)

	proc, err := findDingoProcess(context.Background(), cfg, procLookups{
		socketOwner: func(context.Context, string, uint32) (int32, error) {
			return 0, errors.New("permission denied")
		},
		listProcs: func(context.Context, string) ([]*process.Process, error) {
			return []*process.Process{matchingProc}, nil
		},
	})
	if err != nil {
		t.Fatalf("findDingoProcess() returned error: %v", err)
	}
	if proc.Pid != matchingProc.Pid {
		t.Fatalf("findDingoProcess() returned pid %d, expected cmdline match pid %d", proc.Pid, matchingProc.Pid)
	}
}

// TestFindDingoProcessUsesEnvMetricsPortMatch verifies that
// DINGO_METRICS_PORT can identify the process serving the scrape port.
func TestFindDingoProcessUsesEnvMetricsPortMatch(t *testing.T) {
	cfg := config.GetConfig()
	originalNodePid := cfg.Node.Pid
	originalPromPort := cfg.Prometheus.Port
	defer func() {
		cfg.Node.Pid = originalNodePid
		cfg.Prometheus.Port = originalPromPort
	}()

	cfg.Node.Pid = 0
	cfg.Prometheus.Port = 12798
	matchingProc := startDingoProcessHelper(
		t,
		[]string{"DINGO_METRICS_PORT=12798"},
		"--data-dir",
		"/tmp/dingo-mainnet",
	)

	proc, err := findDingoProcess(context.Background(), cfg, procLookups{
		socketOwner: func(context.Context, string, uint32) (int32, error) {
			return 0, errors.New("no socket owner")
		},
		listProcs: func(context.Context, string) ([]*process.Process, error) {
			return []*process.Process{matchingProc}, nil
		},
	})
	if err != nil {
		t.Fatalf("findDingoProcess() returned error: %v", err)
	}
	if proc.Pid != matchingProc.Pid {
		t.Fatalf("findDingoProcess() returned pid %d, expected env match pid %d", proc.Pid, matchingProc.Pid)
	}
}

// TestFindDingoProcessWarnsAndPicksLowestPIDForAmbiguousMultiMatch verifies
// the deterministic fallback and visible warning for ambiguous multi-instance setups.
func TestFindDingoProcessWarnsAndPicksLowestPIDForAmbiguousMultiMatch(t *testing.T) {
	cfg := config.GetConfig()
	originalNodePid := cfg.Node.Pid
	originalPromPort := cfg.Prometheus.Port
	originalLogBufferSize := cfg.App.LogBufferSize
	originalLogger := logger
	originalSelectionLogged := dingoProcessSelectionLogged.Load()
	originalAmbiguityLogged := dingoProcessAmbiguityLogged.Load()
	defer func() {
		cfg.Node.Pid = originalNodePid
		cfg.Prometheus.Port = originalPromPort
		cfg.App.LogBufferSize = originalLogBufferSize
		logger = originalLogger
		dingoProcessSelectionLogged.Store(originalSelectionLogged)
		dingoProcessAmbiguityLogged.Store(originalAmbiguityLogged)
		logMutex.Lock()
		logBuffer = nil
		logMutex.Unlock()
	}()

	cfg.Node.Pid = 0
	cfg.Prometheus.Port = 12798
	cfg.App.LogBufferSize = 10
	dingoProcessSelectionLogged.Store(false)
	dingoProcessAmbiguityLogged.Store(false)
	logger = slog.New(&bufferHandler{
		handler: slog.NewTextHandler(&strings.Builder{}, &slog.HandlerOptions{}),
	})
	logMutex.Lock()
	logBuffer = nil
	logMutex.Unlock()

	lowest := &process.Process{Pid: 12345}
	highest := &process.Process{Pid: 23456}
	proc, err := findDingoProcess(context.Background(), cfg, procLookups{
		socketOwner: func(context.Context, string, uint32) (int32, error) {
			return 0, errors.New("no socket owner")
		},
		listProcs: func(context.Context, string) ([]*process.Process, error) {
			return []*process.Process{highest, lowest}, nil
		},
	})
	if err != nil {
		t.Fatalf("findDingoProcess() returned error: %v", err)
	}
	if proc.Pid != lowest.Pid {
		t.Fatalf("findDingoProcess() returned pid %d, expected lowest pid %d", proc.Pid, lowest.Pid)
	}

	logMutex.Lock()
	defer logMutex.Unlock()
	if len(logBuffer) == 0 {
		t.Fatal("expected warning log entry in buffer")
	}
	joinedLogs := strings.Join(logBuffer, "")
	for _, expected := range []string{
		"WARN",
		"multiple dingo processes found",
		"picked-pid=12345",
		"pid=12345 metrics-port=- data-dir=-",
		"pid=23456 metrics-port=- data-dir=-",
	} {
		if !strings.Contains(joinedLogs, expected) {
			t.Fatalf("expected log buffer to contain %q, got %q", expected, joinedLogs)
		}
	}
}

// TestFindDingoProcessUsesSingleNameMatchWhenCmdlineUnreadable verifies that a
// lone Dingo process is used even when cmdline and env metadata cannot be read.
func TestFindDingoProcessUsesSingleNameMatchWhenCmdlineUnreadable(t *testing.T) {
	cfg := config.GetConfig()
	originalNodePid := cfg.Node.Pid
	defer func() {
		cfg.Node.Pid = originalNodePid
	}()

	cfg.Node.Pid = 0
	namedProc := &process.Process{Pid: 54321}
	proc, err := findDingoProcess(context.Background(), cfg, procLookups{
		socketOwner: func(context.Context, string, uint32) (int32, error) {
			return 0, errors.New("no socket owner")
		},
		listProcs: func(context.Context, string) ([]*process.Process, error) {
			return []*process.Process{namedProc}, nil
		},
	})
	if err != nil {
		t.Fatalf("findDingoProcess() returned error: %v", err)
	}
	if proc.Pid != namedProc.Pid {
		t.Fatalf("findDingoProcess() returned pid %d, expected single name match pid %d", proc.Pid, namedProc.Pid)
	}
}

// TestFindDingoProcessFallsBackToPID1WhenNoNameMatches verifies that PID 1 is
// only used after automatic discovery finds no Dingo process by name.
func TestFindDingoProcessFallsBackToPID1WhenNoNameMatches(t *testing.T) {
	cfg := config.GetConfig()
	originalNodePid := cfg.Node.Pid
	defer func() {
		cfg.Node.Pid = originalNodePid
	}()

	cfg.Node.Pid = 0
	proc, err := findDingoProcess(context.Background(), cfg, procLookups{
		socketOwner: func(context.Context, string, uint32) (int32, error) {
			return 0, errors.New("no socket owner")
		},
		listProcs: func(context.Context, string) ([]*process.Process, error) {
			return nil, nil
		},
	})
	if err != nil {
		t.Fatalf("findDingoProcess() returned error: %v", err)
	}
	if proc.Pid != 1 {
		t.Fatalf("findDingoProcess() returned pid %d, expected PID 1 fallback", proc.Pid)
	}
}

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
				t.Errorf(
					"getEpochProgress() = %v, expected %v",
					result,
					tt.expected,
				)
			}
		})
	}
}

func TestGetEpochText(t *testing.T) {
	tests := []struct {
		name          string
		promMetrics   *PromMetrics
		currentEpoch  uint64
		checkContains string
	}{
		{
			name: "normal case",
			promMetrics: &PromMetrics{
				EpochNum:    100,
				SlotInEpoch: 216000,
			},
			currentEpoch:  100,
			checkContains: "100",
		},
		{
			name:          "nil promMetrics",
			promMetrics:   nil,
			currentEpoch:  100,
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
				t.Errorf(
					"getEpochText() = %q, expected to contain %q",
					result,
					tt.checkContains,
				)
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
				t.Errorf(
					"tcpinfoRtt(%q) = %v, expected %v",
					tt.address,
					result,
					tt.expected,
				)
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
	textHandler := slog.NewTextHandler(
		&strings.Builder{},
		&slog.HandlerOptions{},
	)
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
				t.Errorf(
					"logBuffer[0] does not contain %q: %q",
					part,
					logBuffer[0],
				)
			}
		}
	}
}

func TestBufferHandler_Enabled(t *testing.T) {
	// Create a bufferHandler with a text handler
	textHandler := slog.NewTextHandler(
		&strings.Builder{},
		&slog.HandlerOptions{Level: slog.LevelInfo},
	)
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
	textHandler := slog.NewTextHandler(
		&strings.Builder{},
		&slog.HandlerOptions{},
	)
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
	textHandler := slog.NewTextHandler(
		&strings.Builder{},
		&slog.HandlerOptions{},
	)
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
		name          string
		p2p           bool
		promMetrics   *PromMetrics
		checkContains string
	}{
		{
			name: "P2P enabled with metrics",
			p2p:  true,
			promMetrics: &PromMetrics{
				ConnIncoming:   10,
				ConnOutgoing:   5,
				PeersCold:      2,
				PeersWarm:      3,
				PeersHot:       1,
				ConnUniDir:     4,
				ConnBiDir:      6,
				ConnFullDuplex: 8,
			},
			checkContains: "P2P        : enabled",
		},
		{
			name:          "P2P enabled nil metrics",
			p2p:           true,
			promMetrics:   nil,
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
			if tt.checkContains != "" &&
				!strings.Contains(result, tt.checkContains) {
				t.Errorf(
					"getConnectionText() = %q, expected to contain %q",
					result,
					tt.checkContains,
				)
			}
		})
	}
}

func TestGetCoreText(t *testing.T) {
	tests := []struct {
		name          string
		role          string
		promMetrics   *PromMetrics
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
			name:          "Non-core role",
			role:          "Relay",
			promMetrics:   &PromMetrics{},
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
				t.Errorf(
					"getCoreText() = %q, expected to contain %q",
					result,
					tt.checkContains,
				)
			}
		})
	}
}
