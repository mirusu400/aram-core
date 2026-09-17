package application

import (
	"encoding/binary"
	"errors"

	"github.com/mirusu400/aram-core/loader/gnex"
)

// ErrInvalidGVMScalarDimensions reports dimensions outside the strict host
// policy of 1..256 inclusive. No defaults, clamping or truncation are applied.
var ErrInvalidGVMScalarDimensions = errors.New("application: invalid GVM scalar dimensions")

// ErrInvalidGVMScalarSymbols reports fewer than 16 symbols or a scalar symbol
// without a complete two-byte backing span.
var ErrInvalidGVMScalarSymbols = errors.New("application: invalid GVM scalar symbols")

// PrepareGVMScalarPrefix returns INCOMPLETE scalar-prefix preparation, NOT an
// initialized runtime, VM or Machine. Callers MUST NOT treat its result as a
// complete initializer or as evidence that a game is ready to execute.
//
// Validation precedence is dimensions first (strict host policy, 1..256), then
// DecodeExecutionImage with its errors propagated unchanged, then at least 16
// symbols and full two-byte spans for slots 0,9,10,15,1,2,3,4,5,6. All validation
// completes before any writes. Errors return a zero ExecutionImage.
//
// A fresh decode owns the entire input and preserves the decoder's file/RAM
// aliases. Only first LE16 words are written, in exactly this order:
// 0=0, 9=0, 10=0, 15=0x3009, 1=width, 2=height, 3=0, 4=0, 5=0, 6=0.
// Caller input and unrelated trailing storage/media are not modified.
//
// This deliberately omits the final mode store, symbol 7/8 writes, lookup/table
// initialization, timers, reserved host-state resets, VM/Factory construction,
// Ready/frame delivery, execution and save-state. In particular it supplies no
// replacement for the unresolved symbol-8 contents and does not enable ordinary
// factory execution. Positive dimension and span guards are host safety policy,
// not a claim about native validation.
func PrepareGVMScalarPrefix(data []byte, width, height int32) (gnex.ExecutionImage, error) {
	if width < 1 || width > 256 || height < 1 || height > 256 {
		return gnex.ExecutionImage{}, ErrInvalidGVMScalarDimensions
	}
	image, err := gnex.DecodeExecutionImage(data)
	if err != nil {
		return gnex.ExecutionImage{}, err
	}
	if len(image.Symbols) < 16 {
		return gnex.ExecutionImage{}, ErrInvalidGVMScalarSymbols
	}
	for _, index := range [...]int{0, 9, 10, 15, 1, 2, 3, 4, 5, 6} {
		if len(image.Symbols[index].Data) < 2 {
			return gnex.ExecutionImage{}, ErrInvalidGVMScalarSymbols
		}
	}
	binary.LittleEndian.PutUint16(image.Symbols[0].Data, 0)
	binary.LittleEndian.PutUint16(image.Symbols[9].Data, 0)
	binary.LittleEndian.PutUint16(image.Symbols[10].Data, 0)
	binary.LittleEndian.PutUint16(image.Symbols[15].Data, 0x3009)
	binary.LittleEndian.PutUint16(image.Symbols[1].Data, uint16(width))
	binary.LittleEndian.PutUint16(image.Symbols[2].Data, uint16(height))
	binary.LittleEndian.PutUint16(image.Symbols[3].Data, 0)
	binary.LittleEndian.PutUint16(image.Symbols[4].Data, 0)
	binary.LittleEndian.PutUint16(image.Symbols[5].Data, 0)
	binary.LittleEndian.PutUint16(image.Symbols[6].Data, 0)
	return image, nil
}
