#!/bin/bash
#
# Abfrage ColorRelief für eine Kachel mit 1000x1000 Meter. 

postdata=$(cat <<EOF
{
  "Type": "ColorReliefRequest",
  "ID": "Hegekopf, Edersee, Hessen",
  "Attributes": {
    "Zone": 0,
    "Easting": 0.0,
    "Northing": 0.0,
    "Longitude": 8.964229,
    "Latitude": 51.185913,
    "ColorTextFileContent": [
      "# Farbdefinition für ColorRelief",
      "# Format: Wert Rot Grün Blau Alpha",
      "0 0 0 139 255",
      "200 0 191 255 255",
      "400 34 139 34 255",
      "600 50 205 50 255",
      "800 173 255 47 255",
      "1000 255 255 0 255",
      "1200 255 165 0 255",
      "1400 255 100 0 255",
      "1600 255 0 0 255",
      "1800 200 0 0 255",
      "2000 139 69 19 255",
      "2200 169 169 169 255",
      "2400 192 192 192 255",
      "2600 255 255 255 255",
      "nv 0 0 0 255"
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
https://api.hoehendaten.de:14444/v1/colorrelief

