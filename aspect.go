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
aspectRequest handles 'aspect request' from client.
*/
func aspectRequest(writer http.ResponseWriter, request *http.Request) {
	var aspectResponse = AspectResponse{Type: TypeAspectResponse, ID: "unknown"}
	aspectResponse.Attributes.IsError = true

	// statistics
	atomic.AddUint64(&AspectRequests, 1)

	// limit overall request body size
	request.Body = http.MaxBytesReader(writer, request.Body, MaxAspectRequestBodySize)

	// read request
	bodyData, err := io.ReadAll(request.Body)
	if err != nil {
		// check specifically for the error returned by MaxBytesReader
		var maxBytesErr *http.MaxBytesError
		if errors.As(err, &maxBytesErr) {
			slog.Warn("aspect request: request body too large", "limit", maxBytesErr.Limit, "ID", "unknown")
			aspectResponse.Attributes.Error.Code = "7000"
			aspectResponse.Attributes.Error.Title = "request body too large"
			aspectResponse.Attributes.Error.Detail = fmt.Sprintf("request body exceeds limit of %d bytes", maxBytesErr.Limit)
			buildAspectResponse(writer, http.StatusRequestEntityTooLarge, aspectResponse)
		} else {
			// handle other read errors
			slog.Warn("aspect request: error reading request body", "error", err, "ID", "unknown")
			aspectResponse.Attributes.Error.Code = "7020"
			aspectResponse.Attributes.Error.Title = "error reading request body"
			aspectResponse.Attributes.Error.Detail = err.Error()
			buildAspectResponse(writer, http.StatusBadRequest, aspectResponse)
		}
		return
	}

	// unmarshal request
	aspectRequest := AspectRequest{}
	err = json.Unmarshal(bodyData, &aspectRequest)
	if err != nil {
		slog.Warn("aspect request: error unmarshaling request body", "error", err, "ID", "unknown")
		aspectResponse.Attributes.Error.Code = "7040"
		aspectResponse.Attributes.Error.Title = "error unmarshaling request body"
		aspectResponse.Attributes.Error.Detail = err.Error()
		buildAspectResponse(writer, http.StatusBadRequest, aspectResponse)
		return
	}

	// verify request data
	err = verifyAspectRequestData(request, aspectRequest)
	if err != nil {
		slog.Warn("aspect request: error verifying request data", "error", err, "ID", aspectRequest.ID)
		aspectResponse.Attributes.Error.Code = "7060"
		aspectResponse.Attributes.Error.Title = "error verifying request data"
		aspectResponse.Attributes.Error.Detail = err.Error()
		buildAspectResponse(writer, http.StatusBadRequest, aspectResponse)
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
	if aspectRequest.Attributes.Zone != 0 {
		// input from UTM coordinates
		zone = aspectRequest.Attributes.Zone
		easting = aspectRequest.Attributes.Easting
		northing = aspectRequest.Attributes.Northing
		outputFormat = "geotiff"

		// get tile metadata for primary tile (e.g. "32_507_5491")
		tile, err = getGeotiffTile(easting, northing, zone, 1)
		if err != nil {
			slog.Warn("aspect request: error getting GeoTIFF tile for UTM coordinates", "error", err,
				"easting", easting, "northing", northing, "zone", zone, "ID", aspectRequest.ID)
			aspectResponse.Attributes.Error.Code = "7080"
			aspectResponse.Attributes.Error.Title = "getting GeoTIFF tile for UTM coordinates"
			aspectResponse.Attributes.Error.Detail = err.Error()
			buildAspectResponse(writer, http.StatusBadRequest, aspectResponse)
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
		longitude = aspectRequest.Attributes.Longitude
		latitude = aspectRequest.Attributes.Latitude
		outputFormat = "png"

		// get tile metadata for primary tile (e.g. "32_507_5491")
		tile, zone, easting, northing, err = getTileUTM(longitude, latitude)
		if err != nil {
			err = fmt.Errorf("error [%w] getting tile for coordinates lon: %.8f, lat: %.8f", err, longitude, latitude)
			slog.Warn("aspect request: error getting GeoTIFF tile for lon/lat coordinates", "error", err,
				"longitude", longitude, "latitude", latitude, "ID", aspectRequest.ID)
			aspectResponse.Attributes.Error.Code = "7100"
			aspectResponse.Attributes.Error.Title = "getting GeoTIFF tile for lon/lat coordinates"
			aspectResponse.Attributes.Error.Detail = err.Error()
			buildAspectResponse(writer, http.StatusBadRequest, aspectResponse)
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

	// build aspect for all existing tiles
	for _, tile := range tiles {
		aspect, err := generateAspectObjectForTile(tile, outputFormat, aspectRequest.Attributes.GradientAlgorithm, aspectRequest.Attributes.ColorTextFileContent, aspectRequest.Attributes.ColoringAlgorithm)
		if err != nil {
			slog.Warn("aspect request: error generating aspect object for tile", "error", err, "ID", aspectRequest.ID)
			aspectResponse.Attributes.Error.Code = "7120"
			aspectResponse.Attributes.Error.Title = "error generating aspect object for tile"
			aspectResponse.Attributes.Error.Detail = err.Error()
			buildAspectResponse(writer, http.StatusBadRequest, aspectResponse)
			return
		}
		aspectResponse.Attributes.Aspects = append(aspectResponse.Attributes.Aspects, aspect)
	}

	// copy request parameters into response
	aspectResponse.ID = aspectRequest.ID
	aspectResponse.Attributes.IsError = false
	aspectResponse.Attributes.Zone = aspectRequest.Attributes.Zone
	aspectResponse.Attributes.Easting = aspectRequest.Attributes.Easting
	aspectResponse.Attributes.Northing = aspectRequest.Attributes.Northing
	aspectResponse.Attributes.Longitude = aspectRequest.Attributes.Longitude
	aspectResponse.Attributes.Latitude = aspectRequest.Attributes.Latitude
	aspectResponse.Attributes.GradientAlgorithm = aspectRequest.Attributes.GradientAlgorithm
	aspectResponse.Attributes.ColorTextFileContent = aspectRequest.Attributes.ColorTextFileContent
	aspectResponse.Attributes.ColoringAlgorithm = aspectRequest.Attributes.ColoringAlgorithm

	// success response
	buildAspectResponse(writer, http.StatusOK, aspectResponse)
}

/*
verifyAspectRequestData verifies 'aspect' request data.
It performs several checks on the request data to ensure its validity.
*/
func verifyAspectRequestData(request *http.Request, aspectRequest AspectRequest) error {
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
	if aspectRequest.Type != TypeAspectRequest {
		return fmt.Errorf("unexpected request Type [%v]", aspectRequest.Type)
	}

	// verify ID
	if len(aspectRequest.ID) > 1024 {
		return errors.New("ID must be 0-1024 characters long")
	}

	// verify coordinates (either utm or lon/lat coordinates must be set)
	if aspectRequest.Attributes.Zone == 0 && aspectRequest.Attributes.Longitude == 0 {
		return errors.New("either utm or lon/lat coordinates must be set")
	}

	// verify zone for Germany (Zone: 32 or 33)
	if aspectRequest.Attributes.Zone != 0 {
		if aspectRequest.Attributes.Zone < 32 || aspectRequest.Attributes.Zone > 33 {
			return errors.New("invalid zone for Germany")
		}
	}

	// verify longitude for Germany (Longitude: from  5.8663° E to 15.0419° E)
	if aspectRequest.Attributes.Longitude != 0 {
		if aspectRequest.Attributes.Longitude > 15.3 || aspectRequest.Attributes.Longitude < 5.5 {
			return errors.New("invalid longitude for Germany")
		}
	}

	// verify latitude for Germany (Latitude: from 47.2701° N to 55.0586° N)
	if aspectRequest.Attributes.Latitude != 0 {
		if aspectRequest.Attributes.Latitude > 55.3 || aspectRequest.Attributes.Latitude < 47.0 {
			return errors.New("invalid latitude for Germany")
		}
	}

	// verify gradient algorithm
	if !(aspectRequest.Attributes.GradientAlgorithm == "Horn" || aspectRequest.Attributes.GradientAlgorithm == "ZevenbergenThorne") {
		return errors.New("unsupported gradient algorithm (not Horn or ZevenbergenThorne)")
	}

	// verify 'color text file content'
	err := verifyColorTextFileContent(aspectRequest.Attributes.ColorTextFileContent)
	if err != nil {
		return errors.New("invalid color text file content (%w)")
	}

	// verify coloring algorithm
	if aspectRequest.Attributes.ColoringAlgorithm != "" {
		if !(aspectRequest.Attributes.ColoringAlgorithm == "interpolation" || aspectRequest.Attributes.ColoringAlgorithm == "rounding") {
			return errors.New("unsupported coloring algorithm (not 'interpolation' or 'rounding')")
		}
	}
	return nil
}

/*
buildAspectResponse builds HTTP responses with specified status and body.
It sets the Content-Type and Content-Length headers before writing the response body.
This function is used to construct consistent HTTP responses throughout the application.
*/
func buildAspectResponse(writer http.ResponseWriter, httpStatus int, aspectResponse AspectResponse) {
	// log limit length of body (e.g., the aspect objects as part of the body can be very large)
	maxBodyLength := 1024

	// CORS: allow requests from any origin
	writer.Header().Set("Access-Control-Allow-Origin", "*")
	// CORS: allowed methods
	writer.Header().Set("Access-Control-Allow-Methods", "POST")
	// CORS: allowed headers
	writer.Header().Set("Access-Control-Allow-Headers", "Content-Type")

	// marshal response
	body, err := json.MarshalIndent(aspectResponse, "", "  ")
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
generateAspectObjectForTile builds aspect object for given tile index.
*/
func generateAspectObjectForTile(tile TileMetadata, outputFormat string, gradientAlgorithm string, colorTextFileContent []string, coloringAlgorithm string) (Aspect, error) {
	var aspect Aspect
	var boundingBox WGS84BoundingBox

	// run operations in temp directory
	tempDir, err := os.MkdirTemp("", "dtm-elevation-service-aspect-")
	if err != nil {
		return aspect, fmt.Errorf("error [%w] at os.MkdirTemp()", err)
	}
	defer func() {
		_ = os.RemoveAll(tempDir)
	}()

	// create 'color-text-file' for 'gdaldem color-relief' in temp directory
	colorTextFile := filepath.Join(tempDir, "color-text-file.txt")
	err = createColorTextFile(colorTextFile, colorTextFileContent)
	if err != nil {
		return aspect, fmt.Errorf("error [%w] creating 'color-text-file'", err)
	}

	inputGeoTIFF := tile.Path
	aspectUTMGeoTIFF := filepath.Join(tempDir, tile.Index+".aspect.utm.tif")
	aspectColorUTMGeoTIFF := filepath.Join(tempDir, tile.Index+".aspect.color.utm.tif")
	aspectWebmercatorGeoTIFF := filepath.Join(tempDir, tile.Index+".aspect.webmercator.tif")
	aspectColorWebmercatoPNG := filepath.Join(tempDir, tile.Index+".aspect.color.webmercator.png")

	// 1. create native aspect with 'gdaldem aspect'
	// e.g. gdaldem aspect dgm1_32_497_5670_1_he.tif 32_497_5670_hangexposition.utm.tif -alg Horn -compute_edges
	commandExitStatus, commandOutput, err := runCommand("gdaldem", []string{"aspect", inputGeoTIFF, aspectUTMGeoTIFF, "-alg", gradientAlgorithm, "-compute_edges"})
	if err != nil {
		return aspect, fmt.Errorf("error [%w: %d - %s] at runCommand()", err, commandExitStatus, commandOutput)
	}
	// fmt.Printf("commandExitStatus: %d\n", commandExitStatus)
	// fmt.Printf("commandOutput: %s\n", commandOutput)

	var data []byte
	switch strings.ToLower(outputFormat) {
	case "geotiff":
		// 2. colorize aspect with 'gdaldem color-relief'
		options := []string{"color-relief", aspectUTMGeoTIFF, colorTextFile, aspectColorUTMGeoTIFF, "-alpha"}
		if coloringAlgorithm == "rounding" {
			options = append(options, "-nearest_color_entry")
		}
		commandExitStatus, commandOutput, err = runCommand("gdaldem", options)
		if err != nil {
			return aspect, fmt.Errorf("error [%w: %d - %s] at runCommand()", err, commandExitStatus, commandOutput)
		}
		// fmt.Printf("commandExitStatus: %d\n", commandExitStatus)
		// fmt.Printf("commandOutput: %s\n", commandOutput)

		data, err = os.ReadFile(aspectColorUTMGeoTIFF)
		if err != nil {
			return aspect, fmt.Errorf("error [%w] at os.ReadFile()", err)
		}

	case "png":
		// 2. convert UTM (EPSG:25832/EPSG:25833) to Webmercator (EPSG:3857) with 'gdalwarp'
		// e.g. gdalwarp -t_srs EPSG:3857 32_497_5670_hangexposition.utm.tif 32_497_5670_hangexposition.webmercator.tif
		commandExitStatus, commandOutput, err = runCommand("gdalwarp", []string{"-t_srs", "EPSG:3857", aspectUTMGeoTIFF, aspectWebmercatorGeoTIFF})
		if err != nil {
			return aspect, fmt.Errorf("error [%w: %d - %s] at runCommand()", err, commandExitStatus, commandOutput)
		}
		// fmt.Printf("commandExitStatus: %d\n", commandExitStatus)
		// fmt.Printf("commandOutput: %s\n", commandOutput)

		// 3. colorize aspect with 'gdaldem color-relief' (creates PNG file)
		// e.g. gdaldem color-relief 32_497_5670_hangexposition.webmercator.tif aspect-colors.txt 32_497_5670_hangexposition.webmercator.png -alpha
		options := []string{"color-relief", aspectWebmercatorGeoTIFF, colorTextFile, aspectColorWebmercatoPNG, "-alpha"}
		if coloringAlgorithm == "rounding" {
			options = append(options, "-nearest_color_entry")
		}
		commandExitStatus, commandOutput, err = runCommand("gdaldem", options)
		if err != nil {
			return aspect, fmt.Errorf("error [%w: %d - %s] at runCommand()", err, commandExitStatus, commandOutput)
		}
		// fmt.Printf("commandExitStatus: %d\n", commandExitStatus)
		// fmt.Printf("commandOutput: %s\n", commandOutput)

		// 4. get bounding box (in wgs84) for webmercator tif (georeference of webmercator png )
		boundingBox, err = calculateWGS84BoundingBox(tile.Path)
		if err != nil {
			return aspect, fmt.Errorf("error [%w] at calculateWGS84BoundingBox(), file: %s", err, tile.Path)
		}

		// read result file
		data, err = os.ReadFile(aspectColorWebmercatoPNG)
		if err != nil {
			return aspect, fmt.Errorf("error [%w] at os.ReadFile()", err)
		}

	default:
		return aspect, fmt.Errorf("unsupported format [%s]", outputFormat)
	}

	// set aspect return structure
	aspect.Data = data
	aspect.DataFormat = outputFormat
	aspect.Actuality = tile.Actuality
	aspect.Origin = tile.Source
	aspect.TileIndex = tile.Index
	aspect.BoundingBox = boundingBox // only relevant for PNG

	// get attribution for resource
	attribution := "unknown"
	resource, err := getElevationResource(tile.Source)
	if err != nil {
		slog.Error("aspect request: error getting elevation resource", "error", err, "source", tile.Source)
	} else {
		attribution = resource.Attribution
	}
	aspect.Attribution = attribution

	return aspect, nil
}
