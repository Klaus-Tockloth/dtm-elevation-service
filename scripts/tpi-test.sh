#!/bin/bash
#
# Abfrage TPI für eine Kachel mit 1000x1000 Meter. 

postdata=$(cat <<EOF
{
  "Type": "TPIRequest",
  "ID": "Hegekopf, Edersee, Hessen",
  "Attributes": {
    "Zone": 0,
    "Easting": 0.0,
    "Northing": 0.0,
    "Longitude": 8.964229,
    "Latitude": 51.185913,
    "ColorTextFileContent": [
      "# Farbdefinition für TPI",
      "# Format: Wert Rot Grün Blau Alpha",
      "# Senken: kleiner -0.05",
      "# Ebenen: -0.05 ... 0.05",
      "# Kuppen: größer 0.05",
      "-0.050001 0 0 0 255",
      "-0.05 255 255 255 255",
      "0.05 255 255 255 255",
      "0.050001 0 0 0 255",
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
https://api.hoehendaten.de:14444/v1/tpi

