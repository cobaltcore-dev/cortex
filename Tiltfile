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

docker_build('cortex', '.', only=['internal/', 'main.go', 'go.mod', 'go.sum', 'Makefile'])
k8s_yaml(helm('./helm/cortex', name='cortex', set=values))
k8s_resource('cortex', port_forwards='8080:8080', labels=['Core-Services']) # api endpoint
k8s_resource('cortex', port_forwards='2112:2112', labels=['Core-Services']) # metrics endpoint

k8s_yaml(helm('./helm/postgres', name='cortex-postgres'))
k8s_resource('cortex-postgresql', port_forwards='5432:5432', labels=['Core-Services'])

docker_build('cortex-prometheus', 'prometheus')
k8s_yaml('./prometheus/app.yaml')
k8s_resource('cortex-prometheus', port_forwards='9090:9090', labels=['Monitoring'])

docker_build('cortex-plutono', 'plutono')
k8s_yaml('./plutono/app.yaml')
k8s_resource('cortex-plutono', port_forwards='3000:3000', labels=['Monitoring'])

docker_build('cortex-alertmanager', 'alertmanager')
k8s_yaml('./alertmanager/app.yaml')
k8s_resource('cortex-alertmanager', port_forwards='9093:9093', labels=['Monitoring'])

docker_build('cortex-alertmanager-logger', 'alertmanager/logger')
k8s_yaml('./alertmanager/logger/app.yaml')
k8s_resource('cortex-alertmanager-logger', port_forwards='9094:9094', labels=['Monitoring'])
