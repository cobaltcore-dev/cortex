# Copyright 2025 SAP SE
# SPDX-License-Identifier: Apache-2.0

# For Pylance to not complain around:
# type: ignore

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

# Build the helm charts
local('test -f ./helm/cortex/Chart.lock || helm dep up ./helm/cortex')
local('test -f ./helm/prometheus/Chart.lock || helm dep up ./helm/prometheus')
local('test -f ./helm/postgres/Chart.lock || helm dep up ./helm/postgres')

########### Cortex Core Services
tilt_values = os.getenv('TILT_VALUES_PATH')
docker_build('ghcr.io/cobaltcore-dev/cortex', '.', only=[
    'internal/', 'main.go', 'go.mod', 'go.sum', 'Makefile', tilt_values,
])
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
k8s_resource('cortex-scheduler', port_forwards=[
    port_forward(8080, 8080),
    port_forward(8003, 2112),
], links=[
    link('localhost:8003/metrics', '/metrics'),
], labels=['Core-Services'])
k8s_resource('cortex-mqtt', port_forwards=[
    port_forward(1883, 1883), # Direct TCP connection
    port_forward(8004, 8080), # Websocket connection
], labels=['Core-Services'])

########### Postgres DB for Cortex Core Service
k8s_yaml(helm('./helm/postgres', name='cortex-postgres'))
k8s_resource('cortex-postgresql', port_forwards=[
    port_forward(5432, 5432),
], labels=['Core-Services'])

########### Monitoring
k8s_yaml(helm('./helm/prometheus', name='cortex-prometheus', set=[
    # Deploy prometheus operator CRDs, Prometheus, and Alertmanager
    'kube-prometheus-stack.enabled=true',
]))
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
    port_forward(8005, 80),
], links=[
    link('localhost:8005', 'visualizer'),
], labels=['Monitoring'])

########### Plutono (Grafana Fork)
docker_build('cortex-plutono', 'plutono')
k8s_yaml('./plutono/app.yaml')
k8s_resource('cortex-plutono', port_forwards=[
    port_forward(3000, 3000, name='plutono'),
], links=[
    link('http://localhost:3000/d/cortex/cortex?orgId=1', 'cortex dashboard'),
], labels=['Monitoring'])
