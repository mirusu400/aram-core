package raptor

import (
	"context"
	"fmt"
	"testing"

	"github.com/mirusu400/aram-core/cpu"
)

func TestRaptorPrimitiveArrayRankPreservesReferenceStorage(t *testing.T) {
	for _, atype := range []uint32{8, 9, 10} {
		for _, rank := range []uint32{1, 2, 3, 4} {
			t.Run(fmt.Sprintf("type%d/rank%d", atype, rank), func(t *testing.T) {
				public := newPublicRuntime(t)
				runtime := &Runtime{CPU: public.CPU, Public: public,
					resolvedImports: make(map[raptorImportKey]uint64),
					importSlotByKey: make(map[raptorImportKey]uint32)}
				check(t, public.CPU.WriteRegister(cpu.RegisterR0, rank))
				check(t, public.CPU.WriteRegister(cpu.RegisterR1, 0))
				check(t, public.CPU.WriteRegister(cpu.RegisterR2, atype))
				check(t, runtime.dispatchImport(context.Background(), raptorImportKey{Module: 100, Ordinal: 14}))
				check(t, public.CPU.WriteRegister(cpu.RegisterR1, 33))
				check(t, runtime.dispatchImport(context.Background(), raptorImportKey{Module: 100, Ordinal: 16}))
				array, err := public.CPU.ReadRegister(cpu.RegisterR0)
				check(t, err)
				java, err := runtime.ensureJavaRuntime()
				check(t, err)
				mirrorBody, count, width, primitive, ok := java.Host.ArrayShape(java.lgtToKTF[array])
				wantWidth := uint32(4)
				if rank == 1 {
					wantWidth, _ = raptorJavaPrimitiveArrayElementSize(raptorPrimitiveArrayElementChar(atype))
				}
				if !ok || count != 33 || width != wantWidth || primitive != (rank == 1) {
					t.Fatalf("shape = %d/%d/%t/%t, want 33/%d/%t/true", count, width, primitive, ok, wantWidth, rank == 1)
				}
				if rank == 1 {
					return
				}
				// A jagged array stores all child references, including the final
				// slot. Previously these writes overran byte/short-sized bodies
				// and destroyed adjacent child headers and mirror objects.
				children := make([]uint32, 33)
				for i := range children {
					children[i], err = runtime.newRaptorJavaArray('B', 6)
					check(t, err)
					check(t, runtime.storeRaptorJavaArray(array, uint32(i), children[i]))
				}
				body, err := public.ReadU32(array + 8)
				check(t, err)
				for i, child := range children {
					got, err := public.ReadU32(body + 4 + uint32(i)*4)
					check(t, err)
					mirror, err := public.ReadU32(mirrorBody + uint32(i)*4)
					check(t, err)
					if got != child || mirror != java.lgtToKTF[child] {
						t.Fatalf("slot %d lost its child or mirror", i)
					}
					childBody, err := public.ReadU32(child + 8)
					check(t, err)
					length, err := public.ReadU32(childBody)
					check(t, err)
					if length != 6 {
						t.Fatalf("child %d length = %d, want 6", i, length)
					}
				}
				child := children[len(children)-1]
				childBody, err := public.ReadU32(child + 8)
				check(t, err)
				check(t, public.CPU.WriteMemory(childBody+4, []byte("abcdef")))
				receiver, err := runtime.NewRaptorJavaString("")
				check(t, err)
				for i, value := range []uint32{receiver, child, 1, 3} {
					check(t, public.CPU.WriteRegister(uint32(i), value))
				}
				_, err = runtime.callJavaHostMethod(context.Background(), raptorJavaMethod{
					className: "java/lang/String", Name: "<init>", descriptor: "([BII)V"})
				check(t, err)
				check(t, public.CPU.WriteRegister(cpu.RegisterR0, receiver))
				result, err := runtime.callJavaHostMethod(context.Background(), raptorJavaMethod{
					className: "java/lang/String", Name: "length", descriptor: "()I"})
				check(t, err)
				if result.Low != 3 {
					t.Fatalf("String length = %d, want 3", result.Low)
				}
			})
		}
	}
}

func TestRaptorRankedMultiArrayPreservesLeafType(t *testing.T) {
	for _, counts := range [][]uint32{{2, 3}, {2, 3, 4}} {
		public := newPublicRuntime(t)
		runtime := &Runtime{CPU: public.CPU, Public: public,
			resolvedImports: make(map[raptorImportKey]uint64),
			importSlotByKey: make(map[raptorImportKey]uint32)}
		array, err := runtime.buildRaptorJavaMultiArray(raptorRankedPrimitiveArrayType|3<<8|'B', counts)
		check(t, err)
		java, err := runtime.ensureJavaRuntime()
		check(t, err)
		for level, count := range counts {
			_, gotCount, width, primitive, ok := java.Host.ArrayShape(java.lgtToKTF[array])
			wantPrimitive := level == 2
			wantWidth := uint32(4)
			if wantPrimitive {
				wantWidth = 1
			}
			if !ok || gotCount != count || width != wantWidth || primitive != wantPrimitive {
				t.Fatalf("level %d shape = %d/%d/%t/%t", level, gotCount, width, primitive, ok)
			}
			if level+1 < len(counts) {
				body, err := public.ReadU32(array + 8)
				check(t, err)
				array, err = public.ReadU32(body + 4)
				check(t, err)
			}
		}
	}
}
