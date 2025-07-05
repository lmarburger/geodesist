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
- `amplifi_happiness_score{host="..."}` - Happiness score per client (gauge)