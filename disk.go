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
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"sync/atomic"

	"github.com/blinklabs-io/nview/internal/config"
	"github.com/shirou/gopsutil/v3/disk"
	"github.com/shirou/gopsutil/v3/process"
)

// dataDirFlags are the command line flags, per node binary, that name the
// node's data directory.
var dataDirFlags = map[string]string{
	DINGO_BINARY:   "--data-dir",
	"cardano-node": "--database-path",
}

// dataDirCache remembers the data directory resolved for a process so the
// command line is not re-read on every refresh. It is keyed on PID and process
// start time: a restarted node can reuse its PID (always so for a containerized
// PID 1) but not its start time.
var dataDirCache struct {
	sync.Mutex
	pid        int32
	createTime int64
	path       string
}

// resolveDataDir returns the node's data directory: the configured value if
// set, otherwise the one found from the node process. It returns "" when the
// directory cannot be determined.
func resolveDataDir(
	ctx context.Context,
	cfg *config.Config,
	proc *process.Process,
) string {
	if cfg.Node.DataDir != "" {
		return cfg.Node.DataDir
	}
	if proc == nil || proc.Pid <= 0 {
		return ""
	}
	binary := getEffectiveNodeBinary()
	flag, ok := dataDirFlags[binary]
	if !ok {
		return ""
	}
	createTime, err := proc.CreateTimeWithContext(ctx)
	if err != nil {
		return ""
	}
	dataDirCache.Lock()
	defer dataDirCache.Unlock()
	if dataDirCache.pid == proc.Pid &&
		dataDirCache.createTime == createTime &&
		dataDirCache.path != "" {
		return dataDirCache.path
	}
	args, err := proc.CmdlineSliceWithContext(ctx)
	if err != nil {
		return ""
	}
	cwd := func() (string, error) { return proc.CwdWithContext(ctx) }
	var path string
	if binary == DINGO_BINARY {
		env, _ := proc.EnvironWithContext(ctx)
		path = dingoDataDir(dingoProcessInfo{args: args, env: env, cwd: cwd})
	} else {
		path = dataDirFromArgs(args, flag, cwd)
	}
	if path == "" {
		return ""
	}
	dataDirCache.pid = proc.Pid
	dataDirCache.createTime = createTime
	dataDirCache.path = path
	return path
}

// dataDirFromArgs extracts the value of flag from a node command line. A
// relative value is resolved against the node's working directory, because
// nview's own directory is the wrong base. It returns "" when the flag is
// absent or the working directory is needed but unreadable.
func dataDirFromArgs(
	args []string,
	flag string,
	cwd func() (string, error),
) string {
	path := valueFromArgs(args, flag)
	if path == "" || filepath.IsAbs(path) {
		return path
	}
	base, err := cwd()
	if err != nil {
		return ""
	}
	return filepath.Join(base, path)
}

func diskSeverity(percent float64) uiSeverity {
	switch {
	case percent >= 90:
		return uiSeverityCritical
	case percent >= 80:
		return uiSeverityWarn
	default:
		return uiSeverityOK
	}
}

// diskSnapshot is the most recent usage reading for the node's data directory.
// The Dingo console renderers take no process or context, so they read the
// snapshot that refreshDiskSnapshot stored for the current refresh.
type diskSnapshot struct {
	Used    uint64
	Total   uint64
	Percent float64
}

var latestDisk atomic.Pointer[diskSnapshot]

// rebaseToRoot returns path as seen through root when that location exists,
// and path unchanged otherwise.
func rebaseToRoot(root, path string) string {
	candidate := filepath.Join(root, path)
	if _, err := os.Stat(candidate); err != nil {
		return path
	}
	return candidate
}

// nodeRootedPath maps a path taken from the node's command line into nview's
// view of the node's filesystem. A node in another mount namespace, such as a
// container sharing the PID namespace, reports paths that are only valid
// under its own root, which Linux exposes at /proc/<pid>/root.
func nodeRootedPath(pid int32, path string) string {
	if runtime.GOOS != "linux" {
		return path
	}
	return rebaseToRoot(fmt.Sprintf("/proc/%d/root", pid), path)
}

// refreshDiskSnapshot measures the filesystem holding the node's data
// directory and stores the result. It stores and returns nil when the
// directory is unknown or its usage is unreadable. A configured directory is
// used as given because the operator wrote it for nview's own namespace.
func refreshDiskSnapshot(
	ctx context.Context,
	cfg *config.Config,
	proc *process.Process,
) *diskSnapshot {
	dataDir := resolveDataDir(ctx, cfg, proc)
	if dataDir != "" && cfg.Node.DataDir == "" && proc != nil {
		dataDir = nodeRootedPath(proc.Pid, dataDir)
	}
	var snap *diskSnapshot
	if dataDir != "" {
		if usage, err := disk.UsageWithContext(ctx, dataDir); err == nil {
			snap = &diskSnapshot{
				Used:    usage.Used,
				Total:   usage.Total,
				Percent: usage.UsedPercent,
			}
		}
	}
	latestDisk.Store(snap)
	return snap
}

func diskPercentText(snap *diskSnapshot) string {
	return uiSeverityValue(
		fmt.Sprintf("%.2f%%", snap.Percent),
		diskSeverity(snap.Percent),
	)
}

func diskUsedText(snap *diskSnapshot) string {
	return formatMemoryBytes(snap.Used) + " / " + formatMemoryBytes(snap.Total)
}

// diskResourceText renders the Disk and Used rows for the Runtime pane. It
// returns "" when the data directory is unknown or its usage is unreadable, so
// a missing disk never breaks the rest of the pane.
func diskResourceText(
	ctx context.Context,
	cfg *config.Config,
	proc *process.Process,
) string {
	snap := refreshDiskSnapshot(ctx, cfg, proc)
	if snap == nil {
		return ""
	}
	return fmt.Sprintf(
		" %s %s   %s\n %s\n",
		uiLabel("Disk"),
		diskPercentText(snap),
		uiProgressBar(snap.Percent, 14, diskSeverity(snap.Percent)),
		uiKV("Used", uiValue(diskUsedText(snap))),
	)
}

// dingoDiskRows returns the Disk row for the Dingo console panels, or nothing
// when the latest refresh found no readable data directory.
func dingoDiskRows(innerWidth int) []string {
	snap := latestDisk.Load()
	if snap == nil {
		return nil
	}
	severity := diskSeverity(snap.Percent)
	return []string{dingoMetricRowColumns(
		innerWidth,
		2,
		dingoMetricStyledSpan(
			"Disk",
			diskPercentText(snap)+" "+
				uiProgressBar(snap.Percent, 10, severity)+" "+
				uiValue(diskUsedText(snap)),
			severity,
			2,
		),
	)}
}
