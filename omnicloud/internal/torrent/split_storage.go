package torrent

// SplitPathStorage is a torrent storage implementation for co-seeding deduplicated DCPs.
//
// Background: When two cinema sites receive the same DCP via RosettaBridge, the MXF
// content files are byte-for-byte identical but the XML metadata files (ASSETMAP.xml,
// PKL.xml, VOLINDEX.xml) differ because RosettaBridge generates fresh ones per delivery.
// Since BitTorrent piece hashes are positional and cover all files, the second site
// cannot seed the canonical torrent unless its XML files exactly match.
//
// We solve this WITHOUT overwriting the library XMLs (which RosettaBridge depends on)
// by storing canonical XML copies in a separate shadow directory. SplitPathStorage
// routes reads to the right place:
//   - .mxf files  → mxfBaseDir  (the original library, e.g. /library/DCP_NAME/)
//   - XML/other   → xmlBaseDir  (the shadow dir, e.g. /omnicloud/canonical-xml/<pkg_id>/)
//
// Both directories use the same relative file layout (relative to the DCP root), so
// the storage layer can resolve any file by checking its extension.

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/anacrolix/torrent/metainfo"
	"github.com/anacrolix/torrent/storage"
)

// NewSplitPathStorage creates a storage ClientImpl that reads:
//   - .mxf files from mxfParentDir/<torrent_name>/...
//   - all other files from xmlParentDir/<torrent_name>/... (the canonical XML shadow dir)
//
// mxfParentDir is the parent of the DCP directory in the library.
// xmlParentDir is the parent of the shadow DCP directory (usually the shadow base itself,
// since the shadow dir IS the DCP directory already named after the canonical package ID —
// the torrent's info.Name is used as a subdirectory).
//
// completion is the piece completion tracker (PostgreSQL-backed).
func NewSplitPathStorage(mxfParentDir, xmlParentDir string, completion storage.PieceCompletion) storage.ClientImplCloser {
	return &splitClientImpl{
		mxfParentDir: mxfParentDir,
		xmlParentDir: xmlParentDir,
		completion:   completion,
	}
}

type splitClientImpl struct {
	mxfParentDir string
	xmlParentDir string
	completion   storage.PieceCompletion
}

func (s *splitClientImpl) Close() error {
	return s.completion.Close()
}

func (s *splitClientImpl) OpenTorrent(info *metainfo.Info, infoHash metainfo.Hash) (storage.TorrentImpl, error) {
	// Resolve full base directories for MXF and XML files using the torrent name
	// (torrent name == DCP directory name, e.g. "IZ469BACK_ADV_F_EN-XX_UK-XX_51_2K_...")
	mxfDir := filepath.Join(s.mxfParentDir, info.Name)
	xmlDir := filepath.Join(s.xmlParentDir, info.Name)

	return &splitTorrentImpl{
		info:       info,
		infoHash:   infoHash,
		mxfDir:     mxfDir,
		xmlDir:     xmlDir,
		completion: s.completion,
	}, nil
}

type splitTorrentImpl struct {
	info       *metainfo.Info
	infoHash   metainfo.Hash
	mxfDir     string // full path to DCP dir with MXF files
	xmlDir     string // full path to shadow dir with canonical XML files
	completion storage.PieceCompletion
}

func (t *splitTorrentImpl) Piece(p metainfo.Piece) storage.PieceImpl {
	return &splitPieceImpl{
		torrent:      t,
		piece:        p,
		completionPK: metainfo.PieceKey{InfoHash: t.infoHash, Index: p.Index()},
	}
}

func (t *splitTorrentImpl) Close() error {
	return nil
}

// resolveFile returns the full filesystem path for a file in the torrent,
// routing .mxf files to mxfDir and everything else to xmlDir.
func (t *splitTorrentImpl) resolveFile(fi metainfo.FileInfo) string {
	relParts := fi.Path
	// Determine base directory by extension of the last path component
	name := relParts[len(relParts)-1]
	ext := strings.ToLower(filepath.Ext(name))

	var base string
	if ext == ".mxf" {
		base = t.mxfDir
	} else {
		base = t.xmlDir
	}

	parts := append([]string{base}, relParts...)
	return filepath.Join(parts...)
}

type splitPieceImpl struct {
	torrent      *splitTorrentImpl
	piece        metainfo.Piece
	completionPK metainfo.PieceKey
}

func (p *splitPieceImpl) ReadAt(b []byte, pieceOffset int64) (int, error) {
	// pieceOffset is relative to the start of this piece within the torrent's data stream.
	// We need to find which file(s) this reads from, accounting for the absolute offset
	// within the entire torrent data.
	absOff := p.piece.Offset() + pieceOffset
	return p.readFromFiles(b, absOff)
}

func (p *splitPieceImpl) WriteAt(b []byte, pieceOffset int64) (int, error) {
	// Co-seeding: we should only need to read. If the library tries to write (e.g. during
	// hash verification for a new download), route to the appropriate file.
	absOff := p.piece.Offset() + pieceOffset
	return p.writeToFiles(b, absOff)
}

func (p *splitPieceImpl) readFromFiles(b []byte, absOff int64) (int, error) {
	n := 0
	remaining := b

	for _, fi := range p.torrent.info.UpvertedFiles() {
		if absOff >= fi.Length {
			absOff -= fi.Length
			continue
		}

		toRead := int64(len(remaining))
		if toRead > fi.Length-absOff {
			toRead = fi.Length - absOff
		}

		fpath := p.torrent.resolveFile(fi)
		f, err := os.OpenFile(fpath, os.O_RDONLY, 0)
		if err != nil {
			return n, fmt.Errorf("open %s: %w", fpath, err)
		}
		n1, err := f.ReadAt(remaining[:toRead], absOff)
		f.Close()
		n += n1
		remaining = remaining[n1:]
		if err != nil && err != io.EOF {
			return n, err
		}
		if len(remaining) == 0 {
			return n, nil
		}
		absOff = 0 // subsequent files start from offset 0
	}

	if len(remaining) > 0 {
		return n, io.EOF
	}
	return n, nil
}

func (p *splitPieceImpl) writeToFiles(b []byte, absOff int64) (int, error) {
	n := 0
	remaining := b

	for _, fi := range p.torrent.info.UpvertedFiles() {
		if absOff >= fi.Length {
			absOff -= fi.Length
			continue
		}

		toWrite := int64(len(remaining))
		if toWrite > fi.Length-absOff {
			toWrite = fi.Length - absOff
		}

		fpath := p.torrent.resolveFile(fi)
		os.MkdirAll(filepath.Dir(fpath), 0755)
		f, err := os.OpenFile(fpath, os.O_WRONLY|os.O_CREATE, 0666)
		if err != nil {
			return n, fmt.Errorf("open %s for write: %w", fpath, err)
		}
		n1, err := f.WriteAt(remaining[:toWrite], absOff)
		f.Close()
		n += n1
		remaining = remaining[n1:]
		if err != nil {
			return n, err
		}
		if len(remaining) == 0 {
			return n, nil
		}
		absOff = 0
	}
	return n, nil
}

func (p *splitPieceImpl) MarkComplete() error {
	p.torrent.completion.Set(p.completionPK, true)
	return nil
}

func (p *splitPieceImpl) MarkNotComplete() error {
	p.torrent.completion.Set(p.completionPK, false)
	return nil
}

func (p *splitPieceImpl) Completion() storage.Completion {
	c, err := p.torrent.completion.Get(p.completionPK)
	return storage.Completion{Complete: c.Complete, Ok: err == nil}
}
