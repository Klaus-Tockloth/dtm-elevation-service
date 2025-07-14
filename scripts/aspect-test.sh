#!/bin/bash
#
# Abfrage der Hangexposition für eine Kachel mit 1000x1000 Meter. 

# Kachel durch UTM-Koordinaten referenziert.
postdata=$(cat <<EOF
{
  "Type": "AspectRequest",
  "ID": "Hegekopf, Edersee, Hessen",
  "Attributes": {
    "Zone": 0,
    "Easting": 0.0,
    "Northing": 0.0,
    "Longitude": 8.964229,
    "Latitude": 51.185913,
    "GradientAlgorithm": "ZevenbergenThorne",
    "ColorTextFileContent": [
      "# Winkel- und Farbschema für die Abbildung von Hangexpositionen.",
      "# Musterdefinition als Basis für eigene Anpassungen.",
      "# Format: Wert Rot Grün Blau Alpha",
      "0 190 190 220 255",
      "22.5 180 220 200 255",
      "67.5 255 255 180 255",
      "112.5 255 220 160 255",
      "157.5 255 180 120 255",
      "202.5 245 190 130 255",
      "247.5 200 210 230 255",
      "292.5 190 190 220 255",
      "337.5 190 190 220 255",
      "360.0 190 190 220 255",
      "nv 0 0 0 0"
    ]
  }
}
EOF
)

echo "postdata =\n$postdata"

curl \
--silent \
--include \
--compressed \
--header "Content-Type: application/json" \
--header "Accept: application/json" \
--header "Accept-Encoding: gzip" \
--data "$postdata" \
https://api.hoehendaten.de:14444/v1/aspect

