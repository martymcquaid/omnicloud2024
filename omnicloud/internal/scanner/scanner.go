package scanner

import (
	"fmt"
	"log"
	"path/filepath"
	"time"

	"github.com/omnicloud/omnicloud/internal/parser"
)

// DCPPackageInfo holds complete metadata about a DCP package
type DCPPackageInfo struct {
	PackagePath    string
	PackageName    string
	AssetMap       *parser.AssetMap
	CPLs           []*parser.CompositionPlaylist
	PKLs           []*parser.PackingList
	TotalSize      int64
	FileCount      int
	DiscoveredAt   time.Time
}

// ScanPackage scans a single DCP package and extracts all metadata
func ScanPackage(packagePath string) (*DCPPackageInfo, error) {
	log.Printf("Scanning package: %s", packagePath)
	
	info := &DCPPackageInfo{
		PackagePath:  packagePath,
		PackageName:  filepath.Base(packagePath),
		DiscoveredAt: time.Now(),
	}
	
	// Find and parse ASSETMAP
	assetMapPath, err := FindAssetMapFile(packagePath)
	if err != nil {
		return nil, fmt.Errorf("failed to find ASSETMAP: %w", err)
	}
	
	info.AssetMap, err = parser.ParseAssetMap(assetMapPath)
	if err != nil {
		return nil, fmt.Errorf("failed to parse ASSETMAP: %w", err)
	}
	
	// Find and parse all CPL files
	cplFiles, err := FindCPLFiles(packagePath)
	if err != nil {
		log.Printf("Warning: failed to find CPL files: %v", err)
	}
	
	for _, cplPath := range cplFiles {
		cpl, err := parser.ParseCPL(cplPath)
		if err != nil {
			log.Printf("Warning: failed to parse CPL %s: %v", cplPath, err)
			continue
		}
		info.CPLs = append(info.CPLs, cpl)
	}
	
	// Find and parse all PKL files
	pklFiles, err := FindPKLFiles(packagePath)
	if err != nil {
		log.Printf("Warning: failed to find PKL files: %v", err)
	}
	
	for _, pklPath := range pklFiles {
		pkl, err := parser.ParsePKL(pklPath)
		if err != nil {
			log.Printf("Warning: failed to parse PKL %s: %v", pklPath, err)
			continue
		}
		info.PKLs = append(info.PKLs, pkl)
	}
	
	// Calculate total size and file count
	info.TotalSize, info.FileCount, err = CalculateDirectorySize(packagePath)
	if err != nil {
		log.Printf("Warning: failed to calculate directory size: %v", err)
	}
	
	log.Printf("Package scan complete: %s (Size: %d bytes, Files: %d, CPLs: %d, PKLs: %d)",
		info.PackageName, info.TotalSize, info.FileCount, len(info.CPLs), len(info.PKLs))
	
	return info, nil
}

// ScanMultiplePackages scans multiple packages concurrently
func ScanMultiplePackages(packagePaths []string, workers int) ([]*DCPPackageInfo, []error) {
	if workers <= 0 {
		workers = 4 // Default to 4 workers
	}
	
	jobs := make(chan string, len(packagePaths))
	results := make(chan *DCPPackageInfo, len(packagePaths))
	errors := make(chan error, len(packagePaths))
	
	// Start worker goroutines
	for w := 0; w < workers; w++ {
		go func() {
			for packagePath := range jobs {
				info, err := ScanPackage(packagePath)
				if err != nil {
					errors <- fmt.Errorf("error scanning %s: %w", packagePath, err)
					continue
				}
				results <- info
			}
		}()
	}
	
	// Send jobs to workers
	for _, path := range packagePaths {
		jobs <- path
	}
	close(jobs)
	
	// Collect results
	var packageInfos []*DCPPackageInfo
	var scanErrors []error
	
	for i := 0; i < len(packagePaths); i++ {
		select {
		case info := <-results:
			packageInfos = append(packageInfos, info)
		case err := <-errors:
			scanErrors = append(scanErrors, err)
		}
	}
	
	return packageInfos, scanErrors
}
