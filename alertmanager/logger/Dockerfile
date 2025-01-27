FROM golang:1.23.4-alpine3.21 AS builder
WORKDIR /app
COPY logger.go ./
RUN go build -o alert_logger logger.go
FROM alpine:3.21
WORKDIR /root/
COPY --from=builder /app/alert_logger .
EXPOSE 9094
CMD ["./alert_logger"]