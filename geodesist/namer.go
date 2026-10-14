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
	probeTimeout = time.Second
	renameRetry  = time.Minute
)

// NamesConfig is the --names file.
type NamesConfig struct {
	// Generic lists the labels that mark a client as needing a name, as they
	// appear in the host label: "unknown" for a client with no router name,
	// and router defaults such as "iPhone". Only these clients are probed.
	Generic []string `json:"generic"`
	// Names maps the hostname a device announces over mDNS to the router
	// name it should have. Hostnames match case-insensitively.
	Names map[string]string `json:"names"`
}

func LoadNames(path string) (NamesConfig, error) {
	var cfg NamesConfig
	data, err := os.ReadFile(path)
	if err != nil {
		return cfg, err
	}
	if err := json.Unmarshal(data, &cfg); err != nil {
		return cfg, fmt.Errorf("failed to parse %s: %w", path, err)
	}
	return cfg, nil
}

// Namer keeps router client names correct for devices with rotating private
// MAC addresses. The router stores names by MAC, so a rotated iPhone comes
// back unnamed or as a generic "iPhone". The hostname the device announces
// over mDNS comes from the name set on the device and survives rotation, so
// it identifies the device again and the namer renames it in the router.
//
// A locked iPhone answers only for about 13 seconds after its screen wakes,
// or about 25 seconds after it is unlocked and locked again, so generic
// clients are probed on every poll rather than occasionally.
type Namer struct {
	generic map[string]bool
	names   map[string]string // lowercased announced hostname -> router name
	lookup  func(ctx context.Context, ip string) (string, error)
	now     func() time.Time
	renames prometheus.Counter

	// Both by MAC, and forgotten when the client leaves.
	unmapped   map[string]string    // answered with a hostname not in names
	retryAfter map[string]time.Time // rename failed; wait before trying again
}

func NewNamer(cfg NamesConfig) *Namer {
	n := &Namer{
		generic: map[string]bool{},
		names:   map[string]string{},
		lookup: func(ctx context.Context, ip string) (string, error) {
			return lookupMDNSHostname(ctx, net.JoinHostPort(ip, "5353"))
		},
		now: time.Now,
		renames: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "amplifi_client_renames_total",
			Help: "Router clients renamed from the hostname they announce over mDNS.",
		}),
		unmapped:   map[string]string{},
		retryAfter: map[string]time.Time{},
	}
	for _, label := range cfg.Generic {
		n.generic[label] = true
	}
	for host, name := range cfg.Names {
		n.names[strings.ToLower(host)] = name
	}
	return n
}

// Name probes the clients with a generic label and renames the ones whose
// announced hostname is in the config.
func (n *Namer) Name(ctx context.Context, clients []Client, rename func(ctx context.Context, mac, name string) error) {
	now := n.now()
	present := map[string]bool{}
	var due []Client
	for _, c := range clients {
		present[c.MAC] = true
		if c.IP == "" || !n.generic[hostLabel(c.Description)] {
			continue
		}
		if _, ok := n.unmapped[c.MAC]; ok || now.Before(n.retryAfter[c.MAC]) {
			continue
		}
		due = append(due, c)
	}
	// Rotated MACs never come back, so forget clients as they leave.
	for mac := range n.unmapped {
		if !present[mac] {
			delete(n.unmapped, mac)
		}
	}
	for mac := range n.retryAfter {
		if !present[mac] {
			delete(n.retryAfter, mac)
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
			log.Printf("client %s at %s (%q) announces %q, which is not in the names file",
				c.MAC, c.IP, hostLabel(c.Description), host)
			n.unmapped[c.MAC] = host
			continue
		}
		if err := rename(ctx, c.MAC, name); err != nil {
			log.Printf("renaming client %s at %s to %q failed: %v", c.MAC, c.IP, name, err)
			n.retryAfter[c.MAC] = now.Add(renameRetry)
			continue
		}
		n.renames.Inc()
		log.Printf("renamed client %s at %s from %q to %q (announces %q)",
			c.MAC, c.IP, hostLabel(c.Description), name, host)
	}
}

func (n *Namer) Describe(ch chan<- *prometheus.Desc) { n.renames.Describe(ch) }
func (n *Namer) Collect(ch chan<- prometheus.Metric) { n.renames.Collect(ch) }
