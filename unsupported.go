package main

import (
	"fmt"
	"log/slog"
	"net/http"
)

/*
unsupportedRequest handles 'unsupported' requests from clients.
It sends a "400 Bad Request" error message for unexpected HTTP requests.
The function logs a warning message and writes an error message to the response.
*/
func unsupportedRequest(writer http.ResponseWriter, _ *http.Request) {
	// prepare response
	writer.Header().Set("Content-Type", TextPlainMediaType)
	writer.WriteHeader(http.StatusBadRequest)
	errorMessage := "unsupported http request (e.g. route or method)"
	slog.Warn(errorMessage)
	fmt.Fprint(writer, errorMessage)
}
