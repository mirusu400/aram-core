package runtime

import (
	"bytes"
	"errors"
	"reflect"
	"testing"
	"time"
)

func newTestStorage(t *testing.T) (*Registry, *Clock, *Storage) {
	t.Helper()
	registry := NewRegistry(64)
	clock, err := NewClock(0, 0, "")
	if err != nil {
		t.Fatal(err)
	}
	storage, err := NewStorage(registry, clock, StorageLimits{})
	if err != nil {
		t.Fatal(err)
	}
	return registry, clock, storage
}

func TestStorageSeparatesPackageAndMutableNamespaces(t *testing.T) {
	_, clock, storage := newTestStorage(t)
	if err := storage.MountPackage(map[string][]byte{
		"config/data.bin": {1, 2, 3},
	}); err != nil {
		t.Fatal(err)
	}
	if err := storage.WriteFile(NamespacePrivate, "config/data.bin", []byte{4, 5}); err != nil {
		t.Fatal(err)
	}
	resource, err := storage.ReadFile(NamespacePackage, "/config/data.bin")
	if err != nil {
		t.Fatal(err)
	}
	private, err := storage.ReadFile(NamespacePrivate, "/config/data.bin")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(resource, []byte{1, 2, 3}) ||
		!bytes.Equal(private, []byte{4, 5}) {
		t.Fatalf("resource = %v, private = %v", resource, private)
	}
	if err := storage.WriteFile(NamespacePackage, "config/data.bin", nil); !errors.Is(err, ErrReadOnly) {
		t.Fatalf("write package error = %v", err)
	}
	if err := clock.Advance(time.Second); err != nil {
		t.Fatal(err)
	}
	if err := storage.WriteFile(NamespacePrivate, "config/data.bin", []byte{6}); err != nil {
		t.Fatal(err)
	}
	info, err := storage.Stat(NamespacePrivate, "config/data.bin")
	if err != nil {
		t.Fatal(err)
	}
	if info.Modified != time.Second || info.Size != 1 {
		t.Fatalf("private file info = %+v", info)
	}
}

func TestMountPackageRejectsBatchAtomically(t *testing.T) {
	_, _, storage := newTestStorage(t)
	before := storage.Snapshot()
	err := storage.MountPackage(map[string][]byte{
		"valid.bin":  {1},
		"../bad.bin": {2},
	})
	if err == nil {
		t.Fatal("MountPackage accepted an escaping path")
	}
	if after := storage.Snapshot(); !reflect.DeepEqual(after, before) {
		t.Fatal("rejected package mount changed storage")
	}
}

func TestStorageRejectsEscapingAndHostPaths(t *testing.T) {
	_, _, storage := newTestStorage(t)
	for _, name := range []string{
		"../secret",
		"a/../../secret",
		`C:\host\secret`,
		`folder\file`,
		"a\x00b",
		"/",
	} {
		if _, err := storage.NormalizePath(name); err == nil {
			t.Fatalf("NormalizePath(%q) succeeded", name)
		}
	}
	if got, err := storage.NormalizePath("folder/./file"); err != nil || got != "/folder/file" {
		t.Fatalf("NormalizePath = %q, %v", got, err)
	}
}

func TestStorageHandlesHaveIndependentPositionsAndStableIDs(t *testing.T) {
	_, _, storage := newTestStorage(t)
	if err := storage.WriteFile(NamespacePrivate, "save.dat", []byte("abcdef")); err != nil {
		t.Fatal(err)
	}
	first, err := storage.Open(1, NamespacePrivate, "save.dat", OpenRead|OpenWrite)
	if err != nil {
		t.Fatal(err)
	}
	second, err := storage.Open(1, NamespacePrivate, "save.dat", OpenRead)
	if err != nil {
		t.Fatal(err)
	}
	data, err := storage.Read(1, first, 2)
	if err != nil || string(data) != "ab" {
		t.Fatalf("first read = %q, %v", data, err)
	}
	data, err = storage.Read(1, second, 3)
	if err != nil || string(data) != "abc" {
		t.Fatalf("second read = %q, %v", data, err)
	}
	if _, err := storage.Seek(1, first, -1, SeekCurrent); err != nil {
		t.Fatal(err)
	}
	if _, err := storage.Write(1, first, []byte("Z")); err != nil {
		t.Fatal(err)
	}
	got, err := storage.ReadFile(NamespacePrivate, "save.dat")
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "aZcdef" {
		t.Fatalf("updated file = %q", got)
	}
	if err := storage.Close(1, first); err != nil {
		t.Fatal(err)
	}
	if _, err := storage.Read(1, first, 1); !errors.Is(err, ErrNotFound) && !errors.Is(err, ErrStaleID) {
		t.Fatalf("read closed handle error = %v", err)
	}
}

func TestStorageFailedAppendDoesNotMoveHandle(t *testing.T) {
	registry := NewRegistry(8)
	clock, err := NewClock(0, 0, "")
	if err != nil {
		t.Fatal(err)
	}
	limits := DefaultStorageLimits()
	limits.MaxFileBytes = 6
	limits.MaxStorageBytes = 6
	storage, err := NewStorage(registry, clock, limits)
	if err != nil {
		t.Fatal(err)
	}
	if err := storage.WriteFile(
		NamespacePrivate,
		"save.dat",
		[]byte("abcdef"),
	); err != nil {
		t.Fatal(err)
	}
	handle, err := storage.Open(
		1,
		NamespacePrivate,
		"save.dat",
		OpenRead|OpenWrite|OpenAppend,
	)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := storage.Seek(1, handle, 0, SeekStart); err != nil {
		t.Fatal(err)
	}
	if _, err := storage.Write(
		1,
		handle,
		[]byte("x"),
	); !errors.Is(err, ErrLimitExceeded) {
		t.Fatalf("over-limit append error = %v", err)
	}
	data, err := storage.Read(1, handle, 1)
	if err != nil || string(data) != "a" {
		t.Fatalf("read after failed append = %q, %v", data, err)
	}
}

func TestStorageDirectoriesRenameOpenFilesAndRoundTrip(t *testing.T) {
	registry, _, storage := newTestStorage(t)
	if err := storage.MakeDirectory(
		NamespacePrivate,
		"saves/slot1",
	); err != nil {
		t.Fatal(err)
	}
	if err := storage.WriteFile(
		NamespacePrivate,
		"saves/slot1/data.bin",
		[]byte("save"),
	); err != nil {
		t.Fatal(err)
	}
	handle, err := storage.Open(
		7,
		NamespacePrivate,
		"saves/slot1/data.bin",
		OpenRead,
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := storage.RenameDirectory(
		NamespacePrivate,
		"saves",
		"archive",
	); err != nil {
		t.Fatal(err)
	}
	if storage.DirectoryExists(NamespacePrivate, "saves") ||
		!storage.DirectoryExists(NamespacePrivate, "archive/slot1") {
		t.Fatal("renamed directory tree is inconsistent")
	}
	data, err := storage.Read(7, handle, 4)
	if err != nil || string(data) != "save" {
		t.Fatalf("read through renamed open file = %q, %v", data, err)
	}
	entries, err := storage.List(NamespacePrivate, "/")
	if err != nil || !reflect.DeepEqual(entries, []string{"archive"}) {
		t.Fatalf("root entries = %v, %v", entries, err)
	}
	if err := storage.RemoveDirectory(
		NamespacePrivate,
		"archive",
	); !errors.Is(err, ErrInvalidState) {
		t.Fatalf("remove non-empty directory error = %v", err)
	}

	state := storage.Snapshot()
	cloneClock, err := NewClock(0, 0, "")
	if err != nil {
		t.Fatal(err)
	}
	clone, err := NewStorage(registry, cloneClock, StorageLimits{})
	if err != nil {
		t.Fatal(err)
	}
	if err := clone.Restore(state); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(clone.Snapshot(), state) {
		t.Fatal("directory state did not round-trip")
	}
}

func TestStorageDirectoryRenameRejectsExpandedPathAtomically(t *testing.T) {
	registry := NewRegistry(8)
	clock, err := NewClock(0, 0, "")
	if err != nil {
		t.Fatal(err)
	}
	limits := DefaultStorageLimits()
	limits.MaxPathBytes = 12
	storage, err := NewStorage(registry, clock, limits)
	if err != nil {
		t.Fatal(err)
	}
	if err := storage.WriteFile(
		NamespacePrivate,
		"a/12345678",
		[]byte("save"),
	); err != nil {
		t.Fatal(err)
	}
	before := storage.Snapshot()
	if err := storage.RenameDirectory(
		NamespacePrivate,
		"a",
		"long",
	); !errors.Is(err, ErrLimitExceeded) {
		t.Fatalf("expanded directory rename error = %v", err)
	}
	if !reflect.DeepEqual(storage.Snapshot(), before) {
		t.Fatal("rejected directory rename mutated storage")
	}
}

func TestStoragePackageDirectoriesAreAtomicAndReadOnly(t *testing.T) {
	_, _, storage := newTestStorage(t)
	if err := storage.MountPackage(map[string][]byte{
		"assets/image.bin": {1, 2, 3},
	}); err != nil {
		t.Fatal(err)
	}
	if !storage.DirectoryExists(NamespacePackage, "assets") {
		t.Fatal("package parent directory was not created")
	}
	if err := storage.MakeDirectory(
		NamespacePackage,
		"other",
	); !errors.Is(err, ErrReadOnly) {
		t.Fatalf("create package directory error = %v", err)
	}
	before := storage.Snapshot()
	err := storage.MountPackage(map[string][]byte{
		"node":       {4},
		"node/child": {5},
	})
	if err == nil {
		t.Fatal("package mount accepted a file/directory collision")
	}
	if after := storage.Snapshot(); !reflect.DeepEqual(after, before) {
		t.Fatal("rejected package directory collision changed storage")
	}
}

func TestRecordStoreUsesStableNonReusedRecordIDs(t *testing.T) {
	_, _, storage := newTestStorage(t)
	store, err := storage.CreateRecordStore(9, "game-rms")
	if err != nil {
		t.Fatal(err)
	}
	first, err := storage.AddRecord(9, store, []byte("one"))
	if err != nil {
		t.Fatal(err)
	}
	second, err := storage.AddRecord(9, store, []byte("two"))
	if err != nil {
		t.Fatal(err)
	}
	if err := storage.DeleteRecord(9, store, first); err != nil {
		t.Fatal(err)
	}
	third, err := storage.AddRecord(9, store, []byte("three"))
	if err != nil {
		t.Fatal(err)
	}
	if first != 1 || second != 2 || third != 3 {
		t.Fatalf("record IDs = %d, %d, %d", first, second, third)
	}
	count, err := storage.RecordCount(9, store)
	if err != nil {
		t.Fatal(err)
	}
	next, err := storage.NextRecordID(9, store)
	if err != nil {
		t.Fatal(err)
	}
	if count != 2 || next != 4 {
		t.Fatalf("record count/next = %d/%d, want 2/4", count, next)
	}
	ids, err := storage.RecordIDs(9, store)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(ids, []uint32{2, 3}) {
		t.Fatalf("record IDs = %v", ids)
	}
}

func TestStorageRestoreValidatesBeforeMutation(t *testing.T) {
	registry, _, storage := newTestStorage(t)
	if err := storage.WriteFile(NamespacePrivate, "save.dat", []byte("original")); err != nil {
		t.Fatal(err)
	}
	handle, err := storage.Open(2, NamespacePrivate, "save.dat", OpenRead)
	if err != nil {
		t.Fatal(err)
	}
	store, err := storage.CreateRecordStore(2, "rms")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := storage.AddRecord(2, store, []byte("record")); err != nil {
		t.Fatal(err)
	}
	before := storage.Snapshot()
	invalid := storage.Snapshot()
	invalid.OpenFiles[0].ID = makeServiceID(handle.Slot(), handle.Generation()+1)
	if err := storage.Restore(invalid); !errors.Is(err, ErrInvalidState) {
		t.Fatalf("Restore invalid storage error = %v", err)
	}
	after := storage.Snapshot()
	if !reflect.DeepEqual(after, before) {
		t.Fatal("storage mutated after rejected restore")
	}
	for name, mutate := range map[string]func(*StorageState){
		"mutable read-only file": func(state *StorageState) {
			state.Files[0].ReadOnly = true
		},
		"future file timestamp": func(state *StorageState) {
			state.Files[0].Modified = 1
		},
	} {
		t.Run(name, func(t *testing.T) {
			invalid := storage.Snapshot()
			mutate(&invalid)
			if err := storage.Restore(invalid); !errors.Is(err, ErrInvalidState) {
				t.Fatalf("Restore invalid storage error = %v", err)
			}
			if after := storage.Snapshot(); !reflect.DeepEqual(after, before) {
				t.Fatal("storage mutated after rejected restore")
			}
		})
	}

	cloneClock, err := NewClock(0, 0, "")
	if err != nil {
		t.Fatal(err)
	}
	clone, err := NewStorage(registry, cloneClock, StorageLimits{})
	if err != nil {
		t.Fatal(err)
	}
	if err := clone.Restore(before); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(clone.Snapshot(), before) {
		t.Fatal("storage state did not round-trip")
	}
}

func TestStorageOpenRegistryFailureDoesNotCreateOrTruncate(t *testing.T) {
	registry := NewRegistry(1)
	clock, err := NewClock(0, 0, "")
	if err != nil {
		t.Fatal(err)
	}
	storage, err := NewStorage(registry, clock, StorageLimits{})
	if err != nil {
		t.Fatal(err)
	}
	if err := storage.WriteFile(
		NamespacePrivate,
		"existing.dat",
		[]byte("keep"),
	); err != nil {
		t.Fatal(err)
	}
	occupied, err := registry.Create(99, KindSurface)
	if err != nil {
		t.Fatal(err)
	}
	beforeStorage, beforeRegistry := storage.Snapshot(), registry.Snapshot()
	if _, err := storage.Open(
		1,
		NamespacePrivate,
		"new.dat",
		OpenWrite|OpenCreate,
	); !errors.Is(err, ErrLimitExceeded) {
		t.Fatalf("Open create with exhausted registry error = %v", err)
	}
	if _, err := storage.Open(
		1,
		NamespacePrivate,
		"existing.dat",
		OpenWrite|OpenTruncate,
	); !errors.Is(err, ErrLimitExceeded) {
		t.Fatalf("Open truncate with exhausted registry error = %v", err)
	}
	if !reflect.DeepEqual(storage.Snapshot(), beforeStorage) ||
		!reflect.DeepEqual(registry.Snapshot(), beforeRegistry) {
		t.Fatal("failed open create/truncate mutated storage or registry")
	}
	if err := registry.Destroy(occupied, 99, KindSurface); err != nil {
		t.Fatal(err)
	}
}

func TestStoragePersistenceRoundTripExcludesProcessLocalState(t *testing.T) {
	_, _, source := newTestStorage(t)
	if err := source.MountPackage(map[string][]byte{
		"assets/runtime.bin": []byte("source-package"),
	}); err != nil {
		t.Fatal(err)
	}
	if err := source.WriteFile(
		NamespacePrivate,
		"saves/slot/data.bin",
		[]byte("private-save"),
	); err != nil {
		t.Fatal(err)
	}
	if err := source.WriteFile(
		NamespaceShared,
		"shared/config.bin",
		[]byte("shared-save"),
	); err != nil {
		t.Fatal(err)
	}
	if err := source.WriteFile(
		NamespaceTemporary,
		"cache.bin",
		[]byte("source-temporary"),
	); err != nil {
		t.Fatal(err)
	}
	store, err := source.CreateRecordStore(27, "game-rms")
	if err != nil {
		t.Fatal(err)
	}
	first, err := source.AddRecord(27, store, []byte("discarded"))
	if err != nil {
		t.Fatal(err)
	}
	second, err := source.AddRecord(27, store, []byte("record-two"))
	if err != nil {
		t.Fatal(err)
	}
	if err := source.DeleteRecord(27, store, first); err != nil {
		t.Fatal(err)
	}

	exported := source.ExportPersistence()
	if len(exported.Files) == 0 || len(exported.RecordStores) != 1 ||
		len(exported.RecordStores[0].Records) != 1 {
		t.Fatalf("unexpected persistence export: %+v", exported)
	}
	exported.Files[0].Data[0] ^= 0xff
	exported.RecordStores[0].Records[0].Data[0] ^= 0xff
	privateData, err := source.ReadFile(
		NamespacePrivate,
		"saves/slot/data.bin",
	)
	if err != nil {
		t.Fatal(err)
	}
	recordData, err := source.Record(27, store, second)
	if err != nil {
		t.Fatal(err)
	}
	if string(privateData) != "private-save" ||
		string(recordData) != "record-two" {
		t.Fatal("persistence export aliased live storage")
	}
	exported = source.ExportPersistence()

	_, _, destination := newTestStorage(t)
	if err := destination.MountPackage(map[string][]byte{
		"assets/runtime.bin": []byte("destination-package"),
	}); err != nil {
		t.Fatal(err)
	}
	if err := destination.WriteFile(
		NamespaceTemporary,
		"cache.bin",
		[]byte("destination-temporary"),
	); err != nil {
		t.Fatal(err)
	}
	if err := destination.WriteFile(
		NamespacePrivate,
		"old.bin",
		[]byte("replace-me"),
	); err != nil {
		t.Fatal(err)
	}
	replacedStore, err := destination.CreateRecordStore(88, "replace-me")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := destination.AddRecord(
		88,
		replacedStore,
		[]byte("replace-me"),
	); err != nil {
		t.Fatal(err)
	}
	if err := destination.ImportPersistence(exported); err != nil {
		t.Fatal(err)
	}
	for namespace, nameAndWant := range map[Namespace][2]string{
		NamespacePackage:   {"assets/runtime.bin", "destination-package"},
		NamespacePrivate:   {"saves/slot/data.bin", "private-save"},
		NamespaceShared:    {"shared/config.bin", "shared-save"},
		NamespaceTemporary: {"cache.bin", "destination-temporary"},
	} {
		data, err := destination.ReadFile(namespace, nameAndWant[0])
		if err != nil || string(data) != nameAndWant[1] {
			t.Fatalf(
				"%s file = %q, %v; want %q",
				namespace,
				data,
				err,
				nameAndWant[1],
			)
		}
	}
	if _, err := destination.ReadFile(
		NamespacePrivate,
		"old.bin",
	); !errors.Is(err, ErrNotFound) {
		t.Fatalf("replaced private file error = %v", err)
	}
	if _, err := destination.RecordCount(
		88,
		replacedStore,
	); !errors.Is(err, ErrStaleID) {
		t.Fatalf("replaced record store ID error = %v", err)
	}
	if _, err := destination.OpenRecordStore(
		88,
		"replace-me",
	); !errors.Is(err, ErrNotFound) {
		t.Fatalf("replaced named record store error = %v", err)
	}
	if after := destination.ExportPersistence(); !reflect.DeepEqual(after, exported) {
		t.Fatalf("persistence did not round-trip:\ngot  %+v\nwant %+v", after, exported)
	}
	restoredStore, err := destination.OpenRecordStore(27, "game-rms")
	if err != nil {
		t.Fatal(err)
	}
	ids, err := destination.RecordIDs(27, restoredStore)
	if err != nil || !reflect.DeepEqual(ids, []uint32{second}) {
		t.Fatalf("restored record IDs = %v, %v", ids, err)
	}
	next, err := destination.AddRecord(27, restoredStore, []byte("record-three"))
	if err != nil || next != 3 {
		t.Fatalf("next restored record ID = %d, %v; want 3", next, err)
	}
}

func TestStoragePersistenceRejectsInvalidStateAtomically(t *testing.T) {
	_, _, source := newTestStorage(t)
	if err := source.WriteFile(
		NamespacePrivate,
		"saves/data.bin",
		[]byte("save"),
	); err != nil {
		t.Fatal(err)
	}
	persistence := source.ExportPersistence()
	persistence.Files[0].Path = "/missing/data.bin"

	registry, _, destination := newTestStorage(t)
	if err := destination.WriteFile(
		NamespacePrivate,
		"existing.bin",
		[]byte("existing"),
	); err != nil {
		t.Fatal(err)
	}
	beforeStorage, beforeRegistry := destination.Snapshot(), registry.Snapshot()
	if err := destination.ImportPersistence(persistence); !errors.Is(err, ErrInvalidState) {
		t.Fatalf("ImportPersistence invalid state error = %v", err)
	}
	if !reflect.DeepEqual(destination.Snapshot(), beforeStorage) ||
		!reflect.DeepEqual(registry.Snapshot(), beforeRegistry) {
		t.Fatal("rejected persistence import mutated storage or registry")
	}
}

func TestStoragePersistenceRegistryFailureIsAtomic(t *testing.T) {
	_, _, source := newTestStorage(t)
	if _, err := source.CreateRecordStore(5, "rms"); err != nil {
		t.Fatal(err)
	}
	persistence := source.ExportPersistence()

	registry := NewRegistry(1)
	clock, err := NewClock(0, 0, "")
	if err != nil {
		t.Fatal(err)
	}
	destination, err := NewStorage(registry, clock, StorageLimits{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := registry.Create(99, KindSurface); err != nil {
		t.Fatal(err)
	}
	beforeStorage, beforeRegistry := destination.Snapshot(), registry.Snapshot()
	if err := destination.ImportPersistence(persistence); !errors.Is(err, ErrLimitExceeded) {
		t.Fatalf("ImportPersistence exhausted registry error = %v", err)
	}
	if !reflect.DeepEqual(destination.Snapshot(), beforeStorage) ||
		!reflect.DeepEqual(registry.Snapshot(), beforeRegistry) {
		t.Fatal("failed persistence import mutated storage or registry")
	}
}

func TestStoragePersistenceRebasesTimestampsForNewRuntime(t *testing.T) {
	source, err := NewServices(Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := source.Clock.Advance(time.Second); err != nil {
		t.Fatal(err)
	}
	if err := source.Storage.WriteFile(
		NamespacePrivate,
		"saves/slot.bin",
		[]byte("save"),
	); err != nil {
		t.Fatal(err)
	}
	persistence := source.Storage.ExportPersistence()

	destination, err := NewServices(Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := destination.Storage.ImportPersistence(persistence); err != nil {
		t.Fatal(err)
	}
	info, err := destination.Storage.Stat(
		NamespacePrivate,
		"saves/slot.bin",
	)
	if err != nil {
		t.Fatal(err)
	}
	if info.Modified != destination.Clock.Monotonic() {
		t.Fatalf(
			"imported timestamp = %s, want current runtime time %s",
			info.Modified,
			destination.Clock.Monotonic(),
		)
	}
	if _, err := destination.MarshalBinary(); err != nil {
		t.Fatalf("save after persistence import: %v", err)
	}
}
