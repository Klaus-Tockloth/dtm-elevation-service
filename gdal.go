package main

import (
	"fmt"
	"math"

	"github.com/airbusgeo/godal"
)

/*
transformLonLatToUTM transforms lon/lat coordinates (WGS84, EPSG:4326) to the given UTM zone.
*/
func transformLonLatToUTM(lon, lat float64, targetEPSG int) (x, y float64, err error) {
	// define source: WGS84 (EPSG:4326)
	sourceSRS, err := godal.NewSpatialRefFromEPSG(4326)
	if err != nil {
		err = fmt.Errorf("error creating source SRS (EPSG:4326): %w", err)
		return
	}
	defer sourceSRS.Close()

	// define target: dynamically calculated UTM Zone (e.g. 32632 for Zone 32N)
	targetSRS, err := godal.NewSpatialRefFromEPSG(targetEPSG)
	if err != nil {
		err = fmt.Errorf("error creating target SRS (EPSG:%d): %w", targetEPSG, err)
		return
	}
	defer targetSRS.Close()

	transform, err := godal.NewTransform(sourceSRS, targetSRS)
	if err != nil {
		err = fmt.Errorf("error creating coordinate transformation from EPSG:4326 to EPSG:%d: %w", targetEPSG, err)
		return
	}
	defer transform.Close()

	// define transformation parameters (e.g., slices of coordinates)
	xCoords := []float64{lon} // longitude in WGS84
	yCoords := []float64{lat} // latitude in WGS84
	zCoords := []float64{}    // elevation (optional)
	numPoints := len(xCoords)
	successFlags := make([]bool, numPoints)

	// perform transformation
	err = transform.TransformEx(xCoords, yCoords, zCoords, successFlags)
	if err != nil {
		err = fmt.Errorf("error during coordinate transformation: %w", err)
		return
	}

	// check success
	if !successFlags[0] {
		err = fmt.Errorf("transformation from EPSG:4326 to EPSG:%d failed for coordinates (%.8f, %.8f)", targetEPSG, lon, lat)
		return
	}

	// assign results to return variables
	x = xCoords[0]
	y = yCoords[0]

	return // return named results (x, y, err)
}

/*
getElevationFromUTM retrieves the elevation value from a GeoTIFF DGM file for a given UTM coordinate.

Input:
  - xUTM, yUTM: The UTM coordinates (Easting, Northing).
    These coordinates MUST be in the SAME Coordinate Reference System (CRS) as the provided GeoTIFF file.
  - filename: Path to the GeoTIFF file containing elevation data (e.g., DGM1).

Output:
- elevation: The elevation value at the specified coordinates (typically in meters).
- err: if
  - the file cannot be opened
  - the coordinates are outside the file's extent
  - the coordinate system is rotated (not supported by this simple implementation),
  - the pixel value is the NoData value
  - or any other reading error occurs.
*/
func getElevationFromUTM(xUTM, yUTM float64, filename string) (elevation float64, err error) {
	// check if file exists
	if !FileExists(filename) {
		err = fmt.Errorf("file [%s] does not exist", filename)
		return
	}

	// open the raster file in ReadOnly mode
	dataset, err := godal.Open(filename)
	if err != nil {
		err = fmt.Errorf("error opening file [%s]: %w", filename, err)
		return
	}
	defer dataset.Close()

	// get geotransform parameters
	gt, err := dataset.GeoTransform()
	if err != nil {
		err = fmt.Errorf("error getting geotransform from [%s]: %w", filename, err)
		return
	}

	// basic check for rotation / skewing (this implementation assumes a north-up image)
	// gt[2] and gt[4] should be 0 for a standard non-rotated/non-skewed grid
	if gt[2] != 0.0 || gt[4] != 0.0 {
		err = fmt.Errorf("raster [%s] appears to be rotated or skewed (gt[2]=%f, gt[4]=%f)", filename, gt[2], gt[4])
		return
	}

	// calculate pixel coordinates from UTM coordinates using the inverse geotransform
	// For non-rotated images:
	// xUTM = gt[0] + col * gt[1] + row * gt[2]  (gt[2] is 0)
	// yUTM = gt[3] + col * gt[4] + row * gt[5]  (gt[4] is 0)
	// --> col = (xUTM - gt[0]) / gt[1]
	// --> row = (yUTM - gt[3]) / gt[5]
	// Note: Pixel height gt[5] is usually negative.

	if gt[1] == 0 || gt[5] == 0 {
		err = fmt.Errorf("invalid geotransform: pixel width (gt[1]=%f) or height (gt[5]=%f) is zero", gt[1], gt[5])
		return
	}

	colF := (xUTM - gt[0]) / gt[1]
	rowF := (yUTM - gt[3]) / gt[5]

	// get raster size
	structure := dataset.Structure()
	rasterWidth := structure.SizeX
	rasterHeight := structure.SizeY

	// convert float pixel coordinates to integer indices (top-left corner of the pixel)
	col := int(math.Floor(colF))
	row := int(math.Floor(rowF))

	// check if the calculated pixel coordinates are within the raster bounds
	if col < 0 || col >= rasterWidth || row < 0 || row >= rasterHeight {
		err = fmt.Errorf("coordinate (%.3f, %.3f) is outside the raster bounds [%s] (pixel %d, %d)", xUTM, yUTM, filename, col, row)
		return
	}

	// get the first raster band (assuming elevation is in the first band)
	bands := dataset.Bands()
	if len(bands) == 0 {
		err = fmt.Errorf("no raster bands found in file [%s]", filename)
		return
	}
	band := bands[0]
	bandStructure := band.Structure()

	// read the single pixel value at (col, row); create a buffer of appropriate data type to hold the pixel value
	var pixelValue float64 // use float64 for intermediate storage

	switch bandStructure.DataType {
	case godal.Byte:
		buffer := make([]byte, 1)
		if err = band.Read(col, row, buffer, 1, 1); err != nil {
			err = fmt.Errorf("error reading pixel (%d, %d) as Byte: %w", col, row, err)
			return
		}
		pixelValue = float64(buffer[0])
	case godal.Int16:
		buffer := make([]int16, 1)
		if err = band.Read(col, row, buffer, 1, 1); err != nil {
			err = fmt.Errorf("error reading pixel (%d, %d) as Int16: %w", col, row, err)
			return
		}
		pixelValue = float64(buffer[0])
	case godal.UInt16:
		buffer := make([]uint16, 1)
		if err = band.Read(col, row, buffer, 1, 1); err != nil {
			err = fmt.Errorf("error reading pixel (%d, %d) as UInt16: %w", col, row, err)
			return
		}
		pixelValue = float64(buffer[0])
	case godal.Int32:
		buffer := make([]int32, 1)
		if err = band.Read(col, row, buffer, 1, 1); err != nil {
			err = fmt.Errorf("error reading pixel (%d, %d) as Int32: %w", col, row, err)
			return
		}
		pixelValue = float64(buffer[0])
	case godal.UInt32:
		buffer := make([]uint32, 1)
		if err = band.Read(col, row, buffer, 1, 1); err != nil {
			err = fmt.Errorf("error reading pixel (%d, %d) as UInt32: %w", col, row, err)
			return
		}
		pixelValue = float64(buffer[0])
	case godal.Float32:
		buffer := make([]float32, 1)
		if err = band.Read(col, row, buffer, 1, 1); err != nil {
			err = fmt.Errorf("error reading pixel (%d, %d) as Float32: %w", col, row, err)
			return
		}
		pixelValue = float64(buffer[0])
	case godal.Float64:
		buffer := make([]float64, 1)
		if err = band.Read(col, row, buffer, 1, 1); err != nil {
			err = fmt.Errorf("error reading pixel (%d, %d) as Float64: %w", col, row, err)
			return
		}
		pixelValue = buffer[0]
	default:
		err = fmt.Errorf("unsupported data type '%s' for band 1 in file [%s]", bandStructure.DataType, filename)
		return
	}

	// check if the read value is the NoData value
	if nodata, ok := band.NoData(); ok {
		// compare floating point numbers with a small tolerance if needed, but direct comparison often works for NoData values
		if pixelValue == nodata {
			err = fmt.Errorf("coordinate (%.3f, %.3f) corresponds to a NoData value (%.3f) in [%s]", xUTM, yUTM, nodata, filename)
			return
		}
	}

	// assign the result to the return variable
	elevation = pixelValue

	return // return named results (elevation, err)
}

/*
calculateWGS84BoundingBox takes a GeoTIFF filename and calculates the bounding box in
WGS84 (Lon/Lat). It assumes the input file has a defined spatial reference system.
*/
func calculateWGS84BoundingBox(filename string) (WGS84BoundingBox, error) {
	latLonBBox := WGS84BoundingBox{}

	dataset, err := godal.Open(filename)
	if err != nil {
		return latLonBBox, fmt.Errorf("error [%w] at godal.Open(), file %s", err, filename)
	}
	defer dataset.Close()

	// get dataset structure (for size)
	structure := dataset.Structure()
	sizeX := float64(structure.SizeX)
	sizeY := float64(structure.SizeY)

	// get geotransformation
	gt, err := dataset.GeoTransform()
	if err != nil {
		return latLonBBox, fmt.Errorf("error [%w] at dataset.GeoTransform()", err)
	}

	// calculate corner coordinates in the source projection
	// Corner coordinates correspond to the outer edges of the corner pixels.
	// Pixel (0,0) is the upper-left corner.
	// The formula is: X_proj = GT[0] + pixel * GT[1] + line * GT[2]
	//                 Y_proj = GT[3] + pixel * GT[4] + line * GT[5]

	// upper-left corner (pixel 0, 0)
	ulX := gt[0]
	ulY := gt[3]

	// upper-right corner (pixel sizeX, 0)
	urX := gt[0] + sizeX*gt[1] + 0*gt[2]
	urY := gt[3] + sizeX*gt[4] + 0*gt[5]

	// lower-left corner (pixel 0, sizeY)
	llX := gt[0] + 0*gt[1] + sizeY*gt[2]
	llY := gt[3] + 0*gt[4] + sizeY*gt[5]

	// lower-right corner (pixel sizeX, sizeY)
	lrX := gt[0] + sizeX*gt[1] + sizeY*gt[2]
	lrY := gt[3] + sizeX*gt[4] + sizeY*gt[5]

	// store the 4 source corner points in slices for easy processing
	srcXCoords := []float64{ulX, urX, llX, lrX}
	srcYCoords := []float64{ulY, urY, llY, lrY}

	// ----- transform to WGS84 (Lon/Lat) -----

	// get source Spatial Reference System (SRS)
	srcSRS := dataset.SpatialRef()
	if srcSRS == nil {
		return latLonBBox, fmt.Errorf("source Spatial Reference System (SRS) not found, transformation not possible")
	}
	defer srcSRS.Close()

	// create target Spatial Reference System (WGS84, EPSG:4326)
	tgtSRS, err := godal.NewSpatialRefFromEPSG(4326)
	if err != nil {
		return latLonBBox, fmt.Errorf("error [%s] at godal.NewSpatialRefFromEPSG(4326)", err)
	}
	defer tgtSRS.Close()

	// create the coordinate transformation transformer [2]
	transformer, err := godal.NewTransform(srcSRS, tgtSRS)
	if err != nil {
		return latLonBBox, fmt.Errorf("error [%s] at godal.NewTransform()", err)
	}
	defer transformer.Close()

	// transform the source corner coordinates to WGS84 (godal.TransformEx transforms the slices in-place)
	latLonXCoords := make([]float64, 4) // will contain Lon
	latLonYCoords := make([]float64, 4) // will contain Lat
	copy(latLonXCoords, srcXCoords)     // copy source X coordinates
	copy(latLonYCoords, srcYCoords)     // copy source Y coordinates

	// slice for successful transformations for each point
	successful := make([]bool, 4)

	// perform the transformation
	// TransformEx expects x, y (and optional z) slices and a slice for success status.
	// After transformation, latLonXCoords will contain Longitude and latLonYCoords will contain Latitude.
	err = transformer.TransformEx(latLonXCoords, latLonYCoords, nil, successful) // nil for Z-coordinates if not needed
	if err != nil {
		return latLonBBox, fmt.Errorf("error [%w] at transformer.TransformEx()", err)
	}

	// calculate the Lat/Lon bounding box (min/max of transformed corner coordinates)
	latLonBBox.MinLon = math.Inf(1)  // positive infinity (Lon)
	latLonBBox.MaxLon = math.Inf(-1) // negative infinity (Lon)
	latLonBBox.MinLat = math.Inf(1)  // positive infinity (Lat)
	latLonBBox.MaxLat = math.Inf(-1) // negative infinity (Lat)

	for i := 0; i < 4; i++ {
		if successful[i] {
			// latLonXCoords[i] is now Longitude, latLonYCoords[i] is now Latitude
			latLonBBox.MinLon = math.Min(latLonBBox.MinLon, latLonXCoords[i]) // min Longitude
			latLonBBox.MaxLon = math.Max(latLonBBox.MaxLon, latLonXCoords[i]) // max Longitude
			latLonBBox.MinLat = math.Min(latLonBBox.MinLat, latLonYCoords[i]) // min Latitude
			latLonBBox.MaxLat = math.Max(latLonBBox.MaxLat, latLonYCoords[i]) // max Latitude
		} else {
			return latLonBBox, fmt.Errorf("point %d could not be transformed to WGS84", i)
		}
	}

	return latLonBBox, nil
}
