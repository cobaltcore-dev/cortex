package prometheus

import (
	"database/sql"
	"fmt"
	"log"
	"strconv"
	"time"

	_ "github.com/lib/pq"
)

type PrometheusSyncConfig struct {
	PrometheusUrl string
	DbHost        string
	DbPort        string
	DbUser        string
	DbPass        string
}

var prometheusMetricTableDefinition = `
    CREATE TABLE IF NOT EXISTS metrics (
        id SERIAL PRIMARY KEY,
        name TEXT NOT NULL,
        cluster TEXT,
        cluster_type TEXT,
        collector TEXT,
        datacenter TEXT,
        hostsystem TEXT,
        instance_uuid TEXT,
        internal_name TEXT,
        job TEXT,
        project TEXT,
        prometheus TEXT,
        region TEXT,
        vccluster TEXT,
        vcenter TEXT,
        virtualmachine TEXT,
        value FLOAT NOT NULL,
        timestamp TIMESTAMP NOT NULL
    );
`

func getSyncPeriod(db *sql.DB, metricName string) (time.Time, time.Time, error) {
	// Check if there are any metrics in the database.
	var nRows int
	err := db.QueryRow(
		"SELECT COUNT(*) FROM metrics WHERE name = $1",
		metricName,
	).Scan(&nRows)
	if err != nil {
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
	err = db.QueryRow(
		"SELECT MAX(timestamp) FROM metrics WHERE name = $1",
		metricName,
	).Scan(&latestTimestamp)
	if err != nil {
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

func sync(conf PrometheusSyncConfig, db *sql.DB, metricName string) {
	start, end, err := getSyncPeriod(db, metricName)
	if err != nil {
		fmt.Printf("Skipping sync for %s: %v\n", metricName, err)
		return
	}
	log.Printf("Syncing %s from %s to %s\n", metricName, start, end)
	// Drop all metrics that are older than 4 weeks.
	_, err = db.Exec(
		"DELETE FROM metrics WHERE name = $1 AND timestamp < $2",
		metricName,
		time.Now().Add(-4*7*24*time.Hour),
	)
	if err != nil {
		fmt.Printf("Failed to delete old metrics: %v\n", err)
		return
	}
	log.Printf("Deleted old metrics for %s\n", metricName)
	// Fetch the metrics from Prometheus.
	prometheusData, err := fetchMetrics(
		conf.PrometheusUrl,
		metricName,
		start,
		end,
		// Needs to be larger than the sampling rate of the metric.
		6*60*60, // 6 hours resolution
	)
	if err != nil {
		fmt.Printf("Failed to fetch metrics: %v\n", err)
		return
	}
	log.Printf("Fetched %d metrics for %s\n", len(prometheusData.Metrics), metricName)
	// Insert the metrics into the database using a transaction.
	tx, err := db.Begin()
	if err != nil {
		fmt.Printf("Failed to begin transaction: %v\n", err)
		return
	}
	defer tx.Rollback()

	stmt, err := tx.Prepare(`
        INSERT INTO metrics (
            name,
            cluster,
            cluster_type,
            collector,
            datacenter,
            hostsystem,
            instance_uuid,
            internal_name,
            job,
            project,
            prometheus,
            region,
            vccluster,
            vcenter,
            virtualmachine,
            value,
            timestamp
        ) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17)
    `)
	if err != nil {
		fmt.Printf("Failed to prepare statement: %v\n", err)
		return
	}
	defer stmt.Close()

	var insertedMetrics int
	for _, metric := range prometheusData.Metrics {
		for _, value := range metric.Values {
			valFloat, err := strconv.ParseFloat(value[1].(string), 64)
			if err != nil {
				log.Fatal(err)
			}
			_, err = stmt.Exec(
				metric.Metric.Name,
				metric.Metric.Cluster,
				metric.Metric.ClusterType,
				metric.Metric.Collector,
				metric.Metric.Datacenter,
				metric.Metric.HostSystem,
				metric.Metric.InstanceUUID,
				metric.Metric.InternalName,
				metric.Metric.Job,
				metric.Metric.Project,
				metric.Metric.Prometheus,
				metric.Metric.Region,
				metric.Metric.VCCluster,
				metric.Metric.VCenter,
				metric.Metric.VirtualMachine,
				valFloat,
				time.Unix(int64(value[0].(float64)), 0),
			)
			if err != nil {
				fmt.Printf("Failed to insert metric: %v\n", err)
				return
			}
			insertedMetrics++
		}
	}

	err = tx.Commit()
	if err != nil {
		fmt.Printf("Failed to commit transaction: %v\n", err)
		return
	}

	log.Printf("Inserted %d metrics for %s\n", insertedMetrics, metricName)
}

func SyncPeriodic(conf PrometheusSyncConfig) {
	db, err := sql.Open("postgres", fmt.Sprintf(
		"host=%s port=%s user=%s password=%s dbname=postgres sslmode=disable",
		conf.DbHost,
		conf.DbPort,
		conf.DbUser,
		conf.DbPass,
	))
	if err != nil {
		log.Fatal(err)
	}
	defer db.Close()

	_, err = db.Exec(prometheusMetricTableDefinition)
	if err != nil {
		log.Fatal(err)
	}

	for {
		sync(conf, db, "vrops_virtualmachine_cpu_demand_ratio")
		time.Sleep(5 * time.Second)
	}
}
