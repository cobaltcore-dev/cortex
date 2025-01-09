package main

import (
	"fmt"
	"os"

	"github.com/cobaltcore-dev/cortex/internal/datasources/prometheus"
)

func main() {
	data, err := prometheus.FetchMetrics(
		os.Getenv("PROMETHEUS_URL"),
		"vrops_virtualmachine_cpu_demand_ratio",
		1*24*60*60,
		1*60*60,
	)
	if err != nil {
		panic(err)
	}
	for _, metric := range data.Metrics {
		// Print out some nice stats
		fmt.Printf("Metric: %s\n", metric.Meta.VirtualMachine)
		fmt.Printf("Duration: %s\n", data.Duration)
		fmt.Printf("Start: %s\n", data.Start)
		fmt.Printf("End: %s\n", data.End)
		fmt.Printf("Values: %v\n", metric.Values)
	}
}
