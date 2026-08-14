package ktf

import (
	"errors"
	"fmt"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
	"math"
	"path"
	"slices"
	"strings"
	"time"

	shared "github.com/mirusu400/aram-core/runtime"
)

func (r *Runtime) handleFileMethod(
	name, descriptor string,
) (uint32, error) {
	switch name + descriptor {
	case "<init>(Ljava/lang/String;I)V",
		"<init>(Ljava/lang/String;II)V",
		"<init>(Ljava/lang/String;)V":
		instance, err := r.parameter(1)
		if err != nil {
			return 0, err
		}
		nameAddress, err := r.parameter(2)
		if err != nil {
			return 0, err
		}
		mode := uint32(1)
		namespace := shared.NamespacePrivate
		if descriptor != "(Ljava/lang/String;)V" {
			mode, err = r.parameter(3)
			if err != nil {
				return 0, err
			}
		}
		if descriptor == "(Ljava/lang/String;II)V" {
			flag, flagErr := r.parameter(4)
			if flagErr != nil {
				return 0, flagErr
			}
			namespace, err = r.ktfStorageNamespace(flag)
			if err != nil {
				return 0, err
			}
		}
		filename := normalizeKTFFileName(r.javaStringValue(nameAddress))
		file := &ktfFile{namespace: namespace, name: filename, mode: mode}
		legacyData, legacyExists := r.FileData[filename]
		if namespace == shared.NamespacePrivate && legacyExists {
			if _, statErr := r.Services.Storage.Stat(
				namespace,
				filename,
			); statErr != nil {
				if writeErr := r.Services.Storage.WriteFile(
					namespace,
					filename,
					legacyData,
				); writeErr != nil {
					return 0, writeErr
				}
			}
		}
		_, statErr := r.Services.Storage.Stat(namespace, filename)
		exists := statErr == nil
		openMode := shared.OpenMode(0)
		switch mode {
		case ktfFileReadOnly:
			openMode = shared.OpenRead
			if !exists {
				if r.LenientMissingRead && namespace == shared.NamespacePrivate {
					// No guest exception unwinding is available (Raptor host):
					// materialize the missing private file empty so the title
					// reads EOF and falls back to defaults, matching a device
					// that catches the first-run IOException.
					if writeErr := r.Services.Storage.WriteFile(
						namespace,
						filename,
						nil,
					); writeErr != nil {
						return 0, r.raiseHostJavaException("java/io/IOException")
					}
					exists = true
					r.tracef("java_file_open_missing_lenient:%s", filename)
				} else {
					r.tracef("java_file_open_missing:%s", filename)
					return 0, r.raiseHostJavaException("java/io/IOException")
				}
			}
		case ktfFileWrite:
			openMode = shared.OpenRead | shared.OpenWrite |
				shared.OpenCreate | shared.OpenAppend
		case ktfFileWriteTrunc:
			openMode = shared.OpenRead | shared.OpenWrite |
				shared.OpenCreate | shared.OpenTruncate
		case ktfFileReadWrite:
			openMode = shared.OpenRead | shared.OpenWrite | shared.OpenCreate
		default:
			r.tracef(
				"java_file_open_invalid_mode:%s:mode=%d",
				filename,
				mode,
			)
			return 0, r.raiseHostJavaException("java/io/IOException")
		}
		serviceID, serviceErr := r.Services.Storage.Open(
			r.ServiceOwner,
			namespace,
			filename,
			openMode,
		)
		if serviceErr != nil {
			return 0, r.raiseHostJavaException("java/io/IOException")
		}
		data, serviceErr := r.Services.Storage.ReadFile(
			namespace,
			filename,
		)
		if serviceErr != nil {
			_ = r.Services.Storage.Close(r.ServiceOwner, serviceID)
			return 0, serviceErr
		}
		if namespace == shared.NamespacePrivate {
			r.FileData[filename] = data
		}
		if mode == ktfFileWrite {
			file.position = uint32(len(data))
		}
		r.files[instance] = file
		r.fileServices[instance] = serviceID
		r.tracef("java_file_open:%s:mode=%d", filename, mode)
		return 0, nil
	case "close()V":
		instance, err := r.parameter(1)
		if err != nil {
			return 0, err
		}
		if file := r.files[instance]; file != nil {
			if serviceID := r.fileServices[instance]; serviceID != 0 {
				if err := r.Services.Storage.Close(
					r.ServiceOwner,
					serviceID,
				); err != nil {
					return 0, err
				}
				delete(r.fileServices, instance)
			}
			file.closed = true
		}
		return 0, nil
	case "openInputStream()Ljava/io/InputStream;",
		"openDataInputStream()Ljava/io/DataInputStream;":
		fileInstance, err := r.parameter(1)
		if err != nil {
			return 0, err
		}
		className := "java/io/InputStream"
		if strings.HasPrefix(name, "openData") {
			className = "java/io/DataInputStream"
		}
		instance, err := r.NewHostJavaObject(className)
		if err != nil {
			return 0, err
		}
		var data []byte
		if file := r.files[fileInstance]; file != nil {
			data, err = r.Services.Storage.ReadFile(
				ktfFileNamespace(file),
				file.name,
			)
			if err != nil {
				return 0, err
			}
		}
		r.inputStreams[instance] = &ktfInputStream{data: data}
		return instance, nil
	case "openOutputStream()Ljava/io/OutputStream;",
		"openDataOutputStream()Ljava/io/DataOutputStream;":
		fileInstance, err := r.parameter(1)
		if err != nil {
			return 0, err
		}
		className := "java/io/OutputStream"
		if strings.HasPrefix(name, "openData") {
			className = "java/io/DataOutputStream"
		}
		instance, err := r.NewHostJavaObject(className)
		if err != nil {
			return 0, err
		}
		r.outputStreams[instance] = nil
		r.fileStreamTargets[instance] = fileInstance
		return instance, nil
	case "sizeOf()I":
		instance, err := r.parameter(1)
		if err != nil {
			return 0, err
		}
		if file := r.files[instance]; file != nil {
			info, err := r.Services.Storage.Stat(
				ktfFileNamespace(file),
				file.name,
			)
			if err != nil {
				return 0, err
			}
			return uint32(min(info.Size, uint64(^uint32(0)))), nil
		}
		return 0, nil
	case "seek(I)V":
		instance, err := r.parameter(1)
		if err != nil {
			return 0, err
		}
		position, err := r.parameter(2)
		if err != nil {
			return 0, err
		}
		if file := r.files[instance]; file != nil {
			serviceID, serviceErr := r.ensureKTFFileService(instance)
			if serviceErr != nil {
				return 0, serviceErr
			}
			if _, serviceErr := r.Services.Storage.Seek(
				r.ServiceOwner,
				serviceID,
				int64(position),
				shared.SeekStart,
			); serviceErr != nil {
				return 0, serviceErr
			}
			file.position = position
		}
		return 0, nil
	case "tell()I":
		instance, err := r.parameter(1)
		if err != nil {
			return 0, err
		}
		if file := r.files[instance]; file != nil {
			return file.position, nil
		}
		return 0, nil
	case "read()I":
		instance, err := r.parameter(1)
		if err != nil {
			return 0, err
		}
		data, err := r.readKTFFileBytes(instance, 1)
		if err != nil {
			return 0, err
		}
		if len(data) == 0 {
			return ^uint32(0), nil
		}
		return uint32(data[0]), nil
	case "read([B)I", "read([BII)I":
		instance, err := r.parameter(1)
		if err != nil {
			return 0, err
		}
		array, err := r.parameter(2)
		if err != nil {
			return 0, err
		}
		offset := uint32(0)
		count, err := r.javaArrayLength(array)
		if err != nil {
			return 0, err
		}
		if descriptor == "([BII)I" {
			offset, err = r.parameter(3)
			if err != nil {
				return 0, err
			}
			count, err = r.parameter(4)
			if err != nil {
				return 0, err
			}
		}
		return r.readKTFFile(instance, array, offset, count)
	case "write(I)I", "write(I)V":
		instance, err := r.parameter(1)
		if err != nil {
			return 0, err
		}
		value, err := r.parameter(2)
		if err != nil {
			return 0, err
		}
		return r.writeKTFFile(instance, []byte{byte(value)})
	case "write([B)I", "write([BII)I",
		"write([B)V", "write([BII)V":
		instance, err := r.parameter(1)
		if err != nil {
			return 0, err
		}
		array, err := r.parameter(2)
		if err != nil {
			return 0, err
		}
		offset := uint32(0)
		count, err := r.javaArrayLength(array)
		if err != nil {
			return 0, err
		}
		if strings.Contains(descriptor, "BII") {
			offset, err = r.parameter(3)
			if err != nil {
				return 0, err
			}
			count, err = r.parameter(4)
			if err != nil {
				return 0, err
			}
		}
		data, err := r.readJavaByteArrayRange(array, offset, count)
		if err != nil {
			return 0, err
		}
		return r.writeKTFFile(instance, data)
	default:
		r.recordUnimplementedJava("org/kwis/msp/io/File", name, descriptor)
		return 0, nil
	}
}

func normalizeKTFFileName(filename string) string {
	filename = strings.ReplaceAll(strings.TrimSpace(filename), `\`, "/")
	if filename == "" {
		return "/"
	}
	if !strings.HasPrefix(filename, "/") {
		filename = "/" + filename
	}
	return path.Clean(filename)
}

func (r *Runtime) readKTFFile(
	instance, array, offset, count uint32,
) (uint32, error) {
	length, err := r.javaArrayLength(array)
	if err != nil {
		return 0, err
	}
	if offset > length || count > length-offset {
		return 0, fmt.Errorf(
			"KTF File.read range [%d,%d) exceeds byte array length %d",
			offset,
			uint64(offset)+uint64(count),
			length,
		)
	}
	if count == 0 {
		return 0, nil
	}
	data, err := r.readKTFFileBytes(instance, count)
	if err != nil {
		return 0, err
	}
	if len(data) == 0 {
		return ^uint32(0), nil
	}
	count = uint32(len(data))
	fields, err := r.ReadU32(array)
	if err != nil {
		return 0, err
	}
	if err := r.CPU.WriteMemory(
		fields+8+offset,
		data,
	); err != nil {
		return 0, err
	}
	return count, nil
}

func (r *Runtime) readKTFFileBytes(
	instance, count uint32,
) ([]byte, error) {
	file := r.files[instance]
	if file == nil || count == 0 {
		return nil, nil
	}
	serviceID, err := r.ensureKTFFileService(instance)
	if err != nil {
		return nil, err
	}
	data, err := r.Services.Storage.Read(
		r.ServiceOwner,
		serviceID,
		uint64(count),
	)
	if err != nil {
		return nil, err
	}
	file.position += uint32(len(data))
	r.tracef("java_file_read:%s:%d", file.name, len(data))
	return data, nil
}

func (r *Runtime) writeKTFFile(
	instance uint32,
	data []byte,
) (uint32, error) {
	file := r.files[instance]
	if file == nil {
		file = &ktfFile{
			namespace: shared.NamespacePrivate,
			name:      fmt.Sprintf("/unnamed-%08x", instance),
		}
		r.files[instance] = file
	}
	end := uint64(file.position) + uint64(len(data))
	if end > uint64(^uint32(0)) {
		return 0, errors.New("KTF File.write range overflows uint32")
	}
	serviceID, err := r.ensureKTFFileService(instance)
	if err != nil {
		return 0, err
	}
	written, err := r.Services.Storage.Write(
		r.ServiceOwner,
		serviceID,
		data,
	)
	if err != nil {
		return 0, err
	}
	if written != len(data) {
		return 0, fmt.Errorf(
			"shared KTF file wrote %d bytes, want %d",
			written,
			len(data),
		)
	}
	stored, err := r.Services.Storage.ReadFile(
		ktfFileNamespace(file),
		file.name,
	)
	if err != nil {
		return 0, err
	}
	if ktfFileNamespace(file) == shared.NamespacePrivate {
		r.FileData[file.name] = stored
	}
	file.position = uint32(end)
	r.tracef("java_file_write:%s:%d", file.name, len(data))
	return uint32(len(data)), nil
}

func (r *Runtime) ensureKTFFileService(
	instance uint32,
) (shared.ServiceID, error) {
	if serviceID := r.fileServices[instance]; serviceID != 0 {
		return serviceID, nil
	}
	file := r.files[instance]
	if file == nil {
		return 0, fmt.Errorf("KTF file object 0x%08x is missing", instance)
	}
	namespace := ktfFileNamespace(file)
	if _, err := r.Services.Storage.Stat(
		namespace,
		file.name,
	); err != nil {
		if data, ok := r.FileData[file.name]; namespace == shared.NamespacePrivate && ok {
			if err := r.Services.Storage.WriteFile(
				namespace,
				file.name,
				data,
			); err != nil {
				return 0, err
			}
		}
	}
	mode := shared.OpenRead
	if file.mode != ktfFileReadOnly {
		mode |= shared.OpenWrite | shared.OpenCreate
	}
	serviceID, err := r.Services.Storage.Open(
		r.ServiceOwner,
		namespace,
		file.name,
		mode,
	)
	if err != nil {
		return 0, err
	}
	if _, err := r.Services.Storage.Seek(
		r.ServiceOwner,
		serviceID,
		int64(file.position),
		shared.SeekStart,
	); err != nil {
		_ = r.Services.Storage.Close(r.ServiceOwner, serviceID)
		return 0, err
	}
	r.fileServices[instance] = serviceID
	return serviceID, nil
}

func ktfFileNamespace(file *ktfFile) shared.Namespace {
	if file != nil && file.namespace.Valid() {
		return file.namespace
	}
	return shared.NamespacePrivate
}

func (r *Runtime) ktfStorageNamespace(
	flag uint32,
) (shared.Namespace, error) {
	switch flag {
	case 1:
		return shared.NamespacePrivate, nil
	case 2:
		return shared.NamespaceShared, nil
	case 3:
		return shared.NamespacePackage, nil
	default:
		return 0, r.raiseHostJavaException("java/lang/SecurityException")
	}
}

func (r *Runtime) handleFileSystemMethod(
	name, descriptor string,
) (uint32, error) {
	switch name + descriptor {
	case "<init>()V":
		return 0, nil
	case "getMaxFilenameLength()I":
		return uint32(min(
			r.Services.Config.Limits.Storage.MaxPathBytes,
			uint32(math.MaxInt32),
		)), nil
	case "list(Ljava/lang/String;)Ljava/util/Vector;",
		"list(Ljava/lang/String;I)Ljava/util/Vector;":
		nameAddress, err := r.parameter(1)
		if err != nil {
			return 0, err
		}
		namespace, err := r.ktfFileSystemNamespace(descriptor, 2)
		if err != nil {
			return 0, err
		}
		directory := normalizeKTFFileName(r.javaStringValue(nameAddress))
		entries, err := r.Services.Storage.List(namespace, directory)
		if err != nil {
			return 0, r.raiseHostJavaException("java/io/IOException")
		}
		values := make([]uint32, 0, len(entries))
		for _, entry := range entries {
			value, valueErr := r.NewJavaString(entry)
			if valueErr != nil {
				return 0, valueErr
			}
			values = append(values, value)
		}
		vector, err := r.NewHostJavaObject("java/util/Vector")
		if err != nil {
			return 0, err
		}
		r.Vectors[vector] = values
		return vector, nil
	case "exists(Ljava/lang/String;)Z",
		"exists(Ljava/lang/String;I)Z":
		nameAddress, err := r.parameter(1)
		if err != nil {
			return 0, err
		}
		namespace, err := r.ktfFileSystemNamespace(descriptor, 2)
		if err != nil {
			return 0, err
		}
		filename := normalizeKTFFileName(r.javaStringValue(nameAddress))
		if _, err := r.Services.Storage.Stat(
			namespace,
			filename,
		); err == nil {
			return 1, nil
		}
		return boolWord(r.Services.Storage.DirectoryExists(namespace, filename)), nil
	case "isFile(Ljava/lang/String;)Z",
		"isFile(Ljava/lang/String;I)Z":
		nameAddress, err := r.parameter(1)
		if err != nil {
			return 0, err
		}
		namespace, err := r.ktfFileSystemNamespace(descriptor, 2)
		if err != nil {
			return 0, err
		}
		filename := normalizeKTFFileName(r.javaStringValue(nameAddress))
		if _, err := r.Services.Storage.Stat(namespace, filename); err == nil {
			return 1, nil
		}
		return 0, nil
	case "isDirectory(Ljava/lang/String;)Z",
		"isDirectory(Ljava/lang/String;I)Z":
		nameAddress, err := r.parameter(1)
		if err != nil {
			return 0, err
		}
		namespace, err := r.ktfFileSystemNamespace(descriptor, 2)
		if err != nil {
			return 0, err
		}
		filename := normalizeKTFFileName(r.javaStringValue(nameAddress))
		return boolWord(
			r.Services.Storage.DirectoryExists(namespace, filename),
		), nil
	case "mkdir(Ljava/lang/String;)V",
		"mkdir(Ljava/lang/String;I)V":
		nameAddress, err := r.parameter(1)
		if err != nil {
			return 0, err
		}
		namespace, err := r.ktfFileSystemNamespace(descriptor, 2)
		if err != nil {
			return 0, err
		}
		directory := normalizeKTFFileName(r.javaStringValue(nameAddress))
		if err := r.Services.Storage.MakeDirectory(namespace, directory); err != nil {
			return 0, r.raiseHostJavaException("java/io/IOException")
		}
		r.tracef("java_directory_make:%s:%s", namespace, directory)
		return 0, nil
	case "rmdir(Ljava/lang/String;)V",
		"rmdir(Ljava/lang/String;I)V":
		nameAddress, err := r.parameter(1)
		if err != nil {
			return 0, err
		}
		namespace, err := r.ktfFileSystemNamespace(descriptor, 2)
		if err != nil {
			return 0, err
		}
		directory := normalizeKTFFileName(r.javaStringValue(nameAddress))
		if err := r.Services.Storage.RemoveDirectory(namespace, directory); err != nil {
			return 0, r.raiseHostJavaException("java/io/IOException")
		}
		r.tracef("java_directory_remove:%s:%s", namespace, directory)
		return 0, nil
	case "remove(Ljava/lang/String;)V",
		"remove(Ljava/lang/String;I)V":
		nameAddress, err := r.parameter(1)
		if err != nil {
			return 0, err
		}
		namespace, err := r.ktfFileSystemNamespace(descriptor, 2)
		if err != nil {
			return 0, err
		}
		filename := normalizeKTFFileName(r.javaStringValue(nameAddress))
		// KTF applications commonly retain the File object after closing its
		// derived stream and then remove the path through FileSystem. The
		// handset invalidates that File object as part of the removal. Close
		// the adapter-owned handles first so shared Storage can keep its strict
		// no-delete-while-open invariant.
		var instances []uint32
		for instance, file := range r.files {
			if file != nil && ktfFileNamespace(file) == namespace &&
				file.name == filename && !file.closed {
				instances = append(instances, instance)
			}
		}
		slices.Sort(instances)
		for _, instance := range instances {
			if serviceID := r.fileServices[instance]; serviceID != 0 {
				if err := r.Services.Storage.Close(
					r.ServiceOwner,
					serviceID,
				); err != nil {
					return 0, err
				}
				delete(r.fileServices, instance)
			}
			r.files[instance].closed = true
		}
		if err := r.Services.Storage.Delete(
			namespace,
			filename,
		); err != nil && !errors.Is(err, shared.ErrNotFound) {
			return 0, err
		}
		if namespace == shared.NamespacePrivate {
			delete(r.FileData, filename)
		}
		r.tracef(
			"java_file_remove:%s:closed=%d",
			filename,
			len(instances),
		)
		return 0, nil
	case "toCString(Ljava/lang/String;)[B":
		nameAddress, err := r.parameter(1)
		if err != nil {
			return 0, err
		}
		value := append([]byte(r.javaStringValue(nameAddress)), 0)
		return r.newJavaByteArray(value)
	case "getCreationTime(Ljava/lang/String;)I",
		"getCreationTime(Ljava/lang/String;I)I":
		nameAddress, err := r.parameter(1)
		if err != nil {
			return 0, err
		}
		namespace, err := r.ktfFileSystemNamespace(descriptor, 2)
		if err != nil {
			return 0, err
		}
		filename := normalizeKTFFileName(r.javaStringValue(nameAddress))
		info, err := r.Services.Storage.Stat(namespace, filename)
		if err != nil {
			return ^uint32(0), nil
		}
		seconds := info.Modified / time.Second
		return uint32(min(seconds, time.Duration(math.MaxInt32))), nil
	case "rename(Ljava/lang/String;Ljava/lang/String;)V",
		"rename(Ljava/lang/String;Ljava/lang/String;I)V":
		oldAddress, err := r.parameter(1)
		if err != nil {
			return 0, err
		}
		newAddress, err := r.parameter(2)
		if err != nil {
			return 0, err
		}
		namespace, err := r.ktfFileSystemNamespace(descriptor, 3)
		if err != nil {
			return 0, err
		}
		oldName := normalizeKTFFileName(r.javaStringValue(oldAddress))
		newName := normalizeKTFFileName(r.javaStringValue(newAddress))
		if err := r.Services.Storage.Rename(namespace, oldName, newName); err != nil {
			return 0, r.raiseHostJavaException("java/io/IOException")
		}
		for _, file := range r.files {
			if file != nil && ktfFileNamespace(file) == namespace &&
				file.name == oldName {
				file.name = newName
			}
		}
		if namespace == shared.NamespacePrivate {
			if data, ok := r.FileData[oldName]; ok {
				delete(r.FileData, oldName)
				r.FileData[newName] = data
			}
		}
		r.tracef("java_file_rename:%s:%s->%s", namespace, oldName, newName)
		return 0, nil
	case "getFreeSpace()J":
		return r.javaLongResult(r.ktfFreeStorageBytes()), nil
	case "available()I":
		// KTF titles gate startup on the int form of the free-space query and
		// abort with a "not enough space" card when it reports zero.
		return uint32(min(r.ktfFreeStorageBytes(), math.MaxInt32)), nil
	case "totalSpace()I":
		return uint32(min(r.ktfFreeStorageBytes()*2, math.MaxInt32)), nil
	default:
		r.recordUnimplementedJava("org/kwis/msp/io/FileSystem", name, descriptor)
		return 0, nil
	}
}

func (r *Runtime) ktfFileSystemNamespace(
	descriptor string,
	flagParameter uint32,
) (shared.Namespace, error) {
	if !strings.Contains(descriptor, ";I") {
		return shared.NamespacePrivate, nil
	}
	flag, err := r.parameter(flagParameter)
	if err != nil {
		return 0, err
	}
	return r.ktfStorageNamespace(flag)
}

func (r *Runtime) ktfFreeStorageBytes() uint64 {
	limit := r.Services.Config.Limits.Storage.MaxStorageBytes
	used := r.Services.Storage.Used(shared.NamespacePrivate)
	if used >= limit {
		return 0
	}
	return limit - used
}

// recordUnimplementedJava keeps modeled-class methods that fall through their
// handler visible in diagnostics. Without it a silent zero looks like a real
// answer, which is how a missing free-space query reads as an empty disk.
func (r *Runtime) recordUnimplementedJava(className, name, descriptor string) {
	signature := className + "." + name + descriptor
	r.UnimplementedJava[signature]++
	r.LastUnimplementedJava = signature
	r.tracef("java_unimplemented:%s", signature)
}

func (r *Runtime) NewHostJavaObject(className string) (uint32, error) {
	classAddress, err := r.EnsureJavaClass(className)
	if err != nil {
		return 0, err
	}
	class, err := r.InspectJavaClass(classAddress)
	if err != nil {
		return 0, err
	}
	return r.NewJavaInstanceForClass(class)
}

func (r *Runtime) javaArrayLength(instance uint32) (uint32, error) {
	if instance == 0 {
		return 0, errors.New("KTF Java array is null")
	}
	fields, err := r.ReadU32(instance)
	if err != nil {
		return 0, err
	}
	return r.ReadU32(fields + 4)
}

func (r *Runtime) javaLongResult(value uint64) uint32 {
	r.JavaReturnHigh = uint32(value >> 32)
	return uint32(value)
}
