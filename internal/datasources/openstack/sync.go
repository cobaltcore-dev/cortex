package openstack

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/cobaltcore-dev/cortex/internal/conf"

	"github.com/go-pg/pg/v10"
	"github.com/go-pg/pg/v10/orm"
)

func sync(db *pg.DB) {
	log.Printf("Syncing OpenStack data with %s\n", conf.Get().OSAuthUrl)
	auth, err := getKeystoneAuth()
	if err != nil {
		log.Printf("Failed to authenticate: %v\n", err)
		return
	}
	serverlist, err := getServers(auth)
	if err != nil {
		log.Printf("Failed to get servers: %v\n", err)
		return
	}
	hypervisorlist, err := getHypervisors(auth)
	if err != nil {
		log.Printf("Failed to get hypervisors: %v\n", err)
		return
	}
	db.Model(&serverlist.Servers).Insert()
	db.Model(&hypervisorlist.Hypervisors).Insert()
	log.Printf("Synced %d servers and %d hypervisors\n", len(serverlist.Servers), len(hypervisorlist.Hypervisors))
}

func createSchema(db *pg.DB) error {
	models := []interface{}{
		(*OpenStackServer)(nil),
		(*OpenStackHypervisor)(nil),
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
		sync(db)
		// Won't change that often.
		time.Sleep(30 * time.Minute)
	}
}
