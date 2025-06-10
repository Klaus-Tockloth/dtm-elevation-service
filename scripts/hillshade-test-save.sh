#!/bin/bash
#
# Abfrage der Schummerung für eine Kachel mit 1000x1000 Meter.
# Das Skript wurde überarbeitet, um alle zurückgegebenen Hillshade-Objekte zu verarbeiten
# und den 'Origin'-Wert in den Dateinamen einzuschließen.

# Exit immediately if a command exits with a non-zero status.
set -e
# Exit if any command in a pipeline fails.
set -o pipefail

# --- Check for required tools ---
# Überprüfen, ob curl installiert ist
if ! command -v curl &> /dev/null
then
    echo "Fehler: curl ist nicht installiert." >&2
    exit 1
fi

# Überprüfen, ob jq installiert ist. jq wird zum Parsen der JSON-Antwort benötigt.
if ! command -v jq &> /dev/null
then
    echo "Fehler: jq ist nicht installiert. Bitte installieren Sie es (z.B. sudo apt-get install jq oder brew install jq)." >&2
    exit 1
fi

# Überprüfen, ob base64 installiert ist. base64 wird zum Dekodieren der Bilddaten benötigt.
# Unter Linux ist base64 Teil der GNU coreutils.
if ! command -v base64 &> /dev/null
then
    echo "Fehler: base64 ist nicht installiert. Bitte installieren Sie es (z.B. sudo apt-get install coreutils unter Linux)." >&2
    exit 1
fi
# --- End tool check ---


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

echo "Verwendete POST-Daten:"
echo "$postdataLonLat"
echo "--------------------"

# Führe den curl-Befehl aus und fange die Antwort ab.
# --silent: Unterdrückt Fortschrittsanzeigen und Fehlermeldungen von curl.
# --compressed: Fordert eine komprimierte Antwort an und dekomprimiert den HTTP-Body automatisch.
# --header: Setzt notwendige HTTP-Header.
# --data: Sendet die POST-Daten.
# Die Antwort wird in der Variable curl_response gespeichert.
curl_response=$(curl \
--silent \
--compressed \
--header "Content-Type: application/json" \
--header "Accept: application/json" \
--header "Accept-Encoding: gzip" \
--data "$postdataLonLat" \
https://api.hoehendaten.de:14444/v1/hillshade)

# Überprüfen, ob curl eine leere Antwort zurückgegeben hat.
if [ -z "$curl_response" ]; then
    echo "Fehler: Leere Antwort vom Server erhalten. Überprüfen Sie den Serverstatus oder die URL." >&2
    exit 1
fi

# --- Print truncated JSON response ---
echo "Vollständige JSON-Antwort (Data-Felder gekürzt):"
# Kürze das Data-Feld für jedes Element im Hillshades-Array
echo "$curl_response" | jq '(.Attributes.Hillshades[]?.Data |= (.[:48] + " ..."))'
echo "---------------------------------------------"
# --- End print truncated JSON response ---

# Überprüfen, ob das Attributes.Hillshades Array existiert und nicht leer ist
if ! echo "$curl_response" | jq -e '.Attributes.Hillshades | arrays and length > 0' > /dev/null; then
    echo "Fehler: JSON-Antwort enthält kein nicht-leeres 'Attributes.Hillshades' Array." >&2
    # Versucht, API-Fehlerdetails zu extrahieren und auszugeben, falls vorhanden
    api_error_code=$(echo "$curl_response" | jq -r '.Attributes.Error.Code // empty')
    api_error_detail=$(echo "$curl_response" | jq -r '.Attributes.Error.Detail // empty')
    if [ -n "$api_error_code" ] || [ -n "$api_error_detail" ]; then
      echo "--- API-Fehlerdetails ---" >&2
      echo "Code: $api_error_code" >&2
      echo "Detail: $api_error_detail" >&2
      echo "-------------------------" >&2
    else
      echo "Überprüfen Sie die JSON-Struktur der Antwort:" >&2
      echo "$curl_response" >&2
    fi
    exit 1
fi

echo "Verarbeite Hillshades aus der Antwort ..."

# Parse die JSON-Antwort mit jq, um die base64-Daten, den Kachel-Index und den Origin für jedes Objekt zu extrahieren.
# .Attributes.Hillshades[]: Iteriert über jedes Element des Hillshades-Arrays.
# |: Leitet das Ergebnis an den nächsten jq-Ausdruck weiter.
# "\(.Data) \(.TileIndex) \(.Origin)": Erstellt eine einzelne Zeichenkette pro Hillshade,
# die Data, TileIndex und Origin enthält, getrennt durch Leerzeichen.
# jq -r: Gibt rohe Zeichenketten ohne JSON-Anführungszeichen aus.
# <(...) : Prozess-Substitution, führt den Befehl aus und stellt seine Ausgabe als Datei bereit, aus der read liest.
# read -r: Liest eine Zeile in Variablen. -r verhindert die Interpretation von Backslash-Escapes.
# while read -r ...: Liest jede Zeile der jq-Ausgabe in die angegebenen Variablen und führt den Block aus.
echo "$curl_response" | jq -r '.Attributes.Hillshades[] | "\(.Data) \(.TileIndex) \(.Origin)"' | \
while read -r hillshade_data hillshade_tile_index hillshade_origin; do
  # Überprüfen, ob die extrahierten Variablen nicht leer sind
  if [ -z "$hillshade_data" ] || [ -z "$hillshade_tile_index" ] || [ -z "$hillshade_origin" ]; then
    echo "Warnung: Konnte Data, TileIndex oder Origin für ein Hillshade-Objekt nicht extrahieren. Überspringe." >&2
    # Gibt die Zeile aus, die nicht geparst werden konnte (falls nötig für Debugging)
    # echo "Problemzeile: $REPLY" >&2
    continue # Springt zum nächsten Element im while-Loop
  fi

  # Definiere den Ausgabedateinamen unter Verwendung des extrahierten Kachel-Indexes und Origins.
  # Format: TileIndex.Origin.hillshade.png
  output_filename="${hillshade_tile_index}.${hillshade_origin}.hillshade.png"

  echo "Verarbeite TileIndex: $hillshade_tile_index, Origin: $hillshade_origin"
  echo "Speichere Daten in: $output_filename"

  # Dekodiere die base64-Daten und speichere sie als Binärdatei (das PNG-Bild).
  echo "$hillshade_data" | base64 -d > "$output_filename"
  # Überprüfen, ob der base64 Befehl erfolgreich war
  if [ $? -ne 0 ]; then
      echo "Fehler: base64 Dekodierung fehlgeschlagen für ${output_filename}." >&2
      # Abhängig von den Anforderungen könnte man hier entscheiden, ob das Skript abbricht (exit 1)
      # oder mit den nächsten Kacheln fortfährt (continue). Hier brechen wir ab.
      exit 1
  fi

  echo "Schummerungsdaten erfolgreich in '$output_filename' gespeichert."
  echo "--------------------"
done

# Überprüfen, ob der jq-Befehl, der die Daten für den Loop extrahiert hat, fehlgeschlagen ist.
# $? enthält den Exit-Status des zuletzt ausgeführten Befehls vor der Pipe, hier 'jq'.
if [ ${PIPESTATUS[0]} -ne 0 ]; then
  echo "Fehler beim Ausführen von jq zum Extrahieren der Hillshade-Daten. Überprüfen Sie die jq-Syntax und die JSON-Struktur." >&2
  # Der vollständige Antwort-Body wurde bereits oben ausgegeben, falls nötig.
  exit 1
fi

echo "Alle verfügbaren Hillshades verarbeitet."

# Exit mit Erfolg-Status
exit 0
