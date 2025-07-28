#!/bin/bash
#
# Abfrage der Hangneigung für eine Kachel mit 1000x1000 Meter. 

# Kachel durch UTM-Koordinaten referenziert.
postdata=$(cat <<EOF
{
  "Type": "SlopeRequest",
  "ID": "Hegekopf, Edersee, Hessen",
  "Attributes": {
    "Zone": 32,
    "Easting": 497500.0,
    "Northing": 5670500.0,
    "Longitude": 0.0,
    "Latitude": 0.0,
    "GradientAlgorithm": "ZevenbergenThorne",
    "ColorTextFileContent": [
      "# Winkel- und Farbschema für die Abbildung von Hangneigungen",
      "# Musterdefinition als Basis für eigene Anpassungen",
      "# Format: Wert Rot Grün Blau Alpha",
      "0 0 100 0 255",
      "5 0 200 0 255",
      "10 100 255 0 255",
      "20 200 200 0 255",
      "30 255 150 0 255",
      "40 255 100 0 255",
      "45 255 0 0 255",
      "60 150 0 0 255",
      "90 0 0 0 255",
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
https://api.hoehendaten.de:14444/v1/slope

