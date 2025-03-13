# Copyright 2025 SAP SE
# SPDX-License-Identifier: Apache-2.0

FROM golang:1.24.0-alpine3.21
WORKDIR /app
COPY go.mod go.sum ./
COPY *.go ./
RUN CGO_ENABLED=0 GOOS=linux go build -a -installsuffix cgo -o app .
EXPOSE 8080
CMD ["./app"]