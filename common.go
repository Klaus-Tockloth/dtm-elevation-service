package main

import (
	"fmt"
	"math"
	"os"
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
	TypePointRequest     = "PointRequest"
	TypePointResponse    = "PointResponse"
	TypeUTMPointRequest  = "UTMPointRequest"
	TypeUTMPointResponse = "UTMPointResponse"
	TypeGPXRequest       = "GPXRequest"
	TypeGPXResponse      = "GPXResponse"
)

// request body limits (in bytes, for security reasons)
const (
	MaxPointRequestBodySize = 4 * 1024
	MaxGpxRequestBodySize   = 24 * 1024 * 1024
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
		GPXData string // Base64 encoded GPX XML string
	}
}

// GPXResponse represents modified GPX data for GPX response.
type GPXResponse struct {
	Type       string
	ID         string
	Attributes struct {
		GPXData      string // Base64 encoded GPX XML string
		GPXPoints    int
		DGMPoints    int
		Attributions []string
		IsError      bool
		Error        ErrorObject
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
		err = fmt.Errorf("error [%w] getting elevation from GeoTIFF [%s] for UTM easting: %.3f, northing: %.3f, zone: %d", err, tile.Path, x, y, zone)
		return elevation, tile, err
	}

	// -9999.0 = no data
	if elevation < -9998.9 {
		// lookup for tile (secondary tile / variant 2, e.g. '32_437_5614_2')
		tile, err = getGeotiffTile(x, y, zone, 2)
		if err != nil {
			err = fmt.Errorf("error [%w] getting GeoTIFF tile for UTM easting: %.3f, northing: %.3f, zone: %d", err, x, y, zone)
			return elevation, tile, err
		}

		// retrieve elevation
		elevation, err = getElevationFromUTM(x, y, tile.Path)
		if err != nil {
			err = fmt.Errorf("error [%w] getting elevation from GeoTIFF [%s] for UTM easting: %.3f, northing: %.3f, zone: %d", err, tile.Path, x, y, zone)
			return elevation, tile, err
		}

		// -9999.0 = no data
		if elevation < -9998.9 {
			// lookup for tile (tertiary tile / variant 3, e.g. '32_437_5614_3')
			tile, err = getGeotiffTile(x, y, zone, 3)
			if err != nil {
				err = fmt.Errorf("error [%w] getting GeoTIFF tile for UTM easting: %.3f, northing: %.3f, zone: %d", err, x, y, zone)
				return elevation, tile, err
			}

			// retrieve elevation
			elevation, err = getElevationFromUTM(x, y, tile.Path)
			if err != nil {
				err = fmt.Errorf("error [%w] getting elevation from GeoTIFF [%s] for UTM easting: %.3f, northing: %.3f, zone: %d", err, tile.Path, x, y, zone)
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
		err = fmt.Errorf("error [%w] getting GeoTIFF tile for UTM easting: %.3f, northing: %.3f, zone: %d", err, x, y, zone)
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
		err = fmt.Errorf("error [%w] getting elevation from GeoTIFF [%s] for UTM easting: %.3f, northing: %.3f, zone: %d", err, tile.Path, easting, northing, zone)
		return elevation, tile, err
	}

	// -9999.0 = no data
	if elevation < -9998.9 {
		// lookup for tile (secondary tile / variant 2, e.g. '32_437_5614_2')
		tile, err = getGeotiffTile(easting, northing, zone, 2)
		if err != nil {
			err = fmt.Errorf("error [%w] getting GeoTIFF tile for UTM easting: %.3f, northing: %.3f, zone: %d", err, easting, northing, zone)
			return elevation, tile, err
		}

		// retrieve elevation
		elevation, err = getElevationFromUTM(easting, northing, tile.Path)
		if err != nil {
			err = fmt.Errorf("error [%w] getting elevation from GeoTIFF [%s] for UTM easting: %.3f, northing: %.3f, zone: %d", err, tile.Path, easting, northing, zone)
			return elevation, tile, err
		}

		// -9999.0 = no data
		if elevation < -9998.9 {
			// lookup for tile (tertiary tile / variant 3, e.g. '32_437_5614_3')
			tile, err = getGeotiffTile(easting, northing, zone, 3)
			if err != nil {
				err = fmt.Errorf("error [%w] getting GeoTIFF tile for UTM easting: %.3f, northing: %.3f, zone: %d", err, easting, northing, zone)
				return elevation, tile, err
			}

			// retrieve elevation
			elevation, err = getElevationFromUTM(easting, northing, tile.Path)
			if err != nil {
				err = fmt.Errorf("error [%w] getting elevation from GeoTIFF [%s] for UTM easting: %.3f, northing: %.3f, zone: %d", err, tile.Path, easting, northing, zone)
				return elevation, tile, err
			}
		}
	}

	// success
	return elevation, tile, nil
}
