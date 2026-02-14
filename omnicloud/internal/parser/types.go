package parser

import "encoding/xml"

// ASSETMAP structures
type AssetMap struct {
	XMLName     xml.Name `xml:"AssetMap"`
	ID          string   `xml:"Id"`
	Creator     string   `xml:"Creator"`
	VolumeCount int      `xml:"VolumeCount"`
	IssueDate   string   `xml:"IssueDate"`
	Issuer      string   `xml:"Issuer"`
	AssetList   struct {
		Assets []Asset `xml:"Asset"`
	} `xml:"AssetList"`
}

type Asset struct {
	ID          string `xml:"Id"`
	PackingList bool   `xml:"PackingList"`
	ChunkList   struct {
		Chunks []Chunk `xml:"Chunk"`
	} `xml:"ChunkList"`
}

type Chunk struct {
	Path        string `xml:"Path"`
	VolumeIndex int    `xml:"VolumeIndex"`
	Offset      int64  `xml:"Offset"`
	Length      int64  `xml:"Length"`
}

// PKL structures
type PackingList struct {
	XMLName        xml.Name `xml:"PackingList"`
	ID             string   `xml:"Id"`
	AnnotationText string   `xml:"AnnotationText"`
	IssueDate      string   `xml:"IssueDate"`
	Issuer         string   `xml:"Issuer"`
	Creator        string   `xml:"Creator"`
	AssetList      struct {
		Assets []PKLAsset `xml:"Asset"`
	} `xml:"AssetList"`
}

type PKLAsset struct {
	ID   string `xml:"Id"`
	Hash string `xml:"Hash"`
	Size int64  `xml:"Size"`
	Type string `xml:"Type"`
}

// CPL structures
type CompositionPlaylist struct {
	XMLName          xml.Name `xml:"CompositionPlaylist"`
	ID               string   `xml:"Id"`
	AnnotationText   string   `xml:"AnnotationText"`
	IssueDate        string   `xml:"IssueDate"`
	Issuer           string   `xml:"Issuer"`
	Creator          string   `xml:"Creator"`
	ContentTitleText string   `xml:"ContentTitleText"`
	ContentKind      string   `xml:"ContentKind"`
	ContentVersion   struct {
		ID        string `xml:"Id"`
		LabelText string `xml:"LabelText"`
	} `xml:"ContentVersion"`
	ReelList struct {
		Reels []Reel `xml:"Reel"`
	} `xml:"ReelList"`
}

type Reel struct {
	ID        string    `xml:"Id"`
	AssetList AssetList `xml:"AssetList"`
}

type AssetList struct {
	MainPicture  *MainPicture  `xml:"MainPicture"`
	MainSound    *MainSound    `xml:"MainSound"`
	MainSubtitle *MainSubtitle `xml:"MainSubtitle"`
	Metadata     *Metadata     `xml:"CompositionMetadataAsset"`
}

type MainPicture struct {
	ID                 string `xml:"Id"`
	EditRate           string `xml:"EditRate"`
	IntrinsicDuration  int    `xml:"IntrinsicDuration"`
	EntryPoint         int    `xml:"EntryPoint"`
	Duration           int    `xml:"Duration"`
	KeyID              string `xml:"KeyId"`
	Hash               string `xml:"Hash"`
	FrameRate          string `xml:"FrameRate"`
	ScreenAspectRatio  string `xml:"ScreenAspectRatio"`
}

type MainSound struct {
	ID                string `xml:"Id"`
	EditRate          string `xml:"EditRate"`
	IntrinsicDuration int    `xml:"IntrinsicDuration"`
	EntryPoint        int    `xml:"EntryPoint"`
	Duration          int    `xml:"Duration"`
	KeyID             string `xml:"KeyId"`
	Hash              string `xml:"Hash"`
}

type MainSubtitle struct {
	ID                string `xml:"Id"`
	EditRate          string `xml:"EditRate"`
	IntrinsicDuration int    `xml:"IntrinsicDuration"`
	EntryPoint        int    `xml:"EntryPoint"`
	Duration          int    `xml:"Duration"`
	KeyID             string `xml:"KeyId"`
	Hash              string `xml:"Hash"`
	Language          string `xml:"Language"`
}

type Metadata struct {
	ID                       string `xml:"Id"`
	EditRate                 string `xml:"EditRate"`
	IntrinsicDuration        int    `xml:"IntrinsicDuration"`
	FullContentTitleText     string `xml:"FullContentTitleText"`
	ReleaseTerritory         string `xml:"ReleaseTerritory"`
	VersionNumber            string `xml:"VersionNumber"`
	Chain                    string `xml:"Chain"`
	Distributor              string `xml:"Distributor"`
	Facility                 string `xml:"Facility"`
	Luminance                int    `xml:"Luminance"`
	MainSoundConfiguration   string `xml:"MainSoundConfiguration"`
	MainSoundSampleRate      string `xml:"MainSoundSampleRate"`
	MainPictureStoredArea    struct {
		Width  int `xml:"Width"`
		Height int `xml:"Height"`
	} `xml:"MainPictureStoredArea"`
	MainPictureActiveArea    struct {
		Width  int `xml:"Width"`
		Height int `xml:"Height"`
	} `xml:"MainPictureActiveArea"`
}
