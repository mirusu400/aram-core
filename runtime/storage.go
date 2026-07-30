package runtime

import (
	"fmt"
	"math"
	"path"
	"sort"
	"strings"
	"time"
)

type Namespace uint8

const (
	NamespacePackage Namespace = iota + 1
	NamespacePrivate
	NamespaceShared
	NamespaceTemporary
)

func (n Namespace) Valid() bool {
	return n >= NamespacePackage && n <= NamespaceTemporary
}

func (n Namespace) String() string {
	switch n {
	case NamespacePackage:
		return "package"
	case NamespacePrivate:
		return "private"
	case NamespaceShared:
		return "shared"
	case NamespaceTemporary:
		return "temporary"
	default:
		return "invalid"
	}
}

type OpenMode uint8

const (
	OpenRead OpenMode = 1 << iota
	OpenWrite
	OpenCreate
	OpenTruncate
	OpenAppend
)

func (m OpenMode) Valid() bool {
	const known = OpenRead | OpenWrite | OpenCreate | OpenTruncate | OpenAppend
	return m != 0 && m&^known == 0 &&
		(m&(OpenRead|OpenWrite) != 0) &&
		(m&(OpenCreate|OpenTruncate|OpenAppend) == 0 || m&OpenWrite != 0)
}

type SeekWhence uint8

const (
	SeekStart SeekWhence = iota
	SeekCurrent
	SeekEnd
)

type StorageLimits struct {
	MaxFiles        uint32
	MaxOpenHandles  uint32
	MaxPathBytes    uint32
	MaxFileBytes    uint64
	MaxStorageBytes uint64
	MaxRecordStores uint32
	MaxRecords      uint32
	MaxRecordBytes  uint64
}

func DefaultStorageLimits() StorageLimits {
	return StorageLimits{
		MaxFiles:        4096,
		MaxOpenHandles:  1024,
		MaxPathBytes:    1024,
		MaxFileBytes:    64 << 20,
		MaxStorageBytes: 256 << 20,
		MaxRecordStores: 1024,
		MaxRecords:      65536,
		MaxRecordBytes:  64 << 20,
	}
}

func (l StorageLimits) Validate() error {
	if l.MaxFiles == 0 || l.MaxOpenHandles == 0 || l.MaxPathBytes == 0 ||
		l.MaxFileBytes == 0 || l.MaxStorageBytes == 0 ||
		l.MaxRecordStores == 0 || l.MaxRecords == 0 || l.MaxRecordBytes == 0 ||
		l.MaxFileBytes > l.MaxStorageBytes {
		return fmt.Errorf("%w: invalid storage limits", ErrInvalidArgument)
	}
	return nil
}

type FileInfo struct {
	Namespace Namespace
	Path      string
	Size      uint64
	Modified  time.Duration
	ReadOnly  bool
}

type storageFile struct {
	namespace Namespace
	path      string
	data      []byte
	modified  time.Duration
	readOnly  bool
}

type storageDirectory struct {
	namespace Namespace
	path      string
	modified  time.Duration
	readOnly  bool
}

type openFile struct {
	id        ServiceID
	owner     OwnerID
	namespace Namespace
	path      string
	position  uint64
	mode      OpenMode
}

type recordStore struct {
	id      ServiceID
	owner   OwnerID
	name    string
	nextID  uint32
	records map[uint32][]byte
}

type FileState struct {
	Namespace Namespace
	Path      string
	Data      []byte
	Modified  int64
	ReadOnly  bool
}

type DirectoryState struct {
	Namespace Namespace
	Path      string
	Modified  int64
	ReadOnly  bool
}

type OpenFileState struct {
	ID        ServiceID
	Owner     OwnerID
	Namespace Namespace
	Path      string
	Position  uint64
	Mode      OpenMode
}

type RecordState struct {
	ID   uint32
	Data []byte
}

type RecordStoreState struct {
	ID      ServiceID
	Owner   OwnerID
	Name    string
	NextID  uint32
	Records []RecordState
}

type StorageState struct {
	Limits       StorageLimits
	Directories  []DirectoryState
	Files        []FileState
	OpenFiles    []OpenFileState
	RecordStores []RecordStoreState
}

// Storage provides package resources, mutable namespaces, open handles, and a
// common record engine for WIPI databases and Java RMS.
type Storage struct {
	registry     *Registry
	clock        *Clock
	limits       StorageLimits
	directories  map[string]*storageDirectory
	files        map[string]*storageFile
	openFiles    map[ServiceID]*openFile
	recordStores map[ServiceID]*recordStore
	recordNames  map[string]ServiceID
}

func NewStorage(registry *Registry, clock *Clock, limits StorageLimits) (*Storage, error) {
	if registry == nil {
		registry = NewRegistry(0)
	}
	if clock == nil {
		var err error
		clock, err = NewClock(0, 0, "")
		if err != nil {
			return nil, err
		}
	}
	if limits == (StorageLimits{}) {
		limits = DefaultStorageLimits()
	}
	if err := limits.Validate(); err != nil {
		return nil, err
	}
	return &Storage{
		registry:     registry,
		clock:        clock,
		limits:       limits,
		directories:  make(map[string]*storageDirectory),
		files:        make(map[string]*storageFile),
		openFiles:    make(map[ServiceID]*openFile),
		recordStores: make(map[ServiceID]*recordStore),
		recordNames:  make(map[string]ServiceID),
	}, nil
}

// NormalizePath produces a canonical absolute service path. Parent segments,
// NULs, backslashes, drive prefixes, and host path syntax are rejected before
// cleaning, so a guest path cannot escape into the host filesystem.
func (s *Storage) NormalizePath(value string) (string, error) {
	if value == "" || uint32(len(value)) > s.limits.MaxPathBytes ||
		strings.IndexByte(value, 0) >= 0 ||
		strings.Contains(value, `\`) ||
		strings.Contains(value, ":") {
		return "", fmt.Errorf("%w: invalid service path", ErrInvalidArgument)
	}
	segments := strings.Split(value, "/")
	for _, segment := range segments {
		if segment == ".." {
			return "", fmt.Errorf("%w: parent path segment is forbidden", ErrInvalidArgument)
		}
	}
	cleaned := path.Clean("/" + strings.TrimPrefix(value, "/"))
	if cleaned == "/" || cleaned == "." || len(cleaned) > int(s.limits.MaxPathBytes) {
		return "", fmt.Errorf("%w: invalid service path", ErrInvalidArgument)
	}
	return cleaned, nil
}

func (s *Storage) MountPackage(resources map[string][]byte) error {
	names := make([]string, 0, len(resources))
	for name := range resources {
		names = append(names, name)
	}
	sort.Strings(names)
	type pendingResource struct {
		path string
		data []byte
	}
	pending := make([]pendingResource, 0, len(names))
	total := s.totalUsed()
	for _, name := range names {
		normalized, err := s.NormalizePath(name)
		if err != nil {
			return fmt.Errorf("mount package resource %q: %w", name, err)
		}
		key := storageKey(NamespacePackage, normalized)
		if s.files[key] != nil || s.directories[key] != nil {
			return fmt.Errorf("%w: duplicate package resource %q", ErrInvalidArgument, normalized)
		}
		for _, current := range pending {
			if current.path == normalized {
				return fmt.Errorf("%w: duplicate package resource %q", ErrInvalidArgument, normalized)
			}
		}
		data := resources[name]
		if uint64(len(data)) > s.limits.MaxFileBytes ||
			total > s.limits.MaxStorageBytes-uint64(len(data)) {
			return fmt.Errorf("mount package resource %q: %w", name, ErrLimitExceeded)
		}
		total += uint64(len(data))
		pending = append(pending, pendingResource{path: normalized, data: data})
	}
	if uint64(len(s.files))+uint64(len(s.directories))+uint64(len(pending)) >
		uint64(s.limits.MaxFiles) {
		return fmt.Errorf("%w: file count exceeds %d", ErrLimitExceeded, s.limits.MaxFiles)
	}
	candidate := *s
	candidate.files = cloneStorageFiles(s.files)
	candidate.directories = cloneStorageDirectories(s.directories)
	for _, resource := range pending {
		key := storageKey(NamespacePackage, resource.path)
		if candidate.directories[key] != nil {
			return fmt.Errorf(
				"%w: package resource %q collides with a directory",
				ErrInvalidArgument,
				resource.path,
			)
		}
		candidate.files[key] = &storageFile{
			namespace: NamespacePackage,
			path:      resource.path,
			data:      cloneBytes(resource.data),
			modified:  s.clock.Monotonic(),
			readOnly:  true,
		}
	}
	for _, resource := range pending {
		if err := candidate.ensureParentDirectories(
			NamespacePackage,
			resource.path,
			true,
		); err != nil {
			return err
		}
	}
	s.files = candidate.files
	s.directories = candidate.directories
	return nil
}

// ReplacePackage atomically replaces the read-only package namespace. It is
// intended for adapter bootstrap and persistence import, before package files
// are opened by guest code.
func (s *Storage) ReplacePackage(resources map[string][]byte) error {
	for _, handle := range s.openFiles {
		if handle.namespace == NamespacePackage {
			return fmt.Errorf("%w: package resource is open", ErrInvalidState)
		}
	}
	names := make([]string, 0, len(resources))
	for name := range resources {
		names = append(names, name)
	}
	sort.Strings(names)
	replacement := make(map[string]*storageFile, len(resources))
	replacementDirectories := make(map[string]*storageDirectory)
	total := uint64(0)
	mutableCount := uint64(0)
	for key, current := range s.files {
		if current.namespace == NamespacePackage {
			continue
		}
		total += uint64(len(current.data))
		mutableCount++
		replacement[key] = current
	}
	for key, current := range s.directories {
		if current.namespace != NamespacePackage {
			replacementDirectories[key] = current
		}
	}
	seen := make(map[string]struct{}, len(names))
	for _, name := range names {
		normalized, err := s.NormalizePath(name)
		if err != nil {
			return fmt.Errorf("replace package resource %q: %w", name, err)
		}
		if _, duplicate := seen[normalized]; duplicate {
			return fmt.Errorf(
				"%w: duplicate package resource %q",
				ErrInvalidArgument,
				normalized,
			)
		}
		seen[normalized] = struct{}{}
		data := resources[name]
		if uint64(len(data)) > s.limits.MaxFileBytes ||
			total > s.limits.MaxStorageBytes-uint64(len(data)) {
			return fmt.Errorf("replace package resource %q: %w", name, ErrLimitExceeded)
		}
		total += uint64(len(data))
		replacement[storageKey(NamespacePackage, normalized)] = &storageFile{
			namespace: NamespacePackage,
			path:      normalized,
			data:      cloneBytes(data),
			modified:  s.clock.Monotonic(),
			readOnly:  true,
		}
	}
	if mutableCount+uint64(len(replacementDirectories))+uint64(len(seen)) >
		uint64(s.limits.MaxFiles) {
		return fmt.Errorf("%w: file count exceeds %d", ErrLimitExceeded, s.limits.MaxFiles)
	}
	candidate := *s
	candidate.files = replacement
	candidate.directories = replacementDirectories
	for _, current := range replacement {
		if err := candidate.ensureParentDirectories(
			current.namespace,
			current.path,
			current.readOnly,
		); err != nil {
			return err
		}
	}
	s.files = candidate.files
	s.directories = candidate.directories
	return nil
}

func (s *Storage) ReadFile(namespace Namespace, name string) ([]byte, error) {
	current, err := s.file(namespace, name)
	if err != nil {
		return nil, err
	}
	return cloneBytes(current.data), nil
}

func (s *Storage) WriteFile(namespace Namespace, name string, data []byte) error {
	if namespace == NamespacePackage {
		return ErrReadOnly
	}
	normalized, err := s.validPath(namespace, name)
	if err != nil {
		return err
	}
	key := storageKey(namespace, normalized)
	if s.directories[key] != nil {
		return fmt.Errorf("%w: path is a directory", ErrInvalidArgument)
	}
	current := s.files[key]
	if current == nil {
		return s.putNewFile(namespace, normalized, data, false)
	}
	if current.readOnly {
		return ErrReadOnly
	}
	if err := s.checkResize(current, uint64(len(data))); err != nil {
		return err
	}
	current.data = cloneBytes(data)
	current.modified = s.clock.Monotonic()
	return nil
}

func (s *Storage) Open(owner OwnerID, namespace Namespace, name string, mode OpenMode) (ServiceID, error) {
	if !mode.Valid() {
		return 0, fmt.Errorf("%w: invalid open mode 0x%x", ErrInvalidArgument, mode)
	}
	if namespace == NamespacePackage && mode&OpenWrite != 0 {
		return 0, ErrReadOnly
	}
	if uint32(len(s.openFiles)) >= s.limits.MaxOpenHandles {
		return 0, fmt.Errorf("%w: open file handles reached %d", ErrLimitExceeded, s.limits.MaxOpenHandles)
	}
	normalized, err := s.validPath(namespace, name)
	if err != nil {
		return 0, err
	}
	key := storageKey(namespace, normalized)
	if s.directories[key] != nil {
		return 0, fmt.Errorf("%w: path is a directory", ErrInvalidArgument)
	}
	current := s.files[key]
	if current == nil {
		if mode&OpenCreate == 0 {
			return 0, fmt.Errorf("%w: file %s:%s", ErrNotFound, namespace, normalized)
		}
	}
	if current != nil && current.readOnly && mode&OpenWrite != 0 {
		return 0, ErrReadOnly
	}
	if current != nil && mode&OpenTruncate != 0 {
		if err := s.checkResize(current, 0); err != nil {
			return 0, err
		}
	}
	registryBefore := s.registry.Snapshot()
	id, err := s.registry.Create(owner, KindFile)
	if err != nil {
		return 0, err
	}
	if current == nil {
		if err := s.putNewFile(namespace, normalized, nil, false); err != nil {
			_ = s.registry.Restore(registryBefore)
			return 0, err
		}
		current = s.files[key]
	}
	if mode&OpenTruncate != 0 {
		current.data = nil
		current.modified = s.clock.Monotonic()
	}
	position := uint64(0)
	if mode&OpenAppend != 0 {
		position = uint64(len(current.data))
	}
	s.openFiles[id] = &openFile{
		id:        id,
		owner:     owner,
		namespace: namespace,
		path:      normalized,
		position:  position,
		mode:      mode,
	}
	return id, nil
}

func (s *Storage) Close(owner OwnerID, id ServiceID) error {
	if _, err := s.handle(owner, id); err != nil {
		return err
	}
	if err := s.registry.Destroy(id, owner, KindFile); err != nil {
		return err
	}
	delete(s.openFiles, id)
	return nil
}

func (s *Storage) Read(owner OwnerID, id ServiceID, size uint64) ([]byte, error) {
	handle, err := s.handle(owner, id)
	if err != nil {
		return nil, err
	}
	if handle.mode&OpenRead == 0 {
		return nil, fmt.Errorf("%w: file handle is not readable", ErrInvalidArgument)
	}
	current := s.files[storageKey(handle.namespace, handle.path)]
	if current == nil {
		return nil, fmt.Errorf("%w: open file disappeared", ErrInvalidState)
	}
	if handle.position >= uint64(len(current.data)) {
		return nil, nil
	}
	count := min(size, uint64(len(current.data))-handle.position)
	if count > uint64(math.MaxInt) {
		return nil, fmt.Errorf("%w: read exceeds host address space", ErrLimitExceeded)
	}
	result := cloneBytes(current.data[handle.position : handle.position+count])
	handle.position += count
	return result, nil
}

func (s *Storage) Write(owner OwnerID, id ServiceID, data []byte) (int, error) {
	handle, err := s.handle(owner, id)
	if err != nil {
		return 0, err
	}
	if handle.mode&OpenWrite == 0 {
		return 0, fmt.Errorf("%w: file handle is not writable", ErrInvalidArgument)
	}
	current := s.files[storageKey(handle.namespace, handle.path)]
	if current == nil {
		return 0, fmt.Errorf("%w: open file disappeared", ErrInvalidState)
	}
	if current.readOnly {
		return 0, ErrReadOnly
	}
	position := handle.position
	if handle.mode&OpenAppend != 0 {
		position = uint64(len(current.data))
	}
	end := position + uint64(len(data))
	if end < position {
		return 0, fmt.Errorf("%w: file position overflow", ErrLimitExceeded)
	}
	if err := s.checkResize(current, end); err != nil {
		return 0, err
	}
	if end > uint64(math.MaxInt) {
		return 0, fmt.Errorf("%w: file size exceeds host address space", ErrLimitExceeded)
	}
	if end > uint64(len(current.data)) {
		current.data = append(current.data, make([]byte, int(end)-len(current.data))...)
	}
	copy(current.data[position:end], data)
	handle.position = end
	current.modified = s.clock.Monotonic()
	return len(data), nil
}

func (s *Storage) Seek(owner OwnerID, id ServiceID, offset int64, whence SeekWhence) (uint64, error) {
	handle, err := s.handle(owner, id)
	if err != nil {
		return 0, err
	}
	current := s.files[storageKey(handle.namespace, handle.path)]
	if current == nil {
		return 0, fmt.Errorf("%w: open file disappeared", ErrInvalidState)
	}
	var base uint64
	switch whence {
	case SeekStart:
		base = 0
	case SeekCurrent:
		base = handle.position
	case SeekEnd:
		base = uint64(len(current.data))
	default:
		return 0, fmt.Errorf("%w: invalid seek origin", ErrInvalidArgument)
	}
	var position uint64
	if offset < 0 {
		delta := uint64(-(offset + 1)) + 1
		if delta > base {
			return 0, fmt.Errorf("%w: negative file position", ErrInvalidArgument)
		}
		position = base - delta
	} else {
		position = base + uint64(offset)
		if position < base {
			return 0, fmt.Errorf("%w: file position overflow", ErrLimitExceeded)
		}
	}
	if position > s.limits.MaxFileBytes {
		return 0, fmt.Errorf("%w: file position exceeds limit", ErrLimitExceeded)
	}
	handle.position = position
	return position, nil
}

func (s *Storage) Stat(namespace Namespace, name string) (FileInfo, error) {
	current, err := s.file(namespace, name)
	if err != nil {
		return FileInfo{}, err
	}
	return FileInfo{
		Namespace: current.namespace,
		Path:      current.path,
		Size:      uint64(len(current.data)),
		Modified:  current.modified,
		ReadOnly:  current.readOnly,
	}, nil
}

func (s *Storage) Delete(namespace Namespace, name string) error {
	if namespace == NamespacePackage {
		return ErrReadOnly
	}
	current, err := s.file(namespace, name)
	if err != nil {
		return err
	}
	for _, handle := range s.openFiles {
		if handle.namespace == namespace && handle.path == current.path {
			return fmt.Errorf("%w: file is open", ErrInvalidState)
		}
	}
	delete(s.files, storageKey(namespace, current.path))
	return nil
}

func (s *Storage) Rename(namespace Namespace, oldName, newName string) error {
	if namespace == NamespacePackage {
		return ErrReadOnly
	}
	oldPath, err := s.validPath(namespace, oldName)
	if err != nil {
		return err
	}
	newPath, err := s.validPath(namespace, newName)
	if err != nil {
		return err
	}
	oldKey := storageKey(namespace, oldPath)
	newKey := storageKey(namespace, newPath)
	current := s.files[oldKey]
	if current == nil {
		return fmt.Errorf("%w: file %s:%s", ErrNotFound, namespace, oldPath)
	}
	if s.files[newKey] != nil || s.directories[newKey] != nil {
		return fmt.Errorf("%w: destination file exists", ErrInvalidArgument)
	}
	if err := s.ensureParentDirectories(namespace, newPath, false); err != nil {
		return err
	}
	delete(s.files, oldKey)
	current.path = newPath
	current.modified = s.clock.Monotonic()
	s.files[newKey] = current
	for _, handle := range s.openFiles {
		if handle.namespace == namespace && handle.path == oldPath {
			handle.path = newPath
		}
	}
	return nil
}

func (s *Storage) List(namespace Namespace, directory string) ([]string, error) {
	if !namespace.Valid() {
		return nil, fmt.Errorf("%w: invalid storage namespace", ErrInvalidArgument)
	}
	if directory == "" {
		directory = "/"
	}
	if directory != "/" {
		var err error
		directory, err = s.NormalizePath(directory)
		if err != nil {
			return nil, err
		}
		if s.directories[storageKey(namespace, directory)] == nil {
			return nil, fmt.Errorf(
				"%w: directory %s:%s",
				ErrNotFound,
				namespace,
				directory,
			)
		}
	}
	prefix := strings.TrimSuffix(directory, "/") + "/"
	entries := make(map[string]struct{})
	for _, current := range s.files {
		if current.namespace != namespace || !strings.HasPrefix(current.path, prefix) {
			continue
		}
		remainder := strings.TrimPrefix(current.path, prefix)
		entry := strings.SplitN(remainder, "/", 2)[0]
		if entry != "" {
			entries[entry] = struct{}{}
		}
	}
	for _, current := range s.directories {
		if current.namespace != namespace || !strings.HasPrefix(current.path, prefix) {
			continue
		}
		remainder := strings.TrimPrefix(current.path, prefix)
		entry := strings.SplitN(remainder, "/", 2)[0]
		if entry != "" {
			entries[entry] = struct{}{}
		}
	}
	result := make([]string, 0, len(entries))
	for entry := range entries {
		result = append(result, entry)
	}
	sort.Strings(result)
	return result, nil
}

func (s *Storage) MakeDirectory(namespace Namespace, name string) error {
	if !namespace.Valid() {
		return fmt.Errorf("%w: invalid storage namespace", ErrInvalidArgument)
	}
	if namespace == NamespacePackage {
		return ErrReadOnly
	}
	normalized, err := s.normalizeDirectory(name)
	if err != nil || normalized == "/" {
		return fmt.Errorf("%w: invalid directory path", ErrInvalidArgument)
	}
	key := storageKey(namespace, normalized)
	if s.files[key] != nil || s.directories[key] != nil {
		return fmt.Errorf("%w: directory path exists", ErrInvalidArgument)
	}
	return s.ensureDirectory(namespace, normalized, false)
}

func (s *Storage) RemoveDirectory(namespace Namespace, name string) error {
	if !namespace.Valid() {
		return fmt.Errorf("%w: invalid storage namespace", ErrInvalidArgument)
	}
	if namespace == NamespacePackage {
		return ErrReadOnly
	}
	normalized, err := s.normalizeDirectory(name)
	if err != nil || normalized == "/" {
		return fmt.Errorf("%w: invalid directory path", ErrInvalidArgument)
	}
	key := storageKey(namespace, normalized)
	if s.directories[key] == nil {
		return fmt.Errorf("%w: directory %s:%s", ErrNotFound, namespace, normalized)
	}
	prefix := normalized + "/"
	for _, current := range s.files {
		if current.namespace == namespace && strings.HasPrefix(current.path, prefix) {
			return fmt.Errorf("%w: directory is not empty", ErrInvalidState)
		}
	}
	for _, current := range s.directories {
		if current.namespace == namespace && strings.HasPrefix(current.path, prefix) {
			return fmt.Errorf("%w: directory is not empty", ErrInvalidState)
		}
	}
	delete(s.directories, key)
	return nil
}

func (s *Storage) DirectoryExists(namespace Namespace, name string) bool {
	if !namespace.Valid() {
		return false
	}
	normalized, err := s.normalizeDirectory(name)
	if err != nil {
		return false
	}
	return normalized == "/" ||
		s.directories[storageKey(namespace, normalized)] != nil
}

func (s *Storage) RenameDirectory(
	namespace Namespace,
	oldName, newName string,
) error {
	if !namespace.Valid() {
		return fmt.Errorf("%w: invalid storage namespace", ErrInvalidArgument)
	}
	if namespace == NamespacePackage {
		return ErrReadOnly
	}
	oldPath, err := s.normalizeDirectory(oldName)
	if err != nil || oldPath == "/" {
		return fmt.Errorf("%w: invalid source directory", ErrInvalidArgument)
	}
	newPath, err := s.normalizeDirectory(newName)
	if err != nil || newPath == "/" || strings.HasPrefix(newPath+"/", oldPath+"/") {
		return fmt.Errorf("%w: invalid destination directory", ErrInvalidArgument)
	}
	oldKey := storageKey(namespace, oldPath)
	newKey := storageKey(namespace, newPath)
	if s.directories[oldKey] == nil {
		return fmt.Errorf("%w: source directory", ErrNotFound)
	}
	if s.directories[newKey] != nil || s.files[newKey] != nil {
		return fmt.Errorf("%w: destination exists", ErrInvalidArgument)
	}
	for _, current := range s.directories {
		if current.namespace != namespace ||
			(current.path != oldPath && !strings.HasPrefix(current.path, oldPath+"/")) {
			continue
		}
		replacement := newPath + strings.TrimPrefix(current.path, oldPath)
		if uint32(len(replacement)) > s.limits.MaxPathBytes {
			return fmt.Errorf("%w: renamed directory path exceeds limit", ErrLimitExceeded)
		}
		replacementKey := storageKey(namespace, replacement)
		if existing := s.directories[replacementKey]; existing != nil &&
			existing.path != current.path {
			return fmt.Errorf("%w: destination directory exists", ErrInvalidArgument)
		}
		if existing := s.files[replacementKey]; existing != nil {
			return fmt.Errorf("%w: destination file exists", ErrInvalidArgument)
		}
	}
	for _, current := range s.files {
		if current.namespace != namespace || !strings.HasPrefix(current.path, oldPath+"/") {
			continue
		}
		replacement := newPath + strings.TrimPrefix(current.path, oldPath)
		if uint32(len(replacement)) > s.limits.MaxPathBytes {
			return fmt.Errorf("%w: renamed file path exceeds limit", ErrLimitExceeded)
		}
		replacementKey := storageKey(namespace, replacement)
		if existing := s.files[replacementKey]; existing != nil &&
			existing.path != current.path {
			return fmt.Errorf("%w: destination file exists", ErrInvalidArgument)
		}
		if s.directories[replacementKey] != nil {
			return fmt.Errorf("%w: destination directory exists", ErrInvalidArgument)
		}
	}
	files := make(map[string]*storageFile, len(s.files))
	for key, current := range s.files {
		files[key] = current
	}
	directories := make(map[string]*storageDirectory, len(s.directories))
	for key, current := range s.directories {
		directories[key] = current
	}
	candidate := *s
	candidate.files = files
	candidate.directories = directories
	if err := candidate.ensureParentDirectories(namespace, newPath, false); err != nil {
		return err
	}
	for key, current := range s.directories {
		if current.namespace != namespace ||
			(current.path != oldPath && !strings.HasPrefix(current.path, oldPath+"/")) {
			continue
		}
		delete(candidate.directories, key)
		copyDirectory := *current
		copyDirectory.path = newPath + strings.TrimPrefix(current.path, oldPath)
		copyDirectory.modified = s.clock.Monotonic()
		candidate.directories[storageKey(namespace, copyDirectory.path)] = &copyDirectory
	}
	for key, current := range s.files {
		if current.namespace != namespace || !strings.HasPrefix(current.path, oldPath+"/") {
			continue
		}
		delete(candidate.files, key)
		copyFile := *current
		copyFile.path = newPath + strings.TrimPrefix(current.path, oldPath)
		copyFile.modified = s.clock.Monotonic()
		candidate.files[storageKey(namespace, copyFile.path)] = &copyFile
	}
	for _, handle := range candidate.openFiles {
		if handle.namespace == namespace &&
			strings.HasPrefix(handle.path, oldPath+"/") {
			handle.path = newPath + strings.TrimPrefix(handle.path, oldPath)
		}
	}
	s.files = candidate.files
	s.directories = candidate.directories
	return nil
}

func (s *Storage) Used(namespace Namespace) uint64 {
	var total uint64
	for _, current := range s.files {
		if current.namespace == namespace {
			total += uint64(len(current.data))
		}
	}
	return total
}

func (s *Storage) CreateRecordStore(owner OwnerID, name string) (ServiceID, error) {
	normalized, err := normalizeRecordName(name, s.limits.MaxPathBytes)
	if err != nil {
		return 0, err
	}
	key := recordStoreKey(owner, normalized)
	if existing := s.recordNames[key]; existing != 0 {
		return 0, fmt.Errorf("%w: record store %q already exists", ErrInvalidArgument, name)
	}
	if uint32(len(s.recordStores)) >= s.limits.MaxRecordStores {
		return 0, fmt.Errorf("%w: record stores reached %d", ErrLimitExceeded, s.limits.MaxRecordStores)
	}
	id, err := s.registry.Create(owner, KindRecordBase)
	if err != nil {
		return 0, err
	}
	s.recordStores[id] = &recordStore{
		id:      id,
		owner:   owner,
		name:    normalized,
		nextID:  1,
		records: make(map[uint32][]byte),
	}
	s.recordNames[key] = id
	return id, nil
}

func (s *Storage) OpenRecordStore(owner OwnerID, name string) (ServiceID, error) {
	normalized, err := normalizeRecordName(name, s.limits.MaxPathBytes)
	if err != nil {
		return 0, err
	}
	id := s.recordNames[recordStoreKey(owner, normalized)]
	if id == 0 {
		return 0, fmt.Errorf("%w: record store %q", ErrNotFound, name)
	}
	if err := s.registry.Validate(id, owner, KindRecordBase); err != nil {
		return 0, err
	}
	return id, nil
}

func (s *Storage) DeleteRecordStore(owner OwnerID, id ServiceID) error {
	store, err := s.recordStore(owner, id)
	if err != nil {
		return err
	}
	if err := s.registry.Destroy(id, owner, KindRecordBase); err != nil {
		return err
	}
	delete(s.recordNames, recordStoreKey(owner, store.name))
	delete(s.recordStores, id)
	return nil
}

func (s *Storage) DeleteRecordStoreNamed(owner OwnerID, name string) error {
	id, err := s.OpenRecordStore(owner, name)
	if err != nil {
		return err
	}
	return s.DeleteRecordStore(owner, id)
}

func (s *Storage) RecordCount(owner OwnerID, id ServiceID) (uint32, error) {
	store, err := s.recordStore(owner, id)
	if err != nil {
		return 0, err
	}
	return uint32(len(store.records)), nil
}

func (s *Storage) NextRecordID(owner OwnerID, id ServiceID) (uint32, error) {
	store, err := s.recordStore(owner, id)
	if err != nil {
		return 0, err
	}
	if store.nextID == 0 || store.nextID == math.MaxUint32 {
		return 0, fmt.Errorf("%w: record ID exhausted", ErrLimitExceeded)
	}
	return store.nextID, nil
}

func (s *Storage) AddRecord(owner OwnerID, id ServiceID, data []byte) (uint32, error) {
	store, err := s.recordStore(owner, id)
	if err != nil {
		return 0, err
	}
	if uint32(len(store.records)) >= s.limits.MaxRecords ||
		store.nextID == 0 || store.nextID == math.MaxUint32 {
		return 0, fmt.Errorf("%w: record count or ID exhausted", ErrLimitExceeded)
	}
	if err := s.checkRecordResize(store, 0, uint64(len(data))); err != nil {
		return 0, err
	}
	recordID := store.nextID
	store.nextID++
	store.records[recordID] = cloneBytes(data)
	return recordID, nil
}

func (s *Storage) Record(owner OwnerID, id ServiceID, recordID uint32) ([]byte, error) {
	store, err := s.recordStore(owner, id)
	if err != nil {
		return nil, err
	}
	data, ok := store.records[recordID]
	if !ok {
		return nil, fmt.Errorf("%w: record %d", ErrNotFound, recordID)
	}
	return cloneBytes(data), nil
}

func (s *Storage) SetRecord(owner OwnerID, id ServiceID, recordID uint32, data []byte) error {
	store, err := s.recordStore(owner, id)
	if err != nil {
		return err
	}
	current, ok := store.records[recordID]
	if !ok {
		return fmt.Errorf("%w: record %d", ErrNotFound, recordID)
	}
	if err := s.checkRecordResize(store, uint64(len(current)), uint64(len(data))); err != nil {
		return err
	}
	store.records[recordID] = cloneBytes(data)
	return nil
}

func (s *Storage) DeleteRecord(owner OwnerID, id ServiceID, recordID uint32) error {
	store, err := s.recordStore(owner, id)
	if err != nil {
		return err
	}
	if _, ok := store.records[recordID]; !ok {
		return fmt.Errorf("%w: record %d", ErrNotFound, recordID)
	}
	delete(store.records, recordID)
	return nil
}

func (s *Storage) RecordIDs(owner OwnerID, id ServiceID) ([]uint32, error) {
	store, err := s.recordStore(owner, id)
	if err != nil {
		return nil, err
	}
	ids := make([]uint32, 0, len(store.records))
	for recordID := range store.records {
		ids = append(ids, recordID)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	return ids, nil
}

// ReplaceRecords atomically imports an adapter's record view. Record ID zero
// is accepted because some WIPI database ABIs use it even though Java RMS
// allocates IDs starting at one.
func (s *Storage) ReplaceRecords(
	owner OwnerID,
	id ServiceID,
	nextID uint32,
	records map[uint32][]byte,
) error {
	store, err := s.recordStore(owner, id)
	if err != nil {
		return err
	}
	if nextID == 0 || nextID == math.MaxUint32 ||
		len(records) > int(s.limits.MaxRecords) {
		return fmt.Errorf("%w: invalid record import", ErrInvalidArgument)
	}
	candidate := make(map[uint32][]byte, len(records))
	var total uint64
	for recordID, data := range records {
		if recordID >= nextID {
			return fmt.Errorf(
				"%w: record %d is not below next ID %d",
				ErrInvalidArgument,
				recordID,
				nextID,
			)
		}
		total += uint64(len(data))
		if total > s.limits.MaxRecordBytes {
			return fmt.Errorf("%w: record store byte quota", ErrLimitExceeded)
		}
		candidate[recordID] = cloneBytes(data)
	}
	store.nextID = nextID
	store.records = candidate
	return nil
}

func (s *Storage) Snapshot() StorageState {
	state := StorageState{Limits: s.limits}
	directoryKeys := make([]string, 0, len(s.directories))
	for key := range s.directories {
		directoryKeys = append(directoryKeys, key)
	}
	sort.Strings(directoryKeys)
	for _, key := range directoryKeys {
		current := s.directories[key]
		state.Directories = append(state.Directories, DirectoryState{
			Namespace: current.namespace,
			Path:      current.path,
			Modified:  int64(current.modified),
			ReadOnly:  current.readOnly,
		})
	}
	keys := make([]string, 0, len(s.files))
	for key := range s.files {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		current := s.files[key]
		state.Files = append(state.Files, FileState{
			Namespace: current.namespace,
			Path:      current.path,
			Data:      cloneBytes(current.data),
			Modified:  int64(current.modified),
			ReadOnly:  current.readOnly,
		})
	}
	handleIDs := make([]ServiceID, 0, len(s.openFiles))
	for id := range s.openFiles {
		handleIDs = append(handleIDs, id)
	}
	sort.Slice(handleIDs, func(i, j int) bool { return handleIDs[i] < handleIDs[j] })
	for _, id := range handleIDs {
		handle := s.openFiles[id]
		state.OpenFiles = append(state.OpenFiles, OpenFileState{
			ID:        handle.id,
			Owner:     handle.owner,
			Namespace: handle.namespace,
			Path:      handle.path,
			Position:  handle.position,
			Mode:      handle.mode,
		})
	}
	storeIDs := make([]ServiceID, 0, len(s.recordStores))
	for id := range s.recordStores {
		storeIDs = append(storeIDs, id)
	}
	sort.Slice(storeIDs, func(i, j int) bool { return storeIDs[i] < storeIDs[j] })
	for _, id := range storeIDs {
		store := s.recordStores[id]
		saved := RecordStoreState{
			ID:     store.id,
			Owner:  store.owner,
			Name:   store.name,
			NextID: store.nextID,
		}
		recordIDs := make([]uint32, 0, len(store.records))
		for recordID := range store.records {
			recordIDs = append(recordIDs, recordID)
		}
		sort.Slice(recordIDs, func(i, j int) bool { return recordIDs[i] < recordIDs[j] })
		for _, recordID := range recordIDs {
			saved.Records = append(saved.Records, RecordState{
				ID:   recordID,
				Data: cloneBytes(store.records[recordID]),
			})
		}
		state.RecordStores = append(state.RecordStores, saved)
	}
	return state
}

func (s *Storage) Restore(state StorageState) error {
	if err := state.Limits.Validate(); err != nil ||
		uint64(len(state.Directories)) > uint64(state.Limits.MaxFiles) ||
		uint64(len(state.Files)) > uint64(state.Limits.MaxFiles) ||
		uint64(len(state.Directories))+uint64(len(state.Files)) >
			uint64(state.Limits.MaxFiles) ||
		uint64(len(state.OpenFiles)) > uint64(state.Limits.MaxOpenHandles) ||
		uint64(len(state.RecordStores)) > uint64(state.Limits.MaxRecordStores) {
		return fmt.Errorf("%w: invalid storage state limits", ErrInvalidState)
	}
	candidate, err := NewStorage(s.registry, s.clock, state.Limits)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrInvalidState, err)
	}
	previousDirectoryKey := ""
	for index, saved := range state.Directories {
		normalized, normalizeErr := candidate.normalizeDirectory(saved.Path)
		key := storageKey(saved.Namespace, normalized)
		if !saved.Namespace.Valid() ||
			normalizeErr != nil || normalized == "/" || normalized != saved.Path ||
			(index != 0 && key <= previousDirectoryKey) ||
			saved.Modified < 0 ||
			saved.Modified > int64(s.clock.Monotonic()) ||
			saved.ReadOnly != (saved.Namespace == NamespacePackage) {
			return fmt.Errorf("%w: invalid directory state %d", ErrInvalidState, index)
		}
		candidate.directories[key] = &storageDirectory{
			namespace: saved.Namespace,
			path:      normalized,
			modified:  time.Duration(saved.Modified),
			readOnly:  saved.ReadOnly,
		}
		previousDirectoryKey = key
	}
	previousFileKey := ""
	var storageBytes uint64
	for index, saved := range state.Files {
		normalized, normalizeErr := candidate.validPath(saved.Namespace, saved.Path)
		key := storageKey(saved.Namespace, normalized)
		if normalizeErr != nil || normalized != saved.Path ||
			(index != 0 && key <= previousFileKey) ||
			saved.Modified < 0 ||
			saved.Modified > int64(s.clock.Monotonic()) ||
			candidate.directories[key] != nil ||
			uint64(len(saved.Data)) > state.Limits.MaxFileBytes ||
			saved.ReadOnly != (saved.Namespace == NamespacePackage) {
			return fmt.Errorf("%w: invalid file state %d", ErrInvalidState, index)
		}
		dataSize := uint64(len(saved.Data))
		if dataSize > state.Limits.MaxStorageBytes ||
			storageBytes > state.Limits.MaxStorageBytes-dataSize {
			return fmt.Errorf("%w: saved files exceed storage quota", ErrInvalidState)
		}
		storageBytes += dataSize
		candidate.files[key] = &storageFile{
			namespace: saved.Namespace,
			path:      saved.Path,
			data:      cloneBytes(saved.Data),
			modified:  time.Duration(saved.Modified),
			readOnly:  saved.ReadOnly,
		}
		previousFileKey = key
	}
	for index, directory := range candidate.directories {
		parent := path.Dir(directory.path)
		if parent != "/" &&
			candidate.directories[storageKey(directory.namespace, parent)] == nil {
			return fmt.Errorf("%w: directory %q has no parent", ErrInvalidState, index)
		}
	}
	for index, current := range candidate.files {
		parent := path.Dir(current.path)
		if parent != "/" &&
			candidate.directories[storageKey(current.namespace, parent)] == nil {
			return fmt.Errorf("%w: file %q has no directory", ErrInvalidState, index)
		}
	}
	var previousID ServiceID
	for index, saved := range state.OpenFiles {
		key := storageKey(saved.Namespace, saved.Path)
		current := candidate.files[key]
		if !saved.ID.Valid() || (index != 0 && saved.ID <= previousID) ||
			current == nil || !saved.Mode.Valid() ||
			saved.Position > state.Limits.MaxFileBytes ||
			(current.readOnly && saved.Mode&OpenWrite != 0) ||
			s.registry.Validate(saved.ID, saved.Owner, KindFile) != nil {
			return fmt.Errorf("%w: invalid open file state %d", ErrInvalidState, index)
		}
		candidate.openFiles[saved.ID] = &openFile{
			id:        saved.ID,
			owner:     saved.Owner,
			namespace: saved.Namespace,
			path:      saved.Path,
			position:  saved.Position,
			mode:      saved.Mode,
		}
		previousID = saved.ID
	}
	previousID = 0
	for index, saved := range state.RecordStores {
		name, nameErr := normalizeRecordName(saved.Name, state.Limits.MaxPathBytes)
		if !saved.ID.Valid() || (index != 0 && saved.ID <= previousID) ||
			nameErr != nil || name != saved.Name || saved.NextID == 0 ||
			uint64(len(saved.Records)) > uint64(state.Limits.MaxRecords) ||
			s.registry.Validate(saved.ID, saved.Owner, KindRecordBase) != nil {
			return fmt.Errorf("%w: invalid record store state %d", ErrInvalidState, index)
		}
		key := recordStoreKey(saved.Owner, name)
		if candidate.recordNames[key] != 0 {
			return fmt.Errorf("%w: duplicate record store name", ErrInvalidState)
		}
		store := &recordStore{
			id:      saved.ID,
			owner:   saved.Owner,
			name:    name,
			nextID:  saved.NextID,
			records: make(map[uint32][]byte, len(saved.Records)),
		}
		var previousRecordID uint32
		var recordBytes uint64
		for recordIndex, record := range saved.Records {
			if record.ID >= saved.NextID ||
				(recordIndex != 0 && record.ID <= previousRecordID) {
				return fmt.Errorf(
					"%w: invalid record %d in store %d",
					ErrInvalidState,
					recordIndex,
					index,
				)
			}
			dataSize := uint64(len(record.Data))
			if dataSize > state.Limits.MaxRecordBytes ||
				recordBytes > state.Limits.MaxRecordBytes-dataSize {
				return fmt.Errorf("%w: record store exceeds byte quota", ErrInvalidState)
			}
			recordBytes += dataSize
			store.records[record.ID] = cloneBytes(record.Data)
			previousRecordID = record.ID
		}
		candidate.recordStores[saved.ID] = store
		candidate.recordNames[key] = saved.ID
		previousID = saved.ID
	}
	*s = *candidate
	return nil
}

func (s *Storage) file(namespace Namespace, name string) (*storageFile, error) {
	normalized, err := s.validPath(namespace, name)
	if err != nil {
		return nil, err
	}
	current := s.files[storageKey(namespace, normalized)]
	if current == nil {
		return nil, fmt.Errorf("%w: file %s:%s", ErrNotFound, namespace, normalized)
	}
	return current, nil
}

func (s *Storage) validPath(namespace Namespace, name string) (string, error) {
	if !namespace.Valid() {
		return "", fmt.Errorf("%w: invalid storage namespace", ErrInvalidArgument)
	}
	return s.NormalizePath(name)
}

func (s *Storage) handle(owner OwnerID, id ServiceID) (*openFile, error) {
	if err := s.registry.Validate(id, owner, KindFile); err != nil {
		return nil, err
	}
	handle := s.openFiles[id]
	if handle == nil {
		return nil, fmt.Errorf("%w: file handle %s", ErrInvalidState, id)
	}
	return handle, nil
}

func (s *Storage) recordStore(owner OwnerID, id ServiceID) (*recordStore, error) {
	if err := s.registry.Validate(id, owner, KindRecordBase); err != nil {
		return nil, err
	}
	store := s.recordStores[id]
	if store == nil {
		return nil, fmt.Errorf("%w: record store %s", ErrInvalidState, id)
	}
	return store, nil
}

func (s *Storage) putNewFile(
	namespace Namespace,
	name string,
	data []byte,
	readOnly bool,
) error {
	dataSize := uint64(len(data))
	used := s.totalUsed()
	if dataSize > s.limits.MaxFileBytes ||
		dataSize > s.limits.MaxStorageBytes ||
		used > s.limits.MaxStorageBytes-dataSize {
		return fmt.Errorf("%w: file count or storage quota exceeded", ErrLimitExceeded)
	}
	key := storageKey(namespace, name)
	if s.files[key] != nil || s.directories[key] != nil {
		return fmt.Errorf("%w: path already exists", ErrInvalidArgument)
	}
	directoriesBefore := cloneStorageDirectories(s.directories)
	if err := s.ensureParentDirectories(namespace, name, readOnly); err != nil {
		return err
	}
	if uint64(len(s.files))+uint64(len(s.directories)) >=
		uint64(s.limits.MaxFiles) {
		s.directories = directoriesBefore
		return fmt.Errorf("%w: file count exceeds %d", ErrLimitExceeded, s.limits.MaxFiles)
	}
	s.files[key] = &storageFile{
		namespace: namespace,
		path:      name,
		data:      cloneBytes(data),
		modified:  s.clock.Monotonic(),
		readOnly:  readOnly,
	}
	return nil
}

func (s *Storage) checkResize(current *storageFile, size uint64) error {
	if size > s.limits.MaxFileBytes {
		return fmt.Errorf("%w: file size %d exceeds %d", ErrLimitExceeded, size, s.limits.MaxFileBytes)
	}
	used := s.totalUsed()
	oldSize := uint64(len(current.data))
	if used < oldSize || used-oldSize > s.limits.MaxStorageBytes-size {
		return fmt.Errorf("%w: storage quota exceeded", ErrLimitExceeded)
	}
	return nil
}

func (s *Storage) totalUsed() uint64 {
	var total uint64
	for _, current := range s.files {
		total += uint64(len(current.data))
	}
	return total
}

func (s *Storage) checkRecordResize(store *recordStore, oldSize, newSize uint64) error {
	if newSize > s.limits.MaxRecordBytes {
		return fmt.Errorf("%w: record exceeds byte quota", ErrLimitExceeded)
	}
	var used uint64
	for _, data := range store.records {
		used += uint64(len(data))
	}
	if used < oldSize || used-oldSize > s.limits.MaxRecordBytes-newSize {
		return fmt.Errorf("%w: record store byte quota exceeded", ErrLimitExceeded)
	}
	return nil
}

func storageKey(namespace Namespace, name string) string {
	return fmt.Sprintf("%d:%s", namespace, name)
}

func normalizeRecordName(name string, maxBytes uint32) (string, error) {
	if strings.TrimSpace(name) == "" || uint32(len(name)) > maxBytes ||
		strings.IndexByte(name, 0) >= 0 {
		return "", fmt.Errorf("%w: invalid record store name", ErrInvalidArgument)
	}
	return name, nil
}

func recordStoreKey(owner OwnerID, name string) string {
	return fmt.Sprintf("%d:%s", owner, name)
}

func (s *Storage) normalizeDirectory(name string) (string, error) {
	if name == "/" {
		return "/", nil
	}
	return s.NormalizePath(name)
}

func (s *Storage) ensureParentDirectories(
	namespace Namespace,
	name string,
	readOnly bool,
) error {
	return s.ensureDirectory(namespace, path.Dir(name), readOnly)
}

func (s *Storage) ensureDirectory(
	namespace Namespace,
	name string,
	readOnly bool,
) error {
	if !namespace.Valid() {
		return fmt.Errorf("%w: invalid storage namespace", ErrInvalidArgument)
	}
	if namespace == NamespacePackage && !readOnly {
		return fmt.Errorf("%w: package directory is writable", ErrInvalidArgument)
	}
	normalized, err := s.normalizeDirectory(name)
	if err != nil {
		return err
	}
	if normalized == "/" {
		return nil
	}
	parts := strings.Split(strings.TrimPrefix(normalized, "/"), "/")
	directories := make([]string, 0, len(parts))
	current := ""
	missing := uint64(0)
	for _, part := range parts {
		current += "/" + part
		key := storageKey(namespace, current)
		if s.files[key] != nil {
			return fmt.Errorf(
				"%w: directory %s:%s collides with a file",
				ErrInvalidArgument,
				namespace,
				current,
			)
		}
		if directory := s.directories[key]; directory != nil {
			if directory.readOnly != readOnly {
				return fmt.Errorf(
					"%w: directory access mode differs",
					ErrInvalidState,
				)
			}
			continue
		}
		directories = append(directories, current)
		missing++
	}
	if uint64(len(s.files))+uint64(len(s.directories))+missing >
		uint64(s.limits.MaxFiles) {
		return fmt.Errorf(
			"%w: file and directory count exceeds %d",
			ErrLimitExceeded,
			s.limits.MaxFiles,
		)
	}
	for _, directory := range directories {
		s.directories[storageKey(namespace, directory)] = &storageDirectory{
			namespace: namespace,
			path:      directory,
			modified:  s.clock.Monotonic(),
			readOnly:  readOnly,
		}
	}
	return nil
}

func cloneStorageFiles(
	source map[string]*storageFile,
) map[string]*storageFile {
	result := make(map[string]*storageFile, len(source))
	for key, current := range source {
		result[key] = current
	}
	return result
}

func cloneStorageDirectories(
	source map[string]*storageDirectory,
) map[string]*storageDirectory {
	result := make(map[string]*storageDirectory, len(source))
	for key, current := range source {
		result[key] = current
	}
	return result
}
