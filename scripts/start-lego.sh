#!/bin/sh

# ------------------------------------
# Purpose:
# - Retrieve TLS certificates from letsencrypt using lego client.
#
# Releases:
# - v1.0.0 - 2025-04-18: initial release

# Remarks:
# - Private key and certificate are in '.lego' directory.
# - Requires sudo to create directory .well-known in webroot.
# ------------------------------------

# set -o xtrace
set -o verbose

# start lego client (port 80, Apache webroot)
sudo ./lego \
--accept-tos \
--email 'klaus.tockloth@googlemail.com' \
--key-type ec384 \
--http \
--http.webroot '/var/www/html/www.fzk' \
--domains 'elevation.freizeitkarte-osm.de' \
run

