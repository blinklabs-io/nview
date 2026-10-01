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
	"testing"

	"github.com/blinklabs-io/nview/internal/config"
)

func writeDingoConfig(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func dingoInfo(cwd string, env []string, args ...string) dingoProcessInfo {
	return dingoProcessInfo{
		args: args,
		env:  env,
		cwd:  func() (string, error) { return cwd, nil },
	}
}

// TestDingoDataDirPrecedence covers Dingo's own order of sources: the flag
// beats CARDANO_DATABASE_PATH, which beats databasePath in the config file,
// which beats the .dingo default. Each case removes the winner above it.
func TestDingoDataDirPrecedence(t *testing.T) {
	useNodeBinary(t, DINGO_BINARY)
	home := t.TempDir()
	writeDingoConfig(t, filepath.Join(home, ".dingo", "dingo.yaml"),
		"databasePath: /from/config\n")
	homeEnv := "HOME=" + home

	tests := []struct {
		name string
		env  []string
		args []string
		want string
	}{
		{
			"flag wins",
			[]string{homeEnv, "CARDANO_DATABASE_PATH=/from/env"},
			[]string{"--data-dir", "/from/flag"},
			"/from/flag",
		},
		{
			"env beats config",
			[]string{homeEnv, "CARDANO_DATABASE_PATH=/from/env"},
			nil,
			"/from/env",
		},
		{"config beats default", []string{homeEnv}, nil, "/from/config"},
		{"default", []string{"HOME=" + t.TempDir()}, nil, "/work/.dingo"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := dingoDataDir(dingoInfo("/work", tt.env, tt.args...))
			if got != tt.want {
				t.Fatalf("got %q, want %q", got, tt.want)
			}
		})
	}
}

// TestDingoDataDirRelativePaths covers relative values from each source: they
// resolve against the Dingo process's working directory.
func TestDingoDataDirRelativePaths(t *testing.T) {
	useNodeBinary(t, DINGO_BINARY)
	home := t.TempDir()
	writeDingoConfig(t, filepath.Join(home, ".dingo", "dingo.yaml"),
		"databasePath: cfgdb\n")

	if got := dingoDataDir(dingoInfo("/work", []string{"HOME=" + home})); got != "/work/cfgdb" {
		t.Errorf("config relative: got %q", got)
	}
	env := []string{"HOME=" + t.TempDir(), "CARDANO_DATABASE_PATH=envdb"}
	if got := dingoDataDir(dingoInfo("/work", env)); got != "/work/envdb" {
		t.Errorf("env relative: got %q", got)
	}
}

// TestDingoDataDirConfigFileDiscovery covers where the config file comes from:
// an explicit --config wins outright over the home file, a relative --config
// resolves against the working directory, the home file beats the system file,
// and the system file is used when no home file exists.
func TestDingoDataDirConfigFileDiscovery(t *testing.T) {
	useNodeBinary(t, DINGO_BINARY)
	home := t.TempDir()
	writeDingoConfig(t, filepath.Join(home, ".dingo", "dingo.yaml"),
		"databasePath: /from/home\n")
	explicit := filepath.Join(t.TempDir(), "explicit.yaml")
	writeDingoConfig(t, explicit, "databasePath: /from/explicit\n")
	writeDingoConfig(t, dingoSystemConfig, "databasePath: /from/system\n")
	homeEnv := []string{"HOME=" + home}

	if got := dingoDataDir(dingoInfo("/work", homeEnv, "--config", explicit)); got != "/from/explicit" {
		t.Errorf("explicit config: got %q", got)
	}
	if got := dingoDataDir(dingoInfo(
		filepath.Dir(explicit), homeEnv, "--config=explicit.yaml",
	)); got != "/from/explicit" {
		t.Errorf("relative --config: got %q", got)
	}
	if got := dingoDataDir(dingoInfo("/work", homeEnv)); got != "/from/home" {
		t.Errorf("home config: got %q", got)
	}
	if got := dingoDataDir(dingoInfo(
		"/work", []string{"HOME=" + t.TempDir()},
	)); got != "/from/system" {
		t.Errorf("system config: got %q", got)
	}
}

// TestDingoDataDirConfigProblemsFallThrough covers a config file that cannot
// supply a value: a missing explicit file, invalid YAML, and a file without
// databasePath all fall back to the default instead of failing.
func TestDingoDataDirConfigProblemsFallThrough(t *testing.T) {
	useNodeBinary(t, DINGO_BINARY)
	dir := t.TempDir()
	invalid := filepath.Join(dir, "invalid.yaml")
	writeDingoConfig(t, invalid, "databasePath: [unterminated\n")
	empty := filepath.Join(dir, "empty.yaml")
	writeDingoConfig(t, empty, "network: preview\n")
	env := []string{"HOME=" + t.TempDir()}

	for name, file := range map[string]string{
		"missing":     filepath.Join(dir, "absent.yaml"),
		"invalid":     invalid,
		"no database": empty,
	} {
		t.Run(name, func(t *testing.T) {
			got := dingoDataDir(dingoInfo("/work", env, "--config", file))
			if got != "/work/.dingo" {
				t.Fatalf("got %q, want /work/.dingo", got)
			}
		})
	}
}

// TestDingoDataDirUnreadableWorkingDir covers a relative winner with a working
// directory that cannot be read: the result is "" and a lower-precedence source
// is not used in its place.
func TestDingoDataDirUnreadableWorkingDir(t *testing.T) {
	useNodeBinary(t, DINGO_BINARY)
	p := dingoProcessInfo{
		args: []string{"--data-dir", "db"},
		env:  []string{"HOME=" + t.TempDir(), "CARDANO_DATABASE_PATH=/abs"},
		cwd:  func() (string, error) { return "", errors.New("no cwd") },
	}
	if got := dingoDataDir(p); got != "" {
		t.Fatalf("expected empty path, got %q", got)
	}
}

// TestResolveDataDirDingoSourcesOnRealProcess covers the new sources end to end
// on a real process: with no flag, the environment variable (where the OS lets
// nview read it) and then the default are found, and cardano-node never gets
// the Dingo-only fallbacks.
func TestResolveDataDirDingoSourcesOnRealProcess(t *testing.T) {
	useNodeBinary(t, DINGO_BINARY)
	ctx := context.Background()
	cfg := &config.Config{}

	withEnv := startDingoProcessHelper(t, []string{
		"HOME=" + t.TempDir(), "CARDANO_DATABASE_PATH=/data/from-env",
	})
	if env, err := withEnv.EnvironWithContext(ctx); err != nil || len(env) == 0 {
		t.Logf("process environment unreadable on this OS (%v); skipping env source", err)
	} else if got := resolveDataDir(ctx, cfg, withEnv); got != "/data/from-env" {
		t.Errorf("env var: got %q", got)
	}

	resetDataDirCache()
	bare := startDingoProcessHelper(t, isolatedHome(t))
	cwd, err := bare.CwdWithContext(ctx)
	if err != nil {
		t.Skipf("cannot read helper cwd: %v", err)
	}
	if got := resolveDataDir(ctx, cfg, bare); got != filepath.Join(cwd, ".dingo") {
		t.Errorf("default: got %q", got)
	}

	useNodeBinary(t, "cardano-node")
	if got := resolveDataDir(ctx, cfg, bare); got != "" {
		t.Errorf("cardano-node must not use Dingo fallbacks, got %q", got)
	}
}

// TestDingoDataDirHomeFallsBackToNviewHome covers a process whose environment
// cannot be read: the config file is then looked up under nview's own home.
func TestDingoDataDirHomeFallsBackToNviewHome(t *testing.T) {
	useNodeBinary(t, DINGO_BINARY)
	home := t.TempDir()
	writeDingoConfig(t, filepath.Join(home, ".dingo", "dingo.yaml"),
		"databasePath: /from/nview-home\n")
	original := userHomeDir
	userHomeDir = func() (string, error) { return home, nil }
	t.Cleanup(func() { userHomeDir = original })

	if got := dingoDataDir(dingoInfo("/work", nil)); got != "/from/nview-home" {
		t.Fatalf("got %q", got)
	}
}
