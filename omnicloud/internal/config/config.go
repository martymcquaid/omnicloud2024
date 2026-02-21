package config

import (
	"bufio"
	"fmt"
	"os"
	"runtime"
	"strconv"
	"strings"
)

// Config holds all application configuration
type Config struct {
	// Database configuration
	DBHost     string
	DBPort     int
	DBName     string
	DBUser     string
	DBPassword string

	// Application configuration
	ScanPath       string
	APIPort        int
	ScanInterval   int // hours
	ServerName     string
	ServerLocation string
	
	// Server mode configuration
	ServerMode      string // "main" or "client"
	RegistrationKey string // Authentication key for site registration
	MainServerURL   string // URL of main server (for clients)
	
	// Torrent configuration
	TrackerPort                 int // Port for BitTorrent tracker (main server only)
	TorrentDataPort             int // Port for BitTorrent data (seeding/downloading); 0 = auto-pick
	TorrentDataDir              string
	MaxUploadRate               int // bytes/sec, 0 = unlimited
	MaxDownloadRate             int // bytes/sec, 0 = unlimited
	MaxConcurrentSeeds          int
	MaxConcurrentDownloads      int
	PieceHashWorkers            int // Parallel workers for hashing (per torrent)
	MaxTorrentGenerationWorkers int // Concurrent DCP torrent generations; 0 = use CPU count

	// Relay configuration (NAT traversal)
	RelayEnabled     bool   // Enable relay server (main) / relay client (client)
	RelayPort        int    // Relay server listening port (main server only); default 10866
	RelayMaxSessions int    // Max concurrent relay sessions; default 100
}

// Load reads configuration from auth.config file and environment variables
// Environment variables take precedence over file values
func Load(configPath string) (*Config, error) {
	cfg := &Config{
		// Defaults
		DBHost:         "localhost",
		DBPort:         5432,
		DBName:         "OmniCloud",
		ScanPath:       "/library/omnicloud/testlibrary",
		APIPort:        10858,
		ScanInterval:   12,
		ServerName:     getHostname(),
		ServerLocation: "Local",
		ServerMode:     "main", // Default to main server
		RegistrationKey: generateDefaultKey(),
		MainServerURL:  "",
		
		// Torrent defaults
		TrackerPort:            10859,
		TorrentDataPort:        0, // 0 = auto-pick free port
		TorrentDataDir:         "/opt/OmniCloud/omnicloud2024/omnicloud/data/torrents",
		MaxUploadRate:          0, // unlimited
		MaxDownloadRate:        0, // unlimited
		MaxConcurrentSeeds:          50,
		MaxConcurrentDownloads:      5,
		PieceHashWorkers:            0, // 0 = auto (CPU count)
		MaxTorrentGenerationWorkers: 0, // 0 = auto (CPU count)

		// Relay defaults
		RelayEnabled:     true,  // Relay enabled by default
		RelayPort:        10866, // Default relay port
		RelayMaxSessions: 100,
	}

	// Try to load from auth.config if it exists
	if configPath != "" {
		if err := cfg.loadFromFile(configPath); err != nil {
			// If file doesn't exist, that's okay, we'll use defaults
			if !os.IsNotExist(err) {
				return nil, fmt.Errorf("error reading config file: %w", err)
			}
		}
	}

	// Override with environment variables
	cfg.loadFromEnv()

	// Auto-set torrent resource defaults when 0
	numCPU := runtime.NumCPU()
	if numCPU < 1 {
		numCPU = 1
	}
	if cfg.PieceHashWorkers <= 0 {
		cfg.PieceHashWorkers = numCPU
	}
	// Cap piece-hash workers to avoid excessive memory usage during torrent generation.
	// Each worker holds one full piece in memory (e.g. 16-32MB), so 16 workers = 256-512MB max.
	const maxPieceHashWorkers = 16
	if cfg.PieceHashWorkers > maxPieceHashWorkers {
		cfg.PieceHashWorkers = maxPieceHashWorkers
	}
	// Default max concurrent torrent generations to 2 (I/O heavy; override in config for more)
	if cfg.MaxTorrentGenerationWorkers <= 0 {
		cfg.MaxTorrentGenerationWorkers = 2
	}
	// Cap concurrent generations to avoid overload (e.g. 32)
	const maxGenWorkers = 32
	if cfg.MaxTorrentGenerationWorkers > maxGenWorkers {
		cfg.MaxTorrentGenerationWorkers = maxGenWorkers
	}

	// Validate required fields
	if cfg.DBUser == "" {
		return nil, fmt.Errorf("DB_USER must be set (in config file or environment)")
	}
	if cfg.DBPassword == "" {
		return nil, fmt.Errorf("DB_PASSWORD must be set (in config file or environment)")
	}

	return cfg, nil
}

// loadFromFile reads key=value pairs from auth.config
func (cfg *Config) loadFromFile(path string) error {
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		
		// Skip empty lines and comments
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}

		// Parse key=value
		parts := strings.SplitN(line, "=", 2)
		if len(parts) != 2 {
			continue
		}

		key := strings.TrimSpace(parts[0])
		value := strings.TrimSpace(parts[1])

		// Map config file keys to struct fields
		switch key {
		case "host":
			cfg.DBHost = value
		case "port":
			if port, err := strconv.Atoi(value); err == nil {
				cfg.DBPort = port
			}
		case "database":
			cfg.DBName = value
		case "user":
			cfg.DBUser = value
		case "password":
			cfg.DBPassword = value
		case "scan_interval":
			if interval, err := strconv.Atoi(value); err == nil {
				cfg.ScanInterval = interval
			}
		case "api_port":
			if port, err := strconv.Atoi(value); err == nil {
				cfg.APIPort = port
			}
		case "server_mode":
			cfg.ServerMode = value
		case "registration_key":
			cfg.RegistrationKey = value
		case "main_server_url":
			cfg.MainServerURL = value
		case "tracker_port":
			if port, err := strconv.Atoi(value); err == nil {
				cfg.TrackerPort = port
			}
		case "torrent_data_port":
			if port, err := strconv.Atoi(value); err == nil {
				cfg.TorrentDataPort = port
			}
		case "torrent_data_dir":
			cfg.TorrentDataDir = value
		case "max_upload_rate":
			if rate, err := strconv.Atoi(value); err == nil {
				cfg.MaxUploadRate = rate
			}
		case "max_download_rate":
			if rate, err := strconv.Atoi(value); err == nil {
				cfg.MaxDownloadRate = rate
			}
		case "max_concurrent_seeds":
			if max, err := strconv.Atoi(value); err == nil {
				cfg.MaxConcurrentSeeds = max
			}
	case "max_concurrent_downloads":
		if max, err := strconv.Atoi(value); err == nil {
			cfg.MaxConcurrentDownloads = max
		}
	case "piece_hash_workers":
		if workers, err := strconv.Atoi(value); err == nil {
			cfg.PieceHashWorkers = workers
		}
	case "max_torrent_generation_workers":
		if workers, err := strconv.Atoi(value); err == nil {
			cfg.MaxTorrentGenerationWorkers = workers
		}
	case "scan_path":
		cfg.ScanPath = value
	case "server_name":
		cfg.ServerName = value
	case "server_location":
		cfg.ServerLocation = value
	case "relay_enabled":
		cfg.RelayEnabled = value == "true" || value == "1" || value == "yes"
	case "relay_port":
		if port, err := strconv.Atoi(value); err == nil {
			cfg.RelayPort = port
		}
	case "relay_max_sessions":
		if max, err := strconv.Atoi(value); err == nil {
			cfg.RelayMaxSessions = max
		}
		}
	}

	return scanner.Err()
}

// loadFromEnv reads configuration from environment variables
func (cfg *Config) loadFromEnv() {
	if v := os.Getenv("DB_HOST"); v != "" {
		cfg.DBHost = v
	}
	if v := os.Getenv("DB_PORT"); v != "" {
		if port, err := strconv.Atoi(v); err == nil {
			cfg.DBPort = port
		}
	}
	if v := os.Getenv("DB_NAME"); v != "" {
		cfg.DBName = v
	}
	if v := os.Getenv("DB_USER"); v != "" {
		cfg.DBUser = v
	}
	if v := os.Getenv("DB_PASSWORD"); v != "" {
		cfg.DBPassword = v
	}
	if v := os.Getenv("SCAN_PATH"); v != "" {
		cfg.ScanPath = v
	}
	if v := os.Getenv("API_PORT"); v != "" {
		if port, err := strconv.Atoi(v); err == nil {
			cfg.APIPort = port
		}
	}
	if v := os.Getenv("SCAN_INTERVAL"); v != "" {
		if interval, err := strconv.Atoi(v); err == nil {
			cfg.ScanInterval = interval
		}
	}
	if v := os.Getenv("SERVER_NAME"); v != "" {
		cfg.ServerName = v
	}
	if v := os.Getenv("SERVER_LOCATION"); v != "" {
		cfg.ServerLocation = v
	}
	if v := os.Getenv("SERVER_MODE"); v != "" {
		cfg.ServerMode = v
	}
	if v := os.Getenv("REGISTRATION_KEY"); v != "" {
		cfg.RegistrationKey = v
	}
	if v := os.Getenv("MAIN_SERVER_URL"); v != "" {
		cfg.MainServerURL = v
	}
	if v := os.Getenv("TRACKER_PORT"); v != "" {
		if port, err := strconv.Atoi(v); err == nil {
			cfg.TrackerPort = port
		}
	}
	if v := os.Getenv("TORRENT_DATA_PORT"); v != "" {
		if port, err := strconv.Atoi(v); err == nil {
			cfg.TorrentDataPort = port
		}
	}
	if v := os.Getenv("TORRENT_DATA_DIR"); v != "" {
		cfg.TorrentDataDir = v
	}
	if v := os.Getenv("MAX_UPLOAD_RATE"); v != "" {
		if rate, err := strconv.Atoi(v); err == nil {
			cfg.MaxUploadRate = rate
		}
	}
	if v := os.Getenv("MAX_DOWNLOAD_RATE"); v != "" {
		if rate, err := strconv.Atoi(v); err == nil {
			cfg.MaxDownloadRate = rate
		}
	}
	if v := os.Getenv("MAX_CONCURRENT_SEEDS"); v != "" {
		if max, err := strconv.Atoi(v); err == nil {
			cfg.MaxConcurrentSeeds = max
		}
	}
	if v := os.Getenv("MAX_CONCURRENT_DOWNLOADS"); v != "" {
		if max, err := strconv.Atoi(v); err == nil {
			cfg.MaxConcurrentDownloads = max
		}
	}
	if v := os.Getenv("PIECE_HASH_WORKERS"); v != "" {
		if workers, err := strconv.Atoi(v); err == nil {
			cfg.PieceHashWorkers = workers
		}
	}
	if v := os.Getenv("MAX_TORRENT_GENERATION_WORKERS"); v != "" {
		if workers, err := strconv.Atoi(v); err == nil {
			cfg.MaxTorrentGenerationWorkers = workers
		}
	}
	if v := os.Getenv("RELAY_ENABLED"); v != "" {
		cfg.RelayEnabled = v == "true" || v == "1" || v == "yes"
	}
	if v := os.Getenv("RELAY_PORT"); v != "" {
		if port, err := strconv.Atoi(v); err == nil {
			cfg.RelayPort = port
		}
	}
	if v := os.Getenv("RELAY_MAX_SESSIONS"); v != "" {
		if max, err := strconv.Atoi(v); err == nil {
			cfg.RelayMaxSessions = max
		}
	}
}

// ConnectionString returns a PostgreSQL connection string
func (cfg *Config) ConnectionString() string {
	return fmt.Sprintf(
		"host=%s port=%d user=%s password=%s dbname=%s sslmode=disable",
		cfg.DBHost, cfg.DBPort, cfg.DBUser, cfg.DBPassword, cfg.DBName,
	)
}

// getHostname returns the system hostname
func getHostname() string {
	hostname, err := os.Hostname()
	if err != nil {
		return "unknown"
	}
	return hostname
}

// generateDefaultKey generates a default registration key
func generateDefaultKey() string {
	return "omnicloud-default-key-change-in-production"
}

// IsMainServer returns true if this is the main server
func (cfg *Config) IsMainServer() bool {
	return cfg.ServerMode == "main"
}

// IsClient returns true if this is a client site
func (cfg *Config) IsClient() bool {
	return cfg.ServerMode == "client"
}
