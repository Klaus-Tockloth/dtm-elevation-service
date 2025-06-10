package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"sync/atomic"
)

/*
utmPointRequest handles 'UTM point request' from client.
*/
func utmPointRequest(writer http.ResponseWriter, request *http.Request) {
	var utmPointResponse = UTMPointResponse{Type: TypeUTMPointResponse, ID: "unknown"}
	utmPointResponse.Attributes.Elevation = -8888.0
	utmPointResponse.Attributes.IsError = true

	// statistics
	atomic.AddUint64(&UTMPointRequests, 1)

	// limit overall request body size
	request.Body = http.MaxBytesReader(writer, request.Body, MaxPointRequestBodySize)

	// read request
	bodyData, err := io.ReadAll(request.Body)
	if err != nil {
		// check specifically for the error returned by MaxBytesReader
		var maxBytesErr *http.MaxBytesError
		if errors.As(err, &maxBytesErr) {
			slog.Warn("utm point request: request body too large", "limit", maxBytesErr.Limit, "ID", "unknown")
			utmPointResponse.Attributes.Error.Code = "3000"
			utmPointResponse.Attributes.Error.Title = "request body too large"
			utmPointResponse.Attributes.Error.Detail = fmt.Sprintf("request body exceeds limit of %d bytes", maxBytesErr.Limit)
			buildUTMPointResponse(writer, http.StatusRequestEntityTooLarge, utmPointResponse)
		} else {
			// handle other read errors
			slog.Warn("utm point request: error reading request body", "error", err, "ID", "unknown")
			utmPointResponse.Attributes.Error.Code = "3020"
			utmPointResponse.Attributes.Error.Title = "error reading request body"
			utmPointResponse.Attributes.Error.Detail = err.Error()
			buildUTMPointResponse(writer, http.StatusBadRequest, utmPointResponse)
		}
		return
	}

	// unmarshal request
	utmPointRequest := UTMPointRequest{}
	err = json.Unmarshal(bodyData, &utmPointRequest)
	if err != nil {
		slog.Warn("utm point request: error unmarshaling request body", "error", err, "ID", "unknown")
		utmPointResponse.Attributes.Error.Code = "3040"
		utmPointResponse.Attributes.Error.Title = "error unmarshaling request body"
		utmPointResponse.Attributes.Error.Detail = err.Error()
		buildUTMPointResponse(writer, http.StatusBadRequest, utmPointResponse)
		return
	}

	// copy request parameters into response
	utmPointResponse.ID = utmPointRequest.ID
	utmPointResponse.Attributes.Zone = utmPointRequest.Attributes.Zone
	utmPointResponse.Attributes.Easting = utmPointRequest.Attributes.Easting
	utmPointResponse.Attributes.Northing = utmPointRequest.Attributes.Northing

	// verify request data
	err = verifyUTMPointRequestData(request, utmPointRequest)
	if err != nil {
		slog.Warn("utm point request: error verifying request data", "error", err, "ID", utmPointRequest.ID)
		utmPointResponse.Attributes.Error.Code = "3060"
		utmPointResponse.Attributes.Error.Title = "error verifying request data"
		utmPointResponse.Attributes.Error.Detail = err.Error()
		buildUTMPointResponse(writer, http.StatusBadRequest, utmPointResponse)
		return
	}

	// get elevation
	elevation, tile, err := getElevationForUTMPoint(utmPointRequest.Attributes.Zone, utmPointRequest.Attributes.Easting, utmPointRequest.Attributes.Northing)
	if err != nil {
		slog.Debug("utm point request: error getting elevation for utm point", "error", err, "ID", utmPointRequest.ID)
		utmPointResponse.Attributes.Error.Code = "3080"
		utmPointResponse.Attributes.Error.Title = "error getting elevation"
		utmPointResponse.Attributes.Error.Detail = err.Error()
		buildUTMPointResponse(writer, http.StatusBadRequest, utmPointResponse)
		return
	}

	// get attribution for resource
	attribution := "unknown"
	origin := "unknown"
	resource, err := getElevationResource(tile.Source)
	if err != nil {
		slog.Error("point request: error getting elevation resource", "error", err, "source", tile.Source, "ID", utmPointRequest.ID)
	} else {
		attribution = resource.Attribution
		origin = resource.Code
	}

	// success response
	utmPointResponse.Attributes.Elevation = elevation
	utmPointResponse.Attributes.Actuality = tile.Actuality
	utmPointResponse.Attributes.Origin = origin
	utmPointResponse.Attributes.Attribution = attribution
	utmPointResponse.Attributes.TileIndex = tile.Index
	utmPointResponse.Attributes.IsError = false
	buildUTMPointResponse(writer, http.StatusOK, utmPointResponse)
}

/*
verifyUTMPointRequestData verifies 'utm point' request data.
It performs several checks on the request data to ensure its validity.
*/
func verifyUTMPointRequestData(request *http.Request, utmPointRequest UTMPointRequest) error {
	// verify HTTP header
	contentType := request.Header.Get("Content-Type")
	isContentTypeValid := true
	switch {
	case strings.HasPrefix(strings.ToLower(contentType), "application/json"):
		// potentially check charset=utf-8 specifically if required
	default:
		isContentTypeValid = false
	}
	if !isContentTypeValid {
		return fmt.Errorf("unexpected or missing HTTP header field Content-Type, value = [%s], expected 'application/json'", contentType)
	}

	// verify HTTP header
	accept := request.Header.Get("Accept")
	isAcceptValid := true
	switch {
	case strings.HasPrefix(strings.ToLower(accept), "application/json"):
	default:
		isAcceptValid = false
	}
	if !isAcceptValid {
		return fmt.Errorf("unexpected or missing HTTP header field Accept, value = [%s], expected 'application/json'", accept)
	}

	// verify Type
	if utmPointRequest.Type != TypeUTMPointRequest {
		return fmt.Errorf("unexpected request Type [%v]", utmPointRequest.Type)
	}

	// verify ID
	if len(utmPointRequest.ID) > 1024 {
		return errors.New("ID must be 0-1024 characters long")
	}

	// verify Attributes.Zone for Germany (Zone: 32 or 33)
	if utmPointRequest.Attributes.Zone < 32 || utmPointRequest.Attributes.Zone > 33 {
		return errors.New("invalid zone for Germany")
	}

	return nil
}

/*
buildUTMPointResponse builds HTTP responses with specified status and body.
It sets the Content-Type and Content-Length headers before writing the response body.
This function is used to construct consistent HTTP responses throughout the application.
*/
func buildUTMPointResponse(writer http.ResponseWriter, httpStatus int, utmPointResponse UTMPointResponse) {
	// log limit length of body (we don't expect large bodies)
	maxBodyLength := 1024

	// CORS: allow requests from any origin
	writer.Header().Set("Access-Control-Allow-Origin", "*")
	// CORS: allowed methods
	writer.Header().Set("Access-Control-Allow-Methods", "POST")
	// CORS: allowed headers
	writer.Header().Set("Access-Control-Allow-Headers", "Content-Type")

	// marshal response
	body, err := json.MarshalIndent(utmPointResponse, "", "  ")
	if err != nil {
		slog.Error("error marshaling point response", "error", err, "body length", len(body),
			fmt.Sprintf("body (limited to first %d bytes)", maxBodyLength), body[:maxBodyLength])

		http.Error(writer, "Internal Server Error", http.StatusInternalServerError)
		return
	}

	// send response
	writer.Header().Set("Content-Type", JSONAPIMediaType)
	writer.WriteHeader(httpStatus)
	_, err = writer.Write(body)
	if err != nil {
		slog.Error("error writing HTTP response body", "error", err, "body length", len(body),
			fmt.Sprintf("body (limited to first %d bytes)", maxBodyLength), body[:maxBodyLength])
	}
}
