#!/bin/bash
#
# Abfrage der Höhendaten für alle Punkte einer GPX-Datei

gpxdata=$(cat <<EOF
<?xml version="1.0" encoding="UTF-8"?>
<gpx xmlns="http://www.topografix.com/GPX/1/1" version="1.1">
  <wpt lat="51.276389" lon="8.558333">
    <name>Langenberg (Rothaargebirge, höchster Berg in NRW)</name>
  </wpt>
  <wpt lat="51.179444" lon="8.488889">
    <name>Kahler Asten (Rothaargebirge, zweithöchster Berg in NRW)</name>
  </wpt>
</gpx>
EOF
)
gpxdataBase64=$(base64 -w 0 <<< "$gpxdata")

postdata=$(cat <<EOF
{
  "Type": "GPXRequest",
  "ID": "rothaargebirge.gpx",
  "Attributes": {
      "GPXData": "$gpxdataBase64"
  }
}
EOF
)

echo "postdata =\n$postdata"

curl \
--silent \
--include \
--header "Content-Type: application/json" \
--header "Accept: application/json" \
--data "$postdata" \
https://api.hoehendaten.de:14444/v1/gpx

