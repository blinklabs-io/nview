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
	"path/filepath"
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

// startDingoProcessHelper starts a real child process with optional env and
// args so gopsutil can inspect cmdline/env data in tests.
func startDingoProcessHelper(
	t *testing.T,
	env []string,
	args ...string,
) *process.Process {
	t.Helper()
	return startNamedDingoProcessHelper(t, "", env, args...)
}

// startNamedDingoProcessHelper starts the helper through an optional symlink
// name so socket-owner tests can make the process look like Dingo.
func startNamedDingoProcessHelper(
	t *testing.T,
	name string,
	env []string,
	args ...string,
) *process.Process {
	t.Helper()

	executable, err := os.Executable()
	if err != nil {
		t.Fatalf("failed to find test executable: %v", err)
	}
	if name != "" {
		linkPath := filepath.Join(t.TempDir(), name)
		if err := os.Symlink(executable, linkPath); err != nil {
			t.Skipf("cannot create helper symlink: %v", err)
		}
		executable = linkPath
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

// preserveDingoDiscoveryAtomics restores one-shot Dingo logging flags so tests
// that invoke findDingoProcess do not leak state into later tests.
func preserveDingoDiscoveryAtomics(t *testing.T) {
	t.Helper()
	originalSelectionLogged := dingoProcessSelectionLogged.Load()
	originalAmbiguityLogged := dingoProcessAmbiguityLogged.Load()
	t.Cleanup(func() {
		dingoProcessSelectionLogged.Store(originalSelectionLogged)
		dingoProcessAmbiguityLogged.Store(originalAmbiguityLogged)
	})
}

// TestFindDingoProcessUsesExplicitPID verifies that an operator-provided PID
// wins before any automatic discovery is attempted.
func TestFindDingoProcessUsesExplicitPID(t *testing.T) {
	preserveDingoDiscoveryAtomics(t)

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

	socketOwnerProc := startNamedDingoProcessHelper(t, DINGO_BINARY, nil)
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
			return socketOwnerProc.Pid, nil
		},
	})
	if err != nil {
		t.Fatalf("findDingoProcess() returned error: %v", err)
	}

	if proc.Pid != socketOwnerProc.Pid {
		t.Fatalf("findDingoProcess() returned pid %d, expected socket owner pid %d", proc.Pid, socketOwnerProc.Pid)
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
	preserveDingoDiscoveryAtomics(t)

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
		[]string{"DINGO_METRICS_PORT=12799"},
		"--metrics-port=12798",
		"--data-dir=/tmp/dingo-mainnet",
	)
	nonmatchingProc := startDingoProcessHelper(
		t,
		nil,
		"--metrics-port=12799",
		"--data-dir=/tmp/dingo-preprod",
	)

	proc, err := findDingoProcess(context.Background(), cfg, procLookups{
		socketOwner: func(context.Context, string, uint32) (int32, error) {
			return 0, errors.New("permission denied")
		},
		listProcs: func(context.Context, string) ([]*process.Process, error) {
			return []*process.Process{nonmatchingProc, matchingProc}, nil
		},
	})
	if err != nil {
		t.Fatalf("findDingoProcess() returned error: %v", err)
	}
	if proc.Pid != matchingProc.Pid {
		t.Fatalf("findDingoProcess() returned pid %d, expected cmdline match pid %d", proc.Pid, matchingProc.Pid)
	}
}

// TestFindDingoProcessWarnsAndPicksLowestPIDForDuplicateCmdlineMatches
// verifies duplicate --metrics-port matches are treated as ambiguous.
func TestFindDingoProcessWarnsAndPicksLowestPIDForDuplicateCmdlineMatches(t *testing.T) {
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

	firstProc := startDingoProcessHelper(
		t,
		nil,
		"--metrics-port=12798",
		"--data-dir=/tmp/dingo-mainnet",
	)
	secondProc := startDingoProcessHelper(
		t,
		nil,
		"--metrics-port=12798",
		"--data-dir=/tmp/dingo-preprod",
	)
	expectedPID := firstProc.Pid
	if secondProc.Pid < expectedPID {
		expectedPID = secondProc.Pid
	}

	proc, err := findDingoProcess(context.Background(), cfg, procLookups{
		socketOwner: func(context.Context, string, uint32) (int32, error) {
			return 0, errors.New("permission denied")
		},
		listProcs: func(context.Context, string) ([]*process.Process, error) {
			return []*process.Process{secondProc, firstProc}, nil
		},
	})
	if err != nil {
		t.Fatalf("findDingoProcess() returned error: %v", err)
	}
	if proc.Pid != expectedPID {
		t.Fatalf("findDingoProcess() returned pid %d, expected lowest duplicate match pid %d", proc.Pid, expectedPID)
	}

	logMutex.Lock()
	defer logMutex.Unlock()
	joinedLogs := strings.Join(logBuffer, "")
	for _, expected := range []string{
		"WARN",
		"multiple dingo processes declared metrics-port=12798",
		"picked-pid=",
		"pid=",
		"metrics-port=12798",
	} {
		if !strings.Contains(joinedLogs, expected) {
			t.Fatalf("expected log buffer to contain %q, got %q", expected, joinedLogs)
		}
	}
}

// TestFindDingoProcessUsesEnvMetricsPortMatch verifies that
// DINGO_METRICS_PORT can identify the process serving the scrape port.
func TestFindDingoProcessUsesEnvMetricsPortMatch(t *testing.T) {
	preserveDingoDiscoveryAtomics(t)

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
	nonmatchingProc := startDingoProcessHelper(
		t,
		[]string{"DINGO_METRICS_PORT=12799"},
		"--data-dir",
		"/tmp/dingo-preprod",
	)

	proc, err := findDingoProcess(context.Background(), cfg, procLookups{
		socketOwner: func(context.Context, string, uint32) (int32, error) {
			return 0, errors.New("no socket owner")
		},
		listProcs: func(context.Context, string) ([]*process.Process, error) {
			return []*process.Process{nonmatchingProc, matchingProc}, nil
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

	// Synthetic processes intentionally make cmdline/env reads fail, leaving
	// candidate metadata as "-" to exercise the unreadable-process path.
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
	preserveDingoDiscoveryAtomics(t)

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
	preserveDingoDiscoveryAtomics(t)

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

func TestValueFromArgsUsesLastFlagOccurrence(t *testing.T) {
	tests := []struct {
		name string
		args []string
		want string
	}{
		{
			name: "equals form",
			args: []string{
				"--metrics-port=12798",
				"--metrics-port=12799",
			},
			want: "12799",
		},
		{
			name: "separate value form",
			args: []string{
				"--metrics-port",
				"12798",
				"--metrics-port",
				"12799",
			},
			want: "12799",
		},
		{
			name: "mixed forms",
			args: []string{
				"--metrics-port=12798",
				"--metrics-port",
				"12799",
			},
			want: "12799",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := valueFromArgs(tt.args, "--metrics-port")
			if got != tt.want {
				t.Fatalf("valueFromArgs() = %q, expected %q", got, tt.want)
			}
		})
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

// TestFormatDingoHitRatio verifies cache hit percentages are calculated from
// previous-sample deltas instead of cumulative counter totals.
func TestFormatDingoHitRatio(t *testing.T) {
	tests := []struct {
		name     string
		currHits uint64
		currMiss uint64
		prevHits uint64
		prevMiss uint64
		expected string
	}{
		{
			name:     "zero sample",
			expected: "n/a",
		},
		{
			name:     "growing hits and misses",
			currHits: 150,
			currMiss: 50,
			prevHits: 100,
			prevMiss: 25,
			expected: "66.7%",
		},
		{
			name:     "steady state",
			currHits: 100,
			currMiss: 25,
			prevHits: 100,
			prevMiss: 25,
			expected: "n/a",
		},
		{
			name:     "cumulative only misses",
			currMiss: 10,
			expected: "0.0%",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := formatDingoHitRatio(
				tt.currHits,
				tt.currMiss,
				tt.prevHits,
				tt.prevMiss,
			)
			if result != tt.expected {
				t.Errorf(
					"formatDingoHitRatio() = %q, expected %q",
					result,
					tt.expected,
				)
			}
		})
	}
}

// TestFormatDingoRate verifies cumulative Dingo counters are converted into
// per-second rates using the elapsed time between samples.
func TestFormatDingoRate(t *testing.T) {
	result := formatDingoRate(22, 10, 3*time.Second)
	if result != "4" {
		t.Errorf("formatDingoRate() = %q, expected %q", result, "4")
	}
}

// TestApplyDefaultSecondaryView verifies the first-scrape secondary pane
// default and the NVIEW_DEFAULT_VIEW override behavior.
func TestApplyDefaultSecondaryView(t *testing.T) {
	tests := []struct {
		name     string
		envValue string
		binary   string
		expected secondaryView
	}{
		{
			name:     "auto dingo",
			binary:   DINGO_BINARY,
			expected: viewDingo,
		},
		{
			name:     "auto non-dingo",
			binary:   CARDANO_BINARY,
			expected: viewNone,
		},
		{
			name:     "peers override",
			envValue: "peers",
			binary:   DINGO_BINARY,
			expected: viewPeers,
		},
		{
			name:     "none override",
			envValue: "none",
			binary:   DINGO_BINARY,
			expected: viewNone,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("NVIEW_DEFAULT_VIEW", tt.envValue)
			cfg := config.GetConfig()
			originalBinary := cfg.Node.Binary
			originalDefaultSet := secondaryDefaultSet.Load()
			originalActive := getActiveSecondaryView()
			defer func() {
				cfg.Node.Binary = originalBinary
				secondaryDefaultSet.Store(originalDefaultSet)
				setActiveSecondaryView(originalActive)
				detectedNodeBinary.Store("")
			}()

			cfg.Node.Binary = ""
			detectedNodeBinary.Store(tt.binary)
			secondaryDefaultSet.Store(false)
			setActiveSecondaryView(viewPeers)

			applyDefaultSecondaryView()

			if getActiveSecondaryView() != tt.expected {
				t.Errorf(
					"active secondary = %d, expected %d",
					getActiveSecondaryView(),
					tt.expected,
				)
			}
		})
	}
}

// TestGetDingoStatsRendersDiagnostics verifies the diagnostics pane renders
// Dingo-native values plus derived cache ratios and event/cold-extract rates.
func TestGetDingoStatsRendersDiagnostics(t *testing.T) {
	originalPromMetrics := promMetrics
	originalLastDingoSample := lastDingoSample
	originalLastDingoSampleAt := lastDingoSampleAt
	originalLastDingoRateBase := lastDingoRateBase
	originalLastDingoRateBaseAt := lastDingoRateBaseAt
	originalLastDingoSampleSrc := lastDingoSampleSrc
	defer func() {
		promMetrics = originalPromMetrics
		lastDingoSample = originalLastDingoSample
		lastDingoSampleAt = originalLastDingoSampleAt
		lastDingoRateBase = originalLastDingoRateBase
		lastDingoRateBaseAt = originalLastDingoRateBaseAt
		lastDingoSampleSrc = originalLastDingoSampleSrc
	}()

	lastDingoRateBase = nil
	lastDingoRateBaseAt = time.Time{}
	lastDingoSampleSrc = nil
	lastDingoSample = &PromMetrics{
		DingoCacheUtxoHotHits:  90,
		DingoCacheUtxoHotMiss:  10,
		DingoCacheTxHotHits:    40,
		DingoCacheTxHotMiss:    10,
		DingoCacheBlockLruHits: 5,
		DingoCacheBlockLruMiss: 5,
		DingoCacheColdExtract:  10,
		EventDeliveryErrors:    1,
		EventDeliveryTimeouts:  2,
	}
	lastDingoSampleAt = time.Now().Add(-10 * time.Second)
	promMetrics = &PromMetrics{
		DingoDbSizeBytes:       BytesInGigabyte,
		DingoChainCachedBlocks: 8192,
		DingoTipGapSlots:       0,
		DingoForgeTipGapSlots:  1,
		DingoSlotClockFallback: 2,
		DingoForgeSlotClockErr: 3,
		DingoForgeSyncSkip:     4,
		DingoCacheUtxoHotHits:  188,
		DingoCacheUtxoHotMiss:  12,
		DingoCacheTxHotHits:    131,
		DingoCacheTxHotMiss:    19,
		DingoCacheBlockLruHits: 81,
		DingoCacheBlockLruMiss: 29,
		DingoCacheColdExtract:  130,
		EventTotal:             100,
		EventSubscribers:       14,
		EventDeliveryErrors:    1,
		EventDeliveryTimeouts:  2,
	}

	result := getDingoStats()
	expectedParts := []string{
		"DB Size",
		"1.0G",
		"Cached Blocks",
		"8192",
		"Tip Gap",
		"Forge Gap",
		"CBOR Cache",
		"utxo 98.0%",
		"tx 91.0%",
		"blk 76.0%",
		"Cold Extract",
		"12",
		"Events",
		"14",
		"Slot Clock",
		"fallback 2",
		"forgeErr 3",
		"syncSkip 4",
	}
	for _, part := range expectedParts {
		if !strings.Contains(result, part) {
			t.Errorf("getDingoStats() missing %q in:\n%s", part, result)
		}
	}
}

func TestGetDingoStatsFirstSampleShowsUnavailableDeltas(t *testing.T) {
	originalPromMetrics := promMetrics
	originalLastDingoSample := lastDingoSample
	originalLastDingoSampleAt := lastDingoSampleAt
	originalLastDingoRateBase := lastDingoRateBase
	originalLastDingoRateBaseAt := lastDingoRateBaseAt
	originalLastDingoSampleSrc := lastDingoSampleSrc
	defer func() {
		promMetrics = originalPromMetrics
		lastDingoSample = originalLastDingoSample
		lastDingoSampleAt = originalLastDingoSampleAt
		lastDingoRateBase = originalLastDingoRateBase
		lastDingoRateBaseAt = originalLastDingoRateBaseAt
		lastDingoSampleSrc = originalLastDingoSampleSrc
	}()

	lastDingoSample = nil
	lastDingoSampleAt = time.Time{}
	lastDingoRateBase = nil
	lastDingoRateBaseAt = time.Time{}
	lastDingoSampleSrc = nil
	promMetrics = &PromMetrics{
		DingoCacheUtxoHotHits:  100,
		DingoCacheTxHotHits:    100,
		DingoCacheBlockLruHits: 100,
		DingoCacheColdExtract:  100,
		EventDeliveryErrors:    100,
		EventDeliveryTimeouts:  100,
	}

	result := getDingoStats()
	expectedParts := []string{
		"utxo n/a",
		"tx n/a",
		"blk n/a",
		"Cold Extract : [white]n/a",
		"err [white]n/a",
		"timeout [white]n/a",
	}
	for _, part := range expectedParts {
		if !strings.Contains(result, part) {
			t.Errorf("getDingoStats() missing %q in:\n%s", part, result)
		}
	}
}

// TestGetMithrilStatsRendersView verifies the Mithril sync pane renders all
// key fields including progress bars and error highlighting.
func TestGetMithrilStatsRendersView(t *testing.T) {
	originalPromMetrics := promMetrics
	defer func() { promMetrics = originalPromMetrics }()

	promMetrics = &PromMetrics{
		MithrilSyncCompleted:             0,
		MithrilSyncStartedAt:             1700000000,
		MithrilSyncErrorsTotal:           1,
		MithrilSyncDownloadBytes:         BytesInGigabyte,
		MithrilSyncDownloadTotalBytes:    2 * BytesInGigabyte,
		MithrilSyncDownloadPercent:       45.5,
		MithrilSyncDownloadRate:          1048576,
		MithrilSyncSnapshotSize:          3 * BytesInGigabyte,
		MithrilSyncSnapshotEpoch:         500,
		MithrilSyncSnapshotAncillarySize: BytesInGigabyte,
		MithrilSyncSnapshotImmutableFile: 26153,
		MithrilSyncLedgerImportCurrent:   12345,
		MithrilSyncLedgerImportTotal:     18230,
		MithrilSyncLedgerImportPercent:   67.7,
		MithrilSyncLedgerImportStages: map[string]MithrilLedgerImportStage{
			"accounts": {Current: 89720, Total: 89720, Percent: 100},
			"utxo":     {Current: 12345, Total: 18230, Percent: 67.7},
		},
		MithrilSyncLedgerStateSlot:        112986212,
		MithrilSyncImmutableBlocksCopied:  1234,
		MithrilSyncImmutableCopyPerSecond: 56,
		MithrilSyncImmutableCopyPercent:   23.1,
		MithrilSyncImmutableCurrentSlot:   112985271,
		MithrilSyncImmutableTipSlot:       112985271,
		MithrilSyncGapBlocks:              1200,
		MithrilPhaseImmutable:             1,
	}

	result := getMithrilStats()

	expectedParts := []string{
		"Phase",
		"immutable_copy",
		"Started",
		"Snapshot",
		"Epoch",
		"500",
		"Snapshot Meta",
		"26153",
		"Ancillary",
		"Errors",
		"Download",
		"45.5%",
		"Ledger Import",
		"67.7%",
		"accounts",
		"89720",
		"utxo",
		"12345",
		"18230",
		"Ledger Slot",
		"112986212",
		"Immutable",
		"23.1%",
		"1234",
		"Immutable Slot",
		"112985271",
		"Gap Blocks",
		"1200",
	}
	for _, part := range expectedParts {
		if !strings.Contains(result, part) {
			t.Errorf("getMithrilStats() missing %q in:\n%s", part, result)
		}
	}
}

func TestGetMithrilStatsNilMetrics(t *testing.T) {
	originalPromMetrics := promMetrics
	defer func() { promMetrics = originalPromMetrics }()

	promMetrics = nil
	result := getMithrilStats()
	if result != "" {
		t.Errorf("getMithrilStats() with nil metrics = %q, expected empty string", result)
	}
}

// TestIsMithrilSyncActive verifies the active-sync predicate covers all relevant
// fields and respects the completed flag.
func TestIsMithrilSyncActive(t *testing.T) {
	tests := []struct {
		name    string
		metrics *PromMetrics
		want    bool
	}{
		{
			name:    "nil metrics",
			metrics: nil,
			want:    false,
		},
		{
			name:    "completed flag set",
			metrics: &PromMetrics{MithrilSyncCompleted: 1, MithrilSyncDownloadBytes: 1000},
			want:    false,
		},
		{
			name:    "active via download bytes",
			metrics: &PromMetrics{MithrilSyncCompleted: 0, MithrilSyncDownloadBytes: 1000},
			want:    true,
		},
		{
			name:    "active via started timestamp",
			metrics: &PromMetrics{MithrilSyncCompleted: 0, MithrilSyncStartedAt: 1700000000},
			want:    true,
		},
		{
			name:    "active via errors total",
			metrics: &PromMetrics{MithrilSyncCompleted: 0, MithrilSyncErrorsTotal: 1},
			want:    true,
		},
		{
			name:    "active via download total bytes",
			metrics: &PromMetrics{MithrilSyncCompleted: 0, MithrilSyncDownloadTotalBytes: 1000},
			want:    true,
		},
		{
			name:    "active via download percent",
			metrics: &PromMetrics{MithrilSyncCompleted: 0, MithrilSyncDownloadPercent: 1.5},
			want:    true,
		},
		{
			name:    "active via download rate",
			metrics: &PromMetrics{MithrilSyncCompleted: 0, MithrilSyncDownloadRate: 1.5},
			want:    true,
		},
		{
			name:    "active via snapshot size",
			metrics: &PromMetrics{MithrilSyncCompleted: 0, MithrilSyncSnapshotSize: 1000},
			want:    true,
		},
		{
			name:    "active via snapshot epoch",
			metrics: &PromMetrics{MithrilSyncCompleted: 0, MithrilSyncSnapshotEpoch: 500},
			want:    true,
		},
		{
			name:    "active via snapshot ancillary size",
			metrics: &PromMetrics{MithrilSyncCompleted: 0, MithrilSyncSnapshotAncillarySize: 1000},
			want:    true,
		},
		{
			name:    "active via snapshot immutable file",
			metrics: &PromMetrics{MithrilSyncCompleted: 0, MithrilSyncSnapshotImmutableFile: 26153},
			want:    true,
		},
		{
			name:    "active via ledger import current",
			metrics: &PromMetrics{MithrilSyncCompleted: 0, MithrilSyncLedgerImportCurrent: 1000},
			want:    true,
		},
		{
			name:    "active via ledger import total",
			metrics: &PromMetrics{MithrilSyncCompleted: 0, MithrilSyncLedgerImportTotal: 1000},
			want:    true,
		},
		{
			name:    "active via ledger import percent",
			metrics: &PromMetrics{MithrilSyncCompleted: 0, MithrilSyncLedgerImportPercent: 1.5},
			want:    true,
		},
		{
			name: "active via ledger import stage",
			metrics: &PromMetrics{
				MithrilSyncCompleted: 0,
				MithrilSyncLedgerImportStages: map[string]MithrilLedgerImportStage{
					"accounts": {Current: 1},
				},
			},
			want: true,
		},
		{
			name:    "active via ledger state slot",
			metrics: &PromMetrics{MithrilSyncCompleted: 0, MithrilSyncLedgerStateSlot: 112986212},
			want:    true,
		},
		{
			name:    "active via immutable blocks",
			metrics: &PromMetrics{MithrilSyncCompleted: 0, MithrilSyncImmutableBlocksCopied: 1},
			want:    true,
		},
		{
			name:    "active via immutable rate",
			metrics: &PromMetrics{MithrilSyncCompleted: 0, MithrilSyncImmutableCopyPerSecond: 1.5},
			want:    true,
		},
		{
			name:    "active via immutable percent",
			metrics: &PromMetrics{MithrilSyncCompleted: 0, MithrilSyncImmutableCopyPercent: 1.5},
			want:    true,
		},
		{
			name:    "active via immutable current slot",
			metrics: &PromMetrics{MithrilSyncCompleted: 0, MithrilSyncImmutableCurrentSlot: 112985271},
			want:    true,
		},
		{
			name:    "active via immutable tip slot",
			metrics: &PromMetrics{MithrilSyncCompleted: 0, MithrilSyncImmutableTipSlot: 112985271},
			want:    true,
		},
		{
			name:    "active via gap blocks",
			metrics: &PromMetrics{MithrilSyncCompleted: 0, MithrilSyncGapBlocks: 500},
			want:    true,
		},
		{
			name:    "active via bootstrap phase",
			metrics: &PromMetrics{MithrilSyncCompleted: 0, MithrilPhaseBootstrap: 1},
			want:    true,
		},
		{
			name:    "active via ledger phase",
			metrics: &PromMetrics{MithrilSyncCompleted: 0, MithrilPhaseLedger: 1},
			want:    true,
		},
		{
			name:    "active via immutable phase",
			metrics: &PromMetrics{MithrilSyncCompleted: 0, MithrilPhaseImmutable: 1},
			want:    true,
		},
		{
			name:    "active via gap blocks phase",
			metrics: &PromMetrics{MithrilSyncCompleted: 0, MithrilPhaseGapBlocks: 1},
			want:    true,
		},
		{
			name:    "active via backfill phase",
			metrics: &PromMetrics{MithrilSyncCompleted: 0, MithrilPhaseBackfill: 1},
			want:    true,
		},
		{
			name:    "active via post ledger phase",
			metrics: &PromMetrics{MithrilSyncCompleted: 0, MithrilPhasePostLedger: 1},
			want:    true,
		},
		{
			name:    "all zeros not active",
			metrics: &PromMetrics{},
			want:    false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			originalPromMetrics := promMetrics
			defer func() { promMetrics = originalPromMetrics }()
			promMetrics = tt.metrics

			got := isMithrilSyncActive()
			if got != tt.want {
				t.Errorf("isMithrilSyncActive() = %v, expected %v", got, tt.want)
			}
		})
	}
}

// TestUpdateMithrilViewAutoSwitch verifies that the view switches to viewMithril
// when sync is active on a Dingo node, and back to viewDingo on completion.
func TestUpdateMithrilViewAutoSwitch(t *testing.T) {
	originalPromMetrics := promMetrics
	originalMithrilAutoActive := mithrilViewAutoActive.Load()
	originalActive := getActiveSecondaryView()
	originalDetectedBinary, _ := detectedNodeBinary.Load().(string)
	defer func() {
		promMetrics = originalPromMetrics
		mithrilViewAutoActive.Store(originalMithrilAutoActive)
		setActiveSecondaryView(originalActive)
		detectedNodeBinary.Store(originalDetectedBinary)
	}()

	detectedNodeBinary.Store(DINGO_BINARY)
	mithrilViewAutoActive.Store(false)
	setActiveSecondaryView(viewDingo)

	promMetrics = &PromMetrics{
		MithrilSyncCompleted:    0,
		MithrilSyncSnapshotSize: BytesInGigabyte,
	}
	updateMithrilView()
	if getActiveSecondaryView() != viewMithril {
		t.Errorf("expected viewMithril when sync active, got %d", getActiveSecondaryView())
	}
	if !mithrilViewAutoActive.Load() {
		t.Error("expected mithrilViewAutoActive to be true after auto-switch")
	}

	promMetrics = &PromMetrics{MithrilSyncCompleted: 1}
	updateMithrilView()
	if getActiveSecondaryView() != viewDingo {
		t.Errorf("expected viewDingo after sync completion, got %d", getActiveSecondaryView())
	}
	if mithrilViewAutoActive.Load() {
		t.Error("expected mithrilViewAutoActive to be false after completion")
	}
}

// TestGetDingoStatsShowsGovernanceFailures verifies the governance decode
// failures counter appears in the diagnostics pane.
func TestGetDingoStatsShowsGovernanceFailures(t *testing.T) {
	originalPromMetrics := promMetrics
	originalLastDingoSample := lastDingoSample
	originalLastDingoSampleAt := lastDingoSampleAt
	originalLastDingoSampleSrc := lastDingoSampleSrc
	originalLastDingoRateBase := lastDingoRateBase
	originalLastDingoRateBaseAt := lastDingoRateBaseAt
	defer func() {
		promMetrics = originalPromMetrics
		lastDingoSample = originalLastDingoSample
		lastDingoSampleAt = originalLastDingoSampleAt
		lastDingoSampleSrc = originalLastDingoSampleSrc
		lastDingoRateBase = originalLastDingoRateBase
		lastDingoRateBaseAt = originalLastDingoRateBaseAt
	}()

	lastDingoRateBase = nil
	lastDingoRateBaseAt = time.Time{}
	lastDingoSample = nil
	lastDingoSampleAt = time.Time{}
	lastDingoSampleSrc = nil
	promMetrics = &PromMetrics{
		DingoGovernanceDecodeFailures: 7,
	}

	result := getDingoStats()
	if !strings.Contains(result, "Gov Failures") {
		t.Errorf("getDingoStats() missing 'Gov Failures' in:\n%s", result)
	}
	if !strings.Contains(result, "7") {
		t.Errorf("getDingoStats() missing governance count in:\n%s", result)
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
