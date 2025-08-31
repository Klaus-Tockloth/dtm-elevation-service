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
roughnessRequest handles 'Roughness request' from client.
*/
func roughnessRequest(writer http.ResponseWriter, request *http.Request) {
	var roughnessResponse = RoughnessResponse{Type: TypeRoughnessResponse, ID: "unknown"}
	roughnessResponse.Attributes.IsError = true

	// statistics
	atomic.AddUint64(&RoughnessRequests, 1)

	// limit overall request body size
	request.Body = http.MaxBytesReader(writer, request.Body, MaxRoughnessRequestBodySize)

	// read request
	bodyData, err := io.ReadAll(request.Body)
	if err != nil {
		// check specifically for the error returned by MaxBytesReader
		var maxBytesErr *http.MaxBytesError
		if errors.As(err, &maxBytesErr) {
			slog.Warn("roughness request: request body too large", "limit", maxBytesErr.Limit, "ID", "unknown")
			roughnessResponse.Attributes.Error.Code = "10000"
			roughnessResponse.Attributes.Error.Title = "request body too large"
			roughnessResponse.Attributes.Error.Detail = fmt.Sprintf("request body exceeds limit of %d bytes", maxBytesErr.Limit)
			buildRoughnessResponse(writer, http.StatusRequestEntityTooLarge, roughnessResponse)
		} else {
			// handle other read errors
			slog.Warn("roughness request: error reading request body", "error", err, "ID", "unknown")
			roughnessResponse.Attributes.Error.Code = "10020"
			roughnessResponse.Attributes.Error.Title = "error reading request body"
			roughnessResponse.Attributes.Error.Detail = err.Error()
			buildRoughnessResponse(writer, http.StatusBadRequest, roughnessResponse)
		}
		return
	}

	// unmarshal request
	roughnessRequest := RoughnessRequest{}
	err = json.Unmarshal(bodyData, &roughnessRequest)
	if err != nil {
		slog.Warn("roughness request: error unmarshaling request body", "error", err, "ID", "unknown")
		roughnessResponse.Attributes.Error.Code = "10040"
		roughnessResponse.Attributes.Error.Title = "error unmarshaling request body"
		roughnessResponse.Attributes.Error.Detail = err.Error()
		buildRoughnessResponse(writer, http.StatusBadRequest, roughnessResponse)
		return
	}

	// verify request data
	err = verifyRoughnessRequestData(request, roughnessRequest)
	if err != nil {
		slog.Warn("roughness request: error verifying request data", "error", err, "ID", roughnessRequest.ID)
		roughnessResponse.Attributes.Error.Code = "10060"
		roughnessResponse.Attributes.Error.Title = "error verifying request data"
		roughnessResponse.Attributes.Error.Detail = err.Error()
		buildRoughnessResponse(writer, http.StatusBadRequest, roughnessResponse)
		return
	}

	// copy request parameters into response
	roughnessResponse.ID = roughnessRequest.ID
	roughnessResponse.Attributes.Zone = roughnessRequest.Attributes.Zone
	roughnessResponse.Attributes.Easting = roughnessRequest.Attributes.Easting
	roughnessResponse.Attributes.Northing = roughnessRequest.Attributes.Northing
	roughnessResponse.Attributes.Longitude = roughnessRequest.Attributes.Longitude
	roughnessResponse.Attributes.Latitude = roughnessRequest.Attributes.Latitude
	roughnessResponse.Attributes.ColorTextFileContent = roughnessRequest.Attributes.ColorTextFileContent
	roughnessResponse.Attributes.ColoringAlgorithm = roughnessRequest.Attributes.ColoringAlgorithm

	zone := 0
	easting := 0.0
	northing := 0.0
	longitude := 0.0
	latitude := 0.0
	var tiles []TileMetadata
	var outputFormat string

	// determine type of coordinates
	if roughnessRequest.Attributes.Zone != 0 {
		// input from UTM coordinates
		zone = roughnessRequest.Attributes.Zone
		easting = roughnessRequest.Attributes.Easting
		northing = roughnessRequest.Attributes.Northing
		outputFormat = "geotiff"

		// get all tiles (metadata) for given UTM coordinates
		tiles, err = getAllTilesUTM(zone, easting, northing)
		if err != nil {
			slog.Warn("roughness request: error getting GeoTIFF tile for UTM coordinates", "error", err,
				"easting", easting, "northing", northing, "zone", zone, "ID", roughnessRequest.ID)
			roughnessResponse.Attributes.Error.Code = "10080"
			roughnessResponse.Attributes.Error.Title = "getting GeoTIFF tile for UTM coordinates"
			roughnessResponse.Attributes.Error.Detail = err.Error()
			buildRoughnessResponse(writer, http.StatusBadRequest, roughnessResponse)
			return
		}
	} else {
		// input from lon/lat coordinates
		longitude = roughnessRequest.Attributes.Longitude
		latitude = roughnessRequest.Attributes.Latitude
		outputFormat = "png"

		// get all tiles (metadata) for given lon/lat coordinates
		tiles, err = getAllTilesLonLat(longitude, latitude)
		if err != nil {
			err = fmt.Errorf("error [%w] getting tile for coordinates lon: %.8f, lat: %.8f", err, longitude, latitude)
			slog.Warn("roughness request: error getting GeoTIFF tile for lon/lat coordinates", "error", err,
				"longitude", longitude, "latitude", latitude, "ID", roughnessRequest.ID)
			roughnessResponse.Attributes.Error.Code = "10100"
			roughnessResponse.Attributes.Error.Title = "getting GeoTIFF tile for lon/lat coordinates"
			roughnessResponse.Attributes.Error.Detail = err.Error()
			buildRoughnessResponse(writer, http.StatusBadRequest, roughnessResponse)
			return
		}
	}

	// build roughness for all existing tiles
	for _, tile := range tiles {
		roughness, err := generateRoughnessObjectForTile(tile, outputFormat, roughnessRequest.Attributes.ColorTextFileContent, roughnessRequest.Attributes.ColoringAlgorithm)
		if err != nil {
			slog.Warn("roughness request: error generating roughness object for tile", "error", err, "ID", roughnessRequest.ID)
			roughnessResponse.Attributes.Error.Code = "10120"
			roughnessResponse.Attributes.Error.Title = "error generating roughness object for tile"
			roughnessResponse.Attributes.Error.Detail = err.Error()
			buildRoughnessResponse(writer, http.StatusBadRequest, roughnessResponse)
			return
		}
		roughnessResponse.Attributes.Roughnesses = append(roughnessResponse.Attributes.Roughnesses, roughness)
	}

	// success response
	roughnessResponse.Attributes.IsError = false
	buildRoughnessResponse(writer, http.StatusOK, roughnessResponse)
}

/*
verifyRoughnessRequestData verifies 'Roughness' request data.
It performs several checks on the request data to ensure its validity.
*/
func verifyRoughnessRequestData(request *http.Request, roughnessRequest RoughnessRequest) error {
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
	if roughnessRequest.Type != TypeRoughnessRequest {
		return fmt.Errorf("unexpected request Type [%v]", roughnessRequest.Type)
	}

	// verify ID
	if len(roughnessRequest.ID) > 1024 {
		return errors.New("ID must be 0-1024 characters long")
	}

	// verify coordinates (either utm or lon/lat coordinates must be set)
	if roughnessRequest.Attributes.Zone == 0 && roughnessRequest.Attributes.Longitude == 0 {
		return errors.New("either utm or lon/lat coordinates must be set")
	}

	// verify zone for Germany (Zone: 32 or 33)
	if roughnessRequest.Attributes.Zone != 0 {
		if roughnessRequest.Attributes.Zone < 32 || roughnessRequest.Attributes.Zone > 33 {
			return errors.New("invalid zone for Germany")
		}
	}

	// verify longitude for Germany (Longitude: from  5.8663° E to 15.0419° E)
	if roughnessRequest.Attributes.Longitude != 0 {
		if roughnessRequest.Attributes.Longitude > 15.3 || roughnessRequest.Attributes.Longitude < 5.5 {
			return errors.New("invalid longitude for Germany")
		}
	}

	// verify latitude for Germany (Latitude: from 47.2701° N to 55.0586° N)
	if roughnessRequest.Attributes.Latitude != 0 {
		if roughnessRequest.Attributes.Latitude > 55.3 || roughnessRequest.Attributes.Latitude < 47.0 {
			return errors.New("invalid latitude for Germany")
		}
	}

	// verify 'color text file content'
	err := verifyColorTextFileContent(roughnessRequest.Attributes.ColorTextFileContent)
	if err != nil {
		return errors.New("invalid color text file content (%w)")
	}

	// verify coloring algorithm
	if roughnessRequest.Attributes.ColoringAlgorithm != "" {
		if !(roughnessRequest.Attributes.ColoringAlgorithm == "interpolation" || roughnessRequest.Attributes.ColoringAlgorithm == "rounding") {
			return errors.New("unsupported coloring algorithm (not 'interpolation' or 'rounding')")
		}
	}

	return nil
}

/*
buildRoughnessResponse builds HTTP responses with specified status and body.
It sets the Content-Type and Content-Length headers before writing the response body.
This function is used to construct consistent HTTP responses throughout the application.
*/
func buildRoughnessResponse(writer http.ResponseWriter, httpStatus int, roughnessResponse RoughnessResponse) {
	// log limit length of body (e.g., the roughness objects as part of the body can be very large)
	maxBodyLength := 1024

	// CORS: allow requests from any origin
	writer.Header().Set("Access-Control-Allow-Origin", "*")
	// CORS: allowed methods
	writer.Header().Set("Access-Control-Allow-Methods", "POST")
	// CORS: allowed headers
	writer.Header().Set("Access-Control-Allow-Headers", "Content-Type")

	// marshal response
	body, err := json.MarshalIndent(roughnessResponse, "", "  ")
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
generateRoughnessObjectForTile builds roughness object for given tile index.
*/
func generateRoughnessObjectForTile(tile TileMetadata, outputFormat string, colorTextFileContent []string, coloringAlgorithm string) (Roughness, error) {
	var roughness Roughness
	var boundingBox WGS84BoundingBox

	// run operations in temp directory
	tempDir, err := os.MkdirTemp("", "dtm-elevation-service-roughness-")
	if err != nil {
		return roughness, fmt.Errorf("error [%w] at os.MkdirTemp()", err)
	}
	defer func() {
		_ = os.RemoveAll(tempDir)
	}()

	// create 'color-text-file' for 'gdaldem color-relief' in temp directory
	colorTextFile := filepath.Join(tempDir, "color-text-file.txt")
	err = createColorTextFile(colorTextFile, colorTextFileContent)
	if err != nil {
		return roughness, fmt.Errorf("error [%w] creating 'color-text-file'", err)
	}

	inputGeoTIFF := tile.Path
	roughnessUTMGeoTIFF := filepath.Join(tempDir, tile.Index+".roughnessutm.tif")
	roughnessColorUTMGeoTIFF := filepath.Join(tempDir, tile.Index+".roughnesscolor.utm.tif")
	roughnessWebmercatorGeoTIFF := filepath.Join(tempDir, tile.Index+".roughnesswebmercator.tif")
	roughnessColorWebmercatoPNG := filepath.Join(tempDir, tile.Index+".roughnesscolor.webmercator.png")

	// 1. create native Roughness with 'gdaldem roughness'
	commandExitStatus, commandOutput, err := runCommand("gdaldem", []string{"roughness", inputGeoTIFF, roughnessUTMGeoTIFF, "-compute_edges"})
	if err != nil {
		return roughness, fmt.Errorf("error [%w: %d - %s] at runCommand()", err, commandExitStatus, commandOutput)
	}
	// fmt.Printf("commandExitStatus: %d\n", commandExitStatus)
	// fmt.Printf("commandOutput: %s\n", commandOutput)

	var data []byte
	switch strings.ToLower(outputFormat) {
	case "geotiff":
		// 2. colorize roughness with 'gdaldem color-relief'
		options := []string{"color-relief", roughnessUTMGeoTIFF, colorTextFile, roughnessColorUTMGeoTIFF, "-alpha"}
		if coloringAlgorithm == "rounding" {
			options = append(options, "-nearest_color_entry")
		}
		commandExitStatus, commandOutput, err = runCommand("gdaldem", options)
		if err != nil {
			return roughness, fmt.Errorf("error [%w: %d - %s] at runCommand()", err, commandExitStatus, commandOutput)
		}
		// fmt.Printf("commandExitStatus: %d\n", commandExitStatus)
		// fmt.Printf("commandOutput: %s\n", commandOutput)

		data, err = os.ReadFile(roughnessColorUTMGeoTIFF)
		if err != nil {
			return roughness, fmt.Errorf("error [%w] at os.ReadFile()", err)
		}

	case "png":
		// 2. convert UTM (EPSG:25832/EPSG:25833) to Webmercator (EPSG:3857) with 'gdalwarp'
		commandExitStatus, commandOutput, err = runCommand("gdalwarp", []string{"-t_srs", "EPSG:3857", roughnessUTMGeoTIFF, roughnessWebmercatorGeoTIFF})
		if err != nil {
			return roughness, fmt.Errorf("error [%w: %d - %s] at runCommand()", err, commandExitStatus, commandOutput)
		}
		// fmt.Printf("commandExitStatus: %d\n", commandExitStatus)
		// fmt.Printf("commandOutput: %s\n", commandOutput)

		// 3. colorize roughness with 'gdaldem color-relief' (creates PNG file)
		options := []string{"color-relief", roughnessWebmercatorGeoTIFF, colorTextFile, roughnessColorWebmercatoPNG, "-alpha"}
		if coloringAlgorithm == "rounding" {
			options = append(options, "-nearest_color_entry")
		}
		commandExitStatus, commandOutput, err = runCommand("gdaldem", options)
		if err != nil {
			return roughness, fmt.Errorf("error [%w: %d - %s] at runCommand()", err, commandExitStatus, commandOutput)
		}
		// fmt.Printf("commandExitStatus: %d\n", commandExitStatus)
		// fmt.Printf("commandOutput: %s\n", commandOutput)

		// 4. get bounding box (in wgs84) for webmercator tif (georeference of webmercator png )
		boundingBox, err = calculateWGS84BoundingBox(tile)
		if err != nil {
			return roughness, fmt.Errorf("error [%w] at calculateWGS84BoundingBox(), file: %s", err, tile.Path)
		}

		// read result file
		data, err = os.ReadFile(roughnessColorWebmercatoPNG)
		if err != nil {
			return roughness, fmt.Errorf("error [%w] at os.ReadFile()", err)
		}

	default:
		return roughness, fmt.Errorf("unsupported format [%s]", outputFormat)
	}

	// set contour return structure
	roughness.Data = data
	roughness.DataFormat = outputFormat
	roughness.Actuality = tile.Actuality
	roughness.Origin = tile.Source
	roughness.TileIndex = tile.Index
	roughness.BoundingBox = boundingBox // only relevant for PNG

	// get attribution for resource
	attribution := "unknown"
	resource, err := getElevationResource(tile.Source)
	if err != nil {
		slog.Error("roughness request: error getting elevation resource", "error", err, "source", tile.Source)
	} else {
		attribution = resource.Attribution
	}
	roughness.Attribution = attribution

	return roughness, nil
}
