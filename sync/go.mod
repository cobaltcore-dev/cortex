module github.com/cobaltcore-dev/cortex/sync

go 1.25.0

replace (
	github.com/cobaltcore-dev/cortex/lib => ../lib
	github.com/cobaltcore-dev/cortex/sync/api => ./api
	github.com/cobaltcore-dev/cortex/testlib => ../testlib
)

require (
	github.com/cobaltcore-dev/cortex/lib v0.0.0-00010101000000-000000000000
	github.com/cobaltcore-dev/cortex/sync/api v0.0.0-00010101000000-000000000000
	github.com/cobaltcore-dev/cortex/testlib v0.0.0-20251009112047-8ec59e4f0e57
	github.com/go-gorp/gorp v2.2.0+incompatible
	github.com/gophercloud/gophercloud/v2 v2.8.0
	github.com/sapcc/go-api-declarations v1.17.4
	go.uber.org/automaxprocs v1.6.0
)

require (
	github.com/Azure/go-ansiterm v0.0.0-20250102033503-faa5f7b0171c // indirect
	github.com/Microsoft/go-winio v0.6.2 // indirect
	github.com/Nvveen/Gotty v0.0.0-20120604004816-cd527374f1e5 // indirect
	github.com/cenkalti/backoff v2.2.1+incompatible // indirect
	github.com/containerd/continuity v0.4.5 // indirect
	github.com/dlmiddlecote/sqlstats v1.0.2 // indirect
	github.com/docker/go-connections v0.6.0 // indirect
	github.com/docker/go-units v0.5.0 // indirect
	github.com/eclipse/paho.mqtt.golang v1.5.1 // indirect
	github.com/golang-migrate/migrate/v4 v4.19.0 // indirect
	github.com/gorilla/websocket v1.5.4-0.20250319132907-e064f32e3674 // indirect
	github.com/hashicorp/errwrap v1.1.0 // indirect
	github.com/hashicorp/go-multierror v1.1.1 // indirect
	github.com/kylelemons/godebug v1.1.0 // indirect
	github.com/lib/pq v1.10.9 // indirect
	github.com/mattn/go-sqlite3 v1.14.32 // indirect
	github.com/moby/sys/user v0.4.0 // indirect
	github.com/opencontainers/go-digest v1.0.0 // indirect
	github.com/opencontainers/image-spec v1.1.1 // indirect
	github.com/opencontainers/runc v1.3.2 // indirect
	github.com/ory/dockertest v3.3.5+incompatible // indirect
	github.com/sirupsen/logrus v1.9.3 // indirect
	go.yaml.in/yaml/v2 v2.4.3 // indirect
)

require (
	github.com/beorn7/perks v1.0.1 // indirect
	github.com/cespare/xxhash/v2 v2.3.0 // indirect
	github.com/munnerz/goautoneg v0.0.0-20191010083416-a7dc8b61c822 // indirect
	github.com/pkg/errors v0.9.1 // indirect
	github.com/prometheus/client_golang v1.23.2
	github.com/prometheus/client_model v0.6.2
	github.com/prometheus/common v0.67.1 // indirect
	github.com/prometheus/procfs v0.17.0 // indirect
	github.com/sapcc/go-bits v0.0.0-20251008145151-92546a8461e7
	golang.org/x/net v0.46.0 // indirect
	golang.org/x/sync v0.17.0 // indirect
	golang.org/x/sys v0.37.0 // indirect
	google.golang.org/protobuf v1.36.10 // indirect
)
