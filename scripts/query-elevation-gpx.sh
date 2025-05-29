#!/bin/sh

# ------------------------------------
# Purpose:
# - Query elevation for all points (way, route, track) in given GPX file.
#
# Description:
# - Reads a local GPX file.
# - Encodes the GPX data using Base64.
# - Constructs a JSON payload containing the Base64-encoded GPX data.
# - Sends the JSON payload to a specified API endpoint using curl.
# - Receives a JSON response containing the processed GPX data (Base64 encoded)
#   and potentially data source attribution (licenses, copyrights, URLs).
# - Decodes the received GPX data.
# - Saves the decoded GPX data to a new output file.
# - Includes error handling for file operations, dependencies, network issues,
#   and API responses (both HTTP status and application-level errors).
#
# Releases:
# - v1.0.0 - 2025-04-19: initial release
# - v1.1.0 - 2025-04-23: filename schema modified
# - v1.2.0 - 2025-05-12: API URL modified
#
# Author:
# - Klaus Tockloth
#
# Copyright:
# - Copyright (c) 2025 Klaus Tockloth
#
# Contact (eMail):
# - klaus.tockloth@googlemail.com
#
# Remarks:
# - Required tools: curl, jq, base64, mktemp
# ------------------------------------

# Uncomment for debugging
# set -o xtrace
# set -o verbose

# --- Configuration ---
DEFAULT_API_URL="https://api.hoehendaten.de:14444/v1/gpx"

# --- Function Definitions ---

# Function to print usage instructions and exit
usage() {
  echo
  echo "Error: Incorrect number of arguments."
  echo
  echo "Usage: $0 <input_gpx_file> [api_url]"
  echo "<input_gpx_file> : The path to the GPX file to be sent."
  echo "[api_url]        : The URL of the API endpoint (default: ${DEFAULT_API_URL})."
  echo
  exit 1
}

# --- Argument Parsing and Validation ---

# Check argument count
if [ $# -lt 1 ] || [ $# -gt 2 ]; then
  usage # Call usage function which includes exit
fi

INPUT_GPX_FILE="$1"
# Set API_URL to the second argument if provided, otherwise use the default
API_URL="${2:-$DEFAULT_API_URL}"

# Check if the input file exists and is a regular file
if [ ! -f "$INPUT_GPX_FILE" ]; then
  echo "Error: Input file '$INPUT_GPX_FILE' not found or is not a regular file."
  exit 1
fi

# Check if an API URL has been set (should always be set due to default)
if [ -z "$API_URL" ]; then
  echo "Error: No API URL specified (neither as an argument nor as a default value)."
  exit 1
fi

# --- Dependency Checks ---

# Check if 'jq' is installed
if ! command -v jq > /dev/null 2>&1; then
    echo "Error: 'jq' is not installed. Please install it (e.g., 'sudo apt install jq' or 'brew install jq')."
    exit 1
fi

# Check if 'base64' is installed
if ! command -v base64 > /dev/null 2>&1; then
    echo "Error: 'base64' command not found. Please install the 'coreutils' package."
    exit 1
fi

# Check if 'curl' is installed (implicitly used, good to check)
if ! command -v curl > /dev/null 2>&1; then
    echo "Error: 'curl' command not found. Please install it."
    exit 1
fi

# Check if 'mktemp' is installed (usually part of coreutils)
if ! command -v mktemp > /dev/null 2>&1; then
    echo "Error: 'mktemp' command not found. Please install the 'coreutils' package."
    exit 1
fi

# --- Preparation ---

# Derive the output file name based on the input file name
FILENAME=$(basename "$INPUT_GPX_FILE")
# Check if the filename contains a dot (indicating a potential extension)
if [[ "$FILENAME" == *.* ]]; then
    # Separate name and extension and insert .dgm before the extension
    NAME_PART="${FILENAME%.*}"
    EXT_PART="${FILENAME##*.}"
    OUTPUT_GPX_FILE="${NAME_PART}.dgm.${EXT_PART}"
else
    # No dot found, just append .dgm
    OUTPUT_GPX_FILE="${FILENAME}.dgm"
fi

echo
echo "Querying elevation for all GPX points ..."
echo "------------------------------------------"
echo "GPX input file  : $INPUT_GPX_FILE"
echo "GPX output file : $OUTPUT_GPX_FILE"
echo "Query API URL   : $API_URL"
echo "------------------------------------------"

# Create temporary files safely using mktemp
TEMP_JSON_PAYLOAD_FILE=$(mktemp)
TEMP_RESPONSE_FILE=$(mktemp)

# Ensure temporary files are cleaned up automatically on script exit
# shellcheck disable=SC2064
trap 'rm -f "$TEMP_JSON_PAYLOAD_FILE" "$TEMP_RESPONSE_FILE"' EXIT HUP INT TERM

# --- Step 1: Read and Base64-encode the GPX file ---
echo
echo "1. Reading and encoding GPX data (Base64) ..."
BASE64_GPX_DATA=$(base64 -w 0 < "$INPUT_GPX_FILE")
if [ $? -ne 0 ] || [ -z "$BASE64_GPX_DATA" ]; then
    echo "Error: Could not read or Base64-encode the GPX file '$INPUT_GPX_FILE'."
    exit 1
fi
echo "Encoding successful."

# --- Step 2: Create JSON payload (GPXRequest) ---
echo
echo "2. Creating JSON request payload and writing to temporary file ..."
REQUEST_ID="GPX-$(date +%s)-$$" # $$ is standard sh for process ID
REQUEST_TYPE="GPXRequest"

cat <<EOF > "$TEMP_JSON_PAYLOAD_FILE"
{
  "Type": "$REQUEST_TYPE",
  "ID": "$REQUEST_ID",
  "Attributes": {
    "GPXData": "$BASE64_GPX_DATA"
  }
}
EOF

if [ $? -ne 0 ] || [ ! -s "$TEMP_JSON_PAYLOAD_FILE" ]; then
   echo "Error: Could not create or write JSON payload to temporary file '$TEMP_JSON_PAYLOAD_FILE'."
   exit 1
fi
echo "JSON payload created successfully in temporary file."

# --- Step 3: Send cURL request ---
echo
echo "3. Sending request to the API ($API_URL) using data from file ..."
HTTP_STATUS=$(curl --silent --request POST \
     --max-time 120 \
     --header 'Content-Type: application/json' \
     --header 'Accept: application/json' \
     --data-binary "@$TEMP_JSON_PAYLOAD_FILE" \
     --output "$TEMP_RESPONSE_FILE" \
     --write-out "%{http_code}" \
     "$API_URL")

CURL_EXIT_CODE=$?
if [ $CURL_EXIT_CODE -ne 0 ]; then
    echo "Error: curl command failed (Exit Code: $CURL_EXIT_CODE)."
    case $CURL_EXIT_CODE in
      6) echo "   Could not resolve host: '$API_URL'. Check DNS or hostname." ;;
      7) echo "   Could not connect to the server at '$API_URL'. Is it running? Is the URL/port correct? Check firewalls." ;;
      27) echo "   Write error. Possibly permissions or disk full when writing response to '$TEMP_RESPONSE_FILE'." ;;
      28) echo "   Operation timed out connecting to '$API_URL'." ;;
      *) echo "   Refer to curl error codes for more details." ;;
    esac
    exit 1
fi

unset TEMP_JSON_PAYLOAD_FILE

echo "API response received (HTTP Status: $HTTP_STATUS)."

# --- Step 4: Process API Response ---
echo
echo "4. Processing API response ..."

if [ "$HTTP_STATUS" -ne 200 ]; then
    echo "Error    : API request failed with HTTP Status $HTTP_STATUS."
    echo "Response :"
    if [ -s "$TEMP_RESPONSE_FILE" ]; then
        cat "$TEMP_RESPONSE_FILE"
        echo
    else
        echo "(No response body received)"
    fi
    exit 1
fi

if ! jq -e . >/dev/null 2>&1 < "$TEMP_RESPONSE_FILE"; then
    echo "Error: API response was not valid JSON, although HTTP Status was $HTTP_STATUS."
    echo "Raw response received:"
    cat "$TEMP_RESPONSE_FILE"
    exit 1
fi

# --- Step 4a: Check for Application-Level Errors in JSON ---
IS_ERROR=$(jq -r '.Attributes.IsError // false' < "$TEMP_RESPONSE_FILE")

if [ "$IS_ERROR" = "true" ]; then
    echo "Error: The API reported an application-level error in the response."
    ERROR_MESSAGE=$(jq -r '.Attributes.Error | if . then (.Message // .Code // "No error details available") else "No error object found" end' < "$TEMP_RESPONSE_FILE")
    echo "   Error details: $ERROR_MESSAGE"
    exit 1
fi

# --- Step 4b: Extract Processed GPX Data and Attribution Info ---
# Extract the Base64-encoded GPX data
BASE64_RESPONSE_GPX=$(jq -r '.Attributes.GPXData // empty' < "$TEMP_RESPONSE_FILE")

if [ -z "$BASE64_RESPONSE_GPX" ]; then
    echo "Error: Could not find '.Attributes.GPXData' in the API response, or the value was empty."
    echo "Complete server response:"
    cat "$TEMP_RESPONSE_FILE"
    exit 1
fi

# --- Step 4c: Extract Statistics ---
GPX_POINTS=$(jq -r '.Attributes.GPXPoints // empty' < "$TEMP_RESPONSE_FILE")
DGM_POINTS=$(jq -r '.Attributes.DGMPoints // empty' < "$TEMP_RESPONSE_FILE")

echo "Successful response received, extracting GPX data and attribution."

# Use jq to extract the array elements and print each on a new line.
# If the array does not exist or is empty, jq -r '.[]' on null or empty array
# results in no output, which is handled by the 'if [ -n "$ATTRIBUTIONS_LINES" ]' check.
ATTRIBUTIONS_LINES=$(jq -r '.Attributes.Attributions[]? // empty' < "$TEMP_RESPONSE_FILE")

# --- Step 5: Decode received GPX data and save it ---
echo
echo "5. Decoding received GPX data (Base64) and saving it ..."
echo "$BASE64_RESPONSE_GPX" | base64 --decode > "$OUTPUT_GPX_FILE"

if [ $? -ne 0 ]; then
    echo "Error: Could not decode the received Base64 data or write to '$OUTPUT_GPX_FILE'."
    rm -f "$OUTPUT_GPX_FILE"
    exit 1
fi

echo "GPX data successfully decoded and saved to '$OUTPUT_GPX_FILE'."

# --- Completion ---
echo
echo "GPX Details ..."
echo "------------------------------------------"
echo "GPX input file  : $INPUT_GPX_FILE"
echo "GPX output file : $OUTPUT_GPX_FILE"
echo "GPX points      : $GPX_POINTS"
echo "DGM points      : $DGM_POINTS"
# Check if any attribution lines were produced
if [ -n "$ATTRIBUTIONS_LINES" ]; then
  # Read each line and print it with the required prefix
  echo "$ATTRIBUTIONS_LINES" | while IFS= read -r line; do
    echo "Attribution     : $line"
  done
fi

echo "------------------------------------------"
echo

# Exit with success code
exit 0
