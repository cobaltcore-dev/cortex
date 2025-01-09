package extraction

import (
	"database/sql"
	"fmt"
	"log"
	"time"

	_ "github.com/lib/pq"
)

type ExtractionConfig struct {
	DbHost string
	DbPort string
	DbUser string
	DbPass string
}

var steps = []func(db *sql.DB){
	extractNoisyProjects,
}

func extractNoisyProjects(db *sql.DB) {
	// TODO:
	// - Get vrops_virtualmachine_cpu_demand_ratio metrics from the db.
	// - Get projects that show high spikes / high permanent CPU usage.
	// - Get the hosts these projects are currently running on.
	// - Save pairs of (noisy project, host) to the database.
}

func ExtractFeaturesPeriodic(conf ExtractionConfig) {
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

	for _, step := range steps {
		step(db)
		time.Sleep(30 * time.Minute)
	}
}
