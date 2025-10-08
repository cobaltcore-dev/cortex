# Copyright 2025 SAP SE
# SPDX-License-Identifier: Apache-2.0

FROM nginx

ARG PORTMAPPINGSOBJ={}
ENV PORTMAPPINGSOBJ=$PORTMAPPINGSOBJ
RUN echo $PORTMAPPINGSOBJ > /usr/share/nginx/html/portmappings.json

COPY vendor/mqtt.min.js /usr/share/nginx/html/mqtt.min.js
COPY nova.html /usr/share/nginx/html/nova.html
COPY shared.css /usr/share/nginx/html/shared.css
COPY favicon.ico /usr/share/nginx/html/favicon.ico
