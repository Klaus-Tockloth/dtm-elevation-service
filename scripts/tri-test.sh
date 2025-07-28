#!/bin/bash
#
# Abfrage TRI für eine Kachel mit 1000x1000 Meter. 

postdata=$(cat <<EOF
{
  "Type": "TRIRequest",
  "ID": "Hegekopf, Edersee, Hessen",
  "Attributes": {
    "Zone": 0,
    "Easting": 0.0,
    "Northing": 0.0,
    "Longitude": 8.964229,
    "Latitude": 51.185913,
    "ColorTextFileContent": [
      "# Farbdefinition für TRI - Topographic Ruggedness Index",
      "# Format: Wert Rot Grün Blau Alpha",
      "0 154 205 50 255",
      "0.5 50 150 50 255",
      "2 255 165 0 255",
      "5 255 0 0 255",
      "10 139 0 0 255",
      "nv 0 0 0 0"
    ],
    "ColoringAlgorithm": "interpolation"
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
https://api.hoehendaten.de:14444/v1/tri

