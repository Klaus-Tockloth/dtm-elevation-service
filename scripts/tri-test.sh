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
      "# Farbdefinition für TRI",
      "# Format: Wert Rot Grün Blau Alpha",
      "0.00 173 216 230 255",
      "0.20 57 176 130 255",
      "0.40 104 151 0 255",
      "0.60 255 195 0 255",
      "0.80 255 186 0 255",
      "1.00 255 177 0 255",
      "1.20 255 167 0 255",
      "1.40 255 132 0 255",
      "1.60 255 88 0 255",
      "1.80 255 44 0 255",
      "2.00 255 0 0 255",
      "2.20 245 0 0 255",
      "2.40 235 0 0 255",
      "2.60 225 0 0 255",
      "2.80 215 0 0 255",
      "3.00 205 0 0 255",
      "3.20 195 0 0 255",
      "3.40 185 0 0 255",
      "3.60 168 0 0 255",
      "3.80 144 0 0 255",
      "4.00 120 0 0 255",
      "4.20 96 0 0 255",
      "4.40 72 0 0 255",
      "4.60 48 0 0 255",
      "4.80 24 0 0 255",
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
https://api.hoehendaten.de:14444/v1/tri

