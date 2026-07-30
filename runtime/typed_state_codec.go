package runtime

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"math"
	"reflect"
	"sort"
)

const (
	maxStateCodecDepth        = 64
	maxStateCollectionEntries = uint64(4 << 20)
)

// encodeStateValue serializes state structs by declared field order. Every
// scalar has a fixed width, collections have a 32-bit element count, maps are
// sorted by scalar key, and all multi-byte values use little endian.
// Host-width integers are intentionally unsupported.
func encodeStateValue(value any) ([]byte, error) {
	encoder := typedStateEncoder{}
	encoder.value(reflect.ValueOf(value), 0)
	if encoder.err != nil {
		return nil, encoder.err
	}
	return encoder.output.Bytes(), nil
}

// MarshalStateComponent exposes the shared fixed-width codec to adapter-owned
// save-state components. Callers must use only explicit-width fields and
// validate the decoded object graph before committing it.
func MarshalStateComponent(value any) ([]byte, error) {
	return encodeStateValue(value)
}

// UnmarshalStateComponent decodes one complete fixed-width adapter component.
// It rejects non-canonical booleans, map ordering, collection presence flags,
// unsupported host-width fields, and trailing bytes.
func UnmarshalStateComponent(data []byte, target any) error {
	if uint64(len(data)) > MaxServicesStateBytes {
		return fmt.Errorf("%w: typed state exceeds byte limit", ErrLimitExceeded)
	}
	return decodeStateValue(data, target)
}

func decodeStateValue(data []byte, target any) error {
	value := reflect.ValueOf(target)
	if !value.IsValid() || value.Kind() != reflect.Pointer || value.IsNil() {
		return fmt.Errorf("%w: state decode target is not a pointer", ErrInvalidArgument)
	}
	decoder := binaryStateDecoder{reader: bytes.NewReader(data)}
	decodeTypedStateValue(&decoder, value.Elem(), 0)
	if decoder.err != nil {
		return decoder.err
	}
	if decoder.reader.Len() != 0 {
		return decoder.fail(
			fmt.Sprintf("%d trailing typed-state bytes", decoder.reader.Len()),
		)
	}
	return nil
}

type typedStateEncoder struct {
	output bytes.Buffer
	err    error
}

func (e *typedStateEncoder) value(value reflect.Value, depth int) {
	if e.err != nil {
		return
	}
	if depth > maxStateCodecDepth || !value.IsValid() {
		e.fail("invalid state value or nesting depth")
		return
	}
	switch value.Kind() {
	case reflect.Bool:
		if value.Bool() {
			e.u8(1)
		} else {
			e.u8(0)
		}
	case reflect.Int8:
		e.u8(uint8(value.Int()))
	case reflect.Int16:
		e.u16(uint16(value.Int()))
	case reflect.Int32:
		e.u32(uint32(value.Int()))
	case reflect.Int64:
		e.u64(uint64(value.Int()))
	case reflect.Uint8:
		e.u8(uint8(value.Uint()))
	case reflect.Uint16:
		e.u16(uint16(value.Uint()))
	case reflect.Uint32:
		e.u32(uint32(value.Uint()))
	case reflect.Uint64:
		e.u64(value.Uint())
	case reflect.String:
		text := value.String()
		if uint64(len(text)) > math.MaxUint32 {
			e.fail("state string exceeds 32-bit length")
			return
		}
		e.u32(uint32(len(text)))
		e.write([]byte(text))
	case reflect.Array:
		for index := 0; index < value.Len(); index++ {
			e.value(value.Index(index), depth+1)
		}
	case reflect.Slice:
		if value.IsNil() {
			e.u8(0)
			return
		}
		e.u8(1)
		if uint64(value.Len()) > math.MaxUint32 {
			e.fail("state slice exceeds 32-bit length")
			return
		}
		e.u32(uint32(value.Len()))
		if value.Type().Elem().Kind() == reflect.Uint8 {
			e.write(value.Bytes())
			return
		}
		for index := 0; index < value.Len(); index++ {
			e.value(value.Index(index), depth+1)
		}
	case reflect.Pointer:
		if value.IsNil() {
			e.u8(0)
			return
		}
		e.u8(1)
		e.value(value.Elem(), depth+1)
	case reflect.Map:
		if value.IsNil() {
			e.u8(0)
			return
		}
		if !validStateMapKeyKind(value.Type().Key().Kind()) {
			e.fail(fmt.Sprintf(
				"state map %s has unsupported key type",
				value.Type(),
			))
			return
		}
		e.u8(1)
		if uint64(value.Len()) > math.MaxUint32 {
			e.fail("state map exceeds 32-bit length")
			return
		}
		e.u32(uint32(value.Len()))
		keys := value.MapKeys()
		sort.Slice(keys, func(i, j int) bool {
			return stateMapKeyLess(keys[i], keys[j])
		})
		for _, key := range keys {
			e.value(key, depth+1)
			e.value(value.MapIndex(key), depth+1)
		}
	case reflect.Struct:
		currentType := value.Type()
		for index := 0; index < value.NumField(); index++ {
			if currentType.Field(index).PkgPath != "" {
				e.fail(fmt.Sprintf(
					"state struct %s has unexported field %s",
					currentType,
					currentType.Field(index).Name,
				))
				return
			}
			e.value(value.Field(index), depth+1)
		}
	default:
		e.fail(fmt.Sprintf(
			"state type %s uses unsupported kind %s",
			value.Type(),
			value.Kind(),
		))
	}
}

func (e *typedStateEncoder) write(data []byte) {
	if e.err != nil {
		return
	}
	if uint64(len(data)) > MaxServicesStateBytes ||
		uint64(e.output.Len()) > MaxServicesStateBytes-uint64(len(data)) {
		e.fail("typed state exceeds byte limit")
		return
	}
	_, _ = e.output.Write(data)
}

func (e *typedStateEncoder) u8(value uint8) {
	e.write([]byte{value})
}

func (e *typedStateEncoder) u16(value uint16) {
	var encoded [2]byte
	binary.LittleEndian.PutUint16(encoded[:], value)
	e.write(encoded[:])
}

func (e *typedStateEncoder) u32(value uint32) {
	var encoded [4]byte
	binary.LittleEndian.PutUint32(encoded[:], value)
	e.write(encoded[:])
}

func (e *typedStateEncoder) u64(value uint64) {
	var encoded [8]byte
	binary.LittleEndian.PutUint64(encoded[:], value)
	e.write(encoded[:])
}

func (e *typedStateEncoder) fail(reason string) {
	if e.err == nil {
		e.err = fmt.Errorf("%w: %s", ErrInvalidState, reason)
	}
}

func decodeTypedStateValue(
	decoder *binaryStateDecoder,
	value reflect.Value,
	depth int,
) {
	if decoder.err != nil {
		return
	}
	if depth > maxStateCodecDepth || !value.IsValid() || !value.CanSet() {
		setStateDecoderError(decoder, "invalid state target or nesting depth")
		return
	}
	switch value.Kind() {
	case reflect.Bool:
		encoded := decoder.u8()
		if encoded > 1 {
			setStateDecoderError(decoder, "invalid boolean encoding")
			return
		}
		value.SetBool(encoded != 0)
	case reflect.Int8:
		value.SetInt(int64(int8(decoder.u8())))
	case reflect.Int16:
		value.SetInt(int64(int16(decoder.u16())))
	case reflect.Int32:
		value.SetInt(int64(int32(decoder.u32())))
	case reflect.Int64:
		value.SetInt(int64(decoder.u64()))
	case reflect.Uint8:
		value.SetUint(uint64(decoder.u8()))
	case reflect.Uint16:
		value.SetUint(uint64(decoder.u16()))
	case reflect.Uint32:
		value.SetUint(uint64(decoder.u32()))
	case reflect.Uint64:
		value.SetUint(decoder.u64())
	case reflect.String:
		size := decoder.u32()
		if uint64(size) > uint64(decoder.reader.Len()) {
			setStateDecoderError(decoder, "state string exceeds remaining payload")
			return
		}
		value.SetString(string(decoder.bytes(int(size))))
	case reflect.Array:
		for index := 0; index < value.Len(); index++ {
			decodeTypedStateValue(decoder, value.Index(index), depth+1)
		}
	case reflect.Slice:
		present := decoder.u8()
		if present > 1 {
			setStateDecoderError(decoder, "invalid slice presence encoding")
			return
		}
		if present == 0 {
			value.SetZero()
			return
		}
		count := decoder.u32()
		if value.Type().Elem().Kind() == reflect.Uint8 {
			if uint64(count) > uint64(decoder.reader.Len()) {
				setStateDecoderError(decoder, "byte slice exceeds remaining payload")
				return
			}
			data := decoder.bytes(int(count))
			decoded := reflect.ValueOf(data)
			if decoded.Type() != value.Type() {
				decoded = decoded.Convert(value.Type())
			}
			value.Set(decoded)
			return
		}
		if uint64(count) > maxStateCollectionEntries ||
			uint64(count) > uint64(maxIntValue()) {
			setStateDecoderError(decoder, "state collection count exceeds limit")
			return
		}
		minimum, err := minimumStateEncodingSize(value.Type().Elem(), depth+1)
		if err != nil {
			setStateDecoderError(decoder, err.Error())
			return
		}
		if minimum != 0 &&
			uint64(count) > uint64(decoder.reader.Len())/minimum {
			setStateDecoderError(decoder, "state collection exceeds remaining payload")
			return
		}
		decoded := reflect.MakeSlice(value.Type(), int(count), int(count))
		for index := 0; index < int(count); index++ {
			decodeTypedStateValue(decoder, decoded.Index(index), depth+1)
		}
		value.Set(decoded)
	case reflect.Pointer:
		present := decoder.u8()
		if present > 1 {
			setStateDecoderError(decoder, "invalid pointer presence encoding")
			return
		}
		if present == 0 {
			value.SetZero()
			return
		}
		decoded := reflect.New(value.Type().Elem())
		decodeTypedStateValue(decoder, decoded.Elem(), depth+1)
		value.Set(decoded)
	case reflect.Map:
		present := decoder.u8()
		if present > 1 {
			setStateDecoderError(decoder, "invalid map presence encoding")
			return
		}
		if present == 0 {
			value.SetZero()
			return
		}
		if !validStateMapKeyKind(value.Type().Key().Kind()) {
			setStateDecoderError(
				decoder,
				fmt.Sprintf("state map %s has unsupported key type", value.Type()),
			)
			return
		}
		count := decoder.u32()
		if uint64(count) > maxStateCollectionEntries ||
			uint64(count) > uint64(maxIntValue()) {
			setStateDecoderError(decoder, "state map count exceeds limit")
			return
		}
		keyMinimum, err := minimumStateEncodingSize(
			value.Type().Key(),
			depth+1,
		)
		if err != nil {
			setStateDecoderError(decoder, err.Error())
			return
		}
		valueMinimum, err := minimumStateEncodingSize(
			value.Type().Elem(),
			depth+1,
		)
		if err != nil || keyMinimum > ^uint64(0)-valueMinimum {
			if err != nil {
				setStateDecoderError(decoder, err.Error())
			} else {
				setStateDecoderError(decoder, "state map minimum size overflows")
			}
			return
		}
		entryMinimum := keyMinimum + valueMinimum
		if entryMinimum != 0 &&
			uint64(count) > uint64(decoder.reader.Len())/entryMinimum {
			setStateDecoderError(decoder, "state map exceeds remaining payload")
			return
		}
		decoded := reflect.MakeMapWithSize(value.Type(), int(count))
		var previous reflect.Value
		for index := 0; index < int(count); index++ {
			key := reflect.New(value.Type().Key()).Elem()
			decodeTypedStateValue(decoder, key, depth+1)
			if decoder.err != nil {
				return
			}
			if index != 0 && !stateMapKeyLess(previous, key) {
				setStateDecoderError(decoder, "non-canonical state map key order")
				return
			}
			item := reflect.New(value.Type().Elem()).Elem()
			decodeTypedStateValue(decoder, item, depth+1)
			if decoder.err != nil {
				return
			}
			decoded.SetMapIndex(key, item)
			previous = reflect.New(key.Type()).Elem()
			previous.Set(key)
		}
		value.Set(decoded)
	case reflect.Struct:
		currentType := value.Type()
		for index := 0; index < value.NumField(); index++ {
			if currentType.Field(index).PkgPath != "" {
				setStateDecoderError(
					decoder,
					fmt.Sprintf(
						"state struct %s has unexported field %s",
						currentType,
						currentType.Field(index).Name,
					),
				)
				return
			}
			decodeTypedStateValue(decoder, value.Field(index), depth+1)
		}
	default:
		setStateDecoderError(
			decoder,
			fmt.Sprintf(
				"state type %s uses unsupported kind %s",
				value.Type(),
				value.Kind(),
			),
		)
	}
}

func minimumStateEncodingSize(valueType reflect.Type, depth int) (uint64, error) {
	if depth > maxStateCodecDepth {
		return 0, fmt.Errorf("state type nesting exceeds limit")
	}
	switch valueType.Kind() {
	case reflect.Bool, reflect.Int8, reflect.Uint8:
		return 1, nil
	case reflect.Int16, reflect.Uint16:
		return 2, nil
	case reflect.Int32, reflect.Uint32:
		return 4, nil
	case reflect.Int64, reflect.Uint64:
		return 8, nil
	case reflect.String:
		return 4, nil
	case reflect.Slice:
		return 5, nil
	case reflect.Pointer:
		return 1, nil
	case reflect.Map:
		if !validStateMapKeyKind(valueType.Key().Kind()) {
			return 0, fmt.Errorf(
				"state map %s has unsupported key type",
				valueType,
			)
		}
		return 5, nil
	case reflect.Array:
		element, err := minimumStateEncodingSize(valueType.Elem(), depth+1)
		if err != nil {
			return 0, err
		}
		if element != 0 && uint64(valueType.Len()) > ^uint64(0)/element {
			return 0, fmt.Errorf("state array minimum size overflows")
		}
		return uint64(valueType.Len()) * element, nil
	case reflect.Struct:
		var total uint64
		for index := 0; index < valueType.NumField(); index++ {
			if valueType.Field(index).PkgPath != "" {
				return 0, fmt.Errorf(
					"state struct %s has unexported field %s",
					valueType,
					valueType.Field(index).Name,
				)
			}
			field, err := minimumStateEncodingSize(
				valueType.Field(index).Type,
				depth+1,
			)
			if err != nil {
				return 0, err
			}
			if total > ^uint64(0)-field {
				return 0, fmt.Errorf("state struct minimum size overflows")
			}
			total += field
		}
		return total, nil
	default:
		return 0, fmt.Errorf(
			"state type %s uses unsupported kind %s",
			valueType,
			valueType.Kind(),
		)
	}
}

func validStateMapKeyKind(kind reflect.Kind) bool {
	switch kind {
	case reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64,
		reflect.String:
		return true
	default:
		return false
	}
}

func stateMapKeyLess(left, right reflect.Value) bool {
	switch left.Kind() {
	case reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return left.Int() < right.Int()
	case reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return left.Uint() < right.Uint()
	case reflect.String:
		return left.String() < right.String()
	default:
		return false
	}
}

func (d *binaryStateDecoder) u8() uint8 {
	data := d.bytes(1)
	if len(data) != 1 {
		return 0
	}
	return data[0]
}

func setStateDecoderError(decoder *binaryStateDecoder, reason string) {
	if decoder.err == nil {
		decoder.err = decoder.fail(reason)
	}
}
