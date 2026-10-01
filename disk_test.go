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
	"os"
	"path/filepath"
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
	originalSystemConfig := dingoSystemConfig
	originalHomeDir := userHomeDir
	dingoSystemConfig = filepath.Join(t.TempDir(), "absent.yaml")
	emptyHome := t.TempDir()
	userHomeDir = func() (string, error) { return emptyHome, nil }
	t.Cleanup(func() {
		dingoSystemConfig = originalSystemConfig
		userHomeDir = originalHomeDir
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
// isolatedHome returns an environment that gives the helper process an empty
// home directory, so a developer's own ~/.dingo/dingo.yaml cannot leak in.
func isolatedHome(t *testing.T) []string {
	t.Helper()
	return []string{"HOME=" + t.TempDir()}
}

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
	proc := startDingoProcessHelper(t, isolatedHome(t), "--data-dir", "/from/cmdline")
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
			proc := startDingoProcessHelper(t, isolatedHome(t), tt.args...)

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
	proc := startDingoProcessHelper(t, isolatedHome(t), "--data-dir", "db")
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
// be determined and the result must be "": the process is nil, the cardano-node
// database-path flag is absent, and the effective binary (amaru) has no known
// source.
func TestResolveDataDirEmptyWhenUnknown(t *testing.T) {
	useNodeBinary(t, "cardano-node")
	withoutFlag := startDingoProcessHelper(t, isolatedHome(t))
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
	proc := startDingoProcessHelper(t, isolatedHome(t), "--data-dir", "/data")
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
	first := startDingoProcessHelper(t, isolatedHome(t), "--data-dir", "/data/one")
	second := startDingoProcessHelper(t, isolatedHome(t), "--data-dir", "/data/two")
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
	processMetrics = startDingoProcessHelper(t, isolatedHome(t))
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
	proc := startDingoProcessHelper(t, isolatedHome(t), "--data-dir", "/data/new")
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

// useDingoDashboard prepares the globals refreshDashboardText reads for a
// Dingo node whose command line names dataDir, and restores them afterwards.
func useDingoDashboard(t *testing.T, dataDir string) {
	t.Helper()
	useNodeBinary(t, DINGO_BINARY)
	originalProc := processMetrics
	originalProm := promMetrics.Load()
	originalConsole := dingoConsoleText
	originalDisk := latestDisk.Load()
	t.Cleanup(func() {
		processMetrics = originalProc
		promMetrics.Store(originalProm)
		dingoConsoleText = originalConsole
		latestDisk.Store(originalDisk)
	})
	promMetrics.Store(&PromMetrics{})
	args := []string{}
	if dataDir != "" {
		args = append(args, "--data-dir", dataDir)
	}
	processMetrics = startDingoProcessHelper(t, isolatedHome(t), args...)
	dingoConsoleText = ""
}

// TestRefreshDashboardTextShowsDiskForDingo covers the Dingo entry point: the
// dashboard refresh for a Dingo node must put the Disk row in the console text,
// and must leave it out when the node names no data directory.
func TestRefreshDashboardTextShowsDiskForDingo(t *testing.T) {
	useDingoDashboard(t, t.TempDir())
	refreshDashboardText(context.Background())
	if !strings.Contains(dingoConsoleText, "Disk") ||
		!strings.Contains(dingoConsoleText, "%") {
		t.Fatalf("expected Disk row in Dingo console, got:\n%s", dingoConsoleText)
	}

	useDingoDashboard(t, "")
	refreshDashboardText(context.Background())
	if strings.Contains(dingoConsoleText, "Disk") {
		t.Fatalf("expected no Disk row without a data dir, got:\n%s", dingoConsoleText)
	}
}

// TestDingoCompactPagesShowDisk covers the compact Dingo renderers: the
// dashboard and operations pages carry the Disk row after a refresh, and the
// chain and peer pages are unchanged.
func TestDingoCompactPagesShowDisk(t *testing.T) {
	useDingoDashboard(t, t.TempDir())
	original := compactDingoPage.Load()
	t.Cleanup(func() { compactDingoPage.Store(original) })
	refreshDiskSnapshot(context.Background(), &config.Config{}, processMetrics)

	for page, want := range map[int32]bool{0: true, 1: false, 2: false, 3: true} {
		compactDingoPage.Store(page)
		got := getDingoConsoleCompactText(&PromMetrics{}, 80)
		if strings.Contains(got, "Disk") != want {
			t.Errorf("compact page %d: Disk present = %v, want %v:\n%s",
				page, !want, want, got)
		}
	}

	latestDisk.Store(nil)
	for _, page := range []int32{0, 3} {
		compactDingoPage.Store(page)
		if got := getDingoConsoleCompactText(&PromMetrics{}, 80); strings.Contains(got, "Disk") {
			t.Errorf("compact page %d shows Disk without a snapshot:\n%s", page, got)
		}
	}
}

// TestRebaseToRoot covers mapping a node's path into its mount namespace: a
// path that exists under the node's root is read through that root, and one
// that does not exist there is not found, so the caller never falls back to
// nview's own filesystem.
func TestRebaseToRoot(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "data", "db"), 0o750); err != nil {
		t.Fatal(err)
	}
	ownCopy := t.TempDir()

	if got := rebaseToRoot(root, "/data/db"); got != filepath.Join(root, "data", "db") {
		t.Fatalf("expected path under node root, got %q", got)
	}
	if got := rebaseToRoot(root, "/elsewhere"); got != "" {
		t.Fatalf("expected no path when absent under root, got %q", got)
	}
	if got := rebaseToRoot(root, ownCopy); got != "" {
		t.Fatalf("a path that exists only in nview's namespace must not be used, got %q", got)
	}
}

// TestRebaseToRootUnreadableRoot covers a node root that nview may not read,
// such as /proc/<pid>/root of another user's process without CAP_SYS_PTRACE: the
// result is "" even though the same path exists in nview's own namespace.
func TestRebaseToRootUnreadableRoot(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root bypasses directory permissions")
	}
	root := t.TempDir()
	own := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, own), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(root, 0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(root, 0o750) })

	if got := rebaseToRoot(root, own); got != "" {
		t.Fatalf("expected no path for an unreadable root, got %q", got)
	}
}

// TestNodeRootedPathWithoutProc covers systems without /proc: the path is used
// as given.
func TestNodeRootedPathWithoutProc(t *testing.T) {
	if procRoot(1) != "" {
		t.Skip("this system exposes /proc/<pid>/root")
	}
	if got := nodeRootedPath(1, "/data/db"); got != "/data/db" {
		t.Fatalf("expected path unchanged, got %q", got)
	}
}

// TestDingoSystemsBandShowsDisk covers the full-layout Dingo renderer: the
// systems band carries the Disk row after a refresh and omits it when the
// latest refresh found no readable data directory.
func TestDingoSystemsBandShowsDisk(t *testing.T) {
	useDingoDashboard(t, t.TempDir())
	refreshDiskSnapshot(context.Background(), &config.Config{}, processMetrics)
	got := dingoConsoleSystemsBand(&PromMetrics{}, 120)
	if !strings.Contains(got, "Disk") {
		t.Fatalf("expected Disk row in systems band:\n%s", got)
	}
	dbAt := strings.Index(got, "DB")
	diskAt := strings.Index(got, "Disk")
	cacheAt := strings.Index(got, "UTxO Cache")
	if dbAt < 0 || cacheAt < 0 || !(dbAt < diskAt && diskAt < cacheAt) {
		t.Fatalf("expected Disk row between the DB and cache rows:\n%s", got)
	}
	if !strings.ContainsAny(got[diskAt:cacheAt], "█░") {
		t.Fatalf("expected a progress bar on the Disk row:\n%s", got)
	}

	latestDisk.Store(nil)
	if got := dingoConsoleSystemsBand(&PromMetrics{}, 120); strings.Contains(got, "Disk") {
		t.Fatalf("expected no Disk row without a snapshot:\n%s", got)
	}
}
