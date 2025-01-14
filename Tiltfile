values = [
    'datasources.openstack.authUrl=' + os.getenv('OS_AUTH_URL'),
    'datasources.openstack.username=' + os.getenv('OS_USERNAME'),
    'datasources.openstack.password=' + os.getenv('OS_PASSWORD'),
    'datasources.openstack.projectName=' + os.getenv('OS_PROJECT_NAME'),
    'datasources.openstack.userDomainName=' + os.getenv('OS_USER_DOMAIN_NAME'),
    'datasources.openstack.projectDomainName=' + os.getenv('OS_PROJECT_DOMAIN_NAME'),
    'datasources.prometheus.url=' + os.getenv('PROMETHEUS_URL'),
]

docker_build('cortex', '.')
load('ext://helm_resource', 'helm_resource', 'helm_repo')
helm_repo('bitnami', 'https://charts.bitnami.com/bitnami')
k8s_yaml(helm('./helm', name='cortex', set=values))
k8s_resource('cortex', port_forwards=8080)

docker_build('plutono', 'plutono')
k8s_yaml('./plutono/app.yaml')
k8s_resource('plutono', port_forwards=3000)