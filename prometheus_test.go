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
	"testing"

	"github.com/blinklabs-io/nview/internal/config"
)

func TestDetectNodeType(t *testing.T) {
	tests := []struct {
		name             string
		metrics          *PromMetrics
		initialBinary    string
		initialNodeName  string
		expectedBinary   string
		expectedNodeName string
	}{
		{
			name: "Dingo detection with default node name",
			metrics: &PromMetrics{
				GoMemAlloc: 1000,
				MemLive:    0,
			},
			initialBinary:    "",
			initialNodeName:  "Cardano Node",
			expectedBinary:   "dingo",
			expectedNodeName: "Dingo",
		},
		{
			name: "Dingo detection with custom node name",
			metrics: &PromMetrics{
				GoMemAlloc: 1000,
				MemLive:    0,
			},
			initialBinary:    "",
			initialNodeName:  "My Custom Node",
			expectedBinary:   "dingo",
			expectedNodeName: "My Custom Node",
		},
		{
			name: "Cardano-node detection",
			metrics: &PromMetrics{
				GoMemAlloc: 0,
				MemLive:    500,
			},
			initialBinary:    "",
			initialNodeName:  "Cardano Node",
			expectedBinary:   CARDANO_BINARY,
			expectedNodeName: "Cardano Node",
		},
		{
			name: "Cardano-node detection with MemLive > 0",
			metrics: &PromMetrics{
				GoMemAlloc: 1000,
				MemLive:    500,
			},
			initialBinary:    "",
			initialNodeName:  "Cardano Node",
			expectedBinary:   CARDANO_BINARY,
			expectedNodeName: "Cardano Node",
		},
		{
			name: "Edge case: GoMemAlloc = 0, MemLive = 0",
			metrics: &PromMetrics{
				GoMemAlloc: 0,
				MemLive:    0,
			},
			initialBinary:    "",
			initialNodeName:  "Cardano Node",
			expectedBinary:   CARDANO_BINARY,
			expectedNodeName: "Cardano Node",
		},
		{
			name: "User provided binary - don't override",
			metrics: &PromMetrics{
				GoMemAlloc: 1000,
				MemLive:    0,
			},
			initialBinary:    "custom-node",
			initialNodeName:  "Cardano Node",
			expectedBinary:   "custom-node",
			expectedNodeName: "Cardano Node",
		},
		{
			name: "Amaru binary with default node name",
			metrics: &PromMetrics{
				GoMemAlloc: 1000,
				MemLive:    0,
			},
			initialBinary:    "amaru",
			initialNodeName:  "Cardano Node",
			expectedBinary:   "amaru",
			expectedNodeName: "Amaru",
		},
		{
			name: "Amaru binary with custom node name",
			metrics: &PromMetrics{
				GoMemAlloc: 1000,
				MemLive:    0,
			},
			initialBinary:    "amaru",
			initialNodeName:  "My Custom Node",
			expectedBinary:   "amaru",
			expectedNodeName: "My Custom Node",
		},
		{
			name:             "Nil metrics - early return",
			metrics:          nil,
			initialBinary:    "",
			initialNodeName:  "Cardano Node",
			expectedBinary:   "",
			expectedNodeName: "Cardano Node",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Reset config and detected binary for each test
			cfg := config.GetConfig()
			cfg.Node.Binary = tt.initialBinary
			cfg.App.NodeName = tt.initialNodeName
			detectedNodeBinary.Store("") // Reset detected binary

			// Call the function
			detectNodeType(tt.metrics)

			// Check results
			if getEffectiveNodeBinary() != tt.expectedBinary {
				t.Errorf(
					"expected Binary %q, got %q",
					tt.expectedBinary,
					getEffectiveNodeBinary(),
				)
			}
			if getEffectiveNodeName() != tt.expectedNodeName {
				t.Errorf(
					"expected NodeName %q, got %q",
					tt.expectedNodeName,
					getEffectiveNodeName(),
				)
			}
		})
	}
}
