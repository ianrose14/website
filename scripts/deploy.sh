#!/bin/bash

set -e

# Getting permissions errors on local when trying to push?  Run this:
#   gcloud auth configure-docker us-central1-docker.pkg.dev


# Run on initial instance creation:
# > sudo apt-get update
# > sudo apt-get install podman
# > sudo setcap cap_net_bind_service=+ep /usr/bin/podman
# > sudo setcap cap_net_bind_service=+ep $(which slirp4netns)

# make webapp-linux

#docker buildx build --platform linux/amd64 --build-arg BIN=bin/linux_amd64/webapp -t us-central1-docker.pkg.dev/ian-rose/docker-1/webapp:latest .
#docker build -t us-central1-docker.pkg.dev/ian-rose/docker-1/webapp:latest .
#docker push us-central1-docker.pkg.dev/ian-rose/docker-1/webapp:latest

gcloud compute --project ian-rose ssh ianrose14@instance-1 --zone us-central1-a -- mkdir -p config/
gcloud compute --project ian-rose scp --zone us-central1-a config/* ianrose14@instance-1:config/
gcloud compute --project ian-rose scp --zone us-central1-a scripts/startup.sh ianrose14@instance-1:
gcloud compute --project ian-rose ssh ianrose14@instance-1 --zone us-central1-a -- bash ./startup.sh
