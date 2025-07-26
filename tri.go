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
triRequest handles 'tri request' from client.
*/
func triRequest(writer http.ResponseWriter, request *http.Request) {
	var triResponse = TRIResponse{Type: TypeTRIResponse, ID: "unknown"}
	triResponse.Attributes.IsError = true

	// statistics
	atomic.AddUint64(&TRIRequests, 1)

	// limit overall request body size
	request.Body = http.MaxBytesReader(writer, request.Body, MaxTRIRequestBodySize)

	// read request
	bodyData, err := io.ReadAll(request.Body)
	if err != nil {
		// check specifically for the error returned by MaxBytesReader
		var maxBytesErr *http.MaxBytesError
		if errors.As(err, &maxBytesErr) {
			slog.Warn("tri request: request body too large", "limit", maxBytesErr.Limit, "ID", "unknown")
			triResponse.Attributes.Error.Code = "9000"
			triResponse.Attributes.Error.Title = "request body too large"
			triResponse.Attributes.Error.Detail = fmt.Sprintf("request body exceeds limit of %d bytes", maxBytesErr.Limit)
			buildTRIResponse(writer, http.StatusRequestEntityTooLarge, triResponse)
		} else {
			// handle other read errors
			slog.Warn("tri request: error reading request body", "error", err, "ID", "unknown")
			triResponse.Attributes.Error.Code = "9020"
			triResponse.Attributes.Error.Title = "error reading request body"
			triResponse.Attributes.Error.Detail = err.Error()
			buildTRIResponse(writer, http.StatusBadRequest, triResponse)
		}
		return
	}

	// unmarshal request
	triRequest := TRIRequest{}
	err = json.Unmarshal(bodyData, &triRequest)
	if err != nil {
		slog.Warn("tri request: error unmarshaling request body", "error", err, "ID", "unknown")
		triResponse.Attributes.Error.Code = "9040"
		triResponse.Attributes.Error.Title = "error unmarshaling request body"
		triResponse.Attributes.Error.Detail = err.Error()
		buildTRIResponse(writer, http.StatusBadRequest, triResponse)
		return
	}

	// verify request data
	err = verifyTRIRequestData(request, triRequest)
	if err != nil {
		slog.Warn("tri request: error verifying request data", "error", err, "ID", triRequest.ID)
		triResponse.Attributes.Error.Code = "9060"
		triResponse.Attributes.Error.Title = "error verifying request data"
		triResponse.Attributes.Error.Detail = err.Error()
		buildTRIResponse(writer, http.StatusBadRequest, triResponse)
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
	if triRequest.Attributes.Zone != 0 {
		// input from UTM coordinates
		zone = triRequest.Attributes.Zone
		easting = triRequest.Attributes.Easting
		northing = triRequest.Attributes.Northing
		outputFormat = "geotiff"

		// get tile metadata for primary tile (e.g. "32_507_5491")
		tile, err = getGeotiffTile(easting, northing, zone, 1)
		if err != nil {
			slog.Warn("tri request: error getting GeoTIFF tile for UTM coordinates", "error", err,
				"easting", easting, "northing", northing, "zone", zone, "ID", triRequest.ID)
			triResponse.Attributes.Error.Code = "9080"
			triResponse.Attributes.Error.Title = "getting GeoTIFF tile for UTM coordinates"
			triResponse.Attributes.Error.Detail = err.Error()
			buildTRIResponse(writer, http.StatusBadRequest, triResponse)
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
		longitude = triRequest.Attributes.Longitude
		latitude = triRequest.Attributes.Latitude
		outputFormat = "png"

		// get tile metadata for primary tile (e.g. "32_507_5491")
		tile, zone, easting, northing, err = getTileUTM(longitude, latitude)
		if err != nil {
			err = fmt.Errorf("error [%w] getting tile for coordinates lon: %.8f, lat: %.8f", err, longitude, latitude)
			slog.Warn("tri request: error getting GeoTIFF tile for lon/lat coordinates", "error", err,
				"longitude", longitude, "latitude", latitude, "ID", triRequest.ID)
			triResponse.Attributes.Error.Code = "9100"
			triResponse.Attributes.Error.Title = "getting GeoTIFF tile for lon/lat coordinates"
			triResponse.Attributes.Error.Detail = err.Error()
			buildTRIResponse(writer, http.StatusBadRequest, triResponse)
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

	// build tri for all existing tiles
	for _, tile := range tiles {
		tri, err := generateTRIObjectForTile(tile, outputFormat, triRequest.Attributes.ColorTextFileContent)
		if err != nil {
			slog.Warn("tri request: error generating tri object for tile", "error", err, "ID", triRequest.ID)
			triResponse.Attributes.Error.Code = "9120"
			triResponse.Attributes.Error.Title = "error generating tri object for tile"
			triResponse.Attributes.Error.Detail = err.Error()
			buildTRIResponse(writer, http.StatusBadRequest, triResponse)
			return
		}
		triResponse.Attributes.TRIs = append(triResponse.Attributes.TRIs, tri)
	}

	// copy request parameters into response
	triResponse.ID = triRequest.ID
	triResponse.Attributes.IsError = false
	triResponse.Attributes.Zone = triRequest.Attributes.Zone
	triResponse.Attributes.Easting = triRequest.Attributes.Easting
	triResponse.Attributes.Northing = triRequest.Attributes.Northing
	triResponse.Attributes.Longitude = triRequest.Attributes.Longitude
	triResponse.Attributes.Latitude = triRequest.Attributes.Latitude
	triResponse.Attributes.ColorTextFileContent = triRequest.Attributes.ColorTextFileContent

	// success response
	buildTRIResponse(writer, http.StatusOK, triResponse)
}

/*
verifyTRIRequestData verifies 'TRI' request data.
It performs several checks on the request data to ensure its validity.
*/
func verifyTRIRequestData(request *http.Request, triRequest TRIRequest) error {
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
	if triRequest.Type != TypeTRIRequest {
		return fmt.Errorf("unexpected request Type [%v]", triRequest.Type)
	}

	// verify ID
	if len(triRequest.ID) > 1024 {
		return errors.New("ID must be 0-1024 characters long")
	}

	// verify coordinates (either utm or lon/lat coordinates must be set)
	if triRequest.Attributes.Zone == 0 && triRequest.Attributes.Longitude == 0 {
		return errors.New("either utm or lon/lat coordinates must be set")
	}

	// verify zone for Germany (Zone: 32 or 33)
	if triRequest.Attributes.Zone != 0 {
		if triRequest.Attributes.Zone < 32 || triRequest.Attributes.Zone > 33 {
			return errors.New("invalid zone for Germany")
		}
	}

	// verify longitude for Germany (Longitude: from  5.8663° E to 15.0419° E)
	if triRequest.Attributes.Longitude != 0 {
		if triRequest.Attributes.Longitude > 15.3 || triRequest.Attributes.Longitude < 5.5 {
			return errors.New("invalid longitude for Germany")
		}
	}

	// verify latitude for Germany (Latitude: from 47.2701° N to 55.0586° N)
	if triRequest.Attributes.Latitude != 0 {
		if triRequest.Attributes.Latitude > 55.3 || triRequest.Attributes.Latitude < 47.0 {
			return errors.New("invalid latitude for Germany")
		}
	}

	// verify 'color text file content'
	err := verifyColorTextFileContent(triRequest.Attributes.ColorTextFileContent)
	if err != nil {
		return errors.New("invalid color text file content (%w)")
	}

	return nil
}

/*
buildTRIResponse builds HTTP responses with specified status and body.
It sets the Content-Type and Content-Length headers before writing the response body.
This function is used to construct consistent HTTP responses throughout the application.
*/
func buildTRIResponse(writer http.ResponseWriter, httpStatus int, triResponse TRIResponse) {
	// log limit length of body (e.g., the tri objects as part of the body can be very large)
	maxBodyLength := 1024

	// CORS: allow requests from any origin
	writer.Header().Set("Access-Control-Allow-Origin", "*")
	// CORS: allowed methods
	writer.Header().Set("Access-Control-Allow-Methods", "POST")
	// CORS: allowed headers
	writer.Header().Set("Access-Control-Allow-Headers", "Content-Type")

	// marshal response
	body, err := json.MarshalIndent(triResponse, "", "  ")
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
generateTRIObjectForTile builds tri object for given tile index.
*/
func generateTRIObjectForTile(tile TileMetadata, outputFormat string, colorTextFileContent []string) (TRI, error) {
	var tri TRI
	var boundingBox WGS84BoundingBox

	// run operations in temp directory
	tempDir, err := os.MkdirTemp("", "dtm-elevation-service-tri-")
	if err != nil {
		return tri, fmt.Errorf("error [%w] at os.MkdirTemp()", err)
	}
	defer func() {
		_ = os.RemoveAll(tempDir)
	}()

	// create 'color-text-file' for 'gdaldem color-relief' in temp directory
	colorTextFile := filepath.Join(tempDir, "color-text-file.txt")
	err = createColorTextFile(colorTextFile, colorTextFileContent)
	if err != nil {
		return tri, fmt.Errorf("error [%w] creating 'color-text-file'", err)
	}

	inputGeoTIFF := tile.Path
	triUTMGeoTIFF := filepath.Join(tempDir, tile.Index+".tri.utm.tif")
	triColorUTMGeoTIFF := filepath.Join(tempDir, tile.Index+".tri.color.utm.tif")
	triWebmercatorGeoTIFF := filepath.Join(tempDir, tile.Index+".tri.webmercator.tif")
	triColorWebmercatoPNG := filepath.Join(tempDir, tile.Index+".tri.color.webmercator.png")

	// 1. create native TRI with 'gdaldem TRI'
	// e.g. gdaldem TRI 602_5251.tif 602_5251_tri.utm.tif -alg Wilson -compute_edges
	commandExitStatus, commandOutput, err := runCommand("gdaldem", []string{"TRI", inputGeoTIFF, triUTMGeoTIFF, "-alg", "Wilson", "-compute_edges"})
	if err != nil {
		return tri, fmt.Errorf("error [%w: %d - %s] at runCommand()", err, commandExitStatus, commandOutput)
	}
	// fmt.Printf("commandExitStatus: %d\n", commandExitStatus)
	// fmt.Printf("commandOutput: %s\n", commandOutput)

	var data []byte
	switch strings.ToLower(outputFormat) {
	case "geotiff":
		// 2. colorize tri with 'gdaldem color-relief'
		// e.g. gdaldem color-relief 602_5251_tri.utm.tif tri-colors.txt 602_5251_tri.utm.png -alpha
		commandExitStatus, commandOutput, err = runCommand("gdaldem", []string{"color-relief", triUTMGeoTIFF, colorTextFile, triColorUTMGeoTIFF, "-alpha"})
		if err != nil {
			return tri, fmt.Errorf("error [%w: %d - %s] at runCommand()", err, commandExitStatus, commandOutput)
		}
		// fmt.Printf("commandExitStatus: %d\n", commandExitStatus)
		// fmt.Printf("commandOutput: %s\n", commandOutput)

		data, err = os.ReadFile(triColorUTMGeoTIFF)
		if err != nil {
			return tri, fmt.Errorf("error [%w] at os.ReadFile()", err)
		}

	case "png":
		// 2. convert UTM (EPSG:25832/EPSG:25833) to Webmercator (EPSG:3857) with 'gdalwarp'
		// e.g. gdalwarp -t_srs EPSG:3857 602_5251_tri.utm.tif 602_5251_tri.webmercator.tif
		commandExitStatus, commandOutput, err = runCommand("gdalwarp", []string{"-t_srs", "EPSG:3857", triUTMGeoTIFF, triWebmercatorGeoTIFF})
		if err != nil {
			return tri, fmt.Errorf("error [%w: %d - %s] at runCommand()", err, commandExitStatus, commandOutput)
		}
		// fmt.Printf("commandExitStatus: %d\n", commandExitStatus)
		// fmt.Printf("commandOutput: %s\n", commandOutput)

		// 3. colorize tri with 'gdaldem color-relief' (creates PNG file)
		// e.g. gdaldem color-relief 602_5251_tri.webmercator.tif tri-colors.txt 602_5251_tri.webmercator.png -alpha
		commandExitStatus, commandOutput, err = runCommand("gdaldem", []string{"color-relief", triWebmercatorGeoTIFF, colorTextFile, triColorWebmercatoPNG, "-alpha"})
		if err != nil {
			return tri, fmt.Errorf("error [%w: %d - %s] at runCommand()", err, commandExitStatus, commandOutput)
		}
		// fmt.Printf("commandExitStatus: %d\n", commandExitStatus)
		// fmt.Printf("commandOutput: %s\n", commandOutput)

		// 4. get bounding box (in wgs84) for webmercator tif (georeference of webmercator png )
		boundingBox, err = calculateWGS84BoundingBox(tile.Path)
		if err != nil {
			return tri, fmt.Errorf("error [%w] at calculateWGS84BoundingBox(), file: %s", err, tile.Path)
		}

		// read result file
		data, err = os.ReadFile(triColorWebmercatoPNG)
		if err != nil {
			return tri, fmt.Errorf("error [%w] at os.ReadFile()", err)
		}

	default:
		return tri, fmt.Errorf("unsupported format [%s]", outputFormat)
	}

	// set contour return structure
	tri.Data = data
	tri.DataFormat = outputFormat
	tri.Actuality = tile.Actuality
	tri.Origin = tile.Source
	tri.TileIndex = tile.Index
	tri.BoundingBox = boundingBox // only relevant for PNG

	// get attribution for resource
	attribution := "unknown"
	resource, err := getElevationResource(tile.Source)
	if err != nil {
		slog.Error("tri request: error getting elevation resource", "error", err, "source", tile.Source)
	} else {
		attribution = resource.Attribution
	}
	tri.Attribution = attribution

	return tri, nil
}
