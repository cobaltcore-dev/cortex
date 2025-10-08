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

########### Cortex Scheduler
docker_build('ghcr.io/cobaltcore-dev/cortex-scheduler', '.',
    dockerfile='Dockerfile.kubebuilder',
    build_args={'GO_MOD_PATH': 'scheduler'},
    only=kubebuilder_binary_files('scheduler') + ['reservations/', 'decisions/', 'internal/', 'go.mod', 'go.sum'],
)
local('sh helm/sync.sh scheduler/dist/chart')
# Deployed as part of bundles below.

########### Reservations Operator & CRDs
docker_build('ghcr.io/cobaltcore-dev/cortex-reservations-operator', '.',
    dockerfile='Dockerfile.kubebuilder',
    build_args={'GO_MOD_PATH': 'reservations'},
    only=kubebuilder_binary_files('reservations') + ['scheduler/', 'decisions/', 'internal/', 'go.mod', 'go.sum'],
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

########### Cortex Bundles
docker_build('ghcr.io/cobaltcore-dev/cortex', '.', only=[
    'internal/', 'commands/', 'main.go', 'go.mod', 'go.sum', 'Makefile',
    'reservations/api/', # API module of the reservations operator needed for the scheduler.
    'decisions/api/', # API module of the decisions operator needed for the scheduler.
])
docker_build('ghcr.io/cobaltcore-dev/cortex-postgres', 'postgres')

# Package the lib charts locally and sync them to the bundle charts. In this way
# we can bump the lib charts locally and test them before pushing them to the OCI registry.

dep_charts = [
    ('helm/library/cortex-core', 'cortex-core'),
    ('helm/library/cortex-postgres', 'cortex-postgres'),
    ('helm/library/cortex-mqtt', 'cortex-mqtt'),
    ('scheduler/dist/chart', 'cortex-scheduler'),
]
bundle_charts = [
    ('helm/bundles/cortex-nova', 'cortex-nova'),
    ('helm/bundles/cortex-manila', 'cortex-manila'),
    ('helm/bundles/cortex-cinder', 'cortex-cinder'),
]

for (dep_chart_path, dep_chart_name) in dep_charts:
    watch_file(dep_chart_path)
    local('sh helm/sync.sh ' + dep_chart_path)
    for (bundle_chart_path, bundle_chart_name) in bundle_charts:
        local('helm package ' + dep_chart_path)
        gen_tgz = str(local('ls ' + dep_chart_name + '-*.tgz')).strip()
        cmp = 'sh helm/cmp.sh ' + gen_tgz + ' ' + bundle_chart_path + '/charts/' + gen_tgz
        cmp_result = str(local(cmp)).strip()
        if cmp_result == 'true':
            print('Skipping ' + dep_chart_name + ' as it is already up to date in ' + bundle_chart_name)
            local('rm -f ' + gen_tgz)
        else:
            local('mkdir -p helm/bundles/' + bundle_chart_name + '/charts/')
            local('mv -f ' + gen_tgz + ' ' + bundle_chart_path + '/charts/')
for (bundle_chart_path, _) in bundle_charts:
    local('sh helm/sync.sh ' + bundle_chart_path)

port_mappings = {}
def new_port_mapping(component, local_port, remote_port):
    port_mappings[component] = {'local': local_port, 'remote': remote_port}
    return port_forward(local_port, remote_port, name=component)

if 'nova' in ACTIVE_DEPLOYMENTS:
    print("Activating Cortex Nova bundle")
    k8s_yaml(helm('./helm/bundles/cortex-nova', name='cortex-nova', values=[tilt_values]))
    k8s_resource('cortex-nova-postgresql', labels=['Cortex-Nova'])
    k8s_resource('cortex-nova-mqtt', labels=['Cortex-Nova'])
    k8s_resource('cortex-nova-migrations', labels=['Cortex-Nova'])
    k8s_resource('cortex-nova-syncer', labels=['Cortex-Nova'])
    k8s_resource('cortex-nova-extractor', labels=['Cortex-Nova'])
    k8s_resource('cortex-nova-kpis', labels=['Cortex-Nova'])
    k8s_resource('cortex-nova-descheduler', labels=['Cortex-Nova'])
    k8s_resource('cortex-nova-scheduler', labels=['Cortex-Nova'], port_forwards=[
        new_port_mapping('cortex-nova-scheduler-api', 8000, 8080),
    ])

if 'manila' in ACTIVE_DEPLOYMENTS:
    print("Activating Cortex Manila bundle")
    k8s_yaml(helm('./helm/bundles/cortex-manila', name='cortex-manila', values=[tilt_values]))
    k8s_resource('cortex-manila-postgresql', labels=['Cortex-Manila'])
    k8s_resource('cortex-manila-mqtt', labels=['Cortex-Manila'])
    k8s_resource('cortex-manila-migrations', labels=['Cortex-Manila'])
    k8s_resource('cortex-manila-syncer', labels=['Cortex-Manila'])
    k8s_resource('cortex-manila-extractor', labels=['Cortex-Manila'])
    k8s_resource('cortex-manila-kpis', labels=['Cortex-Manila'])
    k8s_resource('cortex-manila-scheduler', labels=['Cortex-Manila'], port_forwards=[
        new_port_mapping('cortex-manila-scheduler-api', 8001, 8080),
    ])

if 'cinder' in ACTIVE_DEPLOYMENTS:
    k8s_yaml(helm('./helm/bundles/cortex-cinder', name='cortex-cinder', values=[tilt_values]))
    k8s_resource('cortex-cinder-postgresql', labels=['Cortex-Cinder'])
    k8s_resource('cortex-cinder-mqtt', labels=['Cortex-Cinder'])
    k8s_resource('cortex-cinder-migrations', labels=['Cortex-Cinder'])
    k8s_resource('cortex-cinder-syncer', labels=['Cortex-Cinder'])
    k8s_resource('cortex-cinder-extractor', labels=['Cortex-Cinder'])
    k8s_resource('cortex-cinder-kpis', labels=['Cortex-Cinder'])
    k8s_resource('cortex-cinder-scheduler', labels=['Cortex-Cinder'], port_forwards=[
        new_port_mapping('cortex-cinder-scheduler-api', 8002, 8080),
    ])

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
docker_build('cortex-visualizer', 'visualizer', build_args={'PORTMAPPINGSOBJ': encode_json(port_mappings)})
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

########### E2E Tests
local_resource(
    'Scheduler E2E Tests (Nova)',
    '/bin/sh -c "kubectl exec deploy/cortex-nova-scheduler -- /manager e2e-nova"',
    deps=['./scheduler/internal/e2e'],
    labels=['Cortex-Nova'],
    trigger_mode=TRIGGER_MODE_MANUAL,
    auto_init=False,
)
local_resource(
    'Scheduler E2E Tests (Manila)',
    '/bin/sh -c "kubectl exec deploy/cortex-manila-scheduler -- /manager e2e-manila"',
    deps=['./scheduler/internal/e2e'],
    labels=['Cortex-Manila'],
    trigger_mode=TRIGGER_MODE_MANUAL,
    auto_init=False,
)
local_resource(
    'Scheduler E2E Tests (Cinder)',
    '/bin/sh -c "kubectl exec deploy/cortex-cinder-scheduler -- /manager e2e-cinder"',
    deps=['./scheduler/internal/e2e'],
    labels=['Cortex-Cinder'],
    trigger_mode=TRIGGER_MODE_MANUAL,
    auto_init=False,
)
