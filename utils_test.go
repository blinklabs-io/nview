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

import "testing"

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
