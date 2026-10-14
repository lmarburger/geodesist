# Geodesist - AmpliFi Wi-Fi Metrics Exporter

Geodesist is a Prometheus exporter for AmpliFi Wi-Fi routers that collects bandwidth usage metrics.

## Running

```bash
# Using environment variable
AMPLIFI_PASSWORD="your_password" go run main.go

# Using command line flag
go run main.go --password="your_password"

# Override router address
go run main.go --password="your_password" --router="http://192.168.1.1"
```

## Metrics

- `amplifi_total_tx_bytes{host="..."}` - Total transmitted bytes per client (counter)
- `amplifi_total_rx_bytes{host="..."}` - Total received bytes per client (counter)
- `amplifi_global_tx_bitrate` - Global transmit bitrate (gauge)
- `amplifi_global_rx_bitrate` - Global receive bitrate (gauge)
- `amplifi_clients_count` - Number of connected clients (gauge)
- `amplifi_signal_quality{host="..."}` - Wi-Fi signal quality per client (gauge)
- `amplifi_happiness_score{host="..."}` - Happiness score per client (gauge)- `amplifi_router_up` - Whether the most recent router poll succeeded (gauge)
- `amplifi_last_success_timestamp_seconds` - Unix time of the most recent successful poll (gauge)
- `amplifi_poll_errors_total` - Failed router polls (counter)

## Polling

Geodesist polls the router in the background and `/metrics` serves the latest result, so a scrape never talks to the router. One request is in flight at a time, and after a failure the wait between polls doubles up to `--max-backoff`. The router metrics drop out of `/metrics` once the last success is older than two intervals plus the timeout, so a dead router does not keep reporting its last readings. `amplifi_router_up` and `amplifi_last_success_timestamp_seconds` are always present.

| Flag | Default | Purpose |
|------|---------|---------|
| `--interval` | `5s` | Time between polls |
| `--timeout` | `15s` | Time limit for one poll, including any login |
| `--max-backoff` | `10s` | Longest wait between polls while the router is failing |
