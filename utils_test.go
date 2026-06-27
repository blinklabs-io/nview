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
	"os"
	"path/filepath"
	"testing"

	"github.com/blinklabs-io/nview/internal/config"
)

func TestParseNodeVersionOutput(t *testing.T) {
	tests := []struct {
		name         string
		binary       string
		output       string
		wantVersion  string
		wantRevision string
		wantErr      bool
	}{
		{
			name:         "dingo devel",
			binary:       DINGO_BINARY,
			output:       "devel (commit 80ae952)",
			wantVersion:  "devel",
			wantRevision: "80ae952",
		},
		{
			name:         "dingo release truncates commit",
			binary:       DINGO_BINARY,
			output:       "v0.17.0 (commit 1f54020abcdef)",
			wantVersion:  "v0.17.0",
			wantRevision: "1f54020a",
		},
		{
			name:         "cardano node multiline",
			binary:       CARDANO_BINARY,
			output:       "cardano-node 10.5.1 - linux-x86_64 - ghc-9.6.7\ngit rev 1234567890abcdef",
			wantVersion:  "10.5.1",
			wantRevision: "12345678",
		},
		{
			name:    "invalid output",
			binary:  DINGO_BINARY,
			output:  "unexpected",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotVersion, gotRevision, err := parseNodeVersionOutput(
				tt.binary,
				tt.output,
			)
			if tt.wantErr {
				if err == nil {
					t.Fatal("parseNodeVersionOutput() error = nil, expected error")
				}
				return
			}
			if err != nil {
				t.Fatalf("parseNodeVersionOutput() error = %v", err)
			}
			if gotVersion != tt.wantVersion {
				t.Errorf(
					"parseNodeVersionOutput() version = %q, expected %q",
					gotVersion,
					tt.wantVersion,
				)
			}
			if gotRevision != tt.wantRevision {
				t.Errorf(
					"parseNodeVersionOutput() revision = %q, expected %q",
					gotRevision,
					tt.wantRevision,
				)
			}
		})
	}
}

func TestGetNodeVersionDoesNotCacheFailures(t *testing.T) {
	cfg := config.GetConfig()
	originalBinary := cfg.Node.Binary
	originalDetectedBinary, _ := detectedNodeBinary.Load().(string)
	nodeVersionCacheMu.Lock()
	originalCache := nodeVersionCache
	nodeVersionCache = map[string]nodeVersionInfo{}
	nodeVersionCacheMu.Unlock()
	defer func() {
		cfg.Node.Binary = originalBinary
		detectedNodeBinary.Store(originalDetectedBinary)
		nodeVersionCacheMu.Lock()
		nodeVersionCache = originalCache
		nodeVersionCacheMu.Unlock()
	}()

	binary := filepath.Join(t.TempDir(), "node-version")
	if err := os.WriteFile(binary, []byte("#!/bin/sh\nexit 1\n"), 0o755); err != nil {
		t.Fatalf("failed to write failing version helper: %v", err)
	}
	cfg.Node.Binary = binary
	detectedNodeBinary.Store(binary)

	if _, _, err := getNodeVersion(); err == nil {
		t.Fatal("getNodeVersion() first call succeeded, expected failure")
	}
	nodeVersionCacheMu.Lock()
	_, cachedFailure := nodeVersionCache[binary]
	nodeVersionCacheMu.Unlock()
	if cachedFailure {
		t.Fatal("getNodeVersion() cached a failed lookup")
	}

	successScript := `#!/bin/sh
cat <<'EOF'
cardano-node 10.1.0 - linux-x86_64 - ghc-9.6
git rev abcdef123456
EOF
`
	if err := os.WriteFile(binary, []byte(successScript), 0o755); err != nil {
		t.Fatalf("failed to write successful version helper: %v", err)
	}

	version, revision, err := getNodeVersion()
	if err != nil {
		t.Fatalf("getNodeVersion() retry failed: %v", err)
	}
	if version != "10.1.0" || revision != "abcdef12" {
		t.Fatalf("getNodeVersion() = %q, %q; expected 10.1.0, abcdef12", version, revision)
	}
}
