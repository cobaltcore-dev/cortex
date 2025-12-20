# Copyright 2025 SAP SE
# SPDX-License-Identifier: Apache-2.0

# For Pylance to not complain around:
# type: ignore

analytics_settings(False)

# Use the ACTIVE_DEPLOYMENTS env var to select which Cortex bundles to deploy.
ACTIVE_DEPLOYMENTS_ENV = os.getenv('ACTIVE_DEPLOYMENTS', 'nova,manila,cinder,ironcore,pods')
if ACTIVE_DEPLOYMENTS_ENV == "":
    ACTIVE_DEPLOYMENTS = [] # Catch "".split(",") = [""]
else:
    ACTIVE_DEPLOYMENTS = ACTIVE_DEPLOYMENTS_ENV.split(',')

if not os.getenv('TILT_VALUES_PATH'):
    fail("TILT_VALUES_PATH is not set.")
if not os.path.exists(os.getenv('TILT_VALUES_PATH')):
    fail("TILT_VALUES_PATH "+ os.getenv('TILT_VALUES_PATH') + " does not exist.")
tilt_values = [os.getenv('TILT_VALUES_PATH')]

tilt_overrides = os.getenv('TILT_OVERRIDES_PATH')
if tilt_overrides and os.path.exists(tilt_overrides):
    tilt_values.append(tilt_overrides)

load('ext://helm_resource', 'helm_resource', 'helm_repo')
helm_repo(
    'Prometheus Community Helm Repo',
    'https://prometheus-community.github.io/helm-charts',
    labels=['Repositories'],
)

########### Cortex Operator & CRDs
docker_build('ghcr.io/cobaltcore-dev/cortex', '.',
    dockerfile='Dockerfile',
    only=['internal/', 'cmd/', 'api/', 'pkg', 'go.mod', 'go.sum', 'Dockerfile'],
)
local('sh helm/sync.sh dist/chart')

########### Cortex Bundles
docker_build('ghcr.io/cobaltcore-dev/cortex-postgres', 'postgres')

# Package the lib charts locally and sync them to the bundle charts. In this way
# we can bump the lib charts locally and test them before pushing them to the OCI registry.

bundle_charts = [
    ('helm/bundles/cortex-crds', 'cortex-crds'),
    ('helm/bundles/cortex-nova', 'cortex-nova'),
    ('helm/bundles/cortex-manila', 'cortex-manila'),
    ('helm/bundles/cortex-cinder', 'cortex-cinder'),
    ('helm/bundles/cortex-ironcore', 'cortex-ironcore'),
    ('helm/bundles/cortex-pods', 'cortex-pods'),
]
dep_charts = {
    'cortex-crds': [
        ('dist/chart', 'cortex'),
    ],
    'cortex-nova': [
        ('helm/library/cortex-postgres', 'cortex-postgres'),
        ('dist/chart', 'cortex'),
    ],
    'cortex-manila': [
        ('helm/library/cortex-postgres', 'cortex-postgres'),
        ('dist/chart', 'cortex'),
    ],
    'cortex-cinder': [
        ('helm/library/cortex-postgres', 'cortex-postgres'),
        ('dist/chart', 'cortex'),
    ],
    'cortex-ironcore': [
        ('helm/library/cortex-postgres', 'cortex-postgres'),
        ('dist/chart', 'cortex'),
    ],
    'cortex-pods': [
        ('helm/library/cortex-postgres', 'cortex-postgres'),
        ('dist/chart', 'cortex'),
    ],
}

for (bundle_chart_path, bundle_chart_name) in bundle_charts:
    for (dep_chart_path, dep_chart_name) in dep_charts[bundle_chart_name]:
        print('--- Syncing dependency ' + dep_chart_name + ' into bundle ' + bundle_chart_name)
        watch_file(dep_chart_path)
        local('sh helm/sync.sh ' + dep_chart_path)
        local('helm package ' + dep_chart_path)
        gen_tgz = str(local('ls ' + dep_chart_name + '-*.tgz')).strip()
        # If the file isn't there yet, copy it over.
        if not os.path.exists(bundle_chart_path + '/charts/' + gen_tgz):
            print('Adding ' + dep_chart_name + ' to ' + bundle_chart_name)
            local('mkdir -p helm/bundles/' + bundle_chart_name + '/charts/')
            local('mv -f ' + gen_tgz + ' ' + bundle_chart_path + '/charts/')
            continue
        # If it is there, compare the files and only copy if they differ.
        cmp = 'sh helm/cmp.sh ' + gen_tgz + ' ' + bundle_chart_path + '/charts/' + gen_tgz
        cmp_result = str(local(cmp)).strip()
        if cmp_result == 'true':
            print('Skipping ' + dep_chart_name + ' as it is already up to date in ' + bundle_chart_name)
            local('rm -f ' + gen_tgz)
        else:
            print('Updating ' + dep_chart_name + ' in ' + bundle_chart_name)
            local('mkdir -p helm/bundles/' + bundle_chart_name + '/charts/')
            local('mv -f ' + gen_tgz + ' ' + bundle_chart_path + '/charts/')
for (bundle_chart_path, _) in bundle_charts:
    print('--- Final sync of bundle chart: ' + bundle_chart_path)
    local('sh helm/sync.sh ' + bundle_chart_path)


k8s_yaml(helm('./helm/bundles/cortex-crds', name='cortex-crds', set=[
    # Locally enable IronCore CRDs and rolebindings (these are not deployed by default).
    'cortex.crd.ironcore.enable=true',
    'cortex.rbac.ironcore.enable=true',
    # Tilt is weird and thus we need to set this here even when its provided in the values.
    'cortex.namePrefix=cortex-ironcore',

    'cortex.crd.pods.enable=true',
    'cortex.rbac.pods.enable=true',
    'cortex.namePrefix=cortex-pods',
]))

if 'nova' in ACTIVE_DEPLOYMENTS:
    print("Activating Cortex Nova bundle")
    k8s_yaml(helm('./helm/bundles/cortex-nova', name='cortex-nova', values=tilt_values))
    k8s_resource('cortex-nova-postgresql', labels=['Cortex-Nova'], port_forwards=[
        port_forward(8000, 5432),
    ])
    k8s_resource('cortex-nova-scheduling-controller-manager', labels=['Cortex-Nova'], port_forwards=[
        port_forward(8001, 8080),
    ])
    k8s_resource('cortex-nova-knowledge-controller-manager', labels=['Cortex-Nova'])
    local_resource(
        'Scheduler E2E Tests (Nova)',
        '/bin/sh -c "kubectl exec deploy/cortex-nova-scheduling-controller-manager -- /manager e2e-nova"',
        labels=['Cortex-Nova'],
        trigger_mode=TRIGGER_MODE_MANUAL,
        auto_init=False,
    )

if 'manila' in ACTIVE_DEPLOYMENTS:
    print("Activating Cortex Manila bundle")
    k8s_yaml(helm('./helm/bundles/cortex-manila', name='cortex-manila', values=tilt_values))
    k8s_resource('cortex-manila-postgresql', labels=['Cortex-Manila'], port_forwards=[
        port_forward(8002, 5432),
    ])
    k8s_resource('cortex-manila-scheduling-controller-manager', labels=['Cortex-Manila'], port_forwards=[
            port_forward(8003, 8080),
    ])
    k8s_resource('cortex-manila-knowledge-controller-manager', labels=['Cortex-Manila'])
    local_resource(
        'Scheduler E2E Tests (Manila)',
        '/bin/sh -c "kubectl exec deploy/cortex-manila-scheduling-controller-manager -- /manager e2e-manila"',
        labels=['Cortex-Manila'],
        trigger_mode=TRIGGER_MODE_MANUAL,
        auto_init=False,
    )

if 'cinder' in ACTIVE_DEPLOYMENTS:
    k8s_yaml(helm('./helm/bundles/cortex-cinder', name='cortex-cinder', values=tilt_values))
    k8s_resource('cortex-cinder-postgresql', labels=['Cortex-Cinder'], port_forwards=[
        port_forward(8004, 5432),
    ])
    k8s_resource('cortex-cinder-scheduling-controller-manager', labels=['Cortex-Cinder'], port_forwards=[
        port_forward(8005, 8080),
    ])
    k8s_resource('cortex-cinder-knowledge-controller-manager', labels=['Cortex-Cinder'])
    local_resource(
        'Scheduler E2E Tests (Cinder)',
        '/bin/sh -c "kubectl exec deploy/cortex-cinder-scheduling-controller-manager -- /manager e2e-cinder"',
        labels=['Cortex-Cinder'],
        trigger_mode=TRIGGER_MODE_MANUAL,
        auto_init=False,
    )

if 'ironcore' in ACTIVE_DEPLOYMENTS:
    print("Activating Cortex IronCore bundle")
    k8s_yaml(helm('./helm/bundles/cortex-ironcore', name='cortex-ironcore', values=tilt_values))
    k8s_resource('cortex-ironcore-controller-manager', labels=['Cortex-IronCore'])
    # Deploy resources in machines/samples
    k8s_yaml('samples/ironcore/machinepool.yaml')
    k8s_yaml('samples/ironcore/machineclass.yaml')
    k8s_yaml('samples/ironcore/machine.yaml')

if 'pods' in ACTIVE_DEPLOYMENTS:
    print("Activating Cortex Pods bundle")
    k8s_yaml(helm('./helm/bundles/cortex-pods', name='cortex-pods', values=tilt_values),)
    k8s_resource('cortex-pods-controller-manager', labels=['Cortex-Pods'])
    # Deploy example resources
    k8s_yaml('samples/pods/node.yaml')
    k8s_yaml('samples/pods/pod.yaml')
    k8s_yaml('samples/pods/gang-scheduling.yaml')
    k8s_resource('gang-pod', labels=['Cortex-Pods'])
    k8s_resource('test-pod', labels=['Cortex-Pods'])

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

k8s_yaml('./tools/visualizer/role.yaml')
docker_build('cortex-visualizer', './tools/visualizer')
k8s_yaml('./tools/visualizer/app.yaml')
k8s_resource('cortex-visualizer', port_forwards=[
    port_forward(4000, 80),
], links=[
    link('localhost:4000', 'nova visualizer'),
], labels=['Monitoring'])
docker_build('cortex-plutono', './tools/plutono')
k8s_yaml('./tools/plutono/app.yaml')
k8s_resource('cortex-plutono', port_forwards=[
    port_forward(5000, 3000, name='plutono'),
], links=[
    link('http://localhost:5000/d/cortex/cortex?orgId=1', 'cortex dashboard'),
], labels=['Monitoring'])