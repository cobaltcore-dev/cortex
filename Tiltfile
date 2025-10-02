# Copyright 2025 SAP SE
# SPDX-License-Identifier: Apache-2.0

# For Pylance to not complain around:
# type: ignore

analytics_settings(False)

# Use the ACTIVE_DEPLOYMENTS env var to select which Cortex bundles to deploy.
ACTIVE_DEPLOYMENTS_ENV = os.getenv('ACTIVE_DEPLOYMENTS', 'nova,manila,cinder')
if ACTIVE_DEPLOYMENTS_ENV == "":
    ACTIVE_DEPLOYMENTS = [] # Catch "".split(",") = [""]
else:
    ACTIVE_DEPLOYMENTS = ACTIVE_DEPLOYMENTS_ENV.split(',')

if not os.getenv('TILT_VALUES_PATH'):
    fail("TILT_VALUES_PATH is not set.")
if not os.path.exists(os.getenv('TILT_VALUES_PATH')):
    fail("TILT_VALUES_PATH "+ os.getenv('TILT_VALUES_PATH') + " does not exist.")
tilt_values = os.getenv('TILT_VALUES_PATH')

load('ext://helm_resource', 'helm_resource', 'helm_repo')
helm_repo(
    'Prometheus Community Helm Repo',
    'https://prometheus-community.github.io/helm-charts',
    labels=['Repositories'],
)

def kubebuilder_binary_files(path):
    """
    Return all usual binary files in a kubebuilder operator path.
    Can be used to perform selective watching on code paths for docker builds.
    """
    return [path + '/cmd', path + '/api', path + '/internal', path + '/go.mod', path + '/go.sum']

########### Reservations Operator & CRDs
docker_build('ghcr.io/cobaltcore-dev/cortex-reservations-operator', '.',
    dockerfile='Dockerfile.kubebuilder',
    build_args={'GO_MOD_PATH': 'reservations'},
    only=kubebuilder_binary_files('reservations') + ['internal/', 'decisions/', 'go.mod', 'go.sum'],
)
local('sh helm/sync.sh reservations/dist/chart')
k8s_yaml(helm('reservations/dist/chart', name='cortex-reservations', values=[tilt_values]))
k8s_resource('reservations-controller-manager', labels=['Reservations'])

########### Decisions Operator & CRDs
docker_build('ghcr.io/cobaltcore-dev/cortex-decisions-operator', '.',
    dockerfile='Dockerfile.kubebuilder',
    build_args={'GO_MOD_PATH': 'decisions'},
    only=kubebuilder_binary_files('decisions') + ['internal/', 'go.mod', 'go.sum'],
)
local('sh helm/sync.sh decisions/dist/chart')
k8s_yaml(helm('decisions/dist/chart', name='cortex-decisions', values=[tilt_values]))
k8s_resource('decisions-controller-manager', labels=['Decisions'])

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
docker_build('ghcr.io/cobaltcore-dev/cortex', '.', only=[
    'internal/', 'commands/', 'main.go', 'go.mod', 'go.sum', 'Makefile',
    'reservations/api/', # API module of the reservations operator needed for the scheduler.
    'decisions/api/', # API module of the decisions operator needed for the scheduler.
])
docker_build('ghcr.io/cobaltcore-dev/cortex-postgres', 'postgres')

# Package the lib charts locally and sync them to the bundle charts. In this way
# we can bump the lib charts locally and test them before pushing them to the OCI registry.


# --- Chart lists based on ACTIVE_DEPLOYMENTS ---
lib_charts = ['cortex-core', 'cortex-postgres', 'cortex-mqtt']
bundle_charts = ['cortex-' + name for name in ACTIVE_DEPLOYMENTS]

for lib_chart in lib_charts:
    watch_file('helm/library/' + lib_chart)
    local('sh helm/sync.sh helm/library/' + lib_chart)
    for bundle_chart in bundle_charts:
        local('helm package helm/library/' + lib_chart)
        gen_tgz = str(local('ls ' + lib_chart + '-*.tgz')).strip()
        cmp = 'sh helm/cmp.sh ' + gen_tgz + ' helm/bundles/' + bundle_chart + '/charts/' + gen_tgz
        cmp_result = str(local(cmp)).strip()
        if cmp_result == 'true':
            print('Skipping ' + lib_chart + ' as it is already up to date in ' + bundle_chart)
            local('rm -f ' + gen_tgz)
        else:
            local('mkdir -p helm/bundles/' + bundle_chart + '/charts/')
            local('mv -f ' + gen_tgz + ' helm/bundles/' + bundle_chart + '/charts/')
for bundle_chart in bundle_charts:
    local('sh helm/sync.sh helm/bundles/' + bundle_chart)

# Deploy the selected Cortex bundles
for name in ACTIVE_DEPLOYMENTS:
    k8s_yaml(helm('./helm/bundles/cortex-' + name, name='cortex-' + name, values=[tilt_values]))

# Note: place resources higher in this list to ensure their local port stays the same.
# Elements placed lower in the list will have their local port shifted by elements inserted above.

# --- Resource definitions based on ACTIVE_DEPLOYMENTS ---
resources_def = {
    'MQTT': {
        'suffix': 'mqtt',
        'components': lambda name: ['cortex-' + name + '-mqtt'],
        'ports': [(1883, 'tcp'), (15675, 'ws')],
    },
    'Database': {
        'suffix': 'postgresql',
        'components': lambda name: ['cortex-' + name + '-postgresql'],
        'ports': [(5432, 'psql')],
    },
    'Cortex': {
        'suffix': '',
        'components': lambda name: [
            'cortex-' + name + '-migrations',
            'cortex-' + name + '-cli',
            'cortex-' + name + '-syncer',
            'cortex-' + name + '-extractor',
            'cortex-' + name + '-kpis',
            'cortex-' + name + '-scheduler',
        ] + (['cortex-' + name + '-descheduler'] if name == 'nova' else []),
        'ports': [(2112, 'metrics'), (8080, 'api')],
    },
}

local_port = 8000
for name in ACTIVE_DEPLOYMENTS:
    # MQTT
    for component in resources_def['MQTT']['components'](name):
        k8s_resource(
            component,
            port_forwards=[
                port_forward(local_port + i, service_port)
                for i, (service_port, _) in enumerate(resources_def['MQTT']['ports'])
            ],
            links=[
                link('http://localhost:' + str(local_port + i) + '/' + service_port_name, '/' + service_port_name)
                for i, (_, service_port_name) in enumerate(resources_def['MQTT']['ports'])
            ],
            labels=['MQTT'],
        )
        local_port += len(resources_def['MQTT']['ports'])
    # Database
    for component in resources_def['Database']['components'](name):
        k8s_resource(
            component,
            port_forwards=[
                port_forward(local_port + i, service_port)
                for i, (service_port, _) in enumerate(resources_def['Database']['ports'])
            ],
            links=[
                link('http://localhost:' + str(local_port + i) + '/' + service_port_name, '/' + service_port_name)
                for i, (_, service_port_name) in enumerate(resources_def['Database']['ports'])
            ],
            labels=['Database'],
        )
        local_port += len(resources_def['Database']['ports'])
    # Cortex core components
    for component in resources_def['Cortex']['components'](name):
        k8s_resource(
            component,
            port_forwards=[
                port_forward(local_port + i, service_port)
                for i, (service_port, _) in enumerate(resources_def['Cortex']['ports'])
            ],
            links=[
                link('http://localhost:' + str(local_port + i) + '/' + service_port_name, '/' + service_port_name)
                for i, (_, service_port_name) in enumerate(resources_def['Cortex']['ports'])
            ],
            labels=['Cortex-' + name.capitalize()],
        )
        local_port += len(resources_def['Cortex']['ports'])

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
