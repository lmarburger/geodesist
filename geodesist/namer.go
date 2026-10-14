package geodesist

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

const (
	probeEvery   = 5 * time.Minute
	probeTimeout = time.Second
)

// Namer keeps router client names correct for devices with rotating private
// MAC addresses. The router stores names by MAC, so a rotated iPhone comes
// back unnamed or as a generic "iPhone". The hostname the device announces
// over mDNS comes from the name set on the device and survives rotation, so
// it identifies the device again and the namer renames it in the router.
type Namer struct {
	names   map[string]string // lowercased announced hostname -> router name
	targets map[string]bool   // router names the namer assigns
	lookup  func(ctx context.Context, ip string) (string, error)
	now     func() time.Time
	renames prometheus.Counter

	lastProbe map[string]time.Time // by MAC
	logged    map[string]string    // by MAC, the unmapped hostname last logged
}

// LoadNames reads a JSON object mapping announced hostname to router name,
// e.g. {"Tricias-iPad": "Trish’s iPad"}. Hostnames match case-insensitively.
func LoadNames(path string) (map[string]string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var names map[string]string
	if err := json.Unmarshal(data, &names); err != nil {
		return nil, fmt.Errorf("failed to parse %s: %w", path, err)
	}
	return names, nil
}

func NewNamer(names map[string]string) *Namer {
	n := &Namer{
		names:   map[string]string{},
		targets: map[string]bool{},
		lookup: func(ctx context.Context, ip string) (string, error) {
			return lookupMDNSHostname(ctx, net.JoinHostPort(ip, "5353"))
		},
		now: time.Now,
		renames: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "amplifi_client_renames_total",
			Help: "Router clients renamed from the hostname they announce over mDNS.",
		}),
		lastProbe: map[string]time.Time{},
		logged:    map[string]string{},
	}
	for host, name := range names {
		n.names[strings.ToLower(host)] = name
		n.targets[name] = true
	}
	return n
}

// Name probes clients that do not already carry one of the configured names
// and renames the ones whose announced hostname is in the config. Each MAC is
// probed at most once per probeEvery, so a sleeping phone costs one unanswered
// packet every few minutes until it wakes.
func (n *Namer) Name(ctx context.Context, clients []Client, rename func(ctx context.Context, mac, name string) error) {
	now := n.now()
	present := map[string]bool{}
	var due []Client
	for _, c := range clients {
		present[c.MAC] = true
		if c.IP == "" || n.targets[c.Description] || now.Sub(n.lastProbe[c.MAC]) < probeEvery {
			continue
		}
		n.lastProbe[c.MAC] = now
		due = append(due, c)
	}
	// Rotated MACs never come back, so forget clients as they leave.
	for mac := range n.lastProbe {
		if !present[mac] {
			delete(n.lastProbe, mac)
			delete(n.logged, mac)
		}
	}

	hosts := make([]string, len(due))
	var wg sync.WaitGroup
	for i, c := range due {
		wg.Go(func() {
			ctx, cancel := context.WithTimeout(ctx, probeTimeout)
			defer cancel()
			hosts[i], _ = n.lookup(ctx, c.IP)
		})
	}
	wg.Wait()

	for i, c := range due {
		host := hosts[i]
		if host == "" {
			continue
		}
		name, ok := n.names[strings.ToLower(host)]
		if !ok {
			// Logged so a new device's hostname can be found and added.
			if n.logged[c.MAC] != host {
				log.Printf("client %s at %s announces %q, which is not in the names file (router name %q)",
					c.MAC, c.IP, host, c.Description)
				n.logged[c.MAC] = host
			}
			continue
		}
		if err := rename(ctx, c.MAC, name); err != nil {
			log.Printf("renaming client %s at %s to %q failed: %v", c.MAC, c.IP, name, err)
			continue
		}
		n.renames.Inc()
		log.Printf("renamed client %s at %s from %q to %q (announces %q)", c.MAC, c.IP, c.Description, name, host)
	}
}

func (n *Namer) Describe(ch chan<- *prometheus.Desc) { n.renames.Describe(ch) }
func (n *Namer) Collect(ch chan<- prometheus.Metric) { n.renames.Collect(ch) }
