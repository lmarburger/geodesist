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

- `amplifi_total_tx_bytes{host="..."}` - Bytes transmitted per client, as the router counts them (counter)
- `amplifi_total_rx_bytes{host="..."}` - Bytes received per client, as the router counts them (counter)
- `amplifi_global_tx_bitrate` - Global transmit bitrate (gauge)
- `amplifi_global_rx_bitrate` - Global receive bitrate (gauge)
- `amplifi_clients_count` - Number of connected clients (gauge)
- `amplifi_signal_quality{host="..."}` - Wi-Fi signal quality per client (gauge)
- `amplifi_happiness_score{host="..."}` - Happiness score per client (gauge)
- `amplifi_router_up` - Whether the most recent router poll succeeded (gauge)
- `amplifi_last_success_timestamp_seconds` - Unix time of the most recent successful poll (gauge)
- `amplifi_poll_errors_total` - Failed router polls (counter)
- `amplifi_client_renames_total` - Clients renamed from their announced hostname (counter, only with `--rename`)

`host` is the client's name in the router, or `unknown` when it has none. Only clients connected at the last poll are exported, so a device that leaves drops out instead of holding its last values. Clients that share a name are combined: bytes are summed and gauges take the highest value.

The router's 32-bit byte counters stop at 4294967295. Once a counter hits that, the router also sends a 64-bit one (`RxBytes64`, `TxBytes64`), which geodesist uses instead.

## Polling

Geodesist polls the router in the background and `/metrics` serves the latest result, so a scrape never talks to the router. One request is in flight at a time, and after a failure the wait between polls doubles up to `--max-backoff`. The router metrics drop out of `/metrics` once the last success is older than two intervals plus the timeout, so a dead router does not keep reporting its last readings. `amplifi_router_up` and `amplifi_last_success_timestamp_seconds` are always present.

| Flag | Default | Purpose |
|------|---------|---------|
| `--interval` | `5s` | Time between polls |
| `--timeout` | `15s` | Time limit for one poll, including any login |
| `--max-backoff` | `10s` | Longest wait between polls while the router is failing |
| `--rename` | off | Rename private-MAC clients to their announced hostname; see below |

## Client renaming

The router stores client names by MAC address. iPhones and iPads with a rotating private Wi-Fi address periodically show up under a new MAC, so the router forgets their names and lists them as `iPhone`, `iPad`, or nothing.

With `--rename`, geodesist names every client that uses a private MAC after the hostname it announces over mDNS, found with a unicast reverse lookup of the client's own IP. That hostname comes from the name set on the device (`Tricias-iPad`), so the device gets the same name after every rotation and its graphs continue under one label. Clients with a hardware MAC keep whatever name they have in the router and are never probed. There is no configuration: a new phone is named the first time it answers.

A locked iPhone answers only briefly: about 13 seconds after its screen wakes, or about 25 seconds after it is unlocked and locked again. So until a private-MAC client's name is confirmed, it is probed on every poll. Once confirmed it is left alone while it stays connected, unless its name in the router changes, in which case it is checked and put back. A failed rename is retried after a minute.
