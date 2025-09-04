package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math"
	"net/http"
	"strings"
	"sync/atomic"
)

/*
elevationprofileRequest handles 'elevationprofile request' from client. It accepts start and end points
in either UTM or Lon/Lat coordinates and calculates an elevation profile between them.
*/
func elevationprofileRequest(writer http.ResponseWriter, request *http.Request) {
	var profileResponse = ElevationProfileResponse{Type: TypeElevationProfileResponse, ID: "unknown"}
	profileResponse.Attributes.IsError = true

	// statistics
	atomic.AddUint64(&ElevationProfileRequests, 1)

	// limit overall request body size
	request.Body = http.MaxBytesReader(writer, request.Body, MaxElevationProfileRequestBodySize)

	// read request
	bodyData, err := io.ReadAll(request.Body)
	if err != nil {
		var maxBytesErr *http.MaxBytesError
		if errors.As(err, &maxBytesErr) {
			slog.Warn("elevationprofile request: request body too large", "limit", maxBytesErr.Limit, "ID", "unknown")
			profileResponse.Attributes.Error.Code = "14000"
			profileResponse.Attributes.Error.Title = "request body too large"
			profileResponse.Attributes.Error.Detail = fmt.Sprintf("request body exceeds limit of %d bytes", maxBytesErr.Limit)
			buildElevationProfileResponse(writer, http.StatusRequestEntityTooLarge, profileResponse)
		} else {
			slog.Warn("elevationprofile request: error reading request body", "error", err, "ID", "unknown")
			profileResponse.Attributes.Error.Code = "14020"
			profileResponse.Attributes.Error.Title = "error reading request body"
			profileResponse.Attributes.Error.Detail = err.Error()
			buildElevationProfileResponse(writer, http.StatusBadRequest, profileResponse)
		}
		return
	}

	// unmarshal request
	profileRequest := ElevationProfileRequest{}
	err = json.Unmarshal(bodyData, &profileRequest)
	if err != nil {
		slog.Warn("elevationprofile request: error unmarshaling request body", "error", err, "ID", "unknown")
		profileResponse.Attributes.Error.Code = "14040"
		profileResponse.Attributes.Error.Title = "error unmarshaling request body"
		profileResponse.Attributes.Error.Detail = err.Error()
		buildElevationProfileResponse(writer, http.StatusBadRequest, profileResponse)
		return
	}

	// copy request parameters into response
	profileResponse.ID = profileRequest.ID
	profileResponse.Attributes.PointA = profileRequest.Attributes.PointA
	profileResponse.Attributes.PointB = profileRequest.Attributes.PointB
	profileResponse.Attributes.MaxTotalProfilePoints = profileRequest.Attributes.MaxTotalProfilePoints
	profileResponse.Attributes.MinStepSize = profileRequest.Attributes.MinStepSize

	// verify request data
	err = verifyElevationProfileRequestData(request, profileRequest)
	if err != nil {
		slog.Warn("elevationprofile request: error verifying request data", "error", err, "ID", profileRequest.ID)
		profileResponse.Attributes.Error.Code = "14060"
		profileResponse.Attributes.Error.Title = "error verifying request data"
		profileResponse.Attributes.Error.Detail = err.Error()
		buildElevationProfileResponse(writer, http.StatusBadRequest, profileResponse)
		return
	}

	// elevation profile calculation
	profile, usedSources, err := calculateElevationProfile(profileRequest.Attributes.PointA, profileRequest.Attributes.PointB, profileRequest.Attributes.MaxTotalProfilePoints, profileRequest.Attributes.MinStepSize)
	if err != nil {
		slog.Error("elevationprofile request: error calculating profile", "error", err, "ID", profileRequest.ID)
		profileResponse.Attributes.Error.Code = "14080"
		profileResponse.Attributes.Error.Title = "error calculating elevation profile"
		profileResponse.Attributes.Error.Detail = err.Error()
		buildElevationProfileResponse(writer, http.StatusInternalServerError, profileResponse)
		return
	}

	// collect unique source attributions
	uniqueAttributions := make(map[string]string)
	for _, source := range usedSources {
		if source.Attribution != "" {
			uniqueAttributions[source.Code] = fmt.Sprintf("%s: %s", source.Code, source.Attribution)
		}
	}
	var attributions []string
	for _, attr := range uniqueAttributions {
		attributions = append(attributions, attr)
	}

	// successful response
	profileResponse.Attributes.Profile = profile
	profileResponse.Attributes.Attributions = attributions
	profileResponse.Attributes.IsError = false
	buildElevationProfileResponse(writer, http.StatusOK, profileResponse)
}

/*
calculateElevationProfile calculates the elevation profile between two points. The input points
can be in either UTM or Lon/Lat. The calculation is performed in a common UTM space.
*/
func calculateElevationProfile(pointA, pointB PointDefinition, maxTotalProfilePoints int, minStepSize float64) ([]ProfilePoint, []ElevationSource, error) {
	var startUTM, endUTM PointDefinition
	var sourceZone int

	isUTMRequest := pointA.Zone != 0

	if isUTMRequest {
		// both points are already in UTM and verified to be in the same zone
		startUTM = pointA
		endUTM = pointB
		sourceZone = pointA.Zone
	} else {
		// points are in Lon/Lat, need to convert to a common UTM zone for calculation
		// 1. determine UTM for start point A
		_, zone, eastingA, northingA, errA := getTileUTM(pointA.Longitude, pointA.Latitude)
		if errA != nil {
			return nil, nil, fmt.Errorf("could not determine UTM coordinates for PointA: %w", errA)
		}
		sourceZone = zone
		startUTM = PointDefinition{Zone: zone, Easting: eastingA, Northing: northingA}

		// 2. transform end point B into the same UTM zone
		targetEPSG := 25800 + zone
		eastingB, northingB, errB := transformLonLatToUTM(pointB.Longitude, pointB.Latitude, targetEPSG)
		if errB != nil {
			return nil, nil, fmt.Errorf("could not transform PointB to UTM zone %d: %w", zone, errB)
		}
		endUTM = PointDefinition{Zone: zone, Easting: eastingB, Northing: northingB}
	}

	// perform the profile calculation in UTM space
	deltaEasting := endUTM.Easting - startUTM.Easting
	deltaNorthing := endUTM.Northing - startUTM.Northing
	distance := math.Sqrt(deltaEasting*deltaEasting + deltaNorthing*deltaNorthing)

	if distance == 0 {
		return nil, nil, errors.New("start and end points are identical")
	}

	var stepSize float64
	steps := 0
	if maxTotalProfilePoints <= 1 {
		maxTotalProfilePoints = 2 // ensure at least start and end points
	}
	if distance > 0 {
		idealNumberOfSegments := maxTotalProfilePoints - 1
		idealStepSize := distance / float64(idealNumberOfSegments)

		if idealStepSize < minStepSize {
			stepSize = minStepSize
			steps = int(math.Ceil(distance / stepSize))
		} else {
			stepSize = idealStepSize
			steps = idealNumberOfSegments
		}
	}

	unitVectorEasting := deltaEasting / distance
	unitVectorNorthing := deltaNorthing / distance

	var profile []ProfilePoint
	usedSourcesMap := make(map[string]ElevationSource)

	for i := 0; i <= steps; i++ {
		currentDistance := float64(i) * stepSize
		if i == steps { // ensure the last point is exactly point B
			currentDistance = distance
		}

		easting := startUTM.Easting + unitVectorEasting*currentDistance
		northing := startUTM.Northing + unitVectorNorthing*currentDistance

		elevation, tile, err := getElevationForUTMPoint(sourceZone, easting, northing)
		if err != nil {
			slog.Warn("failed to get elevation for profile point, skipping", "easting", easting, "northing", northing, "error", err)
			continue // skip points where elevation cannot be determined
		}

		// get and store the source information if not already stored
		if _, exists := usedSourcesMap[tile.Source]; !exists {
			resource, resErr := getElevationResource(tile.Source)
			if resErr != nil {
				slog.Warn("failed to get elevation resource details", "sourceCode", tile.Source, "error", resErr)
			} else {
				usedSourcesMap[tile.Source] = resource
			}
		}

		profilePoint := ProfilePoint{
			Distance:    currentDistance,
			Elevation:   elevation,
			Attribution: fmt.Sprintf("%s, %s", tile.Source, tile.Actuality),
		}

		// populate coordinates in the response based on the original request type
		if isUTMRequest {
			profilePoint.Easting = easting
			profilePoint.Northing = northing
		} else {
			lon, lat, transErr := transformUTMToLonLat(easting, northing, sourceZone)
			if transErr != nil {
				slog.Warn("failed to convert profile point back to Lon/Lat", "easting", easting, "northing", northing, "zone", sourceZone, "error", transErr)
			} else {
				profilePoint.Longitude = lon
				profilePoint.Latitude = lat
			}
		}
		profile = append(profile, profilePoint)
	}

	// convert map of sources to a slice
	finalElevationSources := make([]ElevationSource, 0, len(usedSourcesMap))
	for _, source := range usedSourcesMap {
		finalElevationSources = append(finalElevationSources, source)
	}

	return profile, finalElevationSources, nil
}

/*
verifyElevationProfileRequestData verifies 'elevationprofile' request data.
*/
func verifyElevationProfileRequestData(request *http.Request, profileRequest ElevationProfileRequest) error {
	// verify HTTP headers
	if !strings.HasPrefix(strings.ToLower(request.Header.Get("Content-Type")), "application/json") {
		return fmt.Errorf("unexpected or missing HTTP header 'Content-Type', expected 'application/json'")
	}
	if !strings.HasPrefix(strings.ToLower(request.Header.Get("Accept")), "application/json") {
		return fmt.Errorf("unexpected or missing HTTP header 'Accept', expected 'application/json'")
	}

	// verify Type and ID
	if profileRequest.Type != TypeElevationProfileRequest {
		return fmt.Errorf("unexpected request Type [%v]", profileRequest.Type)
	}
	if len(profileRequest.ID) > 1024 {
		return errors.New("ID must be 0-1024 characters long")
	}

	// verify coordinate systems are consistent and valid
	attr := profileRequest.Attributes
	isPointAUTM := attr.PointA.Zone != 0
	isPointALonLat := attr.PointA.Longitude != 0.0 && attr.PointA.Latitude != 0.0

	isPointBUTM := attr.PointB.Zone != 0
	isPointBLonLat := attr.PointB.Longitude != 0.0 && attr.PointB.Latitude != 0.0

	if (isPointAUTM && isPointALonLat) || (isPointBUTM && isPointBLonLat) {
		return errors.New("each point must use either UTM or Lon/Lat coordinates, not both")
	}
	if !(isPointAUTM || isPointALonLat) || !(isPointBUTM || isPointBLonLat) {
		return errors.New("coordinates must be provided for both PointA and PointB")
	}
	if isPointAUTM != isPointBUTM {
		return errors.New("PointA and PointB must use the same coordinate system (both UTM or both Lon/Lat)")
	}
	if isPointAUTM && (attr.PointA.Zone != attr.PointB.Zone) {
		return errors.New("for UTM requests, PointA and PointB must be in the same zone")
	}

	// verify other attributes
	if attr.MaxTotalProfilePoints < 2 || attr.MaxTotalProfilePoints > 2000 {
		return errors.New("MaxTotalProfilePoints must be between 2 and 2000")
	}
	if attr.MinStepSize < 1.0 || attr.MinStepSize > 1000.0 {
		return errors.New("MinStepSize must be between 1.0 and 1000.0 meters")
	}

	return nil
}

/*
buildElevationProfileResponse builds HTTP responses.
*/
func buildElevationProfileResponse(writer http.ResponseWriter, httpStatus int, profileResponse ElevationProfileResponse) {
	writer.Header().Set("Access-Control-Allow-Origin", "*")
	writer.Header().Set("Access-Control-Allow-Methods", "POST")
	writer.Header().Set("Access-Control-Allow-Headers", "Content-Type")

	body, err := json.MarshalIndent(profileResponse, "", "  ")
	if err != nil {
		slog.Error("error marshaling elevationprofile response", "error", err)
		http.Error(writer, "Internal Server Error", http.StatusInternalServerError)
		return
	}

	writer.Header().Set("Content-Type", JSONAPIMediaType)
	writer.WriteHeader(httpStatus)
	_, err = writer.Write(body)
	if err != nil {
		slog.Error("error writing HTTP response body", "error", err)
	}
}
