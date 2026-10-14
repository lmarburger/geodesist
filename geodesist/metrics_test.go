package geodesist

import (
	"os"
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
)

func TestParseSnapshotFromRouterSample(t *testing.T) {
	data, err := os.ReadFile("../info.json")
	if err != nil {
		t.Fatal(err)
	}
	s, err := parseSnapshot(data)
	if err != nil {
		t.Fatal(err)
	}

	for _, c := range s.clients {
		if c.IP == "" {
			t.Errorf("client %s has no IP; internal mesh links should be skipped", c.MAC)
		}
	}
	// Main's 32-bit RxBytes is stuck at 4294967295; RxBytes64 has the real total.
	if got := s.rxBytes["Main"]; got != 18189992909 {
		t.Errorf("Main rx bytes = %v, want the 64-bit value", got)
	}
	if got := s.txBytes["Main"]; got != 462250385 {
		t.Errorf("Main tx bytes = %v, want the 32-bit value when no 64-bit one is sent", got)
	}
	if got := s.signal["AWAIR-R2-132512"]; got != 56 {
		t.Errorf("Awair signal = %v", got)
	}
	if s.rxBitrate == nil || *s.rxBitrate != 52001 {
		t.Errorf("global rx bitrate = %v", s.rxBitrate)
	}
}

func TestCollectorExportsOnlyCurrentClients(t *testing.T) {
	c := NewAmpliFiCollector(nil, nil)
	set := func(data string) {
		t.Helper()
		s, err := parseSnapshot([]byte(data))
		if err != nil {
			t.Fatal(err)
		}
		c.snap = s
	}

	// Two clients share the generic name "iPhone" and one has none.
	set(`[{}, {"ap": {"5 GHz": {
		"User network": {
			"aa": {"Address": "10.0.0.2", "Description": "iPhone", "RxBytes": 100, "SignalQuality": 40},
			"bb": {"Address": "10.0.0.3", "Description": "iPhone", "RxBytes": 50, "SignalQuality": 70},
			"cc": {"Address": "10.0.0.4", "RxBytes": 7}
		},
		"Internal network": {"dd": {"RxBytes": 999}}
	}}}, {}, {}, {}]`)

	reg := prometheus.NewPedanticRegistry()
	reg.MustRegister(c)
	if _, err := reg.Gather(); err != nil {
		t.Fatalf("shared labels must not produce duplicate series: %v", err)
	}
	if got := c.snap.rxBytes["iPhone"]; got != 150 {
		t.Errorf("iPhone rx bytes = %v, want the sum 150", got)
	}
	if got := c.snap.signal["iPhone"]; got != 70 {
		t.Errorf("iPhone signal = %v, want the max 70", got)
	}
	if got := c.snap.rxBytes["unknown"]; got != 7 {
		t.Errorf("unnamed client rx bytes = %v", got)
	}
	if n, _ := testutil.GatherAndCount(reg, "amplifi_total_rx_bytes"); n != 2 {
		t.Errorf("rx series = %d, want 2", n)
	}

	set(`[{}, {"ap": {"5 GHz": {"User network": {
		"cc": {"Address": "10.0.0.4", "RxBytes": 9}
	}}}}, {}, {}, {}]`)
	if n, _ := testutil.GatherAndCount(reg, "amplifi_total_rx_bytes"); n != 1 {
		t.Errorf("rx series after the iPhones left = %d, want 1", n)
	}
	if got, _ := testutil.GatherAndCount(reg, "amplifi_clients_count"); got != 1 {
		t.Errorf("clients_count series = %d", got)
	}
}
