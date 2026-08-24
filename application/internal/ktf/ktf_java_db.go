package ktf

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
	"sort"
	"strings"
	"unicode/utf16"

	"github.com/mirusu400/aram-core/cpu"
)

func (r *Runtime) handleDataBaseMethod(
	ctx context.Context,
	name, descriptor string,
) (uint32, error) {
	switch name + descriptor {
	case "<init>(Ljavax/microedition/rms/RecordStore;)V",
		"closeDataBase()V":
		return 0, nil
	case "openDataBase(Ljava/lang/String;IZ)Lorg/kwis/msp/db/DataBase;",
		"openDataBase(Ljava/lang/String;IZI)Lorg/kwis/msp/db/DataBase;":
		nameAddress, err := r.parameter(1)
		if err != nil {
			return 0, err
		}
		recordSize, err := r.parameter(2)
		if err != nil {
			return 0, err
		}
		create, err := r.parameter(3)
		if err != nil {
			return 0, err
		}
		databaseName := r.javaText(nameAddress)
		if databaseName == "" {
			databaseName = fmt.Sprintf("database-%08x", nameAddress)
		}
		store := r.DatabaseStores[databaseName]
		r.tracef(
			"java_database_open:%s:create=%t:exists=%t",
			databaseName,
			create != 0,
			store != nil,
		)
		if store == nil {
			if create == 0 && !r.LenientMissingRead {
				return r.raiseJavaException(
					"org/kwis/msp/db/DataBaseException",
					0,
				)
			}
			// With no guest exception unwinding (Raptor host), a first-run open
			// of a not-yet-created database must not fault the machine: create
			// it empty so the title reads zero records and falls back to
			// defaults, matching a device that catches DataBaseException.
			store = &Database{Name: databaseName, RecordSize: recordSize}
			r.DatabaseStores[databaseName] = store
			serviceID, serviceErr := r.Services.Storage.CreateRecordStore(
				r.ServiceOwner,
				databaseName,
			)
			if serviceErr != nil {
				return 0, serviceErr
			}
			r.DatabaseServices[databaseName] = serviceID
		}
		classAddress, err := r.EnsureJavaClass("org/kwis/msp/db/DataBase")
		if err != nil {
			return 0, err
		}
		class, err := r.InspectJavaClass(classAddress)
		if err != nil {
			return 0, err
		}
		instance, err := r.NewJavaInstanceForClass(class)
		if err != nil {
			return 0, err
		}
		r.databases[instance] = store
		return instance, nil
	case "getNumberOfRecords()I":
		store, err := r.databaseParameter(1)
		if err != nil {
			return 0, err
		}
		return uint32(len(store.Records)), nil
	case "insertRecord([B)I":
		store, err := r.databaseParameter(1)
		if err != nil {
			return 0, err
		}
		array, err := r.parameter(2)
		if err != nil {
			return 0, err
		}
		var data []byte
		if array != 0 {
			data, err = r.readJavaByteArray(array)
			if err != nil {
				return 0, err
			}
		}
		store.Records = append(store.Records, data)
		if err := r.syncKTFDatabase(store); err != nil {
			return 0, err
		}
		return uint32(len(store.Records) - 1), nil
	case "insertRecord([BII)I":
		store, err := r.databaseParameter(1)
		if err != nil {
			return 0, err
		}
		array, err := r.parameter(2)
		if err != nil {
			return 0, err
		}
		offset, err := r.parameter(3)
		if err != nil {
			return 0, err
		}
		count, err := r.parameter(4)
		if err != nil {
			return 0, err
		}
		var data []byte
		if array != 0 {
			data, err = r.readJavaByteArrayRange(array, offset, count)
			if err != nil {
				return 0, err
			}
		}
		store.Records = append(store.Records, data)
		if err := r.syncKTFDatabase(store); err != nil {
			return 0, err
		}
		return uint32(len(store.Records) - 1), nil
	case "selectRecord(I)[B":
		store, err := r.databaseParameter(1)
		if err != nil {
			return 0, err
		}
		recordID, err := r.parameter(2)
		if err != nil {
			return 0, err
		}
		if recordID >= uint32(len(store.Records)) {
			return r.newJavaByteArray(nil)
		}
		return r.newJavaByteArray(store.Records[recordID])
	case "updateRecord(I[B)V", "updateRecord(I[BII)V":
		store, err := r.databaseParameter(1)
		if err != nil {
			return 0, err
		}
		recordID, err := r.parameter(2)
		if err != nil {
			return 0, err
		}
		array, err := r.parameter(3)
		if err != nil {
			return 0, err
		}
		var data []byte
		if array == 0 {
			data = nil
		} else if descriptor == "(I[BII)V" {
			offset, err := r.parameter(4)
			if err != nil {
				return 0, err
			}
			count, err := r.parameter(5)
			if err != nil {
				return 0, err
			}
			data, err = r.readJavaByteArrayRange(array, offset, count)
			if err != nil {
				return 0, err
			}
		} else {
			data, err = r.readJavaByteArray(array)
			if err != nil {
				return 0, err
			}
		}
		if recordID >= uint32(len(store.Records)) {
			if recordID > 65535 {
				return 0, fmt.Errorf(
					"KTF database record ID %d exceeds compatibility limit",
					recordID,
				)
			}
			store.Records = append(
				store.Records,
				make([][]byte, int(recordID)+1-len(store.Records))...,
			)
		}
		store.Records[recordID] = data
		return 0, r.syncKTFDatabase(store)
	case "deleteDataBase(Ljava/lang/String;)V":
		nameAddress, err := r.parameter(1)
		if err != nil {
			return 0, err
		}
		databaseName := r.javaText(nameAddress)
		if serviceID := r.DatabaseServices[databaseName]; serviceID != 0 {
			if err := r.Services.Storage.DeleteRecordStore(
				r.ServiceOwner,
				serviceID,
			); err != nil {
				return 0, err
			}
			delete(r.DatabaseServices, databaseName)
		}
		delete(r.DatabaseStores, databaseName)
		return 0, nil
	case "deleteRecord(I)V":
		store, err := r.databaseParameter(1)
		if err != nil {
			return 0, err
		}
		recordID, err := r.parameter(2)
		if err != nil {
			return 0, err
		}
		if recordID >= uint32(len(store.Records)) {
			return r.raiseJavaException(
				"org/kwis/msp/db/DataBaseRecordException",
				0,
			)
		}
		// Record ids are slot indices, so the slot empties in place to
		// keep every other id stable.
		store.Records[recordID] = nil
		return 0, r.syncKTFDatabase(store)
	case "getDataBaseName()Ljava/lang/String;":
		store, err := r.databaseParameter(1)
		if err != nil {
			return 0, err
		}
		return r.NewJavaString(store.Name)
	case "getDataBaseSize()I":
		store, err := r.databaseParameter(1)
		if err != nil {
			return 0, err
		}
		total := 0
		for _, record := range store.Records {
			total += len(record)
		}
		return uint32(total), nil
	case "getRecordSize()I":
		store, err := r.databaseParameter(1)
		if err != nil {
			return 0, err
		}
		return store.RecordSize, nil
	case "getSizeAvailable()I":
		return 1 << 20, nil
	case "getLastModified()J":
		return r.javaLongResult(r.TickMS), nil
	case "getAccessMode(Ljava/lang/String;)I":
		nameAddress, err := r.parameter(1)
		if err != nil {
			return 0, err
		}
		if r.DatabaseStores[r.javaText(nameAddress)] == nil {
			return ^uint32(0), nil
		}
		// Read and write access.
		return 3, nil
	case "listDataBases()[Ljava/lang/String;":
		names := make([]string, 0, len(r.DatabaseStores))
		for name := range r.DatabaseStores {
			names = append(names, name)
		}
		sort.Strings(names)
		references := make([]uint32, len(names))
		for index, name := range names {
			reference, err := r.NewJavaString(name)
			if err != nil {
				return 0, err
			}
			references[index] = reference
		}
		return r.newJavaReferenceArray("[Ljava/lang/String;", references)
	case "sortRecord(Lorg/kwis/msp/db/DataFilter;" +
		"Lorg/kwis/msp/db/DataComparator;)[I":
		store, err := r.databaseParameter(1)
		if err != nil {
			return 0, err
		}
		filter, err := r.parameter(2)
		if err != nil {
			return 0, err
		}
		comparator, err := r.parameter(3)
		if err != nil {
			return 0, err
		}
		return r.sortKTFDatabase(ctx, store, filter, comparator)
	default:
		return 0, nil
	}
}

// dbCallbackClass resolves the class name behind a DataFilter or
// DataComparator reference so host implementations run natively and guest
// implementations run through their compiled bodies.
func (r *Runtime) dbCallbackClass(instance uint32) string {
	if instance == 0 {
		return ""
	}
	classAddress, err := r.ReadU32(instance + 4)
	if err != nil {
		return ""
	}
	class, err := r.InspectJavaClass(classAddress)
	if err != nil {
		return ""
	}
	return class.Name
}

func (r *Runtime) sortKTFDatabase(
	ctx context.Context,
	store *Database,
	filter, comparator uint32,
) (uint32, error) {
	filterClass := r.dbCallbackClass(filter)
	comparatorClass := r.dbCallbackClass(comparator)
	invokeFilter := func(record []byte) (bool, error) {
		switch filterClass {
		case "":
			return true, nil
		case "org/kwis/msp/db/DataFilterInteger":
			state := r.lwcComponent(filter)
			offset := int(state.mode)
			if offset < 0 || offset+4 > len(record) {
				return false, nil
			}
			value := int32(binary.BigEndian.Uint32(record[offset:]))
			return value >= state.minimum && value <= state.progressMax, nil
		default:
			array, err := r.newJavaByteArray(record)
			if err != nil {
				return false, err
			}
			result, err := r.invokeJavaVirtual(
				ctx,
				filter,
				"filter",
				"([B)Z",
				array,
			)
			return result != 0, err
		}
	}
	compareRecords := func(left, right []byte) (int, error) {
		switch comparatorClass {
		case "":
			return 0, nil
		case "org/kwis/msp/db/DataComparatorInteger":
			leftValue, rightValue := int32(0), int32(0)
			if len(left) >= 4 {
				leftValue = int32(binary.BigEndian.Uint32(left))
			}
			if len(right) >= 4 {
				rightValue = int32(binary.BigEndian.Uint32(right))
			}
			switch {
			case leftValue < rightValue:
				return -1, nil
			case leftValue > rightValue:
				return 1, nil
			}
			return 0, nil
		case "org/kwis/msp/db/DataComparatorString":
			return bytes.Compare(left, right), nil
		default:
			leftArray, err := r.newJavaByteArray(left)
			if err != nil {
				return 0, err
			}
			rightArray, err := r.newJavaByteArray(right)
			if err != nil {
				return 0, err
			}
			result, err := r.invokeJavaVirtual(
				ctx,
				comparator,
				"compare",
				"([B[B)I",
				leftArray,
				rightArray,
			)
			return int(int32(result)), err
		}
	}
	selected := make([]uint32, 0, len(store.Records))
	for recordID, record := range store.Records {
		if record == nil {
			continue
		}
		keep, err := invokeFilter(record)
		if err != nil {
			return 0, err
		}
		if keep {
			selected = append(selected, uint32(recordID))
		}
	}
	var sortErr error
	sort.SliceStable(selected, func(i, j int) bool {
		if sortErr != nil {
			return false
		}
		order, err := compareRecords(
			store.Records[selected[i]],
			store.Records[selected[j]],
		)
		if err != nil {
			sortErr = err
		}
		return order < 0
	})
	if sortErr != nil {
		return 0, sortErr
	}
	return r.newJavaIntArray(selected)
}

func (r *Runtime) newJavaIntArray(values []uint32) (uint32, error) {
	instance, err := r.NewJavaArray("[I", uint32(len(values)), 4)
	if err != nil {
		return 0, err
	}
	fields, err := r.ReadU32(instance)
	if err != nil {
		return 0, err
	}
	if len(values) == 0 {
		return instance, nil
	}
	encoded := make([]byte, len(values)*4)
	for index, value := range values {
		binary.LittleEndian.PutUint32(encoded[index*4:], value)
	}
	if err := r.CPU.WriteMemory(fields+8, encoded); err != nil {
		return 0, err
	}
	return instance, nil
}

// handleDataComparatorMethod backs the host comparator and filter classes.
// Constructor parameters land in the generic per-instance component state:
// mode carries the field offset, minimum and progressMax carry the filter
// bounds.
func (r *Runtime) handleDataComparatorMethod(
	className, name, descriptor string,
) (uint32, error) {
	instance, err := r.parameter(1)
	if err != nil {
		return 0, err
	}
	switch name + descriptor {
	case "<init>()V":
		return 0, nil
	case "<init>(I)V":
		offset, valueErr := r.parameter(2)
		if valueErr != nil {
			return 0, valueErr
		}
		r.lwcComponent(instance).mode = int32(offset)
		return 0, nil
	case "<init>(III)V":
		offset, valueErr := r.parameter(2)
		if valueErr != nil {
			return 0, valueErr
		}
		low, valueErr := r.parameter(3)
		if valueErr != nil {
			return 0, valueErr
		}
		high, valueErr := r.parameter(4)
		if valueErr != nil {
			return 0, valueErr
		}
		state := r.lwcComponent(instance)
		state.mode = int32(offset)
		state.minimum = int32(low)
		state.progressMax = int32(high)
		return 0, nil
	case "compare([B[B)I":
		left, valueErr := r.parameter(2)
		if valueErr != nil {
			return 0, valueErr
		}
		right, valueErr := r.parameter(3)
		if valueErr != nil {
			return 0, valueErr
		}
		leftData, valueErr := r.readJavaByteArray(left)
		if valueErr != nil {
			return 0, valueErr
		}
		rightData, valueErr := r.readJavaByteArray(right)
		if valueErr != nil {
			return 0, valueErr
		}
		if className == "org/kwis/msp/db/DataComparatorString" {
			return uint32(int32(bytes.Compare(leftData, rightData))), nil
		}
		leftValue, rightValue := int32(0), int32(0)
		if len(leftData) >= 4 {
			leftValue = int32(binary.BigEndian.Uint32(leftData))
		}
		if len(rightData) >= 4 {
			rightValue = int32(binary.BigEndian.Uint32(rightData))
		}
		switch {
		case leftValue < rightValue:
			return ^uint32(0), nil
		case leftValue > rightValue:
			return 1, nil
		}
		return 0, nil
	case "filter([B)Z":
		array, valueErr := r.parameter(2)
		if valueErr != nil {
			return 0, valueErr
		}
		data, valueErr := r.readJavaByteArray(array)
		if valueErr != nil {
			return 0, valueErr
		}
		state := r.lwcComponent(instance)
		offset := int(state.mode)
		if offset < 0 || offset+4 > len(data) {
			return 0, nil
		}
		value := int32(binary.BigEndian.Uint32(data[offset:]))
		return boolWord(
			value >= state.minimum && value <= state.progressMax,
		), nil
	default:
		return 0, nil
	}
}

func (r *Runtime) syncKTFDatabase(store *Database) error {
	if store == nil || store.Name == "" {
		return fmt.Errorf("KTF database metadata is invalid")
	}
	serviceID := r.DatabaseServices[store.Name]
	if serviceID == 0 {
		var err error
		serviceID, err = r.Services.Storage.CreateRecordStore(
			r.ServiceOwner,
			store.Name,
		)
		if err != nil {
			return err
		}
		r.DatabaseServices[store.Name] = serviceID
	}
	records := make(map[uint32][]byte, len(store.Records))
	for recordID, data := range store.Records {
		records[uint32(recordID)] = data
	}
	return r.Services.Storage.ReplaceRecords(
		r.ServiceOwner,
		serviceID,
		max(uint32(1), uint32(len(records))),
		records,
	)
}

func (r *Runtime) databaseParameter(index uint32) (*Database, error) {
	instance, err := r.parameter(index)
	if err != nil {
		return nil, err
	}
	store := r.databases[instance]
	if store == nil {
		return nil, fmt.Errorf("KTF database instance 0x%08x is unknown", instance)
	}
	return store, nil
}

func (r *Runtime) readJavaByteArray(instance uint32) ([]byte, error) {
	if instance == 0 {
		return nil, errors.New("KTF Java byte array is null")
	}
	fields, err := r.ReadU32(instance)
	if err != nil {
		return nil, err
	}
	length, err := r.ReadU32(fields + 4)
	if err != nil {
		return nil, err
	}
	return r.readJavaByteArrayRange(instance, 0, length)
}

func (r *Runtime) readJavaByteArrayRange(
	instance, offset, count uint32,
) ([]byte, error) {
	if instance == 0 {
		return nil, errors.New("KTF Java byte array is null")
	}
	fields, err := r.ReadU32(instance)
	if err != nil {
		return nil, err
	}
	length, err := r.ReadU32(fields + 4)
	if err != nil {
		return nil, err
	}
	if offset > length || count > length-offset {
		return nil, fmt.Errorf(
			"KTF Java byte array range %d..%d exceeds length %d",
			offset,
			uint64(offset)+uint64(count),
			length,
		)
	}
	data := make([]byte, count)
	if err := r.CPU.ReadMemory(fields+8+offset, data); err != nil {
		return nil, err
	}
	return data, nil
}

func (r *Runtime) writeJavaByteArrayRange(
	instance, offset uint32,
	data []byte,
) error {
	if instance == 0 {
		return errors.New("KTF Java byte array is null")
	}
	fields, err := r.ReadU32(instance)
	if err != nil {
		return err
	}
	length, err := r.ReadU32(fields + 4)
	if err != nil {
		return err
	}
	if offset > length ||
		uint64(offset)+uint64(len(data)) > uint64(length) {
		return fmt.Errorf(
			"KTF Java byte array range %d..%d exceeds length %d",
			offset,
			uint64(offset)+uint64(len(data)),
			length,
		)
	}
	return r.CPU.WriteMemory(fields+8+offset, data)
}

func (r *Runtime) newJavaByteArray(data []byte) (uint32, error) {
	if uint64(len(data)) > uint64(^uint32(0)) {
		return 0, errors.New("KTF Java byte array exceeds uint32")
	}
	instance, err := r.NewJavaArray("[B", uint32(len(data)), 1)
	if err != nil {
		return 0, err
	}
	fields, err := r.ReadU32(instance)
	if err != nil {
		return 0, err
	}
	if err := r.CPU.WriteMemory(fields+8, data); err != nil {
		return 0, err
	}
	return instance, nil
}

func (r *Runtime) initializeCard(instance, display uint32) error {
	if instance == 0 {
		return errors.New("initialize WIPI Card: instance is null")
	}
	if display == 0 {
		var err error
		display, err = r.ensureDefaultDisplay()
		if err != nil {
			return err
		}
	}
	if err := r.WriteJavaFieldWord(instance, 4, display); err != nil {
		return err
	}
	if err := r.WriteJavaFieldWord(instance, 16, r.DisplayWidth()); err != nil {
		return err
	}
	return r.WriteJavaFieldWord(instance, 20, r.DefaultCardHeight())
}

// displayWidth and displayHeight report the screen the title actually runs on.
// KTF descriptors may name a smaller handset than the default, and a Clet that
// asks for the card size has to be told the same one the framebuffer uses.
func (r *Runtime) DisplayWidth() uint32 {
	if r.frame != nil {
		return uint32(r.frame.Bounds().Dx())
	}
	return ktfDisplayWidth
}

func (r *Runtime) displayHeight() uint32 {
	if r.frame != nil {
		return uint32(r.frame.Bounds().Dy())
	}
	return ktfDisplayHeight
}

func (r *Runtime) DefaultCardHeight() uint32 {
	height := r.displayHeight()
	for _, state := range r.lwcComponents {
		if state.annunciator && state.shown && !state.transparent {
			if height > uint32(ktfAnnunciatorHeight) {
				return height - uint32(ktfAnnunciatorHeight)
			}
			return height
		}
	}
	return height
}

// CardOriginY is the y offset of a Card inside the physical framebuffer. A
// shown, opaque annunciator owns the top of the handset screen and the card is
// laid out below it, which is why DefaultCardHeight subtracts it. Painting the
// card at the top of the framebuffer anyway put every KTF title that shows an
// annunciator one annunciator too high and left a dead strip along the bottom
// edge, so the two have to be derived from each other.
func (r *Runtime) CardOriginY() uint32 {
	return r.displayHeight() - r.DefaultCardHeight()
}

func (r *Runtime) readJavaFieldWord(instance, offset uint32) (uint32, error) {
	if instance == 0 {
		return 0, errors.New("read KTF Java field: instance is null")
	}
	fields, err := r.ReadU32(instance)
	if err != nil {
		return 0, err
	}
	return r.ReadU32(fields + 4 + offset)
}

func (r *Runtime) WriteJavaFieldWord(instance, offset, value uint32) error {
	if instance == 0 {
		return errors.New("write KTF Java field: instance is null")
	}
	fields, err := r.ReadU32(instance)
	if err != nil {
		return err
	}
	return r.WriteU32(fields+4+offset, value)
}

func ktfJavaJump(argumentCount uint32) ktfHostHandler {
	return func(ctx context.Context, runtime *Runtime) (uint32, error) {
		args := make([]uint32, 3)
		for index := uint32(0); index < argumentCount; index++ {
			value, err := runtime.parameter(index)
			if err != nil {
				return 0, err
			}
			args[index] = value
		}
		procedure, err := runtime.parameter(argumentCount)
		if err != nil {
			return 0, err
		}
		if procedure == 0 {
			return 0, errors.New("Java jump target is null")
		}
		lr, _ := runtime.CPU.ReadRegister(cpu.RegisterLR)
		runtime.tracef(
			"java_jump_%d:target=0x%08x:args=%08x:lr=0x%08x",
			argumentCount,
			procedure,
			args,
			lr,
		)
		if host, ok := runtime.hostCalls[procedure&^1]; ok {
			runtime.TraceHostCall(host.name)
			value, err := host.handler(ctx, runtime)
			if err != nil {
				return 0, fmt.Errorf(
					"jump to Java host call %s at 0x%08x: %w",
					host.name,
					procedure,
					err,
				)
			}
			if strings.HasPrefix(host.name, "java.method.") {
				runtime.LastJavaReturn = value
			}
			runtime.lastJavaJump = value
			return value, nil
		}
		result, value, err := runtime.call(
			ctx,
			procedure,
			args,
			ktfBootstrapInstructionMax,
		)
		if err != nil {
			return 0, fmt.Errorf(
				"jump to 0x%08x stopped at PC 0x%08x after %d instructions: %w",
				procedure,
				result.PC,
				result.Instructions,
				err,
			)
		}
		runtime.lastJavaJump = value
		return value, nil
	}
}

// javaRegisterClassTrace returns the host-trace line for registering class,
// reusing the one cached with the class inspection when the class has not
// changed. The text is what tracef used to format on every registration.
func (r *Runtime) javaRegisterClassTrace(
	name string,
	class uint32,
	inspected JavaClass,
) string {
	entry := r.javaClassInspections[class]
	if entry != nil && entry.registerTrace != "" {
		return entry.registerTrace
	}
	methods := make([]string, 0, len(inspected.Methods))
	for _, method := range inspected.Methods {
		methods = append(
			methods,
			fmt.Sprintf(
				"%s%s@0x%08x#%04x",
				method.Name,
				method.Descriptor,
				method.Body,
				method.AccessFlags,
			),
		)
	}
	line := fmt.Sprintf(
		"java_register_class:%s:class=0x%08x:parent=0x%08x:fields=%d:methods=%v",
		name,
		class,
		inspected.Parent,
		inspected.FieldSize,
		methods,
	)
	if entry != nil {
		entry.registerTrace = line
	}
	return line
}

func ktfRegisterJavaClass(ctx context.Context, runtime *Runtime) (uint32, error) {
	class, err := runtime.parameter(0)
	if err != nil {
		return 0, err
	}
	words, err := runtime.ReadWords(class, 5)
	if err != nil {
		return 0, err
	}
	descriptor, err := runtime.ReadWords(words[2], 9)
	if err != nil {
		return 0, err
	}
	name, err := runtime.readCString(descriptor[0], 1024)
	if err != nil {
		return 0, err
	}
	runtime.rememberRegisteredJavaClass(name, class)
	inspected, inspectErr := runtime.InspectJavaClass(class)
	if inspectErr != nil {
		return 0, inspectErr
	}
	if runtime.hostJavaClass[class] {
		if err := runtime.implementBodylessPlatformMethods(inspected); err != nil {
			return 0, err
		}
		inspected, inspectErr = runtime.InspectJavaClass(class)
		if inspectErr != nil {
			return 0, inspectErr
		}
	}
	if inspected.VTable != 0 {
		runtime.javaVTableClasses[inspected.VTable] = inspected.Address
	}
	runtime.trace(runtime.javaRegisterClassTrace(name, class, inspected))
	// KTF AOT images sometimes register a class while another class
	// initializer is still wiring the objects that it references. Loading
	// must remain non-initializing in that case; leave the class pending so
	// new/getstatic/invokestatic can retry at the first active use.
	if err := runtime.ensureJavaClassInitialized(ctx, inspected); err != nil {
		runtime.tracef("java_class_initialization_deferred:%s:%v", inspected.Name, err)
	}
	return 0, nil
}

func (r *Runtime) rememberRegisteredJavaClass(name string, class uint32) {
	// The generation moves only when the class set actually changes. It keys
	// the inspection and native-signature caches, and a guest that registers a
	// class it has already registered - which a Java-heavy title does
	// thousands of times a second - was dropping every cached inspection and
	// forcing a full re-read of every method of every class it then touched.
	// Dropping them on a no-op re-registration buys nothing: each of those
	// caches already revalidates its entry against the live guest words, so a
	// class or method relinked in place is caught without the generation.
	if existing, ok := r.JavaClasses[name]; !ok || existing != class {
		r.JavaClasses[name] = class
		r.javaClassGeneration++
	}
	if strings.HasPrefix(name, "java/") ||
		strings.HasPrefix(name, "javax/") ||
		strings.HasPrefix(name, "org/kwis/") {
		// These namespaces are platform-owned. Carrier libraries frequently
		// register declarations with null bodies and expect the VM to supply
		// their concrete implementations.
		r.hostJavaClass[class] = true
	}
}

func (r *Runtime) implementBodylessPlatformMethods(class JavaClass) error {
	patched := false
	for _, method := range class.Methods {
		if method.Body != 0 || method.NativeBody != 0 {
			continue
		}
		stub := r.RegisterHostCall(
			fmt.Sprintf(
				"java.method.%s.%s%s",
				class.Name,
				method.Name,
				method.Descriptor,
			),
			HostJavaMethod(class.Name, method.Name, method.Descriptor),
		)
		offset := uint32(0)
		if method.AccessFlags&0x0100 != 0 {
			offset = 8
		}
		if err := r.WriteU32(method.Address+offset, stub); err != nil {
			return err
		}
		patched = true
		r.tracef(
			"java_platform_method:%s.%s%s@0x%08x",
			class.Name,
			method.Name,
			method.Descriptor,
			stub,
		)
	}
	if patched {
		// The patched method words are cached by inspectJavaMethod and inside
		// inspectJavaClass results; drop those caches so the stubs are seen.
		r.javaClassGeneration++
	}
	return nil
}

func ktfRegisterJavaString(_ context.Context, runtime *Runtime) (uint32, error) {
	address, err := runtime.parameter(0)
	if err != nil {
		return 0, err
	}
	length, err := runtime.parameter(1)
	if err != nil {
		return 0, err
	}
	if length == ^uint32(0) {
		var encodedLength [2]byte
		if err := runtime.CPU.ReadMemory(address, encodedLength[:]); err != nil {
			return 0, err
		}
		length = uint32(binary.LittleEndian.Uint16(encodedLength[:]))
		address += 2
	}
	if length > 1<<20 {
		return 0, fmt.Errorf("Java string length %d exceeds limit", length)
	}
	encoded := make([]byte, int(length)*2)
	if err := runtime.CPU.ReadMemory(address, encoded); err != nil {
		return 0, err
	}
	codeUnits := make([]uint16, length)
	for index := range codeUnits {
		codeUnits[index] = binary.LittleEndian.Uint16(encoded[index*2:])
	}
	value := string(utf16.Decode(codeUnits))
	instance, err := runtime.NewJavaString(value)
	if err != nil {
		return 0, err
	}
	runtime.trace("java_register_string:" + value)
	return instance, nil
}

func (r *Runtime) newJavaInstance(className string, fieldSize uint32) (uint32, error) {
	classAddress, err := r.EnsureJavaClass(className)
	if err != nil {
		return 0, err
	}
	class, err := r.InspectJavaClass(classAddress)
	if err != nil {
		return 0, err
	}
	if fieldSize > uint32(class.FieldSize) {
		class.FieldSize = uint16(fieldSize)
	}
	return r.NewJavaInstanceForClass(class)
}
