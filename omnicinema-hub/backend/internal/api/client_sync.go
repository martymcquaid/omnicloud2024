package api

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"time"

	"github.com/google/uuid"
	"github.com/omnicloud/omnicloud/internal/db"
	"github.com/omnicloud/omnicloud/pkg/dcp"
)

// ClientSync handles synchronization from client to main server
type ClientSync struct {
	database        *db.DB
	serverID        uuid.UUID
	mainServerURL   string
	macAddress      string
	registrationKey string
	serverName      string
	serverLocation  string
	stopChan        chan struct{}
}

// NewClientSync creates a new client sync service
func NewClientSync(database *db.DB, serverID uuid.UUID, mainServerURL, serverName, serverLocation, registrationKey string) (*ClientSync, error) {
	macAddress, err := dcp.GetMACAddress()
	if err != nil {
		return nil, fmt.Errorf("failed to get MAC address: %w", err)
	}

	return &ClientSync{
		database:        database,
		serverID:        serverID,
		mainServerURL:   mainServerURL,
		macAddress:      macAddress,
		registrationKey: registrationKey,
		serverName:      serverName,
		serverLocation:  serverLocation,
		stopChan:        make(chan struct{}),
	}, nil
}

// Start begins periodic synchronization with main server
func (cs *ClientSync) Start() {
	log.Printf("Client sync service started (Main Server: %s)", cs.mainServerURL)

	// Register with main server immediately
	go cs.registerWithMainServer()

	// Start periodic sync (every 5 minutes)
	go cs.periodicSync()
}

// Stop stops the sync service
func (cs *ClientSync) Stop() {
	close(cs.stopChan)
	log.Println("Client sync service stopped")
}

// registerWithMainServer registers this client with the main server
func (cs *ClientSync) registerWithMainServer() {
	// Get current storage capacity
	packageCount, _ := cs.database.CountDCPPackages()

	registration := ServerRegistration{
		Name:              cs.serverName,
		Location:          cs.serverLocation,
		APIURL:            "",
		MACAddress:        cs.macAddress,
		RegistrationKey:   cs.registrationKey,
		StorageCapacityTB: 0, // Could calculate from DB
	}

	data, err := json.Marshal(registration)
	if err != nil {
		log.Printf("Error marshaling registration: %v", err)
		return
	}

	url := fmt.Sprintf("%s/api/v1/servers/register", cs.mainServerURL)
	resp, err := http.Post(url, "application/json", bytes.NewBuffer(data))
	if err != nil {
		log.Printf("Error registering with main server: %v", err)
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		log.Printf("Registration failed with status: %d", resp.StatusCode)
		return
	}

	log.Printf("Successfully registered with main server (Packages: %d, MAC: %s)", packageCount, cs.macAddress)
}

// periodicSync syncs inventory with main server periodically
func (cs *ClientSync) periodicSync() {
	ticker := time.NewTicker(5 * time.Minute)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			cs.syncInventory()

		case <-cs.stopChan:
			return
		}
	}
}

// syncInventory sends current inventory to main server
func (cs *ClientSync) syncInventory() {
	log.Println("Syncing inventory with main server...")

	// Get all packages from local database
	query := `
		SELECT p.assetmap_uuid, i.local_path, i.status
		FROM dcp_packages p
		JOIN server_dcp_inventory i ON p.id = i.package_id
		WHERE i.server_id = $1
		ORDER BY p.package_name`

	rows, err := cs.database.Query(query, cs.serverID)
	if err != nil {
		log.Printf("Error querying inventory: %v", err)
		return
	}
	defer rows.Close()

	var packages []InventoryPackage
	for rows.Next() {
		var assetMapUUID uuid.UUID
		var localPath, status string

		if err := rows.Scan(&assetMapUUID, &localPath, &status); err != nil {
			log.Printf("Error scanning row: %v", err)
			continue
		}

		packages = append(packages, InventoryPackage{
			AssetMapUUID: assetMapUUID.String(),
			LocalPath:    localPath,
			Status:       status,
		})
	}

	if len(packages) == 0 {
		log.Println("No packages to sync")
		return
	}

	// Send to main server
	update := InventoryUpdate{
		Packages: packages,
	}

	data, err := json.Marshal(update)
	if err != nil {
		log.Printf("Error marshaling inventory: %v", err)
		return
	}

	url := fmt.Sprintf("%s/api/v1/servers/%s/inventory", cs.mainServerURL, cs.serverID)
	resp, err := http.Post(url, "application/json", bytes.NewBuffer(data))
	if err != nil {
		log.Printf("Error syncing inventory: %v", err)
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		log.Printf("Inventory sync failed with status: %d", resp.StatusCode)
		return
	}

	log.Printf("Successfully synced %d packages with main server", len(packages))
}
