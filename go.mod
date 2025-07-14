module github.com/cobaltcore-dev/cortex

go 1.24

replace github.com/cobaltcore-dev/cortex/commands => ./commands

replace github.com/cobaltcore-dev/cortex/testlib => ./testlib

require (
	github.com/dlmiddlecote/sqlstats v1.0.2
	github.com/eclipse/paho.mqtt.golang v1.5.0
	github.com/go-gorp/gorp v2.2.0+incompatible
	github.com/gophercloud/gophercloud/v2 v2.7.0
	github.com/lib/pq v1.10.9
	github.com/mattn/go-sqlite3 v1.14.28
	github.com/ory/dockertest v3.3.5+incompatible
	github.com/prometheus/client_golang v1.22.0
	github.com/prometheus/client_model v0.6.2
	github.com/sapcc/go-api-declarations v1.16.0
	github.com/sapcc/go-bits v0.0.0-20250710190843-788fa8ba727b
	go.uber.org/automaxprocs v1.6.0
)

require (
	github.com/Microsoft/go-winio v0.6.2 // indirect
	github.com/Nvveen/Gotty v0.0.0-20120604004816-cd527374f1e5 // indirect
	github.com/cenkalti/backoff v2.2.1+incompatible // indirect
	github.com/containerd/continuity v0.4.5 // indirect
	github.com/docker/go-connections v0.5.0 // indirect
	github.com/docker/go-units v0.5.0 // indirect
	github.com/go-sql-driver/mysql v1.9.2 // indirect
	github.com/gotestyourself/gotestyourself v2.2.0+incompatible // indirect
	github.com/moby/sys/user v0.4.0 // indirect
	github.com/opencontainers/go-digest v1.0.0 // indirect
	github.com/opencontainers/image-spec v1.1.1 // indirect
	github.com/opencontainers/runc v1.3.0 // indirect
	github.com/pkg/errors v0.9.1 // indirect
	github.com/poy/onpar v1.1.2 // indirect
	github.com/sirupsen/logrus v1.9.3 // indirect
	github.com/ziutek/mymysql v1.5.4 // indirect
	golang.org/x/net v0.40.0 // indirect
	gotest.tools v2.2.0+incompatible // indirect
)

require (
	github.com/Azure/go-ansiterm v0.0.0-20250102033503-faa5f7b0171c // indirect
	github.com/beorn7/perks v1.0.1 // indirect
	github.com/cespare/xxhash/v2 v2.3.0 // indirect
	github.com/golang-migrate/migrate/v4 v4.18.3 // indirect
	github.com/gorilla/websocket v1.5.3 // indirect
	github.com/hashicorp/errwrap v1.1.0 // indirect
	github.com/hashicorp/go-multierror v1.1.1 // indirect
	github.com/kylelemons/godebug v1.1.0 // indirect
	github.com/munnerz/goautoneg v0.0.0-20191010083416-a7dc8b61c822 // indirect
	github.com/prometheus/common v0.65.0 // indirect
	github.com/prometheus/procfs v0.16.1 // indirect
	go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp v0.60.0 // indirect
	go.opentelemetry.io/otel/trace v1.35.0 // indirect
	go.uber.org/atomic v1.11.0 // indirect
	golang.org/x/sync v0.14.0 // indirect
	golang.org/x/sys v0.33.0 // indirect
	google.golang.org/protobuf v1.36.6 // indirect
)
