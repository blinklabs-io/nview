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
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/blinklabs-io/nview/internal/config"
	"github.com/shirou/gopsutil/v3/disk"
)

// useNodeBinary pins the effective node binary and clears the data directory
// cache for the duration of a test.
func useNodeBinary(t *testing.T, binary string) {
	t.Helper()
	previous := detectedNodeBinary.Load()
	detectedNodeBinary.Store(binary)
	resetDataDirCache()
	t.Cleanup(func() {
		if s, ok := previous.(string); ok {
			detectedNodeBinary.Store(s)
		} else {
			detectedNodeBinary.Store("")
		}
		resetDataDirCache()
	})
}

// resetDataDirCache empties the package-level data directory cache so one
// test cannot serve another test's resolved path.
func resetDataDirCache() {
	dataDirCache.Lock()
	dataDirCache.pid = 0
	dataDirCache.createTime = 0
	dataDirCache.path = ""
	dataDirCache.Unlock()
}

// TestResolveDataDirPrefersConfig covers the override: when node.dataDir is
// set in config, resolveDataDir returns it even though the process command
// line names a different --data-dir.
func TestResolveDataDirPrefersConfig(t *testing.T) {
	useNodeBinary(t, DINGO_BINARY)
	proc := startDingoProcessHelper(t, nil, "--data-dir", "/from/cmdline")
	cfg := &config.Config{Node: config.NodeConfig{DataDir: "/from/config"}}

	got := resolveDataDir(context.Background(), cfg, proc)
	if got != "/from/config" {
		t.Fatalf("expected config value, got %q", got)
	}
}

// TestResolveDataDirFromCmdline covers discovery from a real process command
// line with no config override: Dingo with "--data-dir path", Dingo with
// "--data-dir=path", and cardano-node with "--database-path path".
func TestResolveDataDirFromCmdline(t *testing.T) {
	tests := []struct {
		name   string
		binary string
		args   []string
	}{
		{"dingo separate", DINGO_BINARY, []string{"--data-dir", "/data/dingo"}},
		{"dingo equals", DINGO_BINARY, []string{"--data-dir=/data/dingo"}},
		{
			"cardano-node",
			"cardano-node",
			[]string{"--database-path", "/data/dingo"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			useNodeBinary(t, tt.binary)
			proc := startDingoProcessHelper(t, nil, tt.args...)

			got := resolveDataDir(
				context.Background(),
				&config.Config{},
				proc,
			)
			if got != "/data/dingo" {
				t.Fatalf("expected /data/dingo, got %q", got)
			}
		})
	}
}

// TestResolveDataDirResolvesRelativePathAgainstCwd covers a relative
// --data-dir: the result must be joined to the node process's working
// directory, not nview's own. It skips if the platform cannot report the
// helper process's working directory.
func TestResolveDataDirResolvesRelativePathAgainstCwd(t *testing.T) {
	useNodeBinary(t, DINGO_BINARY)
	proc := startDingoProcessHelper(t, nil, "--data-dir", "db")
	cwd, err := proc.CwdWithContext(context.Background())
	if err != nil {
		t.Skipf("cannot read helper cwd: %v", err)
	}

	got := resolveDataDir(context.Background(), &config.Config{}, proc)
	if !strings.HasSuffix(got, "/db") || !strings.HasPrefix(got, cwd) {
		t.Fatalf("expected %s/db, got %q", cwd, got)
	}
}

// TestResolveDataDirEmptyWhenUnknown covers every case where no directory can
// be determined and the result must be "": the data-dir flag is absent, the
// process is nil, and the effective binary (amaru) has no known flag.
func TestResolveDataDirEmptyWhenUnknown(t *testing.T) {
	useNodeBinary(t, DINGO_BINARY)
	withoutFlag := startDingoProcessHelper(t, nil)
	if got := resolveDataDir(
		context.Background(), &config.Config{}, withoutFlag,
	); got != "" {
		t.Fatalf("expected empty path without flag, got %q", got)
	}
	if got := resolveDataDir(
		context.Background(), &config.Config{}, nil,
	); got != "" {
		t.Fatalf("expected empty path for nil process, got %q", got)
	}

	useNodeBinary(t, "amaru")
	proc := startDingoProcessHelper(t, nil, "--data-dir", "/data")
	if got := resolveDataDir(
		context.Background(), &config.Config{}, proc,
	); got != "" {
		t.Fatalf("expected empty path for unsupported binary, got %q", got)
	}
}

// TestResolveDataDirCacheFollowsPID covers cache invalidation: after resolving
// the path for one process, resolving a different PID must return that
// process's path and not the cached one.
func TestResolveDataDirCacheFollowsPID(t *testing.T) {
	useNodeBinary(t, DINGO_BINARY)
	first := startDingoProcessHelper(t, nil, "--data-dir", "/data/one")
	second := startDingoProcessHelper(t, nil, "--data-dir", "/data/two")
	ctx := context.Background()

	if got := resolveDataDir(ctx, &config.Config{}, first); got != "/data/one" {
		t.Fatalf("unexpected first path %q", got)
	}
	if got := resolveDataDir(ctx, &config.Config{}, second); got != "/data/two" {
		t.Fatalf("cache served stale path after PID change: %q", got)
	}
}

// TestDiskUsageReportsFilesystemOfPath covers disk.UsageWithContext on a real directory,
// which must return a non-zero total with used not above total, and on a
// missing path, which must return an error.
func TestDiskUsageReportsFilesystemOfPath(t *testing.T) {
	usage, err := disk.UsageWithContext(context.Background(), t.TempDir())
	if err != nil {
		t.Fatalf("diskUsage failed: %v", err)
	}
	if usage.Total == 0 || usage.Used > usage.Total {
		t.Fatalf("implausible usage: %+v", usage)
	}
	if _, err := disk.UsageWithContext(
		context.Background(), "/nonexistent/nview/path",
	); err == nil {
		t.Fatal("expected error for missing path")
	}
}

// TestDiskSeverity covers the severity thresholds at their boundaries: OK below
// 80%, warn from 80% up to 90%, and critical from 90%.
func TestDiskSeverity(t *testing.T) {
	tests := []struct {
		percent float64
		want    uiSeverity
	}{
		{0, uiSeverityOK},
		{79.9, uiSeverityOK},
		{80, uiSeverityWarn},
		{89.9, uiSeverityWarn},
		{90, uiSeverityCritical},
		{100, uiSeverityCritical},
	}
	for _, tt := range tests {
		if got := diskSeverity(tt.percent); got != tt.want {
			t.Errorf("diskSeverity(%v) = %v, want %v", tt.percent, got, tt.want)
		}
	}
}

// TestDiskResourceTextRendersRows covers the rendered output for a readable
// data directory: the Disk row with a percent and the Used row with used and
// total sizes.
func TestDiskResourceTextRendersRows(t *testing.T) {
	cfg := &config.Config{Node: config.NodeConfig{DataDir: t.TempDir()}}

	got := diskResourceText(context.Background(), cfg, nil)
	for _, want := range []string{"Disk", "%", "Used", " / "} {
		if !strings.Contains(got, want) {
			t.Errorf("expected %q in output, got %q", want, got)
		}
	}
	if strings.Count(got, "\n") != 2 {
		t.Errorf("expected exactly two rows, got %q", got)
	}
}

// TestDiskResourceTextHiddenWhenUnavailable covers the two cases where the
// rows must be omitted entirely: no data directory is known, and the
// configured directory does not exist so disk usage cannot be read.
func TestDiskResourceTextHiddenWhenUnavailable(t *testing.T) {
	useNodeBinary(t, DINGO_BINARY)
	tests := []struct {
		name string
		cfg  *config.Config
	}{
		{"no data dir", &config.Config{}},
		{
			"missing data dir",
			&config.Config{Node: config.NodeConfig{
				DataDir: "/nonexistent/nview/path",
			}},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := diskResourceText(
				context.Background(), tt.cfg, nil,
			); got != "" {
				t.Fatalf("expected no output, got %q", got)
			}
		})
	}
}

// TestGetResourceTextIncludesDiskRows covers the wiring in getResourceText
// end to end: a Dingo process whose command line names a real --data-dir gets
// Disk rows in the Runtime pane, and one without the flag renders the pane
// with no Disk rows and no error.
func TestGetResourceTextIncludesDiskRows(t *testing.T) {
	useNodeBinary(t, DINGO_BINARY)
	originalProc := processMetrics
	originalProm := promMetrics.Load()
	t.Cleanup(func() {
		processMetrics = originalProc
		promMetrics.Store(originalProm)
	})
	promMetrics.Store(&PromMetrics{})

	processMetrics = startDingoProcessHelper(
		t, nil, "--data-dir", t.TempDir(),
	)
	got := getResourceText(context.Background())
	if !strings.Contains(got, "Disk") || !strings.Contains(got, "Used") {
		t.Fatalf("expected Disk rows in pane, got %q", got)
	}
	if !strings.Contains(got, "CPU") {
		t.Fatalf("expected existing rows to remain, got %q", got)
	}

	resetDataDirCache()
	processMetrics = startDingoProcessHelper(t, nil)
	got = getResourceText(context.Background())
	if strings.Contains(got, "Disk") {
		t.Fatalf("expected no Disk rows without a data dir, got %q", got)
	}
	if !strings.Contains(got, "CPU") {
		t.Fatalf("expected existing rows to remain, got %q", got)
	}
}

// TestDataDirFromArgs covers the pure command line parsing, including the
// working directory failure that cannot be provoked on a real process: an
// absolute path never reads the working directory, a relative path is joined to
// it, and a relative path with an unreadable working directory yields "".
func TestDataDirFromArgs(t *testing.T) {
	failCwd := func() (string, error) { return "", errors.New("no cwd") }
	okCwd := func() (string, error) { return "/work", nil }
	tests := []struct {
		name string
		args []string
		cwd  func() (string, error)
		want string
	}{
		{"absolute ignores cwd", []string{"--data-dir", "/abs"}, failCwd, "/abs"},
		{"relative joins cwd", []string{"--data-dir=db"}, okCwd, "/work/db"},
		{"relative cwd error", []string{"--data-dir", "db"}, failCwd, ""},
		{"flag absent", []string{"--other", "x"}, okCwd, ""},
		{"no args", nil, okCwd, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := dataDirFromArgs(tt.args, "--data-dir", tt.cwd); got != tt.want {
				t.Fatalf("got %q, want %q", got, tt.want)
			}
		})
	}
}

// TestResolveDataDirCacheRejectsReusedPID covers a restarted node that reuses
// its PID: a cache entry with the same PID but a different process start time
// must not be served, and the path is re-read from the command line.
func TestResolveDataDirCacheRejectsReusedPID(t *testing.T) {
	useNodeBinary(t, DINGO_BINARY)
	proc := startDingoProcessHelper(t, nil, "--data-dir", "/data/new")
	createTime, err := proc.CreateTimeWithContext(context.Background())
	if err != nil {
		t.Skipf("cannot read helper create time: %v", err)
	}
	dataDirCache.Lock()
	dataDirCache.pid = proc.Pid
	dataDirCache.createTime = createTime - 1
	dataDirCache.path = "/data/old"
	dataDirCache.Unlock()

	got := resolveDataDir(context.Background(), &config.Config{}, proc)
	if got != "/data/new" {
		t.Fatalf("served stale path for reused PID: %q", got)
	}
	dataDirCache.Lock()
	cachedTime := dataDirCache.createTime
	dataDirCache.Unlock()
	if cachedTime != createTime {
		t.Fatalf("cache not refreshed: create time %d, want %d", cachedTime, createTime)
	}
}
