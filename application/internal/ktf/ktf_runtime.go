package ktf

import (
	"context"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"

	"github.com/mirusu400/aram-core/application/internal/guest"
	"github.com/mirusu400/aram-core/cpu"
	"github.com/mirusu400/aram-core/loader/ktf"
	shared "github.com/mirusu400/aram-core/runtime"
)

const (
	ImageBase                  = uint32(0x00100000)
	HostBase                   = uint32(0x01200000)
	HostSize                   = uint32(0x00010000)
	ktfReturnSentinel          = HostBase
	ktfBootstrapInstructionMax = uint64(100_000_000)
	ktfTaskStackSize           = uint32(0x00010000)
	RunBudgetMin               = uint64(10_000)
	MaxTasks                   = int(guest.DefaultStackSize / ktfTaskStackSize)
	ktfMaxPendingJavaCalls     = 4096
	ktfHostVirtualSlotBase     = uint16(256)
	ktfHostVirtualTableReserve = uint32(512)

	// KTF games commonly start their render thread before startApp has
	// published the images that thread paints. The handset scheduler let the
	// starting task continue much longer than ARAM's small host-facing slice.
	// Allow a longer initialization window until the first card is published,
	// then use a small cap so permanent loops cannot starve child threads.
	ktfThreadStartGrace        = uint64(128 * 1024)
	ktfInitialThreadStartGrace = uint64(2 * 1024 * 1024)

	ktfDisplayWidth      = uint32(240)
	ktfDisplayHeight     = uint32(320)
	ktfAnnunciatorHeight = int32(20)

	ktfJavaClassInitializing = uint8(1)
	ktfJavaClassInitialized  = uint8(2)
	ktfJavaTimerScheduled    = uint8(1)
	ktfJavaTimerExecuted     = uint8(2)
	ktfJavaTimerCancelled    = uint8(3)

	KeyPressed  = uint32(1)
	KeyReleased = uint32(2)

	ktfDialogTypeNone     = int32(0)
	ktfDialogTypeOK       = int32(1)
	ktfDialogTypeOKCancel = int32(2)
	ktfDialogTimeout      = int32(10)
	ktfDialogOK           = int32(11)
	ktfDialogCancel       = int32(12)
	ktfDialogOKButton     = int32(20)
	ktfDialogCancelButton = int32(21)

	// KTF's Java execution environment ends with native state fields at
	// offsets 0x24 and 0x28. The current exception-frame pointer is at 0x20.
	ktfJavaEnvironmentWords = 11
)

type Runtime struct {
	CPU     cpu.Backend
	Pkg     ktf.Package
	Mapped  bool
	ImageSz uint32
	Exe     ktfExecutable
	Heap    guest.Heap

	Services             *shared.Services
	serviceConfig        shared.Config
	ServiceOwner         shared.OwnerID
	serviceName          string
	imageServices        map[uint32]shared.ServiceID
	javaAssetServices    map[uint32]shared.ServiceID
	FontServices         map[uint32]shared.ServiceID
	GraphicsServices     map[uint32]shared.ServiceID
	wipicSurfaceServices map[uint32]shared.ServiceID
	wipicAssetServices   map[uint32]shared.ServiceID
	wipicTimerServices   map[uint32]shared.ServiceID
	wipicMediaServices   map[uint32]shared.ServiceID
	clipServices         map[uint32]shared.ServiceID
	DatabaseServices     map[string]shared.ServiceID
	fileServices         map[uint32]shared.ServiceID
	wipicFileServices    map[uint32]shared.ServiceID

	nextHostCall     uint32
	hostCalls        map[uint32]ktfHostCall
	HostTrace        []string
	HostTraceDropped int
	traceMode        KTFTraceMode
	LastHostCall     string
	traceSamples     []ktfTraceSample
	traceSampleNext  uint64
	// traceScratch is the reusable buffer traceJavaMethodCall formats into.
	traceScratch     []byte
	HostCallCount    uint64
	hostTraceSamples map[string]uint64

	knlInterface            uint32
	jbInterface             uint32
	wipicInterface          uint32
	mxUserMemInterface      uint32
	incrementalMemory       []ktfIncrementalMemoryRegion
	incrementalHeaps        map[uint32]*guest.Heap
	JavaClasses             map[string]uint32
	javaClassGeneration     uint64
	nativeSignatures        map[uint32]*ktfNativeSignatureMatches
	nativeSignatureGen      uint64
	javaClassInspections    map[uint32]*ktfJavaClassInspection
	javaMethodInspections   map[uint32]*ktfJavaMethodInspection
	javaInspectGen          uint64
	JavaStrings             map[uint32]string
	javaClassObjs           map[uint32]uint32
	classObjTarget          map[uint32]uint32
	hostJavaClass           map[uint32]bool
	javaClassInit           map[uint32]uint8
	JvmContext              uint32
	exceptionContext        uint32
	javaEnvironment         uint32
	javaVTables             map[uint32]uint32
	javaVTableCapacity      map[uint32]uint32
	javaVTableClasses       map[uint32]uint32
	hostJavaVirtualSlots    map[uint32]uint16
	nextHostVirtualSlot     uint16
	LastJavaMethod          string
	LastJavaReturn          uint32
	lastJavaJump            uint32
	LastJavaCallLR          uint32
	FirstJavaThrowName      string
	FirstJavaThrowRegisters []uint32
	FirstJavaThrowSP        uint32
	FirstJavaThrowStack     []uint32
	LastJavaThrowName       string
	LastJavaThrowRegisters  []uint32
	LastJavaThrowSP         uint32
	LastJavaThrowStack      []uint32
	JavaReturnHigh          uint32
	JavaExceptionFrames     []string
	UnimplementedJava       map[string]uint64
	LastUnimplementedJava   string
	randomSeeds             map[uint32]uint64
	integerValues           map[uint32]int32
	longValues              map[uint32]int64
	throwableMessages       map[uint32]uint32
	dates                   map[uint32]int64
	Vectors                 map[uint32][]uint32
	hashtables              map[uint32]map[string]ktfHashtableEntry
	enumerations            map[uint32]*ktfEnumeration
	clips                   map[uint32]*ktfClip
	listeners               map[uint32]uint32
	lwcEventData            map[uint32]uint32
	lwcChildren             map[uint32][]uint32
	lwcMaxLengths           map[uint32]int32
	lwcComponents           map[uint32]*ktfLWCComponent
	databases               map[uint32]*Database
	DatabaseStores          map[string]*Database
	defaultRuntime          uint32
	DefaultDisplay          uint32
	MainJlet                uint32
	eventQueue              uint32
	sharedBuffers           map[string]uint32
	redispatchActive        map[string]bool
	DisplayCards            map[uint32]uint32
	ThreadTargets           map[uint32]uint32
	javaTimerTasks          map[uint32]*Task
	javaTimerTaskStates     map[uint32]uint8
	currentThread           uint32
	stringBuffers           map[uint32]string
	// stringBuffersConsumed marks a StringBuffer whose value was read out by
	// toString(). The LGT Raptor AOT compiler inlines StringBuffer.setLength(0)
	// as a direct native write to the buffer object's guest memory, which never
	// reaches this Go-backed handler, so a reused buffer would otherwise keep
	// its old text and accumulate (놈3 builds resource names in one buffer:
	// box.pzx -> box.pzxmenu.pzx -> ... so every .pzx lookup after the first
	// misses and loading never completes). Treating toString() as the consume
	// point and clearing on the next append models build->toString->reset->build.
	stringBuffersConsumed  map[uint32]bool
	inputStreams           map[uint32]*ktfInputStream
	inputTargets           map[uint32]uint32
	outputStreams          map[uint32][]byte
	outputTargets          map[uint32]uint32
	files                  map[uint32]*ktfFile
	FileData               map[string][]byte
	fileStreamTargets      map[uint32]uint32
	systemInputStream      uint32
	systemPrintStream      uint32
	images                 map[uint32]image.Image
	defaultFont            uint32
	frame                  *image.RGBA
	Graphics               map[uint32]*ktfGraphics
	graphicsRGBScratch     []byte
	ScreenGraphics         uint32
	menuForegroundCompat   *ktfMenuForegroundCompat
	wipicFramebuffers      map[uint32]*ktfWIPICFramebuffer
	WipicScreenFramebuffer uint32
	WipicScreenPending     bool
	wipicImages            map[uint32]*ktfWIPICImage
	wipicResources         map[uint32][]byte
	wipicResourceIDs       map[string]uint32
	wipicMemory            map[uint32]ktfWIPICMemory
	wipicTimers            map[uint32]*ktfWIPICTimer
	wipicMediaClips        map[uint32]*ktfWIPICMediaClip
	pendingMediaCallbacks  []uint32
	pendingNetCallbacks    []ktfPendingNetCallback
	wipicSystemProperties  map[string]string
	wipicFiles             map[uint32]*ktfFile
	nextWIPICFile          uint32
	dirtyCards             map[uint32]bool
	paintInitializedCards  map[uint32]bool
	PaintTasks             map[uint32]*Task
	PaintStalled           bool
	deferredPaintCards     map[*Task][]uint32
	deferredShownCards     map[*Task]map[uint32]bool
	PresentCount           uint32
	TickMS                 uint64

	NativeParameterBase uint32
	parameterScratch    [4]byte
	DeferThreads        bool
	// LenientMissingRead makes a read-only open of a missing private file
	// succeed with empty contents instead of raising IOException. The Raptor
	// AOT-Java host sets this because it has no guest exception unwinding: a
	// shipped title's first-run "open config read-only" would otherwise fault
	// the machine where a real device catches the exception and falls back to
	// defaults (월드장기체스 opens /CCCcfg before it is ever written).
	LenientMissingRead bool
	// ConsumeStringBufferOnToString treats a StringBuffer as reset once its
	// value has been read out by toString(): the next append starts a fresh
	// value. Only the Raptor AOT-Java host sets this, because that compiler
	// inlines StringBuffer.setLength(0) as a native write this handler never
	// sees, so a reused builder would otherwise accumulate. A plain KTF title
	// keeps exact Java semantics (its setLength(0) reaches the handler). See
	// stringBuffersConsumed.
	ConsumeStringBufferOnToString bool
	yieldRequested                bool
	terminationRequested          bool
	Tasks                         []*Task
	PendingJavaCalls              []ktfPendingJavaCall
	taskCursor                    int
	activeTask                    *Task
	ActiveInstructions            uint64
	executionDepth                int
	hostCallScopes                [16]ktfHostCallScope
	hostCallDepth                 int
	nestedCallContexts            []cpu.ScopedContext
}

type ktfHostCallScope struct {
	frame         cpu.HostCallFrame
	arguments     [cpu.MaxHostCallWords]uint32
	argumentWords uint32
	native        bool
	parameterBase uint32
}

type ktfHostHandler func(context.Context, *Runtime) (uint32, error)

type ktfHostCall struct {
	name    string
	handler ktfHostHandler
}

type ktfExecutable struct {
	WipiExeAddress      uint32
	ExeInterfaceAddress uint32
	FunctionsAddress    uint32
	Name                string
	ExecutableInit      uint32
	InterfaceInit       uint32
	GetDefaultDLL       uint32
	GetClass            uint32
	InterfaceUnknown2   uint32
	InterfaceUnknown3   uint32
}

type JavaClass struct {
	Address     uint32
	Name        string
	Parent      uint32
	VTable      uint32
	FieldSize   uint16
	AccessFlags uint16
	Methods     []JavaMethod
}

type JavaMethod struct {
	Address           uint32
	DeclaringClass    uint32
	Name              string
	Descriptor        string
	Body              uint32
	NativeBody        uint32
	VTableIndex       uint16
	AccessFlags       uint16
	ExceptionCount    uint16
	ExceptionTableRaw uint32
}

// ktfJavaClassInspection caches a parsed class so the Java bridge does not
// re-read every method name out of guest memory on each host call. The raw
// header words are re-read and compared on every hit, so a class whose
// descriptor, vtable, or size words change in place is re-parsed. Host-side
// method-body patches bump javaClassGeneration instead, which drops the whole
// cache.
type ktfJavaClassInspection struct {
	class           JavaClass
	classWords      [5]uint32
	descriptorWords [9]uint32
	// registerTrace is the host-trace line for registering this class, built
	// on first use. A guest can register the same class thousands of times a
	// second, and the line names every method, so formatting it per call was
	// the single most expensive thing a Java-heavy title did. The cache is
	// keyed with the inspection itself, so a class whose words change is
	// re-inspected and its line rebuilt.
	registerTrace string
}

type ktfJavaMethodInspection struct {
	method JavaMethod
	words  [7]uint32
}

type ktfHostJavaMethodSpec struct {
	name       string
	descriptor string
	access     uint16
}

type ktfHostJavaClassSpec struct {
	Parent    string
	fieldSize uint16
	methods   []ktfHostJavaMethodSpec
}

type Database struct {
	Name       string
	RecordSize uint32
	Records    [][]byte
}

type ktfInputStream struct {
	data     []byte
	position uint32
	mark     uint32
}

type ktfFile struct {
	namespace shared.Namespace
	name      string
	position  uint32
	mode      uint32
	closed    bool
}

const (
	ktfFileReadOnly   uint32 = 1
	ktfFileWrite      uint32 = 2
	ktfFileWriteTrunc uint32 = 3
	ktfFileReadWrite  uint32 = 4
)

type ktfHashtableEntry struct {
	key   uint32
	value uint32
}

type ktfEnumeration struct {
	values []uint32
	index  uint32
}

type ktfClip struct {
	volume   int32
	listener uint32
	playing  bool
	data     []byte
}

var ktfJavaExceptionParents = map[string]string{
	"java/lang/Throwable":                       "",
	"java/lang/Error":                           "java/lang/Throwable",
	"java/lang/Exception":                       "java/lang/Throwable",
	"java/lang/RuntimeException":                "java/lang/Exception",
	"java/lang/ArithmeticException":             "java/lang/RuntimeException",
	"java/lang/IllegalArgumentException":        "java/lang/RuntimeException",
	"java/lang/IllegalStateException":           "java/lang/RuntimeException",
	"java/lang/IndexOutOfBoundsException":       "java/lang/RuntimeException",
	"java/lang/ArrayIndexOutOfBoundsException":  "java/lang/IndexOutOfBoundsException",
	"java/lang/ArrayStoreException":             "java/lang/RuntimeException",
	"java/lang/StringIndexOutOfBoundsException": "java/lang/IndexOutOfBoundsException",
	"java/lang/NullPointerException":            "java/lang/RuntimeException",
	"java/lang/NumberFormatException":           "java/lang/IllegalArgumentException",
	"java/lang/SecurityException":               "java/lang/RuntimeException",
	"java/io/IOException":                       "java/lang/Exception",
	"java/io/EOFException":                      "java/io/IOException",
	"java/io/UTFDataFormatException":            "java/io/IOException",
	"org/kwis/msp/db/DataBaseException":         "java/lang/Exception",
	"org/kwis/msp/db/DataBaseRecordException":   "org/kwis/msp/db/DataBaseException",
}

type ktfGraphics struct {
	Target draw.Image
	clip   image.Rectangle
	color  color.RGBA
	// translate is the guest's own Graphics.translate offset, in card
	// coordinates. The guest reads it back with getTranslateX/Y, so it must
	// never carry a host-imposed offset.
	translate image.Point
	// origin places the card inside the physical framebuffer. A handset that
	// shows an annunciator lays the card out below it, so every guest-issued
	// coordinate is drawn origin lower than it asks for while the guest still
	// sees a card whose top-left is (0, 0).
	origin image.Point
	// surface bounds every clip this Graphics can express. It is the card
	// rectangle for the screen Graphics, so a guest clip that reaches above
	// the card cannot spill into the annunciator strip. The zero value means
	// the whole target.
	surface     image.Rectangle
	xorMode     bool
	PixelsDirty bool
}

// drawable is the region of Target this Graphics may touch.
func (g *ktfGraphics) drawable() image.Rectangle {
	if g.surface.Empty() {
		return g.Target.Bounds()
	}
	return g.surface
}

// offset converts a guest card coordinate to a framebuffer coordinate.
func (g *ktfGraphics) offset() image.Point {
	return g.translate.Add(g.origin)
}

// plot writes one pixel honoring XOR paint mode. Vector-drawn titles (놈3 renders
// its whole UI with drawLine while setXORMode(true) is active, inverting the
// white background to draw dark strokes) leave nothing visible if XOR is
// ignored, because the strokes are painted with a color that XORs to a visible
// value but overwrites to white.
func (g *ktfGraphics) plot(x, y int) {
	if !g.xorMode {
		g.Target.Set(x, y, g.color)
		return
	}
	old := color.RGBAModel.Convert(g.Target.At(x, y)).(color.RGBA)
	g.Target.Set(x, y, color.RGBA{
		R: old.R ^ g.color.R,
		G: old.G ^ g.color.G,
		B: old.B ^ g.color.B,
		A: 255,
	})
}

const (
	ktfJavaFontFaceSystem       = uint32(0)
	ktfJavaFontFaceMonospace    = uint32(32)
	ktfJavaFontFaceProportional = uint32(64)
	ktfJavaFontStyleMask        = uint32(7)
	ktfJavaFontSizeMedium       = uint32(0)
	JavaFontSizeSmall           = uint32(8)
	ktfJavaFontSizeLarge        = uint32(16)
)

// KTF Font objects retain WIPI's abstract face/style/size values in their
// guest fields. The selected Graphics font therefore survives save states
// without a second host-only state table.
type JavaFont struct {
	Face  uint32
	Style uint32
	Size  uint32
}

func (font JavaFont) valid() bool {
	switch font.Face {
	case ktfJavaFontFaceSystem,
		ktfJavaFontFaceMonospace,
		ktfJavaFontFaceProportional:
	default:
		return false
	}
	if font.Style&^ktfJavaFontStyleMask != 0 {
		return false
	}
	switch font.Size {
	case ktfJavaFontSizeMedium,
		JavaFontSizeSmall,
		ktfJavaFontSizeLarge:
		return true
	default:
		return false
	}
}

func (font JavaFont) descriptor() shared.FontDescriptor {
	height := int32(12)
	switch font.Size {
	case JavaFontSizeSmall:
		height = 8
	case ktfJavaFontSizeLarge:
		height = 16
	}
	var style shared.FontStyle
	if font.Style&1 != 0 {
		style |= shared.FontBold
	}
	if font.Style&2 != 0 {
		style |= shared.FontItalic
	}
	if font.Style&4 != 0 {
		style |= shared.FontUnderlined
	}
	return shared.FontDescriptor{
		Family: "aram-fallback",
		Size:   height,
		Style:  style,
	}
}

// ktfWIPICFramebuffer describes the provider-private MC_GRP object layout
// exposed by KTF handsets. Native Clet code does not treat MC_GrpFrameBuffer as
// an opaque integer: it follows object->body and reads the surface dimensions,
// stride, depth, and the nested pixel storage directly.
type ktfWIPICFramebuffer struct {
	object      uint32
	body        uint32
	pixelObject uint32
	pixelHeader uint32
	pixels      uint32
	width       int
	height      int
	stride      int
	bits        int
	screen      bool
}

type ktfWIPICImage struct {
	object      uint32
	body        uint32
	framebuffer uint32
	source      uint32
	frameIndex  uint32
	// transparentKey is the RGB565 color a color-keyed bitmap uses for its
	// transparent background, or -1 when the image draws fully opaque.
	transparentKey int32
}

type ktfWIPICMemory struct {
	base uint32
	data uint32
	size uint32
}

type ktfIncrementalMemoryRegion struct {
	base uint32
	size uint32
}

type ktfWIPICTimer struct {
	callback  uint32
	parameter uint32
	deadline  uint64
	active    bool
}

type ktfWIPICMediaClip struct {
	mediaType string
	capacity  uint32
	callback  uint32
	data      []byte
	volume    int32
	state     uint8
	repeat    bool
	// lastTracedState dedups the GetState host trace: a title polls GetState in
	// a tight loop, so only a change (notably the playing->stopped edge when an
	// effect finishes) is worth an entry.
	lastTracedState uint8
	tracedState     bool
}

type ktfLWCComponent struct {
	x               int32
	y               int32
	width           int32
	height          int32
	preferredWidth  int32
	preferredHeight int32
	background      uint32
	foreground      uint32
	Parent          uint32
	card            uint32
	title           uint32
	command         uint32
	work            uint32
	focus           uint32
	text            uint32
	gap             int32
	progressValue   int32
	progressMax     int32
	progressStep    int32
	progressTop     int32
	progressBottom  int32
	dialogType      int32
	dialogTimeout   int32
	dialogAction    int32
	dialogOK        uint32
	dialogCancel    uint32
	font            uint32
	image           uint32
	imageActive     uint32
	group           uint32
	date            uint32
	mode            int32
	minimum         int32
	viewAmount      int32
	changeAmount    int32
	delay           int32
	activeIndex     int32
	shown           bool
	valid           bool
	focused         bool
	vertical        bool
	packed          bool
	annunciator     bool
	transparent     bool
	progressInput   bool
	selected        bool
}

type Task struct {
	Context          []byte
	executionContext cpu.ExecutionContext
	contextDirty     bool
	exceptionFrame   uint32
	LastJavaMethod   string
	instructions     uint64
	slices           uint64
	yields           uint64
	lastYieldReason  string
	WakeAtMS         uint64
	timerTask        uint32
	timerOwner       uint32
	timerPeriodMS    uint64
	timerDeadlineMS  uint64
	timerFixedRate   bool
	Done             bool
	presentOnReturn  bool
	bestEffortPaint  bool
	WipicTimer       bool
	paintCard        uint32
	KeyCard          uint32
	layoutOnReturn   uint32
	startBlocker     *Task
	childStartGrace  uint64
}

func (t *Task) Instructions() uint64 {
	if t == nil {
		return 0
	}
	return t.instructions
}

func (t *Task) Slices() uint64 {
	if t == nil {
		return 0
	}
	return t.slices
}

func (t *Task) Yields() uint64 {
	if t == nil {
		return 0
	}
	return t.yields
}

func (t *Task) LastYieldReason() string {
	if t == nil {
		return ""
	}
	return t.lastYieldReason
}

type ktfPendingJavaCall struct {
	instance   uint32
	name       string
	descriptor string
	args       []uint32
}

type ktfJavaExceptionTarget struct {
	contextBase uint32
	handler     uint32
	restore     uint32
}

type ktfJavaExceptionUnwind struct {
	Target ktfJavaExceptionTarget
}

func (e *ktfJavaExceptionUnwind) Error() string {
	return fmt.Sprintf(
		"KTF Java exception unwind through 0x%08x to handler 0x%08x",
		e.Target.restore,
		e.Target.handler,
	)
}

type ktfUnhandledJavaException struct {
	name    string
	detail  uint32
	Context string
}

func (e *ktfUnhandledJavaException) Error() string {
	return fmt.Sprintf(
		"KTF Java exception %s (detail=0x%08x, %s)",
		e.name,
		e.detail,
		e.Context,
	)
}

func ktfNoop(context.Context, *Runtime) (uint32, error) {
	return 0, nil
}
