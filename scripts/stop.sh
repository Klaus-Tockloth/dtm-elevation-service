#!/bin/sh
# ------------------------------------
# Purpose:
# - Start running DTM (Digital Terrain Model) Elevation Service.
#
# Releases:
# - v1.0.0 - 2025-04-23: initial release

# Remarks:
# - Accesses dgm1 data on /var/www/dgm1.
# ------------------------------------

# set -o xtrace
set -o verbose

# stop service
kill -2 $(ps fauxe | grep " \./dtm-elevation-service" | awk '{print $2}')

