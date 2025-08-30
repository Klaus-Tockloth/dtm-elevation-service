# ------------------------------------
# Function:
# - Cross-Compiles [macOS-arm64 -> linux-amd64] 'dtm-elevation-service' application.
#
# Version:
# - v1.0.0 - 2025-04-17: initial release
# - v1.1.0 - 2025-07-07: 1.24.3 -> 1.24.4
# - v1.2.0 - 2025-07-14: 1.24.4 -> 1.24.5
# - v1.3.0 - 2025-08-09: 1.24.5 -> 1.24.6
# - v1.4.0 - 2025-08-29: 1.24.6 -> 1.25.0
#
# Usage:
#  - docker build --progress=plain -t dtm-elevation-service-linux-amd64 .
#  - docker create --name temp_container dtm-elevation-service-linux-amd64
#  - docker cp temp_container:/app/dtm-elevation-service ./dtm-elevation-service
#  - docker rm temp_container
# ------------------------------------

# go version
FROM golang:1.25.0-bookworm AS builder

# install cross-compilation packages
# RUN apt-get update && apt-get install -y --no-install-recommends gcc-x86-64-linux-gnu
# RUN apt-get update && apt-get install -y --no-install-recommends libgdal-dev
# RUN apt-get update && apt-get install -y --no-install-recommends g++-x86-64-linux-gnu

 # install standard build tools and GDAL dev package (native for the target platform)
 RUN apt-get update && apt-get install -y --no-install-recommends \
 gcc \
 g++ \
 libc6-dev \
 libgdal-dev

# set working directory
WORKDIR /app

# copy go module files and load dependencies
COPY go.mod go.sum ./
RUN go mod download

# copy source code
COPY . .

 # build natively for the target platform (GOOS/GOARCH set by buildx)
 RUN CGO_ENABLED=1 go build -v -o dtm-elevation-service .

# show file stat of binary
RUN ls -la /app/dtm-elevation-service
