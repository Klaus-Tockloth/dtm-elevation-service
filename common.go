package main

import (
	"bufio"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"os"
	"os/exec"
	"strings"
	"syscall"
	"time"
	"unicode"
)

// --------------------------------------------------------------------------------
// Constants.
// --------------------------------------------------------------------------------

// HTTP Accept headers
const (
	JSONAPIMediaType   = "application/json; charset=utf-8"
	TextPlainMediaType = "text/html; charset=utf-8"
)

// JSON API types
const (
	TypePointRequest       = "PointRequest"
	TypePointResponse      = "PointResponse"
	TypeUTMPointRequest    = "UTMPointRequest"
	TypeUTMPointResponse   = "UTMPointResponse"
	TypeGPXRequest         = "GPXRequest"
	TypeGPXResponse        = "GPXResponse"
	TypeGPXAnalyzeRequest  = "GPXAnalyzeRequest"
	TypeGPXAnalyzeResponse = "GPXAnalyzeResponse"
	TypeContoursRequest    = "ContoursRequest"
	TypeContoursResponse   = "ContoursResponse"
	TypeHillshadeRequest   = "HillshadeRequest"
	TypeHillshadeResponse  = "HillshadeResponse"
	TypeSlopeRequest       = "SlopeRequest"
	TypeSlopeResponse      = "SlopeResponse"
	TypeAspectRequest      = "AspectRequest"
	TypeAspectResponse     = "AspectResponse"
	TypeTPIRequest         = "TPIRequest"
	TypeTPIResponse        = "TPIResponse"
	TypeTRIRequest         = "TRIRequest"
	TypeTRIResponse        = "TRIResponse"
	TypeRoughnessRequest   = "RoughnessRequest"
	TypeRoughnessResponse  = "RoughnessResponse"
	TypeRawTIFRequest      = "RawTIFRequest"
	TypeRawTIFResponse     = "RawTIFResponse"
)

// request body limits (in bytes, for security reasons)
const (
	MaxPointRequestBodySize      = 4 * 1024
	MaxGpxRequestBodySize        = 24 * 1024 * 1024
	MaxGpxAnalyzeRequestBodySize = 24 * 1024 * 1024
	MaxContoursRequestBodySize   = 4 * 1024
	MaxHillshadeRequestBodySize  = 4 * 1024
	MaxSlopeRequestBodySize      = 16 * 1024
	MaxAspectRequestBodySize     = 16 * 1024
	MaxTPIRequestBodySize        = 16 * 1024
	MaxTRIRequestBodySize        = 16 * 1024
	MaxRoughnessRequestBodySize  = 16 * 1024
	MaxRawTIFRequestBodySize     = 4 * 1024
)

// ErrorObject represents error details.
type ErrorObject struct {
	Code   string
	Title  string
	Detail string
}

// ElevationSource represents elevation source (according to ISO 3166-2).
type ElevationSource struct {
	Code        string // e.g. DE-NW
	Name        string // e.g. Nordrhein-Westfalen
	Attribution string // e.g. © GeoBasis-DE / LGLN (2025), cc-by/4.0
}

var elevationSources = []ElevationSource{
	{Code: "DE-BW", Name: "Baden-Württemberg", Attribution: "© GeoBasis-DE / LGL-BW (2025), dl-de/by-2-0"},
	{Code: "DE-BY", Name: "Bayern", Attribution: "Datenquelle: Bayerische Vermessungsverwaltung – geodaten.bayern.de, cc-by/4.0"},
	{Code: "DE-BE", Name: "Berlin", Attribution: "siehe Brandenburg"},
	{Code: "DE-BB", Name: "Brandenburg", Attribution: "© GeoBasis-DE / LGB, dl-de/by-2-0"},
	{Code: "DE-HB", Name: "Bremen", Attribution: "Quellenvermerk: Landesamt GeoInformation Bremen, cc-by/4.0, Quelle verändert"},
	{Code: "DE-HH", Name: "Hamburg", Attribution: "Quellenvermerk: Freie und Hansestadt Hamburg, Landesbetrieb Geoinformation und Vermessung (LGV), dl-de/by-2-0"},
	{Code: "DE-HE", Name: "Hessen", Attribution: "Geobasisdaten © Hessische Verwaltung für Bodenmanagement und Geoinformation, dl-de/by-2-0"},
	{Code: "DE-MV", Name: "Mecklenburg-Vorpommern", Attribution: "© GeoBasis-DE/MV (2025), dl-de/by-2-0, Quelle verändert"},
	{Code: "DE-NI", Name: "Niedersachsen", Attribution: "© GeoBasis-DE / LGLN (2025), cc-by/4.0"},
	{Code: "DE-NW", Name: "Nordrhein-Westfalen", Attribution: "© GeoBasis-DE / NRW (2025), dl-de/by-2-0"},
	{Code: "DE-RP", Name: "Rheinland-Pfalz", Attribution: "© GeoBasis-DE / LVermGeoRP (2025), dl-de/by-2-0"},
	{Code: "DE-SL", Name: "Saarland", Attribution: "© GeoBasis DE/LVGL-SL (2025), dl-de/by-2-0"},
	{Code: "DE-SN", Name: "Sachsen", Attribution: "© GeoBasis-DE / GeoSN (2025), dl-de/by-2-0"},
	{Code: "DE-ST", Name: "Sachsen-Anhalt", Attribution: "© GeoBasis-DE / LVermGeo ST, dl-de/by-2-0, Quelle verändert"},
	{Code: "DE-SH", Name: "Schleswig-Holstein", Attribution: "© GeoBasis-DE / LVermGeo SH, cc-by/4.0, Quelle verändert"},
	{Code: "DE-TH", Name: "Thüringen", Attribution: "© GDI-Th (2025), dl-de/by-2-0"},
}

// WGS84BoundingBox represents min/max longitude and latitude coordinates in WGS84.
type WGS84BoundingBox struct {
	MinLon float64
	MaxLon float64
	MinLat float64
	MaxLat float64
}

//
// --------------------------------------------------------------------------------
// Request  : Client -> PointRequest  -> Service
// Response : Client <- PointResponse <- Service
// --------------------------------------------------------------------------------

// PointRequest represents lon/lat coordinates for point request.
type PointRequest struct {
	Type       string
	ID         string
	Attributes struct {
		Longitude float64
		Latitude  float64
	}
}

// PointResponse represents elevation for point response.
type PointResponse struct {
	Type       string
	ID         string
	Attributes struct {
		Longitude   float64
		Latitude    float64
		Elevation   float64
		Actuality   string
		Origin      string
		Attribution string
		TileIndex   string
		IsError     bool
		Error       ErrorObject
	}
}

// --------------------------------------------------------------------------------
// Request  : Client -> UTMPointRequest  -> Service
// Response : Client <- UTMPointResponse <- Service
// --------------------------------------------------------------------------------

// UTMPointRequest represents UTM coordinates for utm point request.
type UTMPointRequest struct {
	Type       string
	ID         string
	Attributes struct {
		Zone     int
		Easting  float64
		Northing float64
	}
}

// UTMPointResponse represents elevation for utm point response.
type UTMPointResponse struct {
	Type       string
	ID         string
	Attributes struct {
		Zone        int
		Easting     float64
		Northing    float64
		Elevation   float64
		Actuality   string
		Origin      string
		Attribution string
		TileIndex   string
		IsError     bool
		Error       ErrorObject
	}
}

// --------------------------------------------------------------------------------
// Request  : Client -> GPXRequest  -> Service
// Response : Client <- GPXResponse <- Service
// --------------------------------------------------------------------------------

// GPXRequest represents GPX data for GPX request.
type GPXRequest struct {
	Type       string
	ID         string
	Attributes struct {
		GPXData string // base64 encoded GPX XML string
	}
}

// GPXResponse represents modified GPX data for GPX response.
type GPXResponse struct {
	Type       string
	ID         string
	Attributes struct {
		GPXData      string // base64 encoded GPX XML string
		GPXPoints    int
		DGMPoints    int
		Attributions []string
		IsError      bool
		Error        ErrorObject
	}
}

// --------------------------------------------------------------------------------
// Request  : Client -> GPXAnalyzeRequest  -> Service
// Response : Client <- GPXAnalyzeResponse <- Service
// --------------------------------------------------------------------------------

// GpxAnalyzeResult holds all processed data from a GPX file.
type GpxAnalyzeResult struct {
	Version     string
	Name        string
	Description string
	Creator     string
	Time        *time.Time
	TotalPoints int
	Tracks      []GpxAnalyzeTrackResult
}

// GpxAnalyzeTrackResult holds data for a single track.
type GpxAnalyzeTrackResult struct {
	Name        string
	Comment     string
	Description string
	Source      string
	Type        string
	Segments    []GpxAnalyzeSegmentResult
}

// GpxAnalyzeSegmentResult holds all calculated statistics for a single segment.
type GpxAnalyzeSegmentResult struct {
	// General
	StartTime time.Time
	EndTime   time.Time
	Duration  float64
	Points    int
	Length2D  float64
	Length3D  float64
	// Moving
	MovingTime      float64
	StoppedTime     float64
	MovingDistance  float64
	StoppedDistance float64
	// Bounding Box
	MaxLatitude  float64
	MaxLongitude float64
	MinLatitude  float64
	MinLongitude float64
	// Elevation
	UphillWMA          float64 // Weighted Moving Average
	DownhillWMA        float64
	UphillUnfiltered   float64
	DownhillUnfiltered float64
	// Point Details for verbose output
	PointDetails []GpxAnalyzePointDetail
}

// GpxAnalyzePointDetail holds detailed information for a single track point.
type GpxAnalyzePointDetail struct {
	Timestamp          time.Time
	TimeDifference     int64 // in seconds from previous point
	Latitude           float64
	Longitude          float64
	Distance           float64 // in meters from previous point
	Elevation          float64
	CumulativeUphill   float64
	CumulativeDownhill float64
}

// GPXAnalyzeRequest represents GPX data for GPX analyze request.
type GPXAnalyzeRequest struct {
	Type       string
	ID         string
	Attributes struct {
		GPXData string // base64 encoded GPX XML string
	}
}

// GPXAnalyzeResponse represents modified GPX data for GPX analyze response.
type GPXAnalyzeResponse struct {
	Type       string
	ID         string
	Attributes struct {
		GPXData          string // base64 encoded GPX XML string
		GpxAnalyzeResult GpxAnalyzeResult
		IsError          bool
		Error            ErrorObject
	}
}

// --------------------------------------------------------------------------------
// Request  : Client -> ContoursRequest  -> Service
// Response : Client <- ContoursResponse <- Service
// --------------------------------------------------------------------------------

// ContoursRequest represents coordinates, format and equidistance for contours request.
type ContoursRequest struct {
	Type       string
	ID         string
	Attributes struct {
		Zone         int
		Easting      float64
		Northing     float64
		Longitude    float64
		Latitude     float64
		Equidistance float64
	}
}

// Contour represents compressed contours lines for one tile.
type Contour struct {
	Data        []byte
	DataFormat  string
	Actuality   string
	Origin      string
	Attribution string
	TileIndex   string
}

// ContoursResponse represents contours objects for contours response.
type ContoursResponse struct {
	Type       string
	ID         string
	Attributes struct {
		Zone         int
		Easting      float64
		Northing     float64
		Longitude    float64
		Latitude     float64
		Equidistance float64
		Contours     []Contour
		IsError      bool
		Error        ErrorObject
	}
}

// --------------------------------------------------------------------------------
// Request  : Client -> HillshadeRequest  -> Service
// Response : Client <- HillshadeResponse <- Service
// --------------------------------------------------------------------------------

// HillshadeRequest represents coordinates and settings for hillshade request.
type HillshadeRequest struct {
	Type       string
	ID         string
	Attributes struct {
		Zone                 int
		Easting              float64
		Northing             float64
		Longitude            float64
		Latitude             float64
		GradientAlgorithm    string // Horn, ZevenbergenThorne
		VerticalExaggeration float64
		AzimuthOfLight       uint
		AltitudeOfLight      uint
		ShadingVariant       string // regular, combined, multidirectional, igor
	}
}

// Hillshade represents compressed hillshade object (PNG  or GeoRawTIFF) for one tile.
type Hillshade struct {
	Data        []byte
	DataFormat  string
	Actuality   string
	Origin      string
	Attribution string
	TileIndex   string
	BoundingBox WGS84BoundingBox
}

// HillshadeResponse represents hillshade objects for hillshade response.
type HillshadeResponse struct {
	Type       string
	ID         string
	Attributes struct {
		Zone                 int
		Easting              float64
		Northing             float64
		Longitude            float64
		Latitude             float64
		GradientAlgorithm    string
		VerticalExaggeration float64
		AzimuthOfLight       uint
		AltitudeOfLight      uint
		ShadingVariant       string
		Hillshades           []Hillshade
		IsError              bool
		Error                ErrorObject
	}
}

// --------------------------------------------------------------------------------
// Request  : Client -> SlopeRequest  -> Service
// Response : Client <- SlopeResponse <- Service
// --------------------------------------------------------------------------------

// SlopeRequest represents coordinates and settings for slope request.
type SlopeRequest struct {
	Type       string
	ID         string
	Attributes struct {
		Zone                 int
		Easting              float64
		Northing             float64
		Longitude            float64
		Latitude             float64
		GradientAlgorithm    string // Horn, ZevenbergenThorne
		ColorTextFileContent []string
		ColoringAlgorithm    string // interpolation, rounding
	}
}

// Slope represents compressed slope object (PNG  or GeoRawTIFF) for one tile.
type Slope struct {
	Data        []byte
	DataFormat  string
	Actuality   string
	Origin      string
	Attribution string
	TileIndex   string
	BoundingBox WGS84BoundingBox
}

// SlopeResponse represents slope objects for slope response.
type SlopeResponse struct {
	Type       string
	ID         string
	Attributes struct {
		Zone                 int
		Easting              float64
		Northing             float64
		Longitude            float64
		Latitude             float64
		GradientAlgorithm    string
		ColorTextFileContent []string
		ColoringAlgorithm    string // interpolation, rounding
		Slopes               []Slope
		IsError              bool
		Error                ErrorObject
	}
}

// --------------------------------------------------------------------------------
// Request  : Client -> AspectRequest  -> Service
// Response : Client <- AspectResponse <- Service
// --------------------------------------------------------------------------------

// AspectRequest represents coordinates and settings for aspect request.
type AspectRequest struct {
	Type       string
	ID         string
	Attributes struct {
		Zone                 int
		Easting              float64
		Northing             float64
		Longitude            float64
		Latitude             float64
		GradientAlgorithm    string // Horn, ZevenbergenThorne
		ColorTextFileContent []string
		ColoringAlgorithm    string // interpolation, rounding
	}
}

// Aspect represents compressed slope object (PNG  or GeoRawTIFF) for one tile.
type Aspect struct {
	Data        []byte
	DataFormat  string
	Actuality   string
	Origin      string
	Attribution string
	TileIndex   string
	BoundingBox WGS84BoundingBox
}

// AspectResponse represents slope objects for aspect response.
type AspectResponse struct {
	Type       string
	ID         string
	Attributes struct {
		Zone                 int
		Easting              float64
		Northing             float64
		Longitude            float64
		Latitude             float64
		GradientAlgorithm    string
		ColorTextFileContent []string
		ColoringAlgorithm    string // interpolation, rounding
		Aspects              []Aspect
		IsError              bool
		Error                ErrorObject
	}
}

// --------------------------------------------------------------------------------
// Request  : Client -> TPIRequest  -> Service
// Response : Client <- TPIResponse <- Service
// --------------------------------------------------------------------------------

// TPIRequest represents coordinates and settings for TPI request.
type TPIRequest struct {
	Type       string
	ID         string
	Attributes struct {
		Zone                 int
		Easting              float64
		Northing             float64
		Longitude            float64
		Latitude             float64
		ColorTextFileContent []string
		ColoringAlgorithm    string // interpolation, rounding
	}
}

// TPI represents compressed TPI object (PNG  or GeoRawTIFF) for one tile.
type TPI struct {
	Data        []byte
	DataFormat  string
	Actuality   string
	Origin      string
	Attribution string
	TileIndex   string
	BoundingBox WGS84BoundingBox
}

// TPIResponse represents TPI objects for aspect response.
type TPIResponse struct {
	Type       string
	ID         string
	Attributes struct {
		Zone                 int
		Easting              float64
		Northing             float64
		Longitude            float64
		Latitude             float64
		ColorTextFileContent []string
		ColoringAlgorithm    string // interpolation, rounding
		TPIs                 []TPI
		IsError              bool
		Error                ErrorObject
	}
}

// --------------------------------------------------------------------------------
// Request  : Client -> TRIRequest  -> Service
// Response : Client <- TRIResponse <- Service
// --------------------------------------------------------------------------------

// TRIRequest represents coordinates and settings for TRI request.
type TRIRequest struct {
	Type       string
	ID         string
	Attributes struct {
		Zone                 int
		Easting              float64
		Northing             float64
		Longitude            float64
		Latitude             float64
		ColorTextFileContent []string
		ColoringAlgorithm    string // interpolation, rounding
	}
}

// TRI represents compressed TRI object (PNG  or GeoRawTIFF) for one tile.
type TRI struct {
	Data        []byte
	DataFormat  string
	Actuality   string
	Origin      string
	Attribution string
	TileIndex   string
	BoundingBox WGS84BoundingBox
}

// TRIResponse represents TRI objects for aspect response.
type TRIResponse struct {
	Type       string
	ID         string
	Attributes struct {
		Zone                 int
		Easting              float64
		Northing             float64
		Longitude            float64
		Latitude             float64
		ColorTextFileContent []string
		ColoringAlgorithm    string // interpolation, rounding
		TRIs                 []TRI
		IsError              bool
		Error                ErrorObject
	}
}

// --------------------------------------------------------------------------------
// Request  : Client -> RoughnessRequest  -> Service
// Response : Client <- RoughnessResponse <- Service
// --------------------------------------------------------------------------------

// RoughnessRequest represents coordinates and settings for Roughness request.
type RoughnessRequest struct {
	Type       string
	ID         string
	Attributes struct {
		Zone                 int
		Easting              float64
		Northing             float64
		Longitude            float64
		Latitude             float64
		ColorTextFileContent []string
		ColoringAlgorithm    string // interpolation, rounding
	}
}

// Roughness represents compressed Roughness object (PNG  or GeoRawTIFF) for one tile.
type Roughness struct {
	Data        []byte
	DataFormat  string
	Actuality   string
	Origin      string
	Attribution string
	TileIndex   string
	BoundingBox WGS84BoundingBox
}

// RoughnessResponse represents slope objects for RI response.
type RoughnessResponse struct {
	Type       string
	ID         string
	Attributes struct {
		Zone                 int
		Easting              float64
		Northing             float64
		Longitude            float64
		Latitude             float64
		ColorTextFileContent []string
		ColoringAlgorithm    string // interpolation, rounding
		Roughnesses          []Roughness
		IsError              bool
		Error                ErrorObject
	}
}

// --------------------------------------------------------------------------------
// Request  : Client -> RawTIFRequest  -> Service
// Response : Client <- RawTIFResponse <- Service
// --------------------------------------------------------------------------------

// RawTIFRequest represents coordinates and settings for RawTIF request.
type RawTIFRequest struct {
	Type       string
	ID         string
	Attributes struct {
		Zone     int
		Easting  float64
		Northing float64
	}
}

// RawTIF represents compressed RawTIF object for one tile.
type RawTIF struct {
	Data        []byte
	DataFormat  string
	Actuality   string
	Origin      string
	Attribution string
	TileIndex   string
}

// RawTIFResponse represents RawTIF objects for RawTIF response.
type RawTIFResponse struct {
	Type       string
	ID         string
	Attributes struct {
		Zone     int
		Easting  float64
		Northing float64
		RawTIFs  []RawTIF
		IsError  bool
		Error    ErrorObject
	}
}

/*
FileExists checks if a file already exists.
It returns true if the file exists, and false otherwise.
*/
func FileExists(filename string) bool {
	info, err := os.Stat(filename)
	if os.IsNotExist(err) {
		return false
	}
	// check if it's actually a file and not a directory
	return !info.IsDir()
}

/*
getGeoTiffTile gets GeoTIFF tile for given UTM coordinates.
Tile variants:
1 = primary tile (from state)
2 = secondary tile (from state neighbor 1)
3 = tertiary tile (from state neighbor 2)
*/
func getGeotiffTile(easting float64, northing float64, zone int, tileVariant int) (TileMetadata, error) {
	// calculate hash value (for 1000 x 1000 m grid)
	eastingPrefix := int(math.Floor(easting / 1000.0))
	northingPrefix := int(math.Floor(northing / 1000.0))

	var hash string
	if tileVariant == 1 {
		hash = fmt.Sprintf("%d_%d_%d", zone, eastingPrefix, northingPrefix)
	} else {
		hash = fmt.Sprintf("%d_%d_%d_%d", zone, eastingPrefix, northingPrefix, tileVariant)
	}

	// get tile resource (GeoTIFF file)
	tile, found := Repository[hash]
	if !found {
		return TileMetadata{}, fmt.Errorf("tile [%s] not found", hash)
	}

	return tile, nil
}

/*
getElevationResource gets elevation source for given county-state code.
*/
func getElevationResource(code string) (ElevationSource, error) {
	for _, resource := range elevationSources {
		if resource.Code == code {
			return resource, nil
		}
	}
	return ElevationSource{}, fmt.Errorf("elevation source for country-statecode [%s] not found", code)
}

/*
getElevationForPoint retrieves the elevation and source metadata for a given lat/lon coordinate.
It encapsulates the logic used in pointRequest for reuse.
*/
func getElevationForPoint(longitude, latitude float64) (float64, TileMetadata, error) {
	var elevation float64
	var tile TileMetadata
	var err error
	var zone int
	var x float64
	var y float64

	// lookup for tile (primary tile / variant 1, e.g. 32_437_5614)
	tile, zone, x, y, err = getTileUTM(longitude, latitude)
	if err != nil {
		err = fmt.Errorf("error [%w] getting tile for coordinates lon: %.8f, lat: %.8f", err, longitude, latitude)
		return elevation, tile, err
	}

	// retrieve elevation
	elevation, err = getElevationFromUTM(x, y, tile.Path)
	if err != nil {
		err = fmt.Errorf("error [%w] getting elevation from GeoRawTIFF [%s] for UTM easting: %.3f, northing: %.3f, zone: %d", err, tile.Path, x, y, zone)
		return elevation, tile, err
	}

	// -9999.0 = no data
	if elevation < -9998.9 {
		// lookup for tile (secondary tile / variant 2, e.g. '32_437_5614_2')
		tile, err = getGeotiffTile(x, y, zone, 2)
		if err != nil {
			err = fmt.Errorf("error [%w] getting GeoRawTIFF tile for UTM easting: %.3f, northing: %.3f, zone: %d", err, x, y, zone)
			return elevation, tile, err
		}

		// retrieve elevation
		elevation, err = getElevationFromUTM(x, y, tile.Path)
		if err != nil {
			err = fmt.Errorf("error [%w] getting elevation from GeoRawTIFF [%s] for UTM easting: %.3f, northing: %.3f, zone: %d", err, tile.Path, x, y, zone)
			return elevation, tile, err
		}

		// -9999.0 = no data
		if elevation < -9998.9 {
			// lookup for tile (tertiary tile / variant 3, e.g. '32_437_5614_3')
			tile, err = getGeotiffTile(x, y, zone, 3)
			if err != nil {
				err = fmt.Errorf("error [%w] getting GeoRawTIFF tile for UTM easting: %.3f, northing: %.3f, zone: %d", err, x, y, zone)
				return elevation, tile, err
			}

			// retrieve elevation
			elevation, err = getElevationFromUTM(x, y, tile.Path)
			if err != nil {
				err = fmt.Errorf("error [%w] getting elevation from GeoRawTIFF [%s] for UTM easting: %.3f, northing: %.3f, zone: %d", err, tile.Path, x, y, zone)
				return elevation, tile, err
			}
		}
	}

	// success
	return elevation, tile, nil
}

// @formatter:off
/*
getTileUTM gets tile hash index and UTM coordinates (zone, x, y) for given lon/lat coordinates.

UTM zones of german states:
                          (6° - 12°)         (12° - 18°)
State   	              UTM Zone 32        UTM Zone 33      DGM1 data            Remarks
----------------------    -----------        -----------      ----------           -------------------
Baden-Württemberg	 	  Yes                No               32                   -
Bayern                    Yes                Yes              32                   all areas treated as in zone 32
Berlin                    No                 Yes              see Brandenburg      -
Brandenburg               Yes                Yes              33                   all areas treated as in zone 33
Bremen                    Yes                No               32                   -
Hamburg                   Yes                No               32                   -
Hessen                    Yes                No               32                   -
Mecklenburg-Vorpommern    Yes                Yes              33                   all areas treated as in zone 33
Niedersachsen             Yes                No               32                   -
Nordrhein-Westfalen       Yes                No               32                   small parts in zone 31, all areas treated as in zone 32
Rheinland-Pfalz           Yes                No               32                   -
Saarland                  Yes                No               32                   -
Sachsen                   Yes                Yes              33                   all areas treated as in zone 33
Sachsen-Anhalt            Yes                Yes              32                   all areas treated as in zone 32
Schleswig-Holstein        Yes                No               32                   -
Thüringen                 Yes                Yes              32                   all areas treated as in zone 32
*/
// @formatter:on
func getTileUTM(longitude, latitude float64) (TileMetadata, int, float64, float64, error) {
	var tile TileMetadata
	var err error
	var zone int
	var neighborZone int
	var targetEPSG int
	var neighborTargetEPSG int
	var x float64
	var y float64

	// derive primary and neighbor zone from longitude
	switch {
	case longitude >= 6.0 && longitude < 12.0:
		zone = 32
		targetEPSG = 25832
		if longitude >= 9.0 {
			// e.g. small area of Brandenburg
			neighborZone = 33
			neighborTargetEPSG = 25833
		} else {
			// not expected for Germany
			neighborZone = 31
			neighborTargetEPSG = 25831
		}
	case longitude >= 12.0 && longitude < 18.0:
		zone = 33
		targetEPSG = 25833
		if longitude >= 15.0 {
			// not expected for Germany
			neighborZone = 34
			neighborTargetEPSG = 25834
		} else {
			neighborZone = 32
			neighborTargetEPSG = 25832
		}
	case longitude >= 0.0 && longitude < 6.0:
		zone = 31
		targetEPSG = 25831
		if longitude >= 3.0 {
			// e.g. small area of Nordrhein-Westfalen
			neighborZone = 32
			neighborTargetEPSG = 25832
		} else {
			// not expected for Germany
			neighborZone = 30
			neighborTargetEPSG = 25830
		}
	default:
		return tile, 0, 0.0, 0.0, fmt.Errorf("invalid longitude [%.8f]", longitude)
	}

	// lookup in primary zone
	x, y, err = transformLonLatToUTM(longitude, latitude, targetEPSG)
	if err != nil {
		err = fmt.Errorf("error [%w] transforming coordinates lon: %.8f, lat: %.8f to EPSG:%d", err, longitude, latitude, targetEPSG)
		return tile, 0, 0.0, 0.0, err
	}
	tile, err = getGeotiffTile(x, y, zone, 1)
	if err == nil {
		// tile in primary zone found
		return tile, zone, x, y, nil
	}

	// lookup in neighbor zone
	x, y, err = transformLonLatToUTM(longitude, latitude, neighborTargetEPSG)
	if err != nil {
		err = fmt.Errorf("error [%w] transforming coordinates lon: %.8f, lat: %.8f to EPSG:%d", err, longitude, latitude, targetEPSG)
		return tile, 0, 0.0, 0.0, err
	}
	tile, err = getGeotiffTile(x, y, neighborZone, 1)
	if err != nil {
		err = fmt.Errorf("error [%w] getting GeoRawTIFF tile for UTM easting: %.3f, northing: %.3f, zone: %d", err, x, y, zone)
		return tile, 0, 0.0, 0.0, err
	}

	// tile in neighbor zone found
	return tile, neighborZone, x, y, nil
}

/*
getElevationForUTMPoint retrieves the elevation and source metadata for a given UTM coordinate.
It encapsulates the logic used in pointRequest for reuse.
*/
func getElevationForUTMPoint(zone int, easting, northing float64) (float64, TileMetadata, error) {
	var elevation float64
	var tile TileMetadata
	var err error

	// lookup for tile (primary tile / variant 1, e.g. 32_437_5614)
	tile, err = getGeotiffTile(easting, northing, zone, 1)
	if err != nil {
		return -8888.0, tile, fmt.Errorf("tile not found")
	}

	// retrieve elevation
	elevation, err = getElevationFromUTM(easting, northing, tile.Path)
	if err != nil {
		err = fmt.Errorf("error [%w] getting elevation from GeoRawTIFF [%s] for UTM easting: %.3f, northing: %.3f, zone: %d", err, tile.Path, easting, northing, zone)
		return elevation, tile, err
	}

	// -9999.0 = no data
	if elevation < -9998.9 {
		// lookup for tile (secondary tile / variant 2, e.g. '32_437_5614_2')
		tile, err = getGeotiffTile(easting, northing, zone, 2)
		if err != nil {
			err = fmt.Errorf("error [%w] getting GeoRawTIFF tile for UTM easting: %.3f, northing: %.3f, zone: %d", err, easting, northing, zone)
			return elevation, tile, err
		}

		// retrieve elevation
		elevation, err = getElevationFromUTM(easting, northing, tile.Path)
		if err != nil {
			err = fmt.Errorf("error [%w] getting elevation from GeoRawTIFF [%s] for UTM easting: %.3f, northing: %.3f, zone: %d", err, tile.Path, easting, northing, zone)
			return elevation, tile, err
		}

		// -9999.0 = no data
		if elevation < -9998.9 {
			// lookup for tile (tertiary tile / variant 3, e.g. '32_437_5614_3')
			tile, err = getGeotiffTile(easting, northing, zone, 3)
			if err != nil {
				err = fmt.Errorf("error [%w] getting GeoRawTIFF tile for UTM easting: %.3f, northing: %.3f, zone: %d", err, easting, northing, zone)
				return elevation, tile, err
			}

			// retrieve elevation
			elevation, err = getElevationFromUTM(easting, northing, tile.Path)
			if err != nil {
				err = fmt.Errorf("error [%w] getting elevation from GeoRawTIFF [%s] for UTM easting: %.3f, northing: %.3f, zone: %d", err, tile.Path, easting, northing, zone)
				return elevation, tile, err
			}
		}
	}

	// success
	return elevation, tile, nil
}

/*
runCommand runs a command or program.
*/
func runCommand(program string, args []string) (commandExitStatus int, commandOutput []byte, err error) {
	cmd := exec.Command(program, args...)
	commandOutput, err = cmd.CombinedOutput()

	// full command for logging
	fullCommand := program + " " + strings.Join(cmd.Args, " ")
	//	fmt.Printf("Full command: %v\n", fullCommand)

	var waitStatus syscall.WaitStatus
	if err != nil {
		// command was not successful
		if exitError, ok := err.(*exec.ExitError); ok {
			// command fails because of an unsuccessful exit code
			waitStatus = exitError.Sys().(syscall.WaitStatus)
			slog.Error("program exit code", "exit code", waitStatus.ExitStatus())
		}
		slog.Error("unexpected error at cmd.CombinedOutput()", "error", err)
		slog.Error("program (not successful)", "program/command", fullCommand)
		if len(commandOutput) > 0 {
			slog.Info("program output (stdout, stderr)", "output", string(commandOutput))
		}
	} else {
		// command was successful
		waitStatus = cmd.ProcessState.Sys().(syscall.WaitStatus)
		/*
			slog.Info("program (successful)", "program/command", fullCommand)
			slog.Info("program exit code", "exit code", waitStatus.ExitStatus())
			if len(commandOutput) > 0 {
				slog.Info("program output (stdout, stderr)", "output", string(commandOutput))
			}
		*/
	}

	commandExitStatus = waitStatus.ExitStatus()
	return
}

/*
verifyColorTextFileContent checks the content of a text file, passed as a slice of strings.
- The total content size must not exceed 12 KB.
- The content must only contain printable characters.
- The content must consist of multiple lines.
*/
func verifyColorTextFileContent(filecontent []string) error {
	const maxFileSize = 12 * 1024 // 12 KB

	// Check if the filecontent contains more than one line.
	if len(filecontent) <= 1 {
		return errors.New("color text file must contain multiple lines")
	}

	var totalSize int
	// Iterate through each line of the filecontent.
	for _, line := range filecontent {
		// Adds the length of the line and one byte for the newline character to the total size.
		totalSize += len(line) + 1

		// Iterate through each character of the line.
		for _, char := range line {
			// unicode.IsPrint checks if a character is printable.
			// Printable characters include letters, numbers, punctuation, symbols, and the ASCII space character.
			if !unicode.IsPrint(char) {
				return fmt.Errorf("invalid, non-printable character found in color text file: %q", char)
			}
		}
	}

	// Since the last line does not have a trailing newline, one byte is subtracted.
	if len(filecontent) > 0 {
		totalSize--
	}

	// Check if the total size exceeds the maximum of 12 KB.
	if totalSize > maxFileSize {
		return fmt.Errorf("color text file with size of %d bytes exceeds the maximum of %d bytes", totalSize, maxFileSize)
	}

	return nil
}

/*
createColorTextFile creates 'color-text-file' from given content passed as a slice of strings.
*/
func createColorTextFile(filename string, filecontent []string) error {
	file, err := os.Create(filename)
	if err != nil {
		return err
	}
	defer file.Close()

	writer := bufio.NewWriter(file)
	for _, line := range filecontent {
		_, err := writer.WriteString(line + "\n")
		if err != nil {
			return err
		}
	}

	err = writer.Flush()
	if err != nil {
		return err
	}
	return nil
}
