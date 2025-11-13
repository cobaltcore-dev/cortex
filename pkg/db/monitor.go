package db

import (
	"github.com/prometheus/client_golang/prometheus"
)

type monitor struct {
	connectionAttempts *prometheus.CounterVec
	selectTimer        *prometheus.HistogramVec

	// See the sqlstats package for reference: https://github.com/dlmiddlecote/sqlstats
	maxOpenDesc *prometheus.Desc
	openDesc    *prometheus.Desc
	inUseDesc   *prometheus.Desc
	idleDesc    *prometheus.Desc
}

func newMonitor() monitor {
	namespace := "cortex"
	subsystem := "db"
	return monitor{
		connectionAttempts: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: prometheus.BuildFQName(namespace, subsystem, "connection_attempts_total"),
			Help: "Total number of database connection attempts",
		}, []string{"host", "database"}),

		selectTimer: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:    prometheus.BuildFQName(namespace, subsystem, "select_duration_seconds"),
			Help:    "Duration of SELECT queries in seconds",
			Buckets: prometheus.DefBuckets,
		}, []string{"group", "query"}),

		maxOpenDesc: prometheus.NewDesc(
			prometheus.BuildFQName(namespace, subsystem, "connections_max_open"),
			"Maximum number of open connections to the database.",
			[]string{"host", "database"},
			nil,
		),
		openDesc: prometheus.NewDesc(
			prometheus.BuildFQName(namespace, subsystem, "connections_open"),
			"The number of established connections both in use and idle.",
			[]string{"host", "database"},
			nil,
		),
		inUseDesc: prometheus.NewDesc(
			prometheus.BuildFQName(namespace, subsystem, "connections_in_use"),
			"The number of connections currently in use.",
			[]string{"host", "database"},
			nil,
		),
		idleDesc: prometheus.NewDesc(
			prometheus.BuildFQName(namespace, subsystem, "connections_idle"),
			"The number of idle connections.",
			[]string{"host", "database"},
			nil,
		),
	}
}

func (m *monitor) Describe(ch chan<- *prometheus.Desc) {
	m.connectionAttempts.Describe(ch)
	m.selectTimer.Describe(ch)

	ch <- m.maxOpenDesc
	ch <- m.openDesc
	ch <- m.inUseDesc
	ch <- m.idleDesc
}

func (m *monitor) Collect(ch chan<- prometheus.Metric) {
	m.connectionAttempts.Collect(ch)
	m.selectTimer.Collect(ch)

	connections.Range(func(key, value any) bool {
		db := value.(*DB)
		host := db.host
		database := db.databaseName
		stats := db.Db.Stats()
		ch <- prometheus.MustNewConstMetric(
			m.maxOpenDesc,
			prometheus.GaugeValue,
			float64(stats.MaxOpenConnections),
			host, database,
		)
		ch <- prometheus.MustNewConstMetric(
			m.openDesc,
			prometheus.GaugeValue,
			float64(stats.OpenConnections),
			host, database,
		)
		ch <- prometheus.MustNewConstMetric(
			m.inUseDesc,
			prometheus.GaugeValue,
			float64(stats.InUse),
			host, database,
		)
		ch <- prometheus.MustNewConstMetric(
			m.idleDesc,
			prometheus.GaugeValue,
			float64(stats.Idle),
			host, database,
		)
		return true
	})
}
