package wipi

import (
	"encoding/binary"
	"fmt"
	"path"
	"sort"
	"strings"

	"github.com/mirusu400/aram-core/application/internal/guest"
	shared "github.com/mirusu400/aram-core/runtime"
)

const wipiFilesystemCapacity = 16 << 20

func (r *Runtime) dispatchFilesystem(name string) (guest.WIPIReturn, bool, error) {
	count := filesystemArgumentCount(name)
	args, err := r.args(count)
	if err != nil {
		return guest.WIPIReturn{}, true, err
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
			return guest.WIPIReturn{Low: ^uint32(0)}, true, nil
		}
		if serviceID := r.fileServices[fd]; serviceID != 0 {
			if err := r.Services.Storage.Close(r.ServiceOwner, serviceID); err != nil {
				return guest.WIPIReturn{}, true, err
			}
			delete(r.fileServices, fd)
		}
		delete(r.fileHandles, fd)
		return guest.WIPIReturn{}, true, nil
	case "MC_fsSeek":
		return r.seekFile(int32(arg(0)), int32(arg(1)), int32(arg(2)))
	case "MC_fsTell":
		handle, ok := r.fileHandles[int32(arg(0))]
		if !ok {
			return guest.WIPIReturn{Low: ^uint32(0)}, true, nil
		}
		return guest.WIPIReturn{Low: uint32(handle.offset)}, true, nil
	case "MC_fsFileAttribute":
		return r.fileAttribute(arg(0), arg(1), int32(arg(2)))
	case "MC_fsRemove":
		name, err := r.guestPath(arg(0), int32(arg(1)))
		if err != nil {
			return guest.WIPIReturn{Low: ^uint32(0)}, true, nil
		}
		if _, ok := r.Files[name]; !ok {
			return guest.WIPIReturn{Low: ^uint32(0)}, true, nil
		}
		namespace, servicePath := wipiStoragePath(name)
		if err := r.Services.Storage.Delete(namespace, servicePath); err != nil {
			return guest.WIPIReturn{Low: ^uint32(0)}, true, nil
		}
		delete(r.Files, name)
		delete(r.FileTimes, name)
		return guest.WIPIReturn{}, true, nil
	case "MC_fsRename":
		return r.renamePath(arg(0), arg(1), int32(arg(2)))
	case "MC_fsMkDir":
		name, err := r.guestPath(arg(0), int32(arg(1)))
		if err != nil {
			return guest.WIPIReturn{Low: ^uint32(0)}, true, nil
		}
		_, fileExists := r.Files[name]
		if r.Directories[name] || fileExists {
			return guest.WIPIReturn{Low: ^uint32(0)}, true, nil
		}
		namespace, servicePath := wipiStoragePath(name)
		if err := r.Services.Storage.MakeDirectory(
			namespace,
			servicePath,
		); err != nil {
			return guest.WIPIReturn{Low: ^uint32(0)}, true, nil
		}
		r.ensureDirectory(path.Dir(name))
		r.Directories[name] = true
		r.FileTimes[name] = uint32(r.Services.Clock.WallMillis() / 1000)
		return guest.WIPIReturn{}, true, nil
	case "MC_fsRmDir":
		name, err := r.guestPath(arg(0), int32(arg(1)))
		if err != nil || !r.Directories[name] || r.hasDirectoryChildren(name) {
			return guest.WIPIReturn{Low: ^uint32(0)}, true, nil
		}
		namespace, servicePath := wipiStoragePath(name)
		if err := r.Services.Storage.RemoveDirectory(
			namespace,
			servicePath,
		); err != nil {
			return guest.WIPIReturn{Low: ^uint32(0)}, true, nil
		}
		delete(r.Directories, name)
		delete(r.FileTimes, name)
		return guest.WIPIReturn{}, true, nil
	case "MC_fsList":
		return r.listDirectory(arg(0), arg(1), int32(arg(2)), int32(arg(3)))
	case "MC_fsTotalSpace":
		return guest.WIPIReturn{Low: wipiFilesystemCapacity}, true, nil
	case "MC_fsAvailable":
		used := r.Services.Storage.Used(shared.NamespacePrivate) +
			r.Services.Storage.Used(shared.NamespaceShared)
		available := uint64(wipiFilesystemCapacity)
		if used >= available {
			available = 0
		} else {
			available -= used
		}
		return guest.WIPIReturn{Low: uint32(available)}, true, nil
	case "MC_fsSetMode":
		name, err := r.guestPath(arg(0), int32(arg(2)))
		_, fileExists := r.Files[name]
		if err != nil || (!r.Directories[name] && !fileExists) {
			return guest.WIPIReturn{Low: ^uint32(0)}, true, nil
		}
		return guest.WIPIReturn{}, true, nil
	case "MC_fsGetCounts":
		name, err := r.guestPath(arg(0), int32(arg(1)))
		if err != nil || !r.Directories[name] {
			return guest.WIPIReturn{Low: ^uint32(0)}, true, nil
		}
		return guest.WIPIReturn{Low: uint32(len(r.directoryEntries(name)))}, true, nil
	case "MC_fsIsExist":
		name, err := r.guestPath(arg(0), int32(arg(1)))
		if err != nil {
			return guest.WIPIReturn{}, true, nil
		}
		_, fileExists := r.Files[name]
		if r.Directories[name] || fileExists {
			return guest.WIPIReturn{Low: 1}, true, nil
		}
		return guest.WIPIReturn{}, true, nil
	default:
		return guest.WIPIReturn{}, false, nil
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

func (r *Runtime) guestPath(address uint32, accessMode int32) (string, error) {
	raw, err := r.ReadCString(address)
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
	r.ensureSharedPackageFile(root + cleaned)
	return root + cleaned, nil
}

func (r *Runtime) openFile(nameAddress uint32, flag, accessMode int32) (guest.WIPIReturn, bool, error) {
	name, err := r.guestPath(nameAddress, accessMode)
	if err != nil {
		return guest.WIPIReturn{Low: ^uint32(0)}, true, nil
	}
	// flag bit 0 (MC_FILE_READ = 1) opens for reading only; it must not create a
	// missing file. A game's existence probe opens with flag 1 and expects the
	// open to FAIL for a missing file so it can stop enumerating (미니게임천국4 walks
	// saved "char%02d.dat" via MC_fsOpen(name, 1, 1) until one is absent, and
	// 던파귀검사편 keys off the same miss for its optional assets). Treating flag 1 as
	// writable created the missing file and returned a valid handle, so the
	// enumeration never terminated and the game read empty data it then derefs.
	writable := flag != 0 && flag != 1
	readable := flag == 0 || flag == 1 || flag&8 != 0
	appendMode := flag&2 != 0
	truncate := flag&4 != 0
	data, exists := r.Files[name]
	if !exists && !writable {
		// M_E_NOENT (-12): the LGT WIPI file layer's "no such entry" code, which a
		// guest compares against exactly (result + 0xC == 0), not the generic -1
		// handle-error sentinel.
		return guest.WIPIReturn{Low: 0xFFFFFFF4}, true, nil
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
	serviceID, serviceErr := r.Services.Storage.Open(
		r.ServiceOwner,
		namespace,
		servicePath,
		mode,
	)
	if serviceErr != nil {
		return guest.WIPIReturn{Low: ^uint32(0)}, true, nil
	}
	if !exists {
		r.ensureDirectory(path.Dir(name))
		r.Files[name] = nil
		r.FileTimes[name] = uint32(r.Services.Clock.WallMillis() / 1000)
		data = nil
	}
	if truncate {
		r.Files[name] = nil
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
	return guest.WIPIReturn{Low: uint32(fd)}, true, nil
}

func (r *Runtime) readFile(fd int32, destination uint32, length int32) (guest.WIPIReturn, bool, error) {
	handle, ok := r.fileHandles[fd]
	if !ok || !handle.readable || length < 0 {
		return guest.WIPIReturn{Low: ^uint32(0)}, true, nil
	}
	data := r.Files[handle.path]
	if handle.offset > len(data) {
		handle.offset = len(data)
	}
	count := min(int(length), len(data)-handle.offset)
	serviceData, serviceErr := r.Services.Storage.Read(
		r.ServiceOwner,
		r.fileServices[fd],
		uint64(count),
	)
	if serviceErr != nil {
		return guest.WIPIReturn{Low: ^uint32(0)}, true, nil
	}
	if len(serviceData) != count {
		return guest.WIPIReturn{}, true, fmt.Errorf(
			"shared WIPI file %q read %d bytes, want %d",
			handle.path,
			len(serviceData),
			count,
		)
	}
	if err := r.CPU.WriteMemory(destination, serviceData); err != nil {
		return guest.WIPIReturn{}, true, err
	}
	handle.offset += count
	r.fileHandles[fd] = handle
	return guest.WIPIReturn{Low: uint32(count)}, true, nil
}

func (r *Runtime) writeFile(fd int32, source uint32, length int32) (guest.WIPIReturn, bool, error) {
	handle, ok := r.fileHandles[fd]
	if !ok || !handle.writable || length < 0 {
		return guest.WIPIReturn{Low: ^uint32(0)}, true, nil
	}
	if length > wipiFilesystemCapacity ||
		r.filesystemUsed()-len(r.Files[handle.path])+max(len(r.Files[handle.path]), handle.offset+int(length)) >
			wipiFilesystemCapacity {
		return guest.WIPIReturn{Low: ^uint32(0)}, true, nil
	}
	data := make([]byte, length)
	if err := r.CPU.ReadMemory(source, data); err != nil {
		return guest.WIPIReturn{}, true, err
	}
	if _, serviceErr := r.Services.Storage.Write(
		r.ServiceOwner,
		r.fileServices[fd],
		data,
	); serviceErr != nil {
		return guest.WIPIReturn{Low: ^uint32(0)}, true, nil
	}
	file := r.Files[handle.path]
	end := handle.offset + len(data)
	if end > len(file) {
		file = append(file, make([]byte, end-len(file))...)
	}
	copy(file[handle.offset:end], data)
	r.Files[handle.path] = file
	handle.offset = end
	r.fileHandles[fd] = handle
	return guest.WIPIReturn{Low: uint32(len(data))}, true, nil
}

func (r *Runtime) seekFile(fd, offset, whence int32) (guest.WIPIReturn, bool, error) {
	handle, ok := r.fileHandles[fd]
	if !ok {
		return guest.WIPIReturn{Low: ^uint32(0)}, true, nil
	}
	base := 0
	switch whence {
	case 0:
	case 1:
		base = handle.offset
	case 2:
		base = len(r.Files[handle.path])
	default:
		return guest.WIPIReturn{Low: ^uint32(0)}, true, nil
	}
	target := int64(base) + int64(offset)
	if target < 0 || target > wipiFilesystemCapacity {
		return guest.WIPIReturn{Low: ^uint32(0)}, true, nil
	}
	serviceWhence := shared.SeekStart
	switch whence {
	case 1:
		serviceWhence = shared.SeekCurrent
	case 2:
		serviceWhence = shared.SeekEnd
	}
	serviceTarget, serviceErr := r.Services.Storage.Seek(
		r.ServiceOwner,
		r.fileServices[fd],
		int64(offset),
		serviceWhence,
	)
	if serviceErr != nil || serviceTarget != uint64(target) {
		return guest.WIPIReturn{Low: ^uint32(0)}, true, nil
	}
	handle.offset = int(target)
	r.fileHandles[fd] = handle
	return guest.WIPIReturn{Low: uint32(handle.offset)}, true, nil
}

func (r *Runtime) fileAttribute(nameAddress, output uint32, accessMode int32) (guest.WIPIReturn, bool, error) {
	name, err := r.guestPath(nameAddress, accessMode)
	if err != nil {
		return guest.WIPIReturn{Low: ^uint32(0)}, true, nil
	}
	var attribute, size uint32
	if r.Directories[name] {
		attribute = 1
	} else if data, ok := r.Files[name]; ok {
		size = uint32(len(data))
	} else {
		return guest.WIPIReturn{Low: ^uint32(0)}, true, nil
	}
	var encoded [12]byte
	binary.LittleEndian.PutUint32(encoded[0:], attribute)
	binary.LittleEndian.PutUint32(encoded[4:], r.FileTimes[name])
	binary.LittleEndian.PutUint32(encoded[8:], size)
	return guest.WIPIReturn{}, true, r.CPU.WriteMemory(output, encoded[:])
}

func (r *Runtime) renamePath(oldAddress, newAddress uint32, accessMode int32) (guest.WIPIReturn, bool, error) {
	oldName, err := r.guestPath(oldAddress, accessMode)
	if err != nil {
		return guest.WIPIReturn{Low: ^uint32(0)}, true, nil
	}
	newName, err := r.guestPath(newAddress, accessMode)
	_, fileExists := r.Files[newName]
	if err != nil || r.Directories[newName] || fileExists {
		return guest.WIPIReturn{Low: ^uint32(0)}, true, nil
	}
	if data, ok := r.Files[oldName]; ok {
		namespace, oldPath := wipiStoragePath(oldName)
		newNamespace, newPath := wipiStoragePath(newName)
		if namespace != newNamespace {
			return guest.WIPIReturn{Low: ^uint32(0)}, true, nil
		}
		if err := r.Services.Storage.Rename(
			namespace,
			oldPath,
			newPath,
		); err != nil {
			return guest.WIPIReturn{Low: ^uint32(0)}, true, nil
		}
		r.ensureDirectory(path.Dir(newName))
		r.Files[newName] = data
		delete(r.Files, oldName)
		r.FileTimes[newName] = r.FileTimes[oldName]
		delete(r.FileTimes, oldName)
		for fd, handle := range r.fileHandles {
			if handle.path == oldName {
				handle.path = newName
				r.fileHandles[fd] = handle
			}
		}
		return guest.WIPIReturn{}, true, nil
	}
	if !r.Directories[oldName] {
		return guest.WIPIReturn{Low: ^uint32(0)}, true, nil
	}
	namespace, oldPath := wipiStoragePath(oldName)
	newNamespace, newPath := wipiStoragePath(newName)
	if namespace != newNamespace {
		return guest.WIPIReturn{Low: ^uint32(0)}, true, nil
	}
	if err := r.Services.Storage.RenameDirectory(
		namespace,
		oldPath,
		newPath,
	); err != nil {
		return guest.WIPIReturn{Low: ^uint32(0)}, true, nil
	}
	r.ensureDirectory(path.Dir(newName))
	directories := make([]string, 0)
	for directory := range r.Directories {
		if directory == oldName || strings.HasPrefix(directory, oldName+"/") {
			directories = append(directories, directory)
		}
	}
	sort.Strings(directories)
	for _, directory := range directories {
		replacement := newName + strings.TrimPrefix(directory, oldName)
		r.Directories[replacement] = true
		delete(r.Directories, directory)
		if modified, ok := r.FileTimes[directory]; ok {
			r.FileTimes[replacement] = modified
			delete(r.FileTimes, directory)
		}
	}
	files := make([]string, 0)
	for name := range r.Files {
		if strings.HasPrefix(name, oldName+"/") {
			files = append(files, name)
		}
	}
	sort.Strings(files)
	for _, name := range files {
		replacement := newName + strings.TrimPrefix(name, oldName)
		r.Files[replacement] = r.Files[name]
		delete(r.Files, name)
		if modified, ok := r.FileTimes[name]; ok {
			r.FileTimes[replacement] = modified
			delete(r.FileTimes, name)
		}
	}
	for descriptor, handle := range r.fileHandles {
		if strings.HasPrefix(handle.path, oldName+"/") {
			handle.path = newName + strings.TrimPrefix(handle.path, oldName)
			r.fileHandles[descriptor] = handle
		}
	}
	return guest.WIPIReturn{}, true, nil
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

func (r *Runtime) listDirectory(nameAddress, output uint32, size, accessMode int32) (guest.WIPIReturn, bool, error) {
	name, err := r.guestPath(nameAddress, accessMode)
	if err != nil || !r.Directories[name] || size < 2 {
		return guest.WIPIReturn{Low: ^uint32(0)}, true, nil
	}
	entries := r.directoryEntries(name)
	var encoded []byte
	for _, entry := range entries {
		encoded = append(encoded, []byte(entry)...)
		encoded = append(encoded, 0)
	}
	encoded = append(encoded, 0)
	if len(encoded) > int(size) {
		return guest.WIPIReturn{Low: ^uint32(0)}, true, nil
	}
	return guest.WIPIReturn{}, true, r.CPU.WriteMemory(output, encoded)
}

func (r *Runtime) directoryEntries(directory string) []string {
	entries := make(map[string]struct{})
	prefix := strings.TrimSuffix(directory, "/") + "/"
	for name := range r.Directories {
		if strings.HasPrefix(name, prefix) {
			remainder := strings.TrimPrefix(name, prefix)
			if remainder != "" && !strings.Contains(remainder, "/") {
				entries[remainder] = struct{}{}
			}
		}
	}
	for name := range r.Files {
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

func (r *Runtime) ensureDirectory(directory string) {
	for directory != "." && directory != "/" && directory != "" {
		r.Directories[directory] = true
		directory = path.Dir(directory)
	}
}

func (r *Runtime) hasDirectoryChildren(directory string) bool {
	return len(r.directoryEntries(directory)) != 0
}

func (r *Runtime) filesystemUsed() int {
	total := 0
	for _, data := range r.Files {
		total += len(data)
	}
	return total
}
