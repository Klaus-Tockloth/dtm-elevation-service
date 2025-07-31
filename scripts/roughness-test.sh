#!/bin/bash
#
# Abfrage Roughness für eine Kachel mit 1000x1000 Meter. 

postdata=$(cat <<EOF
{
  "Type": "RoughnessRequest",
  "ID": "Hegekopf, Edersee, Hessen",
  "Attributes": {
    "Zone": 0,
    "Easting": 0.0,
    "Northing": 0.0,
    "Longitude": 8.964229,
    "Latitude": 51.185913,
    "ColorTextFileContent": [
      "# Farbdefinition für Geländerauheit",
      "# Format: Wert Rot Grün Blau Alpha",
      "0.00 173 216 230 255",
      "0.20 57 176 130 255",
      "0.35 28 126 0 255",
      "0.50 255 200 0 255",
      "1.25 255 165 0 255",
      "2.00 255 0 0 255",
      "3.50 180 0 0 255",
      "5.00 0 0 0 255",
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
https://api.hoehendaten.de:14444/v1/roughness

