package application

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"

	"github.com/mirusu400/aram-core/loader"
	"github.com/mirusu400/aram-core/loader/raptor"
)

// imageIdentityVersion prefixes the digest input so a later change to the
// canonical encoding cannot be mistaken for a different image.
const imageIdentityVersion = "aram-image-identity-v1"

// imageSegment is one mapped range of a loaded application image. Data is nil
// for a zero-filled range, which contributes its placement but no bytes.
type imageSegment struct {
	Address    uint32
	Size       uint32
	Writable   bool
	Executable bool
	Data       []byte
}

// imageSHA256 identifies the executable image an application loads, rather
// than the container that delivered it. Re-archiving a package, renaming it,
// or repacking its JAR leaves this digest unchanged, while any difference in
// mapped bytes or placement changes it.
//
// Hash-keyed data binds to this identity because a patch addresses bytes in
// the loaded image; the container is only how they arrived.
func imageSHA256(kind loader.Kind, segments []imageSegment) string {
	digest := sha256.New()
	digest.Write([]byte(imageIdentityVersion))
	digest.Write([]byte{0})
	digest.Write([]byte(kind))
	digest.Write([]byte{0})

	header := make([]byte, 12)
	for _, segment := range segments {
		binary.BigEndian.PutUint32(header[0:4], segment.Address)
		binary.BigEndian.PutUint32(header[4:8], segment.Size)
		var flags uint32
		if segment.Writable {
			flags |= 1
		}
		if segment.Executable {
			flags |= 2
		}
		if segment.Data == nil {
			flags |= 4
		}
		binary.BigEndian.PutUint32(header[8:12], flags)
		digest.Write(header)
		digest.Write(segment.Data)
	}
	return hex.EncodeToString(digest.Sum(nil))
}

// raptorImageSHA256 covers every allocated section in the order raptorrt.MapRaptorImage
// maps them, so the digest describes the same address space the guest runs in.
func raptorImageSHA256(image raptor.Image) string {
	sections := image.AllocatedSections()
	segments := make([]imageSegment, 0, len(sections))
	for _, section := range sections {
		segment := imageSegment{
			Address:    section.Address,
			Size:       section.Size,
			Writable:   section.Writable(),
			Executable: section.Executable(),
		}
		if !section.ZeroFill() {
			segment.Data = section.Data
		}
		segments = append(segments, segment)
	}
	return imageSHA256(loader.KindRaptor, segments)
}
