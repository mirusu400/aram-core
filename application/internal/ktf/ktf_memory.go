package ktf

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
)

func (r *Runtime) inspectExecutable(address uint32) (ktfExecutable, error) {
	if !r.imagePointer(address, 40) {
		return ktfExecutable{}, fmt.Errorf(
			"KTF WipiExe pointer 0x%08x is outside client image",
			address,
		)
	}
	words, err := r.ReadWords(address, 10)
	if err != nil {
		return ktfExecutable{}, err
	}
	interfaceAddress := words[0]
	if !r.imagePointer(interfaceAddress, 32) {
		return ktfExecutable{}, fmt.Errorf(
			"KTF ExeInterface pointer 0x%08x is outside client image",
			interfaceAddress,
		)
	}
	interfaceWords, err := r.ReadWords(interfaceAddress, 8)
	if err != nil {
		return ktfExecutable{}, err
	}
	functionsAddress := interfaceWords[0]
	if !r.imagePointer(functionsAddress, 28) {
		return ktfExecutable{}, fmt.Errorf(
			"KTF ExeInterfaceFunctions pointer 0x%08x is outside client image",
			functionsAddress,
		)
	}
	functions, err := r.ReadWords(functionsAddress, 7)
	if err != nil {
		return ktfExecutable{}, err
	}
	nameAddress := words[1]
	if nameAddress == 0 {
		nameAddress = interfaceWords[1]
	}
	name, err := r.readImageString(nameAddress, 256)
	if err != nil {
		return ktfExecutable{}, err
	}
	executable := ktfExecutable{
		WipiExeAddress:      address,
		ExeInterfaceAddress: interfaceAddress,
		FunctionsAddress:    functionsAddress,
		Name:                name,
		ExecutableInit:      words[5],
		InterfaceInit:       functions[2],
		GetDefaultDLL:       functions[3],
		GetClass:            functions[4],
		InterfaceUnknown2:   functions[5],
		InterfaceUnknown3:   functions[6],
	}
	for label, procedure := range map[string]uint32{
		"WipiExe.fn_init":             executable.ExecutableInit,
		"ExeInterfaceFunctions.init":  executable.InterfaceInit,
		"ExeInterfaceFunctions.class": executable.GetClass,
	} {
		if procedure != 0 && !r.imagePointer(procedure&^1, 2) {
			return ktfExecutable{}, fmt.Errorf(
				"KTF %s pointer 0x%08x is outside client image",
				label,
				procedure,
			)
		}
	}
	return executable, nil
}

func (r *Runtime) imagePointer(address, size uint32) bool {
	if address < ImageBase {
		return false
	}
	return uint64(address)+uint64(size) <=
		uint64(ImageBase)+uint64(r.ImageSz)
}

func (r *Runtime) ReadWords(address uint32, count int) ([]uint32, error) {
	data := make([]byte, count*4)
	if err := r.CPU.ReadMemory(address, data); err != nil {
		return nil, fmt.Errorf("read KTF structure at 0x%08x: %w", address, err)
	}
	words := make([]uint32, count)
	for index := range words {
		words[index] = binary.LittleEndian.Uint32(data[index*4 : index*4+4])
	}
	return words, nil
}

func (r *Runtime) ReadU32(address uint32) (uint32, error) {
	var data [4]byte
	if err := r.CPU.ReadMemory(address, data[:]); err != nil {
		return 0, fmt.Errorf("read KTF word at 0x%08x: %w", address, err)
	}
	return binary.LittleEndian.Uint32(data[:]), nil
}

func (r *Runtime) WriteU32(address, value uint32) error {
	var data [4]byte
	binary.LittleEndian.PutUint32(data[:], value)
	if err := r.CPU.WriteMemory(address, data[:]); err != nil {
		return fmt.Errorf("write KTF word at 0x%08x: %w", address, err)
	}
	return nil
}

func (r *Runtime) readImageString(address, limit uint32) (string, error) {
	if address == 0 {
		return "", nil
	}
	if !r.imagePointer(address, 1) {
		return "", fmt.Errorf("KTF string pointer 0x%08x is outside client image", address)
	}
	end := min(uint64(ImageBase)+uint64(r.ImageSz), uint64(address)+uint64(limit))
	data := make([]byte, int(end-uint64(address)))
	if err := r.CPU.ReadMemory(address, data); err != nil {
		return "", err
	}
	if index := bytes.IndexByte(data, 0); index >= 0 {
		return string(data[:index]), nil
	}
	return "", fmt.Errorf("KTF string at 0x%08x is not terminated within %d bytes", address, limit)
}

func (r *Runtime) readCString(address, limit uint32) (string, error) {
	if address == 0 {
		return "", errors.New("KTF string pointer is null")
	}
	data := make([]byte, 0, min(limit, 64))
	var chunk [64]byte
	for offset := uint32(0); offset < limit; {
		// Read in blocks. A per-byte ReadMemory takes the backend lock and
		// resolves the mapped region once per character, which dominated
		// Java method-name resolution on KTF titles.
		size := min(uint32(len(chunk)), limit-offset)
		var err error
		for size > 0 {
			if err = r.CPU.ReadMemory(address+offset, chunk[:size]); err == nil {
				break
			}
			// The block may straddle the end of a mapped region. Narrow it
			// until it fits so a string that terminates before the boundary
			// still reads, and only report the fault once nothing fits.
			size /= 2
		}
		if size == 0 {
			return "", fmt.Errorf(
				"read KTF string at 0x%08x: %w",
				address+offset,
				err,
			)
		}
		if index := bytes.IndexByte(chunk[:size], 0); index >= 0 {
			return string(append(data, chunk[:index]...)), nil
		}
		data = append(data, chunk[:size]...)
		offset += size
	}
	return "", fmt.Errorf("KTF string at 0x%08x is not terminated within %d bytes", address, limit)
}
