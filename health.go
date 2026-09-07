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

import "sync/atomic"

// healthSubsystem identifies an independent background subsystem whose
// consecutive failures are tracked separately, so a success in one
// subsystem (for example peer probing) can never mask an ongoing failure in
// another (for example the Prometheus scrape loop), and so a single failed
// attempt in one subsystem is never counted against another.
type healthSubsystem int

const (
	healthSubsystemPrometheus healthSubsystem = iota
	healthSubsystemProcess
	healthSubsystemPeers
	healthSubsystemCount
)

var subsystemFailures [healthSubsystemCount]atomic.Uint32

// recordSubsystemFailure records a failed attempt for the given subsystem.
// Each attempt must record exactly one outcome (recordSubsystemFailure or
// recordSubsystemSuccess) so a single failed attempt is never
// double-counted.
func recordSubsystemFailure(s healthSubsystem) {
	subsystemFailures[s].Add(1)
}

// recordSubsystemSuccess clears the given subsystem's consecutive-failure
// count. Call it only for that subsystem's own successful attempts, never
// on behalf of an unrelated subsystem.
func recordSubsystemSuccess(s healthSubsystem) {
	subsystemFailures[s].Store(0)
}

// failCount reports the worst consecutive-failure streak across all
// tracked subsystems. It drives the overall dashboard health indicator and
// the connect-retry panic threshold.
func failCount() uint32 {
	var worst uint32
	for i := range subsystemFailures {
		if v := subsystemFailures[i].Load(); v > worst {
			worst = v
		}
	}
	return worst
}
