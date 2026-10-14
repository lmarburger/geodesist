package geodesist

import (
	"context"
	"log"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

// Fetcher polls the router and exposes what the last successful poll found.
type Fetcher interface {
	Fetch(ctx context.Context) error
	prometheus.Collector
}

type PollerConfig struct {
	Interval   time.Duration
	Timeout    time.Duration
	MaxBackoff time.Duration
}

var (
	routerUpDesc = prometheus.NewDesc("amplifi_router_up",
		"Whether the most recent poll of the router succeeded.", nil, nil)
	lastSuccessDesc = prometheus.NewDesc("amplifi_last_success_timestamp_seconds",
		"Unix time of the most recent successful poll of the router.", nil, nil)
)

// Poller owns all traffic to the router. Polling used to happen inside each
// Prometheus scrape, so a slow router got a new login attempt every scrape
// interval while earlier ones were still waiting, which is the overlapping
// load that wedges its web server. Now one request is in flight at a time and
// /metrics only reads what the last poll stored.
type Poller struct {
	fetcher Fetcher
	config  PollerConfig
	now     func() time.Time
	errors  prometheus.Counter

	mu          sync.Mutex
	lastSuccess time.Time
	failures    int
}

func NewPoller(fetcher Fetcher, config PollerConfig) *Poller {
	return &Poller{
		fetcher: fetcher,
		config:  config,
		now:     time.Now,
		errors: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "amplifi_poll_errors_total",
			Help: "Failed polls of the router.",
		}),
	}
}

func (p *Poller) Run(ctx context.Context) {
	for {
		p.Poll(ctx)

		timer := time.NewTimer(p.nextDelay())
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
	}
}

func (p *Poller) Poll(ctx context.Context) {
	ctx, cancel := context.WithTimeout(ctx, p.config.Timeout)
	defer cancel()

	err := p.fetcher.Fetch(ctx)

	p.mu.Lock()
	defer p.mu.Unlock()
	if err != nil {
		p.errors.Inc()
		p.failures++
		// Log the transition and then periodically. A router that is down for a
		// day would otherwise write thousands of identical lines.
		if p.failures == 1 || p.failures%10 == 0 {
			log.Printf("router poll failed: consecutive_failures=%d next_attempt_in=%s err=%v",
				p.failures, backoff(p.config.Interval, p.config.MaxBackoff, p.failures), err)
		}
		return
	}
	if p.failures > 0 {
		log.Printf("router poll recovered after %d failures", p.failures)
	}
	p.failures = 0
	p.lastSuccess = p.now()
}

func (p *Poller) Describe(ch chan<- *prometheus.Desc) {
	ch <- routerUpDesc
	ch <- lastSuccessDesc
	p.errors.Describe(ch)
	p.fetcher.Describe(ch)
}

func (p *Poller) Collect(ch chan<- prometheus.Metric) {
	p.mu.Lock()
	lastSuccess, failures := p.lastSuccess, p.failures
	p.mu.Unlock()

	up := 0.0
	if !lastSuccess.IsZero() && failures == 0 {
		up = 1
	}
	ch <- prometheus.MustNewConstMetric(routerUpDesc, prometheus.GaugeValue, up)
	p.errors.Collect(ch)
	if lastSuccess.IsZero() {
		return
	}
	ch <- prometheus.MustNewConstMetric(lastSuccessDesc, prometheus.GaugeValue,
		float64(lastSuccess.UnixNano())/1e9)

	// The next success can legitimately arrive an interval plus a full timeout
	// after this one, so stale means missing more than that.
	if p.now().Sub(lastSuccess) > 2*p.config.Interval+p.config.Timeout {
		return
	}
	p.fetcher.Collect(ch)
}

func (p *Poller) nextDelay() time.Duration {
	p.mu.Lock()
	defer p.mu.Unlock()
	return backoff(p.config.Interval, p.config.MaxBackoff, p.failures)
}

// backoff doubles the wait for each consecutive failure, capped at max. A max
// below the interval means failures are retried sooner than normal polls.
func backoff(interval, max time.Duration, failures int) time.Duration {
	if failures == 0 {
		return interval
	}
	delay := interval
	for i := 1; i < failures && delay < max; i++ {
		delay *= 2
	}
	return min(delay, max)
}
