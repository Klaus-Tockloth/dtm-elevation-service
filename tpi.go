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
tpiRequest handles 'TPI request' from client.
*/
func tpiRequest(writer http.ResponseWriter, request *http.Request) {
	var tpiResponse = TPIResponse{Type: TypeTPIResponse, ID: "unknown"}
	tpiResponse.Attributes.IsError = true

	// statistics
	atomic.AddUint64(&TPIRequests, 1)

	// limit overall request body size
	request.Body = http.MaxBytesReader(writer, request.Body, MaxTPIRequestBodySize)

	// read request
	bodyData, err := io.ReadAll(request.Body)
	if err != nil {
		// check specifically for the error returned by MaxBytesReader
		var maxBytesErr *http.MaxBytesError
		if errors.As(err, &maxBytesErr) {
			slog.Warn("tpi request: request body too large", "limit", maxBytesErr.Limit, "ID", "unknown")
			tpiResponse.Attributes.Error.Code = "8000"
			tpiResponse.Attributes.Error.Title = "request body too large"
			tpiResponse.Attributes.Error.Detail = fmt.Sprintf("request body exceeds limit of %d bytes", maxBytesErr.Limit)
			buildTPIResponse(writer, http.StatusRequestEntityTooLarge, tpiResponse)
		} else {
			// handle other read errors
			slog.Warn("tpi request: error reading request body", "error", err, "ID", "unknown")
			tpiResponse.Attributes.Error.Code = "8020"
			tpiResponse.Attributes.Error.Title = "error reading request body"
			tpiResponse.Attributes.Error.Detail = err.Error()
			buildTPIResponse(writer, http.StatusBadRequest, tpiResponse)
		}
		return
	}

	// unmarshal request
	tpiRequest := TPIRequest{}
	err = json.Unmarshal(bodyData, &tpiRequest)
	if err != nil {
		slog.Warn("tpi request: error unmarshaling request body", "error", err, "ID", "unknown")
		tpiResponse.Attributes.Error.Code = "8040"
		tpiResponse.Attributes.Error.Title = "error unmarshaling request body"
		tpiResponse.Attributes.Error.Detail = err.Error()
		buildTPIResponse(writer, http.StatusBadRequest, tpiResponse)
		return
	}

	// verify request data
	err = verifyTPIRequestData(request, tpiRequest)
	if err != nil {
		slog.Warn("tpi request: error verifying request data", "error", err, "ID", tpiRequest.ID)
		tpiResponse.Attributes.Error.Code = "8060"
		tpiResponse.Attributes.Error.Title = "error verifying request data"
		tpiResponse.Attributes.Error.Detail = err.Error()
		buildTPIResponse(writer, http.StatusBadRequest, tpiResponse)
		return
	}

	zone := 0
	easting := 0.0
	northing := 0.0
	longitude := 0.0
	latitude := 0.0
	var tiles []TileMetadata
	var outputFormat string

	// determine type of coordinates
	if tpiRequest.Attributes.Zone != 0 {
		// input from UTM coordinates
		zone = tpiRequest.Attributes.Zone
		easting = tpiRequest.Attributes.Easting
		northing = tpiRequest.Attributes.Northing
		outputFormat = "geotiff"

		// get all tiles (metadata) for given UTM coordinates
		tiles, err = getAllTilesUTM(zone, easting, northing)
		if err != nil {
			slog.Warn("tpi request: error getting GeoTIFF tile for UTM coordinates", "error", err,
				"easting", easting, "northing", northing, "zone", zone, "ID", tpiRequest.ID)
			tpiResponse.Attributes.Error.Code = "8080"
			tpiResponse.Attributes.Error.Title = "getting GeoTIFF tile for UTM coordinates"
			tpiResponse.Attributes.Error.Detail = err.Error()
			buildTPIResponse(writer, http.StatusBadRequest, tpiResponse)
			return
		}
	} else {
		// input from lon/lat coordinates
		longitude = tpiRequest.Attributes.Longitude
		latitude = tpiRequest.Attributes.Latitude
		outputFormat = "png"

		// get all tiles (metadata) for given lon/lat coordinates
		tiles, err = getAllTilesLonLat(longitude, latitude)
		if err != nil {
			err = fmt.Errorf("error [%w] getting tile for coordinates lon: %.8f, lat: %.8f", err, longitude, latitude)
			slog.Warn("tpi request: error getting GeoTIFF tile for lon/lat coordinates", "error", err,
				"longitude", longitude, "latitude", latitude, "ID", tpiRequest.ID)
			tpiResponse.Attributes.Error.Code = "8100"
			tpiResponse.Attributes.Error.Title = "getting GeoTIFF tile for lon/lat coordinates"
			tpiResponse.Attributes.Error.Detail = err.Error()
			buildTPIResponse(writer, http.StatusBadRequest, tpiResponse)
			return
		}
	}

	// build tpi for all existing tiles
	for _, tile := range tiles {
		tpi, err := generateTPIObjectForTile(tile, outputFormat, tpiRequest.Attributes.ColorTextFileContent, tpiRequest.Attributes.ColoringAlgorithm)
		if err != nil {
			slog.Warn("tpi request: error generating tpi object for tile", "error", err, "ID", tpiRequest.ID)
			tpiResponse.Attributes.Error.Code = "8120"
			tpiResponse.Attributes.Error.Title = "error generating tpi object for tile"
			tpiResponse.Attributes.Error.Detail = err.Error()
			buildTPIResponse(writer, http.StatusBadRequest, tpiResponse)
			return
		}
		tpiResponse.Attributes.TPIs = append(tpiResponse.Attributes.TPIs, tpi)
	}

	// copy request parameters into response
	tpiResponse.ID = tpiRequest.ID
	tpiResponse.Attributes.IsError = false
	tpiResponse.Attributes.Zone = tpiRequest.Attributes.Zone
	tpiResponse.Attributes.Easting = tpiRequest.Attributes.Easting
	tpiResponse.Attributes.Northing = tpiRequest.Attributes.Northing
	tpiResponse.Attributes.Longitude = tpiRequest.Attributes.Longitude
	tpiResponse.Attributes.Latitude = tpiRequest.Attributes.Latitude
	tpiResponse.Attributes.ColorTextFileContent = tpiRequest.Attributes.ColorTextFileContent
	tpiResponse.Attributes.ColoringAlgorithm = tpiRequest.Attributes.ColoringAlgorithm

	// success response
	buildTPIResponse(writer, http.StatusOK, tpiResponse)
}

/*
verifyTPIRequestData verifies 'TPI' request data.
It performs several checks on the request data to ensure its validity.
*/
func verifyTPIRequestData(request *http.Request, tpiRequest TPIRequest) error {
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
	if tpiRequest.Type != TypeTPIRequest {
		return fmt.Errorf("unexpected request Type [%v]", tpiRequest.Type)
	}

	// verify ID
	if len(tpiRequest.ID) > 1024 {
		return errors.New("ID must be 0-1024 characters long")
	}

	// verify coordinates (either utm or lon/lat coordinates must be set)
	if tpiRequest.Attributes.Zone == 0 && tpiRequest.Attributes.Longitude == 0 {
		return errors.New("either utm or lon/lat coordinates must be set")
	}

	// verify zone for Germany (Zone: 32 or 33)
	if tpiRequest.Attributes.Zone != 0 {
		if tpiRequest.Attributes.Zone < 32 || tpiRequest.Attributes.Zone > 33 {
			return errors.New("invalid zone for Germany")
		}
	}

	// verify longitude for Germany (Longitude: from  5.8663° E to 15.0419° E)
	if tpiRequest.Attributes.Longitude != 0 {
		if tpiRequest.Attributes.Longitude > 15.3 || tpiRequest.Attributes.Longitude < 5.5 {
			return errors.New("invalid longitude for Germany")
		}
	}

	// verify latitude for Germany (Latitude: from 47.2701° N to 55.0586° N)
	if tpiRequest.Attributes.Latitude != 0 {
		if tpiRequest.Attributes.Latitude > 55.3 || tpiRequest.Attributes.Latitude < 47.0 {
			return errors.New("invalid latitude for Germany")
		}
	}

	// verify 'color text file content'
	err := verifyColorTextFileContent(tpiRequest.Attributes.ColorTextFileContent)
	if err != nil {
		return errors.New("invalid color text file content (%w)")
	}

	// verify coloring algorithm
	if tpiRequest.Attributes.ColoringAlgorithm != "" {
		if !(tpiRequest.Attributes.ColoringAlgorithm == "interpolation" || tpiRequest.Attributes.ColoringAlgorithm == "rounding") {
			return errors.New("unsupported coloring algorithm (not 'interpolation' or 'rounding')")
		}
	}

	return nil
}

/*
buildTPIResponse builds HTTP responses with specified status and body.
It sets the Content-Type and Content-Length headers before writing the response body.
This function is used to construct consistent HTTP responses throughout the application.
*/
func buildTPIResponse(writer http.ResponseWriter, httpStatus int, tpiResponse TPIResponse) {
	// log limit length of body (e.g., the tpi objects as part of the body can be very large)
	maxBodyLength := 1024

	// CORS: allow requests from any origin
	writer.Header().Set("Access-Control-Allow-Origin", "*")
	// CORS: allowed methods
	writer.Header().Set("Access-Control-Allow-Methods", "POST")
	// CORS: allowed headers
	writer.Header().Set("Access-Control-Allow-Headers", "Content-Type")

	// marshal response
	body, err := json.MarshalIndent(tpiResponse, "", "  ")
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
generateTPIObjectForTile builds tpi object for given tile index.
*/
func generateTPIObjectForTile(tile TileMetadata, outputFormat string, colorTextFileContent []string, coloringAlgorithm string) (TPI, error) {
	var tpi TPI
	var boundingBox WGS84BoundingBox

	// run operations in temp directory
	tempDir, err := os.MkdirTemp("", "dtm-elevation-service-tpi-")
	if err != nil {
		return tpi, fmt.Errorf("error [%w] at os.MkdirTemp()", err)
	}
	defer func() {
		_ = os.RemoveAll(tempDir)
	}()

	// create 'color-text-file' for 'gdaldem color-relief' in temp directory
	colorTextFile := filepath.Join(tempDir, "color-text-file.txt")
	err = createColorTextFile(colorTextFile, colorTextFileContent)
	if err != nil {
		return tpi, fmt.Errorf("error [%w] creating 'color-text-file'", err)
	}

	inputGeoTIFF := tile.Path
	tpiUTMGeoTIFF := filepath.Join(tempDir, tile.Index+".tpi.utm.tif")
	tpiColorUTMGeoTIFF := filepath.Join(tempDir, tile.Index+".tpi.color.utm.tif")
	tpiWebmercatorGeoTIFF := filepath.Join(tempDir, tile.Index+".tpi.webmercator.tif")
	tpiColorWebmercatoPNG := filepath.Join(tempDir, tile.Index+".tpi.color.webmercator.png")

	// 1. create native tpi with 'gdaldem tpi'
	commandExitStatus, commandOutput, err := runCommand("gdaldem", []string{"TPI", inputGeoTIFF, tpiUTMGeoTIFF, "-compute_edges"})
	if err != nil {
		return tpi, fmt.Errorf("error [%w: %d - %s] at runCommand()", err, commandExitStatus, commandOutput)
	}
	// fmt.Printf("commandExitStatus: %d\n", commandExitStatus)
	// fmt.Printf("commandOutput: %s\n", commandOutput)

	var data []byte
	switch strings.ToLower(outputFormat) {
	case "geotiff":
		// 2. colorize tpi with 'gdaldem color-relief'
		options := []string{"color-relief", tpiUTMGeoTIFF, colorTextFile, tpiColorUTMGeoTIFF, "-alpha"}
		if coloringAlgorithm == "rounding" {
			options = append(options, "-nearest_color_entry")
		}
		commandExitStatus, commandOutput, err = runCommand("gdaldem", options)
		if err != nil {
			return tpi, fmt.Errorf("error [%w: %d - %s] at runCommand()", err, commandExitStatus, commandOutput)
		}
		// fmt.Printf("commandExitStatus: %d\n", commandExitStatus)
		// fmt.Printf("commandOutput: %s\n", commandOutput)

		data, err = os.ReadFile(tpiColorUTMGeoTIFF)
		if err != nil {
			return tpi, fmt.Errorf("error [%w] at os.ReadFile()", err)
		}

	case "png":
		// 2. convert UTM (EPSG:25832/EPSG:25833) to Webmercator (EPSG:3857) with 'gdalwarp'
		commandExitStatus, commandOutput, err = runCommand("gdalwarp", []string{"-t_srs", "EPSG:3857", tpiUTMGeoTIFF, tpiWebmercatorGeoTIFF})
		if err != nil {
			return tpi, fmt.Errorf("error [%w: %d - %s] at runCommand()", err, commandExitStatus, commandOutput)
		}
		// fmt.Printf("commandExitStatus: %d\n", commandExitStatus)
		// fmt.Printf("commandOutput: %s\n", commandOutput)

		// 3. colorize TPI with 'gdaldem color-relief' (creates PNG file)
		options := []string{"color-relief", tpiWebmercatorGeoTIFF, colorTextFile, tpiColorWebmercatoPNG, "-alpha"}
		if coloringAlgorithm == "rounding" {
			options = append(options, "-nearest_color_entry")
		}
		commandExitStatus, commandOutput, err = runCommand("gdaldem", options)
		if err != nil {
			return tpi, fmt.Errorf("error [%w: %d - %s] at runCommand()", err, commandExitStatus, commandOutput)
		}
		// fmt.Printf("commandExitStatus: %d\n", commandExitStatus)
		// fmt.Printf("commandOutput: %s\n", commandOutput)

		// 4. get bounding box (in wgs84) for webmercator tif (georeference of webmercator png )
		boundingBox, err = calculateWGS84BoundingBox(tile.Path)
		if err != nil {
			return tpi, fmt.Errorf("error [%w] at calculateWGS84BoundingBox(), file: %s", err, tile.Path)
		}

		// read result file
		data, err = os.ReadFile(tpiColorWebmercatoPNG)
		if err != nil {
			return tpi, fmt.Errorf("error [%w] at os.ReadFile()", err)
		}

	default:
		return tpi, fmt.Errorf("unsupported format [%s]", outputFormat)
	}

	// set TPI return structure
	tpi.Data = data
	tpi.DataFormat = outputFormat
	tpi.Actuality = tile.Actuality
	tpi.Origin = tile.Source
	tpi.TileIndex = tile.Index
	tpi.BoundingBox = boundingBox // only relevant for PNG

	// get attribution for resource
	attribution := "unknown"
	resource, err := getElevationResource(tile.Source)
	if err != nil {
		slog.Error("tpi request: error getting elevation resource", "error", err, "source", tile.Source)
	} else {
		attribution = resource.Attribution
	}
	tpi.Attribution = attribution

	return tpi, nil
}
