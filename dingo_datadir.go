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
	"os"
	"path/filepath"

	"gopkg.in/yaml.v2"
)

const (
	dingoDataDirFlag    = "--data-dir"
	dingoConfigFlag     = "--config"
	dingoDataDirEnv     = "CARDANO_DATABASE_PATH"
	dingoDefaultDataDir = ".dingo"
)

// dingoSystemConfig is the last config file Dingo looks for. It is a variable
// so tests can point it at a temporary file.
var dingoSystemConfig = "/etc/dingo/dingo.yaml"

// userHomeDir is a variable so tests can isolate the home directory.
var userHomeDir = os.UserHomeDir

// dingoProcessInfo is what nview can read about a running Dingo process.
// Relative paths are relative to the process's working directory, not nview's.
// env is empty where the OS does not expose another process's environment,
// which includes macOS, so the environment sources are skipped there.
type dingoProcessInfo struct {
	args []string
	env  []string
	cwd  func() (string, error)
}

// dingoDataDir returns the data directory a Dingo process is using. It applies
// Dingo's own precedence: --data-dir, then CARDANO_DATABASE_PATH, then
// databasePath in the config file, then the built-in default. It returns ""
// when the winning value is relative and the working directory is unreadable.
func dingoDataDir(p dingoProcessInfo) string {
	path := valueFromArgs(p.args, dingoDataDirFlag)
	if path == "" {
		path = valueFromEnv(p.env, dingoDataDirEnv)
	}
	if path == "" {
		path = dingoConfigDatabasePath(p)
	}
	if path == "" {
		path = dingoDefaultDataDir
	}
	return absoluteFromCwd(path, p.cwd)
}

// absoluteFromCwd resolves path against the process working directory when it
// is relative. It returns "" if that directory cannot be read.
func absoluteFromCwd(path string, cwd func() (string, error)) string {
	if path == "" || filepath.IsAbs(path) {
		return path
	}
	base, err := cwd()
	if err != nil {
		return ""
	}
	return filepath.Join(base, path)
}

// dingoConfigDatabasePath reads databasePath from the config file Dingo would
// load. Any problem finding or parsing the file yields "", so the caller falls
// back to the default, as for a Dingo with no config.
func dingoConfigDatabasePath(p dingoProcessInfo) string {
	file := dingoConfigFile(p)
	if file == "" {
		return ""
	}
	buf, err := os.ReadFile(file)
	if err != nil {
		return ""
	}
	var parsed struct {
		DatabasePath string `yaml:"databasePath"`
	}
	if err := yaml.Unmarshal(buf, &parsed); err != nil {
		return ""
	}
	return parsed.DatabasePath
}

// dingoConfigFile mirrors Dingo's config discovery: an explicit --config wins
// outright, then ~/.dingo/dingo.yaml, then the system file. The home directory
// is the one in Dingo's environment, because Dingo may run as another user.
func dingoConfigFile(p dingoProcessInfo) string {
	if file := valueFromArgs(p.args, dingoConfigFlag); file != "" {
		return absoluteFromCwd(file, p.cwd)
	}
	home := valueFromEnv(p.env, "HOME")
	if home == "" {
		home, _ = userHomeDir()
	}
	if home != "" {
		userFile := filepath.Join(home, ".dingo", "dingo.yaml")
		if _, err := os.Stat(userFile); err == nil {
			return userFile
		}
	}
	if _, err := os.Stat(dingoSystemConfig); err == nil {
		return dingoSystemConfig
	}
	return ""
}
