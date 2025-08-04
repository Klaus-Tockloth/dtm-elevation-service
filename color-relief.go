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
colorReliefRequest handles 'colorrelief request' from client.
*/
func colorReliefRequest(writer http.ResponseWriter, request *http.Request) {
	var colorReliefResponse = ColorReliefResponse{Type: TypeColorReliefResponse, ID: "unknown"}
	colorReliefResponse.Attributes.IsError = true

	// statistics
	atomic.AddUint64(&ColorReliefRequests, 1)

	// limit overall request body size
	request.Body = http.MaxBytesReader(writer, request.Body, MaxColorReliefRequestBodySize)

	// read request
	bodyData, err := io.ReadAll(request.Body)
	if err != nil {
		// check specifically for the error returned by MaxBytesReader
		var maxBytesErr *http.MaxBytesError
		if errors.As(err, &maxBytesErr) {
			slog.Warn("color relief request: request body too large", "limit", maxBytesErr.Limit, "ID", "unknown")
			colorReliefResponse.Attributes.Error.Code = "12000"
			colorReliefResponse.Attributes.Error.Title = "request body too large"
			colorReliefResponse.Attributes.Error.Detail = fmt.Sprintf("request body exceeds limit of %d bytes", maxBytesErr.Limit)
			buildColorReliefResponse(writer, http.StatusRequestEntityTooLarge, colorReliefResponse)
		} else {
			// handle other read errors
			slog.Warn("color relief request: error reading request body", "error", err, "ID", "unknown")
			colorReliefResponse.Attributes.Error.Code = "12020"
			colorReliefResponse.Attributes.Error.Title = "error reading request body"
			colorReliefResponse.Attributes.Error.Detail = err.Error()
			buildColorReliefResponse(writer, http.StatusBadRequest, colorReliefResponse)
		}
		return
	}

	// unmarshal request
	colorReliefRequest := ColorReliefRequest{}
	err = json.Unmarshal(bodyData, &colorReliefRequest)
	if err != nil {
		slog.Warn("color relief request: error unmarshaling request body", "error", err, "ID", "unknown")
		colorReliefResponse.Attributes.Error.Code = "12040"
		colorReliefResponse.Attributes.Error.Title = "error unmarshaling request body"
		colorReliefResponse.Attributes.Error.Detail = err.Error()
		buildColorReliefResponse(writer, http.StatusBadRequest, colorReliefResponse)
		return
	}

	// verify request data
	err = verifyColorReliefRequestData(request, colorReliefRequest)
	if err != nil {
		slog.Warn("color relief request: error verifying request data", "error", err, "ID", colorReliefRequest.ID)
		colorReliefResponse.Attributes.Error.Code = "12060"
		colorReliefResponse.Attributes.Error.Title = "error verifying request data"
		colorReliefResponse.Attributes.Error.Detail = err.Error()
		buildColorReliefResponse(writer, http.StatusBadRequest, colorReliefResponse)
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
	if colorReliefRequest.Attributes.Zone != 0 {
		// input from UTM coordinates
		zone = colorReliefRequest.Attributes.Zone
		easting = colorReliefRequest.Attributes.Easting
		northing = colorReliefRequest.Attributes.Northing
		outputFormat = "geotiff"

		// get tile metadata for primary tile (e.g. "32_507_5491")
		tile, err = getGeotiffTile(easting, northing, zone, 1)
		if err != nil {
			slog.Warn("color relief request: error getting GeoTIFF tile for UTM coordinates", "error", err,
				"easting", easting, "northing", northing, "zone", zone, "ID", colorReliefRequest.ID)
			colorReliefResponse.Attributes.Error.Code = "12080"
			colorReliefResponse.Attributes.Error.Title = "getting GeoTIFF tile for UTM coordinates"
			colorReliefResponse.Attributes.Error.Detail = err.Error()
			buildColorReliefResponse(writer, http.StatusBadRequest, colorReliefResponse)
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
		longitude = colorReliefRequest.Attributes.Longitude
		latitude = colorReliefRequest.Attributes.Latitude
		outputFormat = "png"

		// get tile metadata for primary tile (e.g. "32_507_5491")
		tile, zone, easting, northing, err = getTileUTM(longitude, latitude)
		if err != nil {
			err = fmt.Errorf("error [%w] getting tile for coordinates lon: %.8f, lat: %.8f", err, longitude, latitude)
			slog.Warn("color relief request: error getting GeoTIFF tile for lon/lat coordinates", "error", err,
				"longitude", longitude, "latitude", latitude, "ID", colorReliefRequest.ID)
			colorReliefResponse.Attributes.Error.Code = "12100"
			colorReliefResponse.Attributes.Error.Title = "getting GeoTIFF tile for lon/lat coordinates"
			colorReliefResponse.Attributes.Error.Detail = err.Error()
			buildColorReliefResponse(writer, http.StatusBadRequest, colorReliefResponse)
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

	// build colorRelief for all existing tiles
	for _, tile := range tiles {
		colorRelief, err := generateColorReliefObjectForTile(tile, outputFormat, colorReliefRequest.Attributes.ColorTextFileContent, colorReliefRequest.Attributes.ColoringAlgorithm)
		if err != nil {
			slog.Warn("color relief request: error generating colorRelief object for tile", "error", err, "ID", colorReliefRequest.ID)
			colorReliefResponse.Attributes.Error.Code = "12120"
			colorReliefResponse.Attributes.Error.Title = "error generating colorRelief object for tile"
			colorReliefResponse.Attributes.Error.Detail = err.Error()
			buildColorReliefResponse(writer, http.StatusBadRequest, colorReliefResponse)
			return
		}
		colorReliefResponse.Attributes.ColorReliefs = append(colorReliefResponse.Attributes.ColorReliefs, colorRelief)
	}

	// copy request parameters into response
	colorReliefResponse.ID = colorReliefRequest.ID
	colorReliefResponse.Attributes.IsError = false
	colorReliefResponse.Attributes.Zone = colorReliefRequest.Attributes.Zone
	colorReliefResponse.Attributes.Easting = colorReliefRequest.Attributes.Easting
	colorReliefResponse.Attributes.Northing = colorReliefRequest.Attributes.Northing
	colorReliefResponse.Attributes.Longitude = colorReliefRequest.Attributes.Longitude
	colorReliefResponse.Attributes.Latitude = colorReliefRequest.Attributes.Latitude
	colorReliefResponse.Attributes.ColorTextFileContent = colorReliefRequest.Attributes.ColorTextFileContent
	colorReliefResponse.Attributes.ColoringAlgorithm = colorReliefRequest.Attributes.ColoringAlgorithm

	// success response
	buildColorReliefResponse(writer, http.StatusOK, colorReliefResponse)
}

/*
verifyColorReliefRequestData verifies 'ColorRelief' request data.
It performs several checks on the request data to ensure its validity.
*/
func verifyColorReliefRequestData(request *http.Request, colorReliefRequest ColorReliefRequest) error {
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
	if colorReliefRequest.Type != TypeColorReliefRequest {
		return fmt.Errorf("unexpected request Type [%v]", colorReliefRequest.Type)
	}

	// verify ID
	if len(colorReliefRequest.ID) > 1024 {
		return errors.New("ID must be 0-1024 characters long")
	}

	// verify coordinates (either utm or lon/lat coordinates must be set)
	if colorReliefRequest.Attributes.Zone == 0 && colorReliefRequest.Attributes.Longitude == 0 {
		return errors.New("either utm or lon/lat coordinates must be set")
	}

	// verify zone for Germany (Zone: 32 or 33)
	if colorReliefRequest.Attributes.Zone != 0 {
		if colorReliefRequest.Attributes.Zone < 32 || colorReliefRequest.Attributes.Zone > 33 {
			return errors.New("invalid zone for Germany")
		}
	}

	// verify longitude for Germany (Longitude: from  5.8663° E to 15.0419° E)
	if colorReliefRequest.Attributes.Longitude != 0 {
		if colorReliefRequest.Attributes.Longitude > 15.3 || colorReliefRequest.Attributes.Longitude < 5.5 {
			return errors.New("invalid longitude for Germany")
		}
	}

	// verify latitude for Germany (Latitude: from 47.2701° N to 55.0586° N)
	if colorReliefRequest.Attributes.Latitude != 0 {
		if colorReliefRequest.Attributes.Latitude > 55.3 || colorReliefRequest.Attributes.Latitude < 47.0 {
			return errors.New("invalid latitude for Germany")
		}
	}

	// verify 'color text file content'
	err := verifyColorTextFileContent(colorReliefRequest.Attributes.ColorTextFileContent)
	if err != nil {
		return errors.New("invalid color text file content (%w)")
	}

	// verify coloring algorithm
	if colorReliefRequest.Attributes.ColoringAlgorithm != "" {
		if !(colorReliefRequest.Attributes.ColoringAlgorithm == "interpolation" || colorReliefRequest.Attributes.ColoringAlgorithm == "rounding") {
			return errors.New("unsupported coloring algorithm (not 'interpolation' or 'rounding')")
		}
	}

	return nil
}

/*
buildColorReliefResponse builds HTTP responses with specified status and body.
It sets the Content-Type and Content-Length headers before writing the response body.
This function is used to construct consistent HTTP responses throughout the application.
*/
func buildColorReliefResponse(writer http.ResponseWriter, httpStatus int, colorReliefResponse ColorReliefResponse) {
	// log limit length of body (e.g., the colorRelief objects as part of the body can be very large)
	maxBodyLength := 1024

	// CORS: allow requests from any origin
	writer.Header().Set("Access-Control-Allow-Origin", "*")
	// CORS: allowed methods
	writer.Header().Set("Access-Control-Allow-Methods", "POST")
	// CORS: allowed headers
	writer.Header().Set("Access-Control-Allow-Headers", "Content-Type")

	// marshal response
	body, err := json.MarshalIndent(colorReliefResponse, "", "  ")
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
generateColorReliefObjectForTile builds colorRelief object for given tile index.
*/
func generateColorReliefObjectForTile(tile TileMetadata, outputFormat string, colorTextFileContent []string, coloringAlgorithm string) (ColorRelief, error) {
	var colorRelief ColorRelief
	var boundingBox WGS84BoundingBox

	// run operations in temp directory
	tempDir, err := os.MkdirTemp("", "dtm-elevation-service-color-relief-")
	if err != nil {
		return colorRelief, fmt.Errorf("error [%w] at os.MkdirTemp()", err)
	}
	defer func() {
		_ = os.RemoveAll(tempDir)
	}()

	// create 'color-text-file' for 'gdaldem color-relief' in temp directory
	colorTextFile := filepath.Join(tempDir, "color-text-file.txt")
	err = createColorTextFile(colorTextFile, colorTextFileContent)
	if err != nil {
		return colorRelief, fmt.Errorf("error [%w] creating 'color-text-file'", err)
	}

	inputGeoTIFF := tile.Path
	colorReliefColorUTMGeoTIFF := filepath.Join(tempDir, tile.Index+".color-relief.color.utm.tif")
	colorReliefWebmercatorGeoTIFF := filepath.Join(tempDir, tile.Index+".color-relief.webmercator.tif")
	colorReliefColorWebmercatoPNG := filepath.Join(tempDir, tile.Index+".color-relief.color.webmercator.png")
	var data []byte
	switch strings.ToLower(outputFormat) {
	case "geotiff":
		options := []string{"color-relief", inputGeoTIFF, colorTextFile, colorReliefColorUTMGeoTIFF, "-alpha"}
		if coloringAlgorithm == "rounding" {
			options = append(options, "-nearest_color_entry")
		}
		commandExitStatus, commandOutput, err := runCommand("gdaldem", options)
		if err != nil {
			return colorRelief, fmt.Errorf("error [%w: %d - %s] at runCommand()", err, commandExitStatus, commandOutput)
		}
		// fmt.Printf("commandExitStatus: %d\n", commandExitStatus)
		// fmt.Printf("commandOutput: %s\n", commandOutput)

		data, err = os.ReadFile(colorReliefColorUTMGeoTIFF)
		if err != nil {
			return colorRelief, fmt.Errorf("error [%w] at os.ReadFile()", err)
		}

	case "png":
		commandExitStatus, commandOutput, err := runCommand("gdalwarp", []string{"-t_srs", "EPSG:3857", inputGeoTIFF, colorReliefWebmercatorGeoTIFF})
		if err != nil {
			return colorRelief, fmt.Errorf("error [%w: %d - %s] at runCommand()", err, commandExitStatus, commandOutput)
		}
		// fmt.Printf("commandExitStatus: %d\n", commandExitStatus)
		// fmt.Printf("commandOutput: %s\n", commandOutput)

		options := []string{"color-relief", colorReliefWebmercatorGeoTIFF, colorTextFile, colorReliefColorWebmercatoPNG, "-alpha"}
		if coloringAlgorithm == "rounding" {
			options = append(options, "-nearest_color_entry")
		}
		commandExitStatus, commandOutput, err = runCommand("gdaldem", options)
		if err != nil {
			return colorRelief, fmt.Errorf("error [%w: %d - %s] at runCommand()", err, commandExitStatus, commandOutput)
		}
		// fmt.Printf("commandExitStatus: %d\n", commandExitStatus)
		// fmt.Printf("commandOutput: %s\n", commandOutput)

		// 4. get bounding box (in wgs84) for webmercator tif (georeference of webmercator png )
		boundingBox, err = calculateWGS84BoundingBox(tile.Path)
		if err != nil {
			return colorRelief, fmt.Errorf("error [%w] at calculateWGS84BoundingBox(), file: %s", err, tile.Path)
		}

		// read result file
		data, err = os.ReadFile(colorReliefColorWebmercatoPNG)
		if err != nil {
			return colorRelief, fmt.Errorf("error [%w] at os.ReadFile()", err)
		}

	default:
		return colorRelief, fmt.Errorf("unsupported format [%s]", outputFormat)
	}

	// set contour return structure
	colorRelief.Data = data
	colorRelief.DataFormat = outputFormat
	colorRelief.Actuality = tile.Actuality
	colorRelief.Origin = tile.Source
	colorRelief.TileIndex = tile.Index
	colorRelief.BoundingBox = boundingBox // only relevant for PNG

	// get attribution for resource
	attribution := "unknown"
	resource, err := getElevationResource(tile.Source)
	if err != nil {
		slog.Error("color reliefrequest: error getting elevation resource", "error", err, "source", tile.Source)
	} else {
		attribution = resource.Attribution
	}
	colorRelief.Attribution = attribution

	return colorRelief, nil
}
