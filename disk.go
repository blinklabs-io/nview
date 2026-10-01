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
	"path/filepath"
	"sync"

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
// set, otherwise the one found on the process command line. It returns "" when
// the directory cannot be determined.
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
	flag, ok := dataDirFlags[getEffectiveNodeBinary()]
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
	path := dataDirFromArgs(args, flag, func() (string, error) {
		return proc.CwdWithContext(ctx)
	})
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

// diskResourceText renders the Disk and Used rows for the Runtime pane. It
// returns "" when the data directory is unknown or its usage is unreadable, so
// a missing disk never breaks the rest of the pane.
func diskResourceText(
	ctx context.Context,
	cfg *config.Config,
	proc *process.Process,
) string {
	dataDir := resolveDataDir(ctx, cfg, proc)
	if dataDir == "" {
		return ""
	}
	usage, err := disk.UsageWithContext(ctx, dataDir)
	if err != nil {
		return ""
	}
	severity := diskSeverity(usage.UsedPercent)
	return fmt.Sprintf(
		" %s %s   %s\n %s\n",
		uiLabel("Disk"),
		uiSeverityValue(fmt.Sprintf("%.2f%%", usage.UsedPercent), severity),
		uiProgressBar(usage.UsedPercent, 14, severity),
		uiKV("Used", uiValue(
			formatMemoryBytes(usage.Used)+" / "+formatMemoryBytes(usage.Total),
		)),
	)
}
