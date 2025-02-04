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

########### Cortex Core Services
docker_build('cortex', '.', only=[
    'internal/', 'main.go', 'go.mod', 'go.sum', 'Makefile',
])
k8s_yaml(helm('./helm/cortex', name='cortex', set=[
    'secrets.openstack.authUrl=' + os.getenv('OS_AUTH_URL'),
    'secrets.openstack.username=' + os.getenv('OS_USERNAME'),
    'secrets.openstack.password=' + os.getenv('OS_PASSWORD'),
    'secrets.openstack.projectName=' + os.getenv('OS_PROJECT_NAME'),
    'secrets.openstack.userDomainName=' + os.getenv('OS_USER_DOMAIN_NAME'),
    'secrets.openstack.projectDomainName=' + os.getenv('OS_PROJECT_DOMAIN_NAME'),
    'secrets.prometheus.url=' + os.getenv('PROMETHEUS_URL'),
    'secrets.prometheus.ssoPublicKey=' + os.getenv('PROMETHEUS_SSO_PUBLIC_KEY', ''),
    'secrets.prometheus.ssoPrivateKey=' + os.getenv('PROMETHEUS_SSO_PRIVATE_KEY', ''),
]))
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

########### Postgres DB for Cortex Core Service
k8s_yaml(helm('./helm/postgres', name='cortex-postgres'))
k8s_resource('cortex-postgresql', port_forwards=[
    port_forward(5432, 5432),
], labels=['Core-Services'])

########### Prometheus and Alertmanager
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

########### Plutono (Grafana Fork)
docker_build('cortex-plutono', 'plutono')
k8s_yaml('./plutono/app.yaml')
k8s_resource('cortex-plutono', port_forwards=[
    port_forward(3000, 3000, name='plutono'),
], links=[
    link('http://localhost:3000/d/cortex/cortex?orgId=1', 'cortex dashboard'),
], labels=['Monitoring'])
