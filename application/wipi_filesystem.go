package application

import (
	"encoding/binary"
	"fmt"
	"path"
	"sort"
	"strings"

	shared "github.com/mirusu400/aram-core/runtime"
)

const wipiFilesystemCapacity = 16 << 20

func (r *wipiRuntime) dispatchFilesystem(name string) (wipiReturn, bool, error) {
	count := filesystemArgumentCount(name)
	args, err := r.args(count)
	if err != nil {
		return wipiReturn{}, true, err
	}
	arg := func(index int) uint32 {
		if index >= len(args) {
			return 0
		}
		return args[index]
	}
	switch name {
	case "MC_fsOpen":
		return r.openFile(arg(0), int32(arg(1)), int32(arg(2)))
	case "MC_fsRead":
		return r.readFile(int32(arg(0)), arg(1), int32(arg(2)))
	case "MC_fsWrite":
		return r.writeFile(int32(arg(0)), arg(1), int32(arg(2)))
	case "MC_fsClose":
		fd := int32(arg(0))
		if _, ok := r.fileHandles[fd]; !ok {
			return wipiReturn{low: ^uint32(0)}, true, nil
		}
		if serviceID := r.fileServices[fd]; serviceID != 0 {
			if err := r.services.Storage.Close(r.serviceOwner, serviceID); err != nil {
				return wipiReturn{}, true, err
			}
			delete(r.fileServices, fd)
		}
		delete(r.fileHandles, fd)
		return wipiReturn{}, true, nil
	case "MC_fsSeek":
		return r.seekFile(int32(arg(0)), int32(arg(1)), int32(arg(2)))
	case "MC_fsTell":
		handle, ok := r.fileHandles[int32(arg(0))]
		if !ok {
			return wipiReturn{low: ^uint32(0)}, true, nil
		}
		return wipiReturn{low: uint32(handle.offset)}, true, nil
	case "MC_fsFileAttribute":
		return r.fileAttribute(arg(0), arg(1), int32(arg(2)))
	case "MC_fsRemove":
		name, err := r.guestPath(arg(0), int32(arg(1)))
		if err != nil {
			return wipiReturn{low: ^uint32(0)}, true, nil
		}
		if _, ok := r.files[name]; !ok {
			return wipiReturn{low: ^uint32(0)}, true, nil
		}
		namespace, servicePath := wipiStoragePath(name)
		if err := r.services.Storage.Delete(namespace, servicePath); err != nil {
			return wipiReturn{low: ^uint32(0)}, true, nil
		}
		delete(r.files, name)
		delete(r.fileTimes, name)
		return wipiReturn{}, true, nil
	case "MC_fsRename":
		return r.renamePath(arg(0), arg(1), int32(arg(2)))
	case "MC_fsMkDir":
		name, err := r.guestPath(arg(0), int32(arg(1)))
		if err != nil {
			return wipiReturn{low: ^uint32(0)}, true, nil
		}
		_, fileExists := r.files[name]
		if r.directories[name] || fileExists {
			return wipiReturn{low: ^uint32(0)}, true, nil
		}
		namespace, servicePath := wipiStoragePath(name)
		if err := r.services.Storage.MakeDirectory(
			namespace,
			servicePath,
		); err != nil {
			return wipiReturn{low: ^uint32(0)}, true, nil
		}
		r.ensureDirectory(path.Dir(name))
		r.directories[name] = true
		r.fileTimes[name] = uint32(r.services.Clock.WallMillis() / 1000)
		return wipiReturn{}, true, nil
	case "MC_fsRmDir":
		name, err := r.guestPath(arg(0), int32(arg(1)))
		if err != nil || !r.directories[name] || r.hasDirectoryChildren(name) {
			return wipiReturn{low: ^uint32(0)}, true, nil
		}
		namespace, servicePath := wipiStoragePath(name)
		if err := r.services.Storage.RemoveDirectory(
			namespace,
			servicePath,
		); err != nil {
			return wipiReturn{low: ^uint32(0)}, true, nil
		}
		delete(r.directories, name)
		delete(r.fileTimes, name)
		return wipiReturn{}, true, nil
	case "MC_fsList":
		return r.listDirectory(arg(0), arg(1), int32(arg(2)), int32(arg(3)))
	case "MC_fsTotalSpace":
		return wipiReturn{low: wipiFilesystemCapacity}, true, nil
	case "MC_fsAvailable":
		used := r.services.Storage.Used(shared.NamespacePrivate) +
			r.services.Storage.Used(shared.NamespaceShared)
		available := uint64(wipiFilesystemCapacity)
		if used >= available {
			available = 0
		} else {
			available -= used
		}
		return wipiReturn{low: uint32(available)}, true, nil
	case "MC_fsSetMode":
		name, err := r.guestPath(arg(0), int32(arg(2)))
		_, fileExists := r.files[name]
		if err != nil || (!r.directories[name] && !fileExists) {
			return wipiReturn{low: ^uint32(0)}, true, nil
		}
		return wipiReturn{}, true, nil
	case "MC_fsGetCounts":
		name, err := r.guestPath(arg(0), int32(arg(1)))
		if err != nil || !r.directories[name] {
			return wipiReturn{low: ^uint32(0)}, true, nil
		}
		return wipiReturn{low: uint32(len(r.directoryEntries(name)))}, true, nil
	case "MC_fsIsExist":
		name, err := r.guestPath(arg(0), int32(arg(1)))
		if err != nil {
			return wipiReturn{}, true, nil
		}
		_, fileExists := r.files[name]
		if r.directories[name] || fileExists {
			return wipiReturn{low: 1}, true, nil
		}
		return wipiReturn{}, true, nil
	default:
		return wipiReturn{}, false, nil
	}
}

func filesystemArgumentCount(name string) int {
	switch name {
	case "MC_fsTotalSpace", "MC_fsAvailable":
		return 0
	case "MC_fsClose", "MC_fsTell":
		return 1
	case "MC_fsRemove", "MC_fsRmDir", "MC_fsMkDir", "MC_fsGetCounts", "MC_fsIsExist":
		return 2
	case "MC_fsOpen", "MC_fsRead", "MC_fsWrite", "MC_fsSeek",
		"MC_fsFileAttribute", "MC_fsRename", "MC_fsSetMode":
		return 3
	case "MC_fsList":
		return 4
	default:
		return 0
	}
}

func (r *wipiRuntime) guestPath(address uint32, accessMode int32) (string, error) {
	raw, err := r.readCString(address)
	if err != nil {
		return "", err
	}
	if len(raw) == 0 || len(raw) > 255 {
		return "", fmt.Errorf("invalid WIPI path length")
	}
	text := strings.ReplaceAll(string(raw), "\\", "/")
	for _, part := range strings.Split(text, "/") {
		if part == ".." {
			return "", fmt.Errorf("WIPI path traversal")
		}
	}
	root := "/private"
	switch accessMode {
	case 1:
		root = "/shared"
	case 2:
		root = "/system"
	}
	cleaned := path.Clean("/" + strings.TrimLeft(text, "/"))
	if cleaned == "/" {
		return root, nil
	}
	return root + cleaned, nil
}

func (r *wipiRuntime) openFile(nameAddress uint32, flag, accessMode int32) (wipiReturn, bool, error) {
	name, err := r.guestPath(nameAddress, accessMode)
	if err != nil {
		return wipiReturn{low: ^uint32(0)}, true, nil
	}
	writable := flag != 0
	readable := flag == 0 || flag&8 != 0
	appendMode := flag&2 != 0
	truncate := flag&4 != 0
	data, exists := r.files[name]
	if !exists && !writable {
		return wipiReturn{low: ^uint32(0)}, true, nil
	}
	mode := shared.OpenMode(0)
	if readable {
		mode |= shared.OpenRead
	}
	if writable {
		mode |= shared.OpenWrite
	}
	if !exists {
		mode |= shared.OpenCreate
	}
	if truncate {
		mode |= shared.OpenTruncate
	}
	if appendMode {
		mode |= shared.OpenAppend
	}
	namespace, servicePath := wipiStoragePath(name)
	serviceID, serviceErr := r.services.Storage.Open(
		r.serviceOwner,
		namespace,
		servicePath,
		mode,
	)
	if serviceErr != nil {
		return wipiReturn{low: ^uint32(0)}, true, nil
	}
	if !exists {
		r.ensureDirectory(path.Dir(name))
		r.files[name] = nil
		r.fileTimes[name] = uint32(r.services.Clock.WallMillis() / 1000)
		data = nil
	}
	if truncate {
		r.files[name] = nil
		data = nil
	}
	fd := r.nextFile
	r.nextFile++
	offset := 0
	if appendMode {
		offset = len(data)
	}
	r.fileHandles[fd] = wipiFileHandle{
		path:     name,
		offset:   offset,
		readable: readable,
		writable: writable,
	}
	r.fileServices[fd] = serviceID
	return wipiReturn{low: uint32(fd)}, true, nil
}

func (r *wipiRuntime) readFile(fd int32, destination uint32, length int32) (wipiReturn, bool, error) {
	handle, ok := r.fileHandles[fd]
	if !ok || !handle.readable || length < 0 {
		return wipiReturn{low: ^uint32(0)}, true, nil
	}
	data := r.files[handle.path]
	if handle.offset > len(data) {
		handle.offset = len(data)
	}
	count := min(int(length), len(data)-handle.offset)
	serviceData, serviceErr := r.services.Storage.Read(
		r.serviceOwner,
		r.fileServices[fd],
		uint64(count),
	)
	if serviceErr != nil {
		return wipiReturn{low: ^uint32(0)}, true, nil
	}
	if len(serviceData) != count {
		return wipiReturn{}, true, fmt.Errorf(
			"shared WIPI file %q read %d bytes, want %d",
			handle.path,
			len(serviceData),
			count,
		)
	}
	if err := r.cpu.WriteMemory(destination, serviceData); err != nil {
		return wipiReturn{}, true, err
	}
	handle.offset += count
	r.fileHandles[fd] = handle
	return wipiReturn{low: uint32(count)}, true, nil
}

func (r *wipiRuntime) writeFile(fd int32, source uint32, length int32) (wipiReturn, bool, error) {
	handle, ok := r.fileHandles[fd]
	if !ok || !handle.writable || length < 0 {
		return wipiReturn{low: ^uint32(0)}, true, nil
	}
	if length > wipiFilesystemCapacity ||
		r.filesystemUsed()-len(r.files[handle.path])+max(len(r.files[handle.path]), handle.offset+int(length)) >
			wipiFilesystemCapacity {
		return wipiReturn{low: ^uint32(0)}, true, nil
	}
	data := make([]byte, length)
	if err := r.cpu.ReadMemory(source, data); err != nil {
		return wipiReturn{}, true, err
	}
	if _, serviceErr := r.services.Storage.Write(
		r.serviceOwner,
		r.fileServices[fd],
		data,
	); serviceErr != nil {
		return wipiReturn{low: ^uint32(0)}, true, nil
	}
	file := r.files[handle.path]
	end := handle.offset + len(data)
	if end > len(file) {
		file = append(file, make([]byte, end-len(file))...)
	}
	copy(file[handle.offset:end], data)
	r.files[handle.path] = file
	handle.offset = end
	r.fileHandles[fd] = handle
	return wipiReturn{low: uint32(len(data))}, true, nil
}

func (r *wipiRuntime) seekFile(fd, offset, whence int32) (wipiReturn, bool, error) {
	handle, ok := r.fileHandles[fd]
	if !ok {
		return wipiReturn{low: ^uint32(0)}, true, nil
	}
	base := 0
	switch whence {
	case 0:
	case 1:
		base = handle.offset
	case 2:
		base = len(r.files[handle.path])
	default:
		return wipiReturn{low: ^uint32(0)}, true, nil
	}
	target := int64(base) + int64(offset)
	if target < 0 || target > wipiFilesystemCapacity {
		return wipiReturn{low: ^uint32(0)}, true, nil
	}
	serviceWhence := shared.SeekStart
	switch whence {
	case 1:
		serviceWhence = shared.SeekCurrent
	case 2:
		serviceWhence = shared.SeekEnd
	}
	serviceTarget, serviceErr := r.services.Storage.Seek(
		r.serviceOwner,
		r.fileServices[fd],
		int64(offset),
		serviceWhence,
	)
	if serviceErr != nil || serviceTarget != uint64(target) {
		return wipiReturn{low: ^uint32(0)}, true, nil
	}
	handle.offset = int(target)
	r.fileHandles[fd] = handle
	return wipiReturn{low: uint32(handle.offset)}, true, nil
}

func (r *wipiRuntime) fileAttribute(nameAddress, output uint32, accessMode int32) (wipiReturn, bool, error) {
	name, err := r.guestPath(nameAddress, accessMode)
	if err != nil {
		return wipiReturn{low: ^uint32(0)}, true, nil
	}
	var attribute, size uint32
	if r.directories[name] {
		attribute = 1
	} else if data, ok := r.files[name]; ok {
		size = uint32(len(data))
	} else {
		return wipiReturn{low: ^uint32(0)}, true, nil
	}
	var encoded [12]byte
	binary.LittleEndian.PutUint32(encoded[0:], attribute)
	binary.LittleEndian.PutUint32(encoded[4:], r.fileTimes[name])
	binary.LittleEndian.PutUint32(encoded[8:], size)
	return wipiReturn{}, true, r.cpu.WriteMemory(output, encoded[:])
}

func (r *wipiRuntime) renamePath(oldAddress, newAddress uint32, accessMode int32) (wipiReturn, bool, error) {
	oldName, err := r.guestPath(oldAddress, accessMode)
	if err != nil {
		return wipiReturn{low: ^uint32(0)}, true, nil
	}
	newName, err := r.guestPath(newAddress, accessMode)
	_, fileExists := r.files[newName]
	if err != nil || r.directories[newName] || fileExists {
		return wipiReturn{low: ^uint32(0)}, true, nil
	}
	if data, ok := r.files[oldName]; ok {
		namespace, oldPath := wipiStoragePath(oldName)
		newNamespace, newPath := wipiStoragePath(newName)
		if namespace != newNamespace {
			return wipiReturn{low: ^uint32(0)}, true, nil
		}
		if err := r.services.Storage.Rename(
			namespace,
			oldPath,
			newPath,
		); err != nil {
			return wipiReturn{low: ^uint32(0)}, true, nil
		}
		r.ensureDirectory(path.Dir(newName))
		r.files[newName] = data
		delete(r.files, oldName)
		r.fileTimes[newName] = r.fileTimes[oldName]
		delete(r.fileTimes, oldName)
		for fd, handle := range r.fileHandles {
			if handle.path == oldName {
				handle.path = newName
				r.fileHandles[fd] = handle
			}
		}
		return wipiReturn{}, true, nil
	}
	if !r.directories[oldName] {
		return wipiReturn{low: ^uint32(0)}, true, nil
	}
	namespace, oldPath := wipiStoragePath(oldName)
	newNamespace, newPath := wipiStoragePath(newName)
	if namespace != newNamespace {
		return wipiReturn{low: ^uint32(0)}, true, nil
	}
	if err := r.services.Storage.RenameDirectory(
		namespace,
		oldPath,
		newPath,
	); err != nil {
		return wipiReturn{low: ^uint32(0)}, true, nil
	}
	r.ensureDirectory(path.Dir(newName))
	directories := make([]string, 0)
	for directory := range r.directories {
		if directory == oldName || strings.HasPrefix(directory, oldName+"/") {
			directories = append(directories, directory)
		}
	}
	sort.Strings(directories)
	for _, directory := range directories {
		replacement := newName + strings.TrimPrefix(directory, oldName)
		r.directories[replacement] = true
		delete(r.directories, directory)
		if modified, ok := r.fileTimes[directory]; ok {
			r.fileTimes[replacement] = modified
			delete(r.fileTimes, directory)
		}
	}
	files := make([]string, 0)
	for name := range r.files {
		if strings.HasPrefix(name, oldName+"/") {
			files = append(files, name)
		}
	}
	sort.Strings(files)
	for _, name := range files {
		replacement := newName + strings.TrimPrefix(name, oldName)
		r.files[replacement] = r.files[name]
		delete(r.files, name)
		if modified, ok := r.fileTimes[name]; ok {
			r.fileTimes[replacement] = modified
			delete(r.fileTimes, name)
		}
	}
	for descriptor, handle := range r.fileHandles {
		if strings.HasPrefix(handle.path, oldName+"/") {
			handle.path = newName + strings.TrimPrefix(handle.path, oldName)
			r.fileHandles[descriptor] = handle
		}
	}
	return wipiReturn{}, true, nil
}

func wipiStoragePath(name string) (shared.Namespace, string) {
	for _, mapping := range []struct {
		prefix    string
		namespace shared.Namespace
	}{
		{"/private", shared.NamespacePrivate},
		{"/shared", shared.NamespaceShared},
		{"/system", shared.NamespacePackage},
	} {
		if name == mapping.prefix {
			return mapping.namespace, "/"
		}
		if strings.HasPrefix(name, mapping.prefix+"/") {
			return mapping.namespace, strings.TrimPrefix(name, mapping.prefix)
		}
	}
	return shared.NamespacePrivate, name
}

func (r *wipiRuntime) listDirectory(nameAddress, output uint32, size, accessMode int32) (wipiReturn, bool, error) {
	name, err := r.guestPath(nameAddress, accessMode)
	if err != nil || !r.directories[name] || size < 2 {
		return wipiReturn{low: ^uint32(0)}, true, nil
	}
	entries := r.directoryEntries(name)
	var encoded []byte
	for _, entry := range entries {
		encoded = append(encoded, []byte(entry)...)
		encoded = append(encoded, 0)
	}
	encoded = append(encoded, 0)
	if len(encoded) > int(size) {
		return wipiReturn{low: ^uint32(0)}, true, nil
	}
	return wipiReturn{}, true, r.cpu.WriteMemory(output, encoded)
}

func (r *wipiRuntime) directoryEntries(directory string) []string {
	entries := make(map[string]struct{})
	prefix := strings.TrimSuffix(directory, "/") + "/"
	for name := range r.directories {
		if strings.HasPrefix(name, prefix) {
			remainder := strings.TrimPrefix(name, prefix)
			if remainder != "" && !strings.Contains(remainder, "/") {
				entries[remainder] = struct{}{}
			}
		}
	}
	for name := range r.files {
		if strings.HasPrefix(name, prefix) {
			remainder := strings.TrimPrefix(name, prefix)
			if remainder != "" && !strings.Contains(remainder, "/") {
				entries[remainder] = struct{}{}
			}
		}
	}
	result := make([]string, 0, len(entries))
	for entry := range entries {
		result = append(result, entry)
	}
	sort.Strings(result)
	return result
}

func (r *wipiRuntime) ensureDirectory(directory string) {
	for directory != "." && directory != "/" && directory != "" {
		r.directories[directory] = true
		directory = path.Dir(directory)
	}
}

func (r *wipiRuntime) hasDirectoryChildren(directory string) bool {
	return len(r.directoryEntries(directory)) != 0
}

func (r *wipiRuntime) filesystemUsed() int {
	total := 0
	for _, data := range r.files {
		total += len(data)
	}
	return total
}
