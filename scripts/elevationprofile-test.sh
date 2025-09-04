#!/bin/bash
#
# Shell-Script zum Testen der 'elevationprofile' API.
# 1. Test: Abfrage mit UTM-Koordinaten.
# 2. Test: Abfrage mit den equivalenten Lon/Lat-Koordinaten.
#

echo "======================================================================"
echo "1. Starte Test mit UTM-Koordinaten..."
echo "======================================================================"

# POST-Daten für die UTM-Abfrage
postdata_utm=$(cat <<EOF
{
  "Type": "ElevationProfileRequest",
  "ID": "Test-Profil Maifisch UTM",
  "Attributes": {
    "PointA": {
      "Zone": 32,
      "Easting": 378024.474,
      "Northing": 5731697.272
    },
    "PointB": {
      "Zone": 32,
      "Easting": 378053.110,
      "Northing": 5731561.275
    },
    "MaxTotalProfilePoints": 50,
    "MinStepSize": 1.0
  }
}
EOF
)

# Sende die UTM-Anfrage mit curl
curl \
--insecure \
--silent \
--include \
--compressed \
--header "Content-Type: application/json" \
--header "Accept: application/json" \
--header "Accept-Encoding: gzip" \
--data "$postdata_utm" \
https://api.hoehendaten.de:14444/v1/elevationprofile

echo ""
echo ""
echo "======================================================================"
echo "2. Starte Test mit Lon/Lat-Koordinaten..."
echo "======================================================================"

# POST-Daten für die Lon/Lat-Abfrage
# Koordinaten sind umgerechnete Werte der obigen UTM-Punkte.
# PointA: Lon: 7.234057, Lat: 51.722921
# PointB: Lon: 7.234519, Lat: 51.721705
postdata_lonlat=$(cat <<EOF
{
  "Type": "ElevationProfileRequest",
  "ID": "Test-Profil Maifisch LonLat",
  "Attributes": {
    "PointA": {
      "Longitude": 7.234057,
      "Latitude": 51.722921
    },
    "PointB": {
      "Longitude": 7.234519,
      "Latitude": 51.721705
    },
    "MaxTotalProfilePoints": 50,
    "MinStepSize": 1.0
  }
}
EOF
)

# Sende die Lon/Lat-Anfrage mit curl
curl \
--insecure \
--silent \
--include \
--compressed \
--header "Content-Type: application/json" \
--header "Accept: application/json" \
--header "Accept-Encoding: gzip" \
--data "$postdata_lonlat" \
https://api.hoehendaten.de:14444/v1/elevationprofile

echo ""
echo "======================================================================"
echo "Tests abgeschlossen."
echo "======================================================================"
