package db

import (
	"time"

	"github.com/google/uuid"
)

// Server represents a server in the network
type Server struct {
	ID                  uuid.UUID
	Name                string
	Location            string
	APIURL              string
	MACAddress          string
	RegistrationKeyHash string
	IsAuthorized        bool
	LastSeen            *time.Time
	StorageCapacityTB   float64
	CreatedAt           time.Time
	UpdatedAt           time.Time
}

// DCPPackage represents a DCP package
type DCPPackage struct {
	ID              uuid.UUID
	AssetMapUUID    uuid.UUID
	PackageName     string
	ContentTitle    string
	ContentKind     string
	IssueDate       *time.Time
	Issuer          string
	Creator         string
	AnnotationText  string
	VolumeCount     int
	TotalSizeBytes  int64
	FileCount       int
	DiscoveredAt    time.Time
	LastVerified    *time.Time
	CreatedAt       time.Time
	UpdatedAt       time.Time
}

// DCPComposition represents a CPL (Composition Playlist)
type DCPComposition struct {
	ID                     uuid.UUID
	PackageID              uuid.UUID
	CPLUUID                uuid.UUID
	ContentTitleText       string
	FullContentTitle       string
	ContentKind            string
	ContentVersionID       *uuid.UUID
	LabelText              string
	IssueDate              *time.Time
	Issuer                 string
	Creator                string
	EditRate               string
	FrameRate              string
	ScreenAspectRatio      string
	ResolutionWidth        int
	ResolutionHeight       int
	MainSoundConfiguration string
	MainSoundSampleRate    string
	Luminance              int
	ReleaseTerritory       string
	Distributor            string
	Facility               string
	ReelCount              int
	TotalDurationFrames    int
	CreatedAt              time.Time
	UpdatedAt              time.Time
}

// DCPReel represents an individual reel from a CPL
type DCPReel struct {
	ID                       uuid.UUID
	CompositionID            uuid.UUID
	ReelUUID                 uuid.UUID
	ReelNumber               int
	DurationFrames           int
	PictureAssetUUID         *uuid.UUID
	PictureEditRate          string
	PictureEntryPoint        int
	PictureIntrinsicDuration int
	PictureKeyID             *uuid.UUID
	PictureHash              string
	SoundAssetUUID           *uuid.UUID
	SoundConfiguration       string
	SubtitleAssetUUID        *uuid.UUID
	SubtitleLanguage         string
	CreatedAt                time.Time
}

// DCPAsset represents an MXF or other asset file
type DCPAsset struct {
	ID            uuid.UUID
	PackageID     uuid.UUID
	AssetUUID     uuid.UUID
	FilePath      string
	FileName      string
	AssetType     string
	AssetRole     string
	SizeBytes     int64
	HashAlgorithm string
	HashValue     string
	ChunkOffset   int64
	ChunkLength   int64
	CreatedAt     time.Time
}

// DCPPackingList represents a PKL
type DCPPackingList struct {
	ID             uuid.UUID
	PackageID      uuid.UUID
	PKLUUID        uuid.UUID
	AnnotationText string
	IssueDate      *time.Time
	Issuer         string
	Creator        string
	AssetCount     int
	CreatedAt      time.Time
}

// ServerDCPInventory represents the junction table
type ServerDCPInventory struct {
	ID           uuid.UUID
	ServerID     uuid.UUID
	PackageID    uuid.UUID
	LocalPath    string
	Status       string
	LastVerified time.Time
	CreatedAt    time.Time
	UpdatedAt    time.Time
}

// ScanLog represents an audit log entry
type ScanLog struct {
	ID              uuid.UUID
	ServerID        uuid.UUID
	ScanType        string
	StartedAt       time.Time
	CompletedAt     *time.Time
	PackagesFound   int
	PackagesAdded   int
	PackagesUpdated int
	PackagesRemoved int
	Errors          string
	Status          string
}
