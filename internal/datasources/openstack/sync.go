package openstack

import (
	"time"

	"github.com/cobaltcore-dev/cortex/internal/env"
)

var (
	dbHost = env.ForceGetenv("DB_HOST")
	dbPort = env.ForceGetenv("DB_PORT")
	dbUser = env.ForceGetenv("DB_USER")
	dbPass = env.ForceGetenv("DB_PASSWORD")
)

func sync() {

}

func SyncPeriodic(intervalSeconds int) {
	for {
		sync()
		time.Sleep(time.Duration(intervalSeconds) * time.Second)
	}
}
