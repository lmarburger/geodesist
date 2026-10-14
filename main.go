package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"geodesist/geodesist"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

type flags struct {
	addr       string
	routerAddr string
	password   string
	interval   time.Duration
	timeout    time.Duration
	maxBackoff time.Duration
}

func main() {
	f := parseFlags()

	client, err := geodesist.NewAmpliFiClient(f.routerAddr, f.password)
	if err != nil {
		log.Fatalf("Failed to create AmpliFi client: %v", err)
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	poller := geodesist.NewPoller(geodesist.NewAmpliFiCollector(client), geodesist.PollerConfig{
		Interval:   f.interval,
		Timeout:    f.timeout,
		MaxBackoff: f.maxBackoff,
	})
	prometheus.MustRegister(poller)
	go poller.Run(ctx)

	mux := http.NewServeMux()
	mux.Handle("/metrics", promhttp.Handler())
	server := &http.Server{Addr: f.addr, Handler: mux, ReadHeaderTimeout: 10 * time.Second}
	go func() {
		<-ctx.Done()
		server.Close()
	}()

	log.Printf("Starting geodesist on %s", f.addr)
	if err := server.ListenAndServe(); !errors.Is(err, http.ErrServerClosed) {
		log.Fatal(err)
	}
}

func parseFlags() flags {
	addr := flag.String("addr", ":8080", "Listen address for the metrics web server")
	routerAddr := flag.String("router", "http://192.168.119.1", "Address of AmpliFi router website")
	password := flag.String("password", "", "AmpliFi router password")
	interval := flag.Duration("interval", 5*time.Second, "Time between router polls")
	timeout := flag.Duration("timeout", 15*time.Second, "Time limit for one poll, including any login")
	maxBackoff := flag.Duration("max-backoff", 10*time.Second, "Longest wait between polls while the router is failing")
	flag.Parse()

	if *password == "" {
		*password = os.Getenv("AMPLIFI_PASSWORD")
	}

	if *password == "" {
		log.Fatal("Password is required. Use --password flag or AMPLIFI_PASSWORD env var")
	}

	f := flags{
		addr:       *addr,
		routerAddr: *routerAddr,
		password:   *password,
		interval:   *interval,
		timeout:    *timeout,
		maxBackoff: *maxBackoff,
	}
	fmt.Printf("starting geodesist: addr=%q, router=%q, interval=%s, timeout=%s, max_backoff=%s\n",
		f.addr, f.routerAddr, f.interval, f.timeout, f.maxBackoff)

	return f
}
