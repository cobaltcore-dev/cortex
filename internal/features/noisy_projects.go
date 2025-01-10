package features

import (
	"log"

	"github.com/go-pg/pg/v10"
	"github.com/go-pg/pg/v10/orm"
)

type NoisyProject struct {
	//lint:ignore U1000 Ignore unused field warning
	tableName struct{} `pg:"feature_noisy_projects"`
	Project   string   `pg:"project,notnull"`
	Host      string   `pg:"hostsystem,notnull"`
}

func noisyProjectsSchema(db *pg.DB) error {
	if err := db.Model((*NoisyProject)(nil)).CreateTable(&orm.CreateTableOptions{
		IfNotExists: true,
	}); err != nil {
		return err
	}
	return nil
}

func noisyProjectsExtractor(db *pg.DB) error {
	log.Println("Extracting noisy projects")

	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Close()
	if _, err := tx.Exec("DELETE FROM feature_noisy_projects"); err != nil {
		tx.Rollback()
		return err
	}
	query := `
        WITH high_cpu_usage AS (
            SELECT
                m.project AS tenant_id
            FROM metrics m
            WHERE m.name = 'vrops_virtualmachine_cpu_demand_ratio'
            GROUP BY m.project
            HAVING AVG(m.value) > 10.0
        )
        INSERT INTO feature_noisy_projects (project, hostsystem)
        SELECT
            s.tenant_id,
            h.service_host
        FROM openstack_servers s
        JOIN metrics m ON s.id = m.instance_uuid
        JOIN openstack_hypervisors h ON s.os_ext_srv_attr_hypervisor_hostname = h.hostname
        WHERE s.tenant_id IN (SELECT tenant_id FROM high_cpu_usage)
        AND m.name = 'vrops_virtualmachine_cpu_demand_ratio'
        GROUP BY s.tenant_id, h.service_host;
    `
	if _, err := tx.Exec(query); err != nil {
		tx.Rollback()
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}

	// Fetch and display the list of noisy projects.
	var noisyProjects []NoisyProject
	if err := db.Model(&noisyProjects).Select(); err != nil {
		return err
	}
	var hostsByProject = make(map[string][]string)
	for _, p := range noisyProjects {
		hostsByProject[p.Project] = append(hostsByProject[p.Project], p.Host)
	}
	for project, hosts := range hostsByProject {
		log.Printf("Noisy project %s: running on %v\n", project, hosts)
	}
	return nil
}
