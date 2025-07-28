#!/bin/bash
#
# Abfrage TRI für eine Kachel mit 1000x1000 Meter. 

postdata=$(cat <<EOF
{
  "Type": "TRIRequest",
  "ID": "Hegekopf, Edersee, Hessen",
  "Attributes": {
    "Zone": 0,
    "Easting": 0.0,
    "Northing": 0.0,
    "Longitude": 8.964229,
    "Latitude": 51.185913,
    "ColorTextFileContent": [
      "# Farbdefinition für TRI - Topographic Ruggedness Index",
      "# Format: Wert Rot Grün Blau Alpha",
      "0 154 205 50 255",
      "0.5 50 150 50 255",
      "2 255 165 0 255",
      "5 255 0 0 255",
      "10 139 0 0 255",
      "nv 0 0 0 0"
    ],
    "ColoringAlgorithm": "interpolation"
  }
}
EOF
)

echo "postdata = $postdata"

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
--data "$postdata" \
https://api.hoehendaten.de:14444/v1/tri)

# Überprüfen, ob curl eine leere Antwort zurückgegeben hat.
if [ -z "$curl_response" ]; then
    echo "Fehler: Leere Antwort vom Server erhalten. Überprüfen Sie den Serverstatus oder die URL." >&2
    exit 1
fi

# --- Print truncated JSON response ---
echo "Vollständige JSON-Antwort (Data-Felder gekürzt):"
# Kürze das Data-Feld für jedes Element im TRIs-Array
echo "$curl_response" | jq '(.Attributes.TRIs[]?.Data |= (.[:48] + " ..."))'
echo "---------------------------------------------"
# --- End print truncated JSON response ---

# Überprüfen, ob das Attributes.TRIs Array existiert und nicht leer ist
if ! echo "$curl_response" | jq -e '.Attributes.TRIs | arrays and length > 0' > /dev/null; then
    echo "Fehler: JSON-Antwort enthält kein nicht-leeres 'Attributes.TRIs' Array." >&2
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

echo "Verarbeite TRIs aus der Antwort ..."

# Parse die JSON-Antwort mit jq, um die base64-Daten, den Kachel-Index und den Origin für jedes Objekt zu extrahieren.
# .Attributes.TRIs[]: Iteriert über jedes Element des TRIs-Arrays.
# |: Leitet das Ergebnis an den nächsten jq-Ausdruck weiter.
# "\(.Data) \(.TileIndex) \(.Origin)": Erstellt eine einzelne Zeichenkette pro TRI,
# die Data, TileIndex und Origin enthält, getrennt durch Leerzeichen.
# jq -r: Gibt rohe Zeichenketten ohne JSON-Anführungszeichen aus.
# <(...) : Prozess-Substitution, führt den Befehl aus und stellt seine Ausgabe als Datei bereit, aus der read liest.
# read -r: Liest eine Zeile in Variablen. -r verhindert die Interpretation von Backslash-Escapes.
# while read -r ...: Liest jede Zeile der jq-Ausgabe in die angegebenen Variablen und führt den Block aus.
echo "$curl_response" | jq -r '.Attributes.TRIs[] | "\(.Data) \(.TileIndex) \(.Origin)"' | \
while read -r tri_data tri_tile_index tri_origin; do
  # Überprüfen, ob die extrahierten Variablen nicht leer sind
  if [ -z "$tri_data" ] || [ -z "$tri_tile_index" ] || [ -z "$tri_origin" ]; then
    echo "Warnung: Konnte Data, TileIndex oder Origin für ein TRI-Objekt nicht extrahieren. Überspringe." >&2
    # Gibt die Zeile aus, die nicht geparst werden konnte (falls nötig für Debugging)
    # echo "Problemzeile: $REPLY" >&2
    continue # Springt zum nächsten Element im while-Loop
  fi

  # Definiere den Ausgabedateinamen unter Verwendung des extrahierten Kachel-Indexes und Origins.
  # Format: TileIndex.Origin.tri.png
  output_filename="${tri_tile_index}.${tri_origin}.tri.png"

  echo "Verarbeite TileIndex: $tri_tile_index, Origin: $tri_origin"
  echo "Speichere Daten in: $output_filename"

  # Dekodiere die base64-Daten und speichere sie als Binärdatei (das PNG-Bild).
  echo "$tri_data" | base64 -d > "$output_filename"
  # Überprüfen, ob der base64 Befehl erfolgreich war
  if [ $? -ne 0 ]; then
      echo "Fehler: base64 Dekodierung fehlgeschlagen für ${output_filename}." >&2
      # Abhängig von den Anforderungen könnte man hier entscheiden, ob das Skript abbricht (exit 1)
      # oder mit den nächsten Kacheln fortfährt (continue). Hier brechen wir ab.
      exit 1
  fi

  echo "Hangneigungsdaten erfolgreich in '$output_filename' gespeichert."
  echo "--------------------"
done

# Überprüfen, ob der jq-Befehl, der die Daten für den Loop extrahiert hat, fehlgeschlagen ist.
# $? enthält den Exit-Status des zuletzt ausgeführten Befehls vor der Pipe, hier 'jq'.
if [ ${PIPESTATUS[0]} -ne 0 ]; then
  echo "Fehler beim Ausführen von jq zum Extrahieren der TRI-Daten. Überprüfen Sie die jq-Syntax und die JSON-Struktur." >&2
  # Der vollständige Antwort-Body wurde bereits oben ausgegeben, falls nötig.
  exit 1
fi

echo "Alle verfügbaren TRIs verarbeitet."

# Exit mit Erfolg-Status
exit 0
