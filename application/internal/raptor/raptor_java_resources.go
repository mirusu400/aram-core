package raptor

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"path"
	"strings"

	"github.com/mirusu400/aram-core/application/internal/guest"
	"github.com/mirusu400/aram-core/cpu"
)

const raptorResourceBytesHostModule = ^uint32(1)

var raptorResourceBytesHelperPrologue = []byte{
	0x70, 0xb5, // push {r4-r6, lr}
	0x84, 0xb0, // sub sp, #16
	0x01, 0x90, // str r0, [sp, #4]
}

func (r *Runtime) installResourceBytesHelper() error {
	if r.resourceBytesHelper == 0 {
		return nil
	}
	actual := make([]byte, len(raptorResourceBytesHelperPrologue))
	if err := r.CPU.ReadMemory(r.resourceBytesHelper, actual); err != nil {
		return fmt.Errorf("read Raptor resource helper: %w", err)
	}
	if !bytes.Equal(actual, raptorResourceBytesHelperPrologue) {
		return fmt.Errorf(
			"Raptor resource helper at 0x%08x has unexpected prologue %x",
			r.resourceBytesHelper,
			actual,
		)
	}
	key := raptorImportKey{Module: raptorResourceBytesHostModule}
	stub, err := r.importStub(key)
	if err != nil {
		return err
	}
	r.resolvedImports[key] = 1
	var veneer [8]byte
	binary.LittleEndian.PutUint16(veneer[0:2], 0x4b00) // ldr r3, [pc, #0]
	binary.LittleEndian.PutUint16(veneer[2:4], 0x4718) // bx r3
	binary.LittleEndian.PutUint32(veneer[4:8], stub|1)
	if err := r.CPU.WriteMemory(r.resourceBytesHelper, veneer[:]); err != nil {
		return fmt.Errorf("patch Raptor resource helper: %w", err)
	}
	return nil
}

func normalizeRaptorResourceName(value string) (string, bool) {
	name := strings.ReplaceAll(value, `\`, "/")
	name = strings.TrimPrefix(name, "/")
	name = path.Clean(name)
	if name == "." || name == ".." || strings.HasPrefix(name, "../") {
		return "", false
	}
	return name, true
}

func (r *Runtime) raptorResourceBytes() (guest.WIPIReturn, error) {
	nameObject, err := r.CPU.ReadRegister(cpu.RegisterR0)
	if err != nil {
		return guest.WIPIReturn{}, err
	}
	java, err := r.ensureJavaRuntime()
	if err != nil {
		return guest.WIPIReturn{}, err
	}
	mirror := java.lgtToKTF[nameObject]
	if mirror == 0 {
		mirror = nameObject
	}
	name, ok := normalizeRaptorResourceName(java.Host.JavaStrings[mirror])
	if !ok {
		return guest.WIPIReturn{}, nil
	}
	data, found := r.Pkg.Resources[name]
	if !found {
		for candidate, payload := range r.Pkg.Resources {
			if strings.EqualFold(candidate, name) {
				data, found = payload, true
				break
			}
		}
	}
	if !found {
		data = nil
	}
	array, err := r.newRaptorJavaArray('B', uint32(len(data)))
	if err != nil {
		return guest.WIPIReturn{}, err
	}
	body, err := r.Public.ReadU32(array + 8)
	if err != nil {
		return guest.WIPIReturn{}, err
	}
	if body == 0 {
		return guest.WIPIReturn{}, errors.New("read Raptor resource byte-array body")
	}
	if err := r.CPU.WriteMemory(body+4, data); err != nil {
		return guest.WIPIReturn{}, err
	}
	if mirrorArray := java.lgtToKTF[array]; mirrorArray != 0 {
		r.syncRaptorArrayArguments(java, []uint32{mirrorArray}, true)
	}
	return guest.WIPIReturn{Low: array}, nil
}
