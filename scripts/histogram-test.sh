#!/bin/bash
#
# Abfrage Histogramm für eine Kachel mit 1000x1000 Meter. 

postdata=$(cat <<EOF
{
  "Type": "HistogramRequest",
  "ID": "Hegekopf, Edersee, Hessen",
  "Attributes": {
    "Zone": 0,
    "Easting": 0.0,
    "Northing": 0.0,
    "Longitude": 8.964229,
    "Latitude": 51.185913,
    "TypeOfVisualization": "slope",
    "GradientAlgorithm": "Horn",
    "TypeOfHistogram": "standard",
    "NumberOfBins": 20,
    "MinValue": "",
    "MaxValue": ""
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
https://api.hoehendaten.de:14444/v1/histogram

