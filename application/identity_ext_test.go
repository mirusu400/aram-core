package application

import (
	"strings"
	"testing"

	"github.com/mirusu400/aram-core/loader"
	"github.com/mirusu400/aram-core/loader/raptor"
)

func TestImageIdentityTracksMappedBytesNotTheContainer(t *testing.T) {
	t.Parallel()
	image := raptor.Image{Sections: []raptor.Section{
		{
			Index:   1,
			Name:    "ER_RO",
			Flags:   sectionAllocFlag | sectionExecFlag,
			Address: 0x00100000,
			Size:    4,
			Data:    []byte{1, 2, 3, 4},
		},
		{
			Index:   2,
			Name:    "ER_ZI",
			Type:    8,
			Flags:   sectionAllocFlag | sectionWriteFlag,
			Address: 0x00101000,
			Size:    0x100,
		},
	}}

	identity := raptorImageSHA256(image)
	if len(identity) != 64 || strings.Trim(identity, "0123456789abcdef") != "" {
		t.Fatalf("image identity = %q", identity)
	}
	// The same mapped bytes always answer the same, whatever delivered them.
	if again := raptorImageSHA256(image); again != identity {
		t.Fatalf("image identity is not stable: %s then %s", identity, again)
	}

	patched := image
	patched.Sections = append([]raptor.Section(nil), image.Sections...)
	patched.Sections[0].Data = []byte{1, 2, 3, 5}
	if raptorImageSHA256(patched) == identity {
		t.Fatal("a changed code byte kept the image identity")
	}

	moved := image
	moved.Sections = append([]raptor.Section(nil), image.Sections...)
	moved.Sections[0].Address = 0x00200000
	if raptorImageSHA256(moved) == identity {
		t.Fatal("a relocated section kept the image identity")
	}

	// Zero-fill placement counts even though it contributes no bytes.
	resized := image
	resized.Sections = append([]raptor.Section(nil), image.Sections...)
	resized.Sections[1].Size = 0x200
	if raptorImageSHA256(resized) == identity {
		t.Fatal("a resized zero-fill section kept the image identity")
	}

	if imageSHA256(loader.KindKTF, nil) == imageSHA256(loader.KindRaptor, nil) {
		t.Fatal("different source kinds share an image identity")
	}
}
