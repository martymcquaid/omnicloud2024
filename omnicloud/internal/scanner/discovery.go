package scanner

import (
	"fmt"
	"io/ioutil"
	"log"
	"os"
	"path/filepath"
	"strings"

	"github.com/omnicloud/omnicloud/internal/parser"
)

// DiscoverDCPPackages walks the archive directory and finds all DCP packages
// A DCP package is identified by the presence of an ASSETMAP or ASSETMAP.xml file
func DiscoverDCPPackages(rootPath string) ([]string, error) {
	var packages []string
	seen := make(map[string]bool)

	log.Printf("Discovering DCP packages in: %s", rootPath)

	// Check if root path exists
	if _, err := os.Stat(rootPath); os.IsNotExist(err) {
		return nil, fmt.Errorf("scan path does not exist: %s", rootPath)
	}

	// Walk the directory tree
	err := filepath.Walk(rootPath, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			log.Printf("Warning: error accessing path %s: %v", path, err)
			return nil // Continue walking despite errors
		}

		// Skip if not a file
		if info.IsDir() {
			return nil
		}

		// Check if this is an ASSETMAP file (a directory may contain both
		// ASSETMAP and ASSETMAP.XML; only count it once)
		fileName := strings.ToUpper(info.Name())
		if fileName == "ASSETMAP" || fileName == "ASSETMAP.XML" {
			packagePath := filepath.Dir(path)
			if !seen[packagePath] {
				seen[packagePath] = true
				packages = append(packages, packagePath)
				log.Printf("Found DCP package: %s", packagePath)
			}
		}

		return nil
	})
	
	if err != nil {
		return nil, fmt.Errorf("error walking directory tree: %w", err)
	}
	
	log.Printf("Discovery complete: found %d DCP packages", len(packages))
	return packages, nil
}

// FindAssetMapFile finds the ASSETMAP file in a package directory
func FindAssetMapFile(packagePath string) (string, error) {
	// Try both ASSETMAP.xml and ASSETMAP (without extension)
	candidates := []string{
		filepath.Join(packagePath, "ASSETMAP.xml"),
		filepath.Join(packagePath, "ASSETMAP"),
		filepath.Join(packagePath, "assetmap.xml"),
		filepath.Join(packagePath, "assetmap"),
	}
	
	for _, candidate := range candidates {
		if _, err := os.Stat(candidate); err == nil {
			return candidate, nil
		}
	}
	
	return "", fmt.Errorf("ASSETMAP file not found in %s", packagePath)
}

// FindCPLFiles finds all CPL XML files in a package directory
func FindCPLFiles(packagePath string) ([]string, error) {
	var cplFiles []string
	
	entries, err := ioutil.ReadDir(packagePath)
	if err != nil {
		return nil, fmt.Errorf("failed to read package directory: %w", err)
	}
	
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		
		name := entry.Name()
		upperName := strings.ToUpper(name)
		
		// Look for CPL_*.xml files
		if strings.HasPrefix(upperName, "CPL_") && strings.HasSuffix(upperName, ".XML") {
			cplFiles = append(cplFiles, filepath.Join(packagePath, name))
		}
		// Also check for .cpl extension
		if strings.HasSuffix(strings.ToLower(name), ".cpl") {
			cplFiles = append(cplFiles, filepath.Join(packagePath, name))
		}
		// RosettaBridge renames CPL files to just "CPL.xml"
		if upperName == "CPL.XML" {
			cplFiles = append(cplFiles, filepath.Join(packagePath, name))
		}
	}
	
	return cplFiles, nil
}

// FindPKLFiles finds all PKL XML files in a package directory
func FindPKLFiles(packagePath string) ([]string, error) {
	var pklFiles []string
	
	entries, err := ioutil.ReadDir(packagePath)
	if err != nil {
		return nil, fmt.Errorf("failed to read package directory: %w", err)
	}
	
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		
		name := entry.Name()
		upperName := strings.ToUpper(name)
		
		// Look for PKL_*.xml files
		if strings.HasPrefix(upperName, "PKL_") && strings.HasSuffix(upperName, ".XML") {
			pklFiles = append(pklFiles, filepath.Join(packagePath, name))
		}
		// Also check for .pkl extension
		if strings.HasSuffix(strings.ToLower(name), ".pkl") {
			pklFiles = append(pklFiles, filepath.Join(packagePath, name))
		}
		// RosettaBridge renames PKL files to just "PKL.xml"
		if upperName == "PKL.XML" {
			pklFiles = append(pklFiles, filepath.Join(packagePath, name))
		}
	}
	
	return pklFiles, nil
}

// FindPKLFilesFromAssetMap finds PKL files using ASSETMAP asset references.
// Use as fallback when filename-based discovery (FindPKLFiles) finds nothing.
func FindPKLFilesFromAssetMap(packagePath string, assetMap *parser.AssetMap) []string {
	var pklFiles []string
	for _, asset := range assetMap.AssetList.Assets {
		if asset.PackingList && len(asset.ChunkList.Chunks) > 0 {
			pklPath := filepath.Join(packagePath, asset.ChunkList.Chunks[0].Path)
			if _, err := os.Stat(pklPath); err == nil {
				pklFiles = append(pklFiles, pklPath)
			}
		}
	}
	return pklFiles
}

// FindCPLFilesFromAssetMap finds potential CPL files using ASSETMAP asset references.
// Looks for non-PKL, non-ASSETMAP XML files referenced in the ASSETMAP.
func FindCPLFilesFromAssetMap(packagePath string, assetMap *parser.AssetMap) []string {
	var cplFiles []string
	for _, asset := range assetMap.AssetList.Assets {
		if asset.PackingList {
			continue // Skip PKL files
		}
		if len(asset.ChunkList.Chunks) == 0 {
			continue
		}
		assetPath := asset.ChunkList.Chunks[0].Path
		upperPath := strings.ToUpper(assetPath)
		// Skip ASSETMAP references and non-XML files
		if strings.Contains(upperPath, "ASSETMAP") {
			continue
		}
		if !strings.HasSuffix(upperPath, ".XML") {
			continue
		}
		// Skip MXF-related XML (e.g., subtitle XMLs typically aren't CPLs)
		// CPLs are top-level XML files, not subdirectory files
		if strings.Contains(assetPath, "/") {
			continue
		}
		fullPath := filepath.Join(packagePath, assetPath)
		if _, err := os.Stat(fullPath); err == nil {
			cplFiles = append(cplFiles, fullPath)
		}
	}
	return cplFiles
}

// CalculateDirectorySize calculates the total size of all files in a directory
func CalculateDirectorySize(path string) (int64, int, error) {
	var totalSize int64
	var fileCount int
	
	err := filepath.Walk(path, func(filePath string, info os.FileInfo, err error) error {
		if err != nil {
			return nil // Skip files we can't access
		}
		if !info.IsDir() {
			totalSize += info.Size()
			fileCount++
		}
		return nil
	})
	
	return totalSize, fileCount, err
}
