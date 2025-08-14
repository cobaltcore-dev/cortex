#!/bin/bash
apt update && apt install -y stress-ng
stress-ng \
    --cpu {{.VCPUs}} \
    --cpu-method zeta \
    --cpu-load 100 \
    --vm-bytes {{.RAM}}M \
    --vm-keep \
    -m 1