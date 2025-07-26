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
riRequest handles 'RI request' from client.
*/
func riRequest(writer http.ResponseWriter, request *http.Request) {
	var riResponse = RIResponse{Type: TypeRIResponse, ID: "unknown"}
	riResponse.Attributes.IsError = true

	// statistics
	atomic.AddUint64(&RIRequests, 1)

	// limit overall request body size
	request.Body = http.MaxBytesReader(writer, request.Body, MaxRIRequestBodySize)

	// read request
	bodyData, err := io.ReadAll(request.Body)
	if err != nil {
		// check specifically for the error returned by MaxBytesReader
		var maxBytesErr *http.MaxBytesError
		if errors.As(err, &maxBytesErr) {
			slog.Warn("ri request: request body too large", "limit", maxBytesErr.Limit, "ID", "unknown")
			riResponse.Attributes.Error.Code = "10000"
			riResponse.Attributes.Error.Title = "request body too large"
			riResponse.Attributes.Error.Detail = fmt.Sprintf("request body exceeds limit of %d bytes", maxBytesErr.Limit)
			buildRIResponse(writer, http.StatusRequestEntityTooLarge, riResponse)
		} else {
			// handle other read errors
			slog.Warn("ri request: error reading request body", "error", err, "ID", "unknown")
			riResponse.Attributes.Error.Code = "10020"
			riResponse.Attributes.Error.Title = "error reading request body"
			riResponse.Attributes.Error.Detail = err.Error()
			buildRIResponse(writer, http.StatusBadRequest, riResponse)
		}
		return
	}

	// unmarshal request
	riRequest := RIRequest{}
	err = json.Unmarshal(bodyData, &riRequest)
	if err != nil {
		slog.Warn("ri request: error unmarshaling request body", "error", err, "ID", "unknown")
		riResponse.Attributes.Error.Code = "10040"
		riResponse.Attributes.Error.Title = "error unmarshaling request body"
		riResponse.Attributes.Error.Detail = err.Error()
		buildRIResponse(writer, http.StatusBadRequest, riResponse)
		return
	}

	// verify request data
	err = verifyRIRequestData(request, riRequest)
	if err != nil {
		slog.Warn("ri request: error verifying request data", "error", err, "ID", riRequest.ID)
		riResponse.Attributes.Error.Code = "10060"
		riResponse.Attributes.Error.Title = "error verifying request data"
		riResponse.Attributes.Error.Detail = err.Error()
		buildRIResponse(writer, http.StatusBadRequest, riResponse)
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
	if riRequest.Attributes.Zone != 0 {
		// input from UTM coordinates
		zone = riRequest.Attributes.Zone
		easting = riRequest.Attributes.Easting
		northing = riRequest.Attributes.Northing
		outputFormat = "geotiff"

		// get tile metadata for primary tile (e.g. "32_507_5491")
		tile, err = getGeotiffTile(easting, northing, zone, 1)
		if err != nil {
			slog.Warn("ri request: error getting GeoTIFF tile for UTM coordinates", "error", err,
				"easting", easting, "northing", northing, "zone", zone, "ID", riRequest.ID)
			riResponse.Attributes.Error.Code = "10080"
			riResponse.Attributes.Error.Title = "getting GeoTIFF tile for UTM coordinates"
			riResponse.Attributes.Error.Detail = err.Error()
			buildRIResponse(writer, http.StatusBadRequest, riResponse)
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
		longitude = riRequest.Attributes.Longitude
		latitude = riRequest.Attributes.Latitude
		outputFormat = "png"

		// get tile metadata for primary tile (e.g. "32_507_5491")
		tile, zone, easting, northing, err = getTileUTM(longitude, latitude)
		if err != nil {
			err = fmt.Errorf("error [%w] getting tile for coordinates lon: %.8f, lat: %.8f", err, longitude, latitude)
			slog.Warn("ri request: error getting GeoTIFF tile for lon/lat coordinates", "error", err,
				"longitude", longitude, "latitude", latitude, "ID", riRequest.ID)
			riResponse.Attributes.Error.Code = "10100"
			riResponse.Attributes.Error.Title = "getting GeoTIFF tile for lon/lat coordinates"
			riResponse.Attributes.Error.Detail = err.Error()
			buildRIResponse(writer, http.StatusBadRequest, riResponse)
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

	// build ri for all existing tiles
	for _, tile := range tiles {
		ri, err := generateRIObjectForTile(tile, outputFormat, riRequest.Attributes.ColorTextFileContent)
		if err != nil {
			slog.Warn("ri request: error generating ri object for tile", "error", err, "ID", riRequest.ID)
			riResponse.Attributes.Error.Code = "10120"
			riResponse.Attributes.Error.Title = "error generating ri object for tile"
			riResponse.Attributes.Error.Detail = err.Error()
			buildRIResponse(writer, http.StatusBadRequest, riResponse)
			return
		}
		riResponse.Attributes.RIs = append(riResponse.Attributes.RIs, ri)
	}

	// copy request parameters into response
	riResponse.ID = riRequest.ID
	riResponse.Attributes.IsError = false
	riResponse.Attributes.Zone = riRequest.Attributes.Zone
	riResponse.Attributes.Easting = riRequest.Attributes.Easting
	riResponse.Attributes.Northing = riRequest.Attributes.Northing
	riResponse.Attributes.Longitude = riRequest.Attributes.Longitude
	riResponse.Attributes.Latitude = riRequest.Attributes.Latitude
	riResponse.Attributes.ColorTextFileContent = riRequest.Attributes.ColorTextFileContent

	// success response
	buildRIResponse(writer, http.StatusOK, riResponse)
}

/*
verifyRIRequestData verifies 'RI' request data.
It performs several checks on the request data to ensure its validity.
*/
func verifyRIRequestData(request *http.Request, riRequest RIRequest) error {
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
	if riRequest.Type != TypeRIRequest {
		return fmt.Errorf("unexpected request Type [%v]", riRequest.Type)
	}

	// verify ID
	if len(riRequest.ID) > 1024 {
		return errors.New("ID must be 0-1024 characters long")
	}

	// verify coordinates (either utm or lon/lat coordinates must be set)
	if riRequest.Attributes.Zone == 0 && riRequest.Attributes.Longitude == 0 {
		return errors.New("either utm or lon/lat coordinates must be set")
	}

	// verify zone for Germany (Zone: 32 or 33)
	if riRequest.Attributes.Zone != 0 {
		if riRequest.Attributes.Zone < 32 || riRequest.Attributes.Zone > 33 {
			return errors.New("invalid zone for Germany")
		}
	}

	// verify longitude for Germany (Longitude: from  5.8663° E to 15.0419° E)
	if riRequest.Attributes.Longitude != 0 {
		if riRequest.Attributes.Longitude > 15.3 || riRequest.Attributes.Longitude < 5.5 {
			return errors.New("invalid longitude for Germany")
		}
	}

	// verify latitude for Germany (Latitude: from 47.2701° N to 55.0586° N)
	if riRequest.Attributes.Latitude != 0 {
		if riRequest.Attributes.Latitude > 55.3 || riRequest.Attributes.Latitude < 47.0 {
			return errors.New("invalid latitude for Germany")
		}
	}

	// verify 'color text file content'
	err := verifyColorTextFileContent(riRequest.Attributes.ColorTextFileContent)
	if err != nil {
		return errors.New("invalid color text file content (%w)")
	}

	return nil
}

/*
buildRIResponse builds HTTP responses with specified status and body.
It sets the Content-Type and Content-Length headers before writing the response body.
This function is used to construct consistent HTTP responses throughout the application.
*/
func buildRIResponse(writer http.ResponseWriter, httpStatus int, riResponse RIResponse) {
	// log limit length of body (e.g., the ri objects as part of the body can be very large)
	maxBodyLength := 1024

	// CORS: allow requests from any origin
	writer.Header().Set("Access-Control-Allow-Origin", "*")
	// CORS: allowed methods
	writer.Header().Set("Access-Control-Allow-Methods", "POST")
	// CORS: allowed headers
	writer.Header().Set("Access-Control-Allow-Headers", "Content-Type")

	// marshal response
	body, err := json.MarshalIndent(riResponse, "", "  ")
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
generateRIObjectForTile builds ri object for given tile index.
*/
func generateRIObjectForTile(tile TileMetadata, outputFormat string, colorTextFileContent []string) (RI, error) {
	var ri RI
	var boundingBox WGS84BoundingBox

	// run operations in temp directory
	tempDir, err := os.MkdirTemp("", "dtm-elevation-service-ri-")
	if err != nil {
		return ri, fmt.Errorf("error [%w] at os.MkdirTemp()", err)
	}
	defer func() {
		_ = os.RemoveAll(tempDir)
	}()

	// create 'color-text-file' for 'gdaldem color-relief' in temp directory
	colorTextFile := filepath.Join(tempDir, "color-text-file.txt")
	err = createColorTextFile(colorTextFile, colorTextFileContent)
	if err != nil {
		return ri, fmt.Errorf("error [%w] creating 'color-text-file'", err)
	}

	inputGeoTIFF := tile.Path
	riUTMGeoTIFF := filepath.Join(tempDir, tile.Index+".ri.utm.tif")
	riColorUTMGeoTIFF := filepath.Join(tempDir, tile.Index+".ri.color.utm.tif")
	riWebmercatorGeoTIFF := filepath.Join(tempDir, tile.Index+".ri.webmercator.tif")
	riColorWebmercatoPNG := filepath.Join(tempDir, tile.Index+".ri.color.webmercator.png")

	// 1. create native RI with 'gdaldem roughness'
	commandExitStatus, commandOutput, err := runCommand("gdaldem", []string{"roughness", inputGeoTIFF, riUTMGeoTIFF, "-compute_edges"})
	if err != nil {
		return ri, fmt.Errorf("error [%w: %d - %s] at runCommand()", err, commandExitStatus, commandOutput)
	}
	// fmt.Printf("commandExitStatus: %d\n", commandExitStatus)
	// fmt.Printf("commandOutput: %s\n", commandOutput)

	var data []byte
	switch strings.ToLower(outputFormat) {
	case "geotiff":
		// 2. colorize ri with 'gdaldem color-relief'
		commandExitStatus, commandOutput, err = runCommand("gdaldem", []string{"color-relief", riUTMGeoTIFF, colorTextFile, riColorUTMGeoTIFF, "-alpha"})
		if err != nil {
			return ri, fmt.Errorf("error [%w: %d - %s] at runCommand()", err, commandExitStatus, commandOutput)
		}
		// fmt.Printf("commandExitStatus: %d\n", commandExitStatus)
		// fmt.Printf("commandOutput: %s\n", commandOutput)

		data, err = os.ReadFile(riColorUTMGeoTIFF)
		if err != nil {
			return ri, fmt.Errorf("error [%w] at os.ReadFile()", err)
		}

	case "png":
		// 2. convert UTM (EPSG:25832/EPSG:25833) to Webmercator (EPSG:3857) with 'gdalwarp'
		commandExitStatus, commandOutput, err = runCommand("gdalwarp", []string{"-t_srs", "EPSG:3857", riUTMGeoTIFF, riWebmercatorGeoTIFF})
		if err != nil {
			return ri, fmt.Errorf("error [%w: %d - %s] at runCommand()", err, commandExitStatus, commandOutput)
		}
		// fmt.Printf("commandExitStatus: %d\n", commandExitStatus)
		// fmt.Printf("commandOutput: %s\n", commandOutput)

		// 3. colorize ri with 'gdaldem color-relief' (creates PNG file)
		commandExitStatus, commandOutput, err = runCommand("gdaldem", []string{"color-relief", riWebmercatorGeoTIFF, colorTextFile, riColorWebmercatoPNG, "-alpha"})
		if err != nil {
			return ri, fmt.Errorf("error [%w: %d - %s] at runCommand()", err, commandExitStatus, commandOutput)
		}
		// fmt.Printf("commandExitStatus: %d\n", commandExitStatus)
		// fmt.Printf("commandOutput: %s\n", commandOutput)

		// 4. get bounding box (in wgs84) for webmercator tif (georeference of webmercator png )
		boundingBox, err = calculateWGS84BoundingBox(tile.Path)
		if err != nil {
			return ri, fmt.Errorf("error [%w] at calculateWGS84BoundingBox(), file: %s", err, tile.Path)
		}

		// read result file
		data, err = os.ReadFile(riColorWebmercatoPNG)
		if err != nil {
			return ri, fmt.Errorf("error [%w] at os.ReadFile()", err)
		}

	default:
		return ri, fmt.Errorf("unsupported format [%s]", outputFormat)
	}

	// set contour return structure
	ri.Data = data
	ri.DataFormat = outputFormat
	ri.Actuality = tile.Actuality
	ri.Origin = tile.Source
	ri.TileIndex = tile.Index
	ri.BoundingBox = boundingBox // only relevant for PNG

	// get attribution for resource
	attribution := "unknown"
	resource, err := getElevationResource(tile.Source)
	if err != nil {
		slog.Error("ri request: error getting elevation resource", "error", err, "source", tile.Source)
	} else {
		attribution = resource.Attribution
	}
	ri.Attribution = attribution

	return ri, nil
}
