// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"

	"github.com/cobaltcore-dev/cortex/internal/exporters/schedulingquality/collector"
	"github.com/cobaltcore-dev/cortex/internal/exporters/schedulingquality/nova"
)

func main() {
	var (
		listenAddr     = flag.String("listen-address", ":9199", "address to listen on for metrics")
		metricsPath    = flag.String("metrics-path", "/metrics", "path under which to expose metrics")
		novaEndpoint   = flag.String("nova-endpoint", "", "OpenStack Nova API endpoint (optional; reads OS_AUTH_URL env if unset)")
		scrapeInterval = flag.Duration("scrape-interval", 30*time.Second, "minimum interval between Nova API calls")
		kubeconfig     = flag.String("kubeconfig", "", "path to kubeconfig (uses in-cluster config if unset)")
		isNovaDisabled = flag.Bool("disable-nova", false, "disable Nova flavor lookups; use fixed minimum placeable unit")
		minCPU         = flag.Int64("min-cpu", 1, "minimum placeable CPU cores (used when Nova is disabled)")
		minMemoryMB    = flag.Int64("min-memory-mb", 512, "minimum placeable memory in MB (used when Nova is disabled)")
		logLevel       = flag.String("log-level", "info", "log level (debug, info, warn, error)")
	)
	flag.Parse()

	level := slog.LevelInfo
	switch *logLevel {
	case "debug":
		level = slog.LevelDebug
	case "warn":
		level = slog.LevelWarn
	case "error":
		level = slog.LevelError
	}
	logger := slog.New(slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{Level: level}))
	slog.SetDefault(logger)

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	var novaClient *nova.Client
	if !*isNovaDisabled {
		endpoint := *novaEndpoint
		if endpoint == "" {
			endpoint = os.Getenv("OS_AUTH_URL")
		}
		if endpoint != "" {
			var err error
			novaClient, err = nova.NewClientFromEnv()
			handleError(err, "failed to create nova client")
		}
	}

	k8sClient, err := collector.NewK8sClient(*kubeconfig)
	handleError(err, "failed to create kubernetes client")

	c := collector.New(collector.Options{
		ScrapeInterval: *scrapeInterval,
		NovaClient:     novaClient,
		K8sClient:      k8sClient,
		MinCPU:         *minCPU,
		MinMemoryBytes: *minMemoryMB * 1024 * 1024,
	})
	prometheus.MustRegister(c)

	mux := http.NewServeMux()
	mux.Handle(*metricsPath, promhttp.Handler())
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		fmt.Fprintln(w, "ok")
	})

	srv := &http.Server{
		Addr:         *listenAddr,
		Handler:      mux,
		ReadTimeout:  10 * time.Second,
		WriteTimeout: 30 * time.Second,
	}

	go func() {
		slog.Info("starting cortex-scheduling-quality-exporter", "address", *listenAddr, "path", *metricsPath)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			handleError(err, "server error")
		}
	}()

	<-ctx.Done()
	slog.Info("shutting down")
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer shutdownCancel()
	_ = srv.Shutdown(shutdownCtx)
}

func handleError(err error, message string) {
	if err != nil {
		slog.Error(message, "error", err)
		os.Exit(1)
	}
}
