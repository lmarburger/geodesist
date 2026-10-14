package geodesist

import (
	"context"
	"errors"
	"net"
	"sync"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus/testutil"
	"golang.org/x/net/dns/dnsmessage"
)

func newTestNamer(announced map[string]string, now *time.Time) (*Namer, *[]string) {
	n := NewNamer(map[string]string{"Tricias-iPad": "Trish’s iPad", "Fidel-Castro": "Fidel Castro"})
	n.now = func() time.Time { return *now }
	var probed []string
	var mu sync.Mutex
	n.lookup = func(ctx context.Context, ip string) (string, error) {
		mu.Lock()
		probed = append(probed, ip)
		mu.Unlock()
		if host, ok := announced[ip]; ok {
			return host, nil
		}
		return "", context.DeadlineExceeded
	}
	return n, &probed
}

func TestNamerRenamesFromAnnouncedHostname(t *testing.T) {
	now := time.Unix(1_000_000, 0)
	n, probed := newTestNamer(map[string]string{
		"10.0.0.2": "tricias-ipad", // case differs from the names file
		"10.0.0.3": "Makss-iPhone", // not in the names file
	}, &now)

	renamed := map[string]string{}
	rename := func(ctx context.Context, mac, name string) error {
		renamed[mac] = name
		return nil
	}
	clients := []Client{
		{MAC: "aa", IP: "10.0.0.2", Description: ""},
		{MAC: "bb", IP: "10.0.0.3", Description: "iPhone"},
		{MAC: "cc", IP: "10.0.0.4", Description: "Fidel Castro"}, // already right
		{MAC: "dd", IP: "10.0.0.5", Description: "iPhone"},       // asleep
	}

	n.Name(context.Background(), clients, rename)
	if len(renamed) != 1 || renamed["aa"] != "Trish’s iPad" {
		t.Fatalf("renamed = %v", renamed)
	}
	if len(*probed) != 3 {
		t.Fatalf("probed %v; a client already carrying a configured name should be skipped", *probed)
	}
	if got := testutil.ToFloat64(n.renames); got != 1 {
		t.Fatalf("renames counter = %v", got)
	}

	*probed = nil
	now = now.Add(probeEvery - time.Second)
	n.Name(context.Background(), clients, rename)
	if len(*probed) != 0 {
		t.Fatalf("reprobed %v inside the probe interval", *probed)
	}

	now = now.Add(time.Second)
	n.Name(context.Background(), clients, rename)
	if len(*probed) != 3 {
		t.Fatalf("after the probe interval probed %v, want the 3 unresolved clients again", *probed)
	}
}

func TestNamerKeepsGoingWhenRenameFails(t *testing.T) {
	now := time.Unix(1_000_000, 0)
	n, _ := newTestNamer(map[string]string{"10.0.0.2": "Tricias-iPad", "10.0.0.3": "Fidel-Castro"}, &now)
	var calls int
	n.Name(context.Background(), []Client{
		{MAC: "aa", IP: "10.0.0.2"},
		{MAC: "bb", IP: "10.0.0.3"},
	}, func(ctx context.Context, mac, name string) error {
		calls++
		return errors.New("router said no")
	})
	if calls != 2 {
		t.Fatalf("rename calls = %d, want 2", calls)
	}
	if got := testutil.ToFloat64(n.renames); got != 0 {
		t.Fatalf("renames counter = %v after failures", got)
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
