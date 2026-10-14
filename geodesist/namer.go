package geodesist

import (
	"context"
	"log"
	"net"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

const (
	probeTimeout = time.Second
	renameRetry  = time.Minute
)

// Namer names clients that use a private (randomized) MAC address after the
// hostname they announce over mDNS.
//
// The router stores names by MAC. iPhones and iPads with a rotating private
// Wi-Fi address periodically reappear under a new MAC, unnamed or as a
// generic "iPhone", so any name set in the router is eventually lost. The
// announced hostname comes from the name set on the device ("Tricias-iPad")
// and survives rotation, so naming these clients after it gives each device
// the same name every time. Clients with a hardware MAC keep their names in
// the router and are never touched.
//
// A locked iPhone answers only for about 13 seconds after its screen wakes,
// or about 25 seconds after it is unlocked and locked again, so unresolved
// clients are probed on every poll.
type Namer struct {
	lookup  func(ctx context.Context, ip string) (string, error)
	now     func() time.Time
	renames prometheus.Counter

	// Both by MAC, and forgotten when the client leaves.
	settled    map[string]string    // router name confirmed to match the announced hostname
	retryAfter map[string]time.Time // rename failed; wait before trying again
}

func NewNamer() *Namer {
	return &Namer{
		lookup: func(ctx context.Context, ip string) (string, error) {
			return lookupMDNSHostname(ctx, net.JoinHostPort(ip, "5353"))
		},
		now: time.Now,
		renames: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "amplifi_client_renames_total",
			Help: "Private-MAC clients renamed to the hostname they announce over mDNS.",
		}),
		settled:    map[string]string{},
		retryAfter: map[string]time.Time{},
	}
}

// privateMAC reports whether mac is locally administered, which is how
// randomized private Wi-Fi addresses are marked.
func privateMAC(mac string) bool {
	first, _, _ := strings.Cut(mac, ":")
	b, err := strconv.ParseUint(first, 16, 8)
	return err == nil && b&0x02 != 0
}

// Name probes private-MAC clients whose router name has not been confirmed
// and renames any whose name differs from the hostname they announce.
func (n *Namer) Name(ctx context.Context, clients []Client, rename func(ctx context.Context, mac, name string) error) {
	now := n.now()
	present := map[string]bool{}
	var due []Client
	for _, c := range clients {
		present[c.MAC] = true
		if c.IP == "" || !privateMAC(c.MAC) || now.Before(n.retryAfter[c.MAC]) {
			continue
		}
		if name, ok := n.settled[c.MAC]; ok && name == c.Description {
			continue
		}
		due = append(due, c)
	}
	// Rotated MACs never come back, so forget clients as they leave.
	for mac := range n.settled {
		if !present[mac] {
			delete(n.settled, mac)
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
		if host == c.Description {
			n.settled[c.MAC] = host
			continue
		}
		if err := rename(ctx, c.MAC, host); err != nil {
			log.Printf("renaming client %s at %s from %q to %q failed: %v",
				c.MAC, c.IP, hostLabel(c.Description), host, err)
			n.retryAfter[c.MAC] = now.Add(renameRetry)
			continue
		}
		n.settled[c.MAC] = host
		n.renames.Inc()
		log.Printf("renamed client %s at %s from %q to %q", c.MAC, c.IP, hostLabel(c.Description), host)
	}
}

func (n *Namer) Describe(ch chan<- *prometheus.Desc) { n.renames.Describe(ch) }
func (n *Namer) Collect(ch chan<- prometheus.Metric) { n.renames.Collect(ch) }
