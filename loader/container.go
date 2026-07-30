package loader

import (
	"bytes"
	"errors"
	"fmt"
	"math"

	"github.com/mirusu400/aram-core/loader/abhs"
	"github.com/mirusu400/aram-core/loader/eads"
)

var ErrNoContainerRecords = errors.New("no valid ABHS or EADS records")

type ContainerRecord struct {
	Kind   Kind
	Offset uint32
	Size   uint32
	Index  int
}

type Container struct {
	Modules           []abhs.Module
	Images            []eads.Image
	Records           []ContainerRecord
	FirstModuleOffset uint32
	ModuleChainEnd    uint32
	FirstImageOffset  uint32
}

// InspectContainer scans a DAT/resource blob for structurally valid ABHS and
// EADS records. Invalid magic-like byte strings are ignored. Once a valid
// record is found, its complete range is skipped so magic bytes embedded in
// code are not reported as additional records.
func InspectContainer(data []byte) (Container, error) {
	if uint64(len(data)) > math.MaxUint32 {
		return Container{}, fmt.Errorf("inspect container: input exceeds 32-bit format")
	}

	var found Container
	for offset := 0; offset+4 <= len(data); {
		switch {
		case bytes.Equal(data[offset:offset+4], abhs.Magic):
			module, err := abhs.Parse(data, uint32(offset))
			if err != nil {
				offset++
				continue
			}
			index := len(found.Modules)
			if index == 0 {
				found.FirstModuleOffset = module.RecordOffset
			}
			found.Modules = append(found.Modules, module)
			found.ModuleChainEnd = module.RecordEnd()
			found.Records = append(found.Records, ContainerRecord{
				Kind:   KindABHS,
				Offset: module.RecordOffset,
				Size:   module.RecordSize,
				Index:  index,
			})
			offset = int(module.RecordEnd())
		case bytes.Equal(data[offset:offset+4], eads.Magic):
			image, err := eads.Parse(data, uint32(offset))
			if err != nil {
				offset++
				continue
			}
			index := len(found.Images)
			if index == 0 {
				found.FirstImageOffset = image.RecordOffset
			}
			found.Images = append(found.Images, image)
			found.Records = append(found.Records, ContainerRecord{
				Kind:   KindEADS,
				Offset: image.RecordOffset,
				Size:   image.RecordEnd() - image.RecordOffset,
				Index:  index,
			})
			offset = int(image.RecordEnd())
		default:
			offset++
		}
	}
	if len(found.Records) == 0 {
		return Container{}, ErrNoContainerRecords
	}
	return found, nil
}
