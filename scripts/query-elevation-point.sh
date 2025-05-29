#!/bin/sh

# ------------------------------------
# Purpose:
# - Query elevation for given lon/lat point.
#
# Description:
# - Sends longitude and latitude coordinates to a specified API endpoint.
# - Parses the JSON response to extract and display geographical details
#   (e.g., Easting, Northing, Elevation, Zone, etc.).
# - Includes basic error handling for network issues and API responses.
#
# Releases:
# - v1.0.0 - 2025-04-19: initial release
# - v1.1.0 - 2025-05-12: API URL modified
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
# - Required tools: curl, jq
# ------------------------------------

# Uncomment for debugging
# set -o xtrace
# set -o verbose

# --- Configuration ---
DEFAULT_API_URL="https://api.hoehendaten.de:14444/v1/point"

# --- Function Definitions ---

# Function to print usage instructions and exit
usage() {
  echo
  echo "Usage: $0 <longitude> <latitude> [api_url]"
  echo "<longitude> : The longitude (decimal degrees, e.g., 7.5259)."
  echo "<latitude>  : The latitude (decimal degrees, e.g., 51.9556)."
  echo "[api_url]   : The URL of the API endpoint (default: ${DEFAULT_API_URL})."
  echo
  exit 1
}

# Function to check if a string looks like a valid floating point number
# Note: This is a basic validation.
is_float() {
    # Basic check: allows optional sign, digits, optional decimal part.
    # Does not handle scientific notation or locale-specific separators.
    case "$1" in
        ''|*[!-.0-9]*) return 1 ;; # Contains invalid chars or is empty
        *.*.*) return 1 ;;         # More than one decimal point
        *-*) return 1 ;;           # More than one hyphen or hyphen not at start
        -*.?*) ;;                  # Negative float okay
        -?*) return 1 ;;           # Hyphen but not followed by digit/dot
        *.) ;;                     # Ends with dot okay (e.g. 10.)
        .*) ;;                     # Starts with dot okay (e.g. .5)
        *) ;;                      # Integer or float without leading/trailing dot
    esac
    # Check if it's just "." or "-"
    case "$1" in
        .|-) return 1 ;;
    esac
    return 0 # Looks like a float
}

# --- Argument Parsing and Validation ---

# Check argument count
if [ $# -lt 2 ] || [ $# -gt 3 ]; then
  echo
  echo "Error: Incorrect number of arguments."
  usage
fi

LONGITUDE="$1"
LATITUDE="$2"
# Set API_URL to the third argument if provided, otherwise use the default
API_URL="${3:-$DEFAULT_API_URL}"

# Validate latitude and longitude format
if ! is_float "$LATITUDE"; then
    echo "Error: Invalid format for latitude: '$LATITUDE'. Please provide a number."
    exit 1
fi
if ! is_float "$LONGITUDE"; then
    echo "Error: Invalid format for longitude: '$LONGITUDE'. Please provide a number."
    exit 1
fi

# Check if an API URL has been set
if [ -z "$API_URL" ]; then
  echo "Error: No API URL specified (neither as an argument nor as a default value)."
  exit 1
fi

# --- Dependency Checks ---

# Check if 'curl' is installed
if ! command -v curl > /dev/null 2>&1; then
    echo "Error: 'curl' command not found. Please install it."
    exit 1
fi

# Check if 'jq' is installed
if ! command -v jq > /dev/null 2>&1; then
    echo "Error: 'jq' command not found. Please install it (e.g. 'sudo apt install jq' or 'brew install jq')."
    exit 1
fi

# Check if 'mktemp' is installed (less common to be missing, but good practice)
if ! command -v mktemp > /dev/null 2>&1; then
    echo "Error: 'mktemp' command not found. Please install the appropriate package (often 'coreutils' or 'util-linux')."
    exit 1
fi

# --- Preparation ---
echo
echo "Querying elevation for lon/lat point ..."
echo "------------------------------------------"
echo "Longitude    : $LONGITUDE"
echo "Latitude     : $LATITUDE"
echo "API Endpoint : $API_URL"
echo "------------------------------------------"

# Create a temporary file for the API response safely
TEMP_RESPONSE_FILE=$(mktemp)

# Ensure the temporary file is cleaned up on exit (including errors, SIGINT, SIGTERM)
# shellcheck disable=SC2064 # We want $TEMP_RESPONSE_FILE evaluated now for the trap
trap 'rm -f "$TEMP_RESPONSE_FILE"' EXIT HUP INT TERM

# --- Create JSON Payload ---
echo
echo "1. Creating JSON request payload ..."
# Generate a unique request ID using timestamp and process ID
REQUEST_ID="Point-$(date +%s)-$$"
REQUEST_TYPE="PointRequest"

# Create JSON payload string using printf for better handling of variables, especially floats.
# Note: POSIX printf float handling might vary slightly across implementations.
JSON_PAYLOAD=$(printf '{
  "Type": "%s",
  "ID": "%s",
  "Attributes": {
    "Longitude": %s,
    "Latitude": %s
  }
}' "$REQUEST_TYPE" "$REQUEST_ID" "$LONGITUDE" "$LATITUDE")

# Basic check if payload creation seemed successful
if [ -z "$JSON_PAYLOAD" ]; then
   echo "Error: Failed to create JSON payload string."
   # trap will clean up temp file
   exit 1
fi
echo "JSON payload created."
# echo "Payload: $JSON_PAYLOAD" # Uncomment for debugging the payload

# --- Send cURL Request ---
echo
echo "2. Sending request to the API ($API_URL) ..."
# Execute curl: POST request, set headers, send JSON data,
# write response body to temp file, write HTTP status code to stdout.
HTTP_STATUS=$(curl --silent --request POST \
     --max-time 120 \
     --header 'Content-Type: application/json' \
     --header 'Accept: application/json' \
     --data "$JSON_PAYLOAD" \
     --output "$TEMP_RESPONSE_FILE" \
     --write-out "%{http_code}" \
     "$API_URL")

# Check if the curl command itself encountered an error (e.g., network issue)
CURL_EXIT_CODE=$?
if [ $CURL_EXIT_CODE -ne 0 ]; then
    echo "Error: curl command failed (Exit Code: $CURL_EXIT_CODE)."
    # Provide more specific feedback based on common curl exit codes
    case $CURL_EXIT_CODE in
      6) echo "   Could not resolve host: '$API_URL'. Check DNS configuration or hostname validity." ;;
      7) echo "   Could not connect to the server at '$API_URL'. Is the server running? Is the URL/port correct? Check firewalls." ;;
      28) echo "   Operation timed out connecting to '$API_URL'." ;;
      *) echo "   Refer to curl error codes for more details: https://curl.se/libcurl/c/libcurl-errors.html" ;;
    esac
    # trap handles temp file cleanup
    exit 1
fi

echo "API response received (HTTP Status: $HTTP_STATUS)."

# --- Process Response ---
echo
echo "3. Processing API response ..."

# Check if the HTTP status indicates success (expecting 200 OK)
if [ "$HTTP_STATUS" -ne 200 ]; then
    echo "Error    : API request failed with HTTP Status $HTTP_STATUS."
    echo "Response :"
    # Check if the temporary response file has content before attempting to display it
    if [ -s "$TEMP_RESPONSE_FILE" ]; then
        cat "$TEMP_RESPONSE_FILE"
        echo # Add a newline for better formatting if JSON was output
    else
        echo "(No response body received from API)"
    fi
    # trap handles temp file cleanup
    exit 1
fi

# Validate that the response body contains valid JSON before attempting jq parsing
if ! jq -e . >/dev/null 2>&1 < "$TEMP_RESPONSE_FILE"; then
    echo "Error: API response was not valid JSON, although HTTP Status was $HTTP_STATUS."
    echo "Raw response received:"
    cat "$TEMP_RESPONSE_FILE"
    # trap handles temp file cleanup
    exit 1
fi

# Parse the JSON response using jq
# Check for an application-level error indicated by the 'IsError' flag in the response.
# Default to 'false' if the 'IsError' field is missing entirely (using jq's // operator).
IS_ERROR=$(jq -r '.Attributes.IsError // false' < "$TEMP_RESPONSE_FILE")

if [ "$IS_ERROR" = "true" ]; then
    echo "Error: The API reported an application-level error in the response."
    # Attempt to extract and display specific error details from the response JSON
    ERROR_MESSAGE=$(jq -r '.Attributes.Error | if . then (.Message // .Detail // .Code // "No specific error details available in response") else "No error object found in response Attributes" end' < "$TEMP_RESPONSE_FILE")
    echo "   Error details: $ERROR_MESSAGE"
    # Optional: Output the entire error object from the JSON for detailed debugging
    # echo "   Complete error object:"
    # jq '.Attributes.Error' < "$TEMP_RESPONSE_FILE"
    # trap handles temp file cleanup
    exit 1
fi

echo "Successful response received, extracting details ..."

# --- Extract and Display Data ---
# Extract relevant fields from the JSON response using jq.
# Use the '// "N/A"' fallback in jq to handle cases where a field might be missing or null in the response.
LONGITUDE_RESP=$(jq -r '.Attributes.Longitude // "N/A"' < "$TEMP_RESPONSE_FILE")
LATITUDE_RESP=$(jq -r '.Attributes.Latitude // "N/A"' < "$TEMP_RESPONSE_FILE")
ELEVATION=$(jq -r '.Attributes.Elevation // "N/A"' < "$TEMP_RESPONSE_FILE")
ACTUALITY=$(jq -r '.Attributes.Actuality // "N/A"' < "$TEMP_RESPONSE_FILE")
ATTRIBUTION=$(jq -r '.Attributes.Attribution // "N/A"' < "$TEMP_RESPONSE_FILE")
TILE_INDEX=$(jq -r '.Attributes.TileIndex // "N/A"' < "$TEMP_RESPONSE_FILE")

# Display the extracted information
echo
echo "Point Details ..."
echo "------------------------------------------"
echo "Longitude     : $LONGITUDE_RESP"
echo "Latitude      : $LATITUDE_RESP"
echo "Elevation     : $ELEVATION"
echo "Actuality     : $ACTUALITY"
echo "Attribution   : $ATTRIBUTION"
echo "Tile Index    : $TILE_INDEX"
echo "------------------------------------------"

# The temporary response file is automatically removed by the trap defined earlier

echo
echo "Processing completed successfully!"
echo

exit 0
