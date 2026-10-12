package skvm

import (
	"bytes"
	"reflect"
	"testing"
)

func TestClassReplacementRebuildsReferenceCache(t *testing.T) {
	data := syntheticClass(t)
	vm, err := New(map[string][]byte{"Game": data})
	check(t, err)
	before, err := vm.classes["Game"].class.Reference(11)
	check(t, err)
	if before.Descriptor != "()V" {
		t.Fatal("synthetic reference descriptor changed")
	}
	replacement := bytes.Replace(data, []byte("()V"), []byte("()I"), 1)
	check(t, vm.ReplaceClassData("Game", replacement))
	after, err := vm.classes["Game"].class.Reference(11)
	check(t, err)
	if after.Descriptor != "()I" {
		t.Fatal("replacement class retained its old constant-pool cache")
	}
}

func TestDerivedExecutionCachesDoNotChangeState(t *testing.T) {
	vm, err := New(map[string][]byte{"Game": syntheticClass(t)})
	check(t, err)
	before, err := vm.MarshalBinary()
	check(t, err)
	_, _, err = vm.methodDescriptor("(ILjava/lang/String;)V")
	check(t, err)
	vm.resolveField(Reference{Kind: ReferenceField, Class: "Game", Name: "value", Descriptor: "I"})
	current := vm.borrowFrame(2, 3)
	current.class = vm.classes["Game"].class
	current.method = current.class.Methods[0]
	vm.releaseFrame(current)
	after, err := vm.MarshalBinary()
	check(t, err)
	if !bytes.Equal(after, before) {
		t.Fatal("warming derived execution caches changed the save state")
	}
	check(t, vm.UnmarshalBinary(after))
	if len(vm.methodDescriptors) != 0 || len(vm.fieldOwners) != 0 || len(vm.framePool) != 0 {
		t.Fatal("restored VM retained old derived execution caches")
	}
	restored, err := vm.MarshalBinary()
	check(t, err)
	if !bytes.Equal(restored, before) {
		t.Fatal("discarding derived execution caches changed the restored state")
	}
}

func TestParsedReferenceCachePreservesAllPoolErrors(t *testing.T) {
	class, err := ParseClass("Game.class", syntheticClass(t))
	check(t, err)
	if len(class.references) != 1 {
		t.Fatal("parsed method reference was not cached")
	}
	for index := 0; index <= len(class.pool)+1; index++ {
		got, gotErr := class.Reference(uint16(index))
		want, wantErr := class.resolveReference(uint16(index))
		if got != want || errorText(gotErr) != errorText(wantErr) {
			t.Fatalf("reference %d differs: %v / %v, want %v / %v", index, got, gotErr, want, wantErr)
		}
	}
	// A broken, otherwise unused name-and-type retains validation at access,
	// including after the cache has resolved the remaining valid references.
	class.pool[11].b = 0
	class.referenceIndices, class.references = nil, nil
	class.prepareReferences()
	if _, err := class.Reference(11); errorText(err) != "reference 11 has an invalid name-and-type" {
		t.Fatalf("malformed reference error = %v", err)
	}
}

func errorText(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

func TestMethodDescriptorCacheMatchesParser(t *testing.T) {
	vm := &VM{}
	for _, descriptor := range []string{
		"()V", "(IJDLjava/lang/String;[[B)[Ljava/lang/Object;", "([I)J",
		"", "bad", "(V)V", "(I", "()", "()Vextra", "([V)V", "(L;)I",
	} {
		wantParams, wantResult, wantErr := parseMethodDescriptor(descriptor)
		for repeat := 0; repeat < 3; repeat++ {
			gotParams, gotResult, gotErr := vm.methodDescriptor(descriptor)
			if !reflect.DeepEqual(gotParams, wantParams) || gotResult != wantResult || errorText(gotErr) != errorText(wantErr) {
				t.Fatalf("descriptor %q changed parsed types or error", descriptor)
			}
		}
	}
}

func BenchmarkMethodDescriptor(b *testing.B) {
	const descriptor = "(IIII[BLjava/lang/String;)Z"
	b.Run("parse", func(b *testing.B) {
		b.ReportAllocs()
		for n := 0; n < b.N; n++ {
			_, _, err := parseMethodDescriptor(descriptor)
			if err != nil {
				b.Fatal(err)
			}
		}
	})
	b.Run("cached", func(b *testing.B) {
		vm := &VM{}
		_, _, _ = vm.methodDescriptor(descriptor)
		b.ReportAllocs()
		b.ResetTimer()
		for n := 0; n < b.N; n++ {
			_, _, err := vm.methodDescriptor(descriptor)
			if err != nil {
				b.Fatal(err)
			}
		}
	})
}
