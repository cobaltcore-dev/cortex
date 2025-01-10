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

func getSyncWindowStart(db *pg.DB, metricName string) (time.Time, error) {
	// Check if there are any metrics in the database.
	var nRows int
	if _, err := db.QueryOne(
		pg.Scan(&nRows),
		"SELECT COUNT(*) FROM metrics WHERE name = ?",
		metricName,
	); err != nil {
		return time.Time{}, fmt.Errorf("failed to count rows: %v", err)
	}
	log.Printf("Number of rows for %s: %d\n", metricName, nRows)
	if nRows == 0 {
		// No metrics in the database yet. Start 4 weeks in the past.
		start := time.Now().Add(-4 * 7 * 24 * time.Hour)
		return start, nil
	}
	var latestTimestamp time.Time
	if _, err := db.QueryOne(
		pg.Scan(&latestTimestamp),
		"SELECT MAX(timestamp) FROM metrics WHERE name = ?",
		metricName,
	); err != nil {
		return time.Time{}, fmt.Errorf("failed to get latest timestamp: %v", err)
	}
	if latestTimestamp.IsZero() {
		return time.Time{}, fmt.Errorf("latestTimestamp is zero")
	}
	log.Printf("Latest timestamp for %s: %s\n", metricName, latestTimestamp)
	return latestTimestamp, nil
}

func sync(
	db *pg.DB,
	start time.Time,
	interval time.Duration,
	resolutionSeconds int,
	metricName string,
) {
	// Sync full days only.
	end := start.Add(interval)
	if start.After(time.Now()) || end.After(time.Now()) {
		return // Finished syncing.
	}

	log.Printf("Syncing %s from %s to %s\n", metricName, start, end)
	// Drop all metrics that are older than 4 weeks.
	result, err := db.Exec(
		"DELETE FROM metrics WHERE name = ? AND timestamp < ?",
		metricName,
		time.Now().Add(-4*7*24*time.Hour),
	)
	if err != nil {
		fmt.Printf("Failed to delete old metrics: %v\n", err)
		return
	}
	log.Printf("Deleted %d old metrics for %s\n", result.RowsAffected(), metricName)
	// Fetch the metrics from Prometheus.
	prometheusData, err := fetchMetrics(
		conf.Get().PrometheusUrl,
		metricName,
		start,
		end,
		resolutionSeconds,
	)
	if err != nil {
		fmt.Printf("Failed to fetch metrics: %v\n", err)
		return
	}
	db.Model(&prometheusData.Metrics).Insert()
	log.Printf("Fetched and inserted %d metrics for %s\n", len(prometheusData.Metrics), metricName)

	// Don't overload the Prometheus server.
	time.Sleep(1 * time.Second)
	// Continue syncing.
	sync(db, end, interval, resolutionSeconds, metricName)
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

	metrics := []string{
		"vrops_virtualmachine_cpu_demand_ratio",
	}

	for {
		for _, metricName := range metrics {
			// Sync this metric until we are caught up.
			start, err := getSyncWindowStart(db, metricName)
			if err != nil {
				log.Printf("Failed to get %s sync window start: %v\n", metricName, err)
				continue
			}
			sync(
				db,
				start,
				24*time.Hour,
				// Needs to be larger than the sampling rate of the metric.
				12*60*60, // 12 hours (2 datapoints per day per metric)
				metricName,
			)
		}
		// Check hourly if we need to catch up again.
		time.Sleep(1 * time.Hour)
	}
}
