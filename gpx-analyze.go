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

	"github.com/tkrajina/gpxgo/gpx"
)

/*
gpxAnalyzeRequest handles 'gpx analyze request' from client.
*/
func gpxAnalyzeRequest(writer http.ResponseWriter, request *http.Request) {
	var gpxAnalyzeResponse = GPXAnalyzeResponse{Type: TypeGPXAnalyzeResponse, ID: "unknown"}
	gpxAnalyzeResponse.Attributes.IsError = true

	// statistics
	atomic.AddUint64(&GPXAnalyzeRequests, 1)

	// limit overall request body size
	request.Body = http.MaxBytesReader(writer, request.Body, MaxGpxAnalyzeRequestBodySize)

	// read request
	bodyData, err := io.ReadAll(request.Body)
	if err != nil {
		// check specifically for the error returned by MaxBytesReader
		var maxBytesErr *http.MaxBytesError
		if errors.As(err, &maxBytesErr) {
			slog.Warn("gpx analyze request: request body too large", "limit", maxBytesErr.Limit, "ID", "unknown")
			gpxAnalyzeResponse.Attributes.Error.Code = "8000"
			gpxAnalyzeResponse.Attributes.Error.Title = "request body too large"
			gpxAnalyzeResponse.Attributes.Error.Detail = fmt.Sprintf("request body exceeds limit of %d bytes", maxBytesErr.Limit)
			buildGpxAnalyzeResponse(writer, http.StatusRequestEntityTooLarge, gpxAnalyzeResponse)
		} else {
			// handle other read errors
			slog.Warn("gpx analyze request: error reading request body", "error", err, "ID", "unknown")
			gpxAnalyzeResponse.Attributes.Error.Code = "8020"
			gpxAnalyzeResponse.Attributes.Error.Title = "error reading request body"
			gpxAnalyzeResponse.Attributes.Error.Detail = err.Error()
			buildGpxAnalyzeResponse(writer, http.StatusBadRequest, gpxAnalyzeResponse)
		}
		return
	}

	// unmarshal request
	gpxAnalyzeRequest := GPXAnalyzeRequest{}
	err = json.Unmarshal(bodyData, &gpxAnalyzeRequest)
	if err != nil {
		slog.Warn("gpx analyze request: error unmarshaling request body", "error", err, "ID", "unknown")
		gpxAnalyzeResponse.Attributes.Error.Code = "8040"
		gpxAnalyzeResponse.Attributes.Error.Title = "error unmarshaling request body"
		gpxAnalyzeResponse.Attributes.Error.Detail = err.Error()
		buildGpxAnalyzeResponse(writer, http.StatusBadRequest, gpxAnalyzeResponse)
		return
	}

	// copy request parameters into response
	gpxAnalyzeResponse.ID = gpxAnalyzeRequest.ID

	// verify request data
	err = verifyGpxAnalyzeRequestData(request, gpxAnalyzeRequest)
	if err != nil {
		slog.Warn("gpx analyze request: error verifying request data", "error", err, "ID", gpxAnalyzeRequest.ID)
		gpxAnalyzeResponse.Attributes.Error.Code = "8060"
		gpxAnalyzeResponse.Attributes.Error.Title = "error verifying request data"
		gpxAnalyzeResponse.Attributes.Error.Detail = err.Error()
		buildGpxAnalyzeResponse(writer, http.StatusBadRequest, gpxAnalyzeResponse)
		return
	}

	// parse GPX data
	gpxBytes, _ := base64.StdEncoding.DecodeString(gpxAnalyzeRequest.Attributes.GPXData) // error already checked in verifyGpxAnalyzeRequestData()
	gpxData, err := gpx.ParseBytes(gpxBytes)
	if err != nil {
		slog.Warn("gpx analyze request: error parsing GPX data", "error", err, "ID", gpxAnalyzeRequest.ID)
		gpxAnalyzeResponse.Attributes.Error.Code = "8080"
		gpxAnalyzeResponse.Attributes.Error.Title = "error parsing GPX data"
		gpxAnalyzeResponse.Attributes.Error.Detail = err.Error()
		buildGpxAnalyzeResponse(writer, http.StatusBadRequest, gpxAnalyzeResponse)
		return
	}

	gpxAnalyzeResult, err := analyzeGpxData(gpxData)
	if err != nil {
		slog.Warn("gpx analyze request: error analyzing GPX data", "error", err, "ID", gpxAnalyzeRequest.ID)
		gpxAnalyzeResponse.Attributes.Error.Code = "8100"
		gpxAnalyzeResponse.Attributes.Error.Title = "error analyzing GPX data"
		gpxAnalyzeResponse.Attributes.Error.Detail = err.Error()
		buildGpxAnalyzeResponse(writer, http.StatusBadRequest, gpxAnalyzeResponse)
		return
	}

	// successful response
	gpxAnalyzeResponse.Attributes.GPXData = base64.StdEncoding.EncodeToString(gpxBytes)
	gpxAnalyzeResponse.Attributes.GpxAnalyzeResult = *gpxAnalyzeResult
	gpxAnalyzeResponse.Attributes.IsError = false
	buildGpxAnalyzeResponse(writer, http.StatusOK, gpxAnalyzeResponse)
}

/*
verifyGpxAnalyzeRequestData verifies 'gpx' request data.
It performs several checks on the request data to ensure its validity.
*/
func verifyGpxAnalyzeRequestData(request *http.Request, gpxAnalyzeRequest GPXAnalyzeRequest) error {
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
	if gpxAnalyzeRequest.Type != TypeGPXAnalyzeRequest {
		return fmt.Errorf("unexpected request Type [%v]", gpxAnalyzeRequest.Type)
	}

	// verify ID
	if len(gpxAnalyzeRequest.ID) > 1024 {
		return errors.New("ID must be 0-1024 characters long")
	}

	// minimal struct to check the root element of the XML
	type gpxRoot struct {
		XMLName xml.Name
	}

	// verify GPX data
	if gpxAnalyzeRequest.Attributes.GPXData == "" {
		return errors.New("GPXData must not be empty")
	}
	gpxXMLBytes, err := base64.StdEncoding.DecodeString(gpxAnalyzeRequest.Attributes.GPXData)
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
buildGpxAnalyzeResponse builds HTTP responses with specified status and body.
It sets the Content-Type and Content-Length headers before writing the response body.
This function is used to construct consistent HTTP responses throughout the application.
*/
func buildGpxAnalyzeResponse(writer http.ResponseWriter, httpStatus int, gpxAnalyzeResponse GPXAnalyzeResponse) {
	// log limit length of body (e.g., the GPXData object as part of the body can be very large)
	maxBodyLength := 1024

	// CORS: allow requests from any origin
	writer.Header().Set("Access-Control-Allow-Origin", "*")
	// CORS: allowed methods
	writer.Header().Set("Access-Control-Allow-Methods", "POST")
	// CORS: allowed headers
	writer.Header().Set("Access-Control-Allow-Headers", "Content-Type")

	// marshal response
	body, err := json.MarshalIndent(gpxAnalyzeResponse, "", "  ")
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
analyzeGpxData analyzes GPX (file) data, calculates statistics, and returns them in a GpxAnlyzeResult structure.
*/
func analyzeGpxData(gpxData *gpx.GPX) (*GpxAnalyzeResult, error) {
	result := &GpxAnalyzeResult{
		Version:     gpxData.Version,
		Name:        gpxData.Name,
		Description: gpxData.Description,
		Creator:     gpxData.Creator,
		Time:        gpxData.Time,
		TotalPoints: gpxData.GetTrackPointsNo(),
		Tracks:      []GpxAnalyzeTrackResult{},
	}

	// process track data for all segments
	for _, track := range gpxData.Tracks {
		trackResult := GpxAnalyzeTrackResult{
			Name:        track.Name,
			Comment:     track.Comment,
			Description: track.Description,
			Source:      track.Source,
			Type:        track.Type,
			Segments:    []GpxAnalyzeSegmentResult{},
		}

		for _, segment := range track.Segments {
			// calculate unfiltered data
			gpxUphillUnfiltered, gpxDownhillUnfiltered := calculateUphillDownhill(segment.Points)

			timeBounds := segment.TimeBounds()
			movingData := segment.MovingData()
			gpxBounds := segment.Bounds()

			// calculate weighted moving average data
			upDownWMA := segment.UphillDownhill()

			// calculate detailed point statistics
			pointDetails := calculatePointDetails(segment.Points)

			// populate segment result structure
			segResult := GpxAnalyzeSegmentResult{
				// General
				StartTime: timeBounds.StartTime,
				EndTime:   timeBounds.EndTime,
				Duration:  segment.Duration(),
				Points:    segment.GetTrackPointsNo(),
				Length2D:  segment.Length2D(),
				Length3D:  segment.Length3D(),
				// Moving
				MovingTime:      movingData.MovingTime,
				StoppedTime:     movingData.StoppedTime,
				MovingDistance:  movingData.MovingDistance,
				StoppedDistance: movingData.StoppedDistance,
				// Bounding Box
				MaxLatitude:  gpxBounds.MaxLatitude,
				MaxLongitude: gpxBounds.MaxLongitude,
				MinLatitude:  gpxBounds.MinLatitude,
				MinLongitude: gpxBounds.MinLongitude,
				// Elevation
				UphillWMA:          upDownWMA.Uphill,
				DownhillWMA:        upDownWMA.Downhill,
				UphillUnfiltered:   gpxUphillUnfiltered,
				DownhillUnfiltered: gpxDownhillUnfiltered,
				// Details
				PointDetails: pointDetails,
			}
			trackResult.Segments = append(trackResult.Segments, segResult)
		}
		result.Tracks = append(result.Tracks, trackResult)
	}
	return result, nil
}

/*
calculatePointDetails calculates detailed statistics for each point in a segment.
*/
func calculatePointDetails(points []gpx.GPXPoint) []GpxAnalyzePointDetail {
	if len(points) == 0 {
		return nil
	}

	details := make([]GpxAnalyzePointDetail, len(points))
	uphill := 0.0
	downhill := 0.0

	// Handle the first point
	details[0] = GpxAnalyzePointDetail{
		Timestamp:          points[0].Timestamp,
		TimeDifference:     0,
		Latitude:           points[0].Latitude,
		Longitude:          points[0].Longitude,
		Distance:           0,
		Elevation:          points[0].Elevation.Value(),
		CumulativeUphill:   0,
		CumulativeDownhill: 0,
	}

	for i := 1; i < len(points); i++ {
		previousPoint := points[i-1]
		currentPoint := points[i]

		timeDifferenceInSeconds := int64(currentPoint.Timestamp.Sub(previousPoint.Timestamp).Seconds())
		distance := currentPoint.Distance2D(&previousPoint)

		elevationDiff := currentPoint.Elevation.Value() - previousPoint.Elevation.Value()
		if elevationDiff > 0 {
			uphill += elevationDiff
		} else {
			downhill -= elevationDiff // downhill is positive
		}

		details[i] = GpxAnalyzePointDetail{
			Timestamp:          currentPoint.Timestamp,
			TimeDifference:     timeDifferenceInSeconds,
			Latitude:           currentPoint.Latitude,
			Longitude:          currentPoint.Longitude,
			Distance:           distance,
			Elevation:          currentPoint.Elevation.Value(),
			CumulativeUphill:   uphill,
			CumulativeDownhill: downhill,
		}
	}
	return details
}

/*
calculateUphillDownhill calculates the total ascent and descent from a slice of GPX points.
*/
func calculateUphillDownhill(points []gpx.GPXPoint) (uphill, downhill float64) {
	for i := 1; i < len(points); i++ {
		previousElevation := points[i-1].Elevation.Value()
		currentElevation := points[i].Elevation.Value()

		if currentElevation > previousElevation {
			uphill += currentElevation - previousElevation
		} else {
			downhill += previousElevation - currentElevation
		}
	}
	return uphill, downhill
}
