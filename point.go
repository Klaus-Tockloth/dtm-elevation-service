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
pointRequest handles 'point request' from client.
*/
func pointRequest(writer http.ResponseWriter, request *http.Request) {
	var pointResponse = PointResponse{Type: TypePointResponse, ID: "unknown"}
	pointResponse.Attributes.Elevation = -8888.0
	pointResponse.Attributes.IsError = true

	// statistics
	atomic.AddUint64(&PointRequests, 1)

	// limit overall request body size
	request.Body = http.MaxBytesReader(writer, request.Body, MaxPointRequestBodySize)

	// read request
	bodyData, err := io.ReadAll(request.Body)
	if err != nil {
		// check specifically for the error returned by MaxBytesReader
		var maxBytesErr *http.MaxBytesError
		if errors.As(err, &maxBytesErr) {
			slog.Warn("point request: request body too large", "limit", maxBytesErr.Limit, "ID", "unknown")
			pointResponse.Attributes.Error.Code = "1000"
			pointResponse.Attributes.Error.Title = "request body too large"
			pointResponse.Attributes.Error.Detail = fmt.Sprintf("request body exceeds limit of %d bytes", maxBytesErr.Limit)
			buildPointResponse(writer, http.StatusRequestEntityTooLarge, pointResponse)
		} else {
			// handle other read errors
			slog.Warn("point request: error reading request body", "error", err, "ID", "unknown")
			pointResponse.Attributes.Error.Code = "1020"
			pointResponse.Attributes.Error.Title = "error reading request body"
			pointResponse.Attributes.Error.Detail = err.Error()
			buildPointResponse(writer, http.StatusBadRequest, pointResponse)
		}
		return
	}

	// unmarshal request
	pointRequest := PointRequest{}
	err = json.Unmarshal(bodyData, &pointRequest)
	if err != nil {
		slog.Warn("point request: error unmarshaling request body", "error", err, "ID", "unknown")
		pointResponse.Attributes.Error.Code = "1040"
		pointResponse.Attributes.Error.Title = "error unmarshaling request body"
		pointResponse.Attributes.Error.Detail = err.Error()
		buildPointResponse(writer, http.StatusBadRequest, pointResponse)
		return
	}

	// copy request parameters into response
	pointResponse.ID = pointRequest.ID
	pointResponse.Attributes.Latitude = pointRequest.Attributes.Latitude
	pointResponse.Attributes.Longitude = pointRequest.Attributes.Longitude

	// verify request data
	err = verifyPointRequestData(request, pointRequest)
	if err != nil {
		slog.Warn("point request: error verifying request data", "error", err, "ID", pointRequest.ID)
		pointResponse.Attributes.Error.Code = "1060"
		pointResponse.Attributes.Error.Title = "error verifying request data"
		pointResponse.Attributes.Error.Detail = err.Error()
		buildPointResponse(writer, http.StatusBadRequest, pointResponse)
		return
	}

	// get elevation
	elevation, tile, err := getElevationForPoint(pointRequest.Attributes.Longitude, pointRequest.Attributes.Latitude)
	if err != nil {
		slog.Debug("point request: error getting elevation for point", "error", err, "ID", pointRequest.ID)
		pointResponse.Attributes.Error.Code = "1080"
		pointResponse.Attributes.Error.Title = "error getting elevation"
		pointResponse.Attributes.Error.Detail = err.Error()
		buildPointResponse(writer, http.StatusBadRequest, pointResponse)
		return
	}

	// get attribution for resource
	attribution := "unknown"
	origin := "unknown"
	resource, err := getElevationResource(tile.Source)
	if err != nil {
		slog.Error("point request: error getting elevation resource", "error", err, "source", tile.Source, "ID", pointRequest.ID)
	} else {
		attribution = resource.Attribution
		origin = resource.Code
	}

	// success response
	pointResponse.Attributes.Elevation = elevation
	pointResponse.Attributes.Actuality = tile.Actuality
	pointResponse.Attributes.Origin = origin
	pointResponse.Attributes.Attribution = attribution
	pointResponse.Attributes.TileIndex = tile.Index
	pointResponse.Attributes.IsError = false
	buildPointResponse(writer, http.StatusOK, pointResponse)
}

/*
verifyPointRequestData verifies 'point' request data.
It performs several checks on the request data to ensure its validity.
*/
func verifyPointRequestData(request *http.Request, pointRequest PointRequest) error {
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
	if pointRequest.Type != TypePointRequest {
		return fmt.Errorf("unexpected request Type [%v]", pointRequest.Type)
	}

	// verify ID
	if len(pointRequest.ID) > 1024 {
		return errors.New("ID must be 0-1024 characters long")
	}

	// verify Attributes.Latitude for Germany (Latitude: from 47.2701° N to 55.0586° N)
	if pointRequest.Attributes.Latitude > 55.3 || pointRequest.Attributes.Latitude < 47.0 {
		return errors.New("invalid latitude for Germany")
	}

	// verify Attributes.Longitude for Germany (Longitude: from  5.8663° E to 15.0419° E)
	if pointRequest.Attributes.Longitude > 15.3 || pointRequest.Attributes.Longitude < 5.5 {
		return errors.New("invalid longitude for Germany")
	}

	return nil
}

/*
buildPointResponse builds HTTP responses with specified status and body.
It sets the Content-Type and Content-Length headers before writing the response body.
This function is used to construct consistent HTTP responses throughout the application.
*/
func buildPointResponse(writer http.ResponseWriter, httpStatus int, pointResponse PointResponse) {
	// log limit length of body (we don't expect large bodies)
	maxBodyLength := 1024

	// CORS: allow requests from any origin
	writer.Header().Set("Access-Control-Allow-Origin", "*")
	// CORS: allowed methods
	writer.Header().Set("Access-Control-Allow-Methods", "POST")
	// CORS: allowed headers
	writer.Header().Set("Access-Control-Allow-Headers", "Content-Type")

	// marshal response
	body, err := json.MarshalIndent(pointResponse, "", "  ")
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
