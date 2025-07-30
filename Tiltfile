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

# The upgrade job may take a long time to run, so it is disabled by default.
enable_postgres_upgrade = False

load('ext://helm_resource', 'helm_resource', 'helm_repo')
helm_repo(
    'Prometheus Community Helm Repo',
    'https://prometheus-community.github.io/helm-charts',
    labels=['Repositories'],
)

########### Dev Dependencies
local('sh helm/sync.sh helm/dev/cortex-prometheus-operator')
k8s_yaml(helm('./helm/dev/cortex-prometheus-operator', name='cortex-prometheus-operator')) # Operator
k8s_resource('cortex-prometheus-operator', labels=['Monitoring'])
k8s_resource(
    new_name='cortex-prometheus',
    port_forwards=[port_forward(3000, 9090)],
    links=[
        link('http://localhost:3000', 'metrics'),
        link('http://localhost:3000/alerts', 'alerts'),
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
    port_forward(4000, 80),
], links=[
    link('localhost:4000/nova.html', 'nova visualizer'),
    link('localhost:4000/manila.html', 'manila visualizer'),
], labels=['Monitoring'])
docker_build('cortex-plutono', 'plutono')
k8s_yaml('./plutono/app.yaml')
k8s_resource('cortex-plutono', port_forwards=[
    port_forward(5000, 3000, name='plutono'),
], links=[
    link('http://localhost:5000/d/cortex/cortex?orgId=1', 'cortex dashboard'),
], labels=['Monitoring'])

########### Cortex Bundles
tilt_values = os.getenv('TILT_VALUES_PATH')
docker_build('ghcr.io/cobaltcore-dev/cortex', '.', only=[
    'internal/', 'commands/', 'main.go', 'go.mod', 'go.sum', 'Makefile',
])

# Package the lib charts locally and sync them to the bundle charts. In this way
# we can bump the lib charts locally and test them before pushing them to the OCI registry.
lib_charts = ['cortex-core', 'cortex-postgres', 'cortex-mqtt']
bundle_charts = ['cortex-nova', 'cortex-manila']
for lib_chart in lib_charts:
    watch_file('helm/library/' + lib_chart) # React to lib chart changes.
    local('sh helm/sync.sh helm/library/' + lib_chart)
    for bundle_chart in bundle_charts:
        local('helm package helm/library/' + lib_chart)
        gen_tgz = str(local('ls ' + lib_chart + '-*.tgz')).strip()
        cmp = 'sh helm/cmp.sh ' + gen_tgz + ' helm/bundles/' + bundle_chart + '/charts/' + gen_tgz
        cmp_result = str(local(cmp)).strip()
        if cmp_result == 'true': # same chart
            print('Skipping ' + lib_chart + ' as it is already up to date in ' + bundle_chart)
            # Make sure the gen_tgz is removed from the local directory.
            local('rm -f ' + gen_tgz)
        else:
            local('mv -f ' + gen_tgz + ' helm/bundles/' + bundle_chart + '/charts/')
# Ensure the bundle charts are up to date.
for bundle_chart in bundle_charts:
    local('sh helm/sync.sh helm/bundles/' + bundle_chart)

# Deploy the Cortex bundles.
k8s_yaml(helm('./helm/bundles/cortex-nova', name='cortex-nova', values=[tilt_values]))
k8s_yaml(helm('./helm/bundles/cortex-manila', name='cortex-manila', values=[tilt_values]))

# Note: place resources higher in this list to ensure their local port stays the same.
# Elements placed lower in the list will have their local port shifted by elements inserted above.
resources = [
    (
        'MQTT',
        [
            'cortex-nova-mqtt',
            'cortex-manila-mqtt',
        ],
        [(1883, 'tcp'), (15675, 'ws')],
    ),
    (
        'Database',
        [
            'cortex-nova-postgresql',
            'cortex-manila-postgresql',
        ],
        [(5432, 'psql')],
    ),
    (
        'Cortex-Nova',
        [
            'cortex-nova-migrations',
            'cortex-nova-cli',
            'cortex-nova-syncer',
            'cortex-nova-extractor',
            'cortex-nova-kpis',
            'cortex-nova-scheduler',
            'cortex-nova-descheduler',
        ],
        [(2112, 'metrics'), (8080, 'api')],
    ),
    (
        'Cortex-Manila',
        [
            'cortex-manila-migrations',
            'cortex-manila-cli',
            'cortex-manila-syncer',
            'cortex-manila-extractor',
            'cortex-manila-kpis',
            'cortex-manila-scheduler',
        ],
        [(2112, 'metrics'), (8080, 'api')],
    ),
]
local_port = 8000
for label, components, service_ports in resources:
    for component in components:
        k8s_resource(
            component,
            port_forwards=[
                port_forward(local_port + i, service_port)
                for i, (service_port, _) in enumerate(service_ports)
            ],
            links=[
                link('http://localhost:' + str(local_port + i) + '/' + service_port_name, '/' + service_port_name)
                for i, (_, service_port_name) in enumerate(service_ports)
            ],
            labels=[label],
        )
        local_port += len(service_ports)

########### E2E Tests
local_resource(
    'Run E2E Tests',
    '/bin/sh -c "kubectl exec deploy/cortex-nova-cli -- /usr/bin/cortex checks" && '+\
    '/bin/sh -c "kubectl exec deploy/cortex-manila-cli -- /usr/bin/cortex checks"',
    deps=['./internal/checks'],
    labels=['Commands'],
    trigger_mode=TRIGGER_MODE_MANUAL,
    auto_init=False,
)
