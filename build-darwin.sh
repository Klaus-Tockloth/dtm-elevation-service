#!/bin/sh

# ------------------------------------
# Purpose:
# - Build binary for all target system (from macOS).
#
# Releases:
# - v1.0.0 - 2025-04-17: first release
# - v1.1.0 - 2025-05-06: CGO_CFLAGS for gdal added
# - v1.2.0 - 2025-05-12: CGO_LDFLAGS for gdal added
# - v1.3.0 - 2025-06-07: gdal library configuration modified
# ------------------------------------

# set -o xtrace
set -o verbose

# renew vendor content
go mod vendor

# lint
golangci-lint run --no-config --enable gocritic
revive

# security
govulncheck ./...
gosec -ai-api-provider="gemini" -exclude=G115,G304 ./...

# show compiler version
go version

# current installed version of gdal library
export CGO_CFLAGS="$(pkg-config --cflags gdal)"
export CGO_LDFLAGS="$(pkg-config --libs gdal)"

# compile 'darwin' (native on macOS, arm64 -> arm64)
go build -v -o build/darwin-arm64/dtm-elevation-service
