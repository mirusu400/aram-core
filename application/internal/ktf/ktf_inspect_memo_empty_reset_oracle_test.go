package ktf

// Private prototype oracle. No real inputs, unsafe scanning, timers or benchmarks.
import (
	"context"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/mirusu400/aram-core/cpu"
)

// Literal pre-prototype body. Never delegates to production reset/begin.
func memoEmptyLiteralReset(m *ktfInspectMemo) {
	m.length = 0
	m.next = 0
	m.open = false
	m.addresses = [ktfInspectMemoSize]uint32{}
	m.classes = [ktfInspectMemoSize]JavaClass{}
	m.receiverObject, m.receiverClass, m.receiverValid = 0, 0, false
}

func memoEmptyLiteralBegin(m *ktfInspectMemo) {
	memoEmptyLiteralReset(m)
	m.open = true
}

// The original store is unchanged by the prototype. Keep its independent literal
// here so producer/reference bookkeeping is not inferred from the current writer.
func memoEmptyLiteralStore(m *ktfInspectMemo, address uint32, class JavaClass) {
	if !m.open {
		return
	}
	for index := 0; index < m.length; index++ {
		if m.addresses[index] == address {
			m.classes[index] = class
			return
		}
	}
	if m.length < ktfInspectMemoSize {
		m.addresses[m.length] = address
		m.classes[m.length] = class
		m.length++
		return
	}
	m.addresses[m.next] = address
	m.classes[m.next] = class
	m.next = (m.next + 1) % ktfInspectMemoSize
}

func memoEmptyClass(address uint32, stamp int) JavaClass {
	return JavaClass{
		Address: address, Name: fmt.Sprintf("synthetic-class-%d", stamp),
		Parent: 3, VTable: 4, FieldSize: 5, AccessFlags: 6,
		Methods: []JavaMethod{{Address: 7, DeclaringClass: address,
			Name: fmt.Sprintf("synthetic-method-%d", stamp), Descriptor: "(I)V",
			Body: 8, NativeBody: 9, VTableIndex: 10, AccessFlags: 11,
			ExceptionCount: 12, ExceptionTableRaw: 13}},
	}
}

func memoEmptySeed(length int, receiver bool) ktfInspectMemo {
	m := ktfInspectMemo{length: length, open: true,
		receiverHeader: [8]byte{1, 2, 3, 4, 5, 6, 7, 8}}
	for i := 0; i < length; i++ {
		m.addresses[i] = uint32(i + 1)
		m.classes[i] = memoEmptyClass(uint32(i+1), i)
	}
	if receiver {
		m.receiverObject, m.receiverClass, m.receiverValid = 17, 0x08070605, true
	}
	return m
}

func memoEmptyEqual(t *testing.T, got, want ktfInspectMemo) {
	t.Helper()
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("full memo mismatch\ngot  %+v\nwant %+v", got, want)
	}
}

func memoEmptyCanonical(t *testing.T, m ktfInspectMemo) {
	t.Helper()
	if ktfInspectMemoSize != 8 || m.length < 0 || m.length > 8 ||
		m.next < 0 || m.next >= 8 || m.length < 8 && m.next != 0 {
		t.Fatalf("unexpected canonical bounds: length=%d next=%d", m.length, m.next)
	}
	for i := m.length; i < 8; i++ {
		if m.addresses[i] != 0 || !reflect.DeepEqual(m.classes[i], JavaClass{}) {
			t.Fatalf("inactive slot %d retains state", i)
		}
	}
}

func TestKTFInspectMemoEmptyResetCanonical(t *testing.T) {
	for length := 0; length <= 8; length++ {
		for _, receiver := range []bool{false, true} {
			t.Run(fmt.Sprintf("length%d_receiver%t", length, receiver), func(t *testing.T) {
				// Independent fixtures have distinct method slices, not shared refs.
				got, want := memoEmptySeed(length, receiver), memoEmptySeed(length, receiver)
				memoEmptyCanonical(t, got)
				memoEmptyLiteralReset(&want)
				got.reset()
				memoEmptyEqual(t, got, want)
				memoEmptyCanonical(t, got)
				got.reset()
				memoEmptyLiteralReset(&want)
				memoEmptyEqual(t, got, want)
				got.begin()
				memoEmptyLiteralBegin(&want)
				memoEmptyEqual(t, got, want)
			})
		}
	}
	t.Run("closed-empty-scratch", func(t *testing.T) {
		got, want := memoEmptySeed(0, false), memoEmptySeed(0, false)
		got.open, want.open = false, false
		got.reset()
		memoEmptyLiteralReset(&want)
		memoEmptyEqual(t, got, want)
	})
}

func TestKTFInspectMemoEmptyResetWriterLifecycle(t *testing.T) {
	var got, want ktfInspectMemo
	got.begin()
	memoEmptyLiteralBegin(&want)
	for step := 0; step < 24; step++ {
		address := uint32(step + 1)
		if step == 3 { // Existing-address replacement, without extending length.
			address = 2
		}
		got.store(address, memoEmptyClass(address, step))
		memoEmptyLiteralStore(&want, address, memoEmptyClass(address, step))
		memoEmptyEqual(t, got, want)
		memoEmptyCanonical(t, got)
	}
	// The full ring has wrapped more than once, including next==0..7.
	got.reset()
	memoEmptyLiteralReset(&want)
	memoEmptyEqual(t, got, want)
	memoEmptyCanonical(t, got)
	got.store(31, memoEmptyClass(31, 31))
	memoEmptyLiteralStore(&want, 31, memoEmptyClass(31, 31))
	memoEmptyEqual(t, got, want)
	if _, hit := got.lookup(31); hit {
		t.Fatal("a closed window must not serve a class")
	}
	got.begin()
	memoEmptyLiteralBegin(&want)
	memoEmptyEqual(t, got, want)
}

func TestKTFInspectMemoEmptyResetReceiverOnlyRewrite(t *testing.T) {
	r := newTestRuntime(t)
	object := allocWords(t, r, 2)
	check(t, r.writeWords(object, []uint32{0x11223344, 0x55667788}))
	const shortHeader = uint32(0x0f000000)
	check(t, r.CPU.Map(shortHeader, 4, cpu.PermissionRead|cpu.PermissionWrite))
	check(t, r.CPU.WriteMemory(shortHeader, []byte{9, 10, 11, 12}))
	for _, class := range []uint32{0x55667788, 0x13579bdf} {
		check(t, r.WriteU32(object+4, class))
		r.inspectMemo.begin()
		gotClass, err := r.readHostJavaReceiverClass(object)
		check(t, err)
		if gotClass != class || r.inspectMemo.length != 0 || !r.inspectMemo.receiverValid {
			t.Fatalf("receiver-only capture: class=%x length=%d valid=%t", gotClass, r.inspectMemo.length, r.inspectMemo.receiverValid)
		}
		memoEmptyCanonical(t, r.inspectMemo)
		cachedObject, cachedClass := r.inspectMemo.receiverObject, r.inspectMemo.receiverClass
		if _, err := r.readHostJavaReceiverClass(shortHeader); err == nil {
			t.Fatal("four-byte receiver mapping must not satisfy an eight-byte header read")
		}
		if r.inspectMemo.receiverObject != cachedObject || r.inspectMemo.receiverClass != cachedClass || !r.inspectMemo.receiverValid {
			t.Fatal("a failed different receiver read must not publish a replacement hint")
		}
		before := r.inspectMemo
		want := before
		memoEmptyLiteralReset(&want)
		r.inspectMemo.reset()
		memoEmptyEqual(t, r.inspectMemo, want)
		if r.inspectMemo.receiverHeader != before.receiverHeader {
			t.Fatal("reset must retain the receiver's scratch bytes")
		}
	}
}

func TestKTFInspectMemoEmptyResetGuestRewriteAndReadFault(t *testing.T) {
	r := newTestRuntime(t)
	r.JvmContext = allocWords(t, r, 3+128)
	class := newInspectableJavaClass(t, r, "Original")
	_, err := r.InspectJavaClass(class) // Warm the generation cache first.
	check(t, err)
	r.inspectMemo.begin()
	_, err = r.InspectJavaClass(class)
	check(t, err)
	memoEmptyCanonical(t, r.inspectMemo)
	if r.inspectMemo.length != 1 {
		t.Fatalf("one successful class must be retained, length=%d", r.inspectMemo.length)
	}
	// A later descriptor read faults before store; the earlier class stays live.
	bad := allocWords(t, r, 5)
	check(t, r.writeWords(bad, []uint32{0, 0, 0xfffffff0, 0, 0}))
	beforeFault := r.inspectMemo
	if _, err := r.InspectJavaClass(bad); err == nil {
		t.Fatal("synthetic unmapped descriptor must fault")
	}
	memoEmptyEqual(t, r.inspectMemo, beforeFault)
	memoEmptyCanonical(t, r.inspectMemo)
	// Preserve the old read-only-window behavior, including its intentionally
	// stale answer if a test writes inside the window. Closing must end that.
	descriptor := readU32(t, r, class+8)
	name, err := r.allocateBytes(append([]byte("Rewritten"), 0), true)
	check(t, err)
	check(t, r.WriteU32(descriptor, name))
	if got := inspectClass(t, r, class); got.Name != "Original" {
		t.Fatalf("open memo changed legacy cached answer: %q", got.Name)
	}
	want := r.inspectMemo
	memoEmptyLiteralReset(&want)
	r.inspectMemo.reset()
	memoEmptyEqual(t, r.inspectMemo, want)
	if got := inspectClass(t, r, class); got.Name != "Rewritten" {
		t.Fatalf("closed memo retained a stale answer: %q", got.Name)
	}
}

func TestKTFInspectMemoEmptyResetCleanupAgainstOldLiteral(t *testing.T) {
	for _, scenario := range []string{"long", "int", "body error", "native", "borrowed scope", "commit error", "commit panic", "resolution panic", "capture error", "reentrant commit"} {
		t.Run(scenario, func(t *testing.T) {
			var gotRuntime, wantRuntime *Runtime
			capture := func(factory func(string, string, string) ktfHostHandler, seen **Runtime) func(string, string, string) ktfHostHandler {
				return func(class, name, descriptor string) ktfHostHandler {
					handler := factory(class, name, descriptor)
					return func(ctx context.Context, r *Runtime) (uint32, error) {
						*seen = r
						return handler(ctx, r)
					}
				}
			}
			want := runCleanupKTFCase(t, scenario, capture(memoEmptyOldHostJavaMethod, &wantRuntime))
			got := runCleanupKTFCase(t, scenario, capture(HostJavaMethod, &gotRuntime))
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("cleanup/public outcome mismatch\ngot %+v\nwant %+v", got, want)
			}
			if gotRuntime == nil || wantRuntime == nil {
				t.Fatal("both literal and current handlers must enter")
			}
			memoEmptyEqual(t, gotRuntime.inspectMemo, wantRuntime.inspectMemo)
			memoEmptyCanonical(t, gotRuntime.inspectMemo)
		})
	}
}

// Frozen pre-extraction cleanup factory from ktf_java_cleanup_test.go; only its
// name and three explicit memo begin/reset calls use the old-literal helpers.
func memoEmptyOldHostJavaMethod(className, name, descriptor string) ktfHostHandler {
	sampleDetailedTrace := isKTFHighFrequencyJavaMethod(className, name, descriptor)
	var detailedTraceCalls uint64
	declaredClassName := className
	declaresHostMethod := ktfHostSpecDeclaresJavaMethod(className, name, descriptor)
	argumentWords := uint32(4)
	if parameters, ok := ktfJavaParameterWords(descriptor); ok {
		argumentWords = uint32(min(int(cpu.RegisterR12+1), max(4, parameters+2)))
	}
	return func(ctx context.Context, runtime *Runtime) (value uint32, returnedErr error) {
		className := declaredClassName
		scope, ownedScope, err := runtime.acquireHostCallScope(argumentWords)
		if err != nil {
			return 0, err
		}
		if ownedScope {
			defer runtime.popHostCallScope(scope)
		}
		runtime.JavaReturnHigh = 0
		defer func() {
			if returnedErr != nil || !strings.HasSuffix(descriptor, ")J") || runtime.NativeParameterBase != 0 {
				return
			}
			var commit cpu.RegisterCommit
			_ = commit.Set(cpu.RegisterR1, runtime.JavaReturnHigh)
			if err := cpu.CommitHostCallRegisters(runtime.CPU, commit); err != nil {
				value = 0
				returnedErr = err
			}
		}()
		registers := scope.arguments[:cpu.RegisterR12+1]
		declaredClass := className
		memoEmptyLiteralBegin(&runtime.inspectMemo)
		defer memoEmptyLiteralReset(&runtime.inspectMemo)
		exactHostReceiver := false
		if declaresHostMethod && !strings.HasPrefix(name, "<") && registers[1] != 0 {
			declaredAddress := runtime.JavaClasses[className]
			if runtime.hostJavaClass[declaredAddress] {
				receiverClass, receiverErr := runtime.readHostJavaReceiverClass(registers[1])
				exactHostReceiver = receiverErr == nil && receiverClass == declaredAddress
			}
		}
		if !exactHostReceiver {
			className = runtime.correctHostJavaReceiverClass(className, name, descriptor, registers)
		}
		if className != declaredClass {
			runtime.tracef("java_host_receiver_correct:%s.%s%s->%s", declaredClass, name, descriptor, className)
		}
		runtime.LastJavaCallLR = scope.frame.Registers[cpu.RegisterLR]
		if runtime.traceMode == KTFTraceFull {
			if sampleDetailedTrace {
				detailedTraceCalls++
				if detailedTraceCalls == 1 || detailedTraceCalls%HostTraceSampleInterval == 0 {
					runtime.traceJavaMethodCall(className, name, descriptor, runtime.LastJavaCallLR, registers)
				} else {
					runtime.omitTrace()
				}
			} else {
				runtime.traceJavaMethodCall(className, name, descriptor, runtime.LastJavaCallLR, registers)
			}
		}
		if !exactHostReceiver {
			if value, ok, err := runtime.redispatchGuestJavaMethod(ctx, className, name, descriptor, registers); ok || err != nil {
				return value, err
			}
		}
		memoEmptyLiteralReset(&runtime.inspectMemo)
		return dispatchKTFHostJavaMethod(ctx, runtime, className, name, descriptor, registers)
	}
}
