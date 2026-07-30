// Package skvm implements the portable Java-bytecode core used by SK-VM
// applications. Carrier packaging is kept separately in loader/skvm.
package skvm

import (
	"encoding/binary"
	"fmt"
	"math"
	"strings"
	"unicode/utf16"
)

const (
	MaxClassAttributes = 10_000
	MaxClassMembers    = 10_000
	MaxBytecodeSize    = 65_535
)

const (
	AccessPublic       = uint16(0x0001)
	AccessPrivate      = uint16(0x0002)
	AccessProtected    = uint16(0x0004)
	AccessStatic       = uint16(0x0008)
	AccessFinal        = uint16(0x0010)
	AccessSynchronized = uint16(0x0020)
	AccessNative       = uint16(0x0100)
	AccessInterface    = uint16(0x0200)
	AccessAbstract     = uint16(0x0400)
)

type ReferenceKind string

const (
	ReferenceField     ReferenceKind = "field"
	ReferenceMethod    ReferenceKind = "method"
	ReferenceInterface ReferenceKind = "interface-method"
)

type Reference struct {
	Kind       ReferenceKind
	Class      string
	Name       string
	Descriptor string
}

type ExceptionHandler struct {
	StartPC   uint16
	EndPC     uint16
	HandlerPC uint16
	CatchType string
}

type Field struct {
	AccessFlags   uint16
	Name          string
	Descriptor    string
	ConstantIndex uint16
}

type Method struct {
	AccessFlags uint16
	Name        string
	Descriptor  string
	MaxStack    uint16
	MaxLocals   uint16
	Code        []byte
	Handlers    []ExceptionHandler
}

func (m Method) Native() bool {
	return m.AccessFlags&AccessNative != 0
}

func (m Method) Abstract() bool {
	return m.AccessFlags&AccessAbstract != 0
}

func (m Method) Static() bool {
	return m.AccessFlags&AccessStatic != 0
}

type ConstantKind string

const (
	ConstantInteger ConstantKind = "integer"
	ConstantFloat   ConstantKind = "float"
	ConstantLong    ConstantKind = "long"
	ConstantDouble  ConstantKind = "double"
	ConstantString  ConstantKind = "string"
	ConstantClass   ConstantKind = "class"
)

type Constant struct {
	Kind    ConstantKind
	Integer int32
	Float   float32
	Long    int64
	Double  float64
	String  string
	Class   string
}

type Class struct {
	MinorVersion uint16
	MajorVersion uint16
	AccessFlags  uint16
	Name         string
	SuperName    string
	Interfaces   []string
	Fields       []Field
	Methods      []Method
	pool         []constantPoolEntry
}

type FormatError struct {
	Path   string
	Offset int
	Reason string
}

func (e *FormatError) Error() string {
	if e.Path == "" {
		return fmt.Sprintf("SKVM class at offset 0x%x: %s", e.Offset, e.Reason)
	}
	return fmt.Sprintf("SKVM class %q at offset 0x%x: %s", e.Path, e.Offset, e.Reason)
}

func parseError(path string, offset int, reason string) error {
	return &FormatError{Path: path, Offset: offset, Reason: reason}
}

type constantPoolEntry struct {
	tag   byte
	a     uint16
	b     uint16
	u32   uint32
	u64   uint64
	text  string
	index byte
}

const (
	constantUTF8               = 1
	constantInteger            = 3
	constantFloat              = 4
	constantLong               = 5
	constantDouble             = 6
	constantClass              = 7
	constantString             = 8
	constantFieldref           = 9
	constantMethodref          = 10
	constantInterfaceMethodref = 11
	constantNameAndType        = 12
	constantMethodHandle       = 15
	constantMethodType         = 16
	constantDynamic            = 17
	constantInvokeDynamic      = 18
	constantModule             = 19
	constantPackage            = 20
)

// ParseClass validates and decodes one JVM class file. SKVM titles observed in
// the reference corpus use old CLDC-era class versions, but the constant-pool
// parser accepts later standard tags so diagnostics remain useful.
func ParseClass(path string, data []byte) (*Class, error) {
	reader := classReader{path: path, data: data}
	magic, err := reader.u4("class magic")
	if err != nil {
		return nil, err
	}
	if magic != 0xcafebabe {
		return nil, parseError(path, 0, "class magic is missing")
	}
	minor, err := reader.u2("minor version")
	if err != nil {
		return nil, err
	}
	major, err := reader.u2("major version")
	if err != nil {
		return nil, err
	}
	poolCount, err := reader.u2("constant-pool count")
	if err != nil {
		return nil, err
	}
	if poolCount == 0 {
		return nil, parseError(path, reader.offset-2, "constant-pool count is zero")
	}
	pool := make([]constantPoolEntry, poolCount)
	for index := uint16(1); index < poolCount; index++ {
		tagOffset := reader.offset
		tag, err := reader.u1("constant-pool tag")
		if err != nil {
			return nil, err
		}
		entry := constantPoolEntry{tag: tag}
		switch tag {
		case constantUTF8:
			length, readErr := reader.u2("UTF-8 constant length")
			if readErr != nil {
				return nil, readErr
			}
			raw, readErr := reader.bytes(int(length), "UTF-8 constant")
			if readErr != nil {
				return nil, readErr
			}
			entry.text, readErr = decodeModifiedUTF8(raw)
			if readErr != nil {
				return nil, parseError(path, tagOffset, readErr.Error())
			}
		case constantInteger, constantFloat:
			entry.u32, err = reader.u4("32-bit constant")
		case constantLong, constantDouble:
			var high, low uint32
			high, err = reader.u4("wide constant high word")
			if err == nil {
				low, err = reader.u4("wide constant low word")
			}
			entry.u64 = uint64(high)<<32 | uint64(low)
			if index+1 >= poolCount {
				return nil, parseError(path, tagOffset, "wide constant has no reserved slot")
			}
			pool[index] = entry
			index++
			continue
		case constantClass, constantString, constantMethodType,
			constantModule, constantPackage:
			entry.a, err = reader.u2("constant-pool index")
		case constantFieldref, constantMethodref, constantInterfaceMethodref,
			constantNameAndType, constantDynamic, constantInvokeDynamic:
			entry.a, err = reader.u2("constant-pool index")
			if err == nil {
				entry.b, err = reader.u2("constant-pool index")
			}
		case constantMethodHandle:
			entry.index, err = reader.u1("method-handle kind")
			if err == nil {
				entry.a, err = reader.u2("method-handle reference")
			}
		default:
			return nil, parseError(
				path,
				tagOffset,
				fmt.Sprintf("unsupported constant-pool tag %d", tag),
			)
		}
		if err != nil {
			return nil, err
		}
		pool[index] = entry
	}

	class := &Class{
		MinorVersion: minor,
		MajorVersion: major,
		pool:         pool,
	}
	if class.AccessFlags, err = reader.u2("class access flags"); err != nil {
		return nil, err
	}
	thisIndex, err := reader.u2("this class")
	if err != nil {
		return nil, err
	}
	superIndex, err := reader.u2("super class")
	if err != nil {
		return nil, err
	}
	if class.Name, err = class.className(thisIndex); err != nil {
		return nil, parseError(path, reader.offset-4, "invalid this class: "+err.Error())
	}
	if superIndex != 0 {
		if class.SuperName, err = class.className(superIndex); err != nil {
			return nil, parseError(path, reader.offset-2, "invalid super class: "+err.Error())
		}
	}
	interfaceCount, err := reader.u2("interface count")
	if err != nil {
		return nil, err
	}
	class.Interfaces = make([]string, 0, interfaceCount)
	for range interfaceCount {
		index, readErr := reader.u2("interface")
		if readErr != nil {
			return nil, readErr
		}
		name, resolveErr := class.className(index)
		if resolveErr != nil {
			return nil, parseError(path, reader.offset-2, "invalid interface: "+resolveErr.Error())
		}
		class.Interfaces = append(class.Interfaces, name)
	}

	fieldCount, err := reader.u2("field count")
	if err != nil {
		return nil, err
	}
	if fieldCount > MaxClassMembers {
		return nil, parseError(path, reader.offset-2, "field count exceeds limit")
	}
	class.Fields = make([]Field, 0, fieldCount)
	for range fieldCount {
		field, readErr := parseField(&reader, class)
		if readErr != nil {
			return nil, readErr
		}
		class.Fields = append(class.Fields, field)
	}

	methodCount, err := reader.u2("method count")
	if err != nil {
		return nil, err
	}
	if methodCount > MaxClassMembers {
		return nil, parseError(path, reader.offset-2, "method count exceeds limit")
	}
	class.Methods = make([]Method, 0, methodCount)
	for range methodCount {
		method, readErr := parseMethod(&reader, class)
		if readErr != nil {
			return nil, readErr
		}
		class.Methods = append(class.Methods, method)
	}
	if err := skipAttributes(&reader, class, "class"); err != nil {
		return nil, err
	}
	if reader.offset != len(data) {
		return nil, parseError(path, reader.offset, "trailing bytes after class file")
	}
	return class, nil
}

func parseField(reader *classReader, class *Class) (Field, error) {
	var field Field
	var err error
	if field.AccessFlags, err = reader.u2("field access flags"); err != nil {
		return Field{}, err
	}
	nameIndex, err := reader.u2("field name")
	if err != nil {
		return Field{}, err
	}
	descriptorIndex, err := reader.u2("field descriptor")
	if err != nil {
		return Field{}, err
	}
	if field.Name, err = class.utf8(nameIndex); err != nil {
		return Field{}, parseError(reader.path, reader.offset-4, "invalid field name: "+err.Error())
	}
	if field.Descriptor, err = class.utf8(descriptorIndex); err != nil {
		return Field{}, parseError(reader.path, reader.offset-2, "invalid field descriptor: "+err.Error())
	}
	count, err := reader.u2("field attribute count")
	if err != nil {
		return Field{}, err
	}
	if count > MaxClassAttributes {
		return Field{}, parseError(reader.path, reader.offset-2, "field attribute count exceeds limit")
	}
	for range count {
		name, payload, attrOffset, readErr := readAttribute(reader, class, "field")
		if readErr != nil {
			return Field{}, readErr
		}
		if name != "ConstantValue" {
			continue
		}
		if len(payload) != 2 {
			return Field{}, parseError(reader.path, attrOffset, "ConstantValue attribute has invalid size")
		}
		field.ConstantIndex = binary.BigEndian.Uint16(payload)
		if field.ConstantIndex == 0 || int(field.ConstantIndex) >= len(class.pool) {
			return Field{}, parseError(reader.path, attrOffset, "ConstantValue index is out of range")
		}
	}
	return field, nil
}

func parseMethod(reader *classReader, class *Class) (Method, error) {
	var method Method
	var err error
	if method.AccessFlags, err = reader.u2("method access flags"); err != nil {
		return Method{}, err
	}
	nameIndex, err := reader.u2("method name")
	if err != nil {
		return Method{}, err
	}
	descriptorIndex, err := reader.u2("method descriptor")
	if err != nil {
		return Method{}, err
	}
	if method.Name, err = class.utf8(nameIndex); err != nil {
		return Method{}, parseError(reader.path, reader.offset-4, "invalid method name: "+err.Error())
	}
	if method.Descriptor, err = class.utf8(descriptorIndex); err != nil {
		return Method{}, parseError(reader.path, reader.offset-2, "invalid method descriptor: "+err.Error())
	}
	count, err := reader.u2("method attribute count")
	if err != nil {
		return Method{}, err
	}
	if count > MaxClassAttributes {
		return Method{}, parseError(reader.path, reader.offset-2, "method attribute count exceeds limit")
	}
	haveCode := false
	for range count {
		name, payload, attrOffset, readErr := readAttribute(reader, class, "method")
		if readErr != nil {
			return Method{}, readErr
		}
		if name != "Code" {
			continue
		}
		if haveCode {
			return Method{}, parseError(reader.path, attrOffset, "method has multiple Code attributes")
		}
		haveCode = true
		code, parseErr := parseCode(reader.path, attrOffset, payload, class)
		if parseErr != nil {
			return Method{}, parseErr
		}
		method.MaxStack = code.MaxStack
		method.MaxLocals = code.MaxLocals
		method.Code = code.Code
		method.Handlers = code.Handlers
	}
	if !method.Native() && !method.Abstract() && !haveCode {
		return Method{}, parseError(reader.path, reader.offset, "concrete method has no Code attribute")
	}
	return method, nil
}

func parseCode(path string, base int, data []byte, class *Class) (Method, error) {
	reader := classReader{path: path, data: data, base: base}
	maxStack, err := reader.u2("maximum stack")
	if err != nil {
		return Method{}, err
	}
	maxLocals, err := reader.u2("maximum locals")
	if err != nil {
		return Method{}, err
	}
	codeLength, err := reader.u4("bytecode size")
	if err != nil {
		return Method{}, err
	}
	if codeLength > MaxBytecodeSize {
		return Method{}, parseError(path, base+reader.offset-4, "bytecode exceeds JVM limit")
	}
	code, err := reader.bytes(int(codeLength), "bytecode")
	if err != nil {
		return Method{}, err
	}
	handlerCount, err := reader.u2("exception-handler count")
	if err != nil {
		return Method{}, err
	}
	handlers := make([]ExceptionHandler, 0, handlerCount)
	for range handlerCount {
		startPC, readErr := reader.u2("exception start PC")
		if readErr != nil {
			return Method{}, readErr
		}
		endPC, readErr := reader.u2("exception end PC")
		if readErr != nil {
			return Method{}, readErr
		}
		handlerPC, readErr := reader.u2("exception handler PC")
		if readErr != nil {
			return Method{}, readErr
		}
		catchIndex, readErr := reader.u2("exception catch type")
		if readErr != nil {
			return Method{}, readErr
		}
		if startPC > endPC || uint32(endPC) > codeLength ||
			uint32(handlerPC) >= codeLength {
			return Method{}, parseError(path, base+reader.offset-8, "exception handler is outside bytecode")
		}
		var catchType string
		if catchIndex != 0 {
			catchType, readErr = class.className(catchIndex)
			if readErr != nil {
				return Method{}, parseError(path, base+reader.offset-2, "invalid exception catch type")
			}
		}
		handlers = append(handlers, ExceptionHandler{
			StartPC:   startPC,
			EndPC:     endPC,
			HandlerPC: handlerPC,
			CatchType: catchType,
		})
	}
	if err := skipAttributes(&reader, class, "code"); err != nil {
		return Method{}, err
	}
	if reader.offset != len(data) {
		return Method{}, parseError(path, base+reader.offset, "trailing bytes in Code attribute")
	}
	return Method{
		MaxStack:  maxStack,
		MaxLocals: maxLocals,
		Code:      append([]byte(nil), code...),
		Handlers:  handlers,
	}, nil
}

func skipAttributes(reader *classReader, class *Class, owner string) error {
	count, err := reader.u2(owner + " attribute count")
	if err != nil {
		return err
	}
	if count > MaxClassAttributes {
		return parseError(reader.path, reader.base+reader.offset-2, owner+" attribute count exceeds limit")
	}
	for range count {
		if _, _, _, err := readAttribute(reader, class, owner); err != nil {
			return err
		}
	}
	return nil
}

func readAttribute(
	reader *classReader,
	class *Class,
	owner string,
) (string, []byte, int, error) {
	start := reader.base + reader.offset
	nameIndex, err := reader.u2(owner + " attribute name")
	if err != nil {
		return "", nil, start, err
	}
	name, err := class.utf8(nameIndex)
	if err != nil {
		return "", nil, start, parseError(reader.path, start, "invalid attribute name: "+err.Error())
	}
	length, err := reader.u4(owner + " attribute size")
	if err != nil {
		return "", nil, start, err
	}
	if uint64(length) > uint64(len(reader.data)-reader.offset) {
		return "", nil, start, parseError(reader.path, start, owner+" attribute is truncated")
	}
	payload, err := reader.bytes(int(length), owner+" attribute")
	if err != nil {
		return "", nil, start, err
	}
	return name, payload, start, nil
}

// References lists every field and method reference in constant-pool order.
// Duplicate entries are retained because they are useful when comparing
// obfuscated classes byte-for-byte.
func (c *Class) References() ([]Reference, error) {
	references := make([]Reference, 0)
	for index := 1; index < len(c.pool); index++ {
		switch c.pool[index].tag {
		case constantFieldref, constantMethodref, constantInterfaceMethodref:
			reference, err := c.Reference(uint16(index))
			if err != nil {
				return nil, err
			}
			references = append(references, reference)
		}
	}
	return references, nil
}

func (c *Class) Reference(index uint16) (Reference, error) {
	entry, err := c.entry(index)
	if err != nil {
		return Reference{}, err
	}
	var kind ReferenceKind
	switch entry.tag {
	case constantFieldref:
		kind = ReferenceField
	case constantMethodref:
		kind = ReferenceMethod
	case constantInterfaceMethodref:
		kind = ReferenceInterface
	default:
		return Reference{}, fmt.Errorf("constant-pool entry %d is not a reference", index)
	}
	className, err := c.className(entry.a)
	if err != nil {
		return Reference{}, err
	}
	nameType, err := c.entry(entry.b)
	if err != nil || nameType.tag != constantNameAndType {
		return Reference{}, fmt.Errorf("reference %d has an invalid name-and-type", index)
	}
	name, err := c.utf8(nameType.a)
	if err != nil {
		return Reference{}, err
	}
	descriptor, err := c.utf8(nameType.b)
	if err != nil {
		return Reference{}, err
	}
	return Reference{
		Kind:       kind,
		Class:      className,
		Name:       name,
		Descriptor: descriptor,
	}, nil
}

func (c *Class) Constant(index uint16) (Constant, error) {
	entry, err := c.entry(index)
	if err != nil {
		return Constant{}, err
	}
	switch entry.tag {
	case constantInteger:
		return Constant{Kind: ConstantInteger, Integer: int32(entry.u32)}, nil
	case constantFloat:
		return Constant{Kind: ConstantFloat, Float: math.Float32frombits(entry.u32)}, nil
	case constantLong:
		return Constant{Kind: ConstantLong, Long: int64(entry.u64)}, nil
	case constantDouble:
		return Constant{Kind: ConstantDouble, Double: math.Float64frombits(entry.u64)}, nil
	case constantString:
		value, resolveErr := c.utf8(entry.a)
		return Constant{Kind: ConstantString, String: value}, resolveErr
	case constantClass:
		value, resolveErr := c.utf8(entry.a)
		return Constant{Kind: ConstantClass, Class: value}, resolveErr
	default:
		return Constant{}, fmt.Errorf("constant-pool entry %d is not loadable", index)
	}
}

func (c *Class) Method(name, descriptor string) (Method, bool) {
	for _, method := range c.Methods {
		if method.Name == name && method.Descriptor == descriptor {
			return method, true
		}
	}
	return Method{}, false
}

func (c *Class) utf8(index uint16) (string, error) {
	entry, err := c.entry(index)
	if err != nil {
		return "", err
	}
	if entry.tag != constantUTF8 {
		return "", fmt.Errorf("constant-pool entry %d is not UTF-8", index)
	}
	return entry.text, nil
}

func (c *Class) className(index uint16) (string, error) {
	entry, err := c.entry(index)
	if err != nil {
		return "", err
	}
	if entry.tag != constantClass {
		return "", fmt.Errorf("constant-pool entry %d is not a class", index)
	}
	return c.utf8(entry.a)
}

func (c *Class) entry(index uint16) (constantPoolEntry, error) {
	if index == 0 || int(index) >= len(c.pool) {
		return constantPoolEntry{}, fmt.Errorf("constant-pool index %d is out of range", index)
	}
	entry := c.pool[index]
	if entry.tag == 0 {
		return constantPoolEntry{}, fmt.Errorf("constant-pool index %d is reserved", index)
	}
	return entry, nil
}

type classReader struct {
	path   string
	data   []byte
	offset int
	base   int
}

func (r *classReader) u1(what string) (byte, error) {
	data, err := r.bytes(1, what)
	if err != nil {
		return 0, err
	}
	return data[0], nil
}

func (r *classReader) u2(what string) (uint16, error) {
	data, err := r.bytes(2, what)
	if err != nil {
		return 0, err
	}
	return binary.BigEndian.Uint16(data), nil
}

func (r *classReader) u4(what string) (uint32, error) {
	data, err := r.bytes(4, what)
	if err != nil {
		return 0, err
	}
	return binary.BigEndian.Uint32(data), nil
}

func (r *classReader) bytes(size int, what string) ([]byte, error) {
	if size < 0 || r.offset > len(r.data) || size > len(r.data)-r.offset {
		return nil, parseError(r.path, r.base+r.offset, what+" is truncated")
	}
	data := r.data[r.offset : r.offset+size]
	r.offset += size
	return data, nil
}

func decodeModifiedUTF8(data []byte) (string, error) {
	units := make([]uint16, 0, len(data))
	for offset := 0; offset < len(data); {
		first := data[offset]
		switch {
		case first >= 0x01 && first <= 0x7f:
			units = append(units, uint16(first))
			offset++
		case first&0xe0 == 0xc0:
			if offset+1 >= len(data) || data[offset+1]&0xc0 != 0x80 {
				return "", fmt.Errorf("malformed modified UTF-8")
			}
			value := uint16(first&0x1f)<<6 | uint16(data[offset+1]&0x3f)
			if value != 0 && value < 0x80 {
				return "", fmt.Errorf("overlong modified UTF-8")
			}
			units = append(units, value)
			offset += 2
		case first&0xf0 == 0xe0:
			if offset+2 >= len(data) || data[offset+1]&0xc0 != 0x80 ||
				data[offset+2]&0xc0 != 0x80 {
				return "", fmt.Errorf("malformed modified UTF-8")
			}
			value := uint16(first&0x0f)<<12 |
				uint16(data[offset+1]&0x3f)<<6 |
				uint16(data[offset+2]&0x3f)
			if value < 0x800 {
				return "", fmt.Errorf("overlong modified UTF-8")
			}
			units = append(units, value)
			offset += 3
		default:
			return "", fmt.Errorf("malformed modified UTF-8")
		}
	}
	decoded := utf16.Decode(units)
	if strings.ContainsRune(string(decoded), '\ufffd') {
		for _, unit := range units {
			if unit >= 0xd800 && unit <= 0xdfff {
				return "", fmt.Errorf("unpaired surrogate in modified UTF-8")
			}
		}
	}
	return string(decoded), nil
}
