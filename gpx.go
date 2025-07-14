package main

import (
	"encoding/base64"
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"sync/atomic"
	"time"

	"github.com/tkrajina/gpxgo/gpx"
)

/*
gpxRequest handles 'gpx request' from client.
*/
func gpxRequest(writer http.ResponseWriter, request *http.Request) {
	var gpxResponse = GPXResponse{Type: TypeGPXResponse, ID: "unknown"}
	gpxResponse.Attributes.IsError = true

	// statistics
	atomic.AddUint64(&GPXRequests, 1)

	// limit overall request body size
	request.Body = http.MaxBytesReader(writer, request.Body, MaxGpxRequestBodySize)

	// read request
	bodyData, err := io.ReadAll(request.Body)
	if err != nil {
		// check specifically for the error returned by MaxBytesReader
		var maxBytesErr *http.MaxBytesError
		if errors.As(err, &maxBytesErr) {
			slog.Warn("gpx request: request body too large", "limit", maxBytesErr.Limit, "ID", "unknown")
			gpxResponse.Attributes.Error.Code = "2000"
			gpxResponse.Attributes.Error.Title = "request body too large"
			gpxResponse.Attributes.Error.Detail = fmt.Sprintf("request body exceeds limit of %d bytes", maxBytesErr.Limit)
			buildGpxResponse(writer, http.StatusRequestEntityTooLarge, gpxResponse)
		} else {
			// handle other read errors
			slog.Warn("gpx request: error reading request body", "error", err, "ID", "unknown")
			gpxResponse.Attributes.Error.Code = "2020"
			gpxResponse.Attributes.Error.Title = "error reading request body"
			gpxResponse.Attributes.Error.Detail = err.Error()
			buildGpxResponse(writer, http.StatusBadRequest, gpxResponse)
		}
		return
	}

	// unmarshal request
	gpxRequest := GPXRequest{}
	err = json.Unmarshal(bodyData, &gpxRequest)
	if err != nil {
		slog.Warn("gpx request: error unmarshaling request body", "error", err, "ID", "unknown")
		gpxResponse.Attributes.Error.Code = "2040"
		gpxResponse.Attributes.Error.Title = "error unmarshaling request body"
		gpxResponse.Attributes.Error.Detail = err.Error()
		buildGpxResponse(writer, http.StatusBadRequest, gpxResponse)
		return
	}

	// copy request parameters into response
	gpxResponse.ID = gpxRequest.ID

	// verify request data
	err = verifyGpxRequestData(request, gpxRequest)
	if err != nil {
		slog.Warn("gpx request: error verifying request data", "error", err, "ID", gpxRequest.ID)
		gpxResponse.Attributes.Error.Code = "2060"
		gpxResponse.Attributes.Error.Title = "error verifying request data"
		gpxResponse.Attributes.Error.Detail = err.Error()
		buildGpxResponse(writer, http.StatusBadRequest, gpxResponse)
		return
	}

	// parse GPX data
	gpxBytes, _ := base64.StdEncoding.DecodeString(gpxRequest.Attributes.GPXData) // error already checked in verifyGpxRequestData()
	gpxData, err := gpx.ParseBytes(gpxBytes)
	if err != nil {
		slog.Warn("gpx request: error parsing GPX data", "error", err, "ID", gpxRequest.ID)
		gpxResponse.Attributes.Error.Code = "2080"
		gpxResponse.Attributes.Error.Title = "error parsing GPX data"
		gpxResponse.Attributes.Error.Detail = err.Error()
		buildGpxResponse(writer, http.StatusBadRequest, gpxResponse)
		return
	}

	// add elevation to all points (way, route, track)
	start := time.Now()
	processedGpxData, usedElevationSources, gpxPoints, dgmPoints, err := addElevationToGPX(gpxData, gpxRequest.ID) // pass ID for logging
	if err != nil {
		slog.Error("gpx request: critical error during elevation processing", "error", err, "ID", gpxRequest.ID)
		gpxResponse.Attributes.Error.Code = "2100"
		gpxResponse.Attributes.Error.Title = "critical error adding elevation to GPX"
		gpxResponse.Attributes.Error.Detail = err.Error()
		buildGpxResponse(writer, http.StatusBadRequest, gpxResponse)
		return
	}
	end := time.Now()
	elapsed := end.Sub(start)
	slog.Info("duration of gpx processing", "elapsed (ms)", int64(elapsed/time.Millisecond))

	// add description
	description := "Die Höhenangaben (ele) basieren auf DGM-Daten mit hoher Genauigkeit."
	if processedGpxData.Description == "" {
		processedGpxData.Description = description
	} else {
		processedGpxData.Description += " - " + description
	}

	// add creator
	creator := "Höhenangaben von hoehendaten.de"
	if processedGpxData.Creator == "" {
		processedGpxData.Creator = creator
	} else {
		processedGpxData.Creator += " - " + creator
	}

	// collect unique source attributions from the used sources
	uniqueAttributions := make(map[string]string)
	for _, source := range usedElevationSources {
		if source.Attribution != "" {
			// e.g., "DE-NI: © GeoBasis-DE / LGLN (2025), cc-by/4.0"
			uniqueAttributions[source.Code] = fmt.Sprintf("%s: %s", source.Code, source.Attribution)
		}
	}

	// convert map to slice
	var attributions []string
	for _, attribution := range uniqueAttributions {
		attributions = append(attributions, attribution)
	}

	// add attributions to GPX header
	if processedGpxData.Copyright == "" {
		processedGpxData.Copyright = strings.Join(attributions, ", ")
	} else {
		processedGpxData.Copyright += " " + strings.Join(attributions, ", ")
	}

	// convert modified GPX data to XML
	xmlBytes, err := processedGpxData.ToXml(gpx.ToXmlParams{Indent: true})
	if err != nil {
		slog.Error("gpx request: error creating GPX track", "error", err, "ID", gpxRequest.ID)
		gpxResponse.Attributes.Error.Code = "2120"
		gpxResponse.Attributes.Error.Title = "error creating GPX track"
		gpxResponse.Attributes.Error.Detail = err.Error()
		buildGpxResponse(writer, http.StatusInternalServerError, gpxResponse)
		return
	}

	// statistics
	atomic.AddUint64(&GPXPoints, uint64(gpxPoints))
	atomic.AddUint64(&DGMPoints, uint64(dgmPoints))

	// successful response
	gpxResponse.Attributes.GPXData = base64.StdEncoding.EncodeToString(xmlBytes)
	gpxResponse.Attributes.GPXPoints = gpxPoints
	gpxResponse.Attributes.DGMPoints = dgmPoints
	gpxResponse.Attributes.Attributions = attributions
	gpxResponse.Attributes.IsError = false
	buildGpxResponse(writer, http.StatusOK, gpxResponse)
}

/*
verifyGpxRequestData verifies 'gpx' request data.
It performs several checks on the request data to ensure its validity.
*/
func verifyGpxRequestData(request *http.Request, gpxRequest GPXRequest) error {
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
	if gpxRequest.Type != TypeGPXRequest {
		return fmt.Errorf("unexpected request Type [%v]", gpxRequest.Type)
	}

	// verify ID
	if len(gpxRequest.ID) > 1024 {
		return errors.New("ID must be 0-1024 characters long")
	}

	// minimal struct to check the root element of the XML
	type gpxRoot struct {
		XMLName xml.Name
	}

	// verify GPX data
	if gpxRequest.Attributes.GPXData == "" {
		return errors.New("GPXData must not be empty")
	}
	gpxXMLBytes, err := base64.StdEncoding.DecodeString(gpxRequest.Attributes.GPXData)
	if err != nil {
		return errors.New("GPXData is not valid base64")
	}
	var root gpxRoot
	err = xml.Unmarshal(gpxXMLBytes, &root)
	if err != nil {
		return fmt.Errorf("GPXData is not valid XML: %w", err)
	}
	if root.XMLName.Local != "gpx" {
		return errors.New("GPXData does not contain expected 'gpx' root element")
	}

	return nil
}

/*
buildGpxResponse builds HTTP responses with specified status and body.
It sets the Content-Type and Content-Length headers before writing the response body.
This function is used to construct consistent HTTP responses throughout the application.
*/
func buildGpxResponse(writer http.ResponseWriter, httpStatus int, gpxResponse GPXResponse) {
	// log limit length of body (e.g., the GPXData object as part of the body can be very large)
	maxBodyLength := 1024

	// CORS: allow requests from any origin
	writer.Header().Set("Access-Control-Allow-Origin", "*")
	// CORS: allowed methods
	writer.Header().Set("Access-Control-Allow-Methods", "POST")
	// CORS: allowed headers
	writer.Header().Set("Access-Control-Allow-Headers", "Content-Type")

	// marshal response
	body, err := json.MarshalIndent(gpxResponse, "", "  ")
	if err != nil {
		slog.Error("error marshaling gpx response", "error", err, "body length", len(body),
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

/*
addElevationToGPX adds elevation to all GPX points using actual DTM data.
It iterates through waypoints, route points, and track points, calculates
their elevation using the available GeoTIFF tiles, and updates the GPX data.
It collects metadata about the elevation sources used.
If an error occurs for a specific point, it's logged, and that point is skipped.
Note: A single tile caching adds complexity, but can improve the processing of
large GPX files significantly.
*/
func addElevationToGPX(gpxData *gpx.GPX, requestID string) (*gpx.GPX, []ElevationSource, int, int, error) {
	// map to collect unique elevation sources based on their code (e.g., "DE-NW")
	usedSourcesMap := make(map[string]ElevationSource)

	// statistics
	gpxPoints := 0
	dgmPoints := 0

	processPoint := func(point *gpx.GPXPoint, pointType string, index int) {
		gpxPoints++
		elevation, tile, err := getElevationForPoint(point.Longitude, point.Latitude)
		if err != nil {
			// log error for the specific point but continue processing others
			slog.Warn("failed to get elevation for GPX point", "requestID", requestID, "pointType", pointType,
				"index", index, "longitude", point.Longitude, "latitude", point.Latitude, "error", err)
			return
		}

		// set the elevation
		point.Elevation.SetValue(elevation)
		dgmPoints++

		// describe source and actuality (e.g., "Elevation: DE-NW, 2021-06")
		if point.Description == "" {
			point.Description = fmt.Sprintf("ele: %s, %s", tile.Source, tile.Actuality)
		} else {
			point.Description += fmt.Sprintf(" ele: %s, %s", tile.Source, tile.Actuality)
		}

		// get and store the source information if not already stored
		_, exists := usedSourcesMap[tile.Source]
		if !exists {
			resource, err := getElevationResource(tile.Source)
			if err != nil {
				slog.Warn("failed to get elevation resource details", "requestID", requestID, "sourceCode", tile.Source, "error", err)
				// skip adding if details can't be fetched
			} else {
				usedSourcesMap[tile.Source] = resource
			}
		}
	}

	// iterate over all waypoints
	for i := range gpxData.Waypoints {
		processPoint(&gpxData.Waypoints[i], "waypoint", i)
	}

	// iterate over all routes
	for i := range gpxData.Routes {
		for j := range gpxData.Routes[i].Points {
			processPoint(&gpxData.Routes[i].Points[j], fmt.Sprintf("route %d point", i), j)
		}
	}

	// iterate over all tracks and segments
	for i := range gpxData.Tracks {
		for j := range gpxData.Tracks[i].Segments {
			for k := range gpxData.Tracks[i].Segments[j].Points {
				processPoint(&gpxData.Tracks[i].Segments[j].Points[k], fmt.Sprintf("track %d segment %d point", i, j), k)
			}
		}
	}

	// convert the map of unique sources to a slice
	finalElevationSources := make([]ElevationSource, 0, len(usedSourcesMap))
	for _, source := range usedSourcesMap {
		finalElevationSources = append(finalElevationSources, source)
	}

	return gpxData, finalElevationSources, gpxPoints, dgmPoints, nil
}
