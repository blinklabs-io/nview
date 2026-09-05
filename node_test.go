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
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func writeNodeConfigFixture(t *testing.T, enableP2P bool) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.json")
	content := `{"EnableP2P": false}`
	if enableP2P {
		content = `{"EnableP2P": true}`
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("failed to write fixture: %v", err)
	}
	return path
}

// --config is the first argument on the command line; its value should
// still be found and used.
func TestP2PFromNodeConfigCmdlineLeadingConfig(t *testing.T) {
	path := writeNodeConfigFixture(t, true)
	cmd := "--config " + path + " --extra flag"
	got, err := p2pFromNodeConfigCmdline(cmd, false)
	if err != nil {
		t.Fatalf("p2pFromNodeConfigCmdline() error = %v, want nil", err)
	}
	if got != true {
		t.Fatalf("p2pFromNodeConfigCmdline() = %v, want true", got)
	}
}

// --config sits between other arguments, the common real-world case.
func TestP2PFromNodeConfigCmdlineMiddleConfig(t *testing.T) {
	path := writeNodeConfigFixture(t, true)
	cmd := "cardano-node run --config " + path + " --other flag"
	got, err := p2pFromNodeConfigCmdline(cmd, false)
	if err != nil {
		t.Fatalf("p2pFromNodeConfigCmdline() error = %v, want nil", err)
	}
	if got != true {
		t.Fatalf("p2pFromNodeConfigCmdline() = %v, want true", got)
	}
}

// --config is the last argument with no value after it. This is the exact
// input that used to panic (issue #500); it must now return
// errMissingConfigValue and leave the current P2P value untouched instead.
func TestP2PFromNodeConfigCmdlineTrailingConfigDoesNotPanic(t *testing.T) {
	cmd := "cardano-node run --other flag --config"
	got, err := p2pFromNodeConfigCmdline(cmd, true)
	if !errors.Is(err, errMissingConfigValue) {
		t.Fatalf("p2pFromNodeConfigCmdline() error = %v, want errMissingConfigValue", err)
	}
	if got != true {
		t.Fatalf("p2pFromNodeConfigCmdline() = %v, want unchanged current value true", got)
	}
}

// --config appears twice, pointing at two different config files. The
// second (last) occurrence should win.
func TestP2PFromNodeConfigCmdlineRepeatedConfigUsesLast(t *testing.T) {
	firstPath := writeNodeConfigFixture(t, false)
	lastPath := writeNodeConfigFixture(t, true)
	cmd := "--config " + firstPath + " --config " + lastPath
	got, err := p2pFromNodeConfigCmdline(cmd, false)
	if err != nil {
		t.Fatalf("p2pFromNodeConfigCmdline() error = %v, want nil", err)
	}
	if got != true {
		t.Fatalf("p2pFromNodeConfigCmdline() = %v, want true from last --config", got)
	}
}

// --config appears twice: the first occurrence is valid, but the second
// (last) is missing its value. The earlier valid parse must not leak
// through — the last occurrence is authoritative, so the result should
// fall back to the incoming current value, not the first --config's file.
func TestP2PFromNodeConfigCmdlineRepeatedConfigLastMissingValue(t *testing.T) {
	firstPath := writeNodeConfigFixture(t, true)
	cmd := "--config " + firstPath + " --config"
	got, err := p2pFromNodeConfigCmdline(cmd, false)
	if !errors.Is(err, errMissingConfigValue) {
		t.Fatalf("p2pFromNodeConfigCmdline() error = %v, want errMissingConfigValue", err)
	}
	if got != false {
		t.Fatalf("p2pFromNodeConfigCmdline() = %v, want incoming current value false", got)
	}
}

// --config appears twice: the first occurrence is valid, but the second
// (last) names a file that does not exist. As above, the last occurrence's
// failure must win over the earlier valid parse.
func TestP2PFromNodeConfigCmdlineRepeatedConfigLastUnreadable(t *testing.T) {
	firstPath := writeNodeConfigFixture(t, true)
	cmd := "--config " + firstPath + " --config /nonexistent/path/config.json"
	got, err := p2pFromNodeConfigCmdline(cmd, false)
	if err == nil {
		t.Fatal("p2pFromNodeConfigCmdline() error = nil, want a read error for the missing file")
	}
	if got != false {
		t.Fatalf("p2pFromNodeConfigCmdline() = %v, want incoming current value false", got)
	}
}

// --config is followed by an empty string instead of a real path (not
// missing, just blank). Reading that "file" fails, so the current P2P
// value should be left unchanged rather than being reset.
func TestP2PFromNodeConfigCmdlineEmptyValueLeavesCurrentUnchanged(t *testing.T) {
	cmd := "cardano-node run --config  --other flag"
	got, err := p2pFromNodeConfigCmdline(cmd, true)
	if err == nil {
		t.Fatal("p2pFromNodeConfigCmdline() error = nil, want a read error for the empty path")
	}
	if got != true {
		t.Fatalf("p2pFromNodeConfigCmdline() = %v, want unchanged current value true", got)
	}
}
