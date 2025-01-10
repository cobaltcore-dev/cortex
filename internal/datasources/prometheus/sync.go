package prometheus

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/cobaltcore-dev/cortex/internal/conf"

	"github.com/go-pg/pg/v10"
	"github.com/go-pg/pg/v10/orm"
)

func getSyncPeriod(db *pg.DB, metricName string) (time.Time, time.Time, error) {
	// Check if there are any metrics in the database.
	var nRows int
	if _, err := db.QueryOne(
		pg.Scan(&nRows),
		"SELECT COUNT(*) FROM metrics WHERE name = ?",
		metricName,
	); err != nil {
		return time.Time{}, time.Time{}, fmt.Errorf("failed to count rows: %v", err)
	}
	log.Printf("Number of rows for %s: %d\n", metricName, nRows)
	if nRows == 0 {
		// No metrics in the database yet. Start 4 weeks in the past.
		start := time.Now().Add(-4 * 7 * 24 * time.Hour)
		end := start.Add(24 * time.Hour)
		return start, end, nil
	}
	var latestTimestamp time.Time
	if _, err := db.QueryOne(
		pg.Scan(&latestTimestamp),
		"SELECT MAX(timestamp) FROM metrics WHERE name = ?",
		metricName,
	); err != nil {
		return time.Time{}, time.Time{}, fmt.Errorf("failed to get latest timestamp: %v", err)
	}
	if latestTimestamp.IsZero() {
		return time.Time{}, time.Time{}, fmt.Errorf("latestTimestamp is zero")
	}
	log.Printf("Latest timestamp for %s: %s\n", metricName, latestTimestamp)
	if time.Since(latestTimestamp) < time.Hour {
		// Already synced within the last hour. Throw an error.
		return time.Time{}, time.Time{}, fmt.Errorf("already synced within the last hour")
	}
	start := latestTimestamp
	end := start.Add(24 * time.Hour)
	return start, end, nil
}

func sync(db *pg.DB, metricName string) {
	start, end, err := getSyncPeriod(db, metricName)
	if err != nil {
		fmt.Printf("Skipping sync for %s: %v\n", metricName, err)
		return
	}
	log.Printf("Syncing %s from %s to %s\n", metricName, start, end)
	// Drop all metrics that are older than 4 weeks.
	if _, err := db.Exec(
		"DELETE FROM metrics WHERE name = ? AND timestamp < ?",
		metricName,
		time.Now().Add(-4*7*24*time.Hour),
	); err != nil {
		fmt.Printf("Failed to delete old metrics: %v\n", err)
		return
	}
	log.Printf("Deleted old metrics for %s\n", metricName)
	// Fetch the metrics from Prometheus.
	prometheusData, err := fetchMetrics(
		conf.Get().PrometheusUrl,
		metricName,
		start,
		end,
		// Needs to be larger than the sampling rate of the metric.
		12*60*60, // 12 hours resolution
	)
	if err != nil {
		fmt.Printf("Failed to fetch metrics: %v\n", err)
		return
	}
	db.Model(&prometheusData.Metrics).Insert()
	log.Printf("Fetched and inserted %d metrics for %s\n", len(prometheusData.Metrics), metricName)
}

func createSchema(db *pg.DB) error {
	models := []interface{}{
		(*PrometheusMetric)(nil),
	}
	for _, model := range models {
		if err := db.Model(model).CreateTable(&orm.CreateTableOptions{
			IfNotExists: true,
		}); err != nil {
			return err
		}
	}
	return nil
}

func SyncPeriodic() {
	c := conf.Get()
	db := pg.Connect(&pg.Options{
		Addr:     fmt.Sprintf("%s:%s", c.DBHost, c.DBPort),
		User:     c.DBUser,
		Password: c.DBPass,
		Database: "postgres",
	})
	defer db.Close()

	// Poll until the database is alive
	ctx := context.Background()
	for {
		if err := db.Ping(ctx); err == nil {
			break
		}
		time.Sleep(time.Second * 1)
	}

	if err := createSchema(db); err != nil {
		log.Fatal(err)
	}

	for {
		sync(db, "vrops_virtualmachine_cpu_demand_ratio")
		time.Sleep(5 * time.Second)
	}
}
