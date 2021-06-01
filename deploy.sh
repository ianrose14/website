#!/bin/bash

set -e

gcloud auth login ianrose14@gmail.com
gcloud --project ian-rose app deploy
