package unicornbackend

import "github.com/mirusu400/aram-core/cpu"

const (
	supportedUnicornAPIMajor = uint32(2)
	ucArchARM                = int32(1)
	ucModeARM926             = int32(1 << 7)
	ucPageSize               = uint32(4096)
)

const (
	ucErrOK int32 = iota
	ucErrNoMemory
	ucErrArchitecture
	ucErrHandle
	ucErrMode
	ucErrVersion
	ucErrReadUnmapped
	ucErrWriteUnmapped
	ucErrFetchUnmapped
	ucErrHook
	ucErrInstructionInvalid
	ucErrMap
	ucErrWriteProtection
	ucErrReadProtection
	ucErrFetchProtection
	ucErrArgument
	ucErrReadUnaligned
	ucErrWriteUnaligned
	ucErrFetchUnaligned
	ucErrHookExists
	ucErrResource
	ucErrException
	ucErrOverflow
)

var unicornErrorNames = map[int32]string{
	ucErrNoMemory:           "UC_ERR_NOMEM",
	ucErrArchitecture:       "UC_ERR_ARCH",
	ucErrHandle:             "UC_ERR_HANDLE",
	ucErrMode:               "UC_ERR_MODE",
	ucErrVersion:            "UC_ERR_VERSION",
	ucErrReadUnmapped:       "UC_ERR_READ_UNMAPPED",
	ucErrWriteUnmapped:      "UC_ERR_WRITE_UNMAPPED",
	ucErrFetchUnmapped:      "UC_ERR_FETCH_UNMAPPED",
	ucErrHook:               "UC_ERR_HOOK",
	ucErrInstructionInvalid: "UC_ERR_INSN_INVALID",
	ucErrMap:                "UC_ERR_MAP",
	ucErrWriteProtection:    "UC_ERR_WRITE_PROT",
	ucErrReadProtection:     "UC_ERR_READ_PROT",
	ucErrFetchProtection:    "UC_ERR_FETCH_PROT",
	ucErrArgument:           "UC_ERR_ARG",
	ucErrReadUnaligned:      "UC_ERR_READ_UNALIGNED",
	ucErrWriteUnaligned:     "UC_ERR_WRITE_UNALIGNED",
	ucErrFetchUnaligned:     "UC_ERR_FETCH_UNALIGNED",
	ucErrHookExists:         "UC_ERR_HOOK_EXIST",
	ucErrResource:           "UC_ERR_RESOURCE",
	ucErrException:          "UC_ERR_EXCEPTION",
	ucErrOverflow:           "UC_ERR_OVERFLOW",
}

// Unicorn 2.x ARM register IDs from the public C ABI. General registers R0-R12
// are contiguous; SP/LR/PC precede the vector-register range.
const (
	ucARMRegCPSR = int32(3)
	ucARMRegLR   = int32(10)
	ucARMRegPC   = int32(11)
	ucARMRegSP   = int32(12)
	ucARMRegR0   = int32(66)
)

var unicornRegisterIDs = [17]int32{
	ucARMRegR0 + 0,
	ucARMRegR0 + 1,
	ucARMRegR0 + 2,
	ucARMRegR0 + 3,
	ucARMRegR0 + 4,
	ucARMRegR0 + 5,
	ucARMRegR0 + 6,
	ucARMRegR0 + 7,
	ucARMRegR0 + 8,
	ucARMRegR0 + 9,
	ucARMRegR0 + 10,
	ucARMRegR0 + 11,
	ucARMRegR0 + 12,
	ucARMRegSP,
	ucARMRegLR,
	ucARMRegPC,
	ucARMRegCPSR,
}

const (
	statusN = uint32(1 << 31)
	statusZ = uint32(1 << 30)
	statusC = uint32(1 << 29)
	statusV = uint32(1 << 28)
	statusQ = uint32(1 << 27)

	applicationStatusMask = statusN | statusZ | statusC | statusV | statusQ |
		cpu.StatusThumb
)

func validBackendMode(mode cpu.Mode) bool {
	return mode == cpu.ModeARM || mode == cpu.ModeThumb
}
