package main

import "net/http"

/*
corsOptionsHandler handles CORS preflight (OPTIONS) requests.
*/
func corsOptionsHandler(writer http.ResponseWriter, _ *http.Request) {
	// set CORS headers for the preflight request
	writer.Header().Set("Access-Control-Allow-Origin", "*")

	// allowed methods for the actual request
	writer.Header().Set("Access-Control-Allow-Methods", "POST")

	// allowed headers for the actual request
	writer.Header().Set("Access-Control-Allow-Headers", "Content-Type")

	// caching time for results of preflight request in seconds (86400 seconds = 24 hours)
	writer.Header().Set("Access-Control-Max-Age", "86400")

	// respond with 200 OK status for the preflight request
	writer.WriteHeader(http.StatusOK)
}
