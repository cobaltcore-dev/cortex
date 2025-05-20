# Copyright 2025 SAP SE
# SPDX-License-Identifier: Apache-2.0

# For Pylance to not complain around:
# type: ignore

# Don't track us.
analytics_settings(False)

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

def synced_helm(path_to_chart, name, values=[]):
    """
    Tilt provides a helm() function that renders a chart and watches it.
    However, it also declares file watchers for all files in the chart.
    This means that if we want to run helm dep build to sync the chart/
    dependencies before helm(), this will end in an infinite deployment loop.
    Therefore, we need to selectively watch the files we care about.
    """

    # Build the chart dependencies from the Chart.lock.
    local('helm dep build ' + path_to_chart)

    # Build the command to get the rendered kubernetes yaml.
    cmd = 'helm template'
    for value_file in values:
        cmd += ' -f ' + value_file
    cmd += ' --name-template ' + name
    cmd += ' ' + path_to_chart

    watch_file(path_to_chart + '/Chart.yaml')
    watch_file(path_to_chart + '/Chart.lock')
    for value_file in values:
        watch_file(value_file)
    watch_file(path_to_chart + '/templates')

    return local(cmd)

########### Cortex Core Services
tilt_values = os.getenv('TILT_VALUES_PATH')
docker_build('ghcr.io/cobaltcore-dev/cortex', '.', only=[
    'internal/', 'commands/', 'main.go', 'go.mod', 'go.sum', 'Makefile', tilt_values,
])
k8s_yaml(synced_helm('./helm/cortex', name='cortex', values=[tilt_values]))
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
k8s_resource('cortex-scheduler', port_forwards=[
    port_forward(8080, 8080),
    port_forward(8003, 2112),
], links=[
    link('localhost:8003/metrics', '/metrics'),
], labels=['Core-Services'])
k8s_resource('cortex-kpis', port_forwards=[
    port_forward(8004, 2112),
], links=[
    link('localhost:8004/metrics', '/metrics'),
], labels=['Core-Services'])

########### Cortex Commands
k8s_resource('cortex-cli', labels=['Commands'])
local_resource(
    'Run E2E Tests',
    'kubectl exec -it cortex-cli -- /usr/bin/cortex checks',
    deps=['./internal/checks'],
    labels=['Commands'],
    trigger_mode=TRIGGER_MODE_MANUAL,
    auto_init=False,
)
k8s_resource('cortex-migrations', labels=['Commands'])

########### RabbitMQ MQTT for Cortex Core Service
k8s_yaml(synced_helm('./helm/mqtt', name='cortex-mqtt'))
k8s_resource('cortex-mqtt', port_forwards=[
    port_forward(1883, 1883), # Direct TCP connection
    port_forward(8005, 15675), # Websocket connection
], labels=['Core-Services'])

########### Postgres DB for Cortex Core Service
k8s_yaml(synced_helm('./helm/postgres', name='cortex-postgres'))
k8s_resource('cortex-postgresql', port_forwards=[
    port_forward(5432, 5432),
], labels=['Core-Services'])

########### Monitoring
# TODO: Make the operator work together with synced_helm
k8s_yaml(helm('./helm/prometheus-operator', name='cortex-prometheus-operator')) # Operator
k8s_yaml(synced_helm('./helm/prometheus', name='cortex-prometheus')) # Alerts + ServiceMonitor
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
    port_forward(8006, 80),
], links=[
    link('localhost:8006', 'visualizer'),
], labels=['Monitoring'])

########### Plutono (Grafana Fork)
docker_build('cortex-plutono', 'plutono')
k8s_yaml('./plutono/app.yaml')
k8s_resource('cortex-plutono', port_forwards=[
    port_forward(3000, 3000, name='plutono'),
], links=[
    link('http://localhost:3000/d/cortex/cortex?orgId=1', 'cortex dashboard'),
], labels=['Monitoring'])
