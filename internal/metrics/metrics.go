// Package metrics exposes Prometheus text-format metrics on /metrics,
// disabled by default. Hand-rolled on purpose: pulling in
// client_golang for six series would fight the frugality budget.
package metrics

import (
	"context"
	"fmt"
	"net/http"
	"sync/atomic"
	"time"

	"github.com/lporcheron/quorum/internal/job"
	"github.com/lporcheron/quorum/internal/store"
)

// Metrics accumulates counters; queries gauges at scrape time.
type Metrics struct {
	enabled bool
	store   *store.Store
	version string

	requestsByClass [6]atomic.Int64 // index = status/100
	requestSeconds  atomic.Int64    // microseconds, summed
	requestCount    atomic.Int64
}

// New wires the collector; a disabled collector is a no-op.
func New(enabled bool, st *store.Store, version string) *Metrics {
	return &Metrics{enabled: enabled, store: st, version: version}
}

// Enabled reports whether /metrics should be served.
func (m *Metrics) Enabled() bool { return m.enabled }

// Observe records one HTTP request.
func (m *Metrics) Observe(status int, d time.Duration) {
	if !m.enabled {
		return
	}
	class := status / 100
	if class < 1 || class > 5 {
		class = 5
	}
	m.requestsByClass[class].Add(1)
	m.requestSeconds.Add(d.Microseconds())
	m.requestCount.Add(1)
}

// ServeHTTP writes the exposition.
func (m *Metrics) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")

	fmt.Fprintf(w, "# TYPE quorum_build_info gauge\nquorum_build_info{version=%q} 1\n", m.version)

	fmt.Fprintf(w, "# TYPE quorum_http_requests_total counter\n")
	for class := 1; class <= 5; class++ {
		fmt.Fprintf(w, "quorum_http_requests_total{class=\"%dxx\"} %d\n", class, m.requestsByClass[class].Load())
	}
	fmt.Fprintf(w, "# TYPE quorum_http_request_seconds summary\n")
	fmt.Fprintf(w, "quorum_http_request_seconds_sum %f\n", float64(m.requestSeconds.Load())/1e6)
	fmt.Fprintf(w, "quorum_http_request_seconds_count %d\n", m.requestCount.Load())

	m.gauge(r.Context(), w, "quorum_users", func(ctx context.Context) (int64, error) { return m.store.CountUsers(ctx) })
	m.gauge(r.Context(), w, "quorum_polls", func(ctx context.Context) (int64, error) { return m.store.CountPolls(ctx) })
	m.gauge(r.Context(), w, "quorum_participants", func(ctx context.Context) (int64, error) { return m.store.CountParticipants(ctx) })
	m.gauge(r.Context(), w, "quorum_jobs_pending", func(ctx context.Context) (int64, error) { return m.store.CountPendingJobs(ctx, job.MaxAttempts) })
	m.gauge(r.Context(), w, "quorum_jobs_dead", func(ctx context.Context) (int64, error) { return m.store.CountDeadJobs(ctx, job.MaxAttempts) })
}

func (m *Metrics) gauge(ctx context.Context, w http.ResponseWriter, name string, read func(context.Context) (int64, error)) {
	v, err := read(ctx)
	if err != nil {
		return // scrape stays partial rather than failing
	}
	fmt.Fprintf(w, "# TYPE %s gauge\n%s %d\n", name, name, v)
}
