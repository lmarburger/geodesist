package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"geodesist/geodesist"
	"io/fs"
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
	namesPath  string
}

func main() {
	f := parseFlags()

	client, err := geodesist.NewAmpliFiClient(f.routerAddr, f.password)
	if err != nil {
		log.Fatalf("Failed to create AmpliFi client: %v", err)
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	namer := loadNamer(f.namesPath)
	if namer != nil {
		prometheus.MustRegister(namer)
	}

	poller := geodesist.NewPoller(geodesist.NewAmpliFiCollector(client, namer), geodesist.PollerConfig{
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
	namesPath := flag.String("names", "", "JSON file mapping the hostname a client announces over mDNS to its router name")
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
		namesPath:  *namesPath,
	}
	fmt.Printf("starting geodesist: addr=%q, router=%q, interval=%s, timeout=%s, max_backoff=%s, names=%q\n",
		f.addr, f.routerAddr, f.interval, f.timeout, f.maxBackoff, f.namesPath)

	return f
}

// loadNamer returns nil, disabling renaming, when no names file is configured
// or the configured one does not exist yet. A file that exists but does not
// parse is fatal, so a typo cannot silently turn renaming off.
func loadNamer(path string) *geodesist.Namer {
	if path == "" {
		return nil
	}
	names, err := geodesist.LoadNames(path)
	if errors.Is(err, fs.ErrNotExist) {
		log.Printf("names file %s does not exist; client renaming is off", path)
		return nil
	}
	if err != nil {
		log.Fatal(err)
	}
	log.Printf("renaming clients from %d announced hostnames in %s", len(names), path)
	return geodesist.NewNamer(names)
}
