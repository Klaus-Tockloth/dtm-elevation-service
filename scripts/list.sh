#!/bin/sh
# ------------------------------------
# Function:
# - List running DTM (Digital Terrain Model) Elevation Service.
#
# Version:
# - v1.0.0 - 2024-04-23: initial release
# ------------------------------------
 
# set -o xtrace
set -o verbose
 
# list service
ps fauxe | grep " \./dtm-elevation-service" | awk '{print $2}'

