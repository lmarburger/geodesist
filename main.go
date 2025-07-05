package main

import (
	"flag"
	"fmt"
	"geodesist/geodesist"
	"log"
	"net/http"
	"os"

	"github.com/prometheus/client_golang/prometheus/promhttp"
)

type flags struct {
	addr       string
	routerAddr string
	password   string
}

func main() {
	f := parseFlags()

	client, err := geodesist.NewAmpliFiClient(f.routerAddr, f.password)
	if err != nil {
		log.Fatalf("Failed to create AmpliFi client: %v", err)
	}

	collector := geodesist.NewAmpliFiCollector(client)

	http.HandleFunc("/metrics", func(w http.ResponseWriter, r *http.Request) {
		if err := collector.Collect(); err != nil {
			log.Printf("Failed to collect metrics: %v", err)
			http.Error(w, "Failed to collect metrics", http.StatusInternalServerError)
			return
		}

		promhttp.Handler().ServeHTTP(w, r)
	})

	log.Printf("Starting geodesist on %s", f.addr)
	log.Fatal(http.ListenAndServe(f.addr, nil))
}

func parseFlags() flags {
	addr := flag.String("addr", ":8080", "Listen address for the metrics web server")
	routerAddr := flag.String("router", "http://192.168.119.1", "Address of AmpliFi router website")
	password := flag.String("password", "", "AmpliFi router password")
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
	}
	fmt.Printf("starting geodesist: addr=%q, router=%q\n", f.addr, f.routerAddr)

	return f
}
