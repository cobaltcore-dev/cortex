#!/bin/bash

set -e

echo "Deleting home cluster"
kind delete cluster --name cortex-home

echo "Deleting az-a and az-b clusters"
kind delete cluster --name cortex-remote-az-a
kind delete cluster --name cortex-remote-az-b

echo "Cleaning up temporary files"
rm -f /tmp/root-ca-home.pem \
    /tmp/root-ca-remote-az-a.pem \
    /tmp/root-ca-remote-az-b.pem \
    /tmp/cortex-values.yaml \
    /tmp/hypervisor-crd.yaml