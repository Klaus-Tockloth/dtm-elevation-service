#!/bin/bash
#
# Abfrage der Höhenlinien für eine Kachel mit 1000x1000 Meter. 

# Kachel durch UTM-Koordinaten referenziert.
# Ergebnis: GeoJSON-Datei mit Höhenlinien in UTM-Koordinaten.
postdataUTM=$(cat <<EOF
{
  "Type": "ContoursRequest",
  "ID": "GPS-Referenzpunkt Hannover",
  "Attributes": {
    "Zone": 32,
    "Easting": 550251.23,
    "Northing": 5802052.35,
    "Longitude": 0.0,
    "Latitude": 0.0,
    "Equidistance": 1.0
  }
}
EOF
)

# oder

# Kachel durch lon/lat-Koordinaten referenziert.
# Ergebnis: GeoJSON-Datei mit Höhenlinien in lon/lat-Koordinaten.
postdataLonLat=$(cat <<EOF
{
  "Type": "ContoursRequest",
  "ID": "Langenberg (Rothaargebirge, höchster Berg in NRW)",
  "Attributes": {
    "Zone": 0,
    "Easting": 0.0,
    "Northing": 0.0,
    "Longitude": 8.558333,
    "Latitude": 51.276389,
    "Equidistance": 5.0
  }
}
EOF
)

echo "postdata = $postdataLonLat"

curl \
--silent \
--include \
--compressed \
--header "Content-Type: application/json" \
--header "Accept: application/json" \
--header "Accept-Encoding: gzip" \
--data "$postdataLonLat" \
https://api.hoehendaten.de:14444/v1/contours

