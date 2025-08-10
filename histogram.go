package main

import (
	"bufio" // Added import for bufio.NewScanner
	"bytes"
	"compress/gzip"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math"
	"net/http"
	"os"
	"path/filepath"
	"sort" // Added import
	"strconv"
	"strings"
	"sync/atomic"
)

// Define the sentinel value to be excluded from histogram binning.
const noValueSentinel = -9999.0

/*
histogramRequest handles 'colorrelief request' from client.
*/
func histogramRequest(writer http.ResponseWriter, request *http.Request) {
	var histogramResponse = HistogramResponse{Type: TypeHistogramResponse, ID: "unknown"}
	histogramResponse.Attributes.IsError = true

	// statistics
	atomic.AddUint64(&HistogramRequests, 1)

	// limit overall request body size
	request.Body = http.MaxBytesReader(writer, request.Body, MaxHistogramRequestBodySize)

	// read request
	bodyData, err := io.ReadAll(request.Body)
	if err != nil {
		// check specifically for the error returned by MaxBytesReader
		var maxBytesErr *http.MaxBytesError
		if errors.As(err, &maxBytesErr) {
			slog.Warn("histogram request: request body too large", "limit", maxBytesErr.Limit, "ID", "unknown")
			histogramResponse.Attributes.Error.Code = "13000"
			histogramResponse.Attributes.Error.Title = "request body too large"
			histogramResponse.Attributes.Error.Detail = fmt.Sprintf("request body exceeds limit of %d bytes", maxBytesErr.Limit)
			buildHistogramResponse(writer, http.StatusRequestEntityTooLarge, histogramResponse)
		} else {
			// handle other read errors
			slog.Warn("histogram request: error reading request body", "error", err, "ID", "unknown")
			histogramResponse.Attributes.Error.Code = "13020"
			histogramResponse.Attributes.Error.Title = "error reading request body"
			histogramResponse.Attributes.Error.Detail = err.Error()
			buildHistogramResponse(writer, http.StatusBadRequest, histogramResponse)
		}
		return
	}

	// unmarshal request
	histogramRequest := HistogramRequest{}
	err = json.Unmarshal(bodyData, &histogramRequest)
	if err != nil {
		slog.Warn("histogram request: error unmarshaling request body", "error", err, "ID", "unknown")
		histogramResponse.Attributes.Error.Code = "13040"
		histogramResponse.Attributes.Error.Title = "error unmarshaling request body"
		histogramResponse.Attributes.Error.Detail = err.Error()
		buildHistogramResponse(writer, http.StatusBadRequest, histogramResponse)
		return
	}

	// verify request data
	err = verifyHistogramRequestData(request, histogramRequest)
	if err != nil {
		slog.Warn("histogram request: error verifying request data", "error", err, "ID", histogramRequest.ID)
		histogramResponse.Attributes.Error.Code = "13060"
		histogramResponse.Attributes.Error.Title = "error verifying request data"
		histogramResponse.Attributes.Error.Detail = err.Error()
		buildHistogramResponse(writer, http.StatusBadRequest, histogramResponse)
		return
	}

	zone := 0
	easting := 0.0
	northing := 0.0
	longitude := 0.0
	latitude := 0.0
	var tile TileMetadata
	var tiles []TileMetadata

	// determine type of coordinates
	if histogramRequest.Attributes.Zone != 0 {
		// input from UTM coordinates
		zone = histogramRequest.Attributes.Zone
		easting = histogramRequest.Attributes.Easting
		northing = histogramRequest.Attributes.Northing

		// get tile metadata for primary tile (e.g. "32_507_5491")
		tile, err = getGeotiffTile(easting, northing, zone, 1)
		if err != nil {
			slog.Warn("histogram request: error getting GeoTIFF tile for UTM coordinates", "error", err,
				"easting", easting, "northing", northing, "zone", zone, "ID", histogramRequest.ID)
			histogramResponse.Attributes.Error.Code = "13080"
			histogramResponse.Attributes.Error.Title = "getting GeoTIFF tile for UTM coordinates"
			histogramResponse.Attributes.Error.Detail = err.Error()
			buildHistogramResponse(writer, http.StatusBadRequest, histogramResponse)
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
		longitude = histogramRequest.Attributes.Longitude
		latitude = histogramRequest.Attributes.Latitude

		// get tile metadata for primary tile (e.g. "32_507_5491")
		tile, zone, easting, northing, err = getTileUTM(longitude, latitude)
		if err != nil {
			err = fmt.Errorf("error [%w] getting tile for coordinates lon: %.8f, lat: %.8f", err, longitude, latitude)
			slog.Warn("histogram request: error getting GeoTIFF tile for lon/lat coordinates", "error", err,
				"longitude", longitude, "latitude", latitude, "ID", histogramRequest.ID)
			histogramResponse.Attributes.Error.Code = "13100"
			histogramResponse.Attributes.Error.Title = "getting GeoTIFF tile for lon/lat coordinates"
			histogramResponse.Attributes.Error.Detail = err.Error()
			buildHistogramResponse(writer, http.StatusBadRequest, histogramResponse)
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

	// build histogram for all existing tiles
	for _, tile := range tiles {
		histogram, err := generateHistogramObjectForTile(tile, histogramRequest.Attributes.TypeOfVisualization,
			histogramRequest.Attributes.GradientAlgorithm, histogramRequest.Attributes.TypeOfHistogram,
			histogramRequest.Attributes.NumberOfBins, histogramRequest.Attributes.MinValue, histogramRequest.Attributes.MaxValue)
		if err != nil {
			slog.Warn("histogram request: error generating histogram object for tile", "error", err, "ID", histogramRequest.ID)
			// The error code from generateHistogramObjectForTile should be propagated or remapped
			// If the error originates from processHistogramData, it already has an error message.
			// Let's ensure the full detail is passed.
			histogramResponse.Attributes.Error.Code = "13120"
			histogramResponse.Attributes.Error.Title = "error generating histogram object for tile"
			histogramResponse.Attributes.Error.Detail = err.Error() // Use the detailed error from generateHistogramObjectForTile
			buildHistogramResponse(writer, http.StatusBadRequest, histogramResponse)
			return
		}
		histogramResponse.Attributes.Histograms = append(histogramResponse.Attributes.Histograms, histogram)
	}

	// copy request parameters into response
	histogramResponse.ID = histogramRequest.ID
	histogramResponse.Attributes.IsError = false
	histogramResponse.Attributes.Zone = histogramRequest.Attributes.Zone
	histogramResponse.Attributes.Easting = histogramRequest.Attributes.Easting
	histogramResponse.Attributes.Northing = histogramRequest.Attributes.Northing
	histogramResponse.Attributes.Longitude = histogramRequest.Attributes.Longitude
	histogramResponse.Attributes.Latitude = histogramRequest.Attributes.Latitude
	histogramResponse.Attributes.TypeOfVisualization = histogramRequest.Attributes.TypeOfVisualization
	histogramResponse.Attributes.GradientAlgorithm = histogramRequest.Attributes.GradientAlgorithm
	histogramResponse.Attributes.TypeOfHistogram = histogramRequest.Attributes.TypeOfHistogram
	histogramResponse.Attributes.NumberOfBins = histogramRequest.Attributes.NumberOfBins
	histogramResponse.Attributes.MinValue = histogramRequest.Attributes.MinValue
	histogramResponse.Attributes.MaxValue = histogramRequest.Attributes.MaxValue

	// success response
	buildHistogramResponse(writer, http.StatusOK, histogramResponse)
}

/*
verifyHistogramRequestData verifies 'Histogram' request data.
It performs several checks on the request data to ensure its validity.
*/
func verifyHistogramRequestData(request *http.Request, histogramRequest HistogramRequest) error {
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
	if histogramRequest.Type != TypeHistogramRequest {
		return fmt.Errorf("unexpected request Type [%v]", histogramRequest.Type)
	}

	// verify ID
	if len(histogramRequest.ID) > 1024 {
		return errors.New("ID must be 0-1024 characters long")
	}

	// verify coordinates (either utm or lon/lat coordinates must be set)
	if histogramRequest.Attributes.Zone == 0 && histogramRequest.Attributes.Longitude == 0 {
		return errors.New("either utm or lon/lat coordinates must be set")
	}

	// verify zone for Germany (Zone: 32 or 33)
	if histogramRequest.Attributes.Zone != 0 {
		if histogramRequest.Attributes.Zone < 32 || histogramRequest.Attributes.Zone > 33 {
			return errors.New("invalid zone for Germany")
		}
	}

	// verify longitude for Germany (Longitude: from  5.8663° E to 15.0419° E)
	if histogramRequest.Attributes.Longitude != 0 {
		if histogramRequest.Attributes.Longitude > 15.3 || histogramRequest.Attributes.Longitude < 5.5 {
			return errors.New("invalid longitude for Germany")
		}
	}

	// verify latitude for Germany (Latitude: from 47.2701° N to 55.0586° N)
	if histogramRequest.Attributes.Latitude != 0 {
		if histogramRequest.Attributes.Latitude > 55.3 || histogramRequest.Attributes.Latitude < 47.0 {
			return errors.New("invalid latitude for Germany")
		}
	}

	// verify type of visualization
	histogramRequest.Attributes.TypeOfVisualization = strings.ToLower(histogramRequest.Attributes.TypeOfVisualization)
	switch histogramRequest.Attributes.TypeOfVisualization {
	case "rawtif":
	case "slope":
	case "aspect":
	case "roughness":
	case "tri":
	case "tpi":
	default:
		return errors.New("type of visualization not supported (valid: rawtif, slope, aspect, roughness, tri, tpi)")
	}

	// verify gradient algorithm
	if histogramRequest.Attributes.TypeOfVisualization == "slope" || histogramRequest.Attributes.TypeOfVisualization == "aspec" {
		if !(histogramRequest.Attributes.GradientAlgorithm == "Horn" || histogramRequest.Attributes.GradientAlgorithm == "ZevenbergenThorne") {
			return errors.New("unsupported gradient algorithm (not Horn or ZevenbergenThorne)")
		}
	}

	// verify type of histogram
	histogramRequest.Attributes.TypeOfHistogram = strings.ToLower(histogramRequest.Attributes.TypeOfHistogram)
	switch histogramRequest.Attributes.TypeOfHistogram {
	case "standard":
	case "quantile":
	default:
		return errors.New("type of histogram not supported (valid: standard, quantile)")
	}

	// verify number of bins
	if histogramRequest.Attributes.NumberOfBins < 1 || histogramRequest.Attributes.NumberOfBins > 999 {
		return errors.New("number of bins not between 1 and 999")
	}

	// verify minimum value
	if histogramRequest.Attributes.MinValue != "" {
		_, err := strconv.ParseFloat(histogramRequest.Attributes.MinValue, 64)
		if err != nil {
			return errors.New("minimum value not valid")
		}
	}

	// verify maximum value
	if histogramRequest.Attributes.MaxValue != "" {
		_, err := strconv.ParseFloat(histogramRequest.Attributes.MaxValue, 64)
		if err != nil {
			return errors.New("maximum value not valid")
		}
	}

	return nil
}

/*
buildHistogramResponse builds HTTP responses with specified status and body.
It sets the Content-Type and Content-Length headers before writing the response body.
This function is used to construct consistent HTTP responses throughout the application.
*/
func buildHistogramResponse(writer http.ResponseWriter, httpStatus int, histogramResponse HistogramResponse) {
	// log limit length of body (e.g., the histogram objects as part of the body can be very large)
	maxBodyLength := 1024

	// CORS: allow requests from any origin
	writer.Header().Set("Access-Control-Allow-Origin", "*")
	// CORS: allowed methods
	writer.Header().Set("Access-Control-Allow-Methods", "POST")
	// CORS: allowed headers
	writer.Header().Set("Access-Control-Allow-Headers", "Content-Type")

	// marshal response
	body, err := json.MarshalIndent(histogramResponse, "", "  ")
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
		slog.Error("error at gz.Write()", "error", err)
		http.Error(writer, "Internal Server Error", http.StatusInternalServerError)
		return
	}

	err = gz.Close()
	if err != nil {
		slog.Error("error at gz.Close()", "error", err)
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
generateHistogramObjectForTile builds histogram object for given tile index.
*/
func generateHistogramObjectForTile(tile TileMetadata, typeOfVisualization string, gradientAlgorithm string,
	typeOfHistogram string, numberOfBins int, minValue string, maxValue string) (Histogram, error) {
	var histogram Histogram

	var commandExitStatus int
	var commandOutput []byte
	var err error

	// run operations in temp directory
	tempDir, err := os.MkdirTemp("", "dtm-elevation-service-histogram-")
	if err != nil {
		return histogram, fmt.Errorf("error [%w] at os.MkdirTemp()", err)
	}
	defer func() {
		_ = os.RemoveAll(tempDir)
	}()

	inputGeoTIFF := tile.Path
	histogramVisualization := filepath.Join(tempDir, tile.Index+".visualization")
	histogramVisualizationXYZ := filepath.Join(tempDir, tile.Index+".visualization.xyz")

	// build visulization
	switch strings.ToLower(typeOfVisualization) {
	case "rawtif":
		// For rawtif, the visualization is the input GeoTIFF itself, but we still need an XYZ for histogram
		histogramVisualization = inputGeoTIFF

	case "slope":
		commandExitStatus, commandOutput, err = runCommand("gdaldem", []string{"slope", inputGeoTIFF, histogramVisualization, "-alg", gradientAlgorithm, "-compute_edges"})
		if err != nil {
			return histogram, fmt.Errorf("error [%w: %d - %s] at runCommand()", err, commandExitStatus, commandOutput)
		}

	case "aspect":
		commandExitStatus, commandOutput, err = runCommand("gdaldem", []string{"aspect", inputGeoTIFF, histogramVisualization, "-alg", gradientAlgorithm, "-compute_edges"})
		if err != nil {
			return histogram, fmt.Errorf("error [%w: %d - %s] at runCommand()", err, commandExitStatus, commandOutput)
		}

	case "roughness":
		commandExitStatus, commandOutput, err = runCommand("gdaldem", []string{"roughness", inputGeoTIFF, histogramVisualization, "-compute_edges"})
		if err != nil {
			return histogram, fmt.Errorf("error [%w: %d - %s] at runCommand()", err, commandExitStatus, commandOutput)
		}

	case "tri":
		commandExitStatus, commandOutput, err = runCommand("gdaldem", []string{"TRI", inputGeoTIFF, histogramVisualization, "-alg", "Riley", "-compute_edges"})
		if err != nil {
			return histogram, fmt.Errorf("error [%w: %d - %s] at runCommand()", err, commandExitStatus, commandOutput)
		}

	case "tpi":
		commandExitStatus, commandOutput, err = runCommand("gdaldem", []string{"TPI", inputGeoTIFF, histogramVisualization, "-compute_edges"})
		if err != nil {
			return histogram, fmt.Errorf("error [%w: %d - %s] at runCommand()", err, commandExitStatus, commandOutput)
		}

	default:
		return histogram, fmt.Errorf("unsupported type of visualization [%s]", typeOfVisualization)
	}

	// build XYZ (text) file from visualization
	// e.g. gdal_translate -co DECIMAL_PRECISION=5 -of XYZ 32_497_5670_tri.utm.tif 32_497_5670_tri.utm.xyz
	commandExitStatus, commandOutput, err = runCommand("gdal_translate", []string{"-co", "DECIMAL_PRECISION=5", "-of", "XYZ", histogramVisualization, histogramVisualizationXYZ})
	if err != nil {
		return histogram, fmt.Errorf("error [%w: %d - %s] at runCommand()", err, commandExitStatus, commandOutput)
	}

	// Collect data for histogram
	allNonSentinelValues, noValueCount, totalParsedValues, err := collectAllNonSentinelValues(histogramVisualizationXYZ)
	if err != nil {
		return histogram, fmt.Errorf("error collecting data for histogram from '%s': %w", histogramVisualizationXYZ, err)
	}
	if totalParsedValues == 0 && noValueCount == 0 {
		return histogram, errors.New("no valid numeric data found in file for histogram calculation")
	}

	// calculate histogram
	statistic, entries, err := processHistogramData(allNonSentinelValues, noValueCount, totalParsedValues, typeOfHistogram, numberOfBins, minValue, maxValue)
	if err != nil {
		// Propagate detailed error for better debugging
		return histogram, fmt.Errorf("error processing histogram data: %w", err)
	}

	// set histogram return structure
	histogram.Statistic = statistic
	histogram.Entries = entries
	histogram.Actuality = tile.Actuality
	histogram.Origin = tile.Source
	histogram.TileIndex = tile.Index

	// get attribution for resource
	attribution := "unknown"
	resource, err := getElevationResource(tile.Source)
	if err != nil {
		slog.Error("histogram request: error getting elevation resource", "error", err, "source", tile.Source)
	} else {
		attribution = resource.Attribution
	}
	histogram.Attribution = attribution

	return histogram, nil
}

/*
collectAllNonSentinelValues reads the specified file and collects all  float64 values found in the third space-separated
column of each line, excluding the 'noValueSentinel'. It also counts total parsed values and sentinels.
*/
func collectAllNonSentinelValues(filePath string) (values []float64, noValueCount int, totalProcessedValues int, err error) {
	file, err := os.Open(filePath)
	if err != nil {
		return nil, 0, 0, fmt.Errorf("failed to open file '%s': %w", filePath, err)
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	values = make([]float64, 0)

	for scanner.Scan() {
		line := scanner.Text()
		parts := strings.Fields(line)

		if len(parts) < 3 {
			// Skip lines that do not have at least three columns.
			continue
		}

		val, parseErr := strconv.ParseFloat(parts[2], 64)
		if parseErr != nil {
			slog.Warn("histogram calculation: could not parse float from line", "line", line, "column", parts[2], "error", parseErr)
			continue
		}

		totalProcessedValues++

		if val == noValueSentinel {
			noValueCount++
		} else {
			values = append(values, val)
		}
	}

	if err = scanner.Err(); err != nil {
		return nil, 0, 0, fmt.Errorf("error reading file '%s' during data collection: %w", filePath, err)
	}

	return values, noValueCount, totalProcessedValues, nil
}

/*
findMinMaxFromValues finds the actual minimum and maximum from a slice of values.
*/
func findMinMaxFromValues(values []float64) (minVal, maxVal float64, err error) {
	if len(values) == 0 {
		return math.NaN(), math.NaN(), errors.New("no valid non-sentinel data available to determine min/max")
	}

	minVal = math.MaxFloat64
	maxVal = -math.MaxFloat64

	for _, val := range values {
		if val < minVal {
			minVal = val
		}
		if val > maxVal {
			maxVal = val
		}
	}
	return minVal, maxVal, nil
}

/*
calculateEqualWidthBins returns the upper bounds and an initialized slice for counts for equal-width bins.
*/
func calculateEqualWidthBins(minVal, maxVal float64, numBins int) ([]float64, []int) {
	bins := make([]float64, numBins)
	counts := make([]int, numBins) // Initialized to zero counts
	binWidth := (maxVal - minVal) / float64(numBins)

	for i := 0; i < numBins; i++ {
		bins[i] = minVal + float64(i+1)*binWidth
	}
	// Ensure the upper bound of the last bin is exactly 'max' to avoid floating point inaccuracies
	// This also correctly handles the [lower, upper] interval for the last bin.
	bins[numBins-1] = maxVal

	return bins, counts
}

/*
calculateQuantileBins returns the upper bounds and an initialized slice for counts for quantile bins.
valuesForQuantileCalc must be sorted.
*/
func calculateQuantileBins(valuesForQuantileCalc []float64, minHistVal, maxHistVal float64, numBins int) ([]float64, []int) {
	_ = minHistVal
	bins := make([]float64, numBins)
	counts := make([]int, numBins) // Initialized to zero counts

	numValues := len(valuesForQuantileCalc)
	if numValues == 0 || numBins == 0 {
		return bins, counts
	}

	// calculate bin boundaries based on quantiles
	for i := 0; i < numBins; i++ {
		// Calculate the index for the upper bound of the current bin. The index should be clamped to ensure it's within [0, numValues-1].
		idx := int(math.Floor(float64(numValues) * float64(i+1) / float64(numBins)))
		if idx >= numValues {
			idx = numValues - 1
		}
		if idx < 0 {
			idx = 0
		}
		bins[i] = valuesForQuantileCalc[idx]
	}

	// Explicitly set the upper bound of the last bin to the maxHistVal to ensure it includes the highest value and correct floating point issues.
	bins[numBins-1] = maxHistVal

	return bins, counts
}

/*
processHistogramData performs the core histogram calculation based on provided values and parameters.
*/
func processHistogramData(allNonSentinelValues []float64, noValueCount int, totalParsedValues int, typeOfHistogram string, numberOfBins int, minValueStr string, maxValueStr string) (HistogramStatistic, []HistogramEntry, error) {
	var statistic HistogramStatistic
	var entries []HistogramEntry

	// set initial statistic values
	statistic.NoValueCount = noValueCount
	statistic.ValuesTotal = totalParsedValues

	// determine overall true min/max from all non-sentinel values
	overallTrueMin, overallTrueMax, err := findMinMaxFromValues(allNonSentinelValues)
	if err != nil {
		if len(allNonSentinelValues) == 0 {
			// if no non-sentinel values, all counts related to histogram are zero
			statistic.MinValueAbsolute = math.NaN()
			statistic.MaxValueAbsolute = math.NaN()
			statistic.MinValueHistogram = math.NaN()
			statistic.MaxValueHistogram = math.NaN()
			statistic.NoValuePercent = 100.0 // all values are no-value if totalParsedValues > 0
			if totalParsedValues == 0 {      // if file was empty or only non-parsable lines
				statistic.NoValuePercent = 0.0 // no values, so no 'no-value'
			}
			statistic.AboveHistogramMaxCount = 0
			statistic.BelowHistogramMinCount = 0
			statistic.AboveHistogramMaxPercent = 0.0
			statistic.BelowHistogramMinPercent = 0.0
			return statistic, entries, nil // no histogram to build
		}
		return statistic, entries, fmt.Errorf("could not determine overall true min/max from collected data: %w", err)
	}

	statistic.MinValueAbsolute = overallTrueMin
	statistic.MaxValueAbsolute = overallTrueMax

	userMinProvided := minValueStr != ""
	userMaxProvided := maxValueStr != ""

	var minUserVal float64
	if userMinProvided {
		minUserVal, err = strconv.ParseFloat(minValueStr, 64)
		if err != nil {
			return statistic, entries, fmt.Errorf("invalid minimum value provided: %w", err)
		}
	} else {
		minUserVal = math.NaN() // use NaN to indicate not provided
	}

	var maxUserVal float64
	if userMaxProvided {
		maxUserVal, err = strconv.ParseFloat(maxValueStr, 64)
		if err != nil {
			return statistic, entries, fmt.Errorf("invalid maximum value provided: %w", err)
		}
	} else {
		maxUserVal = math.NaN() // use NaN to indicate not provided
	}

	// preliminary check for explicit user-defined range validity
	if userMinProvided && userMaxProvided && minUserVal >= maxUserVal {
		return statistic, entries, fmt.Errorf("user-defined minimum value (%f) must be less than maximum value (%f)", minUserVal, maxUserVal)
	}

	var effectiveMinVal, effectiveMaxVal float64
	var binUpperBounds []float64
	var tempBinCounts []int // temporary slice to hold bin counts during population

	if typeOfHistogram == "quantile" {
		slog.Debug("histogram calculation: quantile mode selected")
		var valuesForQuantileCalc []float64 // values actually used for quantile boundary determination

		// determine the *filter* range for quantile calculation based on user input or overall data
		filterMin := overallTrueMin
		filterMax := overallTrueMax

		if userMinProvided {
			filterMin = minUserVal
		}
		if userMaxProvided {
			filterMax = maxUserVal
		}

		// ensure the determined filter range is valid before filtering data
		if filterMin >= filterMax {
			return statistic, entries, fmt.Errorf("effective quantile calculation range minimum (%f) must be less than maximum (%f)", filterMin, filterMax)
		}

		// filter values based on the determined filter range for quantile calculation
		for _, val := range allNonSentinelValues {
			if val >= filterMin && val <= filterMax {
				valuesForQuantileCalc = append(valuesForQuantileCalc, val)
			}
		}

		if len(valuesForQuantileCalc) == 0 {
			// If no values within filter range for quantile calculation, the histogram will be empty. Set histogram min/max to the filter range, and other counts to zero.
			statistic.MinValueHistogram = filterMin
			statistic.MaxValueHistogram = filterMax
			// Values outside the filter range (which is now the histogram range) count towards below/above. Re-evaluate lessThanMinCount and greaterThanMaxCount against filterMin/Max.
			for _, val := range allNonSentinelValues {
				if val < filterMin {
					statistic.BelowHistogramMinCount++
				} else if val > filterMax {
					statistic.AboveHistogramMaxCount++
				}
			}
			if totalParsedValues > 0 {
				statistic.BelowHistogramMinPercent = (float64(statistic.BelowHistogramMinCount) / float64(totalParsedValues)) * 100
				statistic.AboveHistogramMaxPercent = (float64(statistic.AboveHistogramMaxCount) / float64(totalParsedValues)) * 100
			}
			return statistic, entries, nil
		}

		sort.Float64s(valuesForQuantileCalc) // quantile calculation requires sorted data

		// The actual histogram range for quantile mode is based on the min/max of the *filtered* data.
		effectiveMinVal = valuesForQuantileCalc[0]
		effectiveMaxVal = valuesForQuantileCalc[len(valuesForQuantileCalc)-1]

		// handle identical min/max values for quantile mode after filtering
		if effectiveMinVal == effectiveMaxVal {
			adjustment := 0.0001 * math.Abs(effectiveMinVal)
			if adjustment == 0 {
				adjustment = 0.0001
			}
			effectiveMinVal -= adjustment
			effectiveMaxVal += adjustment
			slog.Warn("histogram calculation: all valid data for quantile histogram within the selected range are identical; adjusted range to allow bin creation",
				"adjustedMin", effectiveMinVal, "adjustedMax", effectiveMaxVal)
		}

		binUpperBounds, tempBinCounts = calculateQuantileBins(valuesForQuantileCalc, effectiveMinVal, effectiveMaxVal, numberOfBins)

	} else { // equal-width mode
		slog.Debug("histogram calculation: equal-width mode selected")

		// determine effectiveMinVal and effectiveMaxVal based on user input or auto-detection
		effectiveMinVal = overallTrueMin // default to overall min/max
		effectiveMaxVal = overallTrueMax

		switch {
		case userMinProvided && userMaxProvided:
			effectiveMinVal = minUserVal
			effectiveMaxVal = maxUserVal
		case userMinProvided:
			effectiveMinVal = minUserVal
		case userMaxProvided:
			effectiveMaxVal = maxUserVal
		default:
			// already defaulted to overallTrueMin/Max
		}

		// handle identical min/max values for equal-width mode (after determination)
		if effectiveMinVal == effectiveMaxVal {
			adjustment := 0.0001 * math.Abs(effectiveMinVal)
			if adjustment == 0 {
				adjustment = 0.0001
			}
			effectiveMinVal -= adjustment
			effectiveMaxVal += adjustment
			slog.Warn("histogram calculation: determined histogram range is identical; adjusted range to allow bin creation",
				"adjustedMin", effectiveMinVal, "adjustedMax", effectiveMaxVal)
		}

		// final check to ensure a valid range for histogram creation
		if effectiveMinVal >= effectiveMaxVal {
			return statistic, entries, fmt.Errorf("effective histogram minimum value (%f) must be less than maximum value (%f)", effectiveMinVal, effectiveMaxVal)
		}
		binUpperBounds, tempBinCounts = calculateEqualWidthBins(effectiveMinVal, effectiveMaxVal, numberOfBins)
	}

	statistic.MinValueHistogram = effectiveMinVal
	statistic.MaxValueHistogram = effectiveMaxVal

	// populate histogram and count special values (common for both modes)
	for _, val := range allNonSentinelValues {
		switch {
		case val < effectiveMinVal:
			statistic.BelowHistogramMinCount++
		case val > effectiveMaxVal:
			statistic.AboveHistogramMaxCount++
		default:
			// The value is within the chosen range of the histogram. Find the corresponding bin.
			idx := -1
			if val == effectiveMaxVal { // special handling for the maximum value to fall into the last bin
				idx = numberOfBins - 1
			} else {
				// use binary search or linear scan to find the bin index
				for i := 0; i < numberOfBins; i++ {
					if val < binUpperBounds[i] {
						idx = i
						break
					}
				}
			}
			if idx != -1 {
				tempBinCounts[idx]++
			} else {
				slog.Warn("histogram calculation: value within effective range not binned (internal error)", "value", val, "min_hist", effectiveMinVal, "max_hist", effectiveMaxVal)
			}
		}
	}

	// calculate percentages
	totalNonSentinel := float64(totalParsedValues)
	if totalNonSentinel > 0 {
		statistic.NoValuePercent = (float64(noValueCount) / totalNonSentinel) * 100
		statistic.BelowHistogramMinPercent = (float64(statistic.BelowHistogramMinCount) / totalNonSentinel) * 100
		statistic.AboveHistogramMaxPercent = (float64(statistic.AboveHistogramMaxCount) / totalNonSentinel) * 100
	}

	// prepare HistogramEntry objects
	totalBinnedCount := 0
	for _, count := range tempBinCounts {
		totalBinnedCount += count
	}

	currentLowerBound := effectiveMinVal
	for i := 0; i < numberOfBins; i++ {
		entry := HistogramEntry{
			LowerBound: currentLowerBound,
			UpperBound: binUpperBounds[i], // this will be adjusted for the last bin explicitly
			BinCount:   tempBinCounts[i],
		}
		if totalBinnedCount > 0 {
			entry.BinPercent = (float64(tempBinCounts[i]) / float64(totalBinnedCount)) * 100
		}
		entries = append(entries, entry)
		currentLowerBound = binUpperBounds[i] // lower bound for the next bin is current upper bound
	}

	// adjust the upper bound of the very last bin to be exactly effectiveMaxVal
	if len(entries) > 0 {
		entries[len(entries)-1].UpperBound = effectiveMaxVal
	}

	return statistic, entries, nil
}
