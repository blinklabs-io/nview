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
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	dto "github.com/prometheus/client_model/go"
	"github.com/prometheus/common/model"
)

const (
	maxPromMetricSeries          = 2048
	maxPromMetricNameBytes       = 1024
	maxPromMetricLabelsPerSeries = 16
	maxPromMetricLabelBytes      = 4096
	defaultMetricHistorySeries   = 512
	defaultMetricHistorySamples  = 12
	promMetricSeriesKey          = "metric_series"
	promMetricSeriesTruncatedKey = "metric_series_truncated"
)

// PromMetricKind identifies how a structured Prometheus series behaves.
type PromMetricKind string

const (
	PromMetricUnknown PromMetricKind = "unknown"
	PromMetricCounter PromMetricKind = "counter"
	PromMetricGauge   PromMetricKind = "gauge"
)

// PromMetricLabel is one exact, unmodified Prometheus label pair.
type PromMetricLabel struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

// PromMetricSeries is one parsed scalar series with structured labels.
type PromMetricSeries struct {
	Name   string            `json:"name"`
	Value  float64           `json:"value"`
	Kind   PromMetricKind    `json:"kind"`
	Labels []PromMetricLabel `json:"labels,omitempty"`
}

// PromHistogram contains the selected sum and count of a histogram family.
type PromHistogram struct {
	Sum   float64
	Count float64
}

// PromMetricSample is one timestamped value retained in metric history.
type PromMetricSample struct {
	At    time.Time
	Value float64
}

// MetricHistory retains a bounded number of samples for a bounded number of
// exact metric identities. New identities evict the oldest tracked identity.
type MetricHistory struct {
	mu         sync.RWMutex
	maxSeries  int
	maxSamples int
	order      []string
	series     map[string]*metricSampleRing
}

type metricSampleRing struct {
	kind    PromMetricKind
	samples []PromMetricSample
	next    int
	count   int
}

var globalPromMetricHistory = NewMetricHistory(
	defaultMetricHistorySeries,
	defaultMetricHistorySamples,
)

// NewMetricHistory creates bounded in-memory history. Non-positive bounds are
// raised to one so callers cannot accidentally construct an unbounded store.
func NewMetricHistory(maxSeries, maxSamples int) *MetricHistory {
	return &MetricHistory{
		maxSeries:  max(maxSeries, 1),
		maxSamples: max(maxSamples, 1),
		series:     make(map[string]*metricSampleRing),
	}
}

// Observe adds all structured series in a scrape to the history.
func (h *MetricHistory) Observe(at time.Time, metrics *PromMetrics) {
	if h == nil || metrics == nil {
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	observed := make(map[string]struct{}, len(metrics.MetricSeries))
	for _, metric := range metrics.MetricSeries {
		observed[metricSeriesIdentity(metric.Name, metric.Labels)] = struct{}{}
	}
	for _, metric := range metrics.MetricSeries {
		key := metricSeriesIdentity(metric.Name, metric.Labels)
		ring := h.series[key]
		if ring == nil {
			if len(h.series) == h.maxSeries {
				if !h.evictMissing(observed) {
					continue
				}
			}
			ring = &metricSampleRing{
				kind:    metric.Kind,
				samples: make([]PromMetricSample, h.maxSamples),
			}
			h.series[key] = ring
			h.order = append(h.order, key)
		}
		ring.kind = metric.Kind
		ring.samples[ring.next] = PromMetricSample{At: at, Value: metric.Value}
		ring.next = (ring.next + 1) % len(ring.samples)
		if ring.count < len(ring.samples) {
			ring.count++
		}
	}
}

func (h *MetricHistory) evictMissing(observed map[string]struct{}) bool {
	for i, key := range h.order {
		if _, ok := observed[key]; ok {
			continue
		}
		delete(h.series, key)
		h.order = append(h.order[:i], h.order[i+1:]...)
		return true
	}
	return false
}

// Samples returns retained samples in chronological order for an exact metric
// identity. The returned slice does not alias the history.
func (h *MetricHistory) Samples(
	name string,
	labels map[string]string,
) []PromMetricSample {
	if h == nil {
		return nil
	}
	key := metricSeriesIdentity(name, promMetricLabels(labels))
	h.mu.RLock()
	defer h.mu.RUnlock()
	ring := h.series[key]
	if ring == nil {
		return nil
	}
	return ring.orderedSamples()
}

// CounterDelta returns the increase between the newest two samples. When a
// counter decreases, the latest value is the post-reset increase from zero.
func (h *MetricHistory) CounterDelta(
	name string,
	labels map[string]string,
) (float64, time.Duration, bool) {
	if h == nil {
		return 0, 0, false
	}
	key := metricSeriesIdentity(name, promMetricLabels(labels))
	h.mu.RLock()
	defer h.mu.RUnlock()
	ring := h.series[key]
	if ring == nil || ring.kind != PromMetricCounter || ring.count < 2 {
		return 0, 0, false
	}
	samples := ring.orderedSamples()
	previous := samples[len(samples)-2]
	latest := samples[len(samples)-1]
	if !latest.At.After(previous.At) {
		return 0, 0, false
	}
	delta := latest.Value - previous.Value
	if latest.Value < previous.Value {
		delta = latest.Value
	}
	return delta, latest.At.Sub(previous.At), true
}

func (r *metricSampleRing) orderedSamples() []PromMetricSample {
	result := make([]PromMetricSample, r.count)
	start := (r.next - r.count + len(r.samples)) % len(r.samples)
	for i := range r.count {
		result[i] = r.samples[(start+i)%len(r.samples)]
	}
	return result
}

// SeriesByName returns copies of all structured series in the named family.
func (m *PromMetrics) SeriesByName(name string) []PromMetricSeries {
	if m == nil {
		return nil
	}
	result := make([]PromMetricSeries, 0)
	for _, metric := range m.MetricSeries {
		if metric.Name != name {
			continue
		}
		metric.Labels = append([]PromMetricLabel(nil), metric.Labels...)
		result = append(result, metric)
	}
	return result
}

// Histogram returns the sum and count for series whose labels contain every
// selector pair. Multiple matching series are added without flattening labels.
func (m *PromMetrics) Histogram(
	name string,
	selector map[string]string,
) (PromHistogram, bool) {
	if m == nil {
		return PromHistogram{}, false
	}
	type parts struct {
		histogram PromHistogram
		hasSum    bool
		hasCount  bool
	}
	selected := make(map[string]parts)
	matched := false
	for _, metric := range m.MetricSeries {
		if !promMetricLabelsContain(metric.Labels, selector) {
			continue
		}
		key := metricSeriesIdentity("", metric.Labels)
		part := selected[key]
		switch metric.Name {
		case name + "_sum":
			part.histogram.Sum += metric.Value
			part.hasSum = true
		case name + "_count":
			part.histogram.Count += metric.Value
			part.hasCount = true
		default:
			continue
		}
		selected[key] = part
		matched = true
	}
	var result PromHistogram
	for _, part := range selected {
		if !part.hasSum || !part.hasCount {
			return PromHistogram{}, false
		}
		result.Sum += part.histogram.Sum
		result.Count += part.histogram.Count
	}
	return result, matched
}

func recordPromMetricSeries(
	out map[string]any,
	name string,
	value float64,
	labels []*dto.LabelPair,
	kind PromMetricKind,
) {
	if name == "" || strings.HasSuffix(name, "_bucket") {
		return
	}
	if len(name) > maxPromMetricNameBytes {
		out[promMetricSeriesTruncatedKey] = true
		return
	}
	series, _ := out[promMetricSeriesKey].([]PromMetricSeries)
	if len(series) >= maxPromMetricSeries {
		out[promMetricSeriesTruncatedKey] = true
		return
	}
	structured, ok := structuredPromMetricLabels(labels)
	if !ok {
		out[promMetricSeriesTruncatedKey] = true
		return
	}
	out[promMetricSeriesKey] = append(series, PromMetricSeries{
		Name:   name,
		Value:  value,
		Kind:   kind,
		Labels: structured,
	})
}

func structuredPromMetricLabels(labels []*dto.LabelPair) ([]PromMetricLabel, bool) {
	if len(labels) > maxPromMetricLabelsPerSeries {
		return nil, false
	}
	result := make([]PromMetricLabel, 0, len(labels))
	totalBytes := 0
	for _, label := range labels {
		name := label.GetName()
		value := label.GetValue()
		totalBytes += len(name) + len(value)
		if totalBytes > maxPromMetricLabelBytes {
			return nil, false
		}
		result = append(result, PromMetricLabel{Name: name, Value: value})
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].Name == result[j].Name {
			return result[i].Value < result[j].Value
		}
		return result[i].Name < result[j].Name
	})
	return result, true
}

func promMetricKindFromModel(kind model.MetricType) PromMetricKind {
	switch kind {
	case model.MetricTypeCounter:
		return PromMetricCounter
	case model.MetricTypeGauge,
		model.MetricTypeGaugeHistogram,
		model.MetricTypeInfo,
		model.MetricTypeStateset,
		model.MetricTypeSummary:
		return PromMetricGauge
	case model.MetricTypeHistogram, model.MetricTypeUnknown:
		return PromMetricUnknown
	default:
		return PromMetricUnknown
	}
}

func promMetricSeriesKind(
	name string,
	types map[string]model.MetricType,
) PromMetricKind {
	if familyType, ok := types[name]; ok {
		return promMetricKindFromModel(familyType)
	}
	if strings.HasSuffix(name, "_total") {
		if familyType, ok := types[strings.TrimSuffix(name, "_total")]; ok {
			return promMetricKindFromModel(familyType)
		}
		return PromMetricCounter
	}
	for _, suffix := range []string{"_count", "_sum", "_bucket"} {
		if !strings.HasSuffix(name, suffix) {
			continue
		}
		base := strings.TrimSuffix(name, suffix)
		if familyType, ok := types[base]; ok &&
			familyType == model.MetricTypeGaugeHistogram {
			return PromMetricGauge
		}
		return PromMetricCounter
	}
	return PromMetricUnknown
}

func promMetricLabels(labels map[string]string) []PromMetricLabel {
	result := make([]PromMetricLabel, 0, len(labels))
	for name, value := range labels {
		result = append(result, PromMetricLabel{Name: name, Value: value})
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Name < result[j].Name })
	return result
}

func metricSeriesIdentity(name string, labels []PromMetricLabel) string {
	var result strings.Builder
	writeMetricIdentityPart(&result, name)
	for _, label := range labels {
		writeMetricIdentityPart(&result, label.Name)
		writeMetricIdentityPart(&result, label.Value)
	}
	return result.String()
}

func writeMetricIdentityPart(result *strings.Builder, value string) {
	result.WriteString(strconv.Itoa(len(value)))
	result.WriteByte(':')
	result.WriteString(value)
}

func promMetricLabelsContain(
	labels []PromMetricLabel,
	selector map[string]string,
) bool {
	for name, value := range selector {
		found := false
		for _, label := range labels {
			if label.Name == name && label.Value == value {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	return true
}
