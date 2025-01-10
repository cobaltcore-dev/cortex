package openstack

import (
	"log"

	"github.com/cobaltcore-dev/cortex/internal/conf"
	"github.com/cobaltcore-dev/cortex/internal/db"

	"github.com/go-pg/pg/v10/orm"
)

func Init() {
	models := []any{
		(*OpenStackServer)(nil),
		(*OpenStackHypervisor)(nil),
	}
	for _, model := range models {
		if err := db.DB.Model(model).CreateTable(&orm.CreateTableOptions{
			IfNotExists: true,
		}); err != nil {
			log.Fatal(err)
		}
	}
}

func Sync() {
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
	db.DB.Model(&serverlist.Servers).
		OnConflict("(id) DO UPDATE").
		Insert()
	db.DB.Model(&hypervisorlist.Hypervisors).
		OnConflict("(id) DO UPDATE").
		Insert()
	log.Printf("Synced %d servers and %d hypervisors\n", len(serverlist.Servers), len(hypervisorlist.Hypervisors))
}
