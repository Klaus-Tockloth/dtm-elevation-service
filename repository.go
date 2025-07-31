package main

import (
	"encoding/csv"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"sort"
)

// TileMetadata represents meta data about a tile.
type TileMetadata struct {
	Index     string // (hash) index of tile (e.g. 32_383_5802)
	Path      string // path and file name (e.g. /Downloads/dgm1_32_383_5802_1_ni_2017.tif)
	Source    string // source of tile (e.g. DE-NI)
	Actuality string // actuality of Airborne Laser Scanning (ALS) (e.g. 2017-04-19)
}

// Repository represents repository for all tiles (readonly after initialization).
var Repository map[string]TileMetadata

/*
buildRepository builds global repository with all tile meta data.
Each federal state provides a complete set of tiles for its territory.
At the border between two federal states, the tiles exist in duplicate.
Example: "32_410_5812"
Tile for NW: dgm1_32_410_5812_1_nw_2024.tif -> index '32_410_5812'
Tile for NI: dgm1_32_410_5812_1_ni_2017.tif -> index '32_410_5812_2'
We need both tiles, measurements beyond the boundary can be designated as -9999 (no data).
Also possible for a tile: state, neighbor 1, neighbor 2
*/
func buildRepository() error {
	// initialize global tile repository map (Germany has estimated 360.000 entries)
	Repository = make(map[string]TileMetadata, 256*1024)

	stateRepositories := progConfig.TileRepositories

	// iterate over state repositories
	numberOfPrimaryTiles := 0
	numberOfSecondaryTiles := 0
	numberOfTertiaryTiles := 0
	for _, stateRepository := range stateRepositories {
		// read state repository
		stateTileMetadata := []TileMetadata{}
		data, err := os.ReadFile(stateRepository)
		if err != nil {
			return fmt.Errorf("building global tile repository: error [%w] at os.ReadFile()", err)
		}

		err = json.Unmarshal(data, &stateTileMetadata)
		if err != nil {
			return fmt.Errorf("building global tile repository: error [%w] at json.Unmarshal()", err)
		}

		slog.Info("processing state repository tile meta data", "repository", stateRepository, "entries", len(stateTileMetadata))

		// build global repository map
		for _, entry := range stateTileMetadata {
			// check if primary entry already exists
			_, primaryExists := Repository[entry.Index]
			if !primaryExists {
				Repository[entry.Index] = entry
				numberOfPrimaryTiles++
				continue
			}
			// check if secondary entry already exists
			index := entry.Index + "_2"
			_, secondaryExists := Repository[index]
			if !secondaryExists {
				Repository[index] = entry
				numberOfSecondaryTiles++
				continue
			}
			// add entry as tertiary entry
			index = entry.Index + "_3"
			Repository[index] = entry
			numberOfTertiaryTiles++
		}
	}

	slog.Info("global tile repository successfully build", "entries", len(Repository), "primary tiles", numberOfPrimaryTiles,
		"secondary tiles", numberOfSecondaryTiles, "tertiary tiles", numberOfTertiaryTiles)

	return nil
}

/*
saveRepository saves repository as sorted csv file.
*/
func saveRepository() error {
	// extract keys (Index) from map
	keys := make([]string, 0, len(Repository))
	for k := range Repository {
		keys = append(keys, k)
	}

	// sort keys
	sort.Strings(keys)

	// open csv file
	filename := "repository.csv"
	file, err := os.Create(filename)
	if err != nil {
		return fmt.Errorf("error [%v] at os.Create()", err)
	}
	defer file.Close()

	// create csv writer
	writer := csv.NewWriter(file)
	defer writer.Flush()

	// write header
	header := []string{"Index", "Path", "Source", "Actuality"}
	err = writer.Write(header)
	if err != nil {
		return fmt.Errorf("error [%v] at writer.Write()", err)
	}

	// iterate over sorted keys
	for _, key := range keys {
		metadata, ok := Repository[key]
		if !ok {
			return fmt.Errorf("warning: key [%s] not found during writing", key)
		}

		// create and write csv line
		row := []string{key, metadata.Path, metadata.Source, metadata.Actuality}
		err = writer.Write(row)
		if err != nil {
			return fmt.Errorf("error [%v] at writer.Write()", err)
		}
	}

	err = writer.Error()
	if err != nil {
		return fmt.Errorf("error [%v] at writer.Error()", err)
	}

	return nil
}
