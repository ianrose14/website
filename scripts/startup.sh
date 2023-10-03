#!/bin/bash

set -e

(podman stop webapp) || true
(podman rm webapp) || true

gcloud auth --quiet print-access-token | podman login -u oauth2accesstoken --password-stdin https://us-central1-docker.pkg.dev
podman pull us-central1-docker.pkg.dev/ian-rose/docker-1/webapp:latest
podman run -d -p 8080:80 -p 8443:443 --restart=no \
    -v "$(pwd)/config":/root/config/ -v "$(pwd)/data":/root/data/ --name webapp \
    us-central1-docker.pkg.dev/ian-rose/docker-1/webapp:latest /root/webapp -host ianthomasrose.com
