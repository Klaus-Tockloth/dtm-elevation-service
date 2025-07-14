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
slopeRequest handles 'slope request' from client.
*/
func slopeRequest(writer http.ResponseWriter, request *http.Request) {
	var slopeResponse = SlopeResponse{Type: TypeSlopeResponse, ID: "unknown"}
	slopeResponse.Attributes.IsError = true

	// statistics
	atomic.AddUint64(&SlopeRequests, 1)

	// limit overall request body size
	request.Body = http.MaxBytesReader(writer, request.Body, MaxSlopeRequestBodySize)

	// read request
	bodyData, err := io.ReadAll(request.Body)
	if err != nil {
		// check specifically for the error returned by MaxBytesReader
		var maxBytesErr *http.MaxBytesError
		if errors.As(err, &maxBytesErr) {
			slog.Warn("slope request: request body too large", "limit", maxBytesErr.Limit, "ID", "unknown")
			slopeResponse.Attributes.Error.Code = "6000"
			slopeResponse.Attributes.Error.Title = "request body too large"
			slopeResponse.Attributes.Error.Detail = fmt.Sprintf("request body exceeds limit of %d bytes", maxBytesErr.Limit)
			buildSlopeResponse(writer, http.StatusRequestEntityTooLarge, slopeResponse)
		} else {
			// handle other read errors
			slog.Warn("slope request: error reading request body", "error", err, "ID", "unknown")
			slopeResponse.Attributes.Error.Code = "6020"
			slopeResponse.Attributes.Error.Title = "error reading request body"
			slopeResponse.Attributes.Error.Detail = err.Error()
			buildSlopeResponse(writer, http.StatusBadRequest, slopeResponse)
		}
		return
	}

	// unmarshal request
	slopeRequest := SlopeRequest{}
	err = json.Unmarshal(bodyData, &slopeRequest)
	if err != nil {
		slog.Warn("slope request: error unmarshaling request body", "error", err, "ID", "unknown")
		slopeResponse.Attributes.Error.Code = "6040"
		slopeResponse.Attributes.Error.Title = "error unmarshaling request body"
		slopeResponse.Attributes.Error.Detail = err.Error()
		buildSlopeResponse(writer, http.StatusBadRequest, slopeResponse)
		return
	}

	// verify request data
	err = verifySlopeRequestData(request, slopeRequest)
	if err != nil {
		slog.Warn("slope request: error verifying request data", "error", err, "ID", slopeRequest.ID)
		slopeResponse.Attributes.Error.Code = "6060"
		slopeResponse.Attributes.Error.Title = "error verifying request data"
		slopeResponse.Attributes.Error.Detail = err.Error()
		buildSlopeResponse(writer, http.StatusBadRequest, slopeResponse)
		return
	}

	zone := 0
	easting := 0.0
	northing := 0.0
	longitude := 0.0
	latitude := 0.0
	var tile TileMetadata
	var tiles []TileMetadata
	var outputFormat string

	// determine type of coordinates
	if slopeRequest.Attributes.Zone != 0 {
		// input from UTM coordinates
		zone = slopeRequest.Attributes.Zone
		easting = slopeRequest.Attributes.Easting
		northing = slopeRequest.Attributes.Northing
		outputFormat = "geotiff"

		// get tile metadata for primary tile (e.g. "32_507_5491")
		tile, err = getGeotiffTile(easting, northing, zone, 1)
		if err != nil {
			slog.Warn("slope request: error getting GeoTIFF tile for UTM coordinates", "error", err,
				"easting", easting, "northing", northing, "zone", zone, "ID", slopeRequest.ID)
			slopeResponse.Attributes.Error.Code = "6080"
			slopeResponse.Attributes.Error.Title = "getting GeoTIFF tile for UTM coordinates"
			slopeResponse.Attributes.Error.Detail = err.Error()
			buildSlopeResponse(writer, http.StatusBadRequest, slopeResponse)
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
		longitude = slopeRequest.Attributes.Longitude
		latitude = slopeRequest.Attributes.Latitude
		outputFormat = "png"

		// get tile metadata for primary tile (e.g. "32_507_5491")
		tile, zone, easting, northing, err = getTileUTM(longitude, latitude)
		if err != nil {
			err = fmt.Errorf("error [%w] getting tile for coordinates lon: %.8f, lat: %.8f", err, longitude, latitude)
			slog.Warn("slope request: error getting GeoTIFF tile for lon/lat coordinates", "error", err,
				"longitude", longitude, "latitude", latitude, "ID", slopeRequest.ID)
			slopeResponse.Attributes.Error.Code = "6100"
			slopeResponse.Attributes.Error.Title = "getting GeoTIFF tile for lon/lat coordinates"
			slopeResponse.Attributes.Error.Detail = err.Error()
			buildSlopeResponse(writer, http.StatusBadRequest, slopeResponse)
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

	// build slope for all existing tiles
	for _, tile := range tiles {
		slope, err := generateSlopeObjectForTile(tile, outputFormat, slopeRequest.Attributes.GradientAlgorithm, slopeRequest.Attributes.ColorTextFileContent)
		if err != nil {
			slog.Warn("slope request: error generating slope object for tile", "error", err, "ID", slopeRequest.ID)
			slopeResponse.Attributes.Error.Code = "6120"
			slopeResponse.Attributes.Error.Title = "error generating slope object for tile"
			slopeResponse.Attributes.Error.Detail = err.Error()
			buildSlopeResponse(writer, http.StatusBadRequest, slopeResponse)
			return
		}
		slopeResponse.Attributes.Slopes = append(slopeResponse.Attributes.Slopes, slope)
	}

	// copy request parameters into response
	slopeResponse.ID = slopeRequest.ID
	slopeResponse.Attributes.IsError = false
	slopeResponse.Attributes.Zone = slopeRequest.Attributes.Zone
	slopeResponse.Attributes.Easting = slopeRequest.Attributes.Easting
	slopeResponse.Attributes.Northing = slopeRequest.Attributes.Northing
	slopeResponse.Attributes.Longitude = slopeRequest.Attributes.Longitude
	slopeResponse.Attributes.Latitude = slopeRequest.Attributes.Latitude
	slopeResponse.Attributes.GradientAlgorithm = slopeRequest.Attributes.GradientAlgorithm
	slopeResponse.Attributes.ColorTextFileContent = slopeRequest.Attributes.ColorTextFileContent

	// success response
	buildSlopeResponse(writer, http.StatusOK, slopeResponse)
}

/*
verifySlopeRequestData verifies 'slope' request data.
It performs several checks on the request data to ensure its validity.
*/
func verifySlopeRequestData(request *http.Request, slopeRequest SlopeRequest) error {
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
	if slopeRequest.Type != TypeSlopeRequest {
		return fmt.Errorf("unexpected request Type [%v]", slopeRequest.Type)
	}

	// verify ID
	if len(slopeRequest.ID) > 1024 {
		return errors.New("ID must be 0-1024 characters long")
	}

	// verify coordinates (either utm or lon/lat coordinates must be set)
	if slopeRequest.Attributes.Zone == 0 && slopeRequest.Attributes.Longitude == 0 {
		return errors.New("either utm or lon/lat coordinates must be set")
	}

	// verify zone for Germany (Zone: 32 or 33)
	if slopeRequest.Attributes.Zone != 0 {
		if slopeRequest.Attributes.Zone < 32 || slopeRequest.Attributes.Zone > 33 {
			return errors.New("invalid zone for Germany")
		}
	}

	// verify longitude for Germany (Longitude: from  5.8663° E to 15.0419° E)
	if slopeRequest.Attributes.Longitude != 0 {
		if slopeRequest.Attributes.Longitude > 15.3 || slopeRequest.Attributes.Longitude < 5.5 {
			return errors.New("invalid longitude for Germany")
		}
	}

	// verify latitude for Germany (Latitude: from 47.2701° N to 55.0586° N)
	if slopeRequest.Attributes.Latitude != 0 {
		if slopeRequest.Attributes.Latitude > 55.3 || slopeRequest.Attributes.Latitude < 47.0 {
			return errors.New("invalid latitude for Germany")
		}
	}

	// verify gradient algorithm
	if !(slopeRequest.Attributes.GradientAlgorithm == "Horn" || slopeRequest.Attributes.GradientAlgorithm == "ZevenbergenThorne") {
		return errors.New("unsupported gradient algorithm (not Horn or ZevenbergenThorne)")
	}

	// verify 'color text file content'
	err := verifyColorTextFileContent(slopeRequest.Attributes.ColorTextFileContent)
	if err != nil {
		return errors.New("invalid color text file content (%w)")
	}

	return nil
}

/*
buildSlopeResponse builds HTTP responses with specified status and body.
It sets the Content-Type and Content-Length headers before writing the response body.
This function is used to construct consistent HTTP responses throughout the application.
*/
func buildSlopeResponse(writer http.ResponseWriter, httpStatus int, slopeResponse SlopeResponse) {
	// log limit length of body (e.g., the slope objects as part of the body can be very large)
	maxBodyLength := 1024

	// CORS: allow requests from any origin
	writer.Header().Set("Access-Control-Allow-Origin", "*")
	// CORS: allowed methods
	writer.Header().Set("Access-Control-Allow-Methods", "POST")
	// CORS: allowed headers
	writer.Header().Set("Access-Control-Allow-Headers", "Content-Type")

	// marshal response
	body, err := json.MarshalIndent(slopeResponse, "", "  ")
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
generateSlopeObjectForTile builds slope object for given tile index.
*/
func generateSlopeObjectForTile(tile TileMetadata, outputFormat string, gradientAlgorithm string, colorTextFileContent []string) (Slope, error) {
	var slope Slope
	var boundingBox WGS84BoundingBox

	// run operations in temp directory
	tempDir, err := os.MkdirTemp("", "dtm-elevation-service-slope-")
	if err != nil {
		return slope, fmt.Errorf("error [%w] at os.MkdirTemp()", err)
	}
	defer func() {
		_ = os.RemoveAll(tempDir)
	}()

	// create 'color-text-file' for 'gdaldem color-relief' in temp directory
	colorTextFile := filepath.Join(tempDir, "color-text-file.txt")
	err = createColorTextFile(colorTextFile, colorTextFileContent)
	if err != nil {
		return slope, fmt.Errorf("error [%w] creating 'color-text-file'", err)
	}

	inputGeoTIFF := tile.Path
	slopeUTMGeoTIFF := filepath.Join(tempDir, tile.Index+".slope.utm.tif")
	slopeColorUTMGeoTIFF := filepath.Join(tempDir, tile.Index+".slope.color.utm.tif")
	slopeWebmercatorGeoTIFF := filepath.Join(tempDir, tile.Index+".slope.webmercator.tif")
	slopeColorWebmercatoPNG := filepath.Join(tempDir, tile.Index+".slope.color.webmercator.png")

	// 1. create native slope with 'gdaldem slope'
	// e.g. gdaldem slope dgm1_32_497_5670_1_he.tif 32_497_5670_hangneigung.utm.tif -alg Horn -compute_edges
	commandExitStatus, commandOutput, err := runCommand("gdaldem", []string{"slope", inputGeoTIFF, slopeUTMGeoTIFF, "-alg", gradientAlgorithm, "-compute_edges"})
	if err != nil {
		return slope, fmt.Errorf("error [%w: %d - %s] at runCommand()", err, commandExitStatus, commandOutput)
	}
	// fmt.Printf("commandExitStatus: %d\n", commandExitStatus)
	// fmt.Printf("commandOutput: %s\n", commandOutput)

	var data []byte
	switch strings.ToLower(outputFormat) {
	case "geotiff":
		// 2. colorize slope with 'gdaldem color-relief'
		commandExitStatus, commandOutput, err = runCommand("gdaldem", []string{"color-relief", slopeUTMGeoTIFF, colorTextFile, slopeColorUTMGeoTIFF, "-alpha", "-nearest_color_entry"})
		if err != nil {
			return slope, fmt.Errorf("error [%w: %d - %s] at runCommand()", err, commandExitStatus, commandOutput)
		}
		// fmt.Printf("commandExitStatus: %d\n", commandExitStatus)
		// fmt.Printf("commandOutput: %s\n", commandOutput)

		data, err = os.ReadFile(slopeColorUTMGeoTIFF)
		if err != nil {
			return slope, fmt.Errorf("error [%w] at os.ReadFile()", err)
		}

	case "png":
		// 2. convert UTM (EPSG:25832/EPSG:25833) to Webmercator (EPSG:3857) with 'gdalwarp'
		// e.g. gdalwarp -t_srs EPSG:3857 32_497_5670_hangneigung.utm.tif 32_497_5670_hangneigung.webmercator.tif
		commandExitStatus, commandOutput, err = runCommand("gdalwarp", []string{"-t_srs", "EPSG:3857", slopeUTMGeoTIFF, slopeWebmercatorGeoTIFF})
		if err != nil {
			return slope, fmt.Errorf("error [%w: %d - %s] at runCommand()", err, commandExitStatus, commandOutput)
		}
		// fmt.Printf("commandExitStatus: %d\n", commandExitStatus)
		// fmt.Printf("commandOutput: %s\n", commandOutput)

		// 3. colorize slope with 'gdaldem color-relief' (creates PNG file)
		// e.g. gdaldem color-relief 32_497_5670_hangneigung.webmercator.tif slope-colors.txt 32_497_5670_hangneigung.webmercator.png -alpha -nearest_color_entry
		commandExitStatus, commandOutput, err = runCommand("gdaldem", []string{"color-relief", slopeWebmercatorGeoTIFF, colorTextFile, slopeColorWebmercatoPNG, "-alpha", "-nearest_color_entry"})
		if err != nil {
			return slope, fmt.Errorf("error [%w: %d - %s] at runCommand()", err, commandExitStatus, commandOutput)
		}
		// fmt.Printf("commandExitStatus: %d\n", commandExitStatus)
		// fmt.Printf("commandOutput: %s\n", commandOutput)

		// 4. get bounding box (in wgs84) for webmercator tif (georeference of webmercator png )
		boundingBox, err = calculateWGS84BoundingBox(tile.Path)
		if err != nil {
			return slope, fmt.Errorf("error [%w] at calculateWGS84BoundingBox(), file: %s", err, tile.Path)
		}

		// read result file
		data, err = os.ReadFile(slopeColorWebmercatoPNG)
		if err != nil {
			return slope, fmt.Errorf("error [%w] at os.ReadFile()", err)
		}

	default:
		return slope, fmt.Errorf("unsupported format [%s]", outputFormat)
	}

	// set contour return structure
	slope.Data = data
	slope.DataFormat = outputFormat
	slope.Actuality = tile.Actuality
	slope.Origin = tile.Source
	slope.TileIndex = tile.Index
	slope.BoundingBox = boundingBox // only relevant for PNG

	// get attribution for resource
	attribution := "unknown"
	resource, err := getElevationResource(tile.Source)
	if err != nil {
		slog.Error("slope request: error getting elevation resource", "error", err, "source", tile.Source)
	} else {
		attribution = resource.Attribution
	}
	slope.Attribution = attribution

	return slope, nil
}
