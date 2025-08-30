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
	"strings"
	"sync/atomic"
)

/*
rawtifRequest handles 'rawtif request' from client.
*/
func rawtifRequest(writer http.ResponseWriter, request *http.Request) {
	var rawtifResponse = RawTIFResponse{Type: TypeRawTIFResponse, ID: "unknown"}
	rawtifResponse.Attributes.IsError = true

	// statistics
	atomic.AddUint64(&RawTIFRequests, 1)

	// limit overall request body size
	request.Body = http.MaxBytesReader(writer, request.Body, MaxRawTIFRequestBodySize)

	// read request
	bodyData, err := io.ReadAll(request.Body)
	if err != nil {
		// check specifically for the error returned by MaxBytesReader
		var maxBytesErr *http.MaxBytesError
		if errors.As(err, &maxBytesErr) {
			slog.Warn("rawtif request: request body too large", "limit", maxBytesErr.Limit, "ID", "unknown")
			rawtifResponse.Attributes.Error.Code = "11000"
			rawtifResponse.Attributes.Error.Title = "request body too large"
			rawtifResponse.Attributes.Error.Detail = fmt.Sprintf("request body exceeds limit of %d bytes", maxBytesErr.Limit)
			buildRawTIFResponse(writer, http.StatusRequestEntityTooLarge, rawtifResponse)
		} else {
			// handle other read errors
			slog.Warn("rawtif request: error reading request body", "error", err, "ID", "unknown")
			rawtifResponse.Attributes.Error.Code = "11020"
			rawtifResponse.Attributes.Error.Title = "error reading request body"
			rawtifResponse.Attributes.Error.Detail = err.Error()
			buildRawTIFResponse(writer, http.StatusBadRequest, rawtifResponse)
		}
		return
	}

	// unmarshal request
	rawtifRequest := RawTIFRequest{}
	err = json.Unmarshal(bodyData, &rawtifRequest)
	if err != nil {
		slog.Warn("rawtif request: error unmarshaling request body", "error", err, "ID", "unknown")
		rawtifResponse.Attributes.Error.Code = "11040"
		rawtifResponse.Attributes.Error.Title = "error unmarshaling request body"
		rawtifResponse.Attributes.Error.Detail = err.Error()
		buildRawTIFResponse(writer, http.StatusBadRequest, rawtifResponse)
		return
	}

	// verify request data
	err = verifyRawTIFRequestData(request, rawtifRequest)
	if err != nil {
		slog.Warn("rawtif request: error verifying request data", "error", err, "ID", rawtifRequest.ID)
		rawtifResponse.Attributes.Error.Code = "11060"
		rawtifResponse.Attributes.Error.Title = "error verifying request data"
		rawtifResponse.Attributes.Error.Detail = err.Error()
		buildRawTIFResponse(writer, http.StatusBadRequest, rawtifResponse)
		return
	}

	zone := 0
	easting := 0.0
	northing := 0.0
	var tiles []TileMetadata

	// input from UTM coordinates
	zone = rawtifRequest.Attributes.Zone
	easting = rawtifRequest.Attributes.Easting
	northing = rawtifRequest.Attributes.Northing

	// get all tiles (metadata) for given UTM coordinates
	tiles, err = getAllTilesUTM(zone, easting, northing)
	if err != nil {
		slog.Warn("rawtif request: error getting GeoTIFF tile for UTM coordinates", "error", err,
			"easting", easting, "northing", northing, "zone", zone, "ID", rawtifRequest.ID)
		rawtifResponse.Attributes.Error.Code = "11080"
		rawtifResponse.Attributes.Error.Title = "getting GeoTIFF tile for UTM coordinates"
		rawtifResponse.Attributes.Error.Detail = err.Error()
		buildRawTIFResponse(writer, http.StatusBadRequest, rawtifResponse)
		return
	}

	// build rawtif for all existing tiles
	for _, tile := range tiles {
		rawtif, err := generateRawTIFObjectForTile(tile)
		if err != nil {
			slog.Warn("rawtif request: error generating rawtif object for tile", "error", err, "ID", rawtifRequest.ID)
			rawtifResponse.Attributes.Error.Code = "11120"
			rawtifResponse.Attributes.Error.Title = "error generating rawtif object for tile"
			rawtifResponse.Attributes.Error.Detail = err.Error()
			buildRawTIFResponse(writer, http.StatusBadRequest, rawtifResponse)
			return
		}
		rawtifResponse.Attributes.RawTIFs = append(rawtifResponse.Attributes.RawTIFs, rawtif)
	}

	// copy request parameters into response
	rawtifResponse.ID = rawtifRequest.ID
	rawtifResponse.Attributes.IsError = false
	rawtifResponse.Attributes.Zone = rawtifRequest.Attributes.Zone
	rawtifResponse.Attributes.Easting = rawtifRequest.Attributes.Easting
	rawtifResponse.Attributes.Northing = rawtifRequest.Attributes.Northing

	// success response
	buildRawTIFResponse(writer, http.StatusOK, rawtifResponse)
}

/*
verifyRawTIFRequestData verifies 'rawtif' request data.
It performs several checks on the request data to ensure its validity.
*/
func verifyRawTIFRequestData(request *http.Request, rawtifRequest RawTIFRequest) error {
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
	if rawtifRequest.Type != TypeRawTIFRequest {
		return fmt.Errorf("unexpected request Type [%v]", rawtifRequest.Type)
	}

	// verify ID
	if len(rawtifRequest.ID) > 1024 {
		return errors.New("ID must be 0-1024 characters long")
	}

	// verify zone for Germany (Zone: 32 or 33)
	if rawtifRequest.Attributes.Zone != 0 {
		if rawtifRequest.Attributes.Zone < 32 || rawtifRequest.Attributes.Zone > 33 {
			return errors.New("invalid zone for Germany")
		}
	}

	return nil
}

/*
buildRawTIFResponse builds HTTP responses with specified status and body.
It sets the Content-Type and Content-Length headers before writing the response body.
This function is used to construct consistent HTTP responses throughout the application.
*/
func buildRawTIFResponse(writer http.ResponseWriter, httpStatus int, rawtifResponse RawTIFResponse) {
	// log limit length of body (e.g., the rawtif objects as part of the body can be very large)
	maxBodyLength := 1024

	// CORS: allow requests from any origin
	writer.Header().Set("Access-Control-Allow-Origin", "*")
	// CORS: allowed methods
	writer.Header().Set("Access-Control-Allow-Methods", "POST")
	// CORS: allowed headers
	writer.Header().Set("Access-Control-Allow-Headers", "Content-Type")

	// marshal response
	body, err := json.MarshalIndent(rawtifResponse, "", "  ")
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
generateRawTIFObjectForTile builds rawtif object for given tile index.
*/
func generateRawTIFObjectForTile(tile TileMetadata) (RawTIF, error) {
	var rawtif RawTIF

	// read tile data
	data, err := os.ReadFile(tile.Path)
	if err != nil {
		return rawtif, fmt.Errorf("error [%w] reading tile data", err)
	}

	// set RawTIF return structure
	rawtif.Data = data
	rawtif.DataFormat = "GeoTIFF"
	rawtif.Actuality = tile.Actuality
	rawtif.Origin = tile.Source
	rawtif.TileIndex = tile.Index

	// get attribution for resource
	attribution := "unknown"
	resource, err := getElevationResource(tile.Source)
	if err != nil {
		slog.Error("rawtif request: error getting elevation resource", "error", err, "source", tile.Source)
	} else {
		attribution = resource.Attribution
	}
	rawtif.Attribution = attribution

	return rawtif, nil
}
