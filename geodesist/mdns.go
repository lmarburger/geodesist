package geodesist

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strings"

	"golang.org/x/net/dns/dnsmessage"
)

var errNoHostname = errors.New("no PTR record in mDNS response")

// lookupMDNSHostname asks the device at addr (ip:5353) for the hostname it
// announces, by sending it a reverse lookup of its own address. mDNS
// responders answer a query from an ordinary port directly to the sender
// (RFC 6762 legacy unicast), so this works from inside a container where
// multicast does not reach the LAN. A sleeping iPhone does not answer.
func lookupMDNSHostname(ctx context.Context, addr string) (string, error) {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return "", err
	}
	ip := net.ParseIP(host).To4()
	if ip == nil {
		return "", fmt.Errorf("not an IPv4 address: %s", host)
	}
	question, err := dnsmessage.NewName(fmt.Sprintf("%d.%d.%d.%d.in-addr.arpa.", ip[3], ip[2], ip[1], ip[0]))
	if err != nil {
		return "", err
	}
	query, err := (&dnsmessage.Message{
		Questions: []dnsmessage.Question{{Name: question, Type: dnsmessage.TypePTR, Class: dnsmessage.ClassINET}},
	}).Pack()
	if err != nil {
		return "", err
	}

	var d net.Dialer
	conn, err := d.DialContext(ctx, "udp", addr)
	if err != nil {
		return "", err
	}
	defer conn.Close()
	if deadline, ok := ctx.Deadline(); ok {
		conn.SetDeadline(deadline)
	}
	if _, err := conn.Write(query); err != nil {
		return "", err
	}
	buf := make([]byte, 9000)
	n, err := conn.Read(buf)
	if err != nil {
		return "", err
	}

	var p dnsmessage.Parser
	if _, err := p.Start(buf[:n]); err != nil {
		return "", err
	}
	if err := p.SkipAllQuestions(); err != nil {
		return "", err
	}
	for {
		h, err := p.AnswerHeader()
		if errors.Is(err, dnsmessage.ErrSectionDone) {
			return "", errNoHostname
		}
		if err != nil {
			return "", err
		}
		if h.Type != dnsmessage.TypePTR {
			if err := p.SkipAnswer(); err != nil {
				return "", err
			}
			continue
		}
		r, err := p.PTRResource()
		if err != nil {
			return "", err
		}
		return strings.TrimSuffix(r.PTR.String(), ".local."), nil
	}
}
