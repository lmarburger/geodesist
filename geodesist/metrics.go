package geodesist

import (
	"encoding/json"
	"fmt"
	"github.com/prometheus/client_golang/prometheus"
)

var (
	labels         = []string{"host"}
	txBytesCounter = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "amplifi_total_tx_bytes",
		Help: "Total transmitted bytes per client",
	}, labels)

	rxBytesCounter = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "amplifi_total_rx_bytes",
		Help: "Total received bytes per client",
	}, labels)

	globalTxBitrateGauge = prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "amplifi_global_tx_bitrate",
		Help: "Global transmit bitrate",
	})

	globalRxBitrateGauge = prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "amplifi_global_rx_bitrate",
		Help: "Global receive bitrate",
	})

	clientCountGauge = prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "amplifi_clients_count",
		Help: "Number of connected clients",
	})

	signalQualityGauge = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "amplifi_signal_quality",
		Help: "Wi-Fi signal quality per client",
	}, labels)

	happinessGauge = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "amplifi_happiness_score",
		Help: "Happiness score per client",
	}, labels)
)

func init() {
	prometheus.MustRegister(
		txBytesCounter, rxBytesCounter,
		globalTxBitrateGauge, globalRxBitrateGauge,
		clientCountGauge,
		signalQualityGauge,
		happinessGauge,
	)
}

type AmpliFiCollector struct {
	client          *AmpliFiClient
	previousTxBytes map[string]float64
	previousRxBytes map[string]float64
}

func NewAmpliFiCollector(client *AmpliFiClient) *AmpliFiCollector {
	return &AmpliFiCollector{
		client:          client,
		previousTxBytes: make(map[string]float64),
		previousRxBytes: make(map[string]float64),
	}
}

func (c *AmpliFiCollector) Collect() error {
	data, err := c.client.GetMetrics()
	if err != nil {
		return fmt.Errorf("failed to get metrics: %w", err)
	}

	var response []map[string]interface{}
	if err := json.Unmarshal(data, &response); err != nil {
		return fmt.Errorf("failed to parse metrics: %w", err)
	}

	clientCount := float64(0)
	for i, element := range response {
		// Element 4 contains ethernet stats
		if i == 4 {
			for _, deviceData := range element {
				if deviceMap, ok := deviceData.(map[string]interface{}); ok {
					// The modem is connected to eth-0
					if eth0, exists := deviceMap["eth-0"]; exists {
						if ethData, ok := eth0.(map[string]interface{}); ok {
							if txRate, ok := ethData["tx_bitrate"].(float64); ok {
								globalTxBitrateGauge.Set(txRate)
							}

							if rxRate, ok := ethData["rx_bitrate"].(float64); ok {
								globalRxBitrateGauge.Set(rxRate)
							}
						}
					}
				}
			}
		}

		// Element 1 contains client statistics
		// Structure: element[1] -> access_point -> band -> network_type -> client
		if i == 1 {
			for _, apData := range element {
				if apMap, ok := apData.(map[string]interface{}); ok {
					for _, bandData := range apMap {
						if bandMap, ok := bandData.(map[string]interface{}); ok {
							for networkType, networkData := range bandMap {
								if networkType == "Internal network" {
									continue
								}

								if networkMap, ok := networkData.(map[string]interface{}); ok {
									for _, clientData := range networkMap {
										if clientMap, ok := clientData.(map[string]interface{}); ok {
											description := getStringValue(clientMap, "Description", "unknown")
											clientCount++

											if txBytes, hasTx := clientMap["TxBytes"].(float64); hasTx {
												metric := txBytesCounter.WithLabelValues(description)
												if prevTx, exists := c.previousTxBytes[description]; exists {
													delta := txBytes - prevTx
													if delta > 0 {
														metric.Add(delta)
													}
												} else {
													metric.Add(txBytes)
												}
												c.previousTxBytes[description] = txBytes
											}

											if rxBytes, hasRx := clientMap["RxBytes"].(float64); hasRx {
												metric := rxBytesCounter.WithLabelValues(description)
												if prevRx, exists := c.previousRxBytes[description]; exists {
													delta := rxBytes - prevRx
													if delta > 0 {
														metric.Add(delta)
													}
												} else {
													metric.Add(rxBytes)
												}
												c.previousRxBytes[description] = rxBytes
											}

											if signalQuality, hasSignalQuality := clientMap["SignalQuality"].(float64); hasSignalQuality {
												signalQualityGauge.WithLabelValues(description).Set(signalQuality)
											}

											if happiness, hasHappiness := clientMap["HappinessScore"].(float64); hasHappiness {
												happinessGauge.WithLabelValues(description).Set(happiness)
											}
										}
									}
								}
							}
						}
					}
				}
			}
		}
	}

	clientCountGauge.Set(clientCount)
	return nil
}

// Helper function to safely get string values from map
func getStringValue(m map[string]interface{}, key, defaultValue string) string {
	if val, ok := m[key].(string); ok && val != "" {
		return val
	}
	return defaultValue
}
