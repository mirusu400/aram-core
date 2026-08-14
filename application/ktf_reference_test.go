package application

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	ktfrt "github.com/mirusu400/aram-core/application/internal/ktf"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	machinecore "github.com/mirusu400/aram-core/core"
	"github.com/mirusu400/aram-core/cpu"
	"github.com/mirusu400/aram-core/cpu/interpreter"
	"github.com/mirusu400/aram-core/loader/ktf"
)

func TestReferenceKTFBootstrap(t *testing.T) {
	root := os.Getenv("ARAM_TEST_DATA")
	if root == "" {
		t.Skip("ARAM_TEST_DATA is not set")
	}
	var packagePath string
	var pkg ktf.Package
	stop := errors.New("package selected")
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() || !strings.EqualFold(filepath.Ext(path), ".zip") {
			return nil
		}
		data, readErr := os.ReadFile(path)
		if readErr != nil {
			return readErr
		}
		parsed, inspectErr := ktf.Inspect(data)
		if inspectErr != nil {
			return nil
		}
		packagePath, pkg = path, parsed
		return stop
	})
	if err != nil && !errors.Is(err, stop) {
		t.Fatal(err)
	}
	if packagePath == "" {
		t.Fatal("ARAM_TEST_DATA contained no valid KTF package")
	}
	if value := os.Getenv("ARAM_TEST_RESOURCE_ALIAS"); value != "" {
		parts := strings.Split(value, ":")
		if len(parts) != 3 {
			t.Fatalf("invalid ARAM_TEST_RESOURCE_ALIAS %q", value)
		}
		source, sourceErr := strconv.ParseUint(parts[1], 0, 16)
		target, targetErr := strconv.ParseUint(parts[2], 0, 16)
		resource, ok := pkg.Resources[parts[0]]
		if !ok || sourceErr != nil || targetErr != nil {
			t.Fatalf(
				"invalid ARAM_TEST_RESOURCE_ALIAS %q: found=%t source_err=%v target_err=%v",
				value,
				ok,
				sourceErr,
				targetErr,
			)
		}
		pkg.Resources[parts[0]] = ktfAliasFirstResourceShard(
			t,
			resource,
			int(source),
			int(target),
		)
	}

	runtime, err := ktfrt.NewRuntime(interpreter.New(), pkg)
	if err != nil {
		t.Fatal(err)
	}
	runtime.DeferThreads = os.Getenv("ARAM_TEST_DEFER_THREADS") != ""
	defer runtime.CPU.Close()
	if err := runtime.MapImageAndHost(); err != nil {
		t.Fatal(err)
	}
	result, pointer, err := runtime.Bootstrap(context.Background())
	if err != nil {
		t.Fatalf("%s: %v (result %+v)", packagePath, err, result)
	}
	if pointer < ktfrt.ImageBase || pointer >= ktfrt.ImageBase+runtime.ImageSz {
		t.Fatalf("%s: bootstrap pointer 0x%08x is outside client image", packagePath, pointer)
	}
	t.Logf("%s: entry returned 0x%08x after %d instructions",
		packagePath, pointer, result.Instructions)
	t.Logf("executable: %+v", runtime.Exe)
	if err := runtime.Initialize(context.Background()); err != nil {
		t.Logf("initialization frontier: %v", err)
		t.Logf(
			"host trace: calls=%d tail=%v",
			len(runtime.HostTrace),
			ktfTraceTail(runtime.HostTrace, 32),
		)
		return
	}
	t.Logf(
		"KTF native initialization completed; host calls=%d tail=%v",
		len(runtime.HostTrace),
		ktfTraceTail(runtime.HostTrace, 32),
	)
	mainClass, err := runtime.LoadClass(context.Background(), pkg.Descriptor.MainClass)
	if err != nil {
		t.Fatalf("load MClass %q: %v", pkg.Descriptor.MainClass, err)
	}
	t.Logf("MClass: address=0x%08x name=%s parent=0x%08x fields=%d methods=%d",
		mainClass.Address,
		mainClass.Name,
		mainClass.Parent,
		mainClass.FieldSize,
		len(mainClass.Methods),
	)
	for _, method := range mainClass.Methods {
		t.Logf("method: %s%s flags=0x%04x body=0x%08x native=0x%08x",
			method.Name,
			method.Descriptor,
			method.AccessFlags,
			method.Body,
			method.NativeBody,
		)
	}
	traceStart := len(runtime.HostTrace)
	if err := runtime.StartMainClass(context.Background()); err != nil {
		t.Logf("MClass execution frontier: %v", err)
		if runtime.LastJavaReturn != 0 {
			words, readErr := runtime.ReadWords(runtime.LastJavaReturn, 2)
			var classErr error
			var header uint32
			var vtable uint32
			if readErr == nil {
				_, classErr = runtime.InspectJavaClass(words[1])
				header, _ = runtime.ReadU32(words[0])
				vtable, _ = runtime.ReadU32(
					runtime.JvmContext + 12 + (header >> 5),
				)
			}
			t.Logf(
				"last Java return 0x%08x words=%08x header=0x%08x "+
					"vtable=0x%08x err=%v class_err=%v",
				runtime.LastJavaReturn,
				words,
				header,
				vtable,
				readErr,
				classErr,
			)
		}
		if caller := runtime.LastJavaCallLR &^ 1; caller >= 128 {
			code := make([]byte, 256)
			if readErr := runtime.CPU.ReadMemory(caller-128, code); readErr == nil {
				t.Logf("last Java call code at 0x%08x: %x", caller-128, code)
			}
			var containingClass string
			var containingMethod ktfrt.JavaMethod
			for className, address := range runtime.JavaClasses {
				class, inspectErr := runtime.InspectJavaClass(address)
				if inspectErr != nil {
					continue
				}
				for _, method := range class.Methods {
					if method.Body <= caller &&
						method.Body > containingMethod.Body {
						containingClass = className
						containingMethod = method
					}
				}
			}
			if containingMethod.Body != 0 {
				methodWords, methodErr := runtime.ReadWords(
					containingMethod.Address,
					7,
				)
				table, tableErr := runtime.ReadWords(
					containingMethod.ExceptionTableRaw,
					int(containingMethod.ExceptionCount),
				)
				t.Logf(
					"last Java caller belongs to %s.%s%s: "+
						"body=0x%08x exceptions=%d table=%08x err=%v",
					containingClass,
					containingMethod.Name,
					containingMethod.Descriptor,
					containingMethod.Body,
					containingMethod.ExceptionCount,
					table,
					tableErr,
				)
				t.Logf(
					"last Java caller method descriptor 0x%08x: "+
						"%08x err=%v",
					containingMethod.Address,
					methodWords,
					methodErr,
				)
				for _, entry := range table {
					words, entryErr := runtime.ReadWords(entry, 6)
					catchName := ""
					if len(words) >= 4 && words[3] != 0 {
						if class, inspectErr := runtime.InspectJavaClass(
							words[3],
						); inspectErr == nil {
							catchName = class.Name
						}
					}
					t.Logf(
						"exception entry 0x%08x: %08x "+
							"catch=%q err=%v",
						entry,
						words,
						catchName,
						entryErr,
					)
				}
			}
		}
		for index := len(runtime.HostTrace) - 1; index >= traceStart; index-- {
			trace := runtime.HostTrace[index]
			marker := strings.LastIndex(trace, "lr=0x")
			if !strings.HasPrefix(trace, "java_jump_2:") || marker < 0 {
				continue
			}
			var caller uint32
			if _, scanErr := fmt.Sscanf(trace[marker:], "lr=0x%x", &caller); scanErr != nil ||
				caller < 256 {
				continue
			}
			caller &^= 1
			code := make([]byte, 384)
			if readErr := runtime.CPU.ReadMemory(caller-256, code); readErr == nil {
				t.Logf("fault caller code at 0x%08x: %x", caller-256, code)
			}
			break
		}
		registers := make([]uint32, cpu.RegisterCPSR+1)
		for register := range registers {
			registers[register], _ = runtime.CPU.ReadRegister(uint32(register))
		}
		t.Logf("frontier registers: %08x", registers)
		ktfLogJavaThrowStack(t, runtime)
		for name, address := range runtime.JavaClasses {
			class, inspectErr := runtime.InspectJavaClass(address)
			if inspectErr != nil || len(class.Methods) == 0 {
				continue
			}
			t.Logf("host class %s: %+v", name, class.Methods)
		}
		t.Logf(
			"new host trace: calls=%d tail=%v",
			len(runtime.HostTrace)-traceStart,
			ktfTraceTail(runtime.HostTrace[traceStart:], 64),
		)
		ktfLogJavaExceptionSummary(t, runtime.HostTrace[traceStart:])
		if value := os.Getenv("ARAM_TEST_FIELD_CACHE"); value != "" {
			cache, parseErr := strconv.ParseUint(value, 0, 32)
			fieldAddress, readErr := runtime.ReadU32(uint32(cache))
			fieldWords, wordsErr := runtime.ReadWords(fieldAddress, 4)
			name := ""
			descriptor := ""
			if wordsErr == nil {
				name, descriptor, _ = runtime.ReadJavaFullName(fieldWords[2])
			}
			t.Logf(
				"KTF field cache 0x%08x -> 0x%08x words=%08x "+
					"name=%s descriptor=%s parse_err=%v read_err=%v "+
					"words_err=%v",
				cache,
				fieldAddress,
				fieldWords,
				name,
				descriptor,
				parseErr,
				readErr,
				wordsErr,
			)
		}
		if value := os.Getenv("ARAM_TEST_METHOD_CACHE"); value != "" {
			cache, parseErr := strconv.ParseUint(value, 0, 32)
			methodAddress, readErr := runtime.ReadU32(uint32(cache))
			method, methodErr := runtime.InspectJavaMethod(methodAddress)
			className := ""
			if methodErr == nil {
				methodWords, _ := runtime.ReadWords(methodAddress, 2)
				if class, classErr := runtime.InspectJavaClass(
					methodWords[1],
				); classErr == nil {
					className = class.Name
				}
			}
			t.Logf(
				"KTF method cache 0x%08x -> 0x%08x method=%s.%s%s "+
					"body=0x%08x parse_err=%v read_err=%v method_err=%v",
				cache,
				methodAddress,
				className,
				method.Name,
				method.Descriptor,
				method.Body,
				parseErr,
				readErr,
				methodErr,
			)
		}
		if value := os.Getenv("ARAM_TEST_MEMORY"); value != "" {
			parts := strings.SplitN(value, ",", 2)
			address, addressErr := strconv.ParseUint(parts[0], 0, 32)
			count := uint64(16)
			var countErr error
			if len(parts) == 2 {
				count, countErr = strconv.ParseUint(parts[1], 0, 16)
			}
			words := []uint32(nil)
			var readErr error
			if addressErr == nil && countErr == nil && count <= 1024 {
				words, readErr = runtime.ReadWords(uint32(address), int(count))
			}
			t.Logf(
				"KTF memory %s words=%08x address_err=%v count_err=%v read_err=%v",
				value,
				words,
				addressErr,
				countErr,
				readErr,
			)
		}
		if value := os.Getenv("ARAM_TEST_CACHE_RANGE"); value != "" {
			parts := strings.SplitN(value, ",", 2)
			start, startErr := strconv.ParseUint(parts[0], 0, 32)
			count := uint64(1)
			var countErr error
			if len(parts) == 2 {
				count, countErr = strconv.ParseUint(parts[1], 0, 16)
			}
			if startErr != nil || countErr != nil || count > 256 {
				t.Logf(
					"invalid KTF cache range %q start_err=%v count_err=%v",
					value,
					startErr,
					countErr,
				)
			} else {
				for index := uint64(0); index < count; index++ {
					cache := uint32(start) + uint32(index)*4
					target, readErr := runtime.ReadU32(cache)
					method, methodErr := runtime.InspectJavaMethod(target)
					if methodErr == nil {
						t.Logf(
							"KTF cache 0x%08x -> method %s%s body=0x%08x",
							cache,
							method.Name,
							method.Descriptor,
							method.Body,
						)
						continue
					}
					words, wordsErr := runtime.ReadWords(target, 4)
					name := ""
					descriptor := ""
					var fieldErr error
					if wordsErr == nil {
						name, descriptor, fieldErr = runtime.ReadJavaFullName(words[2])
					}
					t.Logf(
						"KTF cache 0x%08x -> field %s%s words=%08x "+
							"read_err=%v method_err=%v words_err=%v field_err=%v",
						cache,
						name,
						descriptor,
						words,
						readErr,
						methodErr,
						wordsErr,
						fieldErr,
					)
				}
			}
		}
		if match := os.Getenv("ARAM_TEST_JAVA_STRINGS"); match != "" {
			for address, value := range runtime.JavaStrings {
				if strings.Contains(value, match) {
					t.Logf("KTF Java string 0x%08x=%q", address, value)
				}
			}
		}
		if value := os.Getenv("ARAM_TEST_STATIC_FIELD"); value != "" {
			parts := strings.SplitN(value, "|", 3)
			if len(parts) != 3 {
				t.Logf("invalid ARAM_TEST_STATIC_FIELD %q", value)
			} else {
				classAddress := runtime.JavaClasses[parts[0]]
				class, classErr := runtime.InspectJavaClass(classAddress)
				fieldAddress, fieldErr := runtime.ResolveJavaField(
					class,
					parts[1],
					parts[2],
				)
				fieldWords, wordsErr := runtime.ReadWords(fieldAddress, 4)
				arrayWords := []uint32(nil)
				if wordsErr == nil && strings.HasPrefix(parts[2], "[") &&
					fieldWords[3] != 0 {
					instanceWords, _ := runtime.ReadWords(fieldWords[3], 2)
					length, lengthErr := runtime.ReadU32(instanceWords[0] + 4)
					if lengthErr == nil && length <= 256 {
						arrayWords, _ = runtime.ReadWords(
							instanceWords[0]+8,
							int(length),
						)
					}
				}
				t.Logf(
					"KTF static field %s class=0x%08x field=0x%08x "+
						"words=%08x array=%08x class_err=%v field_err=%v "+
						"words_err=%v",
					value,
					classAddress,
					fieldAddress,
					fieldWords,
					arrayWords,
					classErr,
					fieldErr,
					wordsErr,
				)
			}
		}
		if match := os.Getenv("ARAM_TEST_TRACE_MATCH"); match != "" {
			for traceIndex, trace := range runtime.HostTrace[traceStart:] {
				if !strings.Contains(trace, match) {
					continue
				}
				start := max(0, traceIndex-128)
				end := min(len(runtime.HostTrace[traceStart:]), traceIndex+17)
				window := make([]string, 0, 64)
				for _, nearby := range runtime.HostTrace[traceStart+start : traceStart+end] {
					if strings.HasPrefix(nearby, "java_register_class:") {
						continue
					}
					window = append(window, nearby)
					if len(window) > 64 {
						window = window[1:]
					}
				}
				t.Logf(
					"KTF trace match %q: %v",
					match,
					window,
				)
				break
			}
		}
		if os.Getenv("ARAM_TEST_TRACE_FIELDS") != "" {
			for _, trace := range runtime.HostTrace[traceStart:] {
				if strings.HasPrefix(trace, "java_field:") {
					t.Log(trace)
				}
			}
		}
		if os.Getenv("ARAM_TEST_TRACE_CLASSES") != "" {
			for _, trace := range runtime.HostTrace[traceStart:] {
				if strings.HasPrefix(trace, "java_class_load:") ||
					strings.HasPrefix(trace, "java_register_class:") {
					t.Log(trace)
				}
			}
		}
		if os.Getenv("ARAM_TEST_TRACE_FILES") != "" {
			for _, trace := range runtime.HostTrace[traceStart:] {
				if strings.HasPrefix(trace, "java_file_") {
					t.Log(trace)
				}
			}
		}
		if os.Getenv("ARAM_TEST_TRACE_RESOURCES") != "" {
			for _, trace := range runtime.HostTrace[traceStart:] {
				if strings.HasPrefix(trace, "java_resource:") {
					t.Log(trace)
				}
			}
		}
		return
	}
	if runtime.DeferThreads {
		original, saveErr := runtime.CPU.SaveContext()
		if saveErr != nil {
			t.Fatal(saveErr)
		}
		defer runtime.CPU.RestoreContext(original)
		for index, task := range runtime.Tasks {
			if restoreErr := runtime.CPU.RestoreContext(
				task.Context,
			); restoreErr != nil {
				t.Fatal(restoreErr)
			}
			pc, _ := runtime.CPU.ReadRegister(cpu.RegisterPC)
			sp, _ := runtime.CPU.ReadRegister(cpu.RegisterSP)
			lr, _ := runtime.CPU.ReadRegister(cpu.RegisterLR)
			status, _ := runtime.CPU.ReadRegister(cpu.RegisterCPSR)
			t.Logf(
				"queued task %d: pc=0x%08x sp=0x%08x "+
					"lr=0x%08x cpsr=0x%08x",
				index,
				pc,
				sp,
				lr,
				status,
			)
		}
	}
	t.Logf(
		"MClass startApp completed; new host calls=%d tail=%v",
		len(runtime.HostTrace)-traceStart,
		ktfTraceTail(runtime.HostTrace[traceStart:], 64),
	)
}

func ktfAliasFirstResourceShard(
	t *testing.T,
	resource []byte,
	source int,
	target int,
) []byte {
	t.Helper()
	if len(resource) < 8 {
		t.Fatal("KTF resource alias input is truncated")
	}
	entryCount := int(binary.LittleEndian.Uint16(resource))
	shardCount := int(binary.LittleEndian.Uint16(resource[2:]))
	headerSize := 4 + shardCount*2
	if shardCount == 0 || len(resource) < headerSize {
		t.Fatal("KTF resource alias input has no shard table")
	}
	firstEntry := int(binary.LittleEndian.Uint16(resource[4:]))
	lastEntry := entryCount
	if shardCount > 1 {
		lastEntry = int(binary.LittleEndian.Uint16(resource[6:]))
	}
	if firstEntry != 0 || source < 0 || source >= lastEntry ||
		target < 0 || target >= lastEntry {
		t.Fatalf(
			"KTF resource alias indexes source=%d target=%d are outside [0,%d)",
			source,
			target,
			lastEntry,
		)
	}
	offsetCount := lastEntry + 1
	if len(resource) < headerSize+offsetCount*4 {
		t.Fatal("KTF resource alias offset table is truncated")
	}
	entries := make([][]byte, lastEntry)
	for index := range entries {
		start := int(binary.LittleEndian.Uint32(
			resource[headerSize+index*4:],
		))
		end := int(binary.LittleEndian.Uint32(
			resource[headerSize+(index+1)*4:],
		))
		if start < 0 || start > end || end > len(resource) {
			t.Fatalf(
				"KTF resource alias entry %d has invalid range [%d,%d)",
				index,
				start,
				end,
			)
		}
		entries[index] = append([]byte(nil), resource[start:end]...)
	}
	entries[target] = append([]byte(nil), entries[source]...)
	output := append([]byte(nil), resource[:headerSize]...)
	offset := headerSize + offsetCount*4
	for _, entry := range entries {
		output = binary.LittleEndian.AppendUint32(output, uint32(offset))
		offset += len(entry)
	}
	output = binary.LittleEndian.AppendUint32(output, uint32(offset))
	for _, entry := range entries {
		output = append(output, entry...)
	}
	return output
}

func TestReferenceKTFFactoryRunsQueuedGameThread(t *testing.T) {
	root := os.Getenv("ARAM_TEST_DATA")
	if root == "" {
		t.Skip("ARAM_TEST_DATA is not set")
	}
	var packagePath string
	var packageData []byte
	stop := errors.New("package selected")
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() || !strings.EqualFold(filepath.Ext(path), ".zip") {
			return nil
		}
		data, readErr := os.ReadFile(path)
		if readErr != nil {
			return readErr
		}
		if _, inspectErr := ktf.Inspect(data); inspectErr != nil {
			return nil
		}
		packagePath, packageData = path, data
		return stop
	})
	if err != nil && !errors.Is(err, stop) {
		t.Fatal(err)
	}
	if packagePath == "" {
		t.Fatal("ARAM_TEST_DATA contained no valid KTF package")
	}

	factory := NewFactory()
	factory.RunBudget = 10_000
	slices := 4096
	if os.Getenv("ARAM_TEST_MATCH_RUNNER") != "" {
		factory.RunBudget = DefaultRunBudget
		factory.FrameRunBudget = DefaultHandsetRunBudget
		factory.KTFRunBudget = DefaultKTFHandsetRunBudget
	}
	if configured := os.Getenv("ARAM_TEST_SLICES"); configured != "" {
		value, parseErr := strconv.Atoi(configured)
		if parseErr != nil || value <= 0 {
			t.Fatalf(
				"ARAM_TEST_SLICES %q is not a positive integer",
				configured,
			)
		}
		slices = value
	}
	created, err := factory.Create(context.Background(), machinecore.Source{
		Name:     filepath.Base(packagePath),
		Path:     packagePath,
		Format:   "java-archive",
		ReaderAt: bytes.NewReader(packageData),
		Size:     int64(len(packageData)),
	})
	if err != nil {
		t.Fatal(err)
	}
	defer created.Close()
	machine := created.(*Machine)
	if info := machine.ImageInfo(); info.SourceKind != "ktf-wipi" ||
		info.ProfileID != ktfrt.ProfileID {
		t.Fatalf("KTF image info = %+v", info)
	}
	if err := machine.Start(context.Background()); err != nil {
		t.Logf("KTF start trace tail: %v", ktfTraceTail(machine.ktf.HostTrace, 64))
		ktfLogCPUState(t, machine.ktf)
		t.Logf(
			"KTF Java throw stack at 0x%08x: %08x",
			machine.ktf.LastJavaThrowSP,
			machine.ktf.LastJavaThrowStack,
		)
		seenCallers := make(map[uint32]bool)
		for _, value := range machine.ktf.LastJavaThrowStack {
			caller := value &^ 1
			if caller < ktfrt.ImageBase ||
				caller >= ktfrt.ImageBase+machine.ktf.ImageSz ||
				seenCallers[caller] {
				continue
			}
			seenCallers[caller] = true
			className, method := ktfContainingJavaMethod(machine.ktf, caller)
			if method.Body != 0 {
				t.Logf(
					"Java throw stack caller 0x%08x belongs to %s.%s%s "+
						"(body=0x%08x)",
					caller,
					className,
					method.Name,
					method.Descriptor,
					method.Body,
				)
			}
		}
		if os.Getenv("ARAM_TEST_TRACE_FIELDS") != "" {
			for _, trace := range machine.ktf.HostTrace {
				if strings.HasPrefix(trace, "java_field:") {
					t.Log(trace)
				}
			}
		}
		if caller := ktfLastJavaThrowCaller(machine.ktf.HostTrace); caller != 0 {
			className, method := ktfContainingJavaMethod(machine.ktf, caller)
			t.Logf(
				"last Java throw caller 0x%08x belongs to %s.%s%s "+
					"(body=0x%08x)",
				caller,
				className,
				method.Name,
				method.Descriptor,
				method.Body,
			)
		}
		t.Fatal(err)
	}
	for range slices {
		if machine.ktf.PresentCount != 0 &&
			ktfFrameContainsColor(machine.frame.Pix) {
			break
		}
		if err := machine.StepFrame(context.Background()); err != nil {
			t.Logf("KTF resume trace tail: %v", ktfTraceTail(machine.ktf.HostTrace, 64))
			var wipicTrace, layoutTrace []string
			for _, trace := range machine.ktf.HostTrace {
				if strings.HasPrefix(trace, "wipic") {
					wipicTrace = append(wipicTrace, trace)
				}
				if strings.HasPrefix(trace, "java_card_layout") {
					layoutTrace = append(layoutTrace, trace)
				}
			}
			t.Logf("KTF resume WIPI-C trace: %v", wipicTrace)
			t.Logf("KTF resume card layout trace: %v", layoutTrace)
			ktfLogCPUState(t, machine.ktf)
			ktfLogJavaExceptionSummary(t, machine.ktf.HostTrace)
			t.Fatal(err)
		}
	}
	if machine.State() != machinecore.StatePaused {
		t.Logf(
			"KTF terminal trace: calls=%d tail=%v",
			len(machine.ktf.HostTrace),
			ktfTraceTail(machine.ktf.HostTrace, 128),
		)
		t.Fatalf("KTF machine state = %s, want paused", machine.State())
	}
	if !machine.ktfStarted || len(machine.ktf.Tasks) == 0 {
		t.Fatalf(
			"KTF task state: started=%t tasks=%d",
			machine.ktfStarted,
			len(machine.ktf.Tasks),
		)
	}
	if os.Getenv("ARAM_TEST_TRACE_MATCH") != "" {
		ktfLogCPUState(t, machine.ktf)
	}
	if result := machine.LastResult(); !(result.Reason == cpu.StopBudget && result.Instructions != 0 ||
		result.Reason == cpu.StopExited &&
			machine.ktf.CanAwaitEvents()) {
		t.Fatalf("KTF execution result = %+v", result)
	}
	if machine.ktf.PresentCount == 0 {
		t.Fatal("KTF game thread did not present a frame")
	}
	if !ktfFrameContainsColor(machine.frame.Pix) {
		screenState := machine.ktf.Graphics[machine.ktf.ScreenGraphics]
		t.Logf(
			"KTF black frame diagnostics: tick_ms=%d presents=%d "+
				"tasks=%d screen_graphics=0x%08x screen_target=%t "+
				"graphics=%d unimplemented=%v trace_tail=%v",
			machine.ktf.TickMS,
			machine.ktf.PresentCount,
			len(machine.ktf.Tasks),
			machine.ktf.ScreenGraphics,
			screenState != nil && screenState.Target == machine.frame,
			len(machine.ktf.Graphics),
			machine.WIPIUnimplementedAPIs(),
			ktfTraceTail(machine.ktf.HostTrace, 64),
		)
		var nativeTrace, wipicTrace []string
		for _, trace := range machine.ktf.HostTrace {
			switch {
			case strings.HasPrefix(trace, "java_native_method_correct:"),
				strings.HasPrefix(trace, "java_native_call:"):
				nativeTrace = append(nativeTrace, trace)
			case strings.HasPrefix(trace, "wipic."):
				wipicTrace = append(wipicTrace, trace)
			}
		}
		t.Logf(
			"KTF black frame native corrections=%v WIPI-C tail=%v",
			ktfTraceTail(nativeTrace, 32),
			ktfTraceTail(wipicTrace, 128),
		)
		collectionTrace := make([]string, 0, 32)
		collectionCounts := make(map[string]int)
		gameClassTrace := make([]string, 0, 64)
		for _, trace := range machine.ktf.HostTrace {
			if strings.Contains(trace, "java/util/Vector.") {
				collectionCounts[strings.SplitN(trace, ":", 2)[0]]++
				if len(collectionTrace) < cap(collectionTrace) {
					collectionTrace = append(collectionTrace, trace)
				}
			}
			if strings.HasPrefix(trace, "java.method.b.") &&
				len(gameClassTrace) < cap(gameClassTrace) {
				gameClassTrace = append(gameClassTrace, trace)
			}
		}
		t.Logf(
			"KTF black frame vectors=%v collection_counts=%v "+
				"collection_first=%v game_class_first=%v",
			machine.ktf.Vectors,
			collectionCounts,
			collectionTrace,
			gameClassTrace,
		)
		t.Fatal("KTF game frame remained completely black")
	}
	if os.Getenv("ARAM_TEST_TRACE_UNIMPLEMENTED") != "" {
		t.Logf(
			"KTF unimplemented Java APIs: %v",
			machine.WIPIUnimplementedAPIs(),
		)
	}
	if configured := os.Getenv("ARAM_TEST_POST_FRAME_SLICES"); configured != "" {
		postFrameSlices, parseErr := strconv.Atoi(configured)
		if parseErr != nil || postFrameSlices <= 0 {
			t.Fatalf(
				"ARAM_TEST_POST_FRAME_SLICES %q is not a positive integer",
				configured,
			)
		}
		for index := range postFrameSlices {
			if err := machine.StepFrame(context.Background()); err != nil {
				t.Logf(
					"KTF post-frame failure after %d/%d slices: trace_tail=%v",
					index+1,
					postFrameSlices,
					ktfTraceTail(machine.ktf.HostTrace, 128),
				)
				ktfLogCPUState(t, machine.ktf)
				ktfLogJavaThrowStack(t, machine.ktf)
				ktfLogJavaExceptionSummary(t, machine.ktf.HostTrace)
				t.Fatal(err)
			}
			if machine.State() != machinecore.StatePaused {
				t.Logf(
					"KTF stopped after %d/%d post-frame slices: trace_tail=%v",
					index+1,
					postFrameSlices,
					ktfTraceTail(machine.ktf.HostTrace, 128),
				)
				ktfLogCPUState(t, machine.ktf)
				ktfLogJavaThrowStack(t, machine.ktf)
				ktfLogJavaExceptionSummary(t, machine.ktf.HostTrace)
				t.Fatalf("KTF machine state = %s, want paused", machine.State())
			}
		}
		t.Logf(
			"KTF sustained for %d post-frame slices: tick_ms=%d presents=%d",
			postFrameSlices,
			machine.ktf.TickMS,
			machine.ktf.PresentCount,
		)
	}
	if controls := strings.TrimSpace(os.Getenv("ARAM_TEST_KTF_INPUT")); controls != "" {
		before := append([]byte(nil), machine.frame.Pix...)
		inputTraceStart := len(machine.ktf.HostTrace)
		queued := 0
		for _, control := range strings.Split(controls, ",") {
			control = strings.TrimSpace(control)
			if control == "" {
				continue
			}
			if err := machine.QueueInput(machinecore.InputEvent{
				Control: control,
				Pressed: true,
			}); err != nil {
				t.Fatal(err)
			}
			queued++
			// A physical press remains down while the game task gets a chance
			// to observe state changed by keyNotify. Enqueuing press and release
			// back-to-back can legitimately collapse a polled key to "up".
			for range 64 {
				if err := machine.StepFrame(context.Background()); err != nil {
					t.Logf(
						"KTF input step diagnostics: state=%s display=0x%08x "+
							"card=0x%08x trace_tail=%v",
						machine.State(),
						machine.ktf.DefaultDisplay,
						machine.ktf.DisplayCards[machine.ktf.DefaultDisplay],
						ktfTraceTail(machine.ktf.HostTrace, 64),
					)
					ktfLogCPUState(t, machine.ktf)
					ktfLogJavaThrowStack(t, machine.ktf)
					t.Fatal(err)
				}
			}
			if err := machine.QueueInput(machinecore.InputEvent{
				Control: control,
				Pressed: false,
			}); err != nil {
				t.Fatal(err)
			}
			queued++
			for range 4 {
				if err := machine.StepFrame(context.Background()); err != nil {
					ktfLogCPUState(t, machine.ktf)
					ktfLogJavaThrowStack(t, machine.ktf)
					t.Fatal(err)
				}
			}
		}
		if queued == 0 {
			t.Fatal("ARAM_TEST_KTF_INPUT contained no controls")
		}
		inputSlices := 4096
		if configured := os.Getenv("ARAM_TEST_INPUT_SLICES"); configured != "" {
			value, parseErr := strconv.Atoi(configured)
			if parseErr != nil || value <= 0 {
				t.Fatalf(
					"ARAM_TEST_INPUT_SLICES %q is not a positive integer",
					configured,
				)
			}
			inputSlices = value
		}
		for range inputSlices {
			if err := machine.StepFrame(context.Background()); err != nil {
				t.Logf(
					"KTF input trace tail: %v",
					ktfTraceTail(machine.ktf.HostTrace, 128),
				)
				ktfLogCPUState(t, machine.ktf)
				ktfLogJavaThrowStack(t, machine.ktf)
				t.Fatal(err)
			}
			if os.Getenv("ARAM_TEST_SUSTAIN_INPUT") == "" &&
				len(machine.input) == 0 &&
				!bytes.Equal(before, machine.frame.Pix) {
				break
			}
		}
		delivered := 0
		released := 0
		for _, trace := range machine.ktf.HostTrace {
			if strings.HasPrefix(trace, "java_key_event:") {
				delivered++
			}
			if strings.HasPrefix(
				trace,
				fmt.Sprintf("java_key_event:type=%d:", ktfrt.KeyReleased),
			) {
				released++
			}
		}
		// Holding a control long enough legitimately adds repeat events between
		// its physical press and release. Require both queued transitions while
		// allowing those handset-generated repeats.
		if delivered < queued || released != queued/2 ||
			len(machine.input) != 0 {
			t.Fatalf(
				"KTF input delivery: delivered=%d released=%d "+
					"queued=%d pending=%#v",
				delivered,
				released,
				queued,
				machine.input,
			)
		}
		if bytes.Equal(before, machine.frame.Pix) {
			inputTrace := make([]string, 0, 32)
			for _, trace := range machine.ktf.HostTrace {
				if strings.Contains(trace, "keyNotify") ||
					strings.HasPrefix(trace, "java_key_event:") ||
					strings.HasPrefix(trace, "java_task_slice:index=0:") {
					inputTrace = append(inputTrace, trace)
					if len(inputTrace) > 32 {
						inputTrace = inputTrace[1:]
					}
				}
			}
			t.Logf("KTF input trace: %v", inputTrace)
			inputWindow := make([]string, 0, 128)
			for _, trace := range machine.ktf.HostTrace[inputTraceStart:] {
				if strings.HasPrefix(trace, "java_register_class:") {
					continue
				}
				inputWindow = append(inputWindow, trace)
				if len(inputWindow) == cap(inputWindow) {
					break
				}
			}
			t.Logf("KTF input execution window: %v", inputWindow)
			t.Fatalf(
				"KTF controls %q reached Card.keyNotify but did not change the frame",
				controls,
			)
		}
		t.Logf(
			"KTF controls %q delivered %d events and changed the frame",
			controls,
			delivered,
		)
	}
	if match := os.Getenv("ARAM_TEST_LOG_TRACE"); match != "" {
		matches := make([]string, 0, 128)
		for _, trace := range machine.ktf.HostTrace {
			if strings.Contains(trace, match) {
				matches = append(matches, trace)
				if len(matches) == cap(matches) {
					break
				}
			}
		}
		t.Logf("KTF configured trace %q: %v", match, matches)
	}
}

func ktfFrameContainsColor(pixels []byte) bool {
	for offset := 0; offset+3 < len(pixels); offset += 4 {
		if pixels[offset] != 0 ||
			pixels[offset+1] != 0 ||
			pixels[offset+2] != 0 {
			return true
		}
	}
	return false
}

func ktfTraceTail(trace []string, count int) []string {
	if count < 0 || len(trace) <= count {
		return trace
	}
	return trace[len(trace)-count:]
}

func ktfLogCPUState(t *testing.T, runtime *ktfrt.Runtime) {
	t.Helper()
	registers := make([]uint32, cpu.RegisterR12+1)
	for register := range registers {
		registers[register], _ = runtime.CPU.ReadRegister(uint32(register))
	}
	sp, _ := runtime.CPU.ReadRegister(cpu.RegisterSP)
	lr, _ := runtime.CPU.ReadRegister(cpu.RegisterLR)
	pc, _ := runtime.CPU.ReadRegister(cpu.RegisterPC)
	status, _ := runtime.CPU.ReadRegister(cpu.RegisterCPSR)
	stack, _ := runtime.ReadWords(sp, 64)
	t.Logf(
		"KTF CPU state: r0-r12=%08x r10=0x%08x sp=%08x lr=%08x "+
			"cpsr=%08x stack=%08x",
		registers,
		registers[cpu.RegisterR10],
		sp,
		lr,
		status,
		stack,
	)
	if len(registers) > int(cpu.RegisterR1) && registers[cpu.RegisterR1] != 0 {
		instance := registers[cpu.RegisterR1]
		instanceWords, instanceErr := runtime.ReadWords(instance, 2)
		var receiverClass ktfrt.JavaClass
		var classErr error
		if instanceErr == nil && len(instanceWords) == 2 {
			receiverClass, classErr = runtime.InspectJavaClass(instanceWords[1])
		}
		t.Logf(
			"KTF receiver: instance=0x%08x words=%08x class=%+v "+
				"instance_err=%v class_err=%v",
			instance,
			instanceWords,
			receiverClass,
			instanceErr,
			classErr,
		)
	}
	className, method := ktfContainingJavaMethod(runtime, pc)
	if method.Body != 0 {
		t.Logf(
			"KTF current PC 0x%08x belongs to %s.%s%s (body=0x%08x)",
			pc,
			className,
			method.Name,
			method.Descriptor,
			method.Body,
		)
	}
	if value := os.Getenv("ARAM_TEST_MEMORY"); value != "" {
		parts := strings.SplitN(value, ",", 2)
		address, addressErr := strconv.ParseUint(parts[0], 0, 32)
		count := uint64(16)
		var countErr error
		if len(parts) == 2 {
			count, countErr = strconv.ParseUint(parts[1], 0, 16)
		}
		words := []uint32(nil)
		var readErr error
		if addressErr == nil && countErr == nil && count <= 1024 {
			words, readErr = runtime.ReadWords(uint32(address), int(count))
		}
		t.Logf(
			"KTF configured memory %s words=%08x address_err=%v "+
				"count_err=%v read_err=%v",
			value,
			words,
			addressErr,
			countErr,
			readErr,
		)
	}
	if value := os.Getenv("ARAM_TEST_CLASS"); value != "" {
		address, parseErr := strconv.ParseUint(value, 0, 32)
		class, inspectErr := runtime.InspectJavaClass(uint32(address))
		t.Logf(
			"KTF configured class %s class=%+v parse_err=%v inspect_err=%v",
			value,
			class,
			parseErr,
			inspectErr,
		)
	}
	if value := os.Getenv("ARAM_TEST_CLASS_NAME"); value != "" {
		address := runtime.JavaClasses[value]
		class, inspectErr := runtime.InspectJavaClass(address)
		t.Logf(
			"KTF configured class name %q address=0x%08x class=%+v "+
				"inspect_err=%v",
			value,
			address,
			class,
			inspectErr,
		)
	}
	if value := os.Getenv("ARAM_TEST_TRACE_REGISTER"); value != "" {
		register, parseErr := strconv.ParseUint(value, 0, 8)
		transitions := make([]string, 0, 64)
		previous := ""
		if parseErr == nil && register <= uint64(cpu.RegisterR12) {
			for traceIndex, trace := range runtime.HostTrace {
				if !strings.HasPrefix(trace, "java_method_call:") {
					continue
				}
				marker := strings.LastIndex(trace, ":[")
				if marker < 0 || !strings.HasSuffix(trace, "]") {
					continue
				}
				registers := strings.Fields(trace[marker+2 : len(trace)-1])
				if int(register) >= len(registers) ||
					registers[register] == previous {
					continue
				}
				previous = registers[register]
				transitions = append(
					transitions,
					fmt.Sprintf(
						"%d:r%d=%s:%s",
						traceIndex,
						register,
						previous,
						trace[:marker],
					),
				)
				if len(transitions) > 64 {
					transitions = transitions[1:]
				}
			}
		}
		t.Logf(
			"KTF configured register transitions %s: %v parse_err=%v",
			value,
			transitions,
			parseErr,
		)
	}
	if value := os.Getenv("ARAM_TEST_TRACE_TAIL"); value != "" {
		count, parseErr := strconv.Atoi(value)
		filtered := make([]string, 0, len(runtime.HostTrace))
		if parseErr == nil && count > 0 && count <= 1024 {
			for _, trace := range runtime.HostTrace {
				if strings.HasPrefix(trace, "java_register_class:") {
					continue
				}
				filtered = append(filtered, trace)
			}
			if len(filtered) > count {
				filtered = filtered[len(filtered)-count:]
			}
		}
		t.Logf(
			"KTF configured trace tail %s: %v parse_err=%v",
			value,
			filtered,
			parseErr,
		)
	}
	if match := os.Getenv("ARAM_TEST_TRACE_MATCH"); match != "" {
		matches := make([]string, 0, 64)
		lastAppended := -1
		for traceIndex, trace := range runtime.HostTrace {
			if !strings.Contains(trace, match) {
				continue
			}
			start := max(0, traceIndex-4)
			end := min(len(runtime.HostTrace), traceIndex+5)
			for nearby := start; nearby < end; nearby++ {
				if nearby <= lastAppended {
					continue
				}
				matches = append(
					matches,
					fmt.Sprintf(
						"%d:%s",
						nearby,
						runtime.HostTrace[nearby],
					),
				)
				lastAppended = nearby
				if len(matches) > 64 {
					matches = matches[1:]
				}
			}
		}
		t.Logf("KTF configured trace match %q: %v", match, matches)
	}
}

func ktfLogJavaThrowStack(t *testing.T, runtime *ktfrt.Runtime) {
	t.Helper()
	if runtime.FirstJavaThrowName != "" {
		t.Logf(
			"KTF first Java throw %s registers=%08x stack at 0x%08x: %08x",
			runtime.FirstJavaThrowName,
			runtime.FirstJavaThrowRegisters,
			runtime.FirstJavaThrowSP,
			runtime.FirstJavaThrowStack,
		)
		seenCallers := make(map[uint32]bool)
		for _, value := range runtime.FirstJavaThrowStack {
			caller := value &^ 1
			if caller < ktfrt.ImageBase ||
				caller >= ktfrt.ImageBase+runtime.ImageSz ||
				seenCallers[caller] {
				continue
			}
			seenCallers[caller] = true
			className, method := ktfContainingJavaMethod(runtime, caller)
			if method.Body == 0 {
				continue
			}
			t.Logf(
				"first Java throw stack caller 0x%08x belongs to %s.%s%s "+
					"(body=0x%08x)",
				caller,
				className,
				method.Name,
				method.Descriptor,
				method.Body,
			)
		}
	}
	t.Logf(
		"KTF Java throw %s registers=%08x stack at 0x%08x: %08x",
		runtime.LastJavaThrowName,
		runtime.LastJavaThrowRegisters,
		runtime.LastJavaThrowSP,
		runtime.LastJavaThrowStack,
	)
	if runtime.LastJavaThrowName == "java/lang/ArrayIndexOutOfBoundsException" &&
		len(runtime.LastJavaThrowRegisters) > 5 {
		index := runtime.LastJavaThrowRegisters[4]
		array := runtime.LastJavaThrowRegisters[5]
		instanceWords, instanceErr := runtime.ReadWords(array, 2)
		if instanceErr != nil {
			t.Logf(
				"KTF failing array index=%d instance=0x%08x: %v",
				index,
				array,
				instanceErr,
			)
		} else {
			fields, fieldsErr := runtime.ReadWords(instanceWords[0], 6)
			className := ""
			if class, classErr := runtime.InspectJavaClass(instanceWords[1]); classErr == nil {
				className = class.Name
			}
			t.Logf(
				"KTF failing array index=%d instance=0x%08x words=%08x "+
					"class=%q fields=%08x err=%v",
				index,
				array,
				instanceWords,
				className,
				fields,
				fieldsErr,
			)
			needle := fmt.Sprintf("@0x%08x", array)
			for traceIndex, entry := range runtime.HostTrace {
				if !strings.Contains(entry, needle) {
					continue
				}
				start := max(0, traceIndex-16)
				end := min(len(runtime.HostTrace), traceIndex+17)
				t.Logf(
					"KTF failing array allocation trace: %v",
					runtime.HostTrace[start:end],
				)
				seenAllocationCallers := make(map[uint32]bool)
				for _, nearby := range runtime.HostTrace[start:end] {
					marker := strings.LastIndex(nearby, "lr=0x")
					if marker < 0 {
						continue
					}
					var caller uint32
					if _, scanErr := fmt.Sscanf(
						nearby[marker:],
						"lr=0x%x",
						&caller,
					); scanErr != nil {
						continue
					}
					caller &^= 1
					if seenAllocationCallers[caller] {
						continue
					}
					seenAllocationCallers[caller] = true
					className, method := ktfContainingJavaMethod(runtime, caller)
					if method.Body != 0 {
						t.Logf(
							"KTF allocation caller 0x%08x belongs to %s.%s%s "+
								"(body=0x%08x)",
							caller,
							className,
							method.Name,
							method.Descriptor,
							method.Body,
						)
					}
				}
				break
			}
		}
	}
	seenCallers := make(map[uint32]bool)
	for _, value := range runtime.LastJavaThrowStack {
		caller := value &^ 1
		if caller < ktfrt.ImageBase ||
			caller >= ktfrt.ImageBase+runtime.ImageSz ||
			seenCallers[caller] {
			continue
		}
		seenCallers[caller] = true
		className, method := ktfContainingJavaMethod(runtime, caller)
		if method.Body == 0 {
			continue
		}
		t.Logf(
			"Java throw stack caller 0x%08x belongs to %s.%s%s "+
				"(body=0x%08x)",
			caller,
			className,
			method.Name,
			method.Descriptor,
			method.Body,
		)
	}
}

func ktfLogJavaExceptionSummary(t *testing.T, trace []string) {
	t.Helper()
	counts := make(map[string]int)
	first := -1
	last := -1
	for index, entry := range trace {
		if !strings.HasPrefix(entry, "java_exception_caught:") {
			continue
		}
		counts[entry]++
		if first < 0 {
			first = index
		}
		last = index
	}
	t.Logf("KTF caught Java exceptions: %v", counts)
	logWindow := func(label string, index int) {
		if index < 0 {
			return
		}
		start := index - 16
		if start < 0 {
			start = 0
		}
		end := index + 17
		if end > len(trace) {
			end = len(trace)
		}
		t.Logf("%s Java exception trace: %v", label, trace[start:end])
	}
	logWindow("first", first)
	if last != first {
		logWindow("last", last)
	}
}

func ktfLastJavaThrowCaller(trace []string) uint32 {
	for index := len(trace) - 1; index >= 0; index-- {
		entry := trace[index]
		if !strings.HasPrefix(entry, "java_jump_2:target=0x01200009:") {
			continue
		}
		marker := strings.LastIndex(entry, "lr=0x")
		if marker < 0 {
			continue
		}
		var caller uint32
		if _, err := fmt.Sscanf(entry[marker:], "lr=0x%x", &caller); err == nil {
			return caller &^ 1
		}
	}
	return 0
}

func ktfContainingJavaMethod(
	runtime *ktfrt.Runtime,
	caller uint32,
) (string, ktfrt.JavaMethod) {
	var containingClass string
	var containingMethod ktfrt.JavaMethod
	for className, address := range runtime.JavaClasses {
		class, err := runtime.InspectJavaClass(address)
		if err != nil {
			continue
		}
		for _, method := range class.Methods {
			if method.Body <= caller && method.Body > containingMethod.Body {
				containingClass = className
				containingMethod = method
			}
		}
	}
	return containingClass, containingMethod
}
