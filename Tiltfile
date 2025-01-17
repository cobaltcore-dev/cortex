values = [
    'secrets.openstack.authUrl=' + os.getenv('OS_AUTH_URL'),
    'secrets.openstack.username=' + os.getenv('OS_USERNAME'),
    'secrets.openstack.password=' + os.getenv('OS_PASSWORD'),
    'secrets.openstack.projectName=' + os.getenv('OS_PROJECT_NAME'),
    'secrets.openstack.userDomainName=' + os.getenv('OS_USER_DOMAIN_NAME'),
    'secrets.openstack.projectDomainName=' + os.getenv('OS_PROJECT_DOMAIN_NAME'),
    'secrets.prometheus.url=' + os.getenv('PROMETHEUS_URL'),
]

docker_build('cortex', '.')
load('ext://helm_resource', 'helm_resource', 'helm_repo')
helm_repo('bitnami', 'https://charts.bitnami.com/bitnami')
k8s_yaml(helm('./helm/cortex', name='cortex', set=values))
k8s_resource('cortex', port_forwards=8080)
k8s_resource('cortex-postgresql', port_forwards=5432)

docker_build('plutono', 'plutono')
k8s_yaml('./plutono/app.yaml')
k8s_resource('plutono', port_forwards=3000)