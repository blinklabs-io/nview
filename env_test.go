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
	"bytes"
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/blinklabs-io/nview/internal/config"
)

// bodyCloseTracker wraps a response body and records whether Close was
// called on it.
type bodyCloseTracker struct {
	io.ReadCloser
	closed *atomic.Bool
}

func (b *bodyCloseTracker) Close() error {
	b.closed.Store(true)
	return b.ReadCloser.Close()
}

// trackingRoundTripper wraps http.DefaultTransport so tests can observe
// whether getNodeMetrics closed the response body it received.
type trackingRoundTripper struct {
	closed *atomic.Bool
}

func (t *trackingRoundTripper) RoundTrip(
	req *http.Request,
) (*http.Response, error) {
	//nolint:usestdlibvars
	resp, err := http.DefaultTransport.RoundTrip(req)
	if resp != nil {
		resp.Body = &bodyCloseTracker{ReadCloser: resp.Body, closed: t.closed}
	}
	return resp, err
}

// pointDefaultClientAtTestServer configures cfg.Prometheus to reach server
// and installs a tracking transport on http.DefaultClient, restoring both
// when the test ends. It returns the flag the tracking transport sets when
// the response body is closed.
func pointDefaultClientAtTestServer(
	t *testing.T,
	server *httptest.Server,
	timeoutSeconds uint32,
) *atomic.Bool {
	t.Helper()

	host, portStr, err := net.SplitHostPort(
		strings.TrimPrefix(server.URL, "http://"),
	)
	if err != nil {
		t.Fatalf("failed to parse test server address: %v", err)
	}
	port, err := strconv.ParseUint(portStr, 10, 32)
	if err != nil {
		t.Fatalf("failed to parse test server port: %v", err)
	}

	cfg := config.GetConfig()
	origHost := cfg.Prometheus.Host
	origPort := cfg.Prometheus.Port
	origTimeout := cfg.Prometheus.Timeout
	cfg.Prometheus.Host = host
	cfg.Prometheus.Port = uint32(port)
	cfg.Prometheus.Timeout = timeoutSeconds

	origTransport := http.DefaultClient.Transport
	closed := &atomic.Bool{}
	http.DefaultClient.Transport = &trackingRoundTripper{closed: closed}

	t.Cleanup(func() {
		cfg.Prometheus.Host = origHost
		cfg.Prometheus.Port = origPort
		cfg.Prometheus.Timeout = origTimeout
		http.DefaultClient.Transport = origTransport
	})

	return closed
}

// TestGetNodeMetricsSuccessClosesBody is a sanity check that the happy path
// still returns the response body and closes it.
func TestGetNodeMetricsSuccessClosesBody(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(
		func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("metric_a 1\n"))
		},
	))
	defer server.Close()

	closed := pointDefaultClientAtTestServer(t, server, 3)

	body, status, err := getNodeMetrics(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if status != http.StatusOK {
		t.Fatalf("expected status 200, got %d", status)
	}
	if string(body) != "metric_a 1\n" {
		t.Fatalf("unexpected body: %q", body)
	}
	if !closed.Load() {
		t.Fatal("expected response body to be closed")
	}
}

// TestGetNodeMetricsOversizedResponseClosesBody verifies that a response
// larger than maxMetricsResponseBytes is rejected with a distinct error and
// that the body is still closed.
func TestGetNodeMetricsOversizedResponseClosesBody(t *testing.T) {
	originalMax := maxMetricsResponseBytes
	defer func() { maxMetricsResponseBytes = originalMax }()
	maxMetricsResponseBytes = 16

	server := httptest.NewServer(http.HandlerFunc(
		func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write(bytes.Repeat([]byte("x"), 64))
		},
	))
	defer server.Close()

	closed := pointDefaultClientAtTestServer(t, server, 3)

	_, _, err := getNodeMetrics(context.Background())
	if !errors.Is(err, errMetricsResponseTooLarge) {
		t.Fatalf("expected errMetricsResponseTooLarge, got %v", err)
	}
	if !closed.Load() {
		t.Fatal("expected response body to be closed")
	}
}

// TestGetNodeMetricsStalledResponseClosesBody verifies that a response whose
// body stalls after headers are sent is aborted by the request deadline
// (rather than hanging or reading unbounded data) and that the body is
// still closed.
func TestGetNodeMetricsStalledResponseClosesBody(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(
		func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
			if f, ok := w.(http.Flusher); ok {
				f.Flush()
			}
			// Stall past the client's request deadline instead of ever
			// completing the response.
			select {
			case <-r.Context().Done():
			case <-time.After(5 * time.Second):
			}
		},
	))
	defer server.Close()

	closed := pointDefaultClientAtTestServer(t, server, 1)

	start := time.Now()
	_, _, err := getNodeMetrics(context.Background())
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("expected an error from a stalled response")
	}
	if elapsed > 4*time.Second {
		t.Fatalf(
			"expected the request deadline to abort the stalled read quickly, took %s",
			elapsed,
		)
	}
	if !closed.Load() {
		t.Fatal("expected response body to be closed")
	}
}

// TestGetNodeMetricsReadErrorClosesBody verifies that a response truncated
// mid-body (fewer bytes than the declared Content-Length) surfaces a read
// error and still closes the body.
func TestGetNodeMetricsReadErrorClosesBody(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(
		func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Length", "1000")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("short"))
		},
	))
	defer server.Close()

	closed := pointDefaultClientAtTestServer(t, server, 3)

	_, _, err := getNodeMetrics(context.Background())
	if err == nil {
		t.Fatal("expected a read error from a truncated response")
	}
	if !closed.Load() {
		t.Fatal("expected response body to be closed")
	}
}
