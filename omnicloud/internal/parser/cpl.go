package parser

import (
	"encoding/xml"
	"fmt"
	"io/ioutil"
)

// ParseCPL reads and parses a CPL (Composition Playlist) XML file
func ParseCPL(path string) (*CompositionPlaylist, error) {
	data, err := ioutil.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read CPL file: %w", err)
	}

	var cpl CompositionPlaylist
	if err := xml.Unmarshal(data, &cpl); err != nil {
		return nil, fmt.Errorf("failed to parse CPL XML: %w", err)
	}

	return &cpl, nil
}

// GetReelCount returns the number of reels in the composition
func (cpl *CompositionPlaylist) GetReelCount() int {
	return len(cpl.ReelList.Reels)
}

// GetTotalDuration calculates the total duration across all reels
func (cpl *CompositionPlaylist) GetTotalDuration() int {
	total := 0
	for _, reel := range cpl.ReelList.Reels {
		if reel.AssetList.MainPicture != nil {
			total += int(reel.AssetList.MainPicture.Duration)
		}
	}
	return total
}

// GetResolution returns the width and height from the first reel's metadata
func (cpl *CompositionPlaylist) GetResolution() (width, height int) {
	for _, reel := range cpl.ReelList.Reels {
		if reel.AssetList.Metadata != nil {
			return reel.AssetList.Metadata.MainPictureStoredArea.Width,
				   reel.AssetList.Metadata.MainPictureStoredArea.Height
		}
	}
	return 0, 0
}

// GetMetadata returns the first available metadata from reels
func (cpl *CompositionPlaylist) GetMetadata() *Metadata {
	for _, reel := range cpl.ReelList.Reels {
		if reel.AssetList.Metadata != nil {
			return reel.AssetList.Metadata
		}
	}
	return nil
}
