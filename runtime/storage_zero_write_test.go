package runtime

import (
	"fmt"
	"reflect"
	"testing"
	"time"
)

func TestStorageZeroLengthWritePreservesFileAndPosition(t *testing.T) {
	for _, mode := range []OpenMode{OpenRead | OpenWrite, OpenRead | OpenWrite | OpenAppend} {
		for _, position := range []int64{0, 10} {
			t.Run(fmt.Sprintf("mode_%d_position_%d", mode, position), func(t *testing.T) {
				_, clock, storage := newTestStorage(t)
				check(t, storage.WriteFile(NamespacePrivate, "save.dat", []byte{1, 2, 3}))
				handle, err := storage.Open(1, NamespacePrivate, "save.dat", mode)
				check(t, err)
				_, err = storage.Seek(1, handle, position, SeekStart)
				check(t, err)
				check(t, clock.Advance(time.Second))
				before := storage.Snapshot()
				count, err := storage.Write(1, handle, nil)
				check(t, err)
				if count != 0 || !reflect.DeepEqual(storage.Snapshot(), before) {
					t.Fatalf("zero-byte write changed storage: count=%d", count)
				}
			})
		}
	}
}
