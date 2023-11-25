#!/bin/bash

set -e

gcloud auth login ianrose14@gmail.com
gcloud config set project ian-rose
gcloud config list
