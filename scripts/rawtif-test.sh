#!/bin/bash
#
# Abfrage der originalen GeoTIIF-Höhendaten für eine Kachel mit 1000x1000 Meter. 

postdata=$(cat <<EOF
{
  "Type": "RawTIFRequest",
  "ID": "Hegekopf, Edersee, Hessen",
  "Attributes": {
    "Zone": 32,
    "Easting": 497500.0,
    "Northing": 5670500.0
  }
}
EOF
)

echo "postdata = $postdata"

curl \
--silent \
--include \
--compressed \
--header "Content-Type: application/json" \
--header "Accept: application/json" \
--header "Accept-Encoding: gzip" \
--data "$postdata" \
https://api.hoehendaten.de:14444/v1/rawtif

