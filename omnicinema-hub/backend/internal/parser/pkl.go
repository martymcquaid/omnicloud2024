package parser

import (
	"encoding/xml"
	"fmt"
	"io/ioutil"
)

// ParsePKL reads and parses a PKL (Packing List) XML file
func ParsePKL(path string) (*PackingList, error) {
	data, err := ioutil.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read PKL file: %w", err)
	}

	var pkl PackingList
	if err := xml.Unmarshal(data, &pkl); err != nil {
		return nil, fmt.Errorf("failed to parse PKL XML: %w", err)
	}

	return &pkl, nil
}

// GetAssetCount returns the number of assets in the packing list
func (pkl *PackingList) GetAssetCount() int {
	return len(pkl.AssetList.Assets)
}

// FindAssetByUUID finds an asset by its UUID
func (pkl *PackingList) FindAssetByUUID(uuid string) *PKLAsset {
	for _, asset := range pkl.AssetList.Assets {
		if ExtractUUID(asset.ID) == uuid {
			return &asset
		}
	}
	return nil
}
