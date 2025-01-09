package openstack

import (
	"database/sql"
	"fmt"
	"log"
	"time"
)

type OpenStackSyncConfig struct {
	OSAuthUrl           string
	OSUsername          string
	OSPassword          string
	OSProjectName       string
	OSUserDomainName    string
	OSProjectDomainName string
	DbHost              string
	DbPort              string
	DbUser              string
	DbPass              string
}

// Note: Not all fields are used at the moment.
var openStackServerTableDefinition = `
	CREATE TABLE IF NOT EXISTS os_server (
		id VARCHAR(255) PRIMARY KEY,
		name VARCHAR(255),
		status VARCHAR(255),
		tenant_id VARCHAR(255),
		user_id VARCHAR(255),
		host_id VARCHAR(255),
		created TIMESTAMP,
		updated TIMESTAMP,
		os_ext_availability_zone VARCHAR(255),
		os_ext_srv_attr_host VARCHAR(255),
		os_ext_srv_attr_instance_name VARCHAR(255),
		os_ext_srv_attr_hypervisor_hostname VARCHAR(255),
		os_ext_sts_task_state VARCHAR(255),
		os_ext_sts_vm_state VARCHAR(255),
		os_ext_sts_power_state INT
	);
`

// Note: Not all fields are used at the moment.
var openStackHypervisorTableDefinition = `
	CREATE TABLE IF NOT EXISTS os_hypervisor (
		id INT PRIMARY KEY,
		hypervisor_hostname VARCHAR(255),
		state VARCHAR(255),
		status VARCHAR(255),
		hypervisor_type VARCHAR(255),
		hypervisor_version INT,
		host_ip VARCHAR(255),
		service_id INT,
		service_host VARCHAR(255),
		service_disabled_reason VARCHAR(255),
		vcpus INT,
		memory_mb INT,
		local_gb INT,
		vcpus_used INT,
		memory_mb_used INT,
		local_gb_used INT,
		free_ram_mb INT,
		free_disk_gb INT,
		current_workload INT,
		running_vms INT,
		disk_available_least INT
	);
`

func sync(conf OpenStackSyncConfig, db *sql.DB) {
	log.Printf("Syncing OpenStack data with %s\n", conf.OSAuthUrl)
	auth, err := getKeystoneAuth(conf)
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

	tx, err := db.Begin()
	if err != nil {
		log.Printf("Failed to begin transaction: %v\n", err)
		return
	}

	for _, server := range serverlist.Servers {
		_, err := tx.Exec(`
            INSERT INTO open_stack_server (
				id,
				name,
				status,
				tenant_id,
				user_id,
				host_id,
				created,
				updated,
				os_ext_availability_zone,
				os_ext_srv_attr_host,
				os_ext_srv_attr_instance_name,
				os_ext_srv_attr_hypervisor_hostname,
				os_ext_sts_task_state,
				os_ext_sts_vm_state,
				os_ext_sts_power_state
			) VALUES (
				$1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15
			) ON CONFLICT (id) DO UPDATE SET
			    name = EXCLUDED.name,
				status = EXCLUDED.status,
				tenant_id = EXCLUDED.tenant_id,
				user_id = EXCLUDED.user_id,
				host_id = EXCLUDED.host_id,
				created = EXCLUDED.created,
				updated = EXCLUDED.updated,
				os_ext_availability_zone = EXCLUDED.os_ext_availability_zone,
				os_ext_srv_attr_host = EXCLUDED.os_ext_srv_attr_host,
				os_ext_srv_attr_instance_name = EXCLUDED.os_ext_srv_attr_instance_name,
				os_ext_srv_attr_hypervisor_hostname = EXCLUDED.os_ext_srv_attr_hypervisor_hostname,
				os_ext_sts_task_state = EXCLUDED.os_ext_sts_task_state,
				os_ext_sts_vm_state = EXCLUDED.os_ext_sts_vm_state,
				os_ext_sts_power_state = EXCLUDED.os_ext_sts_power_state
			`,
			server.ID,
			server.Name,
			server.Status,
			server.TenantID,
			server.UserID,
			server.HostID,
			server.Created,
			server.Updated,
			server.OSEXTAvailabilityZone,
			server.OSEXTSRVATTRHost,
			server.OSEXTSRVATTRInstanceName,
			server.OSEXTSRVATTRHypervisorHostname,
			server.OSEXTSTSTaskState,
			server.OSEXTSTSVmState,
			server.OSEXTSTSPowerState,
		)

		if err != nil {
			tx.Rollback()
			log.Printf("Failed to insert/update server: %v\n", err)
			return
		}
	}

	for _, hypervisor := range hypervisorlist.Hypervisors {
		_, err := tx.Exec(`
			INSERT INTO open_stack_hypervisor (
				id,
				hypervisor_hostname,
				state,
				status,
				hypervisor_type,
				hypervisor_version,
				host_ip,
				service_id,
				service_host,
				service_disabled_reason,
				vcpus,
				memory_mb,
				local_gb,
				vcpus_used,
				memory_mb_used,
				local_gb_used,
				free_ram_mb,
				free_disk_gb,
				current_workload,
				running_vms,
				disk_available_least
			) VALUES (
				$1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15,
				$16, $17, $18, $19, $20, $21
			) ON CONFLICT (id) DO UPDATE SET
				hypervisor_hostname = EXCLUDED.hypervisor_hostname,
                state = EXCLUDED.state,
                status = EXCLUDED.status,
                hypervisor_type = EXCLUDED.hypervisor_type,
                hypervisor_version = EXCLUDED.hypervisor_version,
                host_ip = EXCLUDED.host_ip,
                service_id = EXCLUDED.service_id,
                service_host = EXCLUDED.service_host,
                service_disabled_reason = EXCLUDED.service_disabled_reason,
                vcpus = EXCLUDED.vcpus,
                memory_mb = EXCLUDED.memory_mb,
                local_gb = EXCLUDED.local_gb,
                vcpus_used = EXCLUDED.vcpus_used,
                memory_mb_used = EXCLUDED.memory_mb_used,
                local_gb_used = EXCLUDED.local_gb_used,
                free_ram_mb = EXCLUDED.free_ram_mb,
                free_disk_gb = EXCLUDED.free_disk_gb,
                current_workload = EXCLUDED.current_workload,
                running_vms = EXCLUDED.running_vms,
                disk_available_least = EXCLUDED.disk_available_least
            `,
			hypervisor.ID,
			hypervisor.Hostname,
			hypervisor.State,
			hypervisor.Status,
			hypervisor.HypervisorType,
			hypervisor.HypervisorVersion,
			hypervisor.HostIP,
			hypervisor.Service.ID,
			hypervisor.Service.Host,
			hypervisor.Service.DisabledReason,
			hypervisor.VCPUs,
			hypervisor.MemoryMB,
			hypervisor.LocalGB,
			hypervisor.VCPUsUsed,
			hypervisor.MemoryMBUsed,
			hypervisor.LocalGBUsed,
			hypervisor.FreeRAMMB,
			hypervisor.FreeDiskGB,
			hypervisor.CurrentWorkload,
			hypervisor.RunningVMs,
			hypervisor.DiskAvailableLeast,
		)

		if err != nil {
			tx.Rollback()
			log.Printf("Failed to insert/update hypervisor: %v\n", err)
			return
		}
	}

	err = tx.Commit()
	if err != nil {
		log.Printf("Failed to commit transaction: %v\n", err)
		return
	}

	log.Printf("Synced %d servers and %d hypervisors\n", len(serverlist.Servers), len(hypervisorlist.Hypervisors))
}

func SyncPeriodic(conf OpenStackSyncConfig) {
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

	_, err = db.Exec(openStackServerTableDefinition)
	if err != nil {
		log.Fatal(err)
	}
	_, err = db.Exec(openStackHypervisorTableDefinition)
	if err != nil {
		log.Fatal(err)
	}

	for {
		sync(conf, db)
		// Won't change that often.
		time.Sleep(30 * time.Minute)
	}
}
