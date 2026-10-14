package geodesist

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

type fakeFetcher struct{ err error }

func (f *fakeFetcher) Fetch(ctx context.Context) error { return f.err }

func (f *fakeFetcher) Describe(ch chan<- *prometheus.Desc) { ch <- clientCountDesc }

func (f *fakeFetcher) Collect(ch chan<- prometheus.Metric) {
	ch <- prometheus.MustNewConstMetric(clientCountDesc, prometheus.GaugeValue, 3)
}

func newTestPoller(fetcher Fetcher, now *time.Time) *Poller {
	p := NewPoller(fetcher, PollerConfig{Interval: 5 * time.Second, Timeout: 15 * time.Second, MaxBackoff: time.Minute})
	p.now = func() time.Time { return *now }
	return p
}

// gather returns each exposed metric family's first value by name.
func gather(t *testing.T, p *Poller) map[string]float64 {
	t.Helper()
	reg := prometheus.NewPedanticRegistry()
	reg.MustRegister(p)
	families, err := reg.Gather()
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]float64{}
	for _, mf := range families {
		m := mf.GetMetric()[0]
		got[mf.GetName()] = m.GetGauge().GetValue() + m.GetCounter().GetValue()
	}
	return got
}

func TestPollerExposesRouterMetricsOnlyWhileFresh(t *testing.T) {
	now := time.Unix(1_000_000, 0)
	fetcher := &fakeFetcher{}
	p := newTestPoller(fetcher, &now)

	got := gather(t, p)
	if got["amplifi_router_up"] != 0 {
		t.Fatalf("router_up before any poll = %v", got["amplifi_router_up"])
	}
	for _, name := range []string{"amplifi_clients_count", "amplifi_last_success_timestamp_seconds"} {
		if _, ok := got[name]; ok {
			t.Fatalf("%s exposed before any poll", name)
		}
	}

	p.Poll(context.Background())
	got = gather(t, p)
	if got["amplifi_router_up"] != 1 || got["amplifi_clients_count"] != 3 {
		t.Fatalf("after a successful poll: %v", got)
	}
	if got["amplifi_last_success_timestamp_seconds"] != 1_000_000 {
		t.Fatalf("last success = %v", got["amplifi_last_success_timestamp_seconds"])
	}

	fetcher.err = errors.New("router unreachable")
	p.Poll(context.Background())
	got = gather(t, p)
	if got["amplifi_router_up"] != 0 || got["amplifi_poll_errors_total"] != 1 {
		t.Fatalf("after a failed poll: %v", got)
	}
	if _, ok := got["amplifi_clients_count"]; !ok {
		t.Fatal("one failure inside the freshness window should keep the data")
	}

	// Interval 5s and timeout 15s make the freshness window 25s.
	now = now.Add(26 * time.Second)
	got = gather(t, p)
	if _, ok := got["amplifi_clients_count"]; ok {
		t.Fatal("stale router metrics still exposed")
	}
	if _, ok := got["amplifi_last_success_timestamp_seconds"]; !ok {
		t.Fatal("last success timestamp should stay exposed")
	}
}

func TestBackoff(t *testing.T) {
	for failures, want := range []time.Duration{5, 5, 10, 20, 40, 60, 60} {
		if got := backoff(5*time.Second, time.Minute, failures); got != want*time.Second {
			t.Errorf("backoff(%d failures) = %s, want %s", failures, got, want*time.Second)
		}
	}
}

func TestPollTimeoutCancelsHungRequest(t *testing.T) {
	released := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-r.Context().Done()
		close(released)
	}))
	defer srv.Close()

	client, err := NewAmpliFiClient(srv.URL, "secret")
	if err != nil {
		t.Fatal(err)
	}
	p := NewPoller(NewAmpliFiCollector(client, nil), PollerConfig{Interval: time.Second, Timeout: 100 * time.Millisecond, MaxBackoff: time.Minute})

	start := time.Now()
	p.Poll(context.Background())
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Fatalf("poll took %s; the timeout should have cut it off", elapsed)
	}
	select {
	case <-released:
	case <-time.After(2 * time.Second):
		t.Fatal("the router never saw the request cancelled")
	}
	if got := gather(t, p); got["amplifi_router_up"] != 0 || got["amplifi_poll_errors_total"] != 1 {
		t.Fatalf("after a timed-out poll: %v", got)
	}
}
