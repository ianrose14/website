#!/bin/bash

set -e

(podman stop webapp) || true
(podman rm webapp) || true

mkdir -p data

# ref 1: https://stackoverflow.com/questions/63790529/authenticate-to-google-container-registry-with-podman
# ref 2: https://github.com/containers/podman/issues/13691#issuecomment-1081913637
gcloud auth --quiet print-access-token | podman login -u oauth2accesstoken --password-stdin us-central1-docker.pkg.dev
podman pull us-central1-docker.pkg.dev/ian-rose/docker-1/webapp:latest
podman run -d -p 8080:80 -p 8443:443 --restart=no \
    -v "$(pwd)/config":/root/config/ -v "$(pwd)/data":/root/data/ --name webapp \
    us-central1-docker.pkg.dev/ian-rose/docker-1/webapp:latest /root/webapp -host ianthomasrose.com
