package geodesist

import (
	"context"
	"errors"
	"net"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus/testutil"
	"golang.org/x/net/dns/dnsmessage"
)

func newTestNamer(announced map[string]string, now *time.Time) (*Namer, func() []string) {
	n := NewNamer()
	n.now = func() time.Time { return *now }
	var mu sync.Mutex
	var probed []string
	n.lookup = func(ctx context.Context, ip string) (string, error) {
		mu.Lock()
		probed = append(probed, ip)
		mu.Unlock()
		if host, ok := announced[ip]; ok {
			return host, nil
		}
		return "", context.DeadlineExceeded
	}
	// takeProbed returns the IPs probed since the last call, sorted.
	takeProbed := func() []string {
		mu.Lock()
		defer mu.Unlock()
		got := probed
		probed = nil
		slices.Sort(got)
		return got
	}
	return n, takeProbed
}

func TestPrivateMAC(t *testing.T) {
	for mac, want := range map[string]bool{
		"12:7e:85:2e:fe:4c": true, // 0x12 has the locally administered bit
		"76:22:25:3b:8f:16": true,
		"c6:b6:0f:8c:1e:2f": true,
		"f8:10:93:d4:ad:39": false,
		"9c:c3:94:67:2d:e9": false,
		"garbage":           false,
	} {
		if got := privateMAC(mac); got != want {
			t.Errorf("privateMAC(%q) = %v, want %v", mac, got, want)
		}
	}
}

func TestNamerRenamesPrivateMACClientsToTheirHostname(t *testing.T) {
	now := time.Unix(1_000_000, 0)
	n, takeProbed := newTestNamer(map[string]string{
		"10.0.0.2": "Tricias-iPad",
		"10.0.0.3": "Fidel-Castro",
		"10.0.0.4": "kestrel",
	}, &now)

	renamed := map[string]string{}
	rename := func(ctx context.Context, mac, name string) error {
		renamed[mac] = name
		return nil
	}
	clients := []Client{
		{MAC: "12:7e:85:2e:fe:4c", IP: "10.0.0.2", Description: "Trish’s iPad"},
		{MAC: "56:c2:29:b9:e7:5e", IP: "10.0.0.3", Description: "Fidel-Castro"}, // already right
		{MAC: "50:f2:65:f3:54:be", IP: "10.0.0.4", Description: "My Laptop"},    // hardware MAC
		{MAC: "76:22:25:3b:8f:16", IP: "10.0.0.5", Description: "iPhone"},       // asleep
	}

	n.Name(context.Background(), clients, rename)
	if got := takeProbed(); !slices.Equal(got, []string{"10.0.0.2", "10.0.0.3", "10.0.0.5"}) {
		t.Fatalf("probed %v, want only the private-MAC clients", got)
	}
	if len(renamed) != 1 || renamed["12:7e:85:2e:fe:4c"] != "Tricias-iPad" {
		t.Fatalf("renamed = %v", renamed)
	}
	if got := testutil.ToFloat64(n.renames); got != 1 {
		t.Fatalf("renames counter = %v", got)
	}

	// The router now shows the new name. Confirmed clients are not asked
	// again; the sleeping one is, on every poll.
	clients[0].Description = "Tricias-iPad"
	n.Name(context.Background(), clients, rename)
	if got := takeProbed(); !slices.Equal(got, []string{"10.0.0.5"}) {
		t.Fatalf("second poll probed %v", got)
	}

	// A name changed in the router afterwards is put back.
	clients[1].Description = "Somebody"
	n.Name(context.Background(), clients, rename)
	takeProbed()
	if renamed["56:c2:29:b9:e7:5e"] != "Fidel-Castro" {
		t.Fatalf("renamed = %v", renamed)
	}

	// A client that leaves is forgotten, so it is checked again on return.
	n.Name(context.Background(), clients[1:], rename)
	takeProbed()
	n.Name(context.Background(), clients, rename)
	if got := takeProbed(); !slices.Contains(got, "10.0.0.2") {
		t.Fatalf("returning client not probed: %v", got)
	}
}

func TestNamerWaitsAfterRenameFails(t *testing.T) {
	now := time.Unix(1_000_000, 0)
	n, takeProbed := newTestNamer(map[string]string{"10.0.0.2": "Tricias-iPad"}, &now)
	var calls int
	fail := func(ctx context.Context, mac, name string) error {
		calls++
		return errors.New("router said no")
	}
	clients := []Client{{MAC: "12:7e:85:2e:fe:4c", IP: "10.0.0.2"}}

	n.Name(context.Background(), clients, fail)
	if calls != 1 {
		t.Fatalf("rename calls = %d, want 1", calls)
	}
	if got := testutil.ToFloat64(n.renames); got != 0 {
		t.Fatalf("renames counter = %v after a failure", got)
	}
	takeProbed()

	now = now.Add(renameRetry - time.Second)
	n.Name(context.Background(), clients, fail)
	if got := takeProbed(); len(got) != 0 {
		t.Fatalf("retried %v before the retry delay", got)
	}

	now = now.Add(time.Second)
	n.Name(context.Background(), clients, fail)
	if calls != 2 {
		t.Fatalf("rename calls after the retry delay = %d, want 2", calls)
	}
}

func TestLookupMDNSHostname(t *testing.T) {
	conn, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()

	go func() {
		buf := make([]byte, 1500)
		n, from, err := conn.ReadFrom(buf)
		if err != nil {
			return
		}
		var q dnsmessage.Message
		if err := q.Unpack(buf[:n]); err != nil || len(q.Questions) != 1 {
			return
		}
		// Answer the way an iPhone does: an unrelated record first, then the PTR.
		resp := dnsmessage.Message{
			Header:    dnsmessage.Header{ID: q.ID, Response: true, Authoritative: true},
			Questions: q.Questions,
			Answers: []dnsmessage.Resource{
				{
					Header: dnsmessage.ResourceHeader{Name: dnsmessage.MustNewName("Fidel-Castro.local."), Type: dnsmessage.TypeA, Class: dnsmessage.ClassINET, TTL: 10},
					Body:   &dnsmessage.AResource{A: [4]byte{127, 0, 0, 1}},
				},
				{
					Header: dnsmessage.ResourceHeader{Name: q.Questions[0].Name, Type: dnsmessage.TypePTR, Class: dnsmessage.ClassINET, TTL: 10},
					Body:   &dnsmessage.PTRResource{PTR: dnsmessage.MustNewName("Fidel-Castro.local.")},
				},
			},
		}
		if q.Questions[0].Name.String() != "1.0.0.127.in-addr.arpa." {
			resp.Answers = nil
		}
		b, _ := resp.Pack()
		conn.WriteTo(b, from)
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	host, err := lookupMDNSHostname(ctx, conn.LocalAddr().String())
	if err != nil {
		t.Fatal(err)
	}
	if host != "Fidel-Castro" {
		t.Fatalf("host = %q", host)
	}
}

func TestLookupMDNSHostnameTimesOut(t *testing.T) {
	conn, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	start := time.Now()
	if _, err := lookupMDNSHostname(ctx, conn.LocalAddr().String()); err == nil {
		t.Fatal("lookup of a silent device succeeded")
	}
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Fatalf("lookup took %s; the context deadline should end it", elapsed)
	}
}
