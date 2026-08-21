// Package firmwareset defines privacy-safe, random-access firmware input sets.
// It deliberately carries no filesystem paths: product integrations retain
// host handles while core state and reports use only sizes and content hashes.
package firmwareset

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
)

const ManifestSchema = "aram-firmware-set-v1"

var DefaultLimits = Limits{
	MaxPieces:     32,
	MaxPieceBytes: 1 << 30,
	MaxTotalBytes: 4 << 30,
}

type Limits struct {
	MaxPieces     int
	MaxPieceBytes int64
	MaxTotalBytes int64
}

func (l Limits) validate() error {
	if l.MaxPieces <= 0 || l.MaxPieceBytes <= 0 || l.MaxTotalBytes <= 0 {
		return fmt.Errorf("firmware limits must be positive")
	}
	return nil
}

// Source is an ephemeral host-provided byte source. It intentionally has no
// path or name field, so those values cannot leak into manifests or states.
type Source struct {
	ReaderAt io.ReaderAt
	Size     int64
}

type ReadError struct {
	Piece  int
	Offset int64
	Err    error
}

func (e *ReadError) Error() string {
	return fmt.Sprintf("firmware piece %d read at offset 0x%x: %v", e.Piece, e.Offset, e.Err)
}

func (e *ReadError) Unwrap() error {
	return e.Err
}

type Piece struct {
	index  int
	source Source
	digest [sha256.Size]byte
}

func (p Piece) Index() int {
	return p.index
}

func (p Piece) Size() int64 {
	return p.source.Size
}

func (p Piece) SHA256() string {
	return hex.EncodeToString(p.digest[:])
}

// ReadAt bounds every access to the range that was hashed by NewSet.
func (p Piece) ReadAt(destination []byte, offset int64) (int, error) {
	if offset < 0 || offset > p.source.Size || int64(len(destination)) > p.source.Size-offset {
		return 0, &ReadError{Piece: p.index, Offset: max(offset, 0), Err: io.ErrUnexpectedEOF}
	}
	if len(destination) == 0 {
		return 0, nil
	}
	count, err := p.source.ReaderAt.ReadAt(destination, offset)
	if count == len(destination) {
		return count, nil
	}
	if err == nil || errors.Is(err, io.EOF) {
		err = io.ErrUnexpectedEOF
	}
	return count, &ReadError{
		Piece:  p.index,
		Offset: offset + int64(count),
		Err:    err,
	}
}

type Set struct {
	pieces    []Piece
	totalSize int64
}

func NewSet(sources []Source) (Set, error) {
	return NewSetWithLimits(sources, DefaultLimits)
}

func NewSetWithLimits(sources []Source, limits Limits) (Set, error) {
	if err := limits.validate(); err != nil {
		return Set{}, err
	}
	if len(sources) == 0 {
		return Set{}, fmt.Errorf("firmware set is empty")
	}
	if len(sources) > limits.MaxPieces {
		return Set{}, fmt.Errorf("firmware set has %d pieces, limit is %d", len(sources), limits.MaxPieces)
	}

	set := Set{pieces: make([]Piece, 0, len(sources))}
	for index, source := range sources {
		if source.ReaderAt == nil {
			return Set{}, fmt.Errorf("firmware piece %d has no random-access reader", index)
		}
		if source.Size <= 0 || source.Size > limits.MaxPieceBytes {
			return Set{}, fmt.Errorf(
				"firmware piece %d has size %d, limit is %d",
				index,
				source.Size,
				limits.MaxPieceBytes,
			)
		}
		if source.Size > limits.MaxTotalBytes-set.totalSize {
			return Set{}, fmt.Errorf("firmware set exceeds %d bytes", limits.MaxTotalBytes)
		}
		digest, err := hashSource(index, source)
		if err != nil {
			return Set{}, err
		}
		set.pieces = append(set.pieces, Piece{index: index, source: source, digest: digest})
		set.totalSize += source.Size
	}
	return set, nil
}

func hashSource(index int, source Source) ([sha256.Size]byte, error) {
	hash := sha256.New()
	buffer := make([]byte, 1024*1024)
	for offset := int64(0); offset < source.Size; {
		want := min(int64(len(buffer)), source.Size-offset)
		count, err := source.ReaderAt.ReadAt(buffer[:int(want)], offset)
		if count > 0 {
			_, _ = hash.Write(buffer[:count])
			offset += int64(count)
		}
		if count != int(want) {
			if err == nil || errors.Is(err, io.EOF) {
				err = io.ErrUnexpectedEOF
			}
			return [sha256.Size]byte{}, &ReadError{Piece: index, Offset: offset, Err: err}
		}
		if err != nil && !errors.Is(err, io.EOF) {
			return [sha256.Size]byte{}, &ReadError{Piece: index, Offset: offset, Err: err}
		}
	}
	var digest [sha256.Size]byte
	copy(digest[:], hash.Sum(nil))
	return digest, nil
}

func (s Set) Len() int {
	return len(s.pieces)
}

func (s Set) TotalSize() int64 {
	return s.totalSize
}

func (s Set) Piece(index int) (Piece, error) {
	if index < 0 || index >= len(s.pieces) {
		return Piece{}, fmt.Errorf("firmware piece index %d is out of range", index)
	}
	return s.pieces[index], nil
}

type Manifest struct {
	Schema string          `json:"schema"`
	Pieces []PieceManifest `json:"pieces"`
}

type PieceManifest struct {
	Index  int    `json:"index"`
	Size   int64  `json:"size"`
	SHA256 string `json:"sha256"`
}

func (s Set) Manifest() Manifest {
	manifest := Manifest{Schema: ManifestSchema, Pieces: make([]PieceManifest, len(s.pieces))}
	for index, piece := range s.pieces {
		manifest.Pieces[index] = PieceManifest{
			Index:  piece.Index(),
			Size:   piece.Size(),
			SHA256: piece.SHA256(),
		}
	}
	return manifest
}
