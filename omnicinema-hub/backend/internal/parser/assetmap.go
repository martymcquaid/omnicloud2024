package parser

import (
	"encoding/xml"
	"fmt"
	"io/ioutil"
)

// ParseAssetMap reads and parses an ASSETMAP.xml file
func ParseAssetMap(path string) (*AssetMap, error) {
	data, err := ioutil.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read ASSETMAP file: %w", err)
	}

	var assetMap AssetMap
	if err := xml.Unmarshal(data, &assetMap); err != nil {
		return nil, fmt.Errorf("failed to parse ASSETMAP XML: %w", err)
	}

	return &assetMap, nil
}

// ExtractUUID extracts the UUID from a URN string (e.g., "urn:uuid:xxx" -> "xxx")
func ExtractUUID(urn string) string {
	// URNs are typically in format "urn:uuid:xxxxxxxx-xxxx-xxxx-xxxx-xxxxxxxxxxxx"
	if len(urn) > 9 && urn[:9] == "urn:uuid:" {
		return urn[9:]
	}
	return urn
}
