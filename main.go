/*
Purpose:
- dtm elevation service

Description:
- Service for determining elevation information based on accurate DTM (Digital Terrain Model) data.

Releases:
- v1.0.0 - 2025-05-23: initial release
- v1.1.0 - 2025-06-10: contours added, hillshading added, origin added to point
- v1.1.1 - 2025-06-11: valid equidistance 0.2-25.0 m
- v1.2.0 - 2025-07-14: added: aspect, slope, gpx analyze
- v1.2.1 - 2025-07-23: aspect: gdaldem option -zero_for_flat removed

Author:
- Klaus Tockloth

Copyright:
- © 2025 | Klaus Tockloth

Contact:
- klaus.tockloth@googlemail.com

Remarks:
- Usage 'point' API : see script 'query-elevation-point.sh'
- Usage 'gpx' API : see script 'query-elevation-gpx.sh'
- Single Tile Caching adds complexity but can improve the processing of large GPX files.

TODOs:
- Validieren: Datenbezogene Fehler nur im Debug-Modus loggen.
- Beim Aufbau des globalen Repositories, neuere Tile bevorzugen (betrifft nur mehrfache Tiles an Ländergrenzen).

Links:
- https://pkg.go.dev/github.com/airbusgeo/godal
- https://pkg.go.dev/github.com/tkrajina/gpxgo/gpx
- https://pkg.go.dev/gopkg.in/yaml.v3
- https://pkg.go.dev/gopkg.in/natefinch/lumberjack.v2
*/

// main package
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/airbusgeo/godal"
	"gopkg.in/natefinch/lumberjack.v2"
	"gopkg.in/yaml.v3"
)

// general program info
var (
	progName      = strings.TrimSuffix(filepath.Base(os.Args[0]), filepath.Ext(filepath.Base(os.Args[0])))
	progVersion   = "v1.2.1"
	progDate      = "2025-07-23"
	progPurpose   = "dtm elevation service"
	progInfo      = "Service for determining elevation information based on accurate DTM (Digital Terrain Model) data."
	progCopyright = "© 2025 | Klaus Tockloth"
)

// ProgConfig defines program configuration
type ProgConfig struct {
	ListenAddress       string   `yaml:"ListenAddress"`
	ServerCertificate   string   `yaml:"ServerCertificate"`
	ServerKey           string   `yaml:"ServerKey"`
	TrustedIssuers      []string `yaml:"TrustedIssuers"`
	ShutdownGracePeriod int      `yaml:"ShutdownGracePeriod"`
	LogDirectory        string   `yaml:"LogDirectory"`
	LogLevel            string   `yaml:"LogLevel"`
	TileRepositories    []string `yaml:"TileRepositories"`
}

// progConfig represents program configuration
var progConfig ProgConfig

// statistics
var (
	PointRequests      uint64
	UTMPointRequests   uint64
	GPXRequests        uint64
	GPXAnalyzeRequests uint64
	GPXPoints          uint64
	DGMPoints          uint64
	ContoursRequests   uint64
	HillshadeRequests  uint64
	SlopeRequests      uint64
	AspectRequests     uint64
)

/*
main starts this program.
*/
func main() {
	// load program configuration
	progConfigFile := progName + ".yaml"
	source, err := os.ReadFile(progConfigFile)
	if err != nil {
		fmt.Fprintf(os.Stderr, "configuration file not found, file = [%s]\n", progConfigFile)
		fmt.Fprintf(os.Stderr, "error [%v] at os.ReadFile()\n", err)
		os.Exit(1)
	}
	err = yaml.Unmarshal(source, &progConfig)
	if err != nil {
		fmt.Fprintf(os.Stderr, "configuration file invalid, file = [%s]\n", progConfigFile)
		fmt.Fprintf(os.Stderr, "error [%v] at yaml.Unmarshal()\n", err)
		os.Exit(1)
	}

	// logging: replacer for logging objects
	replacer := func(_ []string, a slog.Attr) slog.Attr {
		if a.Key == slog.SourceKey {
			source := a.Value.Any().(*slog.Source)   // get source object
			source.File = filepath.Base(source.File) // basepath only
		}
		if a.Key == slog.TimeKey {
			return slog.String("time", a.Value.Time().Format(time.RFC3339Nano)) // local time -> RFC3339Nano
		}
		return a
	}

	// logging: log file output and rotate (with lumberjack package)
	logrotateStartYearDay := time.Now().UTC().YearDay()
	logfile := filepath.Join(progConfig.LogDirectory, progName+".log")
	lumberjackLogger := &lumberjack.Logger{
		Filename: logfile,
		MaxSize:  128,  // megabytes
		MaxAge:   28,   // days
		Compress: true, // gzip rotated log
	}

	// log level
	logLevel := new(slog.LevelVar)
	logLevel.Set(parseLogLevel(progConfig.LogLevel))

	// define logger
	logger := slog.New(slog.NewJSONHandler(lumberjackLogger, &slog.HandlerOptions{
		Level:     logLevel,
		AddSource: true, ReplaceAttr: replacer}).WithAttrs([]slog.Attr{slog.String("prog", progName)}))
	slog.SetDefault(logger)

	// log program start
	slog.Info(progPurpose+" startet", "name", progName, "version", progVersion, "date", progDate, "info", progInfo, "copyright", progCopyright, "command line", os.Args)
	jsonData, _ := json.MarshalIndent(progConfig, "", "  ") // encode to JSON for readability
	slog.Info("content of configuration file", "configuration file", progConfigFile, "content", string(jsonData))

	// build global tile repository
	err = buildRepository()
	if err != nil {
		slog.Error("error building global tile repository", "error", err)
		os.Exit(1)
	}

	// save global tile repository
	err = saveRepository()
	if err != nil {
		slog.Error("error saving global tile repository", "error", err)
		os.Exit(1)
	}

	// initialize GDAL, register all known GDAL drivers
	godal.RegisterAll()

	// define routes
	http.HandleFunc("POST /v1/point", pointRequest)
	http.HandleFunc("OPTIONS /v1/point", corsOptionsHandler)

	http.HandleFunc("POST /v1/utmpoint", utmPointRequest)
	http.HandleFunc("OPTIONS /v1/utmpoint", corsOptionsHandler)

	http.HandleFunc("POST /v1/gpx", gpxRequest)
	http.HandleFunc("OPTIONS /v1/gpx", corsOptionsHandler)

	http.HandleFunc("POST /v1/gpxanalyze", gpxAnalyzeRequest)
	http.HandleFunc("OPTIONS /v1/gpxanalyze", corsOptionsHandler)

	http.HandleFunc("POST /v1/contours", contoursRequest)
	http.HandleFunc("OPTIONS /v1/contours", corsOptionsHandler)

	http.HandleFunc("POST /v1/hillshade", hillshadeRequest)
	http.HandleFunc("OPTIONS /v1/hillshade", corsOptionsHandler)

	http.HandleFunc("POST /v1/slope", slopeRequest)
	http.HandleFunc("OPTIONS /v1/slope", corsOptionsHandler)

	http.HandleFunc("POST /v1/aspect", aspectRequest)
	http.HandleFunc("OPTIONS /v1/aspect", corsOptionsHandler)

	// handle unsupported routes or methods
	http.HandleFunc("/", unsupportedRequest)

	// define service
	DtmElevationService := &http.Server{
		Addr:              progConfig.ListenAddress,
		Handler:           nil,
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       120 * time.Second,
		WriteTimeout:      180 * time.Second,
		IdleTimeout:       30 * time.Second,
	}

	// get hostname
	hostname, err := os.Hostname()
	if err != nil {
		hostname = "unknown"
	}

	// create service
	go func() {
		slog.Info("dtm elevation service listening for requests", "ListenAddress", progConfig.ListenAddress, "hostname", hostname)
		err := DtmElevationService.ListenAndServeTLS(progConfig.ServerCertificate, progConfig.ServerKey)
		if err != nil {
			if err != http.ErrServerClosed {
				slog.Error("error at DtmElevationService.ListenAndServe()", "error", err)
				os.Exit(1)
			}
		}
	}()

	// start rotate trigger (checks, if log rotate is required)
	rotateTrigger := time.Tick(time.Second * 60)

	// start shutdown trigger and subscribe to shutdown signals
	shutdownTrigger := make(chan os.Signal, 1)
	signal.Notify(shutdownTrigger, syscall.SIGINT)  // kill -SIGINT pid -> interrupt
	signal.Notify(shutdownTrigger, syscall.SIGTERM) // kill -SIGTERM pid -> terminated

ForeverLoop:
	for {
		// wait for log rotate or shutdown trigger
		select {
		case <-rotateTrigger:
			logrotateCurrentYearDay := time.Now().UTC().YearDay()
			if logrotateCurrentYearDay != logrotateStartYearDay {
				slog.Info("new day detected, log rotate triggered")
				err := lumberjackLogger.Rotate()
				if err != nil {
					slog.Error("error at lumberjackLogger.Rotate()", "error", err)
				}
				logrotateStartYearDay = logrotateCurrentYearDay
				logStatistics()
			}
		case sig := <-shutdownTrigger:
			// initiate shutdown
			slog.Info("signal received, shutting down elevation service", "signal", sig)
			break ForeverLoop
		}
	}

	// shutdown grace period (wait max n seconds before halting)
	gracePeriod := time.Duration(progConfig.ShutdownGracePeriod) * time.Second

	// shutdown service
	ctx, cancel := context.WithTimeout(context.Background(), gracePeriod)
	defer cancel()
	err = DtmElevationService.Shutdown(ctx)
	if err != nil {
		slog.Error("fatal error at DtmElevationService.Shutdown()", "error", err)
	}

	// log program end
	logStatistics()
	slog.Info("service gracefully shut down")
}

/*
logStatistics logs statistics.
*/
func logStatistics() {
	// read statistics
	currentPointRequests := atomic.LoadUint64(&PointRequests)
	currentUTMPointRequests := atomic.LoadUint64(&UTMPointRequests)
	currentGPXRequests := atomic.LoadUint64(&GPXRequests)
	currentGPXAnalyzeRequests := atomic.LoadUint64(&GPXAnalyzeRequests)
	currentGPXPoints := atomic.LoadUint64(&GPXPoints)
	currentDGMPoints := atomic.LoadUint64(&DGMPoints)
	currentContoursRequests := atomic.LoadUint64(&ContoursRequests)
	currentHillshadeRequests := atomic.LoadUint64(&HillshadeRequests)
	currentSlopeRequests := atomic.LoadUint64(&SlopeRequests)
	currentAspectRequests := atomic.LoadUint64(&AspectRequests)

	// reset statistics
	atomic.StoreUint64(&PointRequests, 0)
	atomic.StoreUint64(&UTMPointRequests, 0)
	atomic.StoreUint64(&GPXRequests, 0)
	atomic.StoreUint64(&GPXAnalyzeRequests, 0)
	atomic.StoreUint64(&GPXPoints, 0)
	atomic.StoreUint64(&DGMPoints, 0)
	atomic.StoreUint64(&ContoursRequests, 0)
	atomic.StoreUint64(&HillshadeRequests, 0)
	atomic.StoreUint64(&SlopeRequests, 0)
	atomic.StoreUint64(&AspectRequests, 0)

	// log statistics
	slog.Info("load statistics",
		"PointRequests", currentPointRequests,
		"UTMPointRequests", currentUTMPointRequests,
		"GPXRequests", currentGPXRequests,
		"GPXAnalyzeRequests", currentGPXAnalyzeRequests,
		"GPXPoints", currentGPXPoints,
		"DGMPoints", currentDGMPoints,
		"ContoursRequests", currentContoursRequests,
		"HillshadeRequests", currentHillshadeRequests,
		"SlopeRequests", currentSlopeRequests,
		"AspectRequests", currentAspectRequests,
	)
}

/*
parseLogLevel parses log level setting from configuration.
*/
func parseLogLevel(logLevel string) slog.Level {
	switch strings.ToLower(logLevel) {
	case "debug":
		return slog.LevelDebug
	case "info":
		return slog.LevelInfo
	case "warn":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}
