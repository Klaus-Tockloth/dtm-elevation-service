package main

import (
	"bytes"
	"compress/gzip"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
)

/*
contoursRequest handles 'contours request' from client.
*/
func contoursRequest(writer http.ResponseWriter, request *http.Request) {
	var contoursResponse = ContoursResponse{Type: TypeContoursResponse, ID: "unknown"}
	contoursResponse.Attributes.IsError = true

	// statistics
	atomic.AddUint64(&ContoursRequests, 1)

	// limit overall request body size
	request.Body = http.MaxBytesReader(writer, request.Body, MaxContoursRequestBodySize)

	// read request
	bodyData, err := io.ReadAll(request.Body)
	if err != nil {
		// check specifically for the error returned by MaxBytesReader
		var maxBytesErr *http.MaxBytesError
		if errors.As(err, &maxBytesErr) {
			slog.Warn("contours request: request body too large", "limit", maxBytesErr.Limit, "ID", "unknown")
			contoursResponse.Attributes.Error.Code = "4000"
			contoursResponse.Attributes.Error.Title = "request body too large"
			contoursResponse.Attributes.Error.Detail = fmt.Sprintf("request body exceeds limit of %d bytes", maxBytesErr.Limit)
			buildContoursResponse(writer, http.StatusRequestEntityTooLarge, contoursResponse)
		} else {
			// handle other read errors
			slog.Warn("contours request: error reading request body", "error", err, "ID", "unknown")
			contoursResponse.Attributes.Error.Code = "4020"
			contoursResponse.Attributes.Error.Title = "error reading request body"
			contoursResponse.Attributes.Error.Detail = err.Error()
			buildContoursResponse(writer, http.StatusBadRequest, contoursResponse)
		}
		return
	}

	// unmarshal request
	contoursRequest := ContoursRequest{}
	err = json.Unmarshal(bodyData, &contoursRequest)
	if err != nil {
		slog.Warn("contours request: error unmarshaling request body", "error", err, "ID", "unknown")
		contoursResponse.Attributes.Error.Code = "4040"
		contoursResponse.Attributes.Error.Title = "error unmarshaling request body"
		contoursResponse.Attributes.Error.Detail = err.Error()
		buildContoursResponse(writer, http.StatusBadRequest, contoursResponse)
		return
	}

	// verify request data
	err = verifyContoursRequestData(request, contoursRequest)
	if err != nil {
		slog.Warn("contours request: error verifying request data", "error", err, "ID", contoursRequest.ID)
		contoursResponse.Attributes.Error.Code = "4060"
		contoursResponse.Attributes.Error.Title = "error verifying request data"
		contoursResponse.Attributes.Error.Detail = err.Error()
		buildContoursResponse(writer, http.StatusBadRequest, contoursResponse)
		return
	}

	zone := 0
	easting := 0.0
	northing := 0.0
	longitude := 0.0
	latitude := 0.0
	var tile TileMetadata
	var tiles []TileMetadata
	isLonLat := false

	// determine type of coordinates
	if contoursRequest.Attributes.Zone != 0 {
		// input from UTM coordinates
		zone = contoursRequest.Attributes.Zone
		easting = contoursRequest.Attributes.Easting
		northing = contoursRequest.Attributes.Northing

		// get tile metadata for primary tile (e.g. "32_507_5491")
		tile, err = getGeotiffTile(easting, northing, zone, 1)
		if err != nil {
			slog.Warn("contours request: error getting GeoTIFF tile for UTM coordinates", "error", err,
				"easting", easting, "northing", northing, "zone", zone, "ID", contoursRequest.ID)
			contoursResponse.Attributes.Error.Code = "4080"
			contoursResponse.Attributes.Error.Title = "getting GeoTIFF tile for UTM coordinates"
			contoursResponse.Attributes.Error.Detail = err.Error()
			buildContoursResponse(writer, http.StatusBadRequest, contoursResponse)
			return
		}
		tiles = append(tiles, tile)

		// get tile metadata for secondary tile (e.g. "32_507_5491_2")
		tile, err = getGeotiffTile(easting, northing, zone, 2)
		if err == nil {
			tiles = append(tiles, tile)

			// get tile metadata for tertiary tile (e.g. "32_507_5491_3")
			tile, err = getGeotiffTile(easting, northing, zone, 3)
			if err == nil {
				tiles = append(tiles, tile)
			}
		}
	} else {
		// input from lon/lat coordinates
		longitude = contoursRequest.Attributes.Longitude
		latitude = contoursRequest.Attributes.Latitude
		isLonLat = true

		// get tile metadata for primary tile (e.g. "32_507_5491")
		tile, zone, easting, northing, err = getTileUTM(longitude, latitude)
		if err != nil {
			err = fmt.Errorf("error [%w] getting tile for coordinates lon: %.8f, lat: %.8f", err, longitude, latitude)
			slog.Warn("contours request: error getting GeoTIFF tile for lon/lat coordinates", "error", err,
				"longitude", longitude, "latitude", latitude, "ID", contoursRequest.ID)
			contoursResponse.Attributes.Error.Code = "4100"
			contoursResponse.Attributes.Error.Title = "getting GeoTIFF tile for lon/lat coordinates"
			contoursResponse.Attributes.Error.Detail = err.Error()
			buildContoursResponse(writer, http.StatusBadRequest, contoursResponse)
			return
		}
		tiles = append(tiles, tile)

		// get tile metadata for secondary tile (e.g. "32_507_5491_2")
		tile, err = getGeotiffTile(easting, northing, zone, 2)
		if err == nil {
			tiles = append(tiles, tile)

			// get tile metadata for tertiary tile (e.g. "32_507_5491_3")
			tile, err = getGeotiffTile(easting, northing, zone, 3)
			if err == nil {
				tiles = append(tiles, tile)
			}
		}
	}

	// build contours for all existing tiles
	equidistance := contoursRequest.Attributes.Equidistance
	for _, tile := range tiles {
		contour, err := generateContourObjectForTile(tile, equidistance, isLonLat)
		if err != nil {
			slog.Warn("contours request: error generating contours object for tile", "error", err, "ID", contoursRequest.ID)
			contoursResponse.Attributes.Error.Code = "4120"
			contoursResponse.Attributes.Error.Title = "error generating contours object for tile"
			contoursResponse.Attributes.Error.Detail = err.Error()
			buildContoursResponse(writer, http.StatusBadRequest, contoursResponse)
			return
		}
		contoursResponse.Attributes.Contours = append(contoursResponse.Attributes.Contours, contour)
	}

	// copy request parameters into response
	contoursResponse.ID = contoursRequest.ID
	contoursResponse.Attributes.IsError = false
	contoursResponse.Attributes.Zone = contoursRequest.Attributes.Zone
	contoursResponse.Attributes.Easting = contoursRequest.Attributes.Easting
	contoursResponse.Attributes.Northing = contoursRequest.Attributes.Northing
	contoursResponse.Attributes.Longitude = contoursRequest.Attributes.Longitude
	contoursResponse.Attributes.Latitude = contoursRequest.Attributes.Latitude
	contoursResponse.Attributes.Equidistance = contoursRequest.Attributes.Equidistance

	// success response
	buildContoursResponse(writer, http.StatusOK, contoursResponse)
}

/*
verifyContoursRequestData verifies 'contours' request data.
It performs several checks on the request data to ensure its validity.
*/
func verifyContoursRequestData(request *http.Request, contoursRequest ContoursRequest) error {
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
	if contoursRequest.Type != TypeContoursRequest {
		return fmt.Errorf("unexpected request Type [%v]", contoursRequest.Type)
	}

	// verify ID
	if len(contoursRequest.ID) > 1024 {
		return errors.New("ID must be 0-1024 characters long")
	}

	// verify coordinates (either utm or lon/lat coordinates must be set)
	if contoursRequest.Attributes.Zone == 0 && contoursRequest.Attributes.Longitude == 0 {
		return errors.New("either utm or lon/lat coordinates must be set")
	}

	// verify zone for Germany (Zone: 32 or 33)
	if contoursRequest.Attributes.Zone != 0 {
		if contoursRequest.Attributes.Zone < 32 || contoursRequest.Attributes.Zone > 33 {
			return errors.New("invalid zone for Germany")
		}
	}

	// verify longitude for Germany (Longitude: from  5.8663° E to 15.0419° E)
	if contoursRequest.Attributes.Longitude != 0 {
		if contoursRequest.Attributes.Longitude > 15.3 || contoursRequest.Attributes.Longitude < 5.5 {
			return errors.New("invalid longitude for Germany")
		}
	}

	// verify latitude for Germany (Latitude: from 47.2701° N to 55.0586° N)
	if contoursRequest.Attributes.Latitude != 0 {
		if contoursRequest.Attributes.Latitude > 55.3 || contoursRequest.Attributes.Latitude < 47.0 {
			return errors.New("invalid latitude for Germany")
		}
	}

	// verify equidistance
	if contoursRequest.Attributes.Equidistance < 0.2 || contoursRequest.Attributes.Equidistance > 25.0 {
		return errors.New("equidistance must be between 0.2 and 25.0 meters")
	}

	return nil
}

/*
buildContoursResponse builds HTTP responses with specified status and body.
It sets the Content-Type and Content-Length headers before writing the response body.
This function is used to construct consistent HTTP responses throughout the application.
*/
func buildContoursResponse(writer http.ResponseWriter, httpStatus int, contoursResponse ContoursResponse) {
	// log limit length of body (e.g., the contours objects as part of the body can be very large)
	maxBodyLength := 1024

	// CORS: allow requests from any origin
	writer.Header().Set("Access-Control-Allow-Origin", "*")
	// CORS: allowed methods
	writer.Header().Set("Access-Control-Allow-Methods", "POST")
	// CORS: allowed headers
	writer.Header().Set("Access-Control-Allow-Headers", "Content-Type")

	// marshal response
	body, err := json.MarshalIndent(contoursResponse, "", "  ")
	if err != nil {
		slog.Error("error marshaling point response", "error", err, "body length", len(body),
			fmt.Sprintf("body (limited to first %d bytes)", maxBodyLength), body[:maxBodyLength])

		http.Error(writer, "Internal Server Error", http.StatusInternalServerError)
		return
	}

	// gzip response body
	var bytesBuffer bytes.Buffer
	gz := gzip.NewWriter(&bytesBuffer)

	_, err = gz.Write(body)
	if err != nil {
		slog.Error("error [%v] at gz.Write()", "error", err)
		http.Error(writer, "Internal Server Error", http.StatusInternalServerError)
		return
	}

	err = gz.Close()
	if err != nil {
		slog.Error("error [%v] at gz.Close()", "error", err)
		http.Error(writer, "Internal Server Error", http.StatusInternalServerError)
		return
	}

	// set headers
	writer.Header().Set("Content-Encoding", "gzip")
	writer.Header().Set("Content-Type", JSONAPIMediaType)
	writer.WriteHeader(httpStatus)

	// send response
	_, err = writer.Write(bytesBuffer.Bytes())
	if err != nil {
		slog.Error("error writing HTTP response body", "error", err, "body length", len(body),
			fmt.Sprintf("body (limited to first %d bytes)", maxBodyLength), body[:maxBodyLength])
	}
}

/*
generateContourObjectForTile builds contour object for given tile index.
Strategy to avoid artefact:
- generate contours in the source SRS
- convert generated contours to the target SRS
*/
func generateContourObjectForTile(tile TileMetadata, equidistance float64, isLonLat bool) (Contour, error) {
	var contour Contour

	// run operations in temp directory
	tempDir, err := os.MkdirTemp("", "dtm-elevation-service-contours-")
	if err != nil {
		return contour, fmt.Errorf("error [%w] at os.MkdirTemp()", err)
	}
	defer func() {
		_ = os.RemoveAll(tempDir)
	}()

	filenameTif := tile.Path
	filenameUtmGeoJSON := filepath.Join(tempDir, tile.Index+".utm.geojson")
	filenameLonLatGeoJSON := filepath.Join(tempDir, tile.Index+".lonlat.geojson")

	equidistanceString := fmt.Sprintf("%.2f", equidistance)
	nameOutputLayer := fmt.Sprintf("Höhenlinien %s Meter für Kachel %s", equidistanceString, tile.Index)

	// gdal_contour
	commandExitStatus, commandOutput, err := runCommand("gdal_contour", []string{"-f", "GeoJSON",
		"-i", equidistanceString, "-nln", nameOutputLayer, "-a", "Hoehe", filenameTif, filenameUtmGeoJSON})
	if err != nil {
		return contour, fmt.Errorf("error [%w: %d - %s] at runCommand()", err, commandExitStatus, commandOutput)
	}
	// fmt.Printf("commandExitStatus: %d\n", commandExitStatus)
	// fmt.Printf("commandOutput: %s\n", commandOutput)

	// derive zone from tile index (e.g. 32_383_5802)
	parts := strings.Split(tile.Index, "_")
	zone := parts[0]
	epsgCode := ""
	switch zone {
	case "32":
		epsgCode = "EPSG:25832"
	case "33":
		epsgCode = "EPSG:25833"
	default:
		return contour, fmt.Errorf("invalid zone [%s]", zone)
	}

	if isLonLat {
		// ogr2ogr
		commandExitStatus, commandOutput, err = runCommand("ogr2ogr", []string{"-f", "GeoJSON",
			"-s_srs", epsgCode, "-t_srs", "EPSG:4326", filenameLonLatGeoJSON, filenameUtmGeoJSON})
		if err != nil {
			return contour, fmt.Errorf("error [%w: %d - %s] at runCommand()", err, commandExitStatus, commandOutput)

		}
		// fmt.Printf("commandExitStatus: %d\n", commandExitStatus)
		// fmt.Printf("commandOutput: %s\n", commandOutput)
	}

	// read result file
	var data []byte
	if isLonLat {
		data, err = os.ReadFile(filenameLonLatGeoJSON)
	} else {
		data, err = os.ReadFile(filenameUtmGeoJSON)
	}
	if err != nil {
		return contour, fmt.Errorf("error [%w] at os.ReadFile()", err)
	}

	// set contour return structure
	contour.Data = data
	contour.DataFormat = "geojson"
	contour.Actuality = tile.Actuality
	contour.Origin = tile.Source
	contour.TileIndex = tile.Index

	// get attribution for resource
	attribution := "unknown"
	resource, err := getElevationResource(tile.Source)
	if err != nil {
		slog.Error("contours request: error getting elevation resource", "error", err, "source", tile.Source)
	} else {
		attribution = resource.Attribution
	}
	contour.Attribution = attribution

	return contour, nil
}

/*
generateContourObjectForTile2 builds contour object for given tile index.
*/
func generateContourObjectForTile2(tile TileMetadata, equidistance float64, isLonLat bool) (Contour, error) { //nolint:unused
	var contour Contour
	var commandExitStatus int
	var commandOutput []byte
	var err error

	// run operations in temp directory
	tempDir, err := os.MkdirTemp("", "dtm-elevation-service-contours-")
	if err != nil {
		return contour, fmt.Errorf("error [%w] at os.MkdirTemp()", err)
	}
	defer func() {
		_ = os.RemoveAll(tempDir)
	}()

	filenameTif := tile.Path
	filenameWgs84Tif := filepath.Join(tempDir, tile.Index+".wgs84.tif")
	filenameGeoJSON := filepath.Join(tempDir, tile.Index+".geojson")

	if isLonLat {
		// reprojection with gdalwarp
		commandExitStatus, commandOutput, err = runCommand("gdalwarp", []string{"-t_srs", "EPSG:4326", filenameTif, filenameWgs84Tif})
		if err != nil {
			return contour, fmt.Errorf("error [%w: %d - %s] at runCommand()", err, commandExitStatus, commandOutput)
		}
		// fmt.Printf("commandExitStatus: %d\n", commandExitStatus)
		// fmt.Printf("commandOutput: %s\n", commandOutput)
		filenameTif = filenameWgs84Tif
	}

	equidistanceString := fmt.Sprintf("%.2f", equidistance)
	nameOutputLayer := fmt.Sprintf("Höhenlinien %s Meter für Kachel %s", equidistanceString, tile.Index)

	// gdal_contour (based on srs from tif file)
	commandExitStatus, commandOutput, err = runCommand("gdal_contour", []string{"-f", "GeoJSON",
		"-i", equidistanceString, "-nln", nameOutputLayer, "-a", "Hoehe", filenameTif, filenameGeoJSON})
	if err != nil {
		return contour, fmt.Errorf("error [%w: %d - %s] at runCommand()", err, commandExitStatus, commandOutput)
	}
	// fmt.Printf("commandExitStatus: %d\n", commandExitStatus)
	// fmt.Printf("commandOutput: %s\n", commandOutput)

	// read result file
	data, err := os.ReadFile(filenameGeoJSON)
	if err != nil {
		return contour, fmt.Errorf("error [%w] at os.ReadFile()", err)
	}

	// set contour return structure
	contour.Data = data
	contour.Actuality = tile.Actuality
	contour.Origin = tile.Source
	contour.TileIndex = tile.Index

	// get attribution for resource
	attribution := "unknown"
	resource, err := getElevationResource(tile.Source)
	if err != nil {
		slog.Error("contours request: error getting elevation resource", "error", err, "source", tile.Source)
	} else {
		attribution = resource.Attribution
	}
	contour.Attribution = attribution

	return contour, nil
}
