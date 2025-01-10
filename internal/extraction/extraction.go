package extraction

import (
	"context"
	"fmt"
	"time"

	"github.com/cobaltcore-dev/cortex/internal/conf"
	"github.com/go-pg/pg/v10"
)

var steps = []func(db *pg.DB){
	extractNoisyProjects,
}

func extractNoisyProjects(db *pg.DB) {
	// TODO:
	// - Get vrops_virtualmachine_cpu_demand_ratio metrics from the db.
	// - Get projects that show high spikes / high permanent CPU usage.
	// - Get the hosts these projects are currently running on.
	// - Save pairs of (noisy project, host) to the database.
}

func ExtractFeaturesPeriodic() {
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

	for _, step := range steps {
		step(db)
		time.Sleep(30 * time.Minute)
	}
}
