#!/bin/sh

# ------------------------------------
# Purpose:
# - Query elevation for given UTM point.
#
# Description:
# - Sends UTM coordinates (Zone, Easting, Northing) to a specified API endpoint.
# - Parses the JSON response to extract and display geographical details
#   (e.g., Zone, Easting, Northing, Elevation, Actuality, Attribution, Tile Index).
# - Includes basic error handling for network issues and API responses.
#
# Releases:
# - v1.0.0 - 2025-05-12: initial release
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
DEFAULT_API_URL="https://api.hoehendaten.de:14444/v1/utmpoint"

# --- Function Definitions ---

# Function to print usage instructions and exit
usage() {
  echo
  echo "Usage: $0 <zone> <easting> <northing> [api_url]"
  echo "<zone>      : The UTM zone (integer, e.g., 33)."
  echo "<easting>   : The Easting coordinate (decimal, e.g., 385444.7)."
  echo "<northing>  : The Northing coordinate (decimal, e.g., 5817176.0)."
  echo "[api_url]   : The URL of the API endpoint (default: ${DEFAULT_API_URL})."
  echo
  exit 1
}

# Function to check if a string looks like a valid integer
is_integer() {
    # Basic check: allows optional sign, followed by one or more digits.
    # Does not handle locale-specific issues or overflow.
    case "$1" in
        ''|*[!0-9+-]*) return 1 ;; # Contains invalid chars or is empty
        *[-+]*)        # Contains sign, check if it's only at the beginning
          case "$1" in
            +*|-[*]) ;; # Valid: sign at start
            *) return 1 ;; # Invalid: sign not at start
          esac
        ;;
    esac
     # Check if it's just "+" or "-"
    case "$1" in
        +|-) return 1 ;;
    esac
    return 0 # Looks like an integer
}

# Function to check if a string looks like a valid floating point number
# Note: This is a basic validation.
is_float() {
    # Basic check: allows optional sign, digits, optional decimal part.
    # Does not handle scientific notation or locale-specific separators.
    case "$1" in
        ''|*[!-.0-9]*) return 1 ;; # Contains invalid chars or is empty
        *.*.*) return 1 ;;         # More than one decimal point
        *-*) # Contains hyphen, check if it's only at the beginning
            case "$1" in
              -[*]) ;; # Valid: hyphen at start
              *) return 1 ;; # Invalid: hyphen not at start
            esac
            ;;
    esac
    # Check if it's just "." or "-"
    case "$1" in
        .|-) return 1 ;;
    esac
    return 0 # Looks like a float
}

# --- Argument Parsing and Validation ---

# Check argument count
if [ $# -lt 3 ] || [ $# -gt 4 ]; then
  echo
  echo "Error: Incorrect number of arguments."
  usage
fi

ZONE="$1"
EASTING="$2"
NORTHING="$3"
# Set API_URL to the fourth argument if provided, otherwise use the default
API_URL="${4:-$DEFAULT_API_URL}"

# Validate arguments format
if ! is_integer "$ZONE"; then
    echo "Error: Invalid format for UTM zone: '$ZONE'. Please provide an integer."
    exit 1
fi
if ! is_float "$EASTING"; then
    echo "Error: Invalid format for Easting: '$EASTING'. Please provide a number."
    exit 1
fi
if ! is_float "$NORTHING"; then
    echo "Error: Invalid format for Northing: '$NORTHING'. Please provide a number."
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
echo "Querying elevation for UTM point ..."
echo "------------------------------------------"
echo "UTM Zone     : $ZONE"
echo "Easting      : $EASTING"
echo "Northing     : $NORTHING"
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
REQUEST_ID="UTMPoint-$(date +%s)-$$"
REQUEST_TYPE="UTMPointRequest"

# Create JSON payload string using printf for better handling of variables.
# Note: POSIX printf float handling might vary slightly across implementations.
JSON_PAYLOAD=$(printf '{
  "Type": "%s",
  "ID": "%s",
  "Attributes": {
    "Zone": %d,
    "Easting": %s,
    "Northing": %s
  }
}' "$REQUEST_TYPE" "$REQUEST_ID" "$ZONE" "$EASTING" "$NORTHING")

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
# Using --json for curl version 7.82.0+ is an alternative: curl --json "$JSON_PAYLOAD" "$API_URL"
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
ZONE_RESP=$(jq -r '.Attributes.Zone // "N/A"' < "$TEMP_RESPONSE_FILE")
EASTING_RESP=$(jq -r '.Attributes.Easting // "N/A"' < "$TEMP_RESPONSE_FILE")
NORTHING_RESP=$(jq -r '.Attributes.Northing // "N/A"' < "$TEMP_RESPONSE_FILE")
ELEVATION=$(jq -r '.Attributes.Elevation // "N/A"' < "$TEMP_RESPONSE_FILE")
ACTUALITY=$(jq -r '.Attributes.Actuality // "N/A"' < "$TEMP_RESPONSE_FILE")
ATTRIBUTION=$(jq -r '.Attributes.Attribution // "N/A"' < "$TEMP_RESPONSE_FILE")
TILE_INDEX=$(jq -r '.Attributes.TileIndex // "N/A"' < "$TEMP_RESPONSE_FILE")

# Display the extracted information
echo
echo "Point Details ..."
echo "------------------------------------------"
echo "UTM Zone      : $ZONE_RESP"
echo "Easting       : $EASTING_RESP"
echo "Northing      : $NORTHING_RESP"
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
