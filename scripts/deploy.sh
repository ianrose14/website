#!/bin/bash

set -e

# Getting permissions errors on local when trying to push?  Run this:
#   gcloud auth configure-docker us-central1-docker.pkg.dev

make webapp-linux

docker buildx build --platform linux/amd64 -t us-central1-docker.pkg.dev/ian-rose/docker-1/webapp:latest .
#docker build -t us-central1-docker.pkg.dev/ian-rose/docker-1/webapp:latest .
docker push us-central1-docker.pkg.dev/ian-rose/docker-1/webapp:latest

ssh ianrose14@34.66.56.67 mkdir -p config/
scp config/*  ianrose14@34.66.56.67:config/
scp scripts/startup.sh ianrose14@34.66.56.67:

#scp bin/linux_amd64/webapp ianrose14@34.66.56.67:
ssh ianrose14@34.66.56.67 bash ./startup.sh
