#!/bin/bash
#
# Abfrage der Höhendaten für einen lon/lat Punkt 

postdata=$(cat <<EOF
{
  "Type": "PointRequest",
  "ID": "Langenberg (Rothaargebirge, höchster Berg in NRW)",
  "Attributes": {
      "Longitude": 8.558333,
      "Latitude": 51.276389
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
https://api.hoehendaten.de:14444/v1/point

