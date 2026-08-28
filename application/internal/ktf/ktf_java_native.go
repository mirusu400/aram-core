package ktf

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
	"strings"
	"time"

	"github.com/mirusu400/aram-core/application/internal/guest"
	"github.com/mirusu400/aram-core/cpu"
)

func (r *Runtime) parameter(index uint32) (uint32, error) {
	if r.hostCallDepth != 0 {
		scope := &r.hostCallScopes[r.hostCallDepth-1]
		if err := r.ensureHostCallArguments(scope, index+1); err != nil {
			return 0, err
		}
		return scope.arguments[index], nil
	}
	if r.NativeParameterBase != 0 {
		if index == 0 {
			return 0, nil
		}
		address := r.NativeParameterBase + (index-1)*4
		if err := r.CPU.ReadMemory(address, r.parameterScratch[:]); err != nil {
			return 0, fmt.Errorf("read KTF word at 0x%08x: %w", address, err)
		}
		return binary.LittleEndian.Uint32(r.parameterScratch[:]), nil
	}
	if index < 4 {
		return r.CPU.ReadRegister(cpu.RegisterR0 + index)
	}
	stack, err := r.CPU.ReadRegister(cpu.RegisterSP)
	if err != nil {
		return 0, err
	}
	if err := r.CPU.ReadMemory(
		stack+(index-4)*4,
		r.parameterScratch[:],
	); err != nil {
		return 0, err
	}
	return binary.LittleEndian.Uint32(r.parameterScratch[:]), nil
}

func (r *Runtime) pushHostCallScope(argumentWords uint32) (*ktfHostCallScope, error) {
	if argumentWords > cpu.MaxHostCallWords ||
		r.hostCallDepth >= len(r.hostCallScopes) {
		return nil, errors.New("KTF host-call nesting limit reached")
	}
	scope := &r.hostCallScopes[r.hostCallDepth]
	clear(scope.arguments[:])
	scope.argumentWords = 0
	scope.native = r.NativeParameterBase != 0
	scope.parameterBase = r.NativeParameterBase
	if err := r.ensureHostCallArguments(scope, argumentWords); err != nil {
		// The legacy scalar pre-capture ignored unread optional arguments and
		// only surfaced an address fault if the selected handler actually used
		// that parameter. Preserve that lazy-error behavior while still bulk
		// capturing every valid call frame.
		minimum := min(argumentWords, uint32(4))
		if scope.native {
			minimum = min(argumentWords, uint32(1))
		}
		if minimum == argumentWords {
			return nil, err
		}
		if err := r.ensureHostCallArguments(scope, minimum); err != nil {
			return nil, err
		}
	}
	r.hostCallDepth++
	return scope, nil
}

func (r *Runtime) ensureHostCallArguments(
	scope *ktfHostCallScope,
	argumentWords uint32,
) error {
	if argumentWords <= scope.argumentWords {
		return nil
	}
	if argumentWords > cpu.MaxHostCallWords {
		return errors.New("KTF host-call argument capacity exceeded")
	}
	request := cpu.HostCallFrameRequest{}
	if scope.native {
		request.ParameterAddress = scope.parameterBase
		if argumentWords > 1 {
			request.ParameterWords = argumentWords - 1
		}
	} else if argumentWords > 4 {
		request.StackWords = argumentWords - 4
	}
	if err := cpu.CaptureHostCallFrame(r.CPU, &scope.frame, request); err != nil {
		return err
	}
	for index := uint32(0); index < argumentWords; index++ {
		switch {
		case scope.native && index == 0:
			scope.arguments[index] = 0
		case scope.native:
			scope.arguments[index] = scope.frame.Parameters[index-1]
		case index < 4:
			scope.arguments[index] = scope.frame.Registers[index]
		default:
			scope.arguments[index] = scope.frame.Stack[index-4]
		}
	}
	scope.argumentWords = argumentWords
	return nil
}

func (r *Runtime) acquireHostCallScope(
	argumentWords uint32,
) (*ktfHostCallScope, bool, error) {
	if r.hostCallDepth != 0 {
		scope := &r.hostCallScopes[r.hostCallDepth-1]
		// This is speculative argument capture for receiver correction and full
		// trace. A selected handler still gets a precise lazy error through
		// parameter if an optional stack word is genuinely unavailable.
		_ = r.ensureHostCallArguments(scope, argumentWords)
		return scope, false, nil
	}
	scope, err := r.pushHostCallScope(argumentWords)
	return scope, true, err
}

func (r *Runtime) invokeHostHandler(
	ctx context.Context,
	host ktfHostCall,
) (uint32, error) {
	scope, err := r.pushHostCallScope(4)
	if err != nil {
		return 0, err
	}
	value, callErr := host.handler(ctx, r)
	r.popHostCallScope(scope)
	return value, callErr
}

func (r *Runtime) popHostCallScope(scope *ktfHostCallScope) {
	if r.hostCallDepth == 0 || scope != &r.hostCallScopes[r.hostCallDepth-1] {
		panic("KTF host-call scope stack is corrupt")
	}
	r.hostCallDepth--
}

func ktfGetInterface(_ context.Context, runtime *Runtime) (uint32, error) {
	nameAddress, err := runtime.parameter(0)
	if err != nil {
		return 0, err
	}
	name, err := runtime.readCString(nameAddress, 256)
	if err != nil {
		return 0, err
	}
	runtime.trace("interface:" + name)
	return runtime.lookupInterface(name)
}

func (r *Runtime) lookupInterface(name string) (uint32, error) {
	switch name {
	case "WIPIC_knlInterface":
		return r.buildKnlInterface()
	case "WIPI_JBInterface":
		return r.buildJBInterface()
	case "MXUserMemInterf":
		return r.buildMXUserMemInterface()
	default:
		return 0, nil
	}
}

func (r *Runtime) buildMXUserMemInterface() (uint32, error) {
	if r.mxUserMemInterface != 0 {
		return r.mxUserMemInterface, nil
	}
	slots := []uint32{
		r.RegisterHostCall("mxusermem.add", ktfIncrementalMemoryAdd),
		r.RegisterHostCall("mxusermem.alloc", ktfIncrementalMemoryAllocate),
		r.RegisterHostCall("mxusermem.realloc", ktfIncrementalMemoryReallocate),
		r.RegisterHostCall("mxusermem.free", ktfIncrementalMemoryFree),
	}
	address, err := r.AllocateWords(uint32(len(slots)))
	if err != nil {
		return 0, err
	}
	if err := r.writeWords(address, slots); err != nil {
		return 0, err
	}
	r.mxUserMemInterface = address
	return address, nil
}

func ktfCallNative(ctx context.Context, runtime *Runtime) (uint32, error) {
	address, err := runtime.parameter(0)
	if err != nil {
		return 0, err
	}
	parameters, err := runtime.parameter(1)
	if err != nil {
		return 0, err
	}
	if signature, ok := runtime.javaNativeMethodSignature(address); ok {
		if runtime.LastJavaMethod != signature {
			runtime.tracef(
				"java_native_method_correct:%s->%s@0x%08x",
				runtime.LastJavaMethod,
				signature,
				address,
			)
		}
		runtime.LastJavaMethod = signature
	}
	runtime.tracef(
		"java_native_call:%s@0x%08x",
		runtime.LastJavaMethod,
		address,
	)
	override, hasOverride := ktfJavaNativeOverride(runtime.LastJavaMethod)
	if address == 0 && !hasOverride {
		if runtime.LastJavaMethod != "" {
			return 0, fmt.Errorf(
				"KTF Java native method target is null for %s",
				runtime.LastJavaMethod,
			)
		}
		return 0, errors.New("KTF Java native method target is null")
	}
	if parameters == 0 {
		return 0, errors.New("KTF Java native parameter container is null")
	}
	var value uint32
	host, hostMethod := runtime.hostCalls[address&^1]
	if hasOverride && !hostMethod {
		host = override
		hostMethod = true
	}
	if hostMethod {
		runtime.TraceHostCall(host.name)
		nativeParameterBase := runtime.NativeParameterBase
		runtime.NativeParameterBase = parameters
		value, err = runtime.invokeHostHandler(ctx, host)
		runtime.NativeParameterBase = nativeParameterBase
		if err != nil {
			nativeParameters, _ := runtime.ReadWords(parameters, 10)
			return 0, fmt.Errorf(
				"call Java host method 0x%08x with parameters %08x: %w",
				address,
				nativeParameters,
				err,
			)
		}
	} else {
		environment := uint32(0)
		if runtime.javaEnvironment != 0 {
			environment, _ = runtime.ReadU32(runtime.javaEnvironment)
			if environment == 0 {
				runtime.tracef(
					"java_native_environment_null:%s",
					runtime.LastJavaMethod,
				)
			}
		}
		if environment != 0 {
			// Clear the environment's native return slot so a value the
			// native deposits there can be told apart from a stale one.
			if err := runtime.writeWords(
				environment+0x24,
				[]uint32{0, 0},
			); err != nil {
				return 0, err
			}
		}
		result, resultValue, callErr := runtime.call(
			ctx,
			address,
			[]uint32{parameters, parameters},
			ktfBootstrapInstructionMax,
		)
		if callErr != nil {
			method := runtime.LastJavaMethod
			if method == "" {
				method = "<unknown>"
			}
			nativeParameters, _ := runtime.ReadWords(parameters, 10)
			return 0, fmt.Errorf(
				"call Java native method %s at 0x%08x, PC 0x%08x after %d instructions "+
					"with parameters %08x: %w",
				method,
				address,
				result.PC,
				result.Instructions,
				nativeParameters,
				callErr,
			)
		}
		value = resultValue
		if environment != 0 {
			// KTF natives compiled from the SDK return values through the
			// execution environment: the value kind goes to env+0x24 and the
			// value itself to env+0x28. R0 at return is scratch - dnff's
			// calcClet leaves a structure offset there - so prefer the
			// deposited value whenever the native produced one (issue #45).
			state, stateErr := runtime.ReadWords(environment+0x24, 2)
			if stateErr == nil && state[0] != 0 {
				value = state[1]
			}
		}
	}
	high := uint32(0)
	if strings.HasSuffix(runtime.LastJavaMethod, ")J") {
		high = runtime.JavaReturnHigh
	}
	if err := runtime.writeWords(parameters, []uint32{value, high}); err != nil {
		return 0, err
	}
	return parameters, nil
}

// javaNativeMethodSignature recovers the method identity from the native
// procedure carried by the AOT call trampoline. KTF caches resolved method
// descriptors, so a cached native invocation does not necessarily pass
// through ktfGetJavaMethod first. lastJavaMethod can consequently describe an
// unrelated earlier lookup; using it for framework overrides would dispatch
// the native call to the wrong implementation.
// ktfNativeSignatureMatches records every Java signature whose native body
// resolves to one guest address, alongside the methods that produced them so a
// cached answer can be revalidated without another full scan.
type ktfNativeSignatureMatches struct {
	methods    []uint32
	signatures []string
}

func (r *Runtime) javaNativeMethodSignature(address uint32) (string, bool) {
	target := address &^ 1
	if target == 0 {
		return "", false
	}
	matches := r.nativeSignatureMatches(target)
	// A single native body can back several signatures. The most recently
	// dispatched method wins so the caller keeps resolving the method it is
	// already executing; otherwise only an unambiguous match resolves.
	for _, signature := range matches.signatures {
		if signature == r.LastJavaMethod {
			return signature, true
		}
	}
	if len(matches.signatures) == 1 {
		return matches.signatures[0], true
	}
	return "", false
}

// nativeSignatureMatches resolves target against every loaded class, caching
// the result. Rescanning per native call meant re-reading each class name,
// method name, and descriptor out of guest memory, which dominated KTF
// dispatch. The cache is dropped whenever the class set changes, and a cached
// entry is revalidated against the method words it came from so a guest that
// relinks a method in place cannot be served a stale signature.
func (r *Runtime) nativeSignatureMatches(
	target uint32,
) *ktfNativeSignatureMatches {
	if r.nativeSignatures == nil ||
		r.nativeSignatureGen != r.javaClassGeneration {
		r.nativeSignatures = make(map[uint32]*ktfNativeSignatureMatches)
		r.nativeSignatureGen = r.javaClassGeneration
	}
	if cached, ok := r.nativeSignatures[target]; ok &&
		r.nativeSignatureMatchesValid(target, cached) {
		return cached
	}
	matches := &ktfNativeSignatureMatches{}
	seenClasses := make(map[uint32]bool)
	seenSignatures := make(map[string]bool)
	for _, classAddress := range r.JavaClasses {
		if classAddress == 0 || seenClasses[classAddress] {
			continue
		}
		seenClasses[classAddress] = true
		class, err := r.InspectJavaClass(classAddress)
		if err != nil {
			continue
		}
		for _, method := range class.Methods {
			// The guest relinks resolved native pointers into the method
			// structure in place, which the class inspection cache does not
			// observe. Match against the live word, not the cached one.
			nativeBody, nativeErr := r.ReadU32(method.Address + 8)
			if nativeErr != nil ||
				nativeBody == 0 ||
				nativeBody&^1 != target {
				continue
			}
			declaring, inspectErr := r.InspectJavaClass(method.DeclaringClass)
			if inspectErr != nil {
				continue
			}
			signature := declaring.Name + "." + method.Name + method.Descriptor
			if seenSignatures[signature] {
				continue
			}
			seenSignatures[signature] = true
			matches.methods = append(matches.methods, method.Address)
			matches.signatures = append(matches.signatures, signature)
		}
	}
	r.nativeSignatures[target] = matches
	return matches
}

// nativeSignatureMatchesValid re-reads the native body of every method behind
// a cached entry. This is one small guest read per match instead of walking
// every loaded class.
func (r *Runtime) nativeSignatureMatchesValid(
	target uint32,
	matches *ktfNativeSignatureMatches,
) bool {
	for _, methodAddress := range matches.methods {
		words, err := r.ReadWords(methodAddress, 3)
		if err != nil || words[2] == 0 || words[2]&^1 != target {
			return false
		}
	}
	return true
}

func ktfJavaNativeOverride(signature string) (ktfHostCall, bool) {
	const graphicsPrefix = "org/kwis/msp/lcdui/Graphics."
	if strings.HasPrefix(signature, graphicsPrefix) {
		method := strings.TrimPrefix(signature, graphicsPrefix)
		separator := strings.IndexByte(method, '(')
		if separator < 0 {
			return ktfHostCall{}, false
		}
		return ktfHostCall{
			name: "java.native_override." + signature,
			handler: HostJavaMethod(
				"org/kwis/msp/lcdui/Graphics",
				method[:separator],
				method[separator:],
			),
		}, true
	}
	switch signature {
	case "java/lang/Object.wait(J)V",
		"java/lang/Object.wait(JI)V",
		"java/lang/Object.wait()V":
		method := strings.TrimPrefix(signature, "java/lang/Object.")
		separator := strings.IndexByte(method, '(')
		return ktfHostCall{
			name: "java.native_override." + signature,
			handler: HostJavaMethod(
				"java/lang/Object",
				method[:separator],
				method[separator:],
			),
		}, true
	case "java/lang/System.currentTimeMillis()J":
		return ktfHostCall{
			name: "java.native_override." + signature,
			handler: HostJavaMethod(
				"java/lang/System",
				"currentTimeMillis",
				"()J",
			),
		}, true
	case "java/lang/System.arraycopy(Ljava/lang/Object;ILjava/lang/Object;II)V":
		return ktfHostCall{
			name: "java.native_override." + signature,
			handler: HostJavaMethod(
				"java/lang/System",
				"arraycopy",
				"(Ljava/lang/Object;ILjava/lang/Object;II)V",
			),
		}, true
	case "java/lang/Thread.sleep(J)V", "java/lang/Thread.yield()V":
		method := strings.TrimPrefix(signature, "java/lang/Thread.")
		separator := strings.IndexByte(method, '(')
		return ktfHostCall{
			name: "java.native_override." + signature,
			handler: HostJavaMethod(
				"java/lang/Thread",
				method[:separator],
				method[separator:],
			),
		}, true
	case "java/lang/Runtime.getRuntime()Ljava/lang/Runtime;",
		"java/lang/Runtime.freeMemory()J",
		"java/lang/Runtime.totalMemory()J",
		"java/lang/Runtime.gc()V",
		"java/lang/Runtime.exit(I)V":
		// Runtime is an SDK-native class: the guest leaves these method bodies
		// unlinked, so without a host override getRuntime() faults with a null
		// target the moment a title probes free/total memory (issue #15). The
		// host bodies live in the HostJavaMethod dispatch alongside System.
		method := strings.TrimPrefix(signature, "java/lang/Runtime.")
		separator := strings.IndexByte(method, '(')
		return ktfHostCall{
			name: "java.native_override." + signature,
			handler: HostJavaMethod(
				"java/lang/Runtime",
				method[:separator],
				method[separator:],
			),
		}, true
	case "java/lang/String.valueOf([C)Ljava/lang/String;",
		"java/lang/String.valueOf([CII)Ljava/lang/String;":
		method := strings.TrimPrefix(signature, "java/lang/String.")
		separator := strings.IndexByte(method, '(')
		return ktfHostCall{
			name: "java.native_override." + signature,
			handler: HostJavaMethod(
				"java/lang/String",
				method[:separator],
				method[separator:],
			),
		}, true
	case "org/kwis/msp/lcdui/Display.addJletEventListener(Lorg/kwis/msp/lcdui/JletEventListener;)V":
		return ktfHostCall{
			name: "java.native_override." + signature,
			handler: HostJavaMethod(
				"org/kwis/msp/lcdui/Display",
				"addJletEventListener",
				"(Lorg/kwis/msp/lcdui/JletEventListener;)V",
			),
		}, true
	case "org/kwis/msp/lcdui/Display.getKeyCode(I)I":
		return ktfHostCall{
			name: "java.native_override." + signature,
			handler: HostJavaMethod(
				"org/kwis/msp/lcdui/Display",
				"getKeyCode",
				"(I)I",
			),
		}, true
	case "org/kwis/msp/lcdui/Display.getGameAction(I)I":
		return ktfHostCall{
			name: "java.native_override." + signature,
			handler: HostJavaMethod(
				"org/kwis/msp/lcdui/Display",
				"getGameAction",
				"(I)I",
			),
		}, true
	case "org/kwis/msp/lcdui/Display.getKeyName(I)Ljava/lang/String;":
		return ktfHostCall{
			name: "java.native_override." + signature,
			handler: HostJavaMethod(
				"org/kwis/msp/lcdui/Display",
				"getKeyName",
				"(I)Ljava/lang/String;",
			),
		}, true
	case "org/kwis/msp/lwc/Component.getX()I",
		"org/kwis/msp/lwc/Component.getY()I",
		"org/kwis/msp/lwc/Component.getWidth()I",
		"org/kwis/msp/lwc/Component.getHeight()I",
		"org/kwis/msp/lwc/Component.getXOnScreen()I",
		"org/kwis/msp/lwc/Component.getYOnScreen()I",
		"org/kwis/msp/lwc/Component.getPreferredWidth()I",
		"org/kwis/msp/lwc/Component.getPreferredHeight()I",
		"org/kwis/msp/lwc/Component.getPreferredHeight(I)I",
		"org/kwis/msp/lwc/Component.getBackground()I",
		"org/kwis/msp/lwc/Component.getForeground()I",
		"org/kwis/msp/lwc/Component.hasFocus()Z",
		"org/kwis/msp/lwc/Component.isShown()Z",
		"org/kwis/msp/lwc/Component.isValid()Z":
		method := strings.TrimPrefix(
			signature,
			"org/kwis/msp/lwc/Component.",
		)
		separator := strings.IndexByte(method, '(')
		return ktfHostCall{
			name: "java.native_override." + signature,
			handler: HostJavaMethod(
				"org/kwis/msp/lwc/Component",
				method[:separator],
				method[separator:],
			),
		}, true
	case "org/kwis/msp/media/Volume.get()I":
		return ktfHostCall{
			name: "java.native_override." + signature,
			handler: func(_ context.Context, runtime *Runtime) (uint32, error) {
				return uint32(
					runtime.Services.Media.Snapshot().GlobalVolume / 20,
				), nil
			},
		}, true
	case "org/kwis/msf/io/Network.connect()I":
		return ktfHostCall{
			name: "java.native_override." + signature,
			handler: func(_ context.Context, runtime *Runtime) (uint32, error) {
				runtime.Services.Device.SetNetworkAvailable(true)
				return 1, nil
			},
		}, true
	case "org/kwis/msf/io/Network.disconnect()V":
		return ktfHostCall{
			name: "java.native_override." + signature,
			handler: func(_ context.Context, runtime *Runtime) (uint32, error) {
				runtime.Services.Device.SetNetworkAvailable(false)
				return 0, nil
			},
		}, true
	case "org/kwis/msp/media/Vibrator.on(II)V":
		return ktfHostCall{
			name: "java.native_override." + signature,
			handler: func(_ context.Context, runtime *Runtime) (uint32, error) {
				level, err := runtime.parameter(0)
				if err != nil {
					return 0, err
				}
				millis, err := runtime.parameter(1)
				if err != nil {
					return 0, err
				}
				return 0, runtime.Services.Device.Vibrate(
					uint8(min(level, uint32(100))),
					time.Duration(millis)*time.Millisecond,
					runtime.Services.Clock.Monotonic(),
				)
			},
		}, true
	default:
		return ktfHostJavaSpecOverride(signature)
	}
}

// ktfHostJavaSpecOverride resolves any Java method ARAM implements itself.
// The cases above name individual methods; this is the general rule behind
// them. A guest class table with no native body for such a method reaches
// Clet.callNative with a null address, and without a target that is a fault
// even though the runtime has the method — 부루마불2007 faults that way on
// StringBuffer.append(C) partway through a game (issue #81).
//
// Only a method the host class specs actually declare is resolved, walking the
// spec's own parent chain the way a virtual call does. A guest class that
// happens to share a name with a host one is not in the specs, so its methods
// still fault rather than silently running host code.
func ktfHostJavaSpecOverride(signature string) (ktfHostCall, bool) {
	open := strings.IndexByte(signature, '(')
	if open < 0 {
		return ktfHostCall{}, false
	}
	separator := strings.LastIndexByte(signature[:open], '.')
	if separator < 0 {
		return ktfHostCall{}, false
	}
	className := signature[:separator]
	name := signature[separator+1 : open]
	descriptor := signature[open:]
	for depth := 0; className != "" && depth < 32; depth++ {
		spec, known := HostJavaClassSpecs[className]
		if !known {
			return ktfHostCall{}, false
		}
		for _, method := range spec.methods {
			if method.name != name || method.descriptor != descriptor {
				continue
			}
			return ktfHostCall{
				name: "java.native_override." + signature,
				handler: HostJavaMethod(
					signature[:separator],
					name,
					descriptor,
				),
			}, true
		}
		className = spec.Parent
	}
	return ktfHostCall{}, false
}

func HostJavaMethod(className, name, descriptor string) ktfHostHandler {
	sampleDetailedTrace := isKTFHighFrequencyJavaMethod(
		className,
		name,
		descriptor,
	)
	var detailedTraceCalls uint64
	return func(
		ctx context.Context,
		runtime *Runtime,
	) (value uint32, returnedErr error) {
		argumentWords := uint32(4)
		if parameters, ok := ktfJavaParameterWords(descriptor); ok {
			argumentWords = uint32(min(
				int(cpu.RegisterR12+1),
				max(4, parameters+2),
			))
		}
		scope, ownedScope, err := runtime.acquireHostCallScope(argumentWords)
		if err != nil {
			return 0, err
		}
		if ownedScope {
			defer runtime.popHostCallScope(scope)
		}
		runtime.JavaReturnHigh = 0
		defer func() {
			if returnedErr != nil ||
				!strings.HasSuffix(descriptor, ")J") ||
				runtime.NativeParameterBase != 0 {
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
		className = runtime.correctHostJavaReceiverClass(
			className,
			name,
			descriptor,
			registers,
		)
		if className != declaredClass {
			runtime.tracef(
				"java_host_receiver_correct:%s.%s%s->%s",
				declaredClass,
				name,
				descriptor,
				className,
			)
		}
		runtime.LastJavaCallLR = scope.frame.Registers[cpu.RegisterLR]
		if runtime.traceMode == KTFTraceFull {
			if sampleDetailedTrace {
				detailedTraceCalls++
				if detailedTraceCalls == 1 ||
					detailedTraceCalls%HostTraceSampleInterval == 0 {
					runtime.traceJavaMethodCall(
						className,
						name,
						descriptor,
						runtime.LastJavaCallLR,
						registers,
					)
				} else {
					runtime.omitTrace()
				}
			} else {
				runtime.traceJavaMethodCall(
					className,
					name,
					descriptor,
					runtime.LastJavaCallLR,
					registers,
				)
			}
		}
		// A guest class can override a host-declared virtual method. The
		// receiver's implementation must win over the host model, or the
		// host silently swallows the call (observed: BaseCanvas.performed
		// resolving to the LWC no-op, killing the game's timer loop).
		if value, ok, err := runtime.redispatchGuestJavaMethod(
			ctx,
			className,
			name,
			descriptor,
			registers,
		); ok || err != nil {
			return value, err
		}
		switch className {
		case "java/lang/Object":
			switch name + descriptor {
			case "<init>()V", "notify()V", "notifyAll()V", "finalize()V":
				return 0, nil
			case "wait(J)V", "wait(JI)V", "wait()V":
				if runtime.DeferThreads {
					runtime.yieldRequested = true
				}
				return 0, nil
			case "getClass()Ljava/lang/Class;":
				if registers[1] == 0 {
					return 0, nil
				}
				classAddress, err := runtime.ReadU32(registers[1] + 4)
				if err != nil {
					return 0, err
				}
				return runtime.javaClassObject(classAddress)
			case "equals(Ljava/lang/Object;)Z":
				if registers[1] == registers[2] {
					return 1, nil
				}
				return 0, nil
			case "hashCode()I":
				return registers[1], nil
			case "clone()Ljava/lang/Object;":
				return registers[1], nil
			case "toString()Ljava/lang/String;":
				return runtime.NewJavaString(runtime.javaObjectString(registers[1]))
			}
		case "java/lang/Class":
			return runtime.handleClassMethod(ctx, name, descriptor)
		case "java/io/InputStream", "java/io/ByteArrayInputStream",
			"java/io/DataInputStream":
			return runtime.handleInputStreamMethod(ctx, name, descriptor)
		case "java/io/Reader", "java/io/InputStreamReader":
			return runtime.handleInputStreamReaderMethod(name, descriptor)
		case "java/io/ByteArrayOutputStream":
			return runtime.handleByteArrayOutputStreamMethod(name, descriptor)
		case "java/io/OutputStream", "java/io/DataOutputStream":
			return runtime.handleOutputStreamMethod(name, descriptor)
		case "java/io/PrintStream":
			return 0, nil
		case "java/lang/String":
			return runtime.handleStringMethod(name, descriptor)
		case "java/lang/StringBuffer":
			return runtime.handleStringBufferMethod(name, descriptor)
		case "java/lang/Integer":
			return runtime.handleIntegerMethod(name, descriptor)
		case "java/lang/Long":
			return runtime.handleLongMethod(name, descriptor)
		case "java/lang/Byte":
			return runtime.handleByteMethod(name, descriptor)
		case "java/lang/Boolean":
			return runtime.handleBooleanMethod(name, descriptor)
		case "java/lang/Character":
			return runtime.handleCharacterMethod(name, descriptor)
		case "java/lang/Short":
			return runtime.handleShortMethod(name, descriptor)
		case "java/lang/Float":
			return runtime.handleFloatMethod(name, descriptor)
		case "java/lang/Double":
			return runtime.handleDoubleMethod(name, descriptor)
		case "java/io/Writer", "java/io/OutputStreamWriter":
			return runtime.handleOutputStreamWriterMethod(name, descriptor)
		case "java/lang/Math":
			return runtime.handleMathMethod(name, descriptor)
		case "java/lang/Thread":
			return runtime.handleThreadMethod(ctx, name, descriptor)
		case "java/lang/System":
			switch name + descriptor {
			case "arraycopy(Ljava/lang/Object;ILjava/lang/Object;II)V":
				return 0, runtime.javaArrayCopy(
					registers[1],
					registers[2],
					registers[3],
					registers[4],
					registers[5],
				)
			case "currentTimeMillis()J":
				return runtime.javaLongResult(
					runtime.TickMS,
				), nil
			case "gc()V":
				return 0, nil
			case "getProperty(Ljava/lang/String;)Ljava/lang/String;":
				return runtime.NewJavaString("")
			case "exit(I)V":
				runtime.requestJavaTermination(0)
				return 0, nil
			case "identityHashCode(Ljava/lang/Object;)I":
				return registers[1], nil
			}
		case "java/lang/Runtime":
			switch name + descriptor {
			case "getRuntime()Ljava/lang/Runtime;":
				return runtime.ensureJavaRuntime()
			case "freeMemory()J":
				return runtime.javaLongResult(uint64(guest.HeapSize / 2)), nil
			case "totalMemory()J":
				return runtime.javaLongResult(uint64(guest.HeapSize)), nil
			case "gc()V", "exit(I)V":
				return 0, nil
			}
		case "java/util/Calendar", "java/util/GregorianCalendar":
			return runtime.handleCalendarMethod(name, descriptor)
		case "java/util/Random":
			return runtime.handleRandomMethod(name, descriptor)
		case "java/util/Date":
			return runtime.handleDateMethod(name, descriptor)
		case "java/util/Vector", "java/util/Stack":
			return runtime.handleVectorMethod(name, descriptor)
		case "java/util/Hashtable":
			return runtime.handleHashtableMethod(name, descriptor)
		case "java/util/Enumeration":
			return runtime.handleEnumerationMethod(name, descriptor)
		case "java/util/Timer", "java/util/TimerTask":
			return runtime.handleTimerMethod(ctx, name, descriptor)
		case "java/util/TimeZone", "java/util/SimpleTimeZone":
			return runtime.handleTimeZoneMethod(name, descriptor)
		case "org/kwis/msp/lcdui/Card", "org/kwis/msp/lwc/ProxyCard":
			switch name + descriptor {
			case "<init>()V", "<init>(I)V", "<init>(Z)V":
				if err := runtime.initializeCard(registers[1], 0); err != nil {
					return 0, err
				}
				return 0, nil
			case "<init>(Lorg/kwis/msp/lcdui/Display;)V":
				if err := runtime.initializeCard(
					registers[1],
					registers[2],
				); err != nil {
					return 0, err
				}
				return 0, nil
			case "getDisplay()Lorg/kwis/msp/lcdui/Display;":
				return runtime.readJavaFieldWord(registers[1], 4)
			case "getWidth()I":
				return runtime.readJavaFieldWord(registers[1], 16)
			case "getHeight()I":
				return runtime.readJavaFieldWord(registers[1], 20)
			case "getX()I":
				return runtime.readJavaFieldWord(registers[1], 8)
			case "getY()I":
				return runtime.readJavaFieldWord(registers[1], 12)
			case "isShown()Z":
				return 1, nil
			case "repaint(IIII)V", "repaint()V":
				card := registers[1]
				runtime.dirtyCards[card] = true
				if runtime.DeferThreads && runtime.activeTask != nil {
					runtime.deferCardPaint(runtime.activeTask, card, false)
				}
				return 0, nil
			case "serviceRepaints()V":
				return 0, runtime.serviceCardRepaints(ctx, registers[1])
			case "showNotify(Z)V", "setCanvas(Ljavax/microedition/lcdui/Canvas;)V":
				return 0, nil
			case "keyNotify(II)Z", "pointerNotify(III)Z":
				return 0, nil
			case "move(II)V":
				if err := runtime.WriteJavaFieldWord(
					registers[1],
					8,
					registers[2],
				); err != nil {
					return 0, err
				}
				if err := runtime.WriteJavaFieldWord(
					registers[1],
					12,
					registers[3],
				); err != nil {
					return 0, err
				}
				return 0, nil
			case "resize(II)V":
				if err := runtime.WriteJavaFieldWord(
					registers[1],
					16,
					registers[2],
				); err != nil {
					return 0, err
				}
				if err := runtime.WriteJavaFieldWord(
					registers[1],
					20,
					registers[3],
				); err != nil {
					return 0, err
				}
				return 0, nil
			}
		case "org/kwis/msp/lcdui/Font":
			return runtime.handleFontMethod(name, descriptor)
		case "org/kwis/msp/lcdui/Image":
			return runtime.handleImageMethod(name, descriptor)
		case "org/kwis/msp/lcdui/Graphics":
			return runtime.handleGraphicsMethod(name, descriptor)
		case "org/kwis/msp/media/Volume", "org/kwis/msf/io/Network":
			return 0, nil
		case "org/kwis/msp/media/BaseClip", "org/kwis/msp/media/Clip",
			"org/kwis/msp/media/Player":
			return runtime.handleMediaMethod(name, descriptor)
		case "java/lang/Throwable":
			return runtime.handleThrowableMethod(name, descriptor)
		case "org/kwis/msp/handset/HandsetProperty":
			switch name + descriptor {
			case "getSystemProperty(Ljava/lang/String;)Ljava/lang/String;":
				key := runtime.javaStringValue(registers[1])
				value := runtime.handsetSystemProperty(key)
				runtime.trace("java_handset_property:" + key + "=" + value)
				return runtime.NewJavaString(value)
			case "setSystemProperty(Ljava/lang/String;Ljava/lang/String;)Z":
				key := strings.ToUpper(strings.TrimSpace(
					runtime.javaStringValue(registers[1]),
				))
				value := runtime.javaStringValue(registers[2])
				runtime.wipicSystemProperties[key] = value
				runtime.trace("java_handset_property_set:" + key)
				return 1, nil
			}
		case "org/kwis/msp/lcdui/Jlet":
			return runtime.handleJletMethod(name, descriptor)
		case "org/kwis/msp/lcdui/EventQueue":
			return runtime.handleEventQueueMethod(name, descriptor)
		case "org/kwis/msp/lcdui/DisplayProxy":
			return runtime.handleDisplayMethod(ctx, name, descriptor)
		case "org/kwis/msp/lcdui/InputMethodHandler":
			return runtime.handleInputMethodHandlerMethod(name, descriptor)
		case "org/kwis/msp/lcdui/Main":
			// The host boots titles through startApp directly; the Main
			// wrapper never has work to do.
			return 0, nil
		case "com/ktf/kfc/GForm":
			return 0, nil
		case "org/kwis/msp/handset/BackLight":
			switch name + descriptor {
			case "alwaysOn()V", "on()V", "on(III)V":
				return 0, runtime.Services.Device.SetBacklight(
					true,
					0,
					runtime.Services.Clock.Monotonic(),
				)
			case "off()V", "before()V":
				return 0, runtime.Services.Device.SetBacklight(
					false,
					0,
					runtime.Services.Clock.Monotonic(),
				)
			}
			return 0, nil
		case "org/kwis/msp/handset/LED":
			switch name + descriptor {
			case "set(I)V":
				return 0, runtime.Services.Device.SetLED(
					0,
					int32(registers[1]),
				)
			case "get()I":
				return 0, nil
			case "getCount()I":
				return 1, nil
			}
			return 0, nil
		case "org/kwis/msp/handset/Call":
			// Telephony is absent; call control requests are absorbed the
			// way a handset in flight mode absorbs them.
			if name == "place" || name == "place0" {
				runtime.tracef(
					"java_call_place_unavailable:%s",
					runtime.javaStringValue(registers[1]),
				)
			}
			return 0, nil
		case "org/kwis/msp/lwc/Component",
			"org/kwis/msp/lwc/ContainerComponent",
			"org/kwis/msp/lwc/ShellComponent",
			"org/kwis/msp/lwc/FormComponent",
			"org/kwis/msp/lwc/AnnunciatorComponent",
			"org/kwis/msp/lwc/TextComponent",
			"org/kwis/msp/lwc/TextBoxComponent",
			"org/kwis/msp/lwc/TextFieldComponent",
			"org/kwis/msp/lwc/LabelComponent",
			"org/kwis/msp/lwc/ProgressComponent",
			"org/kwis/msp/lwc/DialogComponent",
			"org/kwis/msp/lwc/ButtonComponent",
			"org/kwis/msp/lwc/CheckboxComponent",
			"org/kwis/msp/lwc/CheckboxGroup",
			"org/kwis/msp/lwc/ComboComponent",
			"org/kwis/msp/lwc/Command",
			"org/kwis/msp/lwc/CommandBarComponent",
			"org/kwis/msp/lwc/DateFieldComponent",
			"org/kwis/msp/lwc/ImageComponent",
			"org/kwis/msp/lwc/ListComponent",
			"org/kwis/msp/lwc/ListItemComponent",
			"org/kwis/msp/lwc/ScrollbarComponent",
			"org/kwis/msp/lwc/TickerComponent",
			"org/kwis/msp/lwc/TextComponent$ModeViewer":
			return runtime.handleLWCMethod(
				ctx,
				className,
				name,
				descriptor,
				registers,
			)
		case "org/kwis/msp/lwc/Decorator":
			return runtime.handleLWCDecoratorMethod(name, descriptor)
		case "com/ktf/kfc/GTextListener":
			return 0, nil
		case "com/ktf/kfc/GTextField":
			if name+descriptor ==
				"getGTextListener()Lcom/ktf/kfc/GTextListener;" {
				instance := registers[1]
				if listener := runtime.listeners[instance]; listener != 0 {
					return listener, nil
				}
				listener, err := runtime.NewHostJavaObject(
					"com/ktf/kfc/GTextListener",
				)
				if err != nil {
					return 0, err
				}
				runtime.listeners[instance] = listener
				return listener, nil
			}
			return 0, nil
		case "org/kwis/msp/io/File":
			return runtime.handleFileMethod(name, descriptor)
		case "org/kwis/msp/io/FileSystem":
			return runtime.handleFileSystemMethod(name, descriptor)
		case "org/kwis/msf/core/Kernel":
			return runtime.handleMSFKernelMethod(name, descriptor)
		case "org/kwis/msf/core/Shared":
			return runtime.handleMSFSharedMethod(name, descriptor)
		case "org/kwis/msf/io/Socket", "org/kwis/msf/io/HttpSocket":
			return runtime.handleMSFSocketMethod(name, descriptor)
		case "org/kwis/msf/io/Message":
			return runtime.handleMSFMessageMethod(name, descriptor)
		case "org/kwis/msf/io/URL":
			if name+descriptor ==
				"find(Ljava/lang/String;)Lorg/kwis/msf/io/Socket;" {
				url := runtime.javaStringValue(registers[1])
				runtime.tracef("java_url_find_unavailable:%s", url)
				return 0, nil
			}
		case "wec/OEMDevice":
			if name+descriptor == "getSYSTheme()Lwec/SYSTheme;" {
				runtime.trace("java_oem_sys_theme_unavailable")
				return 0, nil
			}
		case "org/kwis/msp/db/DataBase":
			return runtime.handleDataBaseMethod(ctx, name, descriptor)
		case "org/kwis/msp/db/DataComparatorInteger",
			"org/kwis/msp/db/DataComparatorString",
			"org/kwis/msp/db/DataFilterInteger":
			return runtime.handleDataComparatorMethod(
				className,
				name,
				descriptor,
			)
		case "org/kwis/msp/lcdui/Display":
			return runtime.handleDisplayMethod(ctx, name, descriptor)
		}
		if strings.HasSuffix(className, "Exception") ||
			strings.HasSuffix(className, "Error") {
			// Every host-modeled exception behaves like Throwable: the
			// constructor stores the optional message and the accessors
			// read it back.
			return runtime.handleThrowableMethod(name, descriptor)
		}
		if value, ok, err := runtime.redispatchGuestJavaMethod(
			ctx,
			className,
			name,
			descriptor,
			registers,
		); ok || err != nil {
			return value, err
		}
		signature := className + "." + name + descriptor
		receiverClass := ""
		if len(registers) > 1 && registers[1] != 0 {
			if receiverWords, receiverErr := runtime.ReadWords(
				registers[1],
				2,
			); receiverErr == nil {
				if class, classErr := runtime.InspectJavaClass(
					receiverWords[1],
				); classErr == nil {
					receiverClass = class.Name
				}
			}
		}
		runtime.UnimplementedJava[signature]++
		runtime.LastUnimplementedJava = signature
		runtime.tracef(
			"java_unimplemented:%s:receiver=0x%08x:class=%s",
			signature,
			registers[1],
			receiverClass,
		)
		return 0, nil
	}
}

// correctHostJavaReceiverClass repairs a KTF AOT cache quirk where an
// invokevirtual occasionally reuses a method stub resolved through an
// unrelated class. The argument container still carries the real receiver,
// so only incompatible, non-static calls are redirected, and only to an
// already modeled host method present on that receiver's hierarchy.
func (r *Runtime) correctHostJavaReceiverClass(
	className, name, descriptor string,
	registers []uint32,
) string {
	if strings.HasPrefix(name, "<") || len(registers) < 2 || registers[1] == 0 {
		return className
	}
	declaredAddress := r.JavaClasses[className]
	if declaredAddress == 0 {
		return className
	}
	declared, err := r.InspectJavaClass(declaredAddress)
	if err != nil {
		return className
	}
	if method, ok := findKTFJavaMethod(declared, name, descriptor); ok &&
		method.AccessFlags&0x0008 != 0 {
		return className
	}
	receiverWords, err := r.ReadWords(registers[1], 2)
	if err != nil || receiverWords[1] == 0 {
		return className
	}
	actual, err := r.InspectJavaClass(receiverWords[1])
	if err != nil {
		return className
	}
	if compatible, compatibilityErr := r.javaClassExtends(
		actual.Address,
		declared.Address,
	); compatibilityErr == nil && compatible {
		return className
	}
	for depth := 0; actual.Address != 0 && depth < 256; depth++ {
		if method, ok := findKTFJavaMethod(actual, name, descriptor); ok &&
			(method.Body != 0 || method.NativeBody != 0) &&
			r.hostJavaClass[method.DeclaringClass] {
			declaring, inspectErr := r.InspectJavaClass(method.DeclaringClass)
			if inspectErr == nil {
				return declaring.Name
			}
			return className
		}
		if actual.Parent == 0 {
			break
		}
		actual, err = r.InspectJavaClass(actual.Parent)
		if err != nil {
			break
		}
	}
	return className
}

func (r *Runtime) redispatchGuestJavaMethod(
	ctx context.Context,
	declaredClass string,
	name string,
	descriptor string,
	registers []uint32,
) (uint32, bool, error) {
	if strings.HasPrefix(name, "<") ||
		len(registers) < 2 ||
		registers[1] == 0 {
		return 0, false, nil
	}
	if declaredAddress := r.JavaClasses[declaredClass]; declaredAddress != 0 {
		if declared, err := r.InspectJavaClass(declaredAddress); err == nil {
			if method, ok := findKTFJavaMethod(
				declared,
				name,
				descriptor,
			); ok && method.AccessFlags&0x0008 != 0 {
				return 0, false, nil
			}
		}
	}
	receiverWords, err := r.ReadWords(registers[1], 2)
	if err != nil {
		return 0, false, nil
	}
	actual, err := r.InspectJavaClass(receiverWords[1])
	if err != nil || actual.Name == declaredClass || r.hostJavaClass[actual.Address] {
		return 0, false, nil
	}
	methodAddress, err := r.resolveJavaMethod(
		actual.Address,
		name,
		descriptor,
	)
	if err != nil {
		return 0, false, nil
	}
	method, err := r.InspectJavaMethod(methodAddress)
	if err != nil || method.Body == 0 {
		return 0, false, nil
	}
	if _, hostMethod := r.hostCalls[method.Body&^1]; hostMethod {
		return 0, false, nil
	}
	parameterWords, ok := ktfJavaParameterWords(descriptor)
	if !ok || parameterWords > len(registers)-2 {
		return 0, false, nil
	}
	// A guest override that calls super resolves the parent method through
	// the same host stub that redispatched into it, which would re-enter
	// the override forever (observed: 아르덴전기 showNotify). While an
	// override runs for a given receiver, an inner call to the same
	// method is a super call and must fall through to the host default.
	guard := fmt.Sprintf("%08x:%s%s", registers[1], name, descriptor)
	if r.redispatchActive[guard] {
		return 0, false, nil
	}
	if r.redispatchActive == nil {
		r.redispatchActive = make(map[string]bool)
	}
	r.tracef(
		"java_virtual_redispatch:%s.%s%s:actual=%s:body=0x%08x",
		declaredClass,
		name,
		descriptor,
		actual.Name,
		method.Body,
	)
	r.redispatchActive[guard] = true
	value, err := r.invokeJavaVirtual(
		ctx,
		registers[1],
		name,
		descriptor,
		registers[2:2+parameterWords]...,
	)
	delete(r.redispatchActive, guard)
	return value, true, err
}

func ktfJavaParameterWords(descriptor string) (int, bool) {
	if len(descriptor) == 0 || descriptor[0] != '(' {
		return 0, false
	}
	words := 0
	for offset := 1; offset < len(descriptor); {
		switch descriptor[offset] {
		case ')':
			return words, true
		case 'J', 'D':
			words += 2
			offset++
		case 'L':
			end := strings.IndexByte(descriptor[offset:], ';')
			if end < 0 {
				return 0, false
			}
			words++
			offset += end + 1
		case '[':
			for offset < len(descriptor) && descriptor[offset] == '[' {
				offset++
			}
			if offset >= len(descriptor) {
				return 0, false
			}
			if descriptor[offset] == 'L' {
				end := strings.IndexByte(descriptor[offset:], ';')
				if end < 0 {
					return 0, false
				}
				offset += end + 1
			} else {
				offset++
			}
			words++
		case 'Z', 'B', 'C', 'S', 'I', 'F':
			words++
			offset++
		default:
			return 0, false
		}
	}
	return 0, false
}

func (r *Runtime) handsetSystemProperty(key string) string {
	normalized := strings.ToUpper(strings.TrimSpace(key))
	if value, ok := r.wipicSystemProperties[normalized]; ok {
		return value
	}
	switch normalized {
	case "PHONEMODEL":
		// LG-KH1300 was a common 240x320 KTF WIPI target. Some games use
		// this property to select resource geometry and otherwise leave
		// array dimensions uninitialized.
		if r.Services == nil || r.Services.Device == nil {
			return "LG-KH1300"
		}
		return r.Services.Device.Config().Model
	case "BATTERYLEVEL":
		return r.batteryLevelSystemProperty()
	case "MAXBATTLEVEL":
		return "5"
	default:
		return ""
	}
}
