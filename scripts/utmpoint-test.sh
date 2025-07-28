#!/bin/bash
#
# Abfrage der Höhendaten für einen UTM Punkt 

postdata=$(cat <<EOF
{
  "Type": "UTMPointRequest",
  "ID": "GPS-Referenzpunkt Hannover",
  "Attributes": {
      "Zone": 32,
      "Easting": 550251.23,
      "Northing": 5802052.35
  }
}
EOF
)

echo "postdata = $postdata"

curl \
--silent \
--include \
--header "Content-Type: application/json" \
--header "Accept: application/json" \
--data "$postdata" \
https://api.hoehendaten.de:14444/v1/utmpoint

