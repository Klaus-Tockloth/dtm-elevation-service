#!/bin/sh

# ------------------------------------
# Purpose:
# - Build binary for linux-amd64 (from macOS).
#
# Releases:
# - v1.0.0 - 2025-04-17: initial release
#
# Remarks:
# - requires Docker
# ------------------------------------

# set -o xtrace
set -o verbose

# create directory if necessary
DIR="./build/linux-amd64"
if [ ! -d "$DIR" ]; then
  mkdir "$DIR"
fi

# build binary
docker buildx build --platform linux/amd64 --progress=plain --tag dtm-elevation-service-linux-amd64 --load .

# create temporary container
docker create --name temp_container dtm-elevation-service-linux-amd64

# copy binary to local filesystem
# TODO
# docker cp temp_container:/app/dtm-elevation-service ./dtm-elevation-service
docker cp temp_container:/app/dtm-elevation-service-dev ./dtm-elevation-service-dev

# remove temporary container
docker rm temp_container

# move binary to target directory
# TODO
# mv ./dtm-elevation-service "$DIR/dtm-elevation-service"
mv ./dtm-elevation-service-dev "$DIR/dtm-elevation-service-dev"

