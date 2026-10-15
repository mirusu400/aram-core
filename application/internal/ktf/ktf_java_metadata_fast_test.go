package ktf

import (
	"bytes"
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/mirusu400/aram-core/application/internal/guest"
	"github.com/mirusu400/aram-core/cpu"
)

type metadataHookBackend struct {
	cpu.Backend
	afterRead                   func(uint32, []byte) error
	failWrite                   uint32
	writeError                  error
	class, descriptor           uint32
	classReads, descriptorReads int
}

func (b *metadataHookBackend) ReadMemory(address uint32, destination []byte) error {
	if address == b.class {
		b.classReads++
	}
	if address == b.descriptor {
		b.descriptorReads++
	}
	if err := b.Backend.ReadMemory(address, destination); err != nil {
		return err
	}
	if b.afterRead != nil {
		return b.afterRead(address, destination)
	}
	return nil
}

func (b *metadataHookBackend) WriteMemory(address uint32, source []byte) error {
	if b.writeError != nil && address == b.failWrite {
		return b.writeError
	}
	return b.Backend.WriteMemory(address, source)
}

func TestKTFInspectionScratchReentrantReadsAndUnwind(t *testing.T) {
	for _, failure := range []string{"none", "inner-error", "outer-error", "panic"} {
		t.Run(failure, func(t *testing.T) {
			r := newTestRuntime(t)
			outer := allocWords(t, r, 9)
			inner := allocWords(t, r, 7)
			wantOuter := []uint32{11, 22, 33, 44, 55, 66, 77, 88, 99}
			wantInner := []uint32{101, 202, 303, 404, 505, 606, 707}
			check(t, r.writeWords(outer, wantOuter))
			check(t, r.writeWords(inner, wantInner))
			readError := errors.New("injected metadata read failure")
			spy := &metadataHookBackend{Backend: r.CPU}
			r.CPU = spy
			spy.afterRead = func(address uint32, data []byte) error {
				if !r.javaInspectionScratchBusy {
					t.Fatal("scratch was released before backend read/decode completed")
				}
				if address == inner {
					if &data[0] == &r.javaInspectionScratch[0] {
						t.Fatal("reentrant read reused the outer scratch")
					}
					if failure == "inner-error" {
						return readError
					}
					return nil
				}
				if address != outer {
					return nil
				}
				if &data[0] != &r.javaInspectionScratch[0] {
					t.Fatal("outer read did not use the runtime scratch")
				}
				var words [7]uint32
				err := r.readInspectionWords(inner, words[:])
				if failure == "inner-error" {
					if !errors.Is(err, readError) {
						t.Fatalf("inner read error = %v", err)
					}
				} else {
					check(t, err)
					if !reflect.DeepEqual(words[:], wantInner) {
						t.Fatalf("inner words = %v, want %v", words, wantInner)
					}
				}
				if !r.javaInspectionScratchBusy {
					t.Fatal("inner unwind released the outer scratch")
				}
				switch failure {
				case "outer-error":
					return readError
				case "panic":
					panic(readError)
				}
				return nil
			}
			var got [9]uint32
			func() {
				if failure == "panic" {
					defer func() {
						if recovered := recover(); recovered != readError {
							t.Fatalf("recovered = %v, want injected panic", recovered)
						}
					}()
				}
				err := r.readInspectionWords(outer, got[:])
				if failure == "outer-error" {
					if !errors.Is(err, readError) {
						t.Fatalf("outer read error = %v", err)
					}
					return
				}
				check(t, err)
				if failure == "panic" {
					t.Fatal("backend panic was swallowed")
				}
				if !reflect.DeepEqual(got[:], wantOuter) {
					t.Fatalf("outer words = %v, want %v", got, wantOuter)
				}
			}()
			if r.javaInspectionScratchBusy {
				t.Fatal("scratch remained busy after outer unwind")
			}
			spy.afterRead = nil
			check(t, r.readInspectionWords(outer, got[:]))
			if !reflect.DeepEqual(got[:], wantOuter) || r.javaInspectionScratchBusy {
				t.Fatal("subsequent read did not recover exact words and scratch ownership")
			}
		})
	}
}

func TestKTFInspectionWordsWarmReadDoesNotAllocate(t *testing.T) {
	r := newTestRuntime(t)
	address := allocWords(t, r, 9)
	var words [9]uint32
	if allocations := testing.AllocsPerRun(100, func() {
		if err := r.readInspectionWords(address, words[:]); err != nil {
			t.Fatal(err)
		}
	}); allocations != 0 {
		t.Fatalf("warm inspection read allocated %g objects, want zero", allocations)
	}
}

func installMetadataTestSpec(t *testing.T, name string, spec ktfHostJavaClassSpec) {
	t.Helper()
	previous, existed := HostJavaClassSpecs[name]
	HostJavaClassSpecs[name] = spec
	t.Cleanup(func() {
		if existed {
			HostJavaClassSpecs[name] = previous
		} else {
			delete(HostJavaClassSpecs, name)
		}
	})
}

// Append a guest declaration directly, without running augmentation or rebuilding
// a vtable. This also changes the descriptor headers so existing inspection
// caches must notice the relink on the next call.
func appendMetadataTestMethod(t *testing.T, r *Runtime, class uint32,
	name, descriptor string, body, nativeBody uint32,
) uint32 {
	t.Helper()
	signature, err := r.allocateBytes(append([]byte{0}, []byte(descriptor+"+"+name)...), true)
	check(t, err)
	method := allocWords(t, r, 7)
	check(t, r.writeWords(method, []uint32{body, class, nativeBody, signature, 0, 0x00080000, 0}))
	classDescriptor := readU32(t, r, class+8)
	table := readU32(t, r, classDescriptor+12)
	countAndFields := readU32(t, r, classDescriptor+24)
	count := uint32(uint16(countAndFields))
	methods := readWords(t, r, table, int(count))
	methods = append(methods, method)
	newTable := allocWords(t, r, count+1)
	check(t, r.writeWords(newTable, methods))
	check(t, r.WriteU32(classDescriptor+12, newTable))
	check(t, r.WriteU32(classDescriptor+24, countAndFields&0xffff0000|(count+1)))
	return method
}

// Preserve the original augmentation algorithm as an independent oracle. It has
// no completion fast path, and deliberately performs every reverse lookup.
func referenceMetadataAugment(r *Runtime, classAddress uint32, name string) error {
	spec, ok := HostJavaClassSpecs[name]
	if !ok {
		return nil
	}
	class, err := r.InspectJavaClass(classAddress)
	if err != nil {
		return err
	}
	r.hostJavaClass[classAddress] = true
	for _, wanted := range spec.methods {
		if (spec.compatibilityVTable || wanted.compatibility) &&
			wanted.access&(0x0002|0x0008) == 0 && !strings.HasPrefix(wanted.name, "<") {
			continue
		}
		var method JavaMethod
		found := false
		for index := len(class.Methods) - 1; index >= 0; index-- {
			candidate := class.Methods[index]
			if candidate.Name == wanted.name && candidate.Descriptor == wanted.descriptor {
				method, found = candidate, true
				break
			}
		}
		if found && (method.Body != 0 || method.NativeBody != 0) {
			_, bodyIsHost := r.hostCalls[method.Body&^1]
			if name != "java/util/Enumeration" || bodyIsHost {
				continue
			}
		}
		if _, err := r.addHostJavaMethod(class, wanted.name, wanted.descriptor); err != nil {
			return err
		}
		class, err = r.InspectJavaClass(classAddress)
		if err != nil {
			return err
		}
	}
	return nil
}

func TestKTFHostClassCompletionMatchesOriginalAugmentation(t *testing.T) {
	for _, mutation := range []string{
		"complete", "reordered", "bodyless-last-duplicate", "native-only",
		"spec-add", "spec-reorder", "spec-access-defer", "spec-compatibility-defer",
		"enumeration-guest-body", "enumeration-native-only", "write-error",
	} {
		t.Run(mutation, func(t *testing.T) {
			name := "test/MetadataComplete"
			if strings.HasPrefix(mutation, "enumeration-") {
				name = "java/util/Enumeration"
			}
			spec := ktfHostJavaClassSpec{methods: []ktfHostJavaMethodSpec{
				{name: "first", descriptor: "()V", access: 0x0008},
				{name: "second", descriptor: "()I", access: 0x0008},
			}}
			installMetadataTestSpec(t, name, spec)
			var runtimes [2]*Runtime
			var addresses [2]uint32
			writeError := errors.New("injected metadata write failure")
			for index := range runtimes {
				r := newTestRuntime(t)
				r.JvmContext = allocWords(t, r, 3+128)
				address := ensureClass(t, r, name)
				class := inspectClass(t, r, address)
				descriptor := readU32(t, r, address+8)
				switch mutation {
				case "reordered":
					table := readU32(t, r, descriptor+12)
					check(t, r.writeWords(table, []uint32{class.Methods[1].Address, class.Methods[0].Address}))
					check(t, r.WriteU32(descriptor+28, readU32(t, r, descriptor+28)^0x10))
				case "bodyless-last-duplicate", "write-error":
					appendMetadataTestMethod(t, r, address, "first", "()V", 0, 0)
				case "native-only", "enumeration-native-only":
					check(t, r.WriteU32(class.Methods[0].Address, 0))
					check(t, r.WriteU32(descriptor+28, readU32(t, r, descriptor+28)^0x10))
				case "enumeration-guest-body":
					check(t, r.WriteU32(class.Methods[0].Address, ImageBase|1))
					check(t, r.WriteU32(class.Methods[0].Address+8, 0))
					check(t, r.WriteU32(descriptor+28, readU32(t, r, descriptor+28)^0x10))
				}
				if mutation == "write-error" {
					r.CPU = &metadataHookBackend{Backend: r.CPU, failWrite: descriptor + 12, writeError: writeError}
				}
				runtimes[index], addresses[index] = r, address
			}
			if addresses[0] != addresses[1] {
				t.Fatal("oracle fixtures allocated different class addresses")
			}
			switch mutation {
			case "spec-add":
				spec.methods = append(spec.methods, ktfHostJavaMethodSpec{name: "third", descriptor: "()J", access: 0x0008})
			case "spec-reorder":
				spec.methods[0], spec.methods[1] = spec.methods[1], spec.methods[0]
			case "spec-access-defer":
				spec.methods[0].access, spec.methods[0].compatibility = 0, true
			case "spec-compatibility-defer":
				spec.methods[0].access, spec.compatibilityVTable = 0, true
			}
			HostJavaClassSpecs[name] = spec
			originalErr := referenceMetadataAugment(runtimes[0], addresses[0], name)
			_, optimizedErr := runtimes[1].EnsureJavaClass(name)
			if errorText(originalErr) != errorText(optimizedErr) {
				t.Fatalf("errors differ: original=%v optimized=%v", originalErr, optimizedErr)
			}
			if mutation == "write-error" && !errors.Is(optimizedErr, writeError) {
				t.Fatal("augmentation did not preserve the injected write error")
			}
			assertMetadataRuntimesEqual(t, runtimes[0], runtimes[1], addresses[0])
		})
	}
}

func errorText(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

func assertMetadataRuntimesEqual(t *testing.T, original, optimized *Runtime, address uint32) {
	t.Helper()
	if !reflect.DeepEqual(original.JavaClasses, optimized.JavaClasses) ||
		!reflect.DeepEqual(original.hostJavaClass, optimized.hostJavaClass) ||
		original.javaClassGeneration != optimized.javaClassGeneration ||
		len(original.hostCalls) != len(optimized.hostCalls) ||
		original.nextHostCall != optimized.nextHostCall ||
		!reflect.DeepEqual(original.Heap.Allocations, optimized.Heap.Allocations) {
		t.Fatal("runtime class/host-call/heap bookkeeping differs from the original path")
	}
	left, leftErr := original.InspectJavaClass(address)
	right, rightErr := optimized.InspectJavaClass(address)
	if errorText(leftErr) != errorText(rightErr) || !reflect.DeepEqual(left, right) {
		t.Fatalf("class metadata differs: original=%+v (%v), optimized=%+v (%v)", left, leftErr, right, rightErr)
	}
}

func TestKTFEnsureClassReusesInspectionButReadsLiveHeaders(t *testing.T) {
	r := newTestRuntime(t)
	r.JvmContext = allocWords(t, r, 3+128)
	address := ensureClass(t, r, "java/lang/String")
	descriptor := readU32(t, r, address+8)
	_ = inspectClass(t, r, address)
	spy := &metadataHookBackend{Backend: r.CPU, class: address, descriptor: descriptor}
	r.CPU = spy
	for iteration := 0; iteration < 2; iteration++ {
		spy.classReads, spy.descriptorReads = 0, 0
		if got := ensureClass(t, r, "java/lang/String"); got != address {
			t.Fatal("complete class was rebuilt")
		}
		if spy.classReads != 1 || spy.descriptorReads != 1 {
			t.Fatalf("ensure reads class=%d descriptor=%d, want one of each", spy.classReads, spy.descriptorReads)
		}
	}
	// A cached name whose heap slot has been reclaimed must still rebuild.
	check(t, r.WriteU32(address+8, 0xfffffff0))
	if got := ensureClass(t, r, "java/lang/String"); got == address {
		t.Fatal("ensure reused a reclaimed class slot")
	}
}

// The pre-optimization registration path is intentionally kept independent of
// the preloaded-header helper, including its original ReadWords error contexts.
func referenceMetadataRegister(ctx context.Context, r *Runtime) (uint32, error) {
	class, err := r.parameter(0)
	if err != nil {
		return 0, err
	}
	words, err := r.ReadWords(class, 5)
	if err != nil {
		return 0, err
	}
	descriptor, err := r.ReadWords(words[2], 9)
	if err != nil {
		return 0, err
	}
	name, err := r.readCString(descriptor[0], 1024)
	if err != nil {
		return 0, err
	}
	r.rememberRegisteredJavaClass(name, class)
	inspected, err := r.InspectJavaClass(class)
	if err != nil {
		return 0, err
	}
	if r.hostJavaClass[class] {
		if err := r.implementBodylessPlatformMethods(inspected); err != nil {
			return 0, err
		}
		inspected, err = r.InspectJavaClass(class)
		if err != nil {
			return 0, err
		}
	}
	if inspected.VTable != 0 {
		r.javaVTableClasses[inspected.VTable] = inspected.Address
	}
	r.trace(r.javaRegisterClassTrace(name, class, inspected))
	if err := r.ensureJavaClassInitialized(ctx, inspected); err != nil {
		r.tracef("java_class_initialization_deferred:%s:%v", inspected.Name, err)
	}
	return 0, nil
}

func TestKTFRegisterPreloadedHeadersPreserveOriginalErrorsAndState(t *testing.T) {
	for _, fault := range []string{"none", "class-header", "descriptor-header", "null-name", "method-count", "method-name", "platform-patch"} {
		t.Run(fault, func(t *testing.T) {
			var runtimes [2]*Runtime
			var addresses [2]uint32
			name := "test/MetadataRegister"
			if fault == "method-count" || fault == "platform-patch" {
				name = "java/test/MetadataRegister"
			}
			for index := range runtimes {
				r := newTestRuntime(t)
				r.JvmContext = allocWords(t, r, 3+128)
				address := newInspectableJavaClass(t, r, name)
				descriptor := readU32(t, r, address+8)
				switch fault {
				case "class-header":
					address = 0xfffffff0
				case "descriptor-header":
					check(t, r.WriteU32(address+8, 0xfffffff0))
				case "null-name":
					check(t, r.WriteU32(descriptor, 0))
				case "method-count":
					check(t, r.WriteU32(descriptor+24, 4097))
				case "method-name", "platform-patch":
					method := appendMetadataTestMethod(t, r, address, "method", "()V", 0, 0)
					if fault == "method-name" {
						badName, err := r.allocateBytes([]byte{0, 'b', 'a', 'd'}, true)
						check(t, err)
						check(t, r.WriteU32(method+12, badName))
					}
				}
				check(t, r.CPU.WriteRegister(cpu.RegisterR0, address))
				runtimes[index], addresses[index] = r, address
			}
			originalValue, originalErr := referenceMetadataRegister(context.Background(), runtimes[0])
			optimizedValue, optimizedErr := ktfRegisterJavaClass(context.Background(), runtimes[1])
			if originalValue != optimizedValue || errorText(originalErr) != errorText(optimizedErr) {
				t.Fatalf("registration differs: original=%08x (%v), optimized=%08x (%v)", originalValue, originalErr, optimizedValue, optimizedErr)
			}
			if (fault == "none" || fault == "platform-patch") != (optimizedErr == nil) {
				t.Fatalf("unexpected registration error: %v", optimizedErr)
			}
			if fault == "method-count" || fault == "method-name" {
				if got := runtimes[1].JavaClasses[name]; got != addresses[1] {
					t.Fatal("method inspection failure lost the already-registered name")
				}
			}
			if fault == "method-count" && !runtimes[1].hostJavaClass[addresses[1]] {
				t.Fatal("failed platform registration lost its already-set host class flag")
			}
			assertMetadataRuntimesEqual(t, runtimes[0], runtimes[1], addresses[0])
			if !reflect.DeepEqual(runtimes[0].javaClassInit, runtimes[1].javaClassInit) ||
				!reflect.DeepEqual(runtimes[0].javaVTableClasses, runtimes[1].javaVTableClasses) ||
				!reflect.DeepEqual(runtimes[0].HostTrace, runtimes[1].HostTrace) {
				t.Fatal("registration initialization/vtable/trace state differs")
			}
			if runtimes[1].javaInspectionScratchBusy {
				t.Fatal("registration failure leaked scratch ownership")
			}
		})
	}
}

func TestKTFRegisterReadsFreshNameAndDoesNotRereadHeaders(t *testing.T) {
	r := newTestRuntime(t)
	r.JvmContext = allocWords(t, r, 3+128)
	address := newInspectableJavaClass(t, r, "test/RegisterOne")
	descriptor := readU32(t, r, address+8)
	nameAddress := readU32(t, r, descriptor)
	check(t, r.CPU.WriteRegister(cpu.RegisterR0, address))
	spy := &metadataHookBackend{Backend: r.CPU, class: address, descriptor: descriptor}
	r.CPU = spy
	for iteration := 0; iteration < 2; iteration++ {
		spy.classReads, spy.descriptorReads = 0, 0
		_, err := ktfRegisterJavaClass(context.Background(), r)
		check(t, err)
		if spy.classReads != 1 || spy.descriptorReads != 1 {
			t.Fatalf("register reads class=%d descriptor=%d, want one of each", spy.classReads, spy.descriptorReads)
		}
	}
	// This changes only the CString bytes, not its pointer or either header.
	check(t, r.CPU.WriteMemory(nameAddress, append([]byte("test/RegisterTwo"), 0)))
	_, err := ktfRegisterJavaClass(context.Background(), r)
	check(t, err)
	if r.JavaClasses["test/RegisterOne"] != address || r.JavaClasses["test/RegisterTwo"] != address {
		t.Fatal("registration did not preserve the old alias and freshly read new name")
	}
	if got := inspectClass(t, r, address).Name; got != "test/RegisterTwo" {
		t.Fatalf("registered name = %q", got)
	}
}

func TestKTFMetadataFastPathsRevalidateAfterRestore(t *testing.T) {
	r := newTestRuntime(t)
	r.JvmContext = allocWords(t, r, 3+128)
	name := "test/MetadataRestore"
	spec := ktfHostJavaClassSpec{methods: []ktfHostJavaMethodSpec{{name: "original", descriptor: "()V", access: 0x0008}}}
	installMetadataTestSpec(t, name, spec)
	address := ensureClass(t, r, name)
	_ = ensureClass(t, r, name)
	var buffer bytes.Buffer
	check(t, WriteState(r, r.CPU, true, guest.NewStateWriter(&buffer)))
	decoder := guest.StateDecoder{Reader: bytes.NewReader(buffer.Bytes())}
	saved, err := ParseState(r, &decoder)
	check(t, err)
	appendMetadataTestMethod(t, r, address, "transient", "()I", ImageBase|1, 0)
	if got := len(inspectClass(t, r, address).Methods); got != 2 {
		t.Fatalf("transient class has %d methods, want two", got)
	}
	started := false
	check(t, RestoreState(r, r.CPU, saved, &started))
	// The host spec is current process configuration, not serialized guest state.
	// A restored class must see a spec change despite both paths having been warm.
	spec.methods = append(spec.methods, ktfHostJavaMethodSpec{name: "afterRestore", descriptor: "()J", access: 0x0008})
	HostJavaClassSpecs[name] = spec
	if got := ensureClass(t, r, name); got != address {
		t.Fatal("restored class address changed unexpectedly")
	}
	class := inspectClass(t, r, address)
	if len(class.Methods) != 2 || class.Methods[0].Name != "original" || class.Methods[1].Name != "afterRestore" {
		t.Fatalf("restored/re-augmented methods = %+v", class.Methods)
	}
	check(t, r.CPU.WriteRegister(cpu.RegisterR0, address))
	_, err = ktfRegisterJavaClass(context.Background(), r)
	check(t, err)
	if got := inspectClass(t, r, address); !reflect.DeepEqual(got, class) {
		t.Fatal("registration reused pre-restore class metadata")
	}
}
