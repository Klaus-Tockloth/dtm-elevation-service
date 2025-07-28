#!/bin/bash
#
# Analyse der Höhendaten für alle Punkte einer GPX-Datei

gpxdata=$(cat <<EOF
<?xml version="1.0" encoding="UTF-8"?>
<gpx xmlns="http://www.topografix.com/GPX/1/1" version="1.1">
  <trk>
    <name>Ahornweg-Bad-Iburg</name>
    <trkseg>
			<trkpt lat="52.15532" lon="8.0607952">
				<ele>142.7689971923828</ele>
				<time>2024-06-28T07:24:09Z</time>
			</trkpt>
			<trkpt lat="52.1553277" lon="8.0608346">
				<ele>154.16900634765625</ele>
				<time>2024-06-28T07:24:19Z</time>
			</trkpt>
			<trkpt lat="52.1553271" lon="8.0609262">
				<ele>153.83700561523438</ele>
				<time>2024-06-28T07:24:29Z</time>
			</trkpt>
			<trkpt lat="52.155319" lon="8.0610889">
				<ele>162.6750030517578</ele>
				<time>2024-06-28T07:24:39Z</time>
			</trkpt>
			<trkpt lat="52.1553107" lon="8.0612431">
				<ele>162.12899780273438</ele>
				<time>2024-06-28T07:24:49Z</time>
			</trkpt>
			<trkpt lat="52.1553158" lon="8.0613957">
				<ele>164.51400756835938</ele>
				<time>2024-06-28T07:24:59Z</time>
			</trkpt>
    </trkseg>
  </trk>
</gpx>
EOF
)
gpxdataBase64=$(base64 -w 0 <<< "$gpxdata")

postdata=$(cat <<EOF
{
  "Type": "GPXAnalyzeRequest",
  "ID": "Ahornweg-Bad-Iburg.gpx",
  "Attributes": {
      "GPXData": "$gpxdataBase64"
  }
}
EOF
)

echo "postdata = $postdata"

curl \
--silent \
--include \
--header "Content-Type: application/json" \
--header "Accept: application/json" \
--data "$postdata" \
https://api.hoehendaten.de:14444/v1/gpxanalyze

