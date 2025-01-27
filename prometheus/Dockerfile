FROM prom/prometheus:v3.1.0
COPY prometheus.yaml /etc/prometheus/prometheus.yaml
COPY alerts.yaml /etc/prometheus/alerts.yaml
CMD [ "--config.file=/etc/prometheus/prometheus.yaml" ]