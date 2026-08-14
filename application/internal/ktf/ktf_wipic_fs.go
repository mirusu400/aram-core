package ktf

import (
	"context"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
	"time"

	shared "github.com/mirusu400/aram-core/runtime"
)

const (
	ktfWIPICFileReadOnly  = uint32(1)
	ktfWIPICFileWriteOnly = uint32(2)
	ktfWIPICFileTruncate  = uint32(4)
	ktfWIPICFileReadWrite = uint32(8)

	ktfWIPICError          = ^uint32(0)
	ktfWIPICErrorBadSeek   = ^uint32(3)
	ktfWIPICErrorInvalid   = ^uint32(8)
	ktfWIPICErrorNoEntry   = ^uint32(11)
	ktfWIPICErrorShortBuf  = ^uint32(17)
	ktfWIPICErrorEOF       = ^uint32(22)
	ktfWIPICErrorBadHandle = ^uint32(100)
)

func ktfWIPICFileOpen(
	_ context.Context,
	runtime *Runtime,
) (uint32, error) {
	nameAddress, err := runtime.parameter(0)
	if err != nil {
		return 0, err
	}
	flag, err := runtime.parameter(1)
	if err != nil {
		return 0, err
	}
	name, err := runtime.readCString(nameAddress, 1024)
	if err != nil {
		return 0, err
	}
	name = normalizeKTFFileName(name)
	openMode := shared.OpenMode(0)
	switch flag {
	case ktfWIPICFileReadOnly:
		openMode = shared.OpenRead
		if _, err := runtime.Services.Storage.Stat(
			shared.NamespacePrivate,
			name,
		); err != nil {
			runtime.tracef(
				"wipic_file_open_missing:%s:flag=%d",
				name,
				flag,
			)
			return ktfWIPICErrorNoEntry, nil
		}
	case ktfWIPICFileWriteOnly:
		openMode = shared.OpenWrite | shared.OpenCreate | shared.OpenAppend
	case ktfWIPICFileTruncate:
		openMode = shared.OpenWrite | shared.OpenCreate | shared.OpenTruncate
	case ktfWIPICFileReadWrite:
		openMode = shared.OpenRead | shared.OpenWrite | shared.OpenCreate
	default:
		return ktfWIPICErrorInvalid, nil
	}
	serviceID, serviceErr := runtime.Services.Storage.Open(
		runtime.ServiceOwner,
		shared.NamespacePrivate,
		name,
		openMode,
	)
	if serviceErr != nil {
		return ktfWIPICError, nil
	}
	data, serviceErr := runtime.Services.Storage.ReadFile(
		shared.NamespacePrivate,
		name,
	)
	if serviceErr != nil {
		_ = runtime.Services.Storage.Close(runtime.ServiceOwner, serviceID)
		return 0, serviceErr
	}
	runtime.FileData[name] = data
	handle := runtime.nextWIPICFile
	for handle == 0 || runtime.wipicFiles[handle] != nil {
		handle++
	}
	runtime.nextWIPICFile = handle + 1
	file := &ktfFile{
		namespace: shared.NamespacePrivate,
		name:      name,
		mode:      flag,
	}
	if flag == ktfWIPICFileWriteOnly {
		file.position = uint32(len(data))
	}
	runtime.wipicFiles[handle] = file
	runtime.wipicFileServices[handle] = serviceID
	runtime.tracef(
		"wipic_file_open:%s:flag=%d:fd=%d",
		name,
		flag,
		handle,
	)
	return handle, nil
}

func ktfWIPICFileRead(
	_ context.Context,
	runtime *Runtime,
) (uint32, error) {
	handle, err := runtime.parameter(0)
	if err != nil {
		return 0, err
	}
	output, err := runtime.parameter(1)
	if err != nil {
		return 0, err
	}
	count, err := runtime.parameter(2)
	if err != nil {
		return 0, err
	}
	file := runtime.wipicFiles[handle]
	if file == nil || file.closed {
		return ktfWIPICErrorBadHandle, nil
	}
	if output == 0 || file.mode != ktfWIPICFileReadOnly &&
		file.mode != ktfWIPICFileReadWrite {
		return ktfWIPICErrorInvalid, nil
	}
	if count == 0 {
		return 0, nil
	}
	serviceID := runtime.wipicFileServices[handle]
	data, serviceErr := runtime.Services.Storage.Read(
		runtime.ServiceOwner,
		serviceID,
		uint64(count),
	)
	if serviceErr != nil {
		return ktfWIPICError, nil
	}
	if len(data) == 0 {
		return ktfWIPICErrorEOF, nil
	}
	if err := runtime.CPU.WriteMemory(output, data); err != nil {
		return 0, err
	}
	file.position += uint32(len(data))
	return uint32(len(data)), nil
}

func ktfWIPICFileWrite(
	_ context.Context,
	runtime *Runtime,
) (uint32, error) {
	handle, err := runtime.parameter(0)
	if err != nil {
		return 0, err
	}
	input, err := runtime.parameter(1)
	if err != nil {
		return 0, err
	}
	count, err := runtime.parameter(2)
	if err != nil {
		return 0, err
	}
	file := runtime.wipicFiles[handle]
	if file == nil || file.closed {
		return ktfWIPICErrorBadHandle, nil
	}
	if input == 0 || file.mode != ktfWIPICFileWriteOnly &&
		file.mode != ktfWIPICFileTruncate &&
		file.mode != ktfWIPICFileReadWrite {
		return ktfWIPICErrorInvalid, nil
	}
	const maxFileSize = uint32(8 * 1024 * 1024)
	if count > maxFileSize || file.position > maxFileSize-count {
		return ktfWIPICError, nil
	}
	inputData := make([]byte, count)
	if err := runtime.CPU.ReadMemory(input, inputData); err != nil {
		return 0, err
	}
	serviceID := runtime.wipicFileServices[handle]
	written, serviceErr := runtime.Services.Storage.Write(
		runtime.ServiceOwner,
		serviceID,
		inputData,
	)
	if serviceErr != nil || written != len(inputData) {
		return ktfWIPICError, nil
	}
	stored, serviceErr := runtime.Services.Storage.ReadFile(
		shared.NamespacePrivate,
		file.name,
	)
	if serviceErr != nil {
		return 0, serviceErr
	}
	runtime.FileData[file.name] = stored
	file.position += uint32(written)
	return count, nil
}

func ktfWIPICFileClose(
	_ context.Context,
	runtime *Runtime,
) (uint32, error) {
	handle, err := runtime.parameter(0)
	if err != nil {
		return 0, err
	}
	file := runtime.wipicFiles[handle]
	if file == nil || file.closed {
		return ktfWIPICErrorBadHandle, nil
	}
	if serviceID := runtime.wipicFileServices[handle]; serviceID != 0 {
		if err := runtime.Services.Storage.Close(
			runtime.ServiceOwner,
			serviceID,
		); err != nil {
			return ktfWIPICError, nil
		}
		delete(runtime.wipicFileServices, handle)
	}
	file.closed = true
	delete(runtime.wipicFiles, handle)
	return 0, nil
}

func ktfWIPICFileSeek(
	_ context.Context,
	runtime *Runtime,
) (uint32, error) {
	handle, err := runtime.parameter(0)
	if err != nil {
		return 0, err
	}
	rawPosition, err := runtime.parameter(1)
	if err != nil {
		return 0, err
	}
	origin, err := runtime.parameter(2)
	if err != nil {
		return 0, err
	}
	file := runtime.wipicFiles[handle]
	if file == nil || file.closed {
		return ktfWIPICErrorBadHandle, nil
	}
	position := int64(int32(rawPosition))
	whence := shared.SeekStart
	switch origin {
	case 0:
	case 1:
		whence = shared.SeekCurrent
	case 2:
		whence = shared.SeekEnd
	default:
		return ktfWIPICErrorInvalid, nil
	}
	servicePosition, serviceErr := runtime.Services.Storage.Seek(
		runtime.ServiceOwner,
		runtime.wipicFileServices[handle],
		position,
		whence,
	)
	if serviceErr != nil || servicePosition > 8*1024*1024 {
		return ktfWIPICErrorBadSeek, nil
	}
	file.position = uint32(servicePosition)
	return file.position, nil
}

func ktfWIPICFileAttribute(
	_ context.Context,
	runtime *Runtime,
) (uint32, error) {
	name, err := runtime.wipicFileNameParameter(0)
	if err != nil {
		return 0, err
	}
	output, err := runtime.parameter(1)
	if err != nil {
		return 0, err
	}
	info, serviceErr := runtime.Services.Storage.Stat(
		shared.NamespacePrivate,
		name,
	)
	if serviceErr != nil {
		return ktfWIPICErrorNoEntry, nil
	}
	if output == 0 {
		return ktfWIPICErrorInvalid, nil
	}
	if err := runtime.writeWords(
		output,
		[]uint32{0, uint32(info.Modified / time.Second), uint32(info.Size)},
	); err != nil {
		return 0, err
	}
	return 0, nil
}

func ktfWIPICFileRemove(
	_ context.Context,
	runtime *Runtime,
) (uint32, error) {
	name, err := runtime.wipicFileNameParameter(0)
	if err != nil {
		return 0, err
	}
	if _, ok := runtime.FileData[name]; !ok {
		return ktfWIPICErrorNoEntry, nil
	}
	if err := runtime.Services.Storage.Delete(
		shared.NamespacePrivate,
		name,
	); err != nil {
		return ktfWIPICError, nil
	}
	delete(runtime.FileData, name)
	return 0, nil
}

func ktfWIPICFileRename(
	_ context.Context,
	runtime *Runtime,
) (uint32, error) {
	oldName, err := runtime.wipicFileNameParameter(0)
	if err != nil {
		return 0, err
	}
	newName, err := runtime.wipicFileNameParameter(1)
	if err != nil {
		return 0, err
	}
	data, ok := runtime.FileData[oldName]
	if !ok {
		return ktfWIPICErrorNoEntry, nil
	}
	if _, exists := runtime.FileData[newName]; exists {
		return ^uint32(4), nil
	}
	if err := runtime.Services.Storage.Rename(
		shared.NamespacePrivate,
		oldName,
		newName,
	); err != nil {
		return ktfWIPICError, nil
	}
	runtime.FileData[newName] = data
	delete(runtime.FileData, oldName)
	for _, file := range runtime.files {
		if file != nil && file.name == oldName {
			file.name = newName
		}
	}
	for _, file := range runtime.wipicFiles {
		if file != nil && file.name == oldName {
			file.name = newName
		}
	}
	return 0, nil
}

func ktfWIPICFileMakeDirectory(
	_ context.Context,
	runtime *Runtime,
) (uint32, error) {
	name, err := runtime.wipicFileNameParameter(0)
	if err != nil {
		return 0, err
	}
	if err := runtime.Services.Storage.MakeDirectory(
		shared.NamespacePrivate,
		name,
	); err != nil {
		return ktfWIPICError, nil
	}
	return 0, nil
}

func ktfWIPICFileRemoveDirectory(
	_ context.Context,
	runtime *Runtime,
) (uint32, error) {
	name, err := runtime.wipicFileNameParameter(0)
	if err != nil {
		return 0, err
	}
	if err := runtime.Services.Storage.RemoveDirectory(
		shared.NamespacePrivate,
		name,
	); err != nil {
		return ktfWIPICError, nil
	}
	return 0, nil
}

func ktfWIPICFileList(
	_ context.Context,
	runtime *Runtime,
) (uint32, error) {
	name, err := runtime.wipicFileNameParameter(0)
	if err != nil {
		return 0, err
	}
	output, err := runtime.parameter(1)
	if err != nil {
		return 0, err
	}
	size, err := runtime.parameter(2)
	if err != nil {
		return 0, err
	}
	if output == 0 || size < 2 {
		return ktfWIPICErrorShortBuf, nil
	}
	entries, serviceErr := runtime.Services.Storage.List(
		shared.NamespacePrivate,
		name,
	)
	if serviceErr != nil {
		return ktfWIPICErrorNoEntry, nil
	}
	encoded := make([]byte, 0)
	for _, entry := range entries {
		encoded = append(encoded, entry...)
		encoded = append(encoded, 0)
	}
	encoded = append(encoded, 0)
	if uint32(len(encoded)) > size {
		return ktfWIPICErrorShortBuf, nil
	}
	if err := runtime.CPU.WriteMemory(output, encoded); err != nil {
		return 0, err
	}
	return 0, nil
}

func ktfWIPICFileTotalSpace(
	context.Context,
	*Runtime,
) (uint32, error) {
	return 16 * 1024 * 1024, nil
}

func ktfWIPICFileAvailable(
	_ context.Context,
	runtime *Runtime,
) (uint32, error) {
	used := runtime.Services.Storage.Used(shared.NamespacePrivate)
	const total = uint64(16 * 1024 * 1024)
	if used >= total {
		return 0, nil
	}
	return uint32(total - used), nil
}

func ktfWIPICFileSetMode(
	context.Context,
	*Runtime,
) (uint32, error) {
	return 0, nil
}

func ktfWIPICFileGetCounts(
	_ context.Context,
	runtime *Runtime,
) (uint32, error) {
	name, err := runtime.wipicFileNameParameter(0)
	if err != nil {
		return 0, err
	}
	entries, err := runtime.Services.Storage.List(
		shared.NamespacePrivate,
		name,
	)
	if err != nil {
		return ktfWIPICErrorNoEntry, nil
	}
	return uint32(len(entries)), nil
}

func ktfWIPICFileIsExist(
	_ context.Context,
	runtime *Runtime,
) (uint32, error) {
	name, err := runtime.wipicFileNameParameter(0)
	if err != nil {
		return 0, err
	}
	if _, err := runtime.Services.Storage.Stat(
		shared.NamespacePrivate,
		name,
	); err != nil && !runtime.Services.Storage.DirectoryExists(
		shared.NamespacePrivate,
		name,
	) {
		return ktfWIPICErrorNoEntry, nil
	}
	return 0, nil
}

func ktfWIPICFileTell(
	_ context.Context,
	runtime *Runtime,
) (uint32, error) {
	handle, err := runtime.parameter(0)
	if err != nil {
		return 0, err
	}
	file := runtime.wipicFiles[handle]
	if file == nil || file.closed {
		return ktfWIPICErrorBadHandle, nil
	}
	return file.position, nil
}

func (r *Runtime) wipicFileNameParameter(index uint32) (string, error) {
	address, err := r.parameter(index)
	if err != nil {
		return "", err
	}
	name, err := r.readCString(address, 1024)
	if err != nil {
		return "", err
	}
	return normalizeKTFFileName(name), nil
}
