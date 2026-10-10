package raptor

import (
	"encoding/binary"
	"fmt"

	"github.com/mirusu400/aram-core/application/internal/guest"
	"github.com/mirusu400/aram-core/cpu"
)

// The observed Raptor long[] ABI uses #91 to load and #253 to store. Both take
// array/index in r0/r1; a store takes the high word in r2 and low word in r3,
// while a load returns low/high in r0/r1. World Janggi Chess uses these calls
// to parse its text resources, so returning zero loses all parsed values.
func (r *Runtime) raptorJavaLongArrayImport(ordinal uint32) (guest.WIPIReturn, string, error) {
	name := "RAPTOR.Java.longArrayLoad"
	if ordinal == 253 {
		name = "RAPTOR.Java.longArrayStore"
	}
	array, err := r.CPU.ReadRegister(cpu.RegisterR0)
	if err != nil {
		return guest.WIPIReturn{}, name, err
	}
	index, err := r.CPU.ReadRegister(cpu.RegisterR1)
	if err != nil {
		return guest.WIPIReturn{}, name, err
	}
	address, valid, err := r.raptorJavaLongArraySlot(array, index)
	if err != nil || !valid {
		return guest.WIPIReturn{}, name, err
	}
	var data [8]byte
	if ordinal == 91 {
		if err := r.CPU.ReadMemory(address, data[:]); err != nil {
			return guest.WIPIReturn{}, name, err
		}
		return guest.WIPIReturn{Low: binary.LittleEndian.Uint32(data[:4]), High: binary.LittleEndian.Uint32(data[4:])}, name, nil
	}
	high, err := r.CPU.ReadRegister(cpu.RegisterR2)
	if err != nil {
		return guest.WIPIReturn{}, name, err
	}
	low, err := r.CPU.ReadRegister(cpu.RegisterR3)
	if err != nil {
		return guest.WIPIReturn{}, name, err
	}
	binary.LittleEndian.PutUint32(data[:4], low)
	binary.LittleEndian.PutUint32(data[4:], high)
	if err := r.CPU.WriteMemory(address, data[:]); err != nil {
		return guest.WIPIReturn{}, name, err
	}
	// Keep the shared host's primitive mirror in agreement. A collected mirror
	// does not invalidate the live guest body, as with reference-array stores.
	if java := r.Java; java != nil {
		mirror := java.lgtToKTF[array]
		body, count, width, primitive, ok := java.Host.ArrayShape(mirror)
		if ok && primitive && width == 8 && index < count {
			if err := r.CPU.WriteMemory(body+index*8, data[:]); err != nil {
				return guest.WIPIReturn{}, name, err
			}
		}
	}
	return guest.WIPIReturn{}, name, nil
}

func (r *Runtime) raptorJavaLongArraySlot(array, index uint32) (uint32, bool, error) {
	throw := func(class string) (uint32, bool, error) {
		site, err := r.CPU.ReadRegister(cpu.RegisterLR)
		if err != nil {
			return 0, false, err
		}
		r.recordRaptorJavaThrow(class, site)
		return 0, false, nil
	}
	if array == 0 {
		return throw("java/lang/NullPointerException")
	}
	body, err := r.Public.ReadU32(array + 8)
	if err != nil {
		return 0, false, err
	}
	if body == 0 {
		return 0, false, fmt.Errorf("Raptor Java long array body is null")
	}
	count, err := r.Public.ReadU32(body)
	if err != nil {
		return 0, false, err
	}
	if index >= count {
		return throw("java/lang/ArrayIndexOutOfBoundsException")
	}
	if count > maxRaptorArraySyncElements {
		return 0, false, fmt.Errorf("Raptor Java long array length %d exceeds limit", count)
	}
	if java := r.Java; java != nil {
		if mirror := java.lgtToKTF[array]; mirror != 0 {
			_, _, width, primitive, ok := java.Host.ArrayShape(mirror)
			if ok && (!primitive || width != 8) {
				return 0, false, fmt.Errorf("Raptor Java long array has incompatible element width %d", width)
			}
		}
	}
	address := uint64(body) + 4 + uint64(index)*8
	if address+8 > 1<<32 {
		return 0, false, fmt.Errorf("Raptor Java long array address wraps")
	}
	return uint32(address), true, nil
}
