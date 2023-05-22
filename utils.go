// Copyright 2023 Blink Labs, LLC.
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
	"encoding/json"
	"strings"

	dto "github.com/prometheus/client_model/go"
	"github.com/prometheus/common/expfmt"
)

// Converts a prometheus http response byte array into a JSON byte array
func prom2json(prom []byte) ([]byte, error) {
	// {"name": 0}
	out := make(map[string]interface{})
	b := []byte{}
	parser := &expfmt.TextParser{}
	families, err := parser.TextToMetricFamilies(strings.NewReader(string(prom)))
	if err != nil {
		return b, err
	}
	for _, val := range families {
		for _, m := range val.GetMetric() {
			switch val.GetType() {
			case dto.MetricType_COUNTER:
				out[val.GetName()] = m.GetCounter().GetValue()
			case dto.MetricType_GAUGE:
				out[val.GetName()] = m.GetGauge().GetValue()
			case dto.MetricType_UNTYPED:
				out[val.GetName()] = m.GetUntyped().GetValue()
			default:
				return b, err
			}
		}
	}
	b, err = json.MarshalIndent(out, "", "    ")
	if err != nil {
		return b, err
	}
	return b, nil
}
