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
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"
)

func TestPromMetricSeriesPreserveExactLabelIdentity(t *testing.T) {
	encoded, err := prometheusText2json([]byte(`# TYPE dingo_leios_input_blocks_total counter
dingo_leios_input_blocks_total{stage="diff-use"} 3
dingo_leios_input_blocks_total{stage="diff_use"} 4
`))
	if err != nil {
		t.Fatal(err)
	}
	var got struct {
		Series []struct {
			Name   string  `json:"name"`
			Value  float64 `json:"value"`
			Labels []struct {
				Name  string `json:"name"`
				Value string `json:"value"`
			} `json:"labels"`
		} `json:"metric_series"`
	}
	if err := json.Unmarshal(encoded, &got); err != nil {
		t.Fatal(err)
	}
	if len(got.Series) != 2 {
		t.Fatalf("series count = %d, expected 2", len(got.Series))
	}
	if got.Series[0].Labels[0].Value != "diff-use" ||
		got.Series[1].Labels[0].Value != "diff_use" {
		t.Fatalf("label identity was not preserved: %#v", got.Series)
	}
}

func TestPromMetricSeriesExposeHistogramSumAndCount(t *testing.T) {
	encoded, err := prometheusText2json([]byte(`# TYPE request_duration_seconds histogram
request_duration_seconds_bucket{route="tip",le="1"} 2
request_duration_seconds_bucket{route="tip",le="+Inf"} 3
request_duration_seconds_sum{route="tip"} 1.5
request_duration_seconds_count{route="tip"} 3
`))
	if err != nil {
		t.Fatal(err)
	}
	var got struct {
		Series []struct {
			Name  string  `json:"name"`
			Value float64 `json:"value"`
		} `json:"metric_series"`
	}
	if err := json.Unmarshal(encoded, &got); err != nil {
		t.Fatal(err)
	}
	if len(got.Series) != 2 || got.Series[0].Name != "request_duration_seconds_count" ||
		got.Series[0].Value != 3 || got.Series[1].Name != "request_duration_seconds_sum" ||
		got.Series[1].Value != 1.5 {
		t.Fatalf("histogram summary series = %#v", got.Series)
	}
}

func TestOpenMetricsPreservesStructuredSeries(t *testing.T) {
	encoded, err := prom2jsonWithContentType(
		[]byte(`# TYPE requests counter
requests_total{route="tip"} 3
# TYPE request_duration_seconds histogram
request_duration_seconds_bucket{route="tip",le="+Inf"} 3
request_duration_seconds_sum{route="tip"} 1.5
request_duration_seconds_count{route="tip"} 3
# EOF
`),
		"application/openmetrics-text; version=1.0.0; charset=utf-8",
	)
	if err != nil {
		t.Fatal(err)
	}
	var got PromMetrics
	if err := json.Unmarshal(encoded, &got); err != nil {
		t.Fatal(err)
	}
	series := got.SeriesByName("requests_total")
	if len(series) != 1 || series[0].Kind != PromMetricCounter ||
		len(series[0].Labels) != 1 || series[0].Labels[0].Name != "route" ||
		series[0].Labels[0].Value != "tip" {
		t.Fatalf("counter series = %#v", series)
	}
	histogram, ok := got.Histogram(
		"request_duration_seconds",
		map[string]string{"route": "tip"},
	)
	if !ok || histogram.Sum != 1.5 || histogram.Count != 3 {
		t.Fatalf("histogram = %#v, %v", histogram, ok)
	}
}

func TestPromMetricSeriesHistoryHandlesCounterReset(t *testing.T) {
	history := NewMetricHistory(2, 3)
	labels := []PromMetricLabel{{Name: "peer", Value: "alpha"}}
	metrics := func(value float64) *PromMetrics {
		return &PromMetrics{MetricSeries: []PromMetricSeries{{
			Name: "requests_total", Value: value, Kind: PromMetricCounter, Labels: labels,
		}}}
	}
	start := time.Unix(100, 0)
	history.Observe(start, metrics(8))
	history.Observe(start.Add(time.Second), metrics(12))
	history.Observe(start.Add(2*time.Second), metrics(2))
	delta, interval, ok := history.CounterDelta(
		"requests_total",
		map[string]string{"peer": "alpha"},
	)
	if !ok || delta != 2 || interval != time.Second {
		t.Fatalf("reset delta = %v over %v, %v; expected 2 over 1s", delta, interval, ok)
	}
	history.Observe(start.Add(3*time.Second), metrics(5))
	samples := history.Samples(
		"requests_total",
		map[string]string{"peer": "alpha"},
	)
	if len(samples) != 3 || samples[0].Value != 12 || samples[1].Value != 2 ||
		samples[2].Value != 5 {
		t.Fatalf("bounded samples = %#v", samples)
	}
}

func TestPromMetricSeriesHistoryEvictsOldestIdentity(t *testing.T) {
	history := NewMetricHistory(2, 2)
	at := time.Unix(100, 0)
	observe := func(name string) {
		history.Observe(at, &PromMetrics{MetricSeries: []PromMetricSeries{{
			Name: name, Value: 1, Kind: PromMetricGauge,
		}}})
	}
	observe("first")
	observe("second")
	observe("third")
	if got := history.Samples("first", nil); got != nil {
		t.Fatalf("evicted samples = %#v, expected nil", got)
	}
	if len(history.Samples("second", nil)) != 1 ||
		len(history.Samples("third", nil)) != 1 {
		t.Fatal("newest identities were not retained")
	}
}

func TestPromMetricSeriesHistoryKeepsStableSubsetAtCapacity(t *testing.T) {
	history := NewMetricHistory(2, 2)
	metrics := &PromMetrics{MetricSeries: []PromMetricSeries{
		{Name: "first", Kind: PromMetricGauge},
		{Name: "second", Kind: PromMetricGauge},
		{Name: "third", Kind: PromMetricGauge},
	}}
	history.Observe(time.Unix(100, 0), metrics)
	history.Observe(time.Unix(101, 0), metrics)
	if len(history.Samples("first", nil)) != 2 ||
		len(history.Samples("second", nil)) != 2 {
		t.Fatal("tracked identities were replaced by later series")
	}
	if got := history.Samples("third", nil); got != nil {
		t.Fatalf("untracked samples = %#v, expected nil", got)
	}
}

func TestPromMetricsHistogramSelectsStructuredLabels(t *testing.T) {
	metrics := decodePromMetrics(t, []byte(`# TYPE request_duration_seconds histogram
request_duration_seconds_bucket{route="tip",le="+Inf"} 3
request_duration_seconds_sum{route="tip"} 1.5
request_duration_seconds_count{route="tip"} 3
request_duration_seconds_bucket{route="block",le="+Inf"} 2
request_duration_seconds_sum{route="block"} 4
request_duration_seconds_count{route="block"} 2
`))
	tip, ok := metrics.Histogram(
		"request_duration_seconds",
		map[string]string{"route": "tip"},
	)
	if !ok || tip.Sum != 1.5 || tip.Count != 3 {
		t.Fatalf("tip histogram = %#v, %v", tip, ok)
	}
	all, ok := metrics.Histogram("request_duration_seconds", nil)
	if !ok || all.Sum != 5.5 || all.Count != 5 {
		t.Fatalf("combined histogram = %#v, %v", all, ok)
	}
}

func TestPromMetricSeriesCardinalityIsBounded(t *testing.T) {
	var input strings.Builder
	input.WriteString("# TYPE bounded gauge\n")
	for i := 0; i <= maxPromMetricSeries; i++ {
		fmt.Fprintf(&input, "bounded{id=%q} %d\n", fmt.Sprint(i), i)
	}
	metrics := decodePromMetrics(t, []byte(input.String()))
	if len(metrics.MetricSeries) != maxPromMetricSeries {
		t.Fatalf("series count = %d, expected %d", len(metrics.MetricSeries), maxPromMetricSeries)
	}
	if !metrics.MetricSeriesTruncated {
		t.Fatal("cardinality truncation was not reported")
	}
}

func FuzzPrometheusTextSeries(f *testing.F) {
	f.Add([]byte("# TYPE x counter\nx_total{peer=\"a\"} 1\n"))
	f.Add([]byte("# TYPE x histogram\nx_sum 1\nx_count 2\n"))
	f.Fuzz(func(t *testing.T, input []byte) {
		encoded, err := prometheusText2json(input)
		if err != nil {
			return
		}
		var metrics PromMetrics
		if err := json.Unmarshal(encoded, &metrics); err != nil {
			t.Fatalf("successful parse produced invalid JSON: %v", err)
		}
		if len(metrics.MetricSeries) > maxPromMetricSeries {
			t.Fatalf("series count %d exceeds bound", len(metrics.MetricSeries))
		}
		for _, series := range metrics.MetricSeries {
			if len(series.Name) > maxPromMetricNameBytes {
				t.Fatalf("metric name length %d exceeds bound", len(series.Name))
			}
			if len(series.Labels) > maxPromMetricLabelsPerSeries {
				t.Fatalf("label count %d exceeds bound", len(series.Labels))
			}
		}
	})
}

func BenchmarkPrometheusTextSeries(b *testing.B) {
	var input strings.Builder
	input.WriteString("# TYPE metric_total counter\n")
	for i := range 256 {
		fmt.Fprintf(&input, "metric_total{network=\"preview\",peer=%q} 1\n", fmt.Sprint(i))
	}
	data := []byte(input.String())
	for b.Loop() {
		_, _ = prometheusText2json(data)
	}
}
