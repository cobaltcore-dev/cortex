values = [
    'secrets.openstack.authUrl=' + os.getenv('OS_AUTH_URL'),
    'secrets.openstack.username=' + os.getenv('OS_USERNAME'),
    'secrets.openstack.password=' + os.getenv('OS_PASSWORD'),
    'secrets.openstack.projectName=' + os.getenv('OS_PROJECT_NAME'),
    'secrets.openstack.userDomainName=' + os.getenv('OS_USER_DOMAIN_NAME'),
    'secrets.openstack.projectDomainName=' + os.getenv('OS_PROJECT_DOMAIN_NAME'),
    'secrets.prometheus.url=' + os.getenv('PROMETHEUS_URL'),
    'secrets.prometheus.ssoPublicKey=' + os.getenv('PROMETHEUS_SSO_PUBLIC_KEY', ''),
    'secrets.prometheus.ssoPrivateKey=' + os.getenv('PROMETHEUS_SSO_PRIVATE_KEY', ''),
]

load('ext://helm_resource', 'helm_resource', 'helm_repo')
helm_repo('bitnami', 'https://charts.bitnami.com/bitnami', labels=['Repositories'])

docker_build('cortex', '.', only=[
    'internal/', 'main.go', 'go.mod', 'go.sum', 'Makefile'
])
k8s_yaml(helm('./helm/cortex', name='cortex', set=values))
k8s_resource('cortex', port_forwards=[
    port_forward(8080, 8080),
    port_forward(2112, 2112),
], links=[
    link('localhost:8080/up', '/up'),
    link('localhost:2112/metrics', '/metrics'),
], labels=['Core-Services']) # api endpoint

k8s_yaml(helm('./helm/postgres', name='cortex-postgres'))
k8s_resource('cortex-postgresql', port_forwards=[
    port_forward(5432, 5432),
], labels=['Core-Services'])

docker_build('cortex-prometheus', 'prometheus')
k8s_yaml('./prometheus/app.yaml')
k8s_resource('cortex-prometheus', port_forwards=[
    port_forward(9090, 9090, name='prometheus'),
], labels=['Monitoring'])

docker_build('cortex-plutono', 'plutono')
k8s_yaml('./plutono/app.yaml')
k8s_resource('cortex-plutono', port_forwards=[
    port_forward(3000, 3000, name='plutono'),
], links=[
    link('http://localhost:3000/d/7MZnLxDHz/cortex?orgId=1', 'cortex dashboard'),
], labels=['Monitoring'])

docker_build('cortex-alertmanager', 'alertmanager')
k8s_yaml('./alertmanager/app.yaml')
k8s_resource('cortex-alertmanager', port_forwards=[
    port_forward(9093, 9093, name='alertmanager'),
], labels=['Monitoring'])

docker_build('cortex-alertmanager-logger', 'alertmanager/logger')
k8s_yaml('./alertmanager/logger/app.yaml')
k8s_resource('cortex-alertmanager-logger', port_forwards=[
    port_forward(9094, 9094, name='logger'),
], labels=['Monitoring'])
