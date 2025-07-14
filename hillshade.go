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
hillshadeRequest handles 'hillshade request' from client.
*/
func hillshadeRequest(writer http.ResponseWriter, request *http.Request) {
	var hillshadeResponse = HillshadeResponse{Type: TypeHillshadeResponse, ID: "unknown"}
	hillshadeResponse.Attributes.IsError = true

	// statistics
	atomic.AddUint64(&HillshadeRequests, 1)

	// limit overall request body size
	request.Body = http.MaxBytesReader(writer, request.Body, MaxHillshadeRequestBodySize)

	// read request
	bodyData, err := io.ReadAll(request.Body)
	if err != nil {
		// check specifically for the error returned by MaxBytesReader
		var maxBytesErr *http.MaxBytesError
		if errors.As(err, &maxBytesErr) {
			slog.Warn("hillshade request: request body too large", "limit", maxBytesErr.Limit, "ID", "unknown")
			hillshadeResponse.Attributes.Error.Code = "5000"
			hillshadeResponse.Attributes.Error.Title = "request body too large"
			hillshadeResponse.Attributes.Error.Detail = fmt.Sprintf("request body exceeds limit of %d bytes", maxBytesErr.Limit)
			buildHillshadeResponse(writer, http.StatusRequestEntityTooLarge, hillshadeResponse)
		} else {
			// handle other read errors
			slog.Warn("hillshade request: error reading request body", "error", err, "ID", "unknown")
			hillshadeResponse.Attributes.Error.Code = "5020"
			hillshadeResponse.Attributes.Error.Title = "error reading request body"
			hillshadeResponse.Attributes.Error.Detail = err.Error()
			buildHillshadeResponse(writer, http.StatusBadRequest, hillshadeResponse)
		}
		return
	}

	// unmarshal request
	hillshadeRequest := HillshadeRequest{}
	err = json.Unmarshal(bodyData, &hillshadeRequest)
	if err != nil {
		slog.Warn("hillshade request: error unmarshaling request body", "error", err, "ID", "unknown")
		hillshadeResponse.Attributes.Error.Code = "5040"
		hillshadeResponse.Attributes.Error.Title = "error unmarshaling request body"
		hillshadeResponse.Attributes.Error.Detail = err.Error()
		buildHillshadeResponse(writer, http.StatusBadRequest, hillshadeResponse)
		return
	}

	// verify request data
	err = verifyHillshadeRequestData(request, hillshadeRequest)
	if err != nil {
		slog.Warn("hillshade request: error verifying request data", "error", err, "ID", hillshadeRequest.ID)
		hillshadeResponse.Attributes.Error.Code = "5060"
		hillshadeResponse.Attributes.Error.Title = "error verifying request data"
		hillshadeResponse.Attributes.Error.Detail = err.Error()
		buildHillshadeResponse(writer, http.StatusBadRequest, hillshadeResponse)
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
	if hillshadeRequest.Attributes.Zone != 0 {
		// input from UTM coordinates
		zone = hillshadeRequest.Attributes.Zone
		easting = hillshadeRequest.Attributes.Easting
		northing = hillshadeRequest.Attributes.Northing
		outputFormat = "geotiff"

		// get tile metadata for primary tile (e.g. "32_507_5491")
		tile, err = getGeotiffTile(easting, northing, zone, 1)
		if err != nil {
			slog.Warn("hillshade request: error getting GeoTIFF tile for UTM coordinates", "error", err,
				"easting", easting, "northing", northing, "zone", zone, "ID", hillshadeRequest.ID)
			hillshadeResponse.Attributes.Error.Code = "5080"
			hillshadeResponse.Attributes.Error.Title = "getting GeoTIFF tile for UTM coordinates"
			hillshadeResponse.Attributes.Error.Detail = err.Error()
			buildHillshadeResponse(writer, http.StatusBadRequest, hillshadeResponse)
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
		longitude = hillshadeRequest.Attributes.Longitude
		latitude = hillshadeRequest.Attributes.Latitude
		outputFormat = "png"

		// get tile metadata for primary tile (e.g. "32_507_5491")
		tile, zone, easting, northing, err = getTileUTM(longitude, latitude)
		if err != nil {
			err = fmt.Errorf("error [%w] getting tile for coordinates lon: %.8f, lat: %.8f", err, longitude, latitude)
			slog.Warn("hillshade request: error getting GeoTIFF tile for lon/lat coordinates", "error", err,
				"longitude", longitude, "latitude", latitude, "ID", hillshadeRequest.ID)
			hillshadeResponse.Attributes.Error.Code = "5100"
			hillshadeResponse.Attributes.Error.Title = "getting GeoTIFF tile for lon/lat coordinates"
			hillshadeResponse.Attributes.Error.Detail = err.Error()
			buildHillshadeResponse(writer, http.StatusBadRequest, hillshadeResponse)
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

	// build hillshade for all existing tiles
	gradientAlgorithm := hillshadeRequest.Attributes.GradientAlgorithm
	verticalExaggeration := hillshadeRequest.Attributes.VerticalExaggeration
	azimuthOfLight := hillshadeRequest.Attributes.AzimuthOfLight
	altitudeOfLight := hillshadeRequest.Attributes.AltitudeOfLight
	shadingVariant := hillshadeRequest.Attributes.ShadingVariant
	for _, tile := range tiles {
		hillshade, err := generateHillshadeObjectForTile(tile, outputFormat, gradientAlgorithm, verticalExaggeration, azimuthOfLight, altitudeOfLight, shadingVariant)
		if err != nil {
			slog.Warn("hillshade request: error generating hillshade object for tile", "error", err, "ID", hillshadeRequest.ID)
			hillshadeResponse.Attributes.Error.Code = "5120"
			hillshadeResponse.Attributes.Error.Title = "error generating hillshade object for tile"
			hillshadeResponse.Attributes.Error.Detail = err.Error()
			buildHillshadeResponse(writer, http.StatusBadRequest, hillshadeResponse)
			return
		}
		hillshadeResponse.Attributes.Hillshades = append(hillshadeResponse.Attributes.Hillshades, hillshade)
	}

	// copy request parameters into response
	hillshadeResponse.ID = hillshadeRequest.ID
	hillshadeResponse.Attributes.IsError = false
	hillshadeResponse.Attributes.Zone = hillshadeRequest.Attributes.Zone
	hillshadeResponse.Attributes.Easting = hillshadeRequest.Attributes.Easting
	hillshadeResponse.Attributes.Northing = hillshadeRequest.Attributes.Northing
	hillshadeResponse.Attributes.Longitude = hillshadeRequest.Attributes.Longitude
	hillshadeResponse.Attributes.Latitude = hillshadeRequest.Attributes.Latitude
	hillshadeResponse.Attributes.GradientAlgorithm = hillshadeRequest.Attributes.GradientAlgorithm
	hillshadeResponse.Attributes.VerticalExaggeration = hillshadeRequest.Attributes.VerticalExaggeration
	hillshadeResponse.Attributes.AzimuthOfLight = hillshadeRequest.Attributes.AzimuthOfLight
	hillshadeResponse.Attributes.AltitudeOfLight = hillshadeRequest.Attributes.AltitudeOfLight
	hillshadeResponse.Attributes.ShadingVariant = hillshadeRequest.Attributes.ShadingVariant

	// success response
	buildHillshadeResponse(writer, http.StatusOK, hillshadeResponse)
}

/*
verifyHillshadeRequestData verifies 'hillshade' request data.
It performs several checks on the request data to ensure its validity.
*/
func verifyHillshadeRequestData(request *http.Request, hillshadeRequest HillshadeRequest) error {
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
	if hillshadeRequest.Type != TypeHillshadeRequest {
		return fmt.Errorf("unexpected request Type [%v]", hillshadeRequest.Type)
	}

	// verify ID
	if len(hillshadeRequest.ID) > 1024 {
		return errors.New("ID must be 0-1024 characters long")
	}

	// verify coordinates (either utm or lon/lat coordinates must be set)
	if hillshadeRequest.Attributes.Zone == 0 && hillshadeRequest.Attributes.Longitude == 0 {
		return errors.New("either utm or lon/lat coordinates must be set")
	}

	// verify zone for Germany (Zone: 32 or 33)
	if hillshadeRequest.Attributes.Zone != 0 {
		if hillshadeRequest.Attributes.Zone < 32 || hillshadeRequest.Attributes.Zone > 33 {
			return errors.New("invalid zone for Germany")
		}
	}

	// verify longitude for Germany (Longitude: from  5.8663° E to 15.0419° E)
	if hillshadeRequest.Attributes.Longitude != 0 {
		if hillshadeRequest.Attributes.Longitude > 15.3 || hillshadeRequest.Attributes.Longitude < 5.5 {
			return errors.New("invalid longitude for Germany")
		}
	}

	// verify latitude for Germany (Latitude: from 47.2701° N to 55.0586° N)
	if hillshadeRequest.Attributes.Latitude != 0 {
		if hillshadeRequest.Attributes.Latitude > 55.3 || hillshadeRequest.Attributes.Latitude < 47.0 {
			return errors.New("invalid latitude for Germany")
		}
	}

	// verify gradient algorithm
	if !(hillshadeRequest.Attributes.GradientAlgorithm == "Horn" || hillshadeRequest.Attributes.GradientAlgorithm == "ZevenbergenThorne") {
		return errors.New("unsupported gradient algorithm (not Horn or ZevenbergenThorne)")
	}

	// verify vertical exaggeration
	if hillshadeRequest.Attributes.VerticalExaggeration < 0.0 || hillshadeRequest.Attributes.VerticalExaggeration > 100.0 {
		return errors.New("vertical exaggeration must be between 0.0 and 100.0")
	}

	// verify azimuth of light source
	if hillshadeRequest.Attributes.AzimuthOfLight > 360 {
		return errors.New("azimuth of light source must be between 0 and 360")
	}

	// verify altitude of light source
	if hillshadeRequest.Attributes.AltitudeOfLight > 90 {
		return errors.New("altitude of light source must be between 0 and 90")
	}

	// verify shading variant
	switch strings.ToLower(hillshadeRequest.Attributes.ShadingVariant) {
	case "regular":
	case "combined":
	case "multidirectional":
	case "igor":
	default:
		return errors.New("unsupported shading variant (not regular, combined, multidirectional, igor)")
	}

	return nil
}

/*
buildHillshadeResponse builds HTTP responses with specified status and body.
It sets the Content-Type and Content-Length headers before writing the response body.
This function is used to construct consistent HTTP responses throughout the application.
*/
func buildHillshadeResponse(writer http.ResponseWriter, httpStatus int, hillshadeResponse HillshadeResponse) {
	// log limit length of body (hillshade objects as part of the body can be very large)
	maxBodyLength := 1024

	// CORS: allow requests from any origin
	writer.Header().Set("Access-Control-Allow-Origin", "*")
	// CORS: allowed methods
	writer.Header().Set("Access-Control-Allow-Methods", "POST")
	// CORS: allowed headers
	writer.Header().Set("Access-Control-Allow-Headers", "Content-Type")

	// marshal response
	body, err := json.MarshalIndent(hillshadeResponse, "", "  ")
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
generateHillshadeObjectForTile builds hillshade object for given tile index.

GeoTIFF in UTM projection:
 1. calculate hillshade on original source data
    gdaldem hillshade dgm1_32_409_5790_1_nw_2024.tif 32_409_5790.hillshade.utm.tif -compute_edges -z 1.0 -az 315 -alt 45 -alg Horn

PNG in webmercator projection with bounding box in wgs84 coordinates:
 2. reproject from EPSG:25832/EPSG:25833 to EPSG:3857 (Webmercator)
    gdalwarp -t_srs EPSG:3857 32_409_5790.hillshade.utm.tif 32_409_5790.hillshade.webmercator.tif
 3. convert webmercator tif to png
    gdal_translate -of PNG 32_409_5790.hillshade.webmercator.tif 32_409_5790.hillshade.webmercator.png
 4. get bounding box (in wgs84) for webmercator tif (georeference for webmercator png)
*/
func generateHillshadeObjectForTile(tile TileMetadata, outputFormat string, gradientAlgorithm string,
	verticalExaggeration float64, azimuthOfLight uint, altitudeOfLight uint, shadingVariant string) (Hillshade, error) {
	var hillshade Hillshade
	var boundingBox WGS84BoundingBox

	// run operations in temp directory
	tempDir, err := os.MkdirTemp("", "dtm-elevation-service-hillshade-")
	if err != nil {
		return hillshade, fmt.Errorf("error [%w] at os.MkdirTemp()", err)
	}
	defer func() {
		_ = os.RemoveAll(tempDir)
	}()

	inputGeoTIFF := tile.Path
	hillshadeUTMGeoTIFF := filepath.Join(tempDir, tile.Index+".hillshade.utm.tif")
	hillshadeWebmercatorGeoTIFF := filepath.Join(tempDir, tile.Index+".hillshade.webmercator.tif")
	hillshadeWebmercatorPNG := filepath.Join(tempDir, tile.Index+".hillshade.webmercator.png")

	// build options
	options := []string{"hillshade",
		inputGeoTIFF,
		hillshadeUTMGeoTIFF,
		"-compute_edges",
		"-z", fmt.Sprintf("%f", verticalExaggeration),
		"-alg", gradientAlgorithm,
	}

	shadingVariant = strings.ToLower(shadingVariant)
	switch shadingVariant {
	case "regular":
		options = append(options, "-az", fmt.Sprintf("%d", azimuthOfLight))
		options = append(options, "-alt", fmt.Sprintf("%d", altitudeOfLight))

	case "multidirectional":
		// omit -az option
		options = append(options, "-alt", fmt.Sprintf("%d", altitudeOfLight))
		options = append(options, "-"+shadingVariant)

	case "combined":
		options = append(options, "-az", fmt.Sprintf("%d", azimuthOfLight))
		options = append(options, "-alt", fmt.Sprintf("%d", altitudeOfLight))
		options = append(options, "-"+shadingVariant)

	case "igor":
		// omit -alt option
		options = append(options, "-az", fmt.Sprintf("%d", azimuthOfLight))
		options = append(options, "-"+shadingVariant)

	default:
		return hillshade, fmt.Errorf("unsupported shading variant [%s]", shadingVariant)
	}

	// 1. calculate hillshade on original source data
	// e.g. gdaldem hillshade dgm1_32_409_5790_1_nw_2024.tif 32_409_5790.hillshade.utm.tif -compute_edges -z 1.0 -az 315 -alt 45 -alg Horn
	commandExitStatus, commandOutput, err := runCommand("gdaldem", options)
	if err != nil {
		return hillshade, fmt.Errorf("error [%w: %d - %s] at runCommand()", err, commandExitStatus, commandOutput)
	}
	// fmt.Printf("commandExitStatus: %d\n", commandExitStatus)
	// fmt.Printf("commandOutput: %s\n", commandOutput)

	var data []byte
	switch strings.ToLower(outputFormat) {
	case "geotiff":
		data, err = os.ReadFile(hillshadeUTMGeoTIFF)
		if err != nil {
			return hillshade, fmt.Errorf("error [%w] at os.ReadFile()", err)
		}

	case "png":
		// 2. reproject from EPSG:25832/EPSG:25833 to EPSG:3857 (Webmercator)
		// e.g. gdalwarp -t_srs EPSG:3857 32_409_5790.hillshade.utm.tif 32_409_5790.hillshade.webmercator.tif
		commandExitStatus, commandOutput, err = runCommand("gdalwarp", []string{"-t_srs", "EPSG:3857", hillshadeUTMGeoTIFF, hillshadeWebmercatorGeoTIFF})
		if err != nil {
			return hillshade, fmt.Errorf("error [%w: %d - %s] at runCommand()", err, commandExitStatus, commandOutput)
		}
		// fmt.Printf("commandExitStatus: %d\n", commandExitStatus)
		// fmt.Printf("commandOutput: %s\n", commandOutput)

		// 3. convert webmercator tif to png
		// e.g. gdal_translate -of PNG 32_409_5790.hillshade.webmercator.tif 32_409_5790.hillshade.webmercator.png
		commandExitStatus, commandOutput, err = runCommand("gdal_translate", []string{"-of", "PNG", hillshadeWebmercatorGeoTIFF, hillshadeWebmercatorPNG})
		if err != nil {
			return hillshade, fmt.Errorf("error [%w: %d - %s] at runCommand()", err, commandExitStatus, commandOutput)
		}
		// fmt.Printf("commandExitStatus: %d\n", commandExitStatus)
		// fmt.Printf("commandOutput: %s\n", commandOutput)

		// 4. get bounding box (in wgs84) for webmercator tif (georeference of webmercator png )
		boundingBox, err = calculateWGS84BoundingBox(tile.Path)
		if err != nil {
			return hillshade, fmt.Errorf("error [%w] at calculateWGS84BoundingBox(), file: %s", err, tile.Path)
		}

		data, err = os.ReadFile(hillshadeWebmercatorPNG)
		if err != nil {
			return hillshade, fmt.Errorf("error [%w] at os.ReadFile()", err)
		}

	default:
		return hillshade, fmt.Errorf("unsupported format [%s]", outputFormat)
	}

	// set hillshade return structure
	hillshade.Data = data
	hillshade.DataFormat = outputFormat
	hillshade.Actuality = tile.Actuality
	hillshade.Origin = tile.Source
	hillshade.TileIndex = tile.Index
	hillshade.BoundingBox = boundingBox // only relevant for PNG

	// get attribution for resource
	attribution := "unknown"
	resource, err := getElevationResource(tile.Source)
	if err != nil {
		slog.Error("hillshade request: error getting elevation resource", "error", err, "source", tile.Source)
	} else {
		attribution = resource.Attribution
	}
	hillshade.Attribution = attribution

	return hillshade, nil
}
