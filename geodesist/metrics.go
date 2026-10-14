package geodesist

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"

	"github.com/prometheus/client_golang/prometheus"
)

var (
	labels      = []string{"host"}
	txBytesDesc = prometheus.NewDesc("amplifi_total_tx_bytes",
		"Total transmitted bytes per client", labels, nil)
	rxBytesDesc = prometheus.NewDesc("amplifi_total_rx_bytes",
		"Total received bytes per client", labels, nil)
	globalTxBitrateDesc = prometheus.NewDesc("amplifi_global_tx_bitrate",
		"Global transmit bitrate", nil, nil)
	globalRxBitrateDesc = prometheus.NewDesc("amplifi_global_rx_bitrate",
		"Global receive bitrate", nil, nil)
	clientCountDesc = prometheus.NewDesc("amplifi_clients_count",
		"Number of connected clients", nil, nil)
	signalQualityDesc = prometheus.NewDesc("amplifi_signal_quality",
		"Wi-Fi signal quality per client", labels, nil)
	happinessDesc = prometheus.NewDesc("amplifi_happiness_score",
		"Happiness score per client", labels, nil)
)

// Client is one wifi client on a user-facing network.
type Client struct {
	MAC         string
	IP          string
	Description string
}

type wifiClient struct {
	Address        string
	Description    string
	TxBytes        float64
	RxBytes        float64
	TxBytes64      *float64
	RxBytes64      *float64
	SignalQuality  *float64
	HappinessScore *float64
}

// The 32-bit counters stick at their maximum, and only then does the router
// add the 64-bit field alongside.
func (c wifiClient) txBytes() float64 { return or64(c.TxBytes64, c.TxBytes) }
func (c wifiClient) rxBytes() float64 { return or64(c.RxBytes64, c.RxBytes) }

func or64(wide *float64, narrow float64) float64 {
	if wide != nil {
		return *wide
	}
	return narrow
}

type ethPort struct {
	TxBitrate *float64 `json:"tx_bitrate"`
	RxBitrate *float64 `json:"rx_bitrate"`
}

// snapshot is one poll's worth of router state. Per-host values are keyed by
// the client's description, which is not unique: two unnamed iPhones are both
// "iPhone". Bytes for a shared label are summed and gauges take the maximum.
type snapshot struct {
	clients   []Client
	txBitrate *float64
	rxBitrate *float64
	txBytes   map[string]float64
	rxBytes   map[string]float64
	signal    map[string]float64
	happiness map[string]float64
}

// hostLabel is the host label for a client's router name.
func hostLabel(description string) string {
	if description == "" {
		return "unknown"
	}
	return description
}

func parseSnapshot(data []byte) (*snapshot, error) {
	var response []json.RawMessage
	if err := json.Unmarshal(data, &response); err != nil {
		return nil, fmt.Errorf("failed to parse metrics: %w", err)
	}
	if len(response) < 5 {
		return nil, fmt.Errorf("metrics response has %d sections, want at least 5", len(response))
	}

	// Section 1 is access point -> band -> network -> client MAC -> stats.
	var aps map[string]map[string]map[string]map[string]wifiClient
	if err := json.Unmarshal(response[1], &aps); err != nil {
		return nil, fmt.Errorf("failed to parse client stats: %w", err)
	}
	// Section 4 is ethernet ports per node. The modem is on the router's eth-0.
	var eth map[string]map[string]ethPort
	if err := json.Unmarshal(response[4], &eth); err != nil {
		return nil, fmt.Errorf("failed to parse ethernet stats: %w", err)
	}

	s := &snapshot{
		txBytes:   map[string]float64{},
		rxBytes:   map[string]float64{},
		signal:    map[string]float64{},
		happiness: map[string]float64{},
	}
	for _, ports := range eth {
		if p, ok := ports["eth-0"]; ok {
			s.txBitrate, s.rxBitrate = p.TxBitrate, p.RxBitrate
		}
	}
	for _, bands := range aps {
		for _, networks := range bands {
			for network, clients := range networks {
				if network == "Internal network" {
					continue
				}
				for mac, c := range clients {
					s.clients = append(s.clients, Client{MAC: mac, IP: c.Address, Description: c.Description})
					host := hostLabel(c.Description)
					s.txBytes[host] += c.txBytes()
					s.rxBytes[host] += c.rxBytes()
					if c.SignalQuality != nil {
						s.signal[host] = max(s.signal[host], *c.SignalQuality)
					}
					if c.HappinessScore != nil {
						s.happiness[host] = max(s.happiness[host], *c.HappinessScore)
					}
				}
			}
		}
	}
	return s, nil
}

// AmpliFiCollector fetches router state on each poll and exposes the latest
// snapshot. Only clients connected at the last poll are exported, so a device
// that leaves drops out of the graphs instead of holding its last value.
type AmpliFiCollector struct {
	client *AmpliFiClient
	namer  *Namer

	mu   sync.Mutex
	snap *snapshot
}

// NewAmpliFiCollector takes an optional namer; nil disables renaming.
func NewAmpliFiCollector(client *AmpliFiClient, namer *Namer) *AmpliFiCollector {
	return &AmpliFiCollector{client: client, namer: namer}
}

func (c *AmpliFiCollector) Fetch(ctx context.Context) error {
	data, err := c.client.GetMetrics(ctx)
	if err != nil {
		return fmt.Errorf("failed to get metrics: %w", err)
	}
	snap, err := parseSnapshot(data)
	if err != nil {
		return err
	}

	c.mu.Lock()
	c.snap = snap
	c.mu.Unlock()

	if c.namer != nil {
		c.namer.Name(ctx, snap.clients, c.client.RenameDevice)
	}
	return nil
}

func (c *AmpliFiCollector) Describe(ch chan<- *prometheus.Desc) {
	for _, d := range []*prometheus.Desc{
		txBytesDesc, rxBytesDesc, globalTxBitrateDesc, globalRxBitrateDesc,
		clientCountDesc, signalQualityDesc, happinessDesc,
	} {
		ch <- d
	}
}

func (c *AmpliFiCollector) Collect(ch chan<- prometheus.Metric) {
	c.mu.Lock()
	s := c.snap
	c.mu.Unlock()
	if s == nil {
		return
	}

	ch <- prometheus.MustNewConstMetric(clientCountDesc, prometheus.GaugeValue, float64(len(s.clients)))
	if s.txBitrate != nil {
		ch <- prometheus.MustNewConstMetric(globalTxBitrateDesc, prometheus.GaugeValue, *s.txBitrate)
	}
	if s.rxBitrate != nil {
		ch <- prometheus.MustNewConstMetric(globalRxBitrateDesc, prometheus.GaugeValue, *s.rxBitrate)
	}
	perHost := []struct {
		desc   *prometheus.Desc
		kind   prometheus.ValueType
		values map[string]float64
	}{
		{txBytesDesc, prometheus.CounterValue, s.txBytes},
		{rxBytesDesc, prometheus.CounterValue, s.rxBytes},
		{signalQualityDesc, prometheus.GaugeValue, s.signal},
		{happinessDesc, prometheus.GaugeValue, s.happiness},
	}
	for _, m := range perHost {
		for host, v := range m.values {
			ch <- prometheus.MustNewConstMetric(m.desc, m.kind, v, host)
		}
	}
}
