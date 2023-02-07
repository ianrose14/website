#!/bin/bash

set -e

make webapp-linux
ssh ianrose14@34.66.56.67 mkdir -p config/
scp config/*  ianrose14@34.66.56.67:config/
scp scripts/startup.sh ianrose14@34.66.56.67:
ssh ianrose14@34.66.56.67 bash ./startup.sh
