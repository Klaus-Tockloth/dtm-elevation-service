#!/bin/bash
#
# Abfrage der Schummerung für eine Kachel mit 1000x1000 Meter. 

# Kachel durch UTM-Koordinaten referenziert.
postdataUTM=$(cat <<EOF
{
  "Type": "HillshadeRequest",
  "ID": "Klettersteinbruch Brumleytal",
  "Attributes": {
    "Zone": 32,
    "Easting": 409000.0,
    "Northing": 5790000.0,
    "Longitude": 0.0,
    "Latitude": 0.0,
    "GradientAlgorithm": "Horn",
    "VerticalExaggeration": 1.0,
    "AzimuthOfLight": 315,
    "AltitudeOfLight": 45,
    "ShadingVariant": "regular"
  }
}
EOF
)

# oder

# Kachel durch lon/lat-Koordinaten referenziert.
postdataLonLat=$(cat <<EOF
{
  "Type": "HillshadeRequest",
  "ID": "Langenberg (Rothaargebirge, höchster Berg in NRW)",
  "Attributes": {
    "Zone": 0,
    "Easting": 0.0,
    "Northing": 0.0,
    "Longitude": 8.558333,
    "Latitude": 51.276389,
    "GradientAlgorithm": "ZevenbergenThorne",
    "VerticalExaggeration": 1.0,
    "AzimuthOfLight": 315,
    "AltitudeOfLight": 45,
    "ShadingVariant": "regular"
  }
}
EOF
)

echo "postdata = $postdataUTM"

curl \
--silent \
--include \
--compressed \
--header "Content-Type: application/json" \
--header "Accept: application/json" \
--header "Accept-Encoding: gzip" \
--data "$postdataUTM" \
https://api.hoehendaten.de:14444/v1/hillshade

