# Copyright 2025 SAP SE
# SPDX-License-Identifier: Apache-2.0

# For Pylance to not complain around:
# type: ignore

# Don't track us.
analytics_settings(False)

# The upgrade job may take a long time to run, so it is disabled by default.
enable_postgres_upgrade = False

if not os.getenv('TILT_VALUES_PATH'):
    fail("TILT_VALUES_PATH is not set.")
if not os.path.exists(os.getenv('TILT_VALUES_PATH')):
    fail("TILT_VALUES_PATH "+ os.getenv('TILT_VALUES_PATH') + " does not exist.")

load('ext://helm_resource', 'helm_resource', 'helm_repo')
helm_repo(
    'Bitnami Helm Repo',
    'https://charts.bitnami.com/bitnami',
    labels=['Repositories'],
)
helm_repo(
    'Prometheus Community Helm Repo',
    'https://prometheus-community.github.io/helm-charts',
    labels=['Repositories'],
)

########### Cortex Core Services
tilt_values = os.getenv('TILT_VALUES_PATH')
docker_build('ghcr.io/cobaltcore-dev/cortex', '.', only=[
    'internal/', 'commands/', 'main.go', 'go.mod', 'go.sum', 'Makefile',
])
local('sh helm/sync.sh helm/cortex')
k8s_yaml(helm('./helm/cortex', name='cortex', values=[tilt_values]))
k8s_resource('cortex-syncer', port_forwards=[
    port_forward(8001, 2112),
], links=[
    link('localhost:8001/metrics', '/metrics'),
], labels=['Core-Services'])
k8s_resource('cortex-extractor', port_forwards=[
    port_forward(8002, 2112),
], links=[
    link('localhost:8002/metrics', '/metrics'),
], labels=['Core-Services'])
k8s_resource('cortex-scheduler-nova', port_forwards=[
    port_forward(8003, 8080),
    port_forward(8004, 2112),
], links=[
    link('localhost:8004/metrics', '/metrics'),
], labels=['Core-Services'])
k8s_resource('cortex-scheduler-manila', port_forwards=[
    port_forward(8005, 8080),
    port_forward(8006, 2112),
], links=[
    link('localhost:8006/metrics', '/metrics'),
], labels=['Core-Services'])
k8s_resource('cortex-kpis', port_forwards=[
    port_forward(8007, 2112),
], links=[
    link('localhost:8007/metrics', '/metrics'),
], labels=['Core-Services'])
k8s_resource('cortex-descheduler-nova', port_forwards=[
    port_forward(8008, 2112),
], links=[
    link('localhost:8008/metrics', '/metrics'),
], labels=['Core-Services'])

########### Cortex Commands
k8s_resource('cortex-cli', labels=['Commands'])
local_resource(
    'Run E2E Tests',
    'kubectl exec -it deploy/cortex-cli -- /usr/bin/cortex checks',
    deps=['./internal/checks'],
    labels=['Commands'],
    trigger_mode=TRIGGER_MODE_MANUAL,
    auto_init=False,
)
k8s_resource('cortex-migrations', labels=['Commands'])

########### RabbitMQ MQTT for Cortex Core Service
local('sh helm/sync.sh helm/cortex-mqtt')
k8s_yaml(helm('./helm/cortex-mqtt', name='cortex-mqtt'))
k8s_resource('cortex-mqtt', port_forwards=[
    port_forward(1883, 1883), # Direct TCP connection
    port_forward(9000, 15675), # Websocket connection
], labels=['Core-Services'])

########### Postgres DB for Cortex Core Service
local('sh helm/sync.sh helm/cortex-postgres')
job_flag = 'upgradeJob.enabled=' + str(enable_postgres_upgrade).lower()
k8s_yaml(helm('./helm/cortex-postgres', name='cortex-postgres', set=job_flag))
k8s_resource('cortex-postgresql', port_forwards=[
    port_forward(5432, 5432),
], labels=['Database'])
if enable_postgres_upgrade:
    # Get the version from the chart.
    cmd = "helm show chart ./helm/cortex-postgres | grep -E '^version:' | awk '{print $2}'"
    chart_version = str(local(cmd)).strip()
    # Use the chart version to name the pre-upgrade job.
    k8s_resource('cortex-postgresql-pre-upgrade-'+chart_version, labels=['Database'])
    k8s_resource('cortex-postgresql-post-upgrade-'+chart_version, labels=['Database'])

########### Monitoring
local('sh helm/sync.sh helm/cortex-prometheus-operator')
k8s_yaml(helm('./helm/cortex-prometheus-operator', name='cortex-prometheus-operator')) # Operator
local('sh helm/sync.sh helm/cortex-prometheus')
k8s_yaml(helm('./helm/cortex-prometheus', name='cortex-prometheus')) # Alerts + ServiceMonitor
k8s_resource('cortex-prometheus-operator', labels=['Monitoring'])
k8s_resource(
    new_name='cortex-prometheus',
    port_forwards=[port_forward(9090, 9090)],
    links=[
        link('http://localhost:9090', 'metrics'),
        link('http://localhost:9090/alerts', 'alerts'),
    ],
    objects=['cortex-prometheus:Prometheus:default'],
    labels=['Monitoring'],
)
k8s_resource(
    new_name='cortex-alertmanager',
    objects=['cortex-alertmanager:Alertmanager:default'],
    labels=['Monitoring'],
)
docker_build('cortex-visualizer', 'visualizer')
k8s_yaml('./visualizer/app.yaml')
k8s_resource('cortex-visualizer', port_forwards=[
    port_forward(8009, 80),
], links=[
    link('localhost:8009/nova.html', 'nova visualizer'),
    link('localhost:8009/manila.html', 'manila visualizer'),
], labels=['Monitoring'])

########### Plutono (Grafana Fork)
docker_build('cortex-plutono', 'plutono')
k8s_yaml('./plutono/app.yaml')
k8s_resource('cortex-plutono', port_forwards=[
    port_forward(3000, 3000, name='plutono'),
], links=[
    link('http://localhost:3000/d/cortex/cortex?orgId=1', 'cortex dashboard'),
], labels=['Monitoring'])
