package wipi

import (
	"encoding/binary"
	"github.com/mirusu400/aram-core/application/internal/guest"
	"time"
)

func (r *Runtime) timeFields(seconds uint64) ([36]byte, error) {
	temporary, err := r.Heap.Allocate(36, true)
	if err != nil || temporary == 0 {
		return [36]byte{}, err
	}
	if err := r.writeU64(temporary, seconds); err != nil {
		return [36]byte{}, err
	}
	result, _, err := r.breakDownTime(temporary)
	r.Heap.Release(temporary)
	if err != nil {
		return [36]byte{}, err
	}
	var fields [36]byte
	if err := r.CPU.ReadMemory(result.Low, fields[:]); err != nil {
		return [36]byte{}, err
	}
	r.Heap.Release(result.Low)
	return fields, nil
}

func (r *Runtime) breakDownTime(pointer uint32) (guest.WIPIReturn, bool, error) {
	seconds := uint64(r.Services.Clock.WallMillis() / 1000)
	if pointer != 0 {
		low, err := r.ReadU32(pointer)
		if err != nil {
			return guest.WIPIReturn{}, true, err
		}
		high, err := r.ReadU32(pointer + 4)
		if err != nil {
			return guest.WIPIReturn{}, true, err
		}
		seconds = uint64(low) | uint64(high)<<32
	}
	value := time.Unix(int64(seconds), 0).UTC()
	target, err := r.Heap.Allocate(9*4, true)
	if err != nil || target == 0 {
		return guest.WIPIReturn{}, true, err
	}
	yearDay := value.YearDay() - 1
	weekDay := int(value.Weekday())
	fields := [...]int32{
		int32(value.Second()),
		int32(value.Minute()),
		int32(value.Hour()),
		int32(value.Day()),
		int32(value.Month() - 1),
		int32(value.Year() - 1900),
		int32(weekDay),
		int32(yearDay),
		0,
	}
	var encoded [9 * 4]byte
	for index, field := range fields {
		binary.LittleEndian.PutUint32(encoded[index*4:], uint32(field))
	}
	return guest.WIPIReturn{Low: target}, true, r.CPU.WriteMemory(target, encoded[:])
}

func (r *Runtime) mktime(pointer uint32) (guest.WIPIReturn, bool, error) {
	var encoded [9 * 4]byte
	if err := r.CPU.ReadMemory(pointer, encoded[:]); err != nil {
		return guest.WIPIReturn{}, true, err
	}
	field := func(index int) int {
		return int(int32(binary.LittleEndian.Uint32(encoded[index*4:])))
	}
	value := time.Date(
		field(5)+1900,
		time.Month(field(4)+1),
		field(3),
		field(2),
		field(1),
		field(0),
		0,
		time.UTC,
	).Unix()
	return wipiU64(uint64(value)), true, nil
}
