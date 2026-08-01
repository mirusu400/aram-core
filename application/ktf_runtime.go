package application

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
	"math"
	"path"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf16"

	"github.com/mirusu400/aram-core/cpu"
	"github.com/mirusu400/aram-core/loader/ktf"
	shared "github.com/mirusu400/aram-core/runtime"
)

const (
	ktfImageBase               = uint32(0x00100000)
	ktfHostBase                = uint32(0x01200000)
	ktfHostSize                = uint32(0x00010000)
	ktfReturnSentinel          = ktfHostBase
	ktfBootstrapInstructionMax = uint64(100_000_000)
	ktfTaskStackSize           = uint32(0x00010000)
	ktfMaxTasks                = int(DefaultStackSize / ktfTaskStackSize)
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

	ktfKeyPressed  = uint32(1)
	ktfKeyReleased = uint32(2)

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

type ktfRuntime struct {
	cpu     cpu.Backend
	pkg     ktf.Package
	mapped  bool
	imageSz uint32
	exe     ktfExecutable
	heap    guestHeap

	services             *shared.Services
	serviceConfig        shared.Config
	serviceOwner         shared.OwnerID
	serviceName          string
	imageServices        map[uint32]shared.ServiceID
	javaAssetServices    map[uint32]shared.ServiceID
	fontServices         map[uint32]shared.ServiceID
	graphicsServices     map[uint32]shared.ServiceID
	wipicSurfaceServices map[uint32]shared.ServiceID
	wipicAssetServices   map[uint32]shared.ServiceID
	wipicTimerServices   map[uint32]shared.ServiceID
	wipicMediaServices   map[uint32]shared.ServiceID
	clipServices         map[uint32]shared.ServiceID
	databaseServices     map[string]shared.ServiceID
	fileServices         map[uint32]shared.ServiceID
	wipicFileServices    map[uint32]shared.ServiceID

	nextHostCall     uint32
	hostCalls        map[uint32]ktfHostCall
	hostTrace        []string
	hostTraceDropped int

	knlInterface            uint32
	jbInterface             uint32
	wipicInterface          uint32
	mxUserMemInterface      uint32
	incrementalMemory       []ktfIncrementalMemoryRegion
	incrementalHeaps        map[uint32]*guestHeap
	javaClasses             map[string]uint32
	javaClassGeneration     uint64
	nativeSignatures        map[uint32]*ktfNativeSignatureMatches
	nativeSignatureGen      uint64
	javaStrings             map[uint32]string
	javaClassObjs           map[uint32]uint32
	classObjTarget          map[uint32]uint32
	hostJavaClass           map[uint32]bool
	javaClassInit           map[uint32]uint8
	jvmContext              uint32
	exceptionContext        uint32
	javaEnvironment         uint32
	javaVTables             map[uint32]uint32
	javaVTableCapacity      map[uint32]uint32
	javaVTableClasses       map[uint32]uint32
	hostJavaVirtualSlots    map[uint32]uint16
	nextHostVirtualSlot     uint16
	lastJavaMethod          string
	lastJavaReturn          uint32
	lastJavaJump            uint32
	lastJavaCallLR          uint32
	firstJavaThrowName      string
	firstJavaThrowRegisters []uint32
	firstJavaThrowSP        uint32
	firstJavaThrowStack     []uint32
	lastJavaThrowName       string
	lastJavaThrowRegisters  []uint32
	lastJavaThrowSP         uint32
	lastJavaThrowStack      []uint32
	javaReturnHigh          uint32
	javaExceptionFrames     []string
	unimplementedJava       map[string]uint64
	lastUnimplementedJava   string
	randomSeeds             map[uint32]uint64
	integerValues           map[uint32]int32
	longValues              map[uint32]int64
	throwableMessages       map[uint32]uint32
	dates                   map[uint32]int64
	vectors                 map[uint32][]uint32
	hashtables              map[uint32]map[string]ktfHashtableEntry
	enumerations            map[uint32]*ktfEnumeration
	clips                   map[uint32]*ktfClip
	listeners               map[uint32]uint32
	lwcEventData            map[uint32]uint32
	lwcChildren             map[uint32][]uint32
	lwcMaxLengths           map[uint32]int32
	lwcComponents           map[uint32]*ktfLWCComponent
	databases               map[uint32]*ktfDatabase
	databaseStores          map[string]*ktfDatabase
	defaultRuntime          uint32
	defaultDisplay          uint32
	displayCards            map[uint32]uint32
	threadTargets           map[uint32]uint32
	javaTimerTasks          map[uint32]*ktfTask
	javaTimerTaskStates     map[uint32]uint8
	currentThread           uint32
	stringBuffers           map[uint32]string
	inputStreams            map[uint32]*ktfInputStream
	inputTargets            map[uint32]uint32
	outputStreams           map[uint32][]byte
	outputTargets           map[uint32]uint32
	files                   map[uint32]*ktfFile
	fileData                map[string][]byte
	fileStreamTargets       map[uint32]uint32
	systemInputStream       uint32
	systemPrintStream       uint32
	images                  map[uint32]image.Image
	defaultFont             uint32
	frame                   *image.RGBA
	graphics                map[uint32]*ktfGraphics
	screenGraphics          uint32
	menuForegroundCompat    *ktfMenuForegroundCompat
	wipicFramebuffers       map[uint32]*ktfWIPICFramebuffer
	wipicScreenFramebuffer  uint32
	wipicImages             map[uint32]*ktfWIPICImage
	wipicResources          map[uint32][]byte
	wipicResourceIDs        map[string]uint32
	wipicMemory             map[uint32]ktfWIPICMemory
	wipicTimers             map[uint32]*ktfWIPICTimer
	wipicMediaClips         map[uint32]*ktfWIPICMediaClip
	wipicSystemProperties   map[string]string
	wipicFiles              map[uint32]*ktfFile
	nextWIPICFile           uint32
	dirtyCards              map[uint32]bool
	paintInitializedCards   map[uint32]bool
	paintTasks              map[uint32]*ktfTask
	paintStalled            bool
	deferredPaintCards      map[*ktfTask][]uint32
	deferredShownCards      map[*ktfTask]map[uint32]bool
	presentCount            uint32
	tickMS                  uint64

	nativeParameterBase  uint32
	deferThreads         bool
	yieldRequested       bool
	terminationRequested bool
	tasks                []*ktfTask
	pendingJavaCalls     []ktfPendingJavaCall
	taskCursor           int
	activeTask           *ktfTask
	activeInstructions   uint64
	executionDepth       int
}

type ktfHostHandler func(context.Context, *ktfRuntime) (uint32, error)

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

type ktfJavaClass struct {
	Address     uint32
	Name        string
	Parent      uint32
	VTable      uint32
	FieldSize   uint16
	AccessFlags uint16
	Methods     []ktfJavaMethod
}

type ktfJavaMethod struct {
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

type ktfHostJavaMethodSpec struct {
	name       string
	descriptor string
	access     uint16
}

type ktfHostJavaClassSpec struct {
	parent    string
	fieldSize uint16
	methods   []ktfHostJavaMethodSpec
}

type ktfDatabase struct {
	name       string
	recordSize uint32
	records    [][]byte
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
	target      draw.Image
	clip        image.Rectangle
	color       color.RGBA
	translate   image.Point
	pixelsDirty bool
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
	parent          uint32
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
	shown           bool
	valid           bool
	focused         bool
	vertical        bool
	packed          bool
	annunciator     bool
	transparent     bool
	progressInput   bool
}

type ktfTask struct {
	context         []byte
	exceptionFrame  uint32
	lastJavaMethod  string
	wakeAtMS        uint64
	timerTask       uint32
	timerOwner      uint32
	timerPeriodMS   uint64
	timerDeadlineMS uint64
	timerFixedRate  bool
	done            bool
	presentOnReturn bool
	bestEffortPaint bool
	wipicTimer      bool
	paintCard       uint32
	keyCard         uint32
	layoutOnReturn  uint32
	startBlocker    *ktfTask
	childStartGrace uint64
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
	target ktfJavaExceptionTarget
}

func (e *ktfJavaExceptionUnwind) Error() string {
	return fmt.Sprintf(
		"KTF Java exception unwind through 0x%08x to handler 0x%08x",
		e.target.restore,
		e.target.handler,
	)
}

type ktfUnhandledJavaException struct {
	name    string
	detail  uint32
	context string
}

func (e *ktfUnhandledJavaException) Error() string {
	return fmt.Sprintf(
		"KTF Java exception %s (detail=0x%08x, %s)",
		e.name,
		e.detail,
		e.context,
	)
}

var ktfHostJavaClassSpecs = map[string]ktfHostJavaClassSpec{
	"java/lang/Object": {
		methods: []ktfHostJavaMethodSpec{
			{name: "<init>", descriptor: "()V"},
			{name: "getClass", descriptor: "()Ljava/lang/Class;"},
			{name: "hashCode", descriptor: "()I"},
			{name: "equals", descriptor: "(Ljava/lang/Object;)Z"},
			{name: "clone", descriptor: "()Ljava/lang/Object;", access: 0x0100},
			{name: "toString", descriptor: "()Ljava/lang/String;"},
			{name: "notify", descriptor: "()V"},
			{name: "notifyAll", descriptor: "()V"},
			{name: "wait", descriptor: "(J)V"},
			{name: "wait", descriptor: "(JI)V"},
			{name: "wait", descriptor: "()V"},
			{name: "finalize", descriptor: "()V"},
		},
	},
	"java/lang/Class": {
		parent:    "java/lang/Object",
		fieldSize: 8,
		methods: []ktfHostJavaMethodSpec{
			{name: "<init>", descriptor: "()V"},
			{name: "getName", descriptor: "()Ljava/lang/String;"},
			{
				name:       "isAssignableFrom",
				descriptor: "(Ljava/lang/Class;)Z",
			},
			{
				name:       "getResourceAsStream",
				descriptor: "(Ljava/lang/String;)Ljava/io/InputStream;",
			},
			{
				name:       "forName",
				descriptor: "(Ljava/lang/String;)Ljava/lang/Class;",
				access:     0x0108,
			},
		},
	},
	"java/lang/String": {
		parent:    "java/lang/Object",
		fieldSize: 12,
		methods: []ktfHostJavaMethodSpec{
			{name: "<init>", descriptor: "()V"},
			{name: "<init>", descriptor: "(Ljava/lang/String;)V"},
			{name: "<init>", descriptor: "([B)V"},
			{name: "<init>", descriptor: "([BII)V"},
			{name: "<init>", descriptor: "([C)V"},
			{name: "<init>", descriptor: "([CII)V"},
			{name: "length", descriptor: "()I"},
			{name: "charAt", descriptor: "(I)C"},
			{name: "substring", descriptor: "(I)Ljava/lang/String;"},
			{name: "substring", descriptor: "(II)Ljava/lang/String;"},
			{name: "trim", descriptor: "()Ljava/lang/String;"},
			{name: "getBytes", descriptor: "()[B"},
			{name: "toCharArray", descriptor: "()[C"},
			{
				name:       "equals",
				descriptor: "(Ljava/lang/Object;)Z",
			},
			{
				name:       "concat",
				descriptor: "(Ljava/lang/String;)Ljava/lang/String;",
			},
			{
				name:       "startsWith",
				descriptor: "(Ljava/lang/String;)Z",
			},
			{
				name:       "endsWith",
				descriptor: "(Ljava/lang/String;)Z",
			},
			{name: "indexOf", descriptor: "(I)I"},
			{name: "indexOf", descriptor: "(II)I"},
			{
				name:       "indexOf",
				descriptor: "(Ljava/lang/String;)I",
			},
			{
				name:       "indexOf",
				descriptor: "(Ljava/lang/String;I)I",
			},
			{
				name:       "toLowerCase",
				descriptor: "()Ljava/lang/String;",
			},
			{
				name:       "toUpperCase",
				descriptor: "()Ljava/lang/String;",
			},
			{
				name:       "valueOf",
				descriptor: "(I)Ljava/lang/String;",
				access:     0x0008,
			},
			{
				name:       "valueOf",
				descriptor: "(J)Ljava/lang/String;",
				access:     0x0008,
			},
			{
				name:       "valueOf",
				descriptor: "(Z)Ljava/lang/String;",
				access:     0x0008,
			},
			{
				name:       "valueOf",
				descriptor: "(C)Ljava/lang/String;",
				access:     0x0008,
			},
			{
				name:       "valueOf",
				descriptor: "(Ljava/lang/Object;)Ljava/lang/String;",
				access:     0x0008,
			},
			{
				name:       "valueOf",
				descriptor: "([C)Ljava/lang/String;",
				access:     0x0008,
			},
			{
				name:       "valueOf",
				descriptor: "([CII)Ljava/lang/String;",
				access:     0x0008,
			},
		},
	},
	"java/lang/StringBuffer": {
		parent:    "java/lang/Object",
		fieldSize: 8,
		methods: []ktfHostJavaMethodSpec{
			{name: "<init>", descriptor: "()V"},
			{name: "<init>", descriptor: "(I)V"},
			{name: "<init>", descriptor: "(Ljava/lang/String;)V"},
			{
				name:       "append",
				descriptor: "(Ljava/lang/String;)Ljava/lang/StringBuffer;",
			},
			{
				name:       "append",
				descriptor: "(Ljava/lang/Object;)Ljava/lang/StringBuffer;",
			},
			{
				name:       "append",
				descriptor: "(Z)Ljava/lang/StringBuffer;",
			},
			{
				name:       "append",
				descriptor: "(I)Ljava/lang/StringBuffer;",
			},
			{
				name:       "append",
				descriptor: "(J)Ljava/lang/StringBuffer;",
			},
			{
				name:       "append",
				descriptor: "(C)Ljava/lang/StringBuffer;",
			},
			{
				name:       "append",
				descriptor: "([CII)Ljava/lang/StringBuffer;",
			},
			{
				name:       "delete",
				descriptor: "(II)Ljava/lang/StringBuffer;",
			},
			{name: "toString", descriptor: "()Ljava/lang/String;"},
			{name: "setLength", descriptor: "(I)V"},
			{name: "length", descriptor: "()I"},
			{name: "charAt", descriptor: "(I)C"},
		},
	},
	"java/io/InputStream": {
		parent: "java/lang/Object",
		methods: []ktfHostJavaMethodSpec{
			{name: "<init>", descriptor: "()V"},
			{name: "available", descriptor: "()I"},
			{name: "read", descriptor: "([BII)I"},
			{name: "read", descriptor: "([B)I"},
			{name: "read", descriptor: "()I"},
			{name: "close", descriptor: "()V"},
			{name: "skip", descriptor: "(J)J"},
			{name: "mark", descriptor: "(I)V"},
			{name: "reset", descriptor: "()V"},
		},
	},
	"java/io/ByteArrayInputStream": {
		parent: "java/io/InputStream",
		methods: []ktfHostJavaMethodSpec{
			{name: "<init>", descriptor: "([B)V"},
			{name: "<init>", descriptor: "([BII)V"},
		},
	},
	"java/io/DataInputStream": {
		parent: "java/io/InputStream",
		methods: []ktfHostJavaMethodSpec{
			{name: "<init>", descriptor: "(Ljava/io/InputStream;)V"},
			{name: "readBoolean", descriptor: "()Z"},
			{name: "readByte", descriptor: "()B"},
			{name: "readUnsignedByte", descriptor: "()I"},
			{name: "readShort", descriptor: "()S"},
			{name: "readUnsignedShort", descriptor: "()I"},
			{name: "readChar", descriptor: "()C"},
			{name: "readInt", descriptor: "()I"},
			{name: "readLong", descriptor: "()J"},
			{name: "readFully", descriptor: "([B)V"},
			{name: "readFully", descriptor: "([BII)V"},
			{name: "skipBytes", descriptor: "(I)I"},
			{name: "close", descriptor: "()V"},
		},
	},
	"java/io/PrintStream": {
		parent: "java/io/OutputStream",
		methods: []ktfHostJavaMethodSpec{
			{name: "<init>", descriptor: "(Ljava/io/OutputStream;)V"},
			{name: "print", descriptor: "(Ljava/lang/Object;)V"},
			{name: "print", descriptor: "(Ljava/lang/String;)V"},
			{name: "print", descriptor: "([C)V"},
			{name: "print", descriptor: "(Z)V"},
			{name: "print", descriptor: "(C)V"},
			{name: "print", descriptor: "(I)V"},
			{name: "print", descriptor: "(J)V"},
			{name: "println", descriptor: "()V"},
			{name: "println", descriptor: "(Ljava/lang/Object;)V"},
			{name: "println", descriptor: "(Ljava/lang/String;)V"},
			{name: "println", descriptor: "([C)V"},
			{name: "println", descriptor: "(Z)V"},
			{name: "println", descriptor: "(C)V"},
			{name: "println", descriptor: "(I)V"},
			{name: "println", descriptor: "(J)V"},
			{name: "flush", descriptor: "()V"},
			{name: "close", descriptor: "()V"},
			{name: "checkError", descriptor: "()Z"},
		},
	},
	"java/io/InputStreamReader": {
		parent:    "java/lang/Object",
		fieldSize: 4,
		methods: []ktfHostJavaMethodSpec{
			{name: "<init>", descriptor: "(Ljava/io/InputStream;)V"},
			{name: "read", descriptor: "([CII)I"},
			{name: "close", descriptor: "()V"},
		},
	},
	"java/io/ByteArrayOutputStream": {
		parent: "java/io/OutputStream",
		methods: []ktfHostJavaMethodSpec{
			{name: "<init>", descriptor: "()V"},
			{name: "<init>", descriptor: "(I)V"},
			{name: "write", descriptor: "(I)V"},
			{name: "write", descriptor: "([B)V"},
			{name: "write", descriptor: "([BII)V"},
			{name: "toByteArray", descriptor: "()[B"},
			{name: "size", descriptor: "()I"},
			{name: "close", descriptor: "()V"},
		},
	},
	"java/io/OutputStream": {
		parent: "java/lang/Object",
		methods: []ktfHostJavaMethodSpec{
			{name: "<init>", descriptor: "()V"},
			{name: "write", descriptor: "(I)V"},
			{name: "write", descriptor: "([B)V"},
			{name: "write", descriptor: "([BII)V"},
			{name: "flush", descriptor: "()V"},
			{name: "close", descriptor: "()V"},
		},
	},
	"java/io/DataOutputStream": {
		parent: "java/io/OutputStream",
		methods: []ktfHostJavaMethodSpec{
			{name: "<init>", descriptor: "(Ljava/io/OutputStream;)V"},
			{name: "write", descriptor: "(I)V"},
			{name: "write", descriptor: "([B)V"},
			{name: "write", descriptor: "([BII)V"},
			{name: "writeBoolean", descriptor: "(Z)V"},
			{name: "writeByte", descriptor: "(I)V"},
			{name: "writeShort", descriptor: "(I)V"},
			{name: "writeChar", descriptor: "(I)V"},
			{name: "writeInt", descriptor: "(I)V"},
			{name: "writeLong", descriptor: "(J)V"},
			{name: "flush", descriptor: "()V"},
			{name: "close", descriptor: "()V"},
		},
	},
	"java/lang/Integer": {
		parent:    "java/lang/Object",
		fieldSize: 4,
		methods: []ktfHostJavaMethodSpec{
			{name: "<init>", descriptor: "(I)V"},
			{name: "byteValue", descriptor: "()B"},
			{name: "shortValue", descriptor: "()S"},
			{name: "intValue", descriptor: "()I"},
			{name: "longValue", descriptor: "()J"},
			{name: "toString", descriptor: "()Ljava/lang/String;"},
			{name: "parseInt", descriptor: "(Ljava/lang/String;)I", access: 0x0008},
			{name: "parseInt", descriptor: "(Ljava/lang/String;I)I", access: 0x0008},
			{name: "toString", descriptor: "(I)Ljava/lang/String;", access: 0x0008},
			{name: "toString", descriptor: "(II)Ljava/lang/String;", access: 0x0008},
			{name: "toHexString", descriptor: "(I)Ljava/lang/String;", access: 0x0008},
			{name: "toOctalString", descriptor: "(I)Ljava/lang/String;", access: 0x0008},
			{name: "toBinaryString", descriptor: "(I)Ljava/lang/String;", access: 0x0008},
		},
	},
	"java/lang/Byte": {
		parent: "java/lang/Object",
		methods: []ktfHostJavaMethodSpec{
			{name: "parseByte", descriptor: "(Ljava/lang/String;)B", access: 0x0008},
			{name: "parseByte", descriptor: "(Ljava/lang/String;I)B", access: 0x0008},
		},
	},
	"java/lang/Math": {
		parent: "java/lang/Object",
		methods: []ktfHostJavaMethodSpec{
			{name: "abs", descriptor: "(I)I", access: 0x0008},
			{name: "max", descriptor: "(II)I", access: 0x0008},
			{name: "min", descriptor: "(II)I", access: 0x0008},
		},
	},
	"java/util/Random": {
		parent:    "java/lang/Object",
		fieldSize: 8,
		methods: []ktfHostJavaMethodSpec{
			{name: "<init>", descriptor: "()V"},
			{name: "<init>", descriptor: "(J)V"},
			{name: "setSeed", descriptor: "(J)V"},
			{name: "nextInt", descriptor: "()I"},
			{name: "nextInt", descriptor: "(I)I"},
			{name: "nextBoolean", descriptor: "()Z"},
		},
	},
	"java/util/Date": {
		parent:    "java/lang/Object",
		fieldSize: 8,
		methods: []ktfHostJavaMethodSpec{
			{name: "<init>", descriptor: "()V"},
			{name: "<init>", descriptor: "(J)V"},
			{name: "getTime", descriptor: "()J"},
			{name: "setTime", descriptor: "(J)V"},
		},
	},
	"java/util/GregorianCalendar": {
		parent: "java/util/Calendar",
		methods: []ktfHostJavaMethodSpec{
			{name: "<init>", descriptor: "()V"},
		},
	},
	"java/util/Vector": {
		parent:    "java/lang/Object",
		fieldSize: 8,
		methods: []ktfHostJavaMethodSpec{
			{name: "<init>", descriptor: "()V"},
			{name: "<init>", descriptor: "(I)V"},
			{name: "<init>", descriptor: "(II)V"},
			{name: "addElement", descriptor: "(Ljava/lang/Object;)V"},
			{name: "elementAt", descriptor: "(I)Ljava/lang/Object;"},
			{name: "setElementAt", descriptor: "(Ljava/lang/Object;I)V"},
			{name: "removeElementAt", descriptor: "(I)V"},
			{name: "removeElement", descriptor: "(Ljava/lang/Object;)Z"},
			{name: "removeAllElements", descriptor: "()V"},
			{name: "size", descriptor: "()I"},
			{name: "capacity", descriptor: "()I"},
			{name: "isEmpty", descriptor: "()Z"},
			{name: "contains", descriptor: "(Ljava/lang/Object;)Z"},
			{name: "indexOf", descriptor: "(Ljava/lang/Object;)I"},
			{name: "copyInto", descriptor: "([Ljava/lang/Object;)V"},
			{name: "elements", descriptor: "()Ljava/util/Enumeration;"},
		},
	},
	"java/util/Stack": {
		parent: "java/util/Vector",
		methods: []ktfHostJavaMethodSpec{
			{name: "<init>", descriptor: "()V"},
			{name: "push", descriptor: "(Ljava/lang/Object;)Ljava/lang/Object;"},
			{name: "pop", descriptor: "()Ljava/lang/Object;"},
			{name: "peek", descriptor: "()Ljava/lang/Object;"},
			{name: "empty", descriptor: "()Z"},
		},
	},
	"java/util/Hashtable": {
		parent:    "java/lang/Object",
		fieldSize: 8,
		methods: []ktfHostJavaMethodSpec{
			{name: "<init>", descriptor: "()V"},
			{name: "<init>", descriptor: "(I)V"},
			{name: "put", descriptor: "(Ljava/lang/Object;Ljava/lang/Object;)Ljava/lang/Object;"},
			{name: "get", descriptor: "(Ljava/lang/Object;)Ljava/lang/Object;"},
			{name: "remove", descriptor: "(Ljava/lang/Object;)Ljava/lang/Object;"},
			{name: "containsKey", descriptor: "(Ljava/lang/Object;)Z"},
			{name: "contains", descriptor: "(Ljava/lang/Object;)Z"},
			{name: "size", descriptor: "()I"},
			{name: "isEmpty", descriptor: "()Z"},
			{name: "clear", descriptor: "()V"},
			{name: "keys", descriptor: "()Ljava/util/Enumeration;"},
			{name: "elements", descriptor: "()Ljava/util/Enumeration;"},
		},
	},
	"java/util/Enumeration": {
		parent: "java/lang/Object",
		methods: []ktfHostJavaMethodSpec{
			{name: "hasMoreElements", descriptor: "()Z"},
			{name: "nextElement", descriptor: "()Ljava/lang/Object;"},
		},
	},
	"java/util/Timer": {
		parent: "java/lang/Object",
		methods: []ktfHostJavaMethodSpec{
			{name: "<init>", descriptor: "()V"},
			{name: "schedule", descriptor: "(Ljava/util/TimerTask;J)V"},
			{name: "schedule", descriptor: "(Ljava/util/TimerTask;JJ)V"},
			{name: "scheduleAtFixedRate", descriptor: "(Ljava/util/TimerTask;JJ)V"},
			{name: "cancel", descriptor: "()V"},
		},
	},
	"java/util/TimerTask": {
		parent: "java/lang/Object",
		methods: []ktfHostJavaMethodSpec{
			{name: "<init>", descriptor: "()V"},
			{name: "run", descriptor: "()V", access: 0x0400},
			{name: "cancel", descriptor: "()Z"},
		},
	},
	"java/util/TimeZone": {
		parent: "java/lang/Object",
		methods: []ktfHostJavaMethodSpec{
			{name: "getAvailableIDs", descriptor: "()[Ljava/lang/String;", access: 0x0008},
		},
	},
	"org/kwis/msp/media/Clip": {
		parent:    "java/lang/Object",
		fieldSize: 8,
		methods: []ktfHostJavaMethodSpec{
			{name: "<init>", descriptor: "(Ljava/lang/String;)V"},
			{name: "<init>", descriptor: "(Ljava/lang/String;I)V"},
			{name: "<init>", descriptor: "(Ljava/lang/String;Ljava/lang/String;)V"},
			{name: "<init>", descriptor: "(Ljava/lang/String;[B)V"},
			{name: "setVolume", descriptor: "(I)Z"},
			{name: "getVolume", descriptor: "()I"},
			{name: "setListener", descriptor: "(Lorg/kwis/msp/media/PlayListener;)V"},
		},
	},
	"org/kwis/msp/media/Player": {
		parent: "java/lang/Object",
		methods: []ktfHostJavaMethodSpec{
			{name: "play", descriptor: "(Lorg/kwis/msp/media/Clip;Z)Z", access: 0x0008},
			{name: "stop", descriptor: "(Lorg/kwis/msp/media/Clip;)Z", access: 0x0008},
		},
	},
	"org/kwis/msp/lcdui/Jlet": {
		parent: "java/lang/Object",
		methods: []ktfHostJavaMethodSpec{
			{name: "<init>", descriptor: "()V"},
		},
	},
	"org/kwis/msp/lwc/Component": {
		parent: "java/lang/Object",
	},
	"org/kwis/msp/lwc/ContainerComponent": {
		parent: "org/kwis/msp/lwc/Component",
	},
	"org/kwis/msp/lwc/ShellComponent": {
		parent: "org/kwis/msp/lwc/ContainerComponent",
	},
	"org/kwis/msp/lwc/FormComponent": {
		parent: "org/kwis/msp/lwc/ContainerComponent",
	},
	"org/kwis/msp/lwc/TextComponent": {
		parent: "org/kwis/msp/lwc/Component",
	},
	"org/kwis/msp/lwc/AnnunciatorComponent": {
		parent: "org/kwis/msp/lwc/ShellComponent",
		methods: []ktfHostJavaMethodSpec{
			{name: "<init>", descriptor: "(Z)V"},
			{name: "show", descriptor: "()V"},
			{name: "hide", descriptor: "()V"},
		},
	},
	"org/kwis/msp/handset/BackLight": {
		parent: "java/lang/Object",
		methods: []ktfHostJavaMethodSpec{
			{name: "alwaysOn", descriptor: "()V", access: 0x0008},
			{name: "on", descriptor: "()V", access: 0x0008},
			{name: "off", descriptor: "()V", access: 0x0008},
		},
	},
	"org/kwis/msp/handset/LED": {
		parent: "java/lang/Object",
		methods: []ktfHostJavaMethodSpec{
			{name: "set", descriptor: "(I)V", access: 0x0008},
		},
	},
	"org/kwis/msp/lwc/TextFieldComponent": {
		parent: "org/kwis/msp/lwc/TextComponent",
		methods: []ktfHostJavaMethodSpec{
			{name: "<init>", descriptor: "(Ljava/lang/String;I)V"},
			{name: "keyNotify", descriptor: "(II)Z"},
		},
	},
	"org/kwis/msp/lwc/LabelComponent": {
		parent: "org/kwis/msp/lwc/Component",
		methods: []ktfHostJavaMethodSpec{
			{name: "<init>", descriptor: "(Ljava/lang/String;)V"},
		},
	},
	"org/kwis/msp/lwc/ProgressComponent": {
		parent: "org/kwis/msp/lwc/Component",
		methods: []ktfHostJavaMethodSpec{
			{name: "<init>", descriptor: "(ZI)V"},
			{name: "setStep", descriptor: "(I)V"},
			{name: "getStep", descriptor: "()I"},
			{name: "setMargin", descriptor: "(II)V"},
			{name: "setMaxValue", descriptor: "(I)V"},
			{name: "getMaxValue", descriptor: "()I"},
			{name: "setValue", descriptor: "(I)I"},
			{name: "getValue", descriptor: "()I"},
			{name: "keyNotify", descriptor: "(II)Z"},
		},
	},
	"org/kwis/msp/lwc/DialogComponent": {
		parent: "org/kwis/msp/lwc/ShellComponent",
		methods: []ktfHostJavaMethodSpec{
			{name: "<init>", descriptor: "(I)V"},
			{
				name: "<init>",
				descriptor: "(Lorg/kwis/msp/lwc/Component;" +
					"Ljava/lang/String;I)V",
			},
			{name: "setButtonString", descriptor: "(ILjava/lang/String;)V"},
			{name: "setType", descriptor: "(I)V"},
			{name: "getType", descriptor: "()I"},
			{name: "setTimeout", descriptor: "(I)V"},
			{name: "getTimeout", descriptor: "()I"},
			{name: "doModal", descriptor: "()I"},
			{name: "getActionState", descriptor: "()I"},
		},
	},
	"com/ktf/kfc/GForm": {
		parent: "java/lang/Object",
		methods: []ktfHostJavaMethodSpec{
			{name: "<init>", descriptor: "(IIII)V"},
		},
	},
	"com/ktf/kfc/GTextField": {
		parent: "java/lang/Object",
		methods: []ktfHostJavaMethodSpec{
			{
				name: "<init>",
				descriptor: "(Lcom/ktf/kfc/GMenubarForm;" +
					"Ljava/lang/String;I)V",
			},
			{
				name:       "getGTextListener",
				descriptor: "()Lcom/ktf/kfc/GTextListener;",
			},
		},
	},
	"java/util/Calendar": {
		parent: "java/lang/Object",
		methods: []ktfHostJavaMethodSpec{
			{
				name:       "getInstance",
				descriptor: "()Ljava/util/Calendar;",
				access:     0x0008,
			},
			{name: "get", descriptor: "(I)I"},
			{name: "set", descriptor: "(II)V"},
			{name: "setTimeInMillis", descriptor: "(J)V"},
			{name: "getTimeInMillis", descriptor: "()J"},
		},
	},
	"java/lang/Thread": {
		parent:    "java/lang/Object",
		fieldSize: 16,
		methods: []ktfHostJavaMethodSpec{
			{name: "<init>", descriptor: "()V"},
			{name: "<init>", descriptor: "(Ljava/lang/Runnable;)V"},
			{name: "start", descriptor: "()V"},
			{name: "join", descriptor: "()V"},
			{name: "run", descriptor: "()V"},
			{name: "isAlive", descriptor: "()Z"},
			{name: "sleep", descriptor: "(J)V", access: 0x0108},
			{name: "yield", descriptor: "()V", access: 0x0108},
			{name: "setPriority", descriptor: "(I)V"},
			{
				name:       "currentThread",
				descriptor: "()Ljava/lang/Thread;",
				access:     0x0108,
			},
			{name: "<init>", descriptor: "(Z)V"},
		},
	},
	"java/lang/System": {
		parent: "java/lang/Object",
		methods: []ktfHostJavaMethodSpec{
			{
				name:       "arraycopy",
				descriptor: "(Ljava/lang/Object;ILjava/lang/Object;II)V",
				access:     0x0108,
			},
			{
				name:       "currentTimeMillis",
				descriptor: "()J",
				access:     0x0108,
			},
			{name: "gc", descriptor: "()V", access: 0x0008},
			{
				name:       "getProperty",
				descriptor: "(Ljava/lang/String;)Ljava/lang/String;",
				access:     0x0008,
			},
		},
	},
	"java/lang/Runtime": {
		parent: "java/lang/Object",
		methods: []ktfHostJavaMethodSpec{
			{
				name:       "getRuntime",
				descriptor: "()Ljava/lang/Runtime;",
				access:     0x0008,
			},
			{name: "freeMemory", descriptor: "()J"},
			{name: "totalMemory", descriptor: "()J"},
			{name: "gc", descriptor: "()V"},
			{name: "exit", descriptor: "(I)V"},
		},
	},
	"org/kwis/msp/lcdui/Card": {
		parent:    "java/lang/Object",
		fieldSize: 24,
		methods: []ktfHostJavaMethodSpec{
			{name: "<init>", descriptor: "()V"},
			{name: "<init>", descriptor: "(I)V"},
			{name: "<init>", descriptor: "(Lorg/kwis/msp/lcdui/Display;)V"},
			{name: "getWidth", descriptor: "()I"},
			{name: "getHeight", descriptor: "()I"},
			{name: "isShown", descriptor: "()Z"},
			{name: "repaint", descriptor: "(IIII)V"},
			{name: "repaint", descriptor: "()V"},
			{name: "serviceRepaints", descriptor: "()V"},
			{name: "showNotify", descriptor: "(Z)V"},
			{name: "keyNotify", descriptor: "(II)Z"},
			{name: "paint", descriptor: "(Lorg/kwis/msp/lcdui/Graphics;)V", access: 0x0400},
			{name: "setCanvas", descriptor: "(Ljavax/microedition/lcdui/Canvas;)V"},
		},
	},
	"org/kwis/msp/lcdui/Font": {
		parent: "java/lang/Object",
		methods: []ktfHostJavaMethodSpec{
			{
				name:       "getDefaultFont",
				descriptor: "()Lorg/kwis/msp/lcdui/Font;",
				access:     0x0008,
			},
			{
				name:       "getFont",
				descriptor: "(III)Lorg/kwis/msp/lcdui/Font;",
				access:     0x0008,
			},
			{name: "getHeight", descriptor: "()I"},
			{name: "charWidth", descriptor: "(C)I"},
			{name: "stringWidth", descriptor: "(Ljava/lang/String;)I"},
			{
				name:       "substringWidth",
				descriptor: "(Ljava/lang/String;II)I",
			},
		},
	},
	"org/kwis/msp/lcdui/Image": {
		parent:    "java/lang/Object",
		fieldSize: 8,
		methods: []ktfHostJavaMethodSpec{
			{
				name:       "createImage",
				descriptor: "(II)Lorg/kwis/msp/lcdui/Image;",
				access:     0x0008,
			},
			{
				name:       "createImage",
				descriptor: "(Ljava/lang/String;)Lorg/kwis/msp/lcdui/Image;",
				access:     0x0008,
			},
			{
				name:       "createImage",
				descriptor: "([BII)Lorg/kwis/msp/lcdui/Image;",
				access:     0x0008,
			},
			{name: "getWidth", descriptor: "()I"},
			{name: "getHeight", descriptor: "()I"},
			{
				name:       "getGraphics",
				descriptor: "()Lorg/kwis/msp/lcdui/Graphics;",
			},
		},
	},
	"org/kwis/msp/lcdui/Graphics": {
		parent:    "java/lang/Object",
		fieldSize: 4,
		methods: []ktfHostJavaMethodSpec{
			{
				name:       "<init>",
				descriptor: "(Lorg/kwis/msp/lcdui/Display;)V",
			},
			{
				name:       "<init>",
				descriptor: "(Ljavax/microedition/lcdui/Graphics;)V",
			},
			{
				name:       "getFont",
				descriptor: "()Lorg/kwis/msp/lcdui/Font;",
			},
			{name: "copyArea", descriptor: "(IIIIII)V"},
			{name: "setColor", descriptor: "(I)V"},
			{name: "setColor", descriptor: "(III)V"},
			{
				name:       "setFont",
				descriptor: "(Lorg/kwis/msp/lcdui/Font;)V",
			},
			{name: "setAlpha", descriptor: "(I)V"},
			{name: "fillRect", descriptor: "(IIII)V"},
			{name: "fillRoundRect", descriptor: "(IIIIII)V"},
			{name: "fillArc", descriptor: "(IIIIII)V"},
			{name: "drawLine", descriptor: "(IIII)V"},
			{name: "drawRect", descriptor: "(IIII)V"},
			{name: "drawRoundRect", descriptor: "(IIIIII)V"},
			{name: "drawArc", descriptor: "(IIIIII)V"},
			{name: "drawPolygon", descriptor: "([I[I)V"},
			{name: "drawChar", descriptor: "(CIII)V"},
			{name: "drawChars", descriptor: "([CIIIII)V"},
			{
				name:       "drawString",
				descriptor: "(Ljava/lang/String;III)V",
			},
			{
				name:       "drawSubstring",
				descriptor: "(Ljava/lang/String;IIIII)V",
			},
			{
				name:       "drawImage",
				descriptor: "(Lorg/kwis/msp/lcdui/Image;III)V",
			},
			{name: "setClip", descriptor: "(IIII)V"},
			{name: "clipRect", descriptor: "(IIII)V"},
			{name: "getAlpha", descriptor: "()I"},
			{name: "getBlueComponent", descriptor: "()I"},
			{name: "getColor", descriptor: "()I"},
			{name: "getClipX", descriptor: "()I"},
			{name: "getClipY", descriptor: "()I"},
			{name: "getClipWidth", descriptor: "()I"},
			{name: "getClipHeight", descriptor: "()I"},
			{name: "getGrayScale", descriptor: "()I"},
			{name: "getGreenComponent", descriptor: "()I"},
			{name: "getPixel", descriptor: "(II)I"},
			{name: "getPixels", descriptor: "(IIII[BII)V"},
			{name: "getRedComponent", descriptor: "()I"},
			{name: "getStrokeStyle", descriptor: "()I"},
			{name: "getTranslateX", descriptor: "()I"},
			{name: "getTranslateY", descriptor: "()I"},
			{name: "isXORMode", descriptor: "()Z"},
			{name: "translate", descriptor: "(II)V"},
			{name: "setPixel", descriptor: "(II)V"},
			{name: "setRGBPixels", descriptor: "(IIII[III)V"},
			{name: "setGrayScale", descriptor: "(I)V"},
			{name: "setStrokeStyle", descriptor: "(I)V"},
			{name: "setXORMode", descriptor: "(Z)V"},
			{name: "encodeImage", descriptor: "(IIII)[B"},
			{name: "getRGBPixels", descriptor: "(IIII[III)V"},
		},
	},
	"org/kwis/msp/media/Volume": {
		parent: "java/lang/Object",
		methods: []ktfHostJavaMethodSpec{
			{name: "set", descriptor: "(I)V", access: 0x0100},
		},
	},
	"org/kwis/msf/io/Network": {
		parent: "java/lang/Object",
		methods: []ktfHostJavaMethodSpec{
			{name: "connect", descriptor: "()I", access: 0x0108},
			{name: "disconnect", descriptor: "()V", access: 0x0108},
		},
	},
	"org/kwis/msp/db/DataBase": {
		parent:    "java/lang/Object",
		fieldSize: 4,
		methods: []ktfHostJavaMethodSpec{
			{name: "<init>", descriptor: "(Ljavax/microedition/rms/RecordStore;)V"},
			{
				name:       "openDataBase",
				descriptor: "(Ljava/lang/String;IZ)Lorg/kwis/msp/db/DataBase;",
				access:     0x0008,
			},
			{
				name:       "openDataBase",
				descriptor: "(Ljava/lang/String;IZI)Lorg/kwis/msp/db/DataBase;",
				access:     0x0008,
			},
			{name: "getNumberOfRecords", descriptor: "()I"},
			{name: "closeDataBase", descriptor: "()V"},
			{name: "insertRecord", descriptor: "([B)I"},
			{name: "insertRecord", descriptor: "([BII)I"},
			{name: "selectRecord", descriptor: "(I)[B"},
			{name: "updateRecord", descriptor: "(I[B)V"},
			{name: "updateRecord", descriptor: "(I[BII)V"},
			{
				name:       "deleteDataBase",
				descriptor: "(Ljava/lang/String;)V",
				access:     0x0008,
			},
		},
	},
	"org/kwis/msp/lcdui/Display": {
		parent:    "java/lang/Object",
		fieldSize: 8,
		methods: []ktfHostJavaMethodSpec{
			{
				name:       "<init>",
				descriptor: "(Lorg/kwis/msp/lcdui/Jlet;Lorg/kwis/msp/lcdui/DisplayProxy;)V",
			},
			{
				name:       "getDisplay",
				descriptor: "(Ljava/lang/String;)Lorg/kwis/msp/lcdui/Display;",
				access:     0x0008,
			},
			{
				name:       "getDefaultDisplay",
				descriptor: "()Lorg/kwis/msp/lcdui/Display;",
				access:     0x0008,
			},
			{name: "isDoubleBuffered", descriptor: "()Z"},
			{
				name:       "getDockedCard",
				descriptor: "()Lorg/kwis/msp/lcdui/Card;",
			},
			{
				name:       "pushCard",
				descriptor: "(Lorg/kwis/msp/lcdui/Card;)V",
			},
			{name: "removeAllCards", descriptor: "()V"},
			{
				name:       "addJletEventListener",
				descriptor: "(Lorg/kwis/msp/lcdui/JletEventListener;)V",
			},
			{name: "getWidth", descriptor: "()I"},
			{name: "getHeight", descriptor: "()I"},
			{
				name:       "callSerially",
				descriptor: "(Ljava/lang/Runnable;)V",
			},
			{
				name:       "getGameAction",
				descriptor: "(I)I",
				access:     0x0108,
			},
			{
				name:       "getKeyCode",
				descriptor: "(I)I",
				access:     0x0108,
			},
			{
				name:       "getKeyName",
				descriptor: "(I)Ljava/lang/String;",
				access:     0x0108,
			},
		},
	},
}

func newKTFRuntime(backend cpu.Backend, pkg ktf.Package) (*ktfRuntime, error) {
	return newKTFRuntimeForProfile(
		backend,
		pkg,
		nil,
		ktfProfileID,
	)
}

func newKTFRuntimeForProfile(
	backend cpu.Backend,
	pkg ktf.Package,
	frame *image.RGBA,
	profileID string,
) (*ktfRuntime, error) {
	if backend == nil {
		return nil, fmt.Errorf("initialize KTF runtime: CPU is nil")
	}
	if len(pkg.Client) == 0 {
		return nil, fmt.Errorf("initialize KTF runtime: client image is empty")
	}
	imageSize := uint64(len(pkg.Client)) + uint64(pkg.BSSSize)
	if imageSize > uint64(^uint32(0))-uint64(ktfImageBase) {
		return nil, fmt.Errorf("initialize KTF runtime: image range exceeds guest address space")
	}
	databaseStores := loadKTFDatabaseStores(pkg.Files)
	fileData := loadKTFPrivateFiles(pkg.JARName, pkg.Files)
	if profileID == "" {
		profileID = ktfProfileID
	}
	serviceConfig := shared.DefaultConfig()
	serviceConfig.Device.ProfileID = profileID
	serviceConfig.Device.Carrier = "ktf"
	serviceConfig.Device.Manufacturer = "LG"
	serviceConfig.Device.Model = "LG-KH1300"
	serviceConfig.Device.ScreenFormat = shared.PixelRGBA8888
	if frame != nil {
		serviceConfig.Device.ScreenWidth = int32(frame.Bounds().Dx())
		serviceConfig.Device.ScreenHeight = int32(frame.Bounds().Dy())
	}
	serviceConfig.Device.Capabilities = []shared.DeviceCapability{
		{Name: "audio", Enabled: true},
		{Name: "backlight", Enabled: true},
		{Name: "graphics", Enabled: true},
		{Name: "images", Enabled: true},
		{Name: "text", Enabled: true},
		{Name: "vibration", Enabled: true},
	}
	services, err := shared.NewServices(serviceConfig)
	if err != nil {
		return nil, fmt.Errorf("initialize KTF shared services: %w", err)
	}
	owner, err := services.Coordinator.Register(
		"ktf",
		serviceConfig.Limits.Coordinator.MaxRunBudget,
	)
	if err != nil {
		return nil, fmt.Errorf("register KTF adapter: %w", err)
	}
	if err := services.Coordinator.Transition(
		owner,
		shared.LifecycleReady,
		services.Clock.Monotonic(),
		services.Events,
	); err != nil {
		return nil, fmt.Errorf("ready KTF adapter: %w", err)
	}
	if err := services.Storage.MountPackage(pkg.Resources); err != nil {
		return nil, fmt.Errorf("mount KTF package resources: %w", err)
	}
	fileNames := make([]string, 0, len(fileData))
	for name := range fileData {
		fileNames = append(fileNames, name)
	}
	sort.Strings(fileNames)
	for _, name := range fileNames {
		if err := services.Storage.WriteFile(
			shared.NamespacePrivate,
			name,
			fileData[name],
		); err != nil {
			return nil, fmt.Errorf("import KTF private file %q: %w", name, err)
		}
	}
	databaseServices := make(map[string]shared.ServiceID, len(databaseStores))
	databaseNames := make([]string, 0, len(databaseStores))
	for name := range databaseStores {
		databaseNames = append(databaseNames, name)
	}
	sort.Strings(databaseNames)
	for _, name := range databaseNames {
		store, err := services.Storage.CreateRecordStore(owner, name)
		if err != nil {
			return nil, fmt.Errorf("import KTF database %q: %w", name, err)
		}
		records := make(map[uint32][]byte, len(databaseStores[name].records))
		for recordID, record := range databaseStores[name].records {
			records[uint32(recordID)] = record
		}
		nextID := max(uint32(1), uint32(len(records)))
		if err := services.Storage.ReplaceRecords(
			owner,
			store,
			nextID,
			records,
		); err != nil {
			return nil, fmt.Errorf("import KTF database %q: %w", name, err)
		}
		databaseServices[name] = store
	}
	return &ktfRuntime{
		cpu:                   backend,
		pkg:                   pkg,
		imageSz:               uint32(imageSize),
		frame:                 frame,
		services:              services,
		serviceConfig:         services.Config,
		serviceOwner:          owner,
		serviceName:           "ktf",
		imageServices:         make(map[uint32]shared.ServiceID),
		javaAssetServices:     make(map[uint32]shared.ServiceID),
		fontServices:          make(map[uint32]shared.ServiceID),
		graphicsServices:      make(map[uint32]shared.ServiceID),
		wipicSurfaceServices:  make(map[uint32]shared.ServiceID),
		wipicAssetServices:    make(map[uint32]shared.ServiceID),
		wipicTimerServices:    make(map[uint32]shared.ServiceID),
		wipicMediaServices:    make(map[uint32]shared.ServiceID),
		clipServices:          make(map[uint32]shared.ServiceID),
		databaseServices:      databaseServices,
		fileServices:          make(map[uint32]shared.ServiceID),
		wipicFileServices:     make(map[uint32]shared.ServiceID),
		nextHostCall:          ktfHostBase + 4,
		hostCalls:             make(map[uint32]ktfHostCall),
		javaClasses:           make(map[string]uint32),
		javaStrings:           make(map[uint32]string),
		javaClassObjs:         make(map[uint32]uint32),
		classObjTarget:        make(map[uint32]uint32),
		hostJavaClass:         make(map[uint32]bool),
		javaClassInit:         make(map[uint32]uint8),
		javaVTables:           make(map[uint32]uint32),
		javaVTableCapacity:    make(map[uint32]uint32),
		javaVTableClasses:     make(map[uint32]uint32),
		hostJavaVirtualSlots:  make(map[uint32]uint16),
		nextHostVirtualSlot:   ktfHostVirtualSlotBase,
		unimplementedJava:     make(map[string]uint64),
		randomSeeds:           make(map[uint32]uint64),
		integerValues:         make(map[uint32]int32),
		longValues:            make(map[uint32]int64),
		throwableMessages:     make(map[uint32]uint32),
		dates:                 make(map[uint32]int64),
		vectors:               make(map[uint32][]uint32),
		hashtables:            make(map[uint32]map[string]ktfHashtableEntry),
		enumerations:          make(map[uint32]*ktfEnumeration),
		clips:                 make(map[uint32]*ktfClip),
		listeners:             make(map[uint32]uint32),
		lwcEventData:          make(map[uint32]uint32),
		lwcChildren:           make(map[uint32][]uint32),
		lwcMaxLengths:         make(map[uint32]int32),
		lwcComponents:         make(map[uint32]*ktfLWCComponent),
		databases:             make(map[uint32]*ktfDatabase),
		databaseStores:        databaseStores,
		displayCards:          make(map[uint32]uint32),
		threadTargets:         make(map[uint32]uint32),
		javaTimerTasks:        make(map[uint32]*ktfTask),
		javaTimerTaskStates:   make(map[uint32]uint8),
		stringBuffers:         make(map[uint32]string),
		inputStreams:          make(map[uint32]*ktfInputStream),
		inputTargets:          make(map[uint32]uint32),
		outputStreams:         make(map[uint32][]byte),
		outputTargets:         make(map[uint32]uint32),
		files:                 make(map[uint32]*ktfFile),
		fileData:              fileData,
		fileStreamTargets:     make(map[uint32]uint32),
		images:                make(map[uint32]image.Image),
		graphics:              make(map[uint32]*ktfGraphics),
		menuForegroundCompat:  newKTFMenuForegroundCompat(pkg),
		wipicFramebuffers:     make(map[uint32]*ktfWIPICFramebuffer),
		wipicImages:           make(map[uint32]*ktfWIPICImage),
		wipicResources:        make(map[uint32][]byte),
		wipicResourceIDs:      make(map[string]uint32),
		wipicMemory:           make(map[uint32]ktfWIPICMemory),
		wipicTimers:           make(map[uint32]*ktfWIPICTimer),
		wipicMediaClips:       make(map[uint32]*ktfWIPICMediaClip),
		wipicSystemProperties: make(map[string]string),
		wipicFiles:            make(map[uint32]*ktfFile),
		nextWIPICFile:         1,
		dirtyCards:            make(map[uint32]bool),
		paintInitializedCards: make(map[uint32]bool),
		paintTasks:            make(map[uint32]*ktfTask),
		deferredPaintCards:    make(map[*ktfTask][]uint32),
		deferredShownCards:    make(map[*ktfTask]map[uint32]bool),
	}, nil
}

func loadKTFPrivateFiles(jarName string, files map[string][]byte) map[string][]byte {
	privateFiles := make(map[string][]byte)
	jarName = path.Clean(strings.ReplaceAll(jarName, `\`, "/"))
	packageRoot := path.Dir(jarName)
	for archiveName, data := range files {
		name := path.Clean(strings.ReplaceAll(archiveName, `\`, "/"))
		relative := name
		if packageRoot != "." {
			prefix := packageRoot + "/"
			if len(name) <= len(prefix) ||
				!strings.EqualFold(name[:len(prefix)], prefix) {
				continue
			}
			relative = name[len(prefix):]
		}
		separator := strings.IndexByte(relative, '/')
		if separator < 0 || !strings.EqualFold(relative[:separator], "P") {
			continue
		}
		privateName := normalizeKTFFileName(relative[separator+1:])
		if privateName == "/" {
			continue
		}
		privateFiles[privateName] = bytes.Clone(data)
	}
	addKTFEmptyFunterPatchFiles(privateFiles)
	return privateFiles
}

func addKTFEmptyFunterPatchFiles(files map[string][]byte) {
	hasFunterDatabase := false
	existing := make(map[string]bool, len(files))
	for name := range files {
		lower := strings.ToLower(name)
		existing[lower] = true
		if lower == "/funter_dl.db" {
			hasFunterDatabase = true
		}
	}
	if !hasFunterDatabase {
		return
	}
	const (
		funterData = 1 << iota
		funterMap
		funterResource
		funterSprite
		funterBaseBundle = funterData | funterMap | funterResource | funterSprite
	)
	type bundle struct {
		stem string
		mask int
	}
	bundles := make(map[string]bundle)
	for name := range files {
		extension := strings.ToLower(path.Ext(name))
		var bit int
		switch extension {
		case ".dat":
			bit = funterData
		case ".map":
			bit = funterMap
		case ".res":
			bit = funterResource
		case ".spr":
			bit = funterSprite
		default:
			continue
		}
		stem := strings.TrimSuffix(name, path.Ext(name))
		key := strings.ToLower(stem)
		current := bundles[key]
		if current.stem == "" || extension == ".dat" {
			current.stem = stem
		}
		current.mask |= bit
		bundles[key] = current
	}
	for key, candidate := range bundles {
		patchName := key + ".pch"
		if candidate.mask != funterBaseBundle || existing[patchName] {
			continue
		}
		// Funter's native bundle reader unconditionally opens the optional
		// patch sidecar even when a distribution carries no patch entries.
		// A zero-entry file is four zero catalog words followed by the
		// two-word header of an empty raw block.
		files[candidate.stem+".pch"] = make([]byte, 6*4)
	}
}

func loadKTFDatabaseStores(files map[string][]byte) map[string]*ktfDatabase {
	stores := make(map[string]*ktfDatabase)
	normalized := make(map[string][]byte, len(files))
	for name, data := range files {
		name = path.Clean(strings.ReplaceAll(name, `\`, "/"))
		normalized[strings.ToLower(name)] = data
	}
	for originalName, indexData := range files {
		name := path.Clean(strings.ReplaceAll(originalName, `\`, "/"))
		if !strings.EqualFold(path.Ext(name), ".idx") ||
			len(indexData) < 13 ||
			!bytes.Equal(indexData[:5], []byte("qtpdb")) {
			continue
		}
		base := strings.TrimSuffix(name, path.Ext(name))
		databaseData, ok := normalized[strings.ToLower(base+".db")]
		if !ok {
			continue
		}
		recordSize := binary.BigEndian.Uint32(indexData[5:9])
		recordCount := binary.BigEndian.Uint32(indexData[9:13])
		required := uint64(recordSize) * uint64(recordCount)
		if recordSize == 0 || required > uint64(len(databaseData)) {
			continue
		}
		databaseName := strings.TrimSuffix(path.Base(name), path.Ext(name))
		store := &ktfDatabase{
			name:       databaseName,
			recordSize: recordSize,
			records:    make([][]byte, 0, recordCount),
		}
		for index := uint32(0); index < recordCount; index++ {
			start := uint64(index) * uint64(recordSize)
			end := start + uint64(recordSize)
			store.records = append(
				store.records,
				bytes.Clone(databaseData[int(start):int(end)]),
			)
		}
		stores[databaseName] = store
	}
	return stores
}

func (r *ktfRuntime) mapImageAndHost() error {
	if r.mapped {
		return nil
	}
	if err := r.cpu.Map(
		ktfImageBase,
		r.imageSz,
		cpu.PermissionRead|cpu.PermissionWrite|cpu.PermissionExecute,
	); err != nil {
		return fmt.Errorf("map KTF client image: %w", err)
	}
	if err := r.cpu.WriteMemory(ktfImageBase, r.pkg.Client); err != nil {
		return fmt.Errorf("copy KTF client image: %w", err)
	}
	if err := r.cpu.Map(
		DefaultStackBase,
		DefaultStackSize,
		cpu.PermissionRead|cpu.PermissionWrite,
	); err != nil {
		return fmt.Errorf("map KTF application stack: %w", err)
	}
	if err := r.cpu.Map(
		ktfHostBase,
		ktfHostSize,
		cpu.PermissionRead|cpu.PermissionWrite|cpu.PermissionExecute,
	); err != nil {
		return fmt.Errorf("map KTF host-call page: %w", err)
	}
	stubs := make([]byte, ktfHostSize)
	for offset := 0; offset < len(stubs); offset += 4 {
		copy(stubs[offset:], []byte{0x00, 0xbe, 0x00, 0xbf})
	}
	if err := r.cpu.WriteMemory(ktfHostBase, stubs); err != nil {
		return fmt.Errorf("install KTF host-call stubs: %w", err)
	}
	if err := r.cpu.Map(
		guestHeapBase,
		guestHeapSize,
		cpu.PermissionRead|cpu.PermissionWrite,
	); err != nil {
		return fmt.Errorf("map KTF guest heap: %w", err)
	}
	r.heap = newGuestHeap(r.cpu, guestHeapBase, guestHeapSize)
	r.mapped = true
	return nil
}

// resetMappedMemory installs a pristine KTF image into mappings already owned
// by the machine. It is the reset counterpart of mapImageAndHost; remapping is
// deliberately avoided because CPU backends reject overlapping regions.
func (r *ktfRuntime) resetMappedMemory() error {
	for _, region := range []struct {
		address uint32
		size    uint32
		label   string
	}{
		{ktfImageBase, r.imageSz, "client image"},
		{DefaultStackBase, DefaultStackSize, "application stack"},
		{ktfHostBase, ktfHostSize, "host-call page"},
		{guestHeapBase, guestHeapSize, "guest heap"},
	} {
		if err := zeroGuestMemory(r.cpu, region.address, region.size); err != nil {
			return fmt.Errorf("reset KTF %s: %w", region.label, err)
		}
	}
	if err := r.cpu.WriteMemory(ktfImageBase, r.pkg.Client); err != nil {
		return fmt.Errorf("restore KTF client image: %w", err)
	}
	stubs := make([]byte, ktfHostSize)
	for offset := 0; offset < len(stubs); offset += 4 {
		copy(stubs[offset:], []byte{0x00, 0xbe, 0x00, 0xbf})
	}
	if err := r.cpu.WriteMemory(ktfHostBase, stubs); err != nil {
		return fmt.Errorf("restore KTF host-call stubs: %w", err)
	}
	r.heap = newGuestHeap(r.cpu, guestHeapBase, guestHeapSize)
	r.mapped = true
	return nil
}

func (r *ktfRuntime) call(
	ctx context.Context,
	procedure uint32,
	args []uint32,
	instructionLimit uint64,
) (result cpu.Result, returnValue uint32, returnedErr error) {
	if !r.mapped {
		return cpu.Result{}, 0, errors.New("KTF runtime memory is not mapped")
	}
	if procedure == 0 {
		return cpu.Result{}, 0, errors.New("KTF procedure is null")
	}
	if instructionLimit == 0 {
		return cpu.Result{}, 0, errors.New("KTF instruction limit is zero")
	}
	nativeParameterBase := r.nativeParameterBase
	r.nativeParameterBase = 0
	r.executionDepth++
	defer func() {
		r.executionDepth--
		r.nativeParameterBase = nativeParameterBase
	}()
	saved, err := r.cpu.SaveContext()
	if err != nil {
		return cpu.Result{Reason: cpu.StopFault, Err: err}, 0, err
	}
	callerStack, callerStackErr := r.cpu.ReadRegister(cpu.RegisterSP)
	if callerStackErr != nil {
		return cpu.Result{Reason: cpu.StopFault, Err: callerStackErr}, 0, callerStackErr
	}
	defer func() {
		if restoreErr := r.cpu.RestoreContext(saved); restoreErr != nil && returnedErr == nil {
			result = cpu.Result{Reason: cpu.StopFault, Err: restoreErr}
			returnValue = 0
			returnedErr = restoreErr
		}
	}()

	stackLimit := DefaultStackBase + DefaultStackSize
	stack := stackLimit - 0x100
	if callerStack >= DefaultStackBase && callerStack <= stackLimit {
		// A host callback can synchronously re-enter guest Java code. Grow that
		// call below the suspended guest frame instead of reusing the root stack
		// top and corrupting its locals.
		if callerStack < DefaultStackBase+0x100 {
			return cpu.Result{}, 0, errors.New("KTF nested call exhausted guest stack")
		}
		stack = callerStack - 0x100
	}
	if extra := len(args) - 4; extra > 0 {
		extraSize := uint32(extra * 4)
		if stack < DefaultStackBase+extraSize {
			return cpu.Result{}, 0, errors.New("KTF nested call exhausted guest stack")
		}
		stack -= extraSize
		for index, value := range args[4:] {
			var encoded [4]byte
			binary.LittleEndian.PutUint32(encoded[:], value)
			if err := r.cpu.WriteMemory(stack+uint32(index*4), encoded[:]); err != nil {
				return cpu.Result{Reason: cpu.StopFault, Err: err}, 0, err
			}
		}
	}
	for register := uint32(cpu.RegisterR0); register <= cpu.RegisterR3; register++ {
		var value uint32
		if int(register) < len(args) {
			value = args[register]
		}
		if err := r.cpu.WriteRegister(register, value); err != nil {
			return cpu.Result{Reason: cpu.StopFault, Err: err}, 0, err
		}
	}
	for _, registerValue := range []struct {
		register uint32
		value    uint32
	}{
		{register: cpu.RegisterSP, value: stack},
		{register: cpu.RegisterLR, value: ktfReturnSentinel | 1},
		{register: cpu.RegisterPC, value: procedure &^ 1},
		{register: cpu.RegisterCPSR, value: modeStatus(procedure)},
	} {
		if err := r.cpu.WriteRegister(registerValue.register, registerValue.value); err != nil {
			return cpu.Result{Reason: cpu.StopFault, Err: err}, 0, err
		}
	}
	mode := cpu.ModeARM
	if procedure&1 != 0 {
		mode = cpu.ModeThumb
	}
	pc := procedure &^ 1
	var instructions uint64
	for instructions < instructionLimit {
		run := r.cpu.Run(ctx, pc, mode, instructionLimit-instructions)
		instructions += run.Instructions
		run.Instructions = instructions
		result = run
		if run.Err != nil {
			registers := make([]uint32, cpu.RegisterR12+1)
			for register := range registers {
				registers[register], _ = r.cpu.ReadRegister(uint32(register))
			}
			sp, _ := r.cpu.ReadRegister(cpu.RegisterSP)
			lr, _ := r.cpu.ReadRegister(cpu.RegisterLR)
			status, _ := r.cpu.ReadRegister(cpu.RegisterCPSR)
			stack, _ := r.readWords(sp, 64)
			err := fmt.Errorf(
				"%w (r0-r12=%08x sp=%08x lr=%08x cpsr=%08x stack=%08x)",
				run.Err,
				registers,
				sp,
				lr,
				status,
				stack,
			)
			run.Err = err
			return run, 0, err
		}
		if run.Reason == cpu.StopBudget {
			if err := r.cpu.WriteRegister(cpu.RegisterPC, run.PC); err != nil {
				run.Reason = cpu.StopFault
				run.Err = err
				return run, 0, err
			}
			status := uint32(0)
			if mode == cpu.ModeThumb {
				status = cpu.StatusThumb
			}
			if err := r.cpu.WriteRegister(cpu.RegisterCPSR, status); err != nil {
				run.Reason = cpu.StopFault
				run.Err = err
				return run, 0, err
			}
			err := fmt.Errorf(
				"KTF procedure 0x%08x reached instruction budget at "+
					"0x%08x after %d instructions",
				procedure,
				run.PC,
				run.Instructions,
			)
			run.Err = err
			return run, 0, err
		}
		if run.Reason != cpu.StopBreakpoint || run.PC < 2 {
			err := fmt.Errorf(
				"KTF procedure 0x%08x stopped as %d at 0x%08x after %d instructions",
				procedure,
				run.Reason,
				run.PC,
				run.Instructions,
			)
			run.Reason = cpu.StopFault
			run.Err = err
			return run, 0, err
		}
		trap := run.PC - 2
		if trap == ktfReturnSentinel {
			returnValue, err = r.cpu.ReadRegister(cpu.RegisterR0)
			if err != nil {
				run.Reason = cpu.StopFault
				run.Err = err
				return run, 0, err
			}
			return run, returnValue, nil
		}
		host, ok := r.hostCalls[trap]
		if !ok {
			err := fmt.Errorf("unknown KTF host call at 0x%08x", trap)
			run.Reason = cpu.StopFault
			run.Err = err
			return run, 0, err
		}
		r.trace(host.name)
		value, err := host.handler(ctx, r)
		if err != nil {
			var unwind *ktfJavaExceptionUnwind
			if errors.As(err, &unwind) &&
				r.callOwnsJavaExceptionUnwind(callerStack, unwind) {
				r.trace("java_exception_unwind_boundary:call")
				pc, mode, err = r.applyJavaExceptionUnwind(unwind)
				if err != nil {
					run.Reason = cpu.StopFault
					run.Err = err
					return run, 0, err
				}
				continue
			}
			err = fmt.Errorf("KTF host call %s: %w", host.name, err)
			run.Reason = cpu.StopFault
			run.Err = err
			return run, 0, err
		}
		if strings.HasPrefix(host.name, "java.method.") {
			r.lastJavaReturn = value
		}
		if r.terminationRequested {
			run.Reason = cpu.StopExited
			return run, value, nil
		}
		if err := r.cpu.WriteRegister(cpu.RegisterR0, value); err != nil {
			return cpu.Result{Reason: cpu.StopFault, PC: trap, Err: err}, 0, err
		}
		lr, err := r.cpu.ReadRegister(cpu.RegisterLR)
		if err != nil {
			return cpu.Result{Reason: cpu.StopFault, PC: trap, Err: err}, 0, err
		}
		pc = lr &^ 1
		mode = cpu.ModeARM
		status := uint32(0)
		if lr&1 != 0 {
			mode = cpu.ModeThumb
			status = cpu.StatusThumb
		}
		if err := r.cpu.WriteRegister(cpu.RegisterPC, pc); err != nil {
			return cpu.Result{Reason: cpu.StopFault, PC: trap, Err: err}, 0, err
		}
		if err := r.cpu.WriteRegister(cpu.RegisterCPSR, status); err != nil {
			return cpu.Result{Reason: cpu.StopFault, PC: trap, Err: err}, 0, err
		}
	}
	err = fmt.Errorf("KTF procedure 0x%08x exceeded %d instructions", procedure, instructionLimit)
	return cpu.Result{
		Reason:       cpu.StopBudget,
		Instructions: instructionLimit,
		PC:           pc,
		Err:          err,
	}, 0, err
}

func (r *ktfRuntime) callOwnsJavaExceptionUnwind(
	callerStack uint32,
	unwind *ktfJavaExceptionUnwind,
) bool {
	if unwind == nil {
		return false
	}
	if r.executionDepth <= 1 {
		return true
	}
	// A synchronous re-entry starts below the suspended caller's SP. Exception
	// frames below that boundary belong to this call and must run their guest
	// restore trampoline here. Frames at or above it belong to the suspended
	// caller and remain for the outer execution boundary to unwind.
	return callerStack >= DefaultStackBase &&
		callerStack <= DefaultStackBase+DefaultStackSize &&
		unwind.target.contextBase >= DefaultStackBase &&
		unwind.target.contextBase < callerStack
}

func (r *ktfRuntime) queueJavaVirtual(
	instance uint32,
	name, descriptor string,
	args ...uint32,
) error {
	if !r.hasJavaTaskCapacity() {
		if len(r.pendingJavaCalls) >= ktfMaxPendingJavaCalls {
			return fmt.Errorf(
				"KTF pending Java call limit %d reached",
				ktfMaxPendingJavaCalls,
			)
		}
		r.pendingJavaCalls = append(r.pendingJavaCalls, ktfPendingJavaCall{
			instance:   instance,
			name:       name,
			descriptor: descriptor,
			args:       append([]uint32(nil), args...),
		})
		r.tracef(
			"java_task_defer:%s%s:instance=0x%08x:pending=%d",
			name,
			descriptor,
			instance,
			len(r.pendingJavaCalls),
		)
		return nil
	}
	_, err := r.queueJavaVirtualTask(instance, name, descriptor, args...)
	return err
}

func (r *ktfRuntime) activatePendingJavaCalls() error {
	for len(r.pendingJavaCalls) != 0 && r.hasJavaTaskCapacity() {
		call := r.pendingJavaCalls[0]
		copy(r.pendingJavaCalls, r.pendingJavaCalls[1:])
		r.pendingJavaCalls = r.pendingJavaCalls[:len(r.pendingJavaCalls)-1]
		if _, err := r.queueJavaVirtualTask(
			call.instance,
			call.name,
			call.descriptor,
			call.args...,
		); err != nil {
			return err
		}
	}
	return nil
}

func (r *ktfRuntime) queueJavaVirtualTask(
	instance uint32,
	name, descriptor string,
	args ...uint32,
) (*ktfTask, error) {
	if instance == 0 {
		return nil, fmt.Errorf(
			"queue Java method %s%s: instance is null",
			name,
			descriptor,
		)
	}
	taskIndex := len(r.tasks)
	for index, task := range r.tasks {
		if task.done {
			taskIndex = index
			break
		}
	}
	if taskIndex >= ktfMaxTasks {
		return nil, fmt.Errorf("KTF Java task limit %d reached", ktfMaxTasks)
	}
	instanceWords, err := r.readWords(instance, 2)
	if err != nil {
		return nil, err
	}
	methodAddress, err := r.resolveJavaMethod(instanceWords[1], name, descriptor)
	if err != nil {
		return nil, err
	}
	method, err := r.inspectJavaMethod(methodAddress)
	if err != nil {
		return nil, err
	}
	if method.Body == 0 {
		return nil, fmt.Errorf(
			"queue Java class 0x%08x method %s%s: method has no executable body",
			instanceWords[1],
			name,
			descriptor,
		)
	}
	callArgs := make([]uint32, 0, len(args)+2)
	callArgs = append(callArgs, 0, instance)
	callArgs = append(callArgs, args...)
	task, err := r.newTask(method.Body, callArgs, taskIndex)
	if err != nil {
		return nil, err
	}
	if taskIndex < len(r.tasks) {
		r.tasks[taskIndex] = task
	} else {
		r.tasks = append(r.tasks, task)
	}
	r.tracef(
		"java_task_queue:%s%s:instance=0x%08x:procedure=0x%08x",
		name,
		descriptor,
		instance,
		method.Body,
	)
	return task, nil
}

func (r *ktfRuntime) hasJavaTaskCapacity() bool {
	if len(r.tasks) < ktfMaxTasks {
		return true
	}
	for _, task := range r.tasks {
		if task.done {
			return true
		}
	}
	return false
}

// queueKeyEvent posts one handset key event to the card currently on the
// primary display. Returning false means the event must remain in Machine's
// input queue until a card or a task slot becomes available.
func (r *ktfRuntime) queueKeyEvent(pressed bool, key int32) (bool, error) {
	card := r.displayCards[r.defaultDisplay]
	if card == 0 || r.pendingKeyTask(card) != nil ||
		r.pendingWIPICTimerTask() != nil ||
		!r.hasJavaTaskCapacity() {
		return false, nil
	}
	if task := r.paintTasks[card]; task != nil && !task.done {
		return false, nil
	}
	eventType := ktfKeyReleased
	if pressed {
		eventType = ktfKeyPressed
	}
	task, err := r.queueJavaVirtualTask(
		card,
		"keyNotify",
		"(II)Z",
		eventType,
		uint32(key),
	)
	if err != nil {
		return false, err
	}
	task.keyCard = card
	r.tracef(
		"java_key_event:type=%d:key=%d:card=0x%08x",
		eventType,
		key,
		card,
	)
	return true, nil
}

func (r *ktfRuntime) pendingKeyTask(card uint32) *ktfTask {
	for _, task := range r.tasks {
		if task != nil && !task.done && task.keyCard == card {
			return task
		}
	}
	return nil
}

func (r *ktfRuntime) pendingWIPICTimerTask() *ktfTask {
	for _, task := range r.tasks {
		if task != nil && !task.done && task.wipicTimer {
			return task
		}
	}
	return nil
}

func (r *ktfRuntime) canAwaitEvents() bool {
	return !r.terminationRequested &&
		r.defaultDisplay != 0 &&
		r.displayCards[r.defaultDisplay] != 0
}

func (r *ktfRuntime) requestJavaTermination(instance uint32) {
	if r.terminationRequested {
		return
	}
	r.terminationRequested = true
	r.pendingJavaCalls = nil
	for _, task := range r.tasks {
		task.done = true
	}
	r.tracef(
		"java_lifecycle:notifyDestroyed:instance=0x%08x",
		instance,
	)
}

func (r *ktfRuntime) deferStartedThread(task *ktfTask) {
	parent := r.activeTask
	if task == nil || parent == nil || parent == task || parent.done {
		return
	}
	grace := ktfThreadStartGrace
	if r.defaultDisplay == 0 || r.displayCards[r.defaultDisplay] == 0 {
		grace = ktfInitialThreadStartGrace
	}
	task.startBlocker = parent
	parent.childStartGrace = grace + r.activeInstructions
	r.tracef(
		"java_thread_start_defer:grace=%d",
		grace,
	)
}

func (r *ktfRuntime) chargeThreadStartGrace(task *ktfTask, instructions uint64) {
	if task == nil || task.childStartGrace == 0 {
		return
	}
	if instructions < task.childStartGrace {
		task.childStartGrace -= instructions
		return
	}
	r.releaseStartedThreads(task, "grace")
}

func (r *ktfRuntime) releaseStartedThreads(parent *ktfTask, reason string) {
	if parent == nil {
		return
	}
	released := 0
	for _, task := range r.tasks {
		if task.startBlocker == parent {
			task.startBlocker = nil
			released++
		}
	}
	parent.childStartGrace = 0
	if released != 0 {
		r.tracef(
			"java_thread_start_release:reason=%s:tasks=%d",
			reason,
			released,
		)
	}
}

func (r *ktfRuntime) newTask(
	procedure uint32,
	args []uint32,
	index int,
) (*ktfTask, error) {
	saved, err := r.cpu.SaveContext()
	if err != nil {
		return nil, err
	}
	restore := func() error {
		return r.cpu.RestoreContext(saved)
	}
	stackTop := DefaultStackBase + DefaultStackSize -
		uint32(index)*ktfTaskStackSize
	stack := stackTop - 0x100
	if extra := len(args) - 4; extra > 0 {
		extraSize := uint32(extra * 4)
		if stack < DefaultStackBase+extraSize {
			_ = restore()
			return nil, errors.New("KTF Java task exhausted guest stack")
		}
		stack -= extraSize
		if err := r.writeWords(stack, args[4:]); err != nil {
			_ = restore()
			return nil, err
		}
	}
	for register := uint32(cpu.RegisterR0); register <= cpu.RegisterR12; register++ {
		var value uint32
		if int(register) < len(args) {
			value = args[register]
		}
		if err := r.cpu.WriteRegister(register, value); err != nil {
			_ = restore()
			return nil, err
		}
	}
	for _, registerValue := range []struct {
		register uint32
		value    uint32
	}{
		{register: cpu.RegisterSP, value: stack},
		{register: cpu.RegisterLR, value: ktfReturnSentinel | 1},
		{register: cpu.RegisterPC, value: procedure &^ 1},
		{register: cpu.RegisterCPSR, value: modeStatus(procedure)},
	} {
		if err := r.cpu.WriteRegister(registerValue.register, registerValue.value); err != nil {
			_ = restore()
			return nil, err
		}
	}
	taskContext, err := r.cpu.SaveContext()
	if err != nil {
		_ = restore()
		return nil, err
	}
	if err := restore(); err != nil {
		return nil, err
	}
	return &ktfTask{context: taskContext}, nil
}

func (r *ktfRuntime) runTaskSlice(
	ctx context.Context,
	instructionLimit uint64,
) cpu.Result {
	if instructionLimit == 0 {
		return cpu.Result{
			Reason: cpu.StopFault,
			Err:    errors.New("KTF task instruction limit is zero"),
		}
	}
	if r.terminationRequested {
		for _, task := range r.tasks {
			task.done = true
		}
		return cpu.Result{Reason: cpu.StopExited}
	}
	r.executionDepth++
	defer func() {
		r.executionDepth--
	}()
	if err := r.activatePendingJavaCalls(); err != nil {
		return cpu.Result{Reason: cpu.StopFault, Err: err}
	}
	if err := r.activateDueWIPICTimers(); err != nil {
		return cpu.Result{Reason: cpu.StopFault, Err: err}
	}
	task := r.nextRunnableTask()
	if task == nil {
		reason := cpu.StopExited
		if r.hasLiveTask() {
			reason = cpu.StopBudget
		}
		return cpu.Result{Reason: reason}
	}
	r.beginJavaTimerTask(task)
	lastJavaMethod := r.lastJavaMethod
	r.lastJavaMethod = task.lastJavaMethod
	r.activeTask = task
	r.activeInstructions = 0
	defer func() {
		r.chargeThreadStartGrace(task, r.activeInstructions)
		task.lastJavaMethod = r.lastJavaMethod
		r.lastJavaMethod = lastJavaMethod
		r.activeTask = nil
		r.activeInstructions = 0
	}()
	if err := r.cpu.RestoreContext(task.context); err != nil {
		return cpu.Result{Reason: cpu.StopFault, Err: err}
	}
	if err := r.restoreTaskExceptionFrame(task); err != nil {
		return cpu.Result{Reason: cpu.StopFault, Err: err}
	}
	taskIndex := -1
	for index, candidate := range r.tasks {
		if candidate == task {
			taskIndex = index
			break
		}
	}
	pc, err := r.cpu.ReadRegister(cpu.RegisterPC)
	if err != nil {
		return cpu.Result{Reason: cpu.StopFault, Err: err}
	}
	status, err := r.cpu.ReadRegister(cpu.RegisterCPSR)
	if err != nil {
		return cpu.Result{Reason: cpu.StopFault, PC: pc, Err: err}
	}
	mode := cpu.ModeARM
	if status&cpu.StatusThumb != 0 {
		mode = cpu.ModeThumb
	}
	stack, _ := r.cpu.ReadRegister(cpu.RegisterSP)
	register10, _ := r.cpu.ReadRegister(cpu.RegisterR10)
	link, _ := r.cpu.ReadRegister(cpu.RegisterLR)
	r.tracef(
		"java_task_slice:index=%d:pc=0x%08x:sp=0x%08x:r10=0x%08x:lr=0x%08x",
		taskIndex,
		pc,
		stack,
		register10,
		link,
	)
	r.yieldRequested = false
	var instructions uint64
	for instructions < instructionLimit {
		runBudget := instructionLimit - instructions
		run := r.cpu.Run(ctx, pc, mode, runBudget)
		instructions += run.Instructions
		r.activeInstructions = instructions
		run.Instructions = instructions
		if run.Err != nil {
			r.tracef(
				"java_task_fault:index=%d:pc=0x%08x:error=%v",
				taskIndex,
				run.PC,
				run.Err,
			)
			return run
		}
		if run.Reason == cpu.StopBudget {
			if err := r.saveTaskContext(task); err != nil {
				run.Reason = cpu.StopFault
				run.Err = err
			}
			return run
		}
		if run.Reason != cpu.StopBreakpoint || run.PC < 2 {
			run.Reason = cpu.StopFault
			run.Err = fmt.Errorf(
				"KTF task stopped as %d at 0x%08x after %d instructions",
				run.Reason,
				run.PC,
				run.Instructions,
			)
			return run
		}
		trap := run.PC - 2
		if trap == ktfReturnSentinel {
			task.done = true
			if err := r.completeJavaTimerTask(task); err != nil {
				run.Reason = cpu.StopFault
				run.Err = err
				return run
			}
			r.releaseStartedThreads(task, "return")
			if task.layoutOnReturn != 0 {
				instance := task.layoutOnReturn
				task.layoutOnReturn = 0
				if _, err := r.queueJavaVirtualTask(
					instance,
					"layout",
					"()V",
				); err != nil {
					run.Reason = cpu.StopFault
					run.Err = err
					return run
				}
				r.tracef(
					"java_main_layout:instance=0x%08x",
					instance,
				)
			}
			if err := r.releaseDeferredCardPaints(ctx, task); err != nil {
				run.Reason = cpu.StopFault
				run.Err = err
				return run
			}
			if task.presentOnReturn {
				task.presentOnReturn = false
				r.paintInitializedCards[task.paintCard] = true
				if err := r.recordPresentation(); err != nil {
					run.Reason = cpu.StopFault
					run.Err = err
					return run
				}
			}
			if !r.hasLiveTask() {
				run.Reason = cpu.StopExited
				return run
			}
			run.Reason = cpu.StopBudget
			return run
		}
		host, ok := r.hostCalls[trap]
		if !ok {
			run.Reason = cpu.StopFault
			run.Err = fmt.Errorf("unknown KTF host call at 0x%08x", trap)
			return run
		}
		r.trace(host.name)
		value, err := host.handler(ctx, r)
		if err != nil {
			var unwind *ktfJavaExceptionUnwind
			if errors.As(err, &unwind) {
				r.tracef(
					"java_exception_unwind_boundary:task=%d",
					taskIndex,
				)
				pc, mode, err = r.applyJavaExceptionUnwind(unwind)
				if err == nil {
					continue
				}
			}
			var unhandled *ktfUnhandledJavaException
			if task.bestEffortPaint && errors.As(err, &unhandled) {
				task.done = true
				task.bestEffortPaint = false
				delete(r.paintTasks, task.paintCard)
				r.tracef(
					"java_initial_paint_discard:%s:card=0x%08x",
					unhandled.name,
					task.paintCard,
				)
				run.Reason = cpu.StopBudget
				run.Err = nil
				if !r.hasLiveTask() {
					run.Reason = cpu.StopExited
				}
				return run
			}
			run.Reason = cpu.StopFault
			run.Err = fmt.Errorf("KTF host call %s: %w", host.name, err)
			return run
		}
		if strings.HasPrefix(host.name, "java.bridge.") {
			register10, _ := r.cpu.ReadRegister(cpu.RegisterR10)
			link, _ := r.cpu.ReadRegister(cpu.RegisterLR)
			stack, _ := r.cpu.ReadRegister(cpu.RegisterSP)
			r.tracef(
				"java_bridge_return:%s:r10=0x%08x:sp=0x%08x:lr=0x%08x",
				host.name,
				register10,
				stack,
				link,
			)
		}
		if strings.HasPrefix(host.name, "java.method.") {
			r.lastJavaReturn = value
		}
		if r.terminationRequested {
			run.Reason = cpu.StopExited
			return run
		}
		if err := r.cpu.WriteRegister(cpu.RegisterR0, value); err != nil {
			return cpu.Result{
				Reason:       cpu.StopFault,
				Instructions: instructions,
				PC:           trap,
				Err:          err,
			}
		}
		lr, err := r.cpu.ReadRegister(cpu.RegisterLR)
		if err != nil {
			return cpu.Result{
				Reason:       cpu.StopFault,
				Instructions: instructions,
				PC:           trap,
				Err:          err,
			}
		}
		pc = lr &^ 1
		mode = cpu.ModeARM
		status = 0
		if lr&1 != 0 {
			mode = cpu.ModeThumb
			status = cpu.StatusThumb
		}
		if err := r.cpu.WriteRegister(cpu.RegisterPC, pc); err != nil {
			return cpu.Result{
				Reason:       cpu.StopFault,
				Instructions: instructions,
				PC:           trap,
				Err:          err,
			}
		}
		if err := r.cpu.WriteRegister(cpu.RegisterCPSR, status); err != nil {
			return cpu.Result{
				Reason:       cpu.StopFault,
				Instructions: instructions,
				PC:           trap,
				Err:          err,
			}
		}
		if r.yieldRequested {
			r.yieldRequested = false
			r.releaseStartedThreads(task, "yield")
			if err := r.saveTaskContext(task); err != nil {
				return cpu.Result{
					Reason:       cpu.StopFault,
					Instructions: instructions,
					PC:           pc,
					Err:          err,
				}
			}
			if err := r.releaseDeferredCardPaints(ctx, task); err != nil {
				return cpu.Result{
					Reason:       cpu.StopFault,
					Instructions: instructions,
					PC:           pc,
					Err:          err,
				}
			}
			return cpu.Result{
				Reason:       cpu.StopBudget,
				Instructions: instructions,
				PC:           pc,
			}
		}
	}
	if err := r.saveTaskContext(task); err != nil {
		return cpu.Result{
			Reason:       cpu.StopFault,
			Instructions: instructions,
			PC:           pc,
			Err:          err,
		}
	}
	return cpu.Result{
		Reason:       cpu.StopBudget,
		Instructions: instructions,
		PC:           pc,
	}
}

func (r *ktfRuntime) activateDueWIPICTimers() error {
	// Carrier WIPI-C timer callbacks are serialized within an application.
	// Starting another callback while one is suspended at a host slice can
	// expose partially initialized Clet globals that the handset scheduler
	// would not make concurrently observable.
	for _, task := range r.tasks {
		if task != nil && !task.done &&
			(task.wipicTimer || task.keyCard != 0) {
			return nil
		}
	}
	for {
		var selected uint32
		found := false
		for address, timer := range r.wipicTimers {
			if timer == nil || !timer.active || timer.deadline > r.tickMS {
				continue
			}
			if !found || address < selected {
				selected = address
				found = true
			}
		}
		if !found {
			return nil
		}
		timer := r.wipicTimers[selected]
		if timer.callback == 0 {
			timer.active = false
			continue
		}
		taskIndex := len(r.tasks)
		for index, task := range r.tasks {
			if task.done {
				taskIndex = index
				break
			}
		}
		if taskIndex >= ktfMaxTasks {
			// Timer callbacks share the cooperative Java/WIPI-C task pool.
			// A full pool delays the callback until a later host slice; it is
			// not a guest fault and must not consume the one-shot timer.
			return nil
		}
		timer.active = false
		task, err := r.newTask(
			timer.callback,
			[]uint32{selected, timer.parameter},
			taskIndex,
		)
		if err != nil {
			return fmt.Errorf(
				"queue KTF WIPI-C timer 0x%08x callback 0x%08x: %w",
				selected,
				timer.callback,
				err,
			)
		}
		if taskIndex < len(r.tasks) {
			r.tasks[taskIndex] = task
		} else {
			r.tasks = append(r.tasks, task)
		}
		task.wipicTimer = true
		r.tracef(
			"wipic_timer_fire:timer=0x%08x:callback=0x%08x:parameter=0x%08x:tick=%d",
			selected,
			timer.callback,
			timer.parameter,
			r.tickMS,
		)
		// Only one native timer callback may be live at a time. Other expired
		// timers remain active and are selected after this callback returns.
		return nil
	}
}

func (r *ktfRuntime) drainServiceEvents(now time.Duration) error {
	for {
		event, ok := r.services.Events.Peek()
		if !ok || event.At > now {
			return nil
		}
		if event.Owner != 0 && event.Owner != r.serviceOwner {
			return fmt.Errorf(
				"KTF service event %d belongs to owner %d",
				event.Sequence,
				event.Owner,
			)
		}
		switch event.Kind {
		case shared.EventInputPress,
			shared.EventInputRelease,
			shared.EventInputRepeat:
			key, known := inputKeyCode(event.Control)
			if !known {
				r.tracef(
					"java_input_drop:control=%q:kind=%s",
					event.Control,
					event.Kind,
				)
				break
			}
			queued, err := r.queueKeyEvent(
				event.Kind != shared.EventInputRelease,
				int32(key),
			)
			if err != nil {
				return fmt.Errorf(
					"deliver shared KTF input %q: %w",
					event.Control,
					err,
				)
			}
			if !queued {
				return nil
			}
		case shared.EventAudioComplete:
			for instance, serviceID := range r.clipServices {
				if serviceID == event.ServiceID {
					if clip := r.clips[instance]; clip != nil {
						clip.playing = false
					}
					break
				}
			}
		}
		popped, ok := r.services.Events.PopReady(now)
		if !ok || popped.Sequence != event.Sequence {
			return fmt.Errorf(
				"KTF service event queue changed while delivering event %d",
				event.Sequence,
			)
		}
	}
}

func (r *ktfRuntime) nextRunnableTask() *ktfTask {
	if len(r.tasks) == 0 {
		return nil
	}
	for offset := range r.tasks {
		index := (r.taskCursor + offset) % len(r.tasks)
		task := r.tasks[index]
		if task.startBlocker != nil &&
			(task.startBlocker.done || task.startBlocker.childStartGrace == 0) {
			task.startBlocker = nil
		}
		if task.wakeAtMS != 0 && task.wakeAtMS <= r.tickMS {
			task.wakeAtMS = 0
		}
		if !task.done && task.startBlocker == nil && task.wakeAtMS == 0 {
			r.taskCursor = (index + 1) % len(r.tasks)
			return task
		}
	}
	return nil
}

func (r *ktfRuntime) hasRunnableTask() bool {
	for _, task := range r.tasks {
		if task.startBlocker != nil &&
			(task.startBlocker.done || task.startBlocker.childStartGrace == 0) {
			task.startBlocker = nil
		}
		if task.wakeAtMS != 0 && task.wakeAtMS <= r.tickMS {
			task.wakeAtMS = 0
		}
		if !task.done && task.startBlocker == nil && task.wakeAtMS == 0 {
			return true
		}
	}
	return false
}

func (r *ktfRuntime) hasLiveTask() bool {
	for _, task := range r.tasks {
		if task != nil && !task.done {
			return true
		}
	}
	return false
}

func (r *ktfRuntime) saveTaskContext(task *ktfTask) error {
	contextData, err := r.cpu.SaveContext()
	if err != nil {
		return err
	}
	task.context = contextData
	if r.exceptionContext != 0 {
		task.exceptionFrame, err = r.readU32(r.exceptionContext + 8*4)
		if err != nil {
			return err
		}
	}
	return nil
}

func (r *ktfRuntime) restoreTaskExceptionFrame(task *ktfTask) error {
	if r.exceptionContext == 0 {
		return nil
	}
	return r.writeU32(r.exceptionContext+8*4, task.exceptionFrame)
}

func (r *ktfRuntime) applyJavaExceptionUnwind(
	unwind *ktfJavaExceptionUnwind,
) (uint32, cpu.Mode, error) {
	if unwind == nil || unwind.target.contextBase == 0 ||
		unwind.target.restore == 0 {
		return 0, cpu.ModeARM, errors.New("invalid KTF Java exception unwind")
	}
	if err := r.cpu.WriteRegister(
		cpu.RegisterR0,
		unwind.target.contextBase,
	); err != nil {
		return 0, cpu.ModeARM, err
	}
	if err := r.cpu.WriteRegister(
		cpu.RegisterR1,
		unwind.target.handler,
	); err != nil {
		return 0, cpu.ModeARM, err
	}
	pc := unwind.target.restore &^ 1
	mode := cpu.ModeARM
	if unwind.target.restore&1 != 0 {
		mode = cpu.ModeThumb
	}
	if err := r.cpu.WriteRegister(cpu.RegisterPC, pc); err != nil {
		return 0, cpu.ModeARM, err
	}
	if err := r.cpu.WriteRegister(
		cpu.RegisterCPSR,
		modeStatus(unwind.target.restore),
	); err != nil {
		return 0, cpu.ModeARM, err
	}
	r.tracef(
		"java_exception_unwind:context=0x%08x:handler=0x%08x:restore=0x%08x",
		unwind.target.contextBase,
		unwind.target.handler,
		unwind.target.restore,
	)
	return pc, mode, nil
}

func (r *ktfRuntime) bootstrap(ctx context.Context) (cpu.Result, uint32, error) {
	result, address, err := r.call(
		ctx,
		ktfImageBase|1,
		[]uint32{r.pkg.BSSSize},
		ktfBootstrapInstructionMax,
	)
	if err != nil {
		return result, address, err
	}
	executable, err := r.inspectExecutable(address)
	if err != nil {
		return result, address, err
	}
	r.exe = executable
	return result, address, nil
}

func (r *ktfRuntime) initialize(ctx context.Context) error {
	if r.exe.InterfaceInit == 0 || r.exe.ExecutableInit == 0 {
		return errors.New("KTF executable has no initialization procedures")
	}
	getInterface := r.registerHostCall("get_interface", ktfGetInterface)
	javaThrow := r.registerHostCall("java_throw", ktfJavaThrow)
	javaThrowObject := r.registerHostCall("java_throw_object", ktfJavaThrowObject)
	javaCheckType := r.registerHostCall("java_check_type", ktfJavaCheckType)
	javaNew := r.registerHostCall("java_new", ktfJavaNew)
	javaArrayNew := r.registerHostCall("java_array_new", ktfJavaArrayNew)
	javaClassLoad := r.registerHostCall("java_class_load", ktfJavaClassLoad)
	javaStringCopy := r.registerHostCall(
		"java_string_copy",
		ktfJavaStringCopy,
	)
	alloc := r.registerHostCall("alloc", ktfAlloc)

	param0, err := r.allocateWords(1)
	if err != nil {
		return err
	}
	exceptionContext, err := r.allocateWords(ktfJavaEnvironmentWords)
	if err != nil {
		return err
	}
	r.exceptionContext = exceptionContext
	param1, err := r.allocateWords(1)
	if err != nil {
		return err
	}
	r.javaEnvironment = param1
	if err := r.writeWords(param1, []uint32{exceptionContext}); err != nil {
		return err
	}
	param2, err := r.allocateWords(3 + 128)
	if err != nil {
		return err
	}
	r.jvmContext = param2
	param3, err := r.allocateWords(12)
	if err != nil {
		return err
	}
	if err := r.writeWords(param3, []uint32{
		0, 0, 0, 0,
		'Z', 'C', 'F', 'D', 'B', 'S', 'I', 'J',
	}); err != nil {
		return err
	}
	param4, err := r.allocateWords(12)
	if err != nil {
		return err
	}
	if err := r.writeWords(param4, []uint32{
		getInterface,
		javaThrow,
		javaThrowObject,
		0,
		javaCheckType,
		javaNew,
		javaArrayNew,
		0,
		javaClassLoad,
		0,
		javaStringCopy,
		alloc,
	}); err != nil {
		return err
	}
	_, value, err := r.call(
		ctx,
		r.exe.InterfaceInit,
		[]uint32{param0, param1, param2, param3, param4},
		ktfBootstrapInstructionMax,
	)
	if err != nil {
		return fmt.Errorf("initialize KTF executable interface: %w", err)
	}
	if value != 0 {
		return fmt.Errorf("initialize KTF executable interface: status 0x%08x", value)
	}
	_, value, err = r.call(
		ctx,
		r.exe.ExecutableInit,
		nil,
		ktfBootstrapInstructionMax,
	)
	if err != nil {
		return fmt.Errorf("initialize KTF application: %w", err)
	}
	if value != 0 {
		return fmt.Errorf("initialize KTF application: status 0x%08x", value)
	}
	return nil
}

func (r *ktfRuntime) loadClass(ctx context.Context, name string) (ktfJavaClass, error) {
	if r.exe.GetClass == 0 {
		return ktfJavaClass{}, errors.New("KTF executable has no class lookup procedure")
	}
	candidates := []string{name}
	if strings.Contains(name, ".") {
		candidates = append(candidates, strings.ReplaceAll(name, ".", "/"))
	}
	for _, candidate := range candidates {
		nameAddress, err := r.allocateBytes([]byte(candidate), true)
		if err != nil {
			return ktfJavaClass{}, err
		}
		_, address, err := r.call(
			ctx,
			r.exe.GetClass,
			[]uint32{nameAddress},
			ktfBootstrapInstructionMax,
		)
		if err != nil {
			return ktfJavaClass{}, fmt.Errorf(
				"load KTF Java class %q: %w",
				candidate,
				err,
			)
		}
		if address != 0 {
			if candidate != name {
				r.tracef("java_main_class_alias:%s=%s", name, candidate)
			}
			class, err := r.inspectJavaClass(address)
			if err != nil {
				return ktfJavaClass{}, err
			}
			r.rememberRegisteredJavaClass(class.Name, class.Address)
			return class, nil
		}
	}
	return ktfJavaClass{}, fmt.Errorf("KTF Java class %q was not found", name)
}

func (r *ktfRuntime) inspectJavaClass(address uint32) (ktfJavaClass, error) {
	classWords, err := r.readWords(address, 5)
	if err != nil {
		return ktfJavaClass{}, err
	}
	descriptorWords, err := r.readWords(classWords[2], 9)
	if err != nil {
		return ktfJavaClass{}, err
	}
	name, err := r.readCString(descriptorWords[0], 1024)
	if err != nil {
		return ktfJavaClass{}, err
	}
	methodCount := uint16(descriptorWords[6])
	if methodCount > 4096 {
		return ktfJavaClass{}, fmt.Errorf(
			"KTF Java class %q has excessive method count %d",
			name,
			methodCount,
		)
	}
	methods := make([]ktfJavaMethod, 0, methodCount)
	for index := uint16(0); index < methodCount; index++ {
		methodAddress, err := r.readU32(descriptorWords[3] + uint32(index)*4)
		if err != nil {
			return ktfJavaClass{}, err
		}
		if methodAddress == 0 {
			continue
		}
		method, err := r.inspectJavaMethod(methodAddress)
		if err != nil {
			return ktfJavaClass{}, err
		}
		methods = append(methods, method)
	}
	return ktfJavaClass{
		Address:     address,
		Name:        name,
		Parent:      descriptorWords[2],
		VTable:      classWords[3],
		FieldSize:   uint16(descriptorWords[6] >> 16),
		AccessFlags: uint16(descriptorWords[7]),
		Methods:     methods,
	}, nil
}

func (r *ktfRuntime) inspectJavaMethod(address uint32) (ktfJavaMethod, error) {
	words, err := r.readWords(address, 7)
	if err != nil {
		return ktfJavaMethod{}, err
	}
	fullName, err := r.readCString(words[3]+1, 4096)
	if err != nil {
		return ktfJavaMethod{}, err
	}
	separator := strings.IndexByte(fullName, '+')
	if separator < 0 {
		return ktfJavaMethod{}, fmt.Errorf(
			"KTF Java method at 0x%08x has malformed name %q",
			address,
			fullName,
		)
	}
	return ktfJavaMethod{
		Address:           address,
		DeclaringClass:    words[1],
		Name:              fullName[separator+1:],
		Descriptor:        fullName[:separator],
		Body:              words[0],
		NativeBody:        words[2],
		ExceptionCount:    uint16(words[4]),
		ExceptionTableRaw: words[2],
		VTableIndex:       uint16(words[5]),
		AccessFlags:       uint16(words[5] >> 16),
	}, nil
}

func (r *ktfRuntime) startMainClass(ctx context.Context) error {
	class, err := r.loadClass(ctx, r.pkg.Descriptor.MainClass)
	if err != nil {
		return err
	}
	if err := r.ensureJavaClassInitialized(ctx, class); err != nil {
		return fmt.Errorf("initialize KTF MClass %q: %w", class.Name, err)
	}
	instance, err := r.newJavaInstanceForClass(class)
	if err != nil {
		return err
	}
	constructor, ok := findKTFJavaMethod(class, "<init>", "()V")
	if !ok {
		return fmt.Errorf("KTF MClass %q has no default constructor", class.Name)
	}
	result, _, err := r.call(
		ctx,
		constructor.Body,
		[]uint32{0, instance},
		ktfBootstrapInstructionMax,
	)
	if err != nil {
		return fmt.Errorf(
			"construct KTF MClass %q at PC 0x%08x after %d instructions: %w",
			class.Name,
			result.PC,
			result.Instructions,
			err,
		)
	}
	mainName, err := r.newJavaString(r.pkg.Descriptor.MainClass)
	if err != nil {
		return err
	}
	args, err := r.newJavaReferenceArray("[Ljava/lang/String;", []uint32{mainName})
	if err != nil {
		return err
	}
	start, ok := findKTFJavaMethod(class, "startApp", "([Ljava/lang/String;)V")
	if !ok {
		return fmt.Errorf("KTF MClass %q has no startApp(String[])", class.Name)
	}
	if r.deferThreads {
		task, err := r.newTask(
			start.Body,
			[]uint32{0, instance, args},
			len(r.tasks),
		)
		if err != nil {
			return fmt.Errorf("queue KTF MClass %q startApp: %w", class.Name, err)
		}
		if layout, ok := findKTFJavaMethod(class, "layout", "()V"); ok &&
			layout.Body != 0 {
			task.layoutOnReturn = instance
		}
		r.tasks = append(r.tasks, task)
		r.tracef(
			"java_task_queue:%s.startApp([Ljava/lang/String;)V:"+
				"instance=0x%08x:procedure=0x%08x",
			class.Name,
			instance,
			start.Body,
		)
		return nil
	}
	if result, _, err := r.call(
		ctx,
		start.Body,
		[]uint32{0, instance, args},
		ktfBootstrapInstructionMax,
	); err != nil {
		return fmt.Errorf(
			"start KTF MClass %q at PC 0x%08x after %d instructions: %w",
			class.Name,
			result.PC,
			result.Instructions,
			err,
		)
	}
	return nil
}

func findKTFJavaMethod(class ktfJavaClass, name, descriptor string) (ktfJavaMethod, bool) {
	// Host augmentation appends a concrete implementation after an abstract
	// or unusable framework declaration with the same signature. The last
	// declaration is therefore the effective one, matching vtable rebuilding.
	for index := len(class.Methods) - 1; index >= 0; index-- {
		method := class.Methods[index]
		if method.Name == name && method.Descriptor == descriptor {
			return method, true
		}
	}
	return ktfJavaMethod{}, false
}

func findKTFDeclaredJavaMethod(
	class ktfJavaClass,
	name, descriptor string,
) (ktfJavaMethod, bool) {
	for index := len(class.Methods) - 1; index >= 0; index-- {
		method := class.Methods[index]
		if method.DeclaringClass == class.Address &&
			method.Name == name &&
			method.Descriptor == descriptor {
			return method, true
		}
	}
	return ktfJavaMethod{}, false
}

func (r *ktfRuntime) ensureJavaClassInitialized(
	ctx context.Context,
	class ktfJavaClass,
) (returnedErr error) {
	switch r.javaClassInit[class.Address] {
	case ktfJavaClassInitializing, ktfJavaClassInitialized:
		return nil
	}
	r.javaClassInit[class.Address] = ktfJavaClassInitializing
	defer func() {
		if returnedErr != nil {
			delete(r.javaClassInit, class.Address)
			return
		}
		r.javaClassInit[class.Address] = ktfJavaClassInitialized
	}()

	if class.Parent != 0 {
		parent, err := r.inspectJavaClass(class.Parent)
		if err != nil {
			return fmt.Errorf(
				"inspect parent of Java class %q: %w",
				class.Name,
				err,
			)
		}
		if err := r.ensureJavaClassInitialized(ctx, parent); err != nil {
			return fmt.Errorf(
				"initialize parent of Java class %q: %w",
				class.Name,
				err,
			)
		}
	}
	initializer, ok := findKTFDeclaredJavaMethod(
		class,
		"<clinit>",
		"()V",
	)
	if !ok || initializer.Body == 0 {
		r.tracef("java_class_initialized:%s:no_clinit", class.Name)
		return nil
	}
	result, _, err := r.call(
		ctx,
		initializer.Body,
		nil,
		ktfBootstrapInstructionMax,
	)
	if err != nil {
		return fmt.Errorf(
			"run Java class initializer %s.<clinit>()V at PC 0x%08x "+
				"after %d instructions: %w",
			class.Name,
			result.PC,
			result.Instructions,
			err,
		)
	}
	r.tracef(
		"java_class_initialized:%s:<clinit>@0x%08x",
		class.Name,
		initializer.Body,
	)
	return nil
}

func (r *ktfRuntime) newJavaInstanceForClass(class ktfJavaClass) (uint32, error) {
	vtableIndex, err := r.ensureJavaVTableIndex(class.Address, class.VTable)
	if err != nil {
		return 0, err
	}
	instance, err := r.allocateWords(2)
	if err != nil {
		return 0, err
	}
	fields, err := r.heap.allocate(uint32(class.FieldSize)+4, true)
	if err != nil {
		return 0, err
	}
	if fields == 0 {
		return 0, errors.New("KTF guest heap exhausted allocating Java object fields")
	}
	if err := r.writeU32(fields, (vtableIndex*4)<<5); err != nil {
		return 0, err
	}
	if err := r.writeWords(instance, []uint32{fields, class.Address}); err != nil {
		return 0, err
	}
	return instance, nil
}

func (r *ktfRuntime) newJavaString(value string) (uint32, error) {
	instance, err := r.newJavaInstance("java/lang/String", 0)
	if err != nil {
		return 0, err
	}
	codeUnits := utf16.Encode([]rune(value))
	characters, err := r.newJavaArray(
		"[C",
		uint32(len(codeUnits)),
		2,
	)
	if err != nil {
		return 0, err
	}
	fields, err := r.readU32(characters)
	if err != nil {
		return 0, err
	}
	encoded := make([]byte, len(codeUnits)*2)
	for index, codeUnit := range codeUnits {
		binary.LittleEndian.PutUint16(encoded[index*2:], codeUnit)
	}
	if err := r.cpu.WriteMemory(fields+8, encoded); err != nil {
		return 0, err
	}
	if err := r.writeJavaFieldWord(instance, 0, characters); err != nil {
		return 0, err
	}
	if err := r.writeJavaFieldWord(instance, 4, 0); err != nil {
		return 0, err
	}
	if err := r.writeJavaFieldWord(
		instance,
		8,
		uint32(len(codeUnits)),
	); err != nil {
		return 0, err
	}
	r.javaStrings[instance] = value
	return instance, nil
}

func (r *ktfRuntime) newJavaReferenceArray(
	className string,
	elements []uint32,
) (uint32, error) {
	instance, err := r.newJavaArray(className, uint32(len(elements)), 4)
	if err != nil {
		return 0, err
	}
	instanceWords, err := r.readWords(instance, 2)
	if err != nil {
		return 0, err
	}
	if err := r.writeWords(instanceWords[0]+8, elements); err != nil {
		return 0, err
	}
	return instance, nil
}

func (r *ktfRuntime) newJavaArray(
	className string,
	count uint32,
	elementSize uint32,
) (uint32, error) {
	if elementSize == 0 || elementSize > 8 {
		return 0, fmt.Errorf("invalid KTF Java array element size %d", elementSize)
	}
	if uint64(count)*uint64(elementSize)+8 > uint64(^uint32(0)) {
		return 0, fmt.Errorf(
			"KTF Java array allocation overflows: count=%d element_size=%d",
			count,
			elementSize,
		)
	}
	classAddress, err := r.ensureJavaClass(className)
	if err != nil {
		return 0, err
	}
	class, err := r.inspectJavaClass(classAddress)
	if err != nil {
		return 0, err
	}
	vtableIndex, err := r.ensureJavaVTableIndex(class.Address, class.VTable)
	if err != nil {
		return 0, err
	}
	instance, err := r.allocateWords(2)
	if err != nil {
		return 0, err
	}
	fields, err := r.heap.allocate(count*elementSize+8, true)
	if err != nil {
		return 0, err
	}
	if fields == 0 {
		return 0, errors.New("KTF guest heap exhausted allocating Java array")
	}
	if err := r.writeU32(fields, (vtableIndex*4)<<5); err != nil {
		return 0, err
	}
	if err := r.writeWords(instance, []uint32{fields, class.Address}); err != nil {
		return 0, err
	}
	if err := r.writeU32(fields+4, count); err != nil {
		return 0, err
	}
	return instance, nil
}

func (r *ktfRuntime) registerHostCall(name string, handler ktfHostHandler) uint32 {
	address := r.nextHostCall
	if address+4 > ktfHostBase+ktfHostSize {
		panic("KTF host-call page exhausted")
	}
	r.nextHostCall += 4
	r.hostCalls[address] = ktfHostCall{name: name, handler: handler}
	return address | 1
}

func (r *ktfRuntime) allocateWords(count uint32) (uint32, error) {
	if count > ^uint32(0)/4 {
		return 0, errors.New("KTF allocation word count overflows")
	}
	address, err := r.heap.allocate(count*4, true)
	if err != nil {
		return 0, err
	}
	if address == 0 {
		return 0, fmt.Errorf("KTF guest heap exhausted allocating %d words", count)
	}
	return address, nil
}

func (r *ktfRuntime) allocateBytes(data []byte, terminate bool) (uint32, error) {
	size := len(data)
	if terminate {
		size++
	}
	if uint64(size) > uint64(^uint32(0)) {
		return 0, errors.New("KTF byte allocation exceeds uint32")
	}
	address, err := r.heap.allocate(uint32(size), true)
	if err != nil {
		return 0, err
	}
	if address == 0 {
		return 0, fmt.Errorf("KTF guest heap exhausted allocating %d bytes", size)
	}
	if err := r.cpu.WriteMemory(address, data); err != nil {
		return 0, err
	}
	return address, nil
}

func (r *ktfRuntime) writeWords(address uint32, words []uint32) error {
	data := make([]byte, len(words)*4)
	for index, value := range words {
		binary.LittleEndian.PutUint32(data[index*4:], value)
	}
	if err := r.cpu.WriteMemory(address, data); err != nil {
		return fmt.Errorf("write KTF structure at 0x%08x: %w", address, err)
	}
	return nil
}

func (r *ktfRuntime) parameter(index uint32) (uint32, error) {
	if r.nativeParameterBase != 0 {
		if index == 0 {
			return 0, nil
		}
		return r.readU32(r.nativeParameterBase + (index-1)*4)
	}
	if index < 4 {
		return r.cpu.ReadRegister(cpu.RegisterR0 + index)
	}
	stack, err := r.cpu.ReadRegister(cpu.RegisterSP)
	if err != nil {
		return 0, err
	}
	var data [4]byte
	if err := r.cpu.ReadMemory(stack+(index-4)*4, data[:]); err != nil {
		return 0, err
	}
	return binary.LittleEndian.Uint32(data[:]), nil
}

func ktfGetInterface(_ context.Context, runtime *ktfRuntime) (uint32, error) {
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

func (r *ktfRuntime) lookupInterface(name string) (uint32, error) {
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

func (r *ktfRuntime) buildMXUserMemInterface() (uint32, error) {
	if r.mxUserMemInterface != 0 {
		return r.mxUserMemInterface, nil
	}
	slots := []uint32{
		r.registerHostCall("mxusermem.add", ktfIncrementalMemoryAdd),
		r.registerHostCall("mxusermem.alloc", ktfIncrementalMemoryAllocate),
		r.registerHostCall("mxusermem.realloc", ktfIncrementalMemoryReallocate),
		r.registerHostCall("mxusermem.free", ktfIncrementalMemoryFree),
	}
	address, err := r.allocateWords(uint32(len(slots)))
	if err != nil {
		return 0, err
	}
	if err := r.writeWords(address, slots); err != nil {
		return 0, err
	}
	r.mxUserMemInterface = address
	return address, nil
}

func ktfAlloc(_ context.Context, runtime *ktfRuntime) (uint32, error) {
	size, err := runtime.parameter(0)
	if err != nil {
		return 0, err
	}
	address, err := runtime.heap.allocate(size, false)
	if err != nil {
		return 0, err
	}
	return address, nil
}

func ktfUnsupportedJavaCallback(name string) ktfHostHandler {
	return func(context.Context, *ktfRuntime) (uint32, error) {
		return 0, fmt.Errorf("%s is not implemented", name)
	}
}

func ktfJavaThrow(_ context.Context, runtime *ktfRuntime) (uint32, error) {
	runtime.snapshotJavaThrow()
	nameAddress, err := runtime.parameter(0)
	if err != nil {
		return 0, err
	}
	if nameAddress == 0 {
		return 0, errors.New("KTF Java exception has a null class name")
	}
	name, err := runtime.readCString(nameAddress, 1024)
	if err != nil {
		return 0, fmt.Errorf(
			"read KTF Java exception class at 0x%08x: %w",
			nameAddress,
			err,
		)
	}
	runtime.rememberJavaThrowName(name)
	detail, _ := runtime.parameter(1)
	return runtime.raiseJavaException(name, detail)
}

func ktfJavaThrowObject(_ context.Context, runtime *ktfRuntime) (uint32, error) {
	runtime.snapshotJavaThrow()
	detail, err := runtime.parameter(0)
	if err != nil {
		return 0, err
	}
	if detail == 0 {
		return 0, errors.New("KTF Java exception object is null")
	}
	classAddress, err := runtime.readU32(detail + 4)
	if err != nil {
		return 0, fmt.Errorf(
			"read KTF Java exception object class at 0x%08x: %w",
			detail,
			err,
		)
	}
	class, err := runtime.inspectJavaClass(classAddress)
	if err != nil {
		return 0, fmt.Errorf(
			"inspect KTF Java exception object class at 0x%08x: %w",
			classAddress,
			err,
		)
	}
	runtime.rememberJavaThrowName(class.Name)
	return runtime.raiseJavaException(class.Name, detail)
}

func (r *ktfRuntime) snapshotJavaThrow() {
	r.lastJavaThrowRegisters = make([]uint32, cpu.RegisterR12+1)
	for register := range r.lastJavaThrowRegisters {
		r.lastJavaThrowRegisters[register], _ =
			r.cpu.ReadRegister(uint32(register))
	}
	r.lastJavaThrowSP, _ = r.cpu.ReadRegister(cpu.RegisterSP)
	r.lastJavaThrowStack, _ = r.readWords(r.lastJavaThrowSP, 64)
	r.tracef("java_throw_snapshot:sp=0x%08x", r.lastJavaThrowSP)
}

func (r *ktfRuntime) rememberJavaThrowName(name string) {
	r.lastJavaThrowName = name
	if r.firstJavaThrowName != "" {
		return
	}
	r.firstJavaThrowName = name
	r.firstJavaThrowRegisters = append(
		[]uint32(nil),
		r.lastJavaThrowRegisters...,
	)
	r.firstJavaThrowSP = r.lastJavaThrowSP
	r.firstJavaThrowStack = append(
		[]uint32(nil),
		r.lastJavaThrowStack...,
	)
}

func (r *ktfRuntime) raiseJavaException(
	name string,
	detail uint32,
) (uint32, error) {
	if detail == 0 {
		var err error
		detail, err = r.newHostJavaObject(name)
		if err != nil {
			return 0, fmt.Errorf(
				"construct KTF Java exception %s: %w",
				name,
				err,
			)
		}
	}
	r.javaExceptionFrames = r.javaExceptionFrames[:0]
	target, caught, dispatchErr := r.dispatchJavaException(name, detail)
	if dispatchErr != nil {
		return 0, fmt.Errorf("dispatch KTF Java exception %s: %w", name, dispatchErr)
	}
	if caught {
		r.tracef(
			"java_exception_caught:%s@0x%08x:restore=0x%08x",
			name,
			target.handler,
			target.restore,
		)
		return 0, &ktfJavaExceptionUnwind{target: target}
	}
	exceptionState, _ := r.readWords(
		r.exceptionContext,
		ktfJavaEnvironmentWords,
	)
	if r.lastJavaMethod != "" {
		return 0, &ktfUnhandledJavaException{
			name:   name,
			detail: detail,
			context: fmt.Sprintf(
				"after=%s, return=0x%08x, jump=0x%08x, "+
					"call_lr=0x%08x, frames=%v, state=%08x",
				r.lastJavaMethod,
				r.lastJavaReturn,
				r.lastJavaJump,
				r.lastJavaCallLR,
				r.javaExceptionFrames,
				exceptionState,
			),
		}
	}
	return 0, &ktfUnhandledJavaException{
		name:    name,
		detail:  detail,
		context: fmt.Sprintf("state=%08x", exceptionState),
	}
}

func (r *ktfRuntime) dispatchJavaException(
	name string,
	detail uint32,
) (ktfJavaExceptionTarget, bool, error) {
	if r.exceptionContext == 0 {
		return ktfJavaExceptionTarget{}, false, nil
	}
	frame, err := r.readU32(r.exceptionContext + 8*4)
	if err != nil {
		return ktfJavaExceptionTarget{}, false, err
	}
	for depth := 0; frame != 0; depth++ {
		if depth >= 4096 {
			return ktfJavaExceptionTarget{}, false, errors.New(
				"KTF Java exception frame chain exceeds limit",
			)
		}
		frameWords, err := r.readWords(frame, 6)
		if err != nil {
			return ktfJavaExceptionTarget{}, false, fmt.Errorf(
				"inspect Java exception frame 0x%08x: %w",
				frame,
				err,
			)
		}
		methodAddress := frameWords[0]
		previousFrame := frameWords[2]
		bytecodePC := frameWords[3]
		methodWords, err := r.readWords(methodAddress, 7)
		if err != nil {
			return ktfJavaExceptionTarget{}, false, fmt.Errorf(
				"inspect Java exception method 0x%08x: %w",
				methodAddress,
				err,
			)
		}
		methodName := fmt.Sprintf("0x%08x", methodAddress)
		if method, inspectErr := r.inspectJavaMethod(methodAddress); inspectErr == nil {
			methodName = method.Name + method.Descriptor
			if declaring, classErr := r.inspectJavaClass(
				methodWords[1],
			); classErr == nil {
				methodName = declaring.Name + "." + methodName
			}
		}
		frameTrace := fmt.Sprintf(
			"java_exception_frame:%s:bcp=%d:frame=0x%08x",
			methodName,
			bytecodePC,
			frame,
		)
		r.trace(frameTrace)
		r.javaExceptionFrames = append(r.javaExceptionFrames, frameTrace)
		exceptionCount := int(methodWords[4] & 0xffff)
		if exceptionCount > 4096 {
			return ktfJavaExceptionTarget{}, false, fmt.Errorf(
				"Java method 0x%08x has excessive exception count %d",
				methodAddress,
				exceptionCount,
			)
		}
		table, err := r.readWords(methodWords[2], exceptionCount)
		if err != nil {
			return ktfJavaExceptionTarget{}, false, fmt.Errorf(
				"inspect Java exception table for method 0x%08x: %w",
				methodAddress,
				err,
			)
		}
		for _, entryAddress := range table {
			entry, err := r.readWords(entryAddress, 4)
			if err != nil {
				return ktfJavaExceptionTarget{}, false, fmt.Errorf(
					"inspect Java exception entry 0x%08x: %w",
					entryAddress,
					err,
				)
			}
			if bytecodePC < entry[0] || bytecodePC > entry[1] {
				continue
			}
			matches, err := r.javaExceptionMatches(name, entry[3])
			if err != nil {
				return ktfJavaExceptionTarget{}, false, err
			}
			if !matches {
				continue
			}
			if err := r.writeU32(frame+4*4, detail); err != nil {
				return ktfJavaExceptionTarget{}, false, err
			}
			handler := entry[2]
			if handler == 0 {
				handler = 1
			}
			// Move the frame's bytecode cursor to the handler before
			// resuming. Compiled KTF methods only publish a bytecode PC at
			// the points that can throw inside the protected region, so a
			// frame left pointing at the original throw makes any exception
			// raised by the handler body look like it came from inside the
			// same try. This method wraps and rethrows in its catch block,
			// which then caught itself forever until the guest heap ran out.
			if err := r.writeU32(frame+3*4, handler); err != nil {
				return ktfJavaExceptionTarget{}, false, err
			}
			if err := r.writeU32(r.exceptionContext+8*4, frame); err != nil {
				return ktfJavaExceptionTarget{}, false, err
			}
			restore, err := r.readU32(frameWords[5] + 4)
			if err != nil {
				return ktfJavaExceptionTarget{}, false, fmt.Errorf(
					"resolve Java exception restore function for frame 0x%08x: %w",
					frame,
					err,
				)
			}
			if restore == 0 {
				return ktfJavaExceptionTarget{}, false, fmt.Errorf(
					"Java exception frame 0x%08x has no restore function",
					frame,
				)
			}
			return ktfJavaExceptionTarget{
				contextBase: frame + 6*4,
				handler:     handler,
				restore:     restore,
			}, true, nil
		}
		frame = previousFrame
	}
	return ktfJavaExceptionTarget{}, false, nil
}

func (r *ktfRuntime) javaExceptionMatches(
	thrownName string,
	catchClass uint32,
) (bool, error) {
	if catchClass == 0 {
		return true, nil
	}
	catch, err := r.inspectJavaClass(catchClass)
	if err != nil {
		return false, fmt.Errorf(
			"inspect Java exception catch class 0x%08x: %w",
			catchClass,
			err,
		)
	}
	if thrownClass := r.javaClasses[thrownName]; thrownClass != 0 {
		for depth, address := 0, thrownClass; address != 0; depth++ {
			if depth >= 256 {
				return false, fmt.Errorf(
					"Java exception hierarchy for %q exceeds limit",
					thrownName,
				)
			}
			if address == catchClass {
				return true, nil
			}
			class, err := r.inspectJavaClass(address)
			if err != nil {
				return false, err
			}
			address = class.Parent
		}
	}
	for depth, current := 0, thrownName; current != ""; depth++ {
		if depth >= 256 {
			return false, fmt.Errorf(
				"Java exception name hierarchy for %q exceeds limit",
				thrownName,
			)
		}
		if current == catch.Name {
			return true, nil
		}
		parent, known := ktfJavaExceptionParents[current]
		if !known {
			if strings.HasSuffix(current, "Exception") {
				parent = "java/lang/Exception"
			} else {
				break
			}
		}
		current = parent
	}
	return false, nil
}

func ktfJavaCheckType(_ context.Context, runtime *ktfRuntime) (uint32, error) {
	targetClass, err := runtime.parameter(0)
	if err != nil {
		return 0, err
	}
	instance, err := runtime.parameter(1)
	if err != nil {
		return 0, err
	}
	unknown, err := runtime.parameter(2)
	if err != nil {
		return 0, err
	}
	if instance == 0 {
		return 0, nil
	}
	instanceWords, err := runtime.readWords(instance, 2)
	if err != nil {
		return 0, fmt.Errorf(
			"inspect KTF Java instance at 0x%08x: %w",
			instance,
			err,
		)
	}
	actualClass := instanceWords[1]
	if actualClass == 0 {
		return 0, fmt.Errorf(
			"KTF Java instance at 0x%08x has a null class",
			instance,
		)
	}
	actual, err := runtime.inspectJavaClass(actualClass)
	if err != nil {
		return 0, fmt.Errorf(
			"inspect KTF Java instance class at 0x%08x: %w",
			actualClass,
			err,
		)
	}
	// KTF's reference runtime treats every array check and the unknown
	// nonzero form as successful before consulting the Java type system.
	if strings.HasPrefix(actual.Name, "[") || unknown != 0 {
		return 1, nil
	}
	if targetClass == 0 {
		return 0, nil
	}
	for depth := 0; ; depth++ {
		if depth >= 256 {
			return 0, fmt.Errorf(
				"KTF Java class hierarchy for %q exceeds limit",
				actual.Name,
			)
		}
		if actual.Address == targetClass {
			return 1, nil
		}
		if actual.Parent == 0 {
			return 0, nil
		}
		actual, err = runtime.inspectJavaClass(actual.Parent)
		if err != nil {
			return 0, fmt.Errorf(
				"inspect KTF Java parent class at 0x%08x: %w",
				actual.Parent,
				err,
			)
		}
	}
}

func ktfJavaClassLoad(_ context.Context, runtime *ktfRuntime) (uint32, error) {
	target, err := runtime.parameter(0)
	if err != nil {
		return 0, err
	}
	nameAddress, err := runtime.parameter(1)
	if err != nil {
		return 0, err
	}
	name, err := runtime.readCString(nameAddress, 1024)
	if err != nil {
		return 0, err
	}
	runtime.tracef("java_class_load:%s@0x%08x", name, target)
	class, err := runtime.ensureJavaClass(name)
	if err != nil {
		return 0, err
	}
	if err := runtime.writeWords(target, []uint32{class}); err != nil {
		return 0, err
	}
	return 0, nil
}

func ktfJavaNew(ctx context.Context, runtime *ktfRuntime) (uint32, error) {
	classAddress, err := runtime.parameter(0)
	if err != nil {
		return 0, err
	}
	if classAddress == 0 {
		return 0, errors.New("Java class pointer is null")
	}
	class, err := runtime.inspectJavaClass(classAddress)
	if err != nil {
		return 0, fmt.Errorf("inspect Java class at 0x%08x: %w", classAddress, err)
	}
	if err := runtime.ensureJavaClassInitialized(ctx, class); err != nil {
		return 0, err
	}
	instance, err := runtime.newJavaInstanceForClass(class)
	if err != nil {
		return 0, fmt.Errorf("allocate Java instance of %q: %w", class.Name, err)
	}
	instanceWords, _ := runtime.readWords(instance, 2)
	header, _ := runtime.readU32(instanceWords[0])
	runtime.tracef(
		"java_new:%s@0x%08x:fields=0x%08x:header=0x%08x",
		class.Name,
		instance,
		instanceWords[0],
		header,
	)
	return instance, nil
}

func ktfJavaArrayNew(_ context.Context, runtime *ktfRuntime) (uint32, error) {
	elementType, err := runtime.parameter(0)
	if err != nil {
		return 0, err
	}
	count, err := runtime.parameter(1)
	if err != nil {
		return 0, err
	}
	className, elementSize, err := runtime.javaArrayClass(elementType)
	if err != nil {
		return 0, err
	}
	instance, err := runtime.newJavaArray(className, count, elementSize)
	if err != nil {
		return 0, fmt.Errorf(
			"allocate Java array %s[%d]: %w",
			className,
			count,
			err,
		)
	}
	runtime.tracef(
		"java_array_new:%s[%d]@0x%08x",
		className,
		count,
		instance,
	)
	return instance, nil
}

// ktfJavaStringCopy implements the AOT VM callback used by native Clet
// wrappers to materialize a Java String as a zero-terminated byte string.
// The carrier runtime passes the destination capacity including the trailing
// zero. WIPI application and class names are ASCII; UTF-8 also preserves
// deterministic behavior for other host-created strings.
func ktfJavaStringCopy(
	_ context.Context,
	runtime *ktfRuntime,
) (uint32, error) {
	source, err := runtime.parameter(0)
	if err != nil {
		return 0, err
	}
	destination, err := runtime.parameter(1)
	if err != nil {
		return 0, err
	}
	capacity, err := runtime.parameter(2)
	if err != nil {
		return 0, err
	}
	if capacity == 0 {
		return 0, nil
	}
	if destination == 0 {
		return 0, errors.New("copy KTF Java string: destination is null")
	}
	encoded := []byte(runtime.javaStringValue(source))
	copyCount := uint32(len(encoded))
	if copyCount >= capacity {
		copyCount = capacity - 1
	}
	output := make([]byte, int(copyCount)+1)
	copy(output, encoded[:copyCount])
	if err := runtime.cpu.WriteMemory(destination, output); err != nil {
		return 0, fmt.Errorf("copy KTF Java string: %w", err)
	}
	runtime.tracef(
		"java_string_copy:source=0x%08x:destination=0x%08x:"+
			"capacity=%d:bytes=%d",
		source,
		destination,
		capacity,
		copyCount,
	)
	return copyCount, nil
}

func (r *ktfRuntime) javaArrayClass(elementType uint32) (string, uint32, error) {
	if elementType <= 0x100 {
		descriptor := byte(elementType)
		elementSizes := map[byte]uint32{
			'Z': 1,
			'B': 1,
			'C': 2,
			'S': 2,
			'I': 4,
			'F': 4,
			'J': 8,
			'D': 8,
		}
		elementSize, ok := elementSizes[descriptor]
		if !ok {
			return "", 0, fmt.Errorf(
				"unsupported KTF Java primitive array type 0x%02x",
				elementType,
			)
		}
		return "[" + string(descriptor), elementSize, nil
	}
	class, err := r.inspectJavaClass(elementType)
	if err != nil {
		return "", 0, fmt.Errorf(
			"inspect KTF Java array element class at 0x%08x: %w",
			elementType,
			err,
		)
	}
	switch {
	case strings.HasPrefix(class.Name, "["):
		// The KTF multi-array helper passes the full array class for each
		// recursive level. Unlike anewarray, it does not pass the component
		// class, so prepending another '[' creates an extra dimension.
		return class.Name, 4, nil
	case strings.HasPrefix(class.Name, "L") &&
		strings.HasSuffix(class.Name, ";"):
		return "[" + class.Name, 4, nil
	default:
		return "[L" + class.Name + ";", 4, nil
	}
}

func (r *ktfRuntime) ensureJavaClass(name string) (uint32, error) {
	if class := r.javaClasses[name]; class != 0 {
		if _, ok := ktfHostJavaClassSpecs[name]; ok {
			if err := r.augmentHostJavaClass(class, name); err != nil {
				return 0, err
			}
		}
		return class, nil
	}
	if name == "" || len(name) > 1024 {
		return 0, fmt.Errorf("invalid Java class name %q", name)
	}
	spec, hasSpec := ktfHostJavaClassSpecs[name]
	parentName := spec.parent
	if !hasSpec && name != "java/lang/Object" {
		parentName = "java/lang/Object"
	}
	var parent uint32
	if parentName != "" {
		var err error
		parent, err = r.ensureJavaClass(parentName)
		if err != nil {
			return 0, err
		}
	}
	class, err := r.allocateWords(5)
	if err != nil {
		return 0, err
	}
	nameAddress, err := r.allocateBytes([]byte(name), true)
	if err != nil {
		return 0, err
	}
	methods, err := r.allocateWords(1)
	if err != nil {
		return 0, err
	}
	fields, err := r.allocateWords(1)
	if err != nil {
		return 0, err
	}
	vtable, err := r.allocateWords(1)
	if err != nil {
		return 0, err
	}
	descriptor, err := r.allocateWords(9)
	if err != nil {
		return 0, err
	}
	if err := r.writeWords(descriptor, []uint32{
		nameAddress,
		0,
		parent,
		methods,
		0,
		fields,
		uint32(spec.fieldSize) << 16,
		0x21,
		0,
	}); err != nil {
		return 0, err
	}
	if err := r.writeWords(class, []uint32{
		class + 4,
		0,
		descriptor,
		vtable,
		8 << 16,
	}); err != nil {
		return 0, err
	}
	r.javaClasses[name] = class
	r.javaClassGeneration++
	r.hostJavaClass[class] = true
	if err := r.rebuildHostJavaVTable(class); err != nil {
		return 0, err
	}
	for _, method := range spec.methods {
		if _, err := r.addHostJavaMethod(
			ktfJavaClass{Address: class, Name: name},
			method.name,
			method.descriptor,
		); err != nil {
			return 0, err
		}
	}
	return class, nil
}

func (r *ktfRuntime) augmentHostJavaClass(classAddress uint32, name string) error {
	spec, ok := ktfHostJavaClassSpecs[name]
	if !ok {
		return nil
	}
	class, err := r.inspectJavaClass(classAddress)
	if err != nil {
		return err
	}
	r.hostJavaClass[classAddress] = true
	for _, wanted := range spec.methods {
		method, found := findKTFJavaMethod(
			class,
			wanted.name,
			wanted.descriptor,
		)
		if found && (method.Body != 0 || method.NativeBody != 0) {
			_, bodyIsHost := r.hostCalls[method.Body&^1]
			if name != "java/util/Enumeration" || bodyIsHost {
				continue
			}
		}
		if _, err := r.addHostJavaMethod(
			class,
			wanted.name,
			wanted.descriptor,
		); err != nil {
			return err
		}
		class, err = r.inspectJavaClass(classAddress)
		if err != nil {
			return err
		}
	}
	return nil
}

func (r *ktfRuntime) ensureJavaVTableIndex(
	classAddress uint32,
	vtableAddress uint32,
) (uint32, error) {
	if vtableAddress != 0 {
		r.javaVTableClasses[vtableAddress] = classAddress
	}
	if index, ok := r.javaVTables[classAddress]; ok {
		if err := r.writeJavaVTable(index, vtableAddress); err != nil {
			return 0, err
		}
		return index, nil
	}
	if len(r.javaVTables) >= 128 {
		return 0, errors.New("KTF Java vtable registry exhausted")
	}
	index := uint32(len(r.javaVTables))
	r.javaVTables[classAddress] = index
	if err := r.writeJavaVTable(index, vtableAddress); err != nil {
		return 0, err
	}
	return index, nil
}

func (r *ktfRuntime) writeJavaVTable(index, address uint32) error {
	if index >= 128 {
		return fmt.Errorf("KTF Java vtable index %d exceeds registry", index)
	}
	if r.jvmContext == 0 {
		return nil
	}
	return r.writeU32(r.jvmContext+12+index*4, address)
}

func (r *ktfRuntime) rebuildHostJavaVTable(classAddress uint32) error {
	class, err := r.inspectJavaClass(classAddress)
	if err != nil {
		return err
	}
	hierarchy := make([]ktfJavaClass, 0, 8)
	for depth := 0; ; depth++ {
		if depth >= 256 {
			return fmt.Errorf("Java class hierarchy for %q exceeds limit", class.Name)
		}
		hierarchy = append(hierarchy, class)
		if class.Parent == 0 {
			break
		}
		class, err = r.inspectJavaClass(class.Parent)
		if err != nil {
			return err
		}
	}
	methods := make([]ktfJavaMethod, 0)
	for index := len(hierarchy) - 1; index >= 0; index-- {
		for _, method := range hierarchy[index].Methods {
			if _, compatibilitySlot := r.hostJavaVirtualSlots[method.Address]; compatibilitySlot {
				continue
			}
			replaced := false
			for current := range methods {
				if methods[current].Name == method.Name &&
					methods[current].Descriptor == method.Descriptor {
					methods[current] = method
					replaced = true
					break
				}
			}
			if !replaced {
				methods = append(methods, method)
			}
		}
	}
	logicalSize := uint32(len(methods))
	for _, slot := range r.hostJavaVirtualSlots {
		if size := uint32(slot) + 1; size > logicalSize {
			logicalSize = size
		}
	}
	capacity := uint32(len(methods) + 1)
	if existing := r.javaVTableCapacity[classAddress]; existing > capacity {
		capacity = existing
	}
	if capacity < logicalSize {
		capacity = ktfHostVirtualTableReserve
		for capacity < logicalSize {
			if capacity > uint32(^uint16(0))/2 {
				capacity = logicalSize
				break
			}
			capacity *= 2
		}
	}
	entries := make([]uint32, capacity)
	for index, method := range methods {
		entries[index] = method.Address
		flags, err := r.readU32(method.Address + 20)
		if err != nil {
			return err
		}
		if err := r.writeU32(
			method.Address+20,
			flags&0xffff0000|uint32(index),
		); err != nil {
			return err
		}
	}
	for methodAddress, slot := range r.hostJavaVirtualSlots {
		if entries[slot] != 0 && entries[slot] != methodAddress {
			method, inspectErr := r.inspectJavaMethod(methodAddress)
			if inspectErr != nil {
				return inspectErr
			}
			compatible, hierarchyErr := r.javaClassExtends(
				classAddress,
				method.DeclaringClass,
			)
			if hierarchyErr != nil {
				return hierarchyErr
			}
			if !compatible {
				r.tracef(
					"java_compat_vtable_collision:class=%s:slot=%d:"+
						"guest=0x%08x:host=0x%08x:preserved",
					hierarchy[0].Name,
					slot,
					entries[slot],
					methodAddress,
				)
				continue
			}
		}
		entries[slot] = methodAddress
		flags, err := r.readU32(methodAddress + 20)
		if err != nil {
			return err
		}
		if err := r.writeU32(
			methodAddress+20,
			flags&0xffff0000|uint32(slot),
		); err != nil {
			return err
		}
	}
	vtable, err := r.allocateWords(uint32(len(entries)))
	if err != nil {
		return err
	}
	if err := r.writeWords(vtable, entries); err != nil {
		return err
	}
	if err := r.writeU32(classAddress+12, vtable); err != nil {
		return err
	}
	classFlags, err := r.readU32(classAddress + 16)
	if err != nil {
		return err
	}
	if err := r.writeU32(
		classAddress+16,
		classFlags&0xffff0000|logicalSize,
	); err != nil {
		return err
	}
	r.javaVTableCapacity[classAddress] = capacity
	vtableIndex, err := r.ensureJavaVTableIndex(classAddress, vtable)
	if err == nil {
		// KTF AOT code can use a class definition itself as the receiver for
		// framework-owned methods. ptr_next points at this word, so encode the
		// class vtable in the same form used by an instance fields header.
		err = r.writeU32(classAddress+4, (vtableIndex*4)<<5)
	}
	if err == nil {
		r.tracef(
			"java_vtable:%s:class=0x%08x:slot=%d@0x%08x[%d]",
			hierarchy[0].Name,
			classAddress,
			vtableIndex,
			vtable,
			len(methods),
		)
	}
	return err
}

func (r *ktfRuntime) buildKnlInterface() (uint32, error) {
	if r.knlInterface != 0 {
		return r.knlInterface, nil
	}
	const slotCount = 65
	slots := make([]uint32, slotCount)
	for index := range slots {
		handler := ktfNoop
		switch index {
		case 1:
			handler = ktfKernelSprintk
		case 20:
			handler = ktfKernelAllocate(false)
		case 21:
			handler = ktfKernelAllocate(true)
		case 22:
			handler = ktfKernelFree
		case 23:
			handler = ktfTotalMemory
		case 24:
			handler = ktfFreeMemory
		case 25:
			handler = ktfKernelDefineTimer
		case 26:
			handler = ktfKernelSetTimer
		case 27:
			handler = ktfKernelUnsetTimer
		case 28:
			handler = ktfKernelCurrentTime
		case 29:
			handler = ktfKernelGetSystemProperty
		case 30:
			handler = ktfKernelSetSystemProperty
		case 31:
			handler = ktfGetResourceID
		case 32:
			handler = ktfGetResource
		case 33:
			handler = ktfGetWIPICInterface
		case 36:
			handler = ktfKernelGetDLLInterface
		}
		slots[index] = r.registerHostCall(
			fmt.Sprintf("wipic.knl.%d", index),
			handler,
		)
	}
	address, err := r.allocateWords(slotCount)
	if err != nil {
		return 0, err
	}
	if err := r.writeWords(address, slots); err != nil {
		return 0, err
	}
	r.knlInterface = address
	return address, nil
}

func (r *ktfRuntime) buildJBInterface() (uint32, error) {
	if r.jbInterface != 0 {
		return r.jbInterface, nil
	}
	const slotCount = 13
	slots := make([]uint32, slotCount)
	for index := 1; index < slotCount; index++ {
		handler := ktfUnsupportedJavaCallback(fmt.Sprintf("java bridge slot %d", index))
		switch index {
		case 1:
			handler = ktfJavaJump(1)
		case 2:
			handler = ktfJavaJump(2)
		case 3:
			handler = ktfJavaJump(3)
		case 4:
			handler = ktfGetJavaMethod
		case 5:
			handler = ktfGetJavaField
		case 6, 7, 8, 9:
			handler = ktfNoop
		case 10:
			handler = ktfRegisterJavaClass
		case 11:
			handler = ktfRegisterJavaString
		case 12:
			handler = ktfCallNative
		}
		slots[index] = r.registerHostCall(
			fmt.Sprintf("java.bridge.%d", index),
			handler,
		)
	}
	address, err := r.allocateWords(slotCount)
	if err != nil {
		return 0, err
	}
	if err := r.writeWords(address, slots); err != nil {
		return 0, err
	}
	r.jbInterface = address
	return address, nil
}

func ktfGetJavaMethod(ctx context.Context, runtime *ktfRuntime) (uint32, error) {
	class, err := runtime.parameter(0)
	if err != nil {
		return 0, err
	}
	nameAddress, err := runtime.parameter(1)
	if err != nil {
		return 0, err
	}
	name, descriptor, err := runtime.readJavaFullName(nameAddress)
	if err != nil {
		parameters := make([]uint32, 4)
		for index := range parameters {
			parameters[index], _ = runtime.parameter(uint32(index))
		}
		return 0, fmt.Errorf(
			"resolve Java method class=0x%08x name=0x%08x parameters=%08x: %w",
			class,
			nameAddress,
			parameters,
			err,
		)
	}
	method, err := runtime.resolveJavaMethod(class, name, descriptor)
	if err != nil {
		return 0, fmt.Errorf(
			"resolve Java method %s%s from 0x%08x: %w",
			name,
			descriptor,
			class,
			err,
		)
	}
	resolved, err := runtime.inspectJavaMethod(method)
	if err != nil {
		return 0, err
	}
	if resolved.AccessFlags&0x0008 != 0 {
		methodWords, err := runtime.readWords(method, 2)
		if err != nil {
			return 0, err
		}
		declaring, err := runtime.inspectJavaClass(methodWords[1])
		if err != nil {
			return 0, err
		}
		if err := runtime.ensureJavaClassInitialized(ctx, declaring); err != nil {
			return 0, err
		}
	}
	runtime.lastJavaMethod = name + descriptor
	if methodWords, methodErr := runtime.readWords(method, 2); methodErr == nil {
		if declaring, classErr := runtime.inspectJavaClass(
			methodWords[1],
		); classErr == nil {
			runtime.lastJavaMethod = declaring.Name + "." +
				runtime.lastJavaMethod
		}
	}
	lr, _ := runtime.cpu.ReadRegister(cpu.RegisterLR)
	runtime.tracef(
		"java_method:%s%s@0x%08x:from=0x%08x:lr=0x%08x",
		name,
		descriptor,
		method,
		class,
		lr,
	)
	return method, nil
}

func ktfGetJavaField(ctx context.Context, runtime *ktfRuntime) (uint32, error) {
	classAddress, err := runtime.parameter(0)
	if err != nil {
		return 0, err
	}
	nameAddress, err := runtime.parameter(1)
	if err != nil {
		return 0, err
	}
	name, descriptor, err := runtime.readJavaFullName(nameAddress)
	if err != nil {
		return 0, err
	}
	class, err := runtime.inspectJavaClass(classAddress)
	if err != nil {
		return 0, err
	}
	field, err := runtime.resolveJavaField(class, name, descriptor)
	if err != nil {
		return 0, err
	}
	fieldWords, err := runtime.readWords(field, 4)
	if err != nil {
		return 0, err
	}
	if fieldWords[0]&0x0008 != 0 {
		declaring, err := runtime.inspectJavaClass(fieldWords[1])
		if err != nil {
			return 0, err
		}
		if err := runtime.ensureJavaClassInitialized(ctx, declaring); err != nil {
			return 0, err
		}
	}
	runtime.tracef(
		"java_field:%s.%s%s@0x%08x",
		class.Name,
		name,
		descriptor,
		field,
	)
	return field, nil
}

func (r *ktfRuntime) readJavaFullName(address uint32) (string, string, error) {
	if address == 0 {
		return "", "", errors.New("Java full-name pointer is null")
	}
	value, err := r.readCString(address+1, 4096)
	if err != nil {
		return "", "", err
	}
	separator := strings.IndexByte(value, '+')
	if separator < 0 {
		return "", "", fmt.Errorf("malformed Java full name %q", value)
	}
	return value[separator+1:], value[:separator], nil
}

func (r *ktfRuntime) resolveJavaField(
	class ktfJavaClass,
	name string,
	descriptor string,
) (uint32, error) {
	classWords, err := r.readWords(class.Address, 5)
	if err != nil {
		return 0, err
	}
	descriptorWords, err := r.readWords(classWords[2], 9)
	if err != nil {
		return 0, err
	}
	if !strings.HasPrefix(class.Name, "[") && descriptorWords[5] != 0 {
		for index := uint32(0); index < 4096; index++ {
			field, err := r.readU32(descriptorWords[5] + index*4)
			if err != nil {
				return 0, err
			}
			if field == 0 {
				break
			}
			words, err := r.readWords(field, 4)
			if err != nil {
				return 0, err
			}
			fieldName, fieldDescriptor, err := r.readJavaFullName(words[2])
			if err != nil {
				return 0, err
			}
			if fieldName == name && fieldDescriptor == descriptor {
				return field, nil
			}
		}
	}
	if r.hostJavaClass[class.Address] {
		return r.addHostJavaField(class, name, descriptor)
	}
	if class.Parent != 0 {
		parent, parentErr := r.inspectJavaClass(class.Parent)
		if parentErr != nil {
			return 0, parentErr
		}
		if field, parentErr := r.resolveJavaField(
			parent,
			name,
			descriptor,
		); parentErr == nil {
			return field, nil
		}
	}
	return 0, fmt.Errorf(
		"Java field %s.%s%s was not found",
		class.Name,
		name,
		descriptor,
	)
}

func (r *ktfRuntime) addHostJavaField(
	class ktfJavaClass,
	name string,
	descriptor string,
) (uint32, error) {
	if name == "" || descriptor == "" {
		return 0, errors.New("host Java field name or descriptor is empty")
	}
	fullName := append([]byte{0}, []byte(descriptor+"+"+name)...)
	nameAddress, err := r.allocateBytes(fullName, true)
	if err != nil {
		return 0, err
	}
	field, err := r.allocateWords(4)
	if err != nil {
		return 0, err
	}
	if err := r.writeWords(field, []uint32{
		0x0009,
		class.Address,
		nameAddress,
		0,
	}); err != nil {
		return 0, err
	}
	value, err := r.hostJavaStaticFieldValue(class.Name, name)
	if err != nil {
		return 0, err
	}
	if err := r.writeU32(field+12, value); err != nil {
		return 0, err
	}
	classWords, err := r.readWords(class.Address, 5)
	if err != nil {
		return 0, err
	}
	descriptorWords, err := r.readWords(classWords[2], 9)
	if err != nil {
		return 0, err
	}
	fields := make([]uint32, 0, 8)
	if descriptorWords[5] != 0 {
		for index := uint32(0); index < 4096; index++ {
			value, err := r.readU32(descriptorWords[5] + index*4)
			if err != nil {
				return 0, err
			}
			if value == 0 {
				break
			}
			fields = append(fields, value)
		}
	}
	fields = append(fields, field, 0)
	table, err := r.allocateWords(uint32(len(fields)))
	if err != nil {
		return 0, err
	}
	if err := r.writeWords(table, fields); err != nil {
		return 0, err
	}
	if err := r.writeU32(classWords[2]+20, table); err != nil {
		return 0, err
	}
	return field, nil
}

func (r *ktfRuntime) hostJavaStaticFieldValue(
	className, name string,
) (uint32, error) {
	if className == "java/lang/System" {
		switch name {
		case "in":
			if r.systemInputStream == 0 {
				instance, err := r.newHostJavaObject("java/io/InputStream")
				if err != nil {
					return 0, err
				}
				r.systemInputStream = instance
				r.inputStreams[instance] = &ktfInputStream{}
			}
			return r.systemInputStream, nil
		case "out", "err":
			if r.systemPrintStream == 0 {
				instance, err := r.newHostJavaObject("java/io/PrintStream")
				if err != nil {
					return 0, err
				}
				r.systemPrintStream = instance
			}
			return r.systemPrintStream, nil
		}
	}
	if className == "org/kwis/msp/lcdui/Font" {
		switch name {
		case "STYLE_BOLD":
			return 1, nil
		case "STYLE_ITALIC":
			return 2, nil
		case "STYLE_UNDERLINED":
			return 4, nil
		case "SIZE_SMALL":
			return 8, nil
		case "SIZE_LARGE":
			return 16, nil
		case "FACE_MONOSPACE":
			return 32, nil
		case "FACE_PROPORTIONAL":
			return 64, nil
		}
	}
	if className == "org/kwis/msp/lcdui/Graphics" {
		switch name {
		case "HCENTER":
			return 1, nil
		case "VCENTER":
			return 2, nil
		case "LEFT":
			return 4, nil
		case "RIGHT":
			return 8, nil
		case "TOP":
			return 16, nil
		case "BOTTOM":
			return 32, nil
		case "BASELINE":
			return 64, nil
		}
	}
	return 0, nil
}

func (r *ktfRuntime) resolveJavaMethod(
	classOrVTable uint32,
	name string,
	descriptor string,
) (uint32, error) {
	if classAddress := r.javaVTableClasses[classOrVTable]; classAddress != 0 {
		return r.resolveJavaMethod(classAddress, name, descriptor)
	}
	first, err := r.readU32(classOrVTable)
	if err != nil {
		return 0, err
	}
	if first != classOrVTable+4 {
		// KTF AOT virtual-call helpers may pass an object reference directly
		// instead of its class descriptor. Object references are two words:
		// fields pointer followed by the actual class pointer. Prefer that
		// unambiguous route before treating the first word as a raw vtable.
		if classAddress, classErr := r.readU32(classOrVTable + 4); classErr == nil &&
			classAddress != 0 {
			if _, inspectErr := r.inspectJavaClass(classAddress); inspectErr == nil {
				return r.resolveJavaMethod(classAddress, name, descriptor)
			}
		}
		// Application class descriptors can store their parent as a one-word
		// indirection whose first word is the actual class definition. This is
		// distinct from an object reference and is common when a guest class
		// extends a framework class loaded through java_class_load.
		if first != 0 {
			if _, inspectErr := r.inspectJavaClass(first); inspectErr == nil {
				return r.resolveJavaMethod(first, name, descriptor)
			}
		}
		table := first
		// Some AOT call sites pass the vtable itself, whose first word is
		// already a Java method descriptor, while others pass a holder whose
		// first word points at that table.
		if _, methodErr := r.inspectJavaMethod(first); methodErr == nil {
			table = classOrVTable
		}
		for index := uint32(0); index < 4096; index++ {
			methodAddress, err := r.readU32(table + index*4)
			if err != nil {
				return 0, err
			}
			if methodAddress == 0 {
				break
			}
			method, err := r.inspectJavaMethod(methodAddress)
			if err != nil {
				referenceWords, _ := r.readWords(classOrVTable, 4)
				tableWords, _ := r.readWords(table, 4)
				return 0, fmt.Errorf(
					"inspect Java vtable method %d at 0x%08x "+
						"(reference=0x%08x words=%08x "+
						"table=0x%08x words=%08x): %w",
					index,
					methodAddress,
					classOrVTable,
					referenceWords,
					table,
					tableWords,
					err,
				)
			}
			if method.Name == name && method.Descriptor == descriptor {
				return method.Address, nil
			}
		}
		return 0, fmt.Errorf(
			"Java method %s%s is absent from vtable 0x%08x",
			name,
			descriptor,
			table,
		)
	}

	class, err := r.inspectJavaClass(classOrVTable)
	if err != nil {
		return 0, err
	}
	if method, ok := findKTFJavaMethod(class, name, descriptor); ok {
		if method.Body != 0 || method.NativeBody != 0 ||
			!r.hostJavaClass[class.Address] {
			return method.Address, nil
		}
	}
	if r.hostJavaClass[class.Address] {
		return r.addHostJavaMethod(class, name, descriptor)
	}
	if class.Parent != 0 {
		if method, parentErr := r.resolveJavaMethod(class.Parent, name, descriptor); parentErr == nil {
			return method, nil
		}
	}
	return 0, fmt.Errorf(
		"Java method %s.%s%s was not found",
		class.Name,
		name,
		descriptor,
	)
}

func (r *ktfRuntime) addHostJavaMethod(
	class ktfJavaClass,
	name string,
	descriptor string,
) (uint32, error) {
	stub := r.registerHostCall(
		fmt.Sprintf("java.method.%s.%s%s", class.Name, name, descriptor),
		ktfHostJavaMethod(class.Name, name, descriptor),
	)
	fullName := append([]byte{0}, []byte(descriptor+"+"+name)...)
	nameAddress, err := r.allocateBytes(fullName, true)
	if err != nil {
		return 0, err
	}
	methodAddress, err := r.allocateWords(7)
	if err != nil {
		return 0, err
	}
	accessFlags := uint16(1)
	declaredByHostSpec := false
	if spec, ok := ktfHostJavaClassSpecs[class.Name]; ok {
		for _, method := range spec.methods {
			if method.name == name && method.descriptor == descriptor {
				accessFlags = method.access
				declaredByHostSpec = true
				break
			}
		}
	}
	body := stub
	nativeBody := uint32(0)
	if accessFlags&0x0100 != 0 {
		body = 0
		nativeBody = stub
	}
	if err := r.writeWords(methodAddress, []uint32{
		body,
		class.Address,
		nativeBody,
		nameAddress,
		0,
		uint32(accessFlags) << 16,
		0,
	}); err != nil {
		return 0, err
	}
	classWords, err := r.readWords(class.Address, 5)
	if err != nil {
		return 0, err
	}
	descriptorWords, err := r.readWords(classWords[2], 9)
	if err != nil {
		return 0, err
	}
	oldCount := uint16(descriptorWords[6])
	methods := make([]uint32, 0, int(oldCount)+2)
	for index := uint16(0); index < oldCount; index++ {
		value, err := r.readU32(descriptorWords[3] + uint32(index)*4)
		if err != nil {
			return 0, err
		}
		methods = append(methods, value)
	}
	methods = append(methods, methodAddress, 0)
	table, err := r.allocateWords(uint32(len(methods)))
	if err != nil {
		return 0, err
	}
	if err := r.writeWords(table, methods); err != nil {
		return 0, err
	}
	if err := r.writeU32(classWords[2]+12, table); err != nil {
		return 0, err
	}
	countAndFields := descriptorWords[6]&0xffff0000 | uint32(oldCount+1)
	if err := r.writeU32(classWords[2]+24, countAndFields); err != nil {
		return 0, err
	}
	compatibilityVirtual := !declaredByHostSpec &&
		accessFlags&(0x0002|0x0008) == 0 &&
		!strings.HasPrefix(name, "<")
	if compatibilityVirtual {
		if err := r.registerHostJavaVirtualMethod(methodAddress); err != nil {
			return 0, err
		}
	}
	if err := r.rebuildHostJavaVTable(class.Address); err != nil {
		return 0, err
	}
	if compatibilityVirtual {
		if err := r.installHostJavaVirtualMethod(methodAddress); err != nil {
			return 0, err
		}
	}
	return methodAddress, nil
}

func (r *ktfRuntime) registerHostJavaVirtualMethod(methodAddress uint32) error {
	if _, exists := r.hostJavaVirtualSlots[methodAddress]; exists {
		return nil
	}
	if r.nextHostVirtualSlot == ^uint16(0) {
		return errors.New("KTF host Java compatibility vtable exhausted")
	}
	slot := r.nextHostVirtualSlot
	r.nextHostVirtualSlot++
	r.hostJavaVirtualSlots[methodAddress] = slot
	flags, err := r.readU32(methodAddress + 20)
	if err != nil {
		return err
	}
	return r.writeU32(
		methodAddress+20,
		flags&0xffff0000|uint32(slot),
	)
}

func (r *ktfRuntime) installHostJavaVirtualMethod(methodAddress uint32) error {
	slot, ok := r.hostJavaVirtualSlots[methodAddress]
	if !ok {
		return fmt.Errorf(
			"KTF host Java method 0x%08x has no compatibility vtable slot",
			methodAddress,
		)
	}
	for classAddress := range r.javaVTables {
		if err := r.installHostJavaVirtualMethodForClass(
			classAddress,
			methodAddress,
			slot,
		); err != nil {
			return err
		}
	}
	return nil
}

func (r *ktfRuntime) installHostJavaVirtualMethodForClass(
	classAddress uint32,
	methodAddress uint32,
	slot uint16,
) error {
	classWords, err := r.readWords(classAddress, 5)
	if err != nil {
		return err
	}
	required := uint32(slot) + 1
	logicalSize := classWords[4] & 0xffff
	capacity := r.javaVTableCapacity[classAddress]
	if capacity < required {
		newCapacity := ktfHostVirtualTableReserve
		requiredCapacity := max(required, logicalSize)
		for newCapacity < requiredCapacity {
			if newCapacity > uint32(^uint16(0))/2 {
				newCapacity = requiredCapacity
				break
			}
			newCapacity *= 2
		}
		entries := make([]uint32, newCapacity)
		copyCount := logicalSize
		if copyCount > capacity && capacity != 0 {
			copyCount = capacity
		}
		if copyCount != 0 {
			existing, err := r.readWords(classWords[3], int(copyCount))
			if err != nil {
				return err
			}
			copy(entries, existing)
		}
		vtable, err := r.allocateWords(newCapacity)
		if err != nil {
			return err
		}
		if err := r.writeWords(vtable, entries); err != nil {
			return err
		}
		if err := r.writeU32(classAddress+12, vtable); err != nil {
			return err
		}
		if _, err := r.ensureJavaVTableIndex(classAddress, vtable); err != nil {
			return err
		}
		r.javaVTableClasses[vtable] = classAddress
		r.javaVTableCapacity[classAddress] = newCapacity
		classWords[3] = vtable
	}
	existing, err := r.readU32(classWords[3] + uint32(slot)*4)
	if err != nil {
		return err
	}
	if existing != 0 && existing != methodAddress {
		className := fmt.Sprintf("0x%08x", classAddress)
		if class, inspectErr := r.inspectJavaClass(classAddress); inspectErr == nil {
			className = class.Name
		}
		method, inspectErr := r.inspectJavaMethod(methodAddress)
		if inspectErr != nil {
			return inspectErr
		}
		compatible, hierarchyErr := r.javaClassExtends(
			classAddress,
			method.DeclaringClass,
		)
		if hierarchyErr != nil {
			return hierarchyErr
		}
		if !compatible {
			r.tracef(
				"java_compat_vtable_collision:class=%s:slot=%d:"+
					"guest=0x%08x:host=0x%08x:preserved",
				className,
				slot,
				existing,
				methodAddress,
			)
			return nil
		}
	}
	if err := r.writeU32(
		classWords[3]+uint32(slot)*4,
		methodAddress,
	); err != nil {
		return err
	}
	if logicalSize < required {
		if err := r.writeU32(
			classAddress+16,
			classWords[4]&0xffff0000|required,
		); err != nil {
			return err
		}
	}
	return nil
}

func (r *ktfRuntime) javaClassExtends(classAddress, parentAddress uint32) (bool, error) {
	for depth := 0; classAddress != 0; depth++ {
		if depth >= 256 {
			return false, errors.New("KTF Java class hierarchy exceeds limit")
		}
		if classAddress == parentAddress {
			return true, nil
		}
		class, err := r.inspectJavaClass(classAddress)
		if err != nil {
			return false, err
		}
		classAddress = class.Parent
	}
	return false, nil
}

func ktfCallNative(ctx context.Context, runtime *ktfRuntime) (uint32, error) {
	address, err := runtime.parameter(0)
	if err != nil {
		return 0, err
	}
	parameters, err := runtime.parameter(1)
	if err != nil {
		return 0, err
	}
	if signature, ok := runtime.javaNativeMethodSignature(address); ok {
		if runtime.lastJavaMethod != signature {
			runtime.tracef(
				"java_native_method_correct:%s->%s@0x%08x",
				runtime.lastJavaMethod,
				signature,
				address,
			)
		}
		runtime.lastJavaMethod = signature
	}
	runtime.tracef(
		"java_native_call:%s@0x%08x",
		runtime.lastJavaMethod,
		address,
	)
	override, hasOverride := ktfJavaNativeOverride(runtime.lastJavaMethod)
	if address == 0 && !hasOverride {
		if runtime.lastJavaMethod != "" {
			return 0, fmt.Errorf(
				"KTF Java native method target is null for %s",
				runtime.lastJavaMethod,
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
		runtime.trace(host.name)
		nativeParameterBase := runtime.nativeParameterBase
		runtime.nativeParameterBase = parameters
		value, err = host.handler(ctx, runtime)
		runtime.nativeParameterBase = nativeParameterBase
		if err != nil {
			nativeParameters, _ := runtime.readWords(parameters, 10)
			return 0, fmt.Errorf(
				"call Java host method 0x%08x with parameters %08x: %w",
				address,
				nativeParameters,
				err,
			)
		}
	} else {
		if runtime.javaEnvironment != 0 {
			environment, _ := runtime.readU32(runtime.javaEnvironment)
			if environment == 0 {
				runtime.tracef(
					"java_native_environment_null:%s",
					runtime.lastJavaMethod,
				)
			}
		}
		result, resultValue, callErr := runtime.call(
			ctx,
			address,
			[]uint32{parameters, parameters},
			ktfBootstrapInstructionMax,
		)
		if callErr != nil {
			method := runtime.lastJavaMethod
			if method == "" {
				method = "<unknown>"
			}
			nativeParameters, _ := runtime.readWords(parameters, 10)
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
	}
	high := uint32(0)
	if strings.HasSuffix(runtime.lastJavaMethod, ")J") {
		high = runtime.javaReturnHigh
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

func (r *ktfRuntime) javaNativeMethodSignature(address uint32) (string, bool) {
	target := address &^ 1
	if target == 0 {
		return "", false
	}
	matches := r.nativeSignatureMatches(target)
	// A single native body can back several signatures. The most recently
	// dispatched method wins so the caller keeps resolving the method it is
	// already executing; otherwise only an unambiguous match resolves.
	for _, signature := range matches.signatures {
		if signature == r.lastJavaMethod {
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
func (r *ktfRuntime) nativeSignatureMatches(
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
	for _, classAddress := range r.javaClasses {
		if classAddress == 0 || seenClasses[classAddress] {
			continue
		}
		seenClasses[classAddress] = true
		class, err := r.inspectJavaClass(classAddress)
		if err != nil {
			continue
		}
		for _, method := range class.Methods {
			if method.NativeBody == 0 ||
				method.NativeBody&^1 != target {
				continue
			}
			declaring, inspectErr := r.inspectJavaClass(method.DeclaringClass)
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
func (r *ktfRuntime) nativeSignatureMatchesValid(
	target uint32,
	matches *ktfNativeSignatureMatches,
) bool {
	for _, methodAddress := range matches.methods {
		words, err := r.readWords(methodAddress, 3)
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
			handler: ktfHostJavaMethod(
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
			handler: ktfHostJavaMethod(
				"java/lang/Object",
				method[:separator],
				method[separator:],
			),
		}, true
	case "java/lang/System.currentTimeMillis()J":
		return ktfHostCall{
			name: "java.native_override." + signature,
			handler: ktfHostJavaMethod(
				"java/lang/System",
				"currentTimeMillis",
				"()J",
			),
		}, true
	case "java/lang/System.arraycopy(Ljava/lang/Object;ILjava/lang/Object;II)V":
		return ktfHostCall{
			name: "java.native_override." + signature,
			handler: ktfHostJavaMethod(
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
			handler: ktfHostJavaMethod(
				"java/lang/Thread",
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
			handler: ktfHostJavaMethod(
				"java/lang/String",
				method[:separator],
				method[separator:],
			),
		}, true
	case "org/kwis/msp/lcdui/Display.addJletEventListener(Lorg/kwis/msp/lcdui/JletEventListener;)V":
		return ktfHostCall{
			name: "java.native_override." + signature,
			handler: ktfHostJavaMethod(
				"org/kwis/msp/lcdui/Display",
				"addJletEventListener",
				"(Lorg/kwis/msp/lcdui/JletEventListener;)V",
			),
		}, true
	case "org/kwis/msp/lcdui/Display.getKeyCode(I)I":
		return ktfHostCall{
			name: "java.native_override." + signature,
			handler: ktfHostJavaMethod(
				"org/kwis/msp/lcdui/Display",
				"getKeyCode",
				"(I)I",
			),
		}, true
	case "org/kwis/msp/lcdui/Display.getKeyName(I)Ljava/lang/String;":
		return ktfHostCall{
			name: "java.native_override." + signature,
			handler: ktfHostJavaMethod(
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
			handler: ktfHostJavaMethod(
				"org/kwis/msp/lwc/Component",
				method[:separator],
				method[separator:],
			),
		}, true
	case "org/kwis/msp/media/Volume.get()I":
		return ktfHostCall{
			name: "java.native_override." + signature,
			handler: func(_ context.Context, runtime *ktfRuntime) (uint32, error) {
				return uint32(
					runtime.services.Media.Snapshot().GlobalVolume / 20,
				), nil
			},
		}, true
	case "org/kwis/msf/io/Network.connect()I":
		return ktfHostCall{
			name: "java.native_override." + signature,
			handler: func(_ context.Context, runtime *ktfRuntime) (uint32, error) {
				runtime.services.Device.SetNetworkAvailable(true)
				return 1, nil
			},
		}, true
	case "org/kwis/msf/io/Network.disconnect()V":
		return ktfHostCall{
			name: "java.native_override." + signature,
			handler: func(_ context.Context, runtime *ktfRuntime) (uint32, error) {
				runtime.services.Device.SetNetworkAvailable(false)
				return 0, nil
			},
		}, true
	case "org/kwis/msp/media/Vibrator.on(II)V":
		return ktfHostCall{
			name: "java.native_override." + signature,
			handler: func(_ context.Context, runtime *ktfRuntime) (uint32, error) {
				level, err := runtime.parameter(0)
				if err != nil {
					return 0, err
				}
				millis, err := runtime.parameter(1)
				if err != nil {
					return 0, err
				}
				return 0, runtime.services.Device.Vibrate(
					uint8(min(level, uint32(100))),
					time.Duration(millis)*time.Millisecond,
					runtime.services.Clock.Monotonic(),
				)
			},
		}, true
	default:
		return ktfHostCall{}, false
	}
}

func ktfHostJavaMethod(className, name, descriptor string) ktfHostHandler {
	return func(
		ctx context.Context,
		runtime *ktfRuntime,
	) (value uint32, returnedErr error) {
		runtime.javaReturnHigh = 0
		defer func() {
			if returnedErr != nil ||
				!strings.HasSuffix(descriptor, ")J") ||
				runtime.nativeParameterBase != 0 {
				return
			}
			if err := runtime.cpu.WriteRegister(
				cpu.RegisterR1,
				runtime.javaReturnHigh,
			); err != nil {
				value = 0
				returnedErr = err
			}
		}()
		registers := make([]uint32, cpu.RegisterR12+1)
		for register := range registers {
			registers[register], _ = runtime.parameter(uint32(register))
		}
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
		runtime.lastJavaCallLR, _ = runtime.cpu.ReadRegister(cpu.RegisterLR)
		runtime.tracef(
			"java_method_call:%s.%s%s:lr=0x%08x:%08x",
			className,
			name,
			descriptor,
			runtime.lastJavaCallLR,
			registers,
		)
		switch className {
		case "java/lang/Object":
			switch name + descriptor {
			case "<init>()V", "notify()V", "notifyAll()V", "finalize()V":
				return 0, nil
			case "wait(J)V", "wait(JI)V", "wait()V":
				if runtime.deferThreads {
					runtime.yieldRequested = true
				}
				return 0, nil
			case "getClass()Ljava/lang/Class;":
				if registers[1] == 0 {
					return 0, nil
				}
				classAddress, err := runtime.readU32(registers[1] + 4)
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
				return runtime.newJavaString(runtime.javaObjectString(registers[1]))
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
					runtime.tickMS,
				), nil
			case "gc()V":
				return 0, nil
			case "getProperty(Ljava/lang/String;)Ljava/lang/String;":
				return runtime.newJavaString("")
			}
		case "java/lang/Runtime":
			switch name + descriptor {
			case "getRuntime()Ljava/lang/Runtime;":
				return runtime.ensureJavaRuntime()
			case "freeMemory()J":
				return runtime.javaLongResult(uint64(guestHeapSize / 2)), nil
			case "totalMemory()J":
				return runtime.javaLongResult(uint64(guestHeapSize)), nil
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
		case "java/util/TimeZone":
			if name+descriptor == "getAvailableIDs()[Ljava/lang/String;" {
				value, err := runtime.newJavaString("GMT")
				if err != nil {
					return 0, err
				}
				return runtime.newJavaReferenceArray(
					"[Ljava/lang/String;",
					[]uint32{value},
				)
			}
		case "org/kwis/msp/lcdui/Card":
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
				if runtime.deferThreads && runtime.activeTask != nil {
					runtime.deferCardPaint(runtime.activeTask, card, false)
				}
				return 0, nil
			case "serviceRepaints()V":
				return 0, runtime.serviceCardRepaints(ctx, registers[1])
			case "showNotify(Z)V", "setCanvas(Ljavax/microedition/lcdui/Canvas;)V":
				return 0, nil
			case "keyNotify(II)Z":
				return 0, nil
			case "move(II)V":
				if err := runtime.writeJavaFieldWord(
					registers[1],
					8,
					registers[2],
				); err != nil {
					return 0, err
				}
				if err := runtime.writeJavaFieldWord(
					registers[1],
					12,
					registers[3],
				); err != nil {
					return 0, err
				}
				return 0, nil
			case "resize(II)V":
				if err := runtime.writeJavaFieldWord(
					registers[1],
					16,
					registers[2],
				); err != nil {
					return 0, err
				}
				if err := runtime.writeJavaFieldWord(
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
			if name+descriptor ==
				"getSystemProperty(Ljava/lang/String;)Ljava/lang/String;" {
				key := runtime.javaStringValue(registers[1])
				value := runtime.handsetSystemProperty(key)
				runtime.trace("java_handset_property:" + key + "=" + value)
				return runtime.newJavaString(value)
			}
		case "org/kwis/msp/lcdui/Jlet":
			if name+descriptor == "notifyDestroyed()V" {
				runtime.requestJavaTermination(registers[1])
			}
			return 0, nil
		case "com/ktf/kfc/GForm":
			return 0, nil
		case "org/kwis/msp/handset/BackLight":
			switch name + descriptor {
			case "alwaysOn()V", "on()V":
				return 0, runtime.services.Device.SetBacklight(
					true,
					0,
					runtime.services.Clock.Monotonic(),
				)
			case "off()V":
				return 0, runtime.services.Device.SetBacklight(
					false,
					0,
					runtime.services.Clock.Monotonic(),
				)
			}
			return 0, nil
		case "org/kwis/msp/handset/LED":
			if name+descriptor == "set(I)V" {
				return 0, runtime.services.Device.SetLED(
					0,
					int32(registers[1]),
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
			"org/kwis/msp/lwc/DialogComponent":
			return runtime.handleLWCMethod(
				ctx,
				className,
				name,
				descriptor,
				registers,
			)
		case "com/ktf/kfc/GTextListener":
			return 0, nil
		case "com/ktf/kfc/GTextField":
			if name+descriptor ==
				"getGTextListener()Lcom/ktf/kfc/GTextListener;" {
				instance := registers[1]
				if listener := runtime.listeners[instance]; listener != 0 {
					return listener, nil
				}
				listener, err := runtime.newHostJavaObject(
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
			return runtime.handleDataBaseMethod(name, descriptor)
		case "org/kwis/msp/lcdui/Display":
			return runtime.handleDisplayMethod(ctx, name, descriptor)
		}
		if strings.HasPrefix(className, "java/lang/") &&
			(strings.HasSuffix(className, "Exception") ||
				strings.HasSuffix(className, "Error")) &&
			name == "<init>" {
			return 0, nil
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
			if receiverWords, receiverErr := runtime.readWords(
				registers[1],
				2,
			); receiverErr == nil {
				if class, classErr := runtime.inspectJavaClass(
					receiverWords[1],
				); classErr == nil {
					receiverClass = class.Name
				}
			}
		}
		runtime.unimplementedJava[signature]++
		runtime.lastUnimplementedJava = signature
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
func (r *ktfRuntime) correctHostJavaReceiverClass(
	className, name, descriptor string,
	registers []uint32,
) string {
	if strings.HasPrefix(name, "<") || len(registers) < 2 || registers[1] == 0 {
		return className
	}
	declaredAddress := r.javaClasses[className]
	if declaredAddress == 0 {
		return className
	}
	declared, err := r.inspectJavaClass(declaredAddress)
	if err != nil {
		return className
	}
	if method, ok := findKTFJavaMethod(declared, name, descriptor); ok &&
		method.AccessFlags&0x0008 != 0 {
		return className
	}
	receiverWords, err := r.readWords(registers[1], 2)
	if err != nil || receiverWords[1] == 0 {
		return className
	}
	actual, err := r.inspectJavaClass(receiverWords[1])
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
			declaring, inspectErr := r.inspectJavaClass(method.DeclaringClass)
			if inspectErr == nil {
				return declaring.Name
			}
			return className
		}
		if actual.Parent == 0 {
			break
		}
		actual, err = r.inspectJavaClass(actual.Parent)
		if err != nil {
			break
		}
	}
	return className
}

func (r *ktfRuntime) redispatchGuestJavaMethod(
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
	receiverWords, err := r.readWords(registers[1], 2)
	if err != nil {
		return 0, false, nil
	}
	actual, err := r.inspectJavaClass(receiverWords[1])
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
	method, err := r.inspectJavaMethod(methodAddress)
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
	r.tracef(
		"java_virtual_redispatch:%s.%s%s:actual=%s:body=0x%08x",
		declaredClass,
		name,
		descriptor,
		actual.Name,
		method.Body,
	)
	value, err := r.invokeJavaVirtual(
		ctx,
		registers[1],
		name,
		descriptor,
		registers[2:2+parameterWords]...,
	)
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

func (r *ktfRuntime) handsetSystemProperty(key string) string {
	switch strings.ToUpper(strings.TrimSpace(key)) {
	case "PHONEMODEL":
		// LG-KH1300 was a common 240x320 KTF WIPI target. Some games use
		// this property to select resource geometry and otherwise leave
		// array dimensions uninitialized.
		if r.services == nil || r.services.Device == nil {
			return "LG-KH1300"
		}
		return r.services.Device.Config().Model
	default:
		return ""
	}
}

func (r *ktfRuntime) handleLWCMethod(
	_ context.Context,
	className, name, descriptor string,
	registers []uint32,
) (uint32, error) {
	instance := registers[1]
	state := r.lwcComponent(instance)
	method := name + descriptor

	switch className {
	case "org/kwis/msp/lwc/Component",
		"org/kwis/msp/lwc/ContainerComponent",
		"org/kwis/msp/lwc/TextComponent",
		"org/kwis/msp/lwc/TextBoxComponent":
		if method == "<init>()V" {
			return 0, nil
		}
	case "org/kwis/msp/lwc/ShellComponent":
		switch method {
		case "<init>()V", "<init>(Z)V", "<init>(ZZ)V":
			r.initializeLWCShell(state)
			return 0, nil
		case "<init>(IIII)V", "<init>(IIIIZ)V":
			r.configureLWC(state, registers[2], registers[3], registers[4], registers[5])
			state.shown = false
			return 0, nil
		}
	case "org/kwis/msp/lwc/FormComponent":
		switch method {
		case "<init>()V":
			state.vertical = true
			return 0, nil
		case "<init>(Z)V":
			state.vertical = registers[2] != 0
			return 0, nil
		}
	case "org/kwis/msp/lwc/AnnunciatorComponent":
		switch method {
		case "<init>(Z)V":
			r.initializeLWCAnnunciator(state)
			state.transparent = registers[2] != 0
			return 0, nil
		case "performed(LXTimer;)V":
			return 0, nil
		}
	case "org/kwis/msp/lwc/TextFieldComponent":
		if method == "<init>(Ljava/lang/String;I)V" {
			state.text = registers[2]
			r.initializeLWCTextSize(state, registers[2], true)
			return 0, nil
		}
	case "org/kwis/msp/lwc/LabelComponent":
		switch method {
		case "<init>()V":
			r.initializeLWCTextSize(state, 0, false)
			return 0, nil
		case "<init>(Ljava/lang/String;)V":
			state.text = registers[2]
			r.initializeLWCTextSize(state, registers[2], false)
			return 0, nil
		}
	case "org/kwis/msp/lwc/ProgressComponent":
		if method == "<init>(ZI)V" {
			maximum := int32(registers[3])
			if maximum <= 0 {
				return 0, r.raiseHostJavaException(
					"java/lang/IllegalArgumentException",
				)
			}
			state.progressInput = registers[2] != 0
			state.progressMax = maximum
			state.progressStep = 1
			state.preferredWidth = 100
			state.preferredHeight = 16
			state.width = state.preferredWidth
			state.height = state.preferredHeight
			return 0, nil
		}
	case "org/kwis/msp/lwc/DialogComponent":
		switch method {
		case "<init>(I)V":
			r.initializeLWCShell(state)
			if err := r.setLWCDialogType(state, int32(registers[2])); err != nil {
				return 0, err
			}
			return 0, nil
		case "<init>(Lorg/kwis/msp/lwc/Component;" +
			"Ljava/lang/String;I)V":
			r.initializeLWCShell(state)
			state.work = registers[2]
			state.text = registers[3]
			r.setLWCParent(registers[2], instance)
			if err := r.setLWCDialogType(state, int32(registers[4])); err != nil {
				return 0, err
			}
			return 0, nil
		}
	}

	switch method {
	case "configure(IIIII)V":
		r.configureLWC(
			state,
			registers[2],
			registers[3],
			registers[4],
			registers[5],
		)
		return 0, nil
	case "getX()I":
		return uint32(state.x), nil
	case "getY()I":
		return uint32(state.y), nil
	case "getWidth()I":
		return uint32(state.width), nil
	case "getHeight()I":
		return uint32(state.height), nil
	case "getXOnScreen()I":
		x, _ := r.lwcScreenPosition(instance)
		return uint32(x), nil
	case "getYOnScreen()I":
		_, y := r.lwcScreenPosition(instance)
		return uint32(y), nil
	case "getPreferredWidth()I":
		return uint32(lwcPreferredWidth(state)), nil
	case "getPreferredHeight()I", "getPreferredHeight(I)I":
		return uint32(lwcPreferredHeight(state)), nil
	case "calcPreferredSize(I)V":
		if state.preferredWidth == 0 {
			state.preferredWidth = int32(registers[2])
		}
		if state.preferredHeight == 0 {
			state.preferredHeight = state.height
		}
		return 0, nil
	case "setBackground(I)V":
		state.background = registers[2]
		return 0, nil
	case "getBackground()I":
		return state.background, nil
	case "setForeground(I)V":
		state.foreground = registers[2]
		return 0, nil
	case "getForeground()I":
		return state.foreground, nil
	case "setEventListener(Lorg/kwis/msp/lwc/EventListener;" +
		"Ljava/lang/Object;)V":
		r.listeners[instance] = registers[2]
		r.lwcEventData[instance] = registers[3]
		return 0, nil
	case "setFocus()V":
		r.setLWCFocus(instance, true)
		return 0, nil
	case "setFocus(Lorg/kwis/msp/lwc/Component;)V":
		state.focus = registers[2]
		r.setLWCFocus(registers[2], true)
		return 0, nil
	case "focusNotify(Z)V":
		r.setLWCFocus(instance, registers[2] != 0)
		return 0, nil
	case "hasFocus()Z":
		return boolWord(state.focused), nil
	case "canHandleInput()Z":
		return 1, nil
	case "invalidate()V":
		r.invalidateLWC(instance)
		return 0, nil
	case "isValid()Z":
		return boolWord(state.valid), nil
	case "isShown()Z":
		return boolWord(r.lwcIsShown(instance)), nil
	case "showNotify(Z)V":
		state.shown = registers[2] != 0
		return 0, nil
	case "getCard()Lorg/kwis/msp/lcdui/Card;":
		return r.lwcCard(instance), nil
	case "show()V":
		r.setLWCShown(instance, true)
		r.markLWCRepaint(instance)
		return 0, nil
	case "hide()V":
		r.setLWCShown(instance, false)
		return 0, nil
	case "addComponent(Lorg/kwis/msp/lwc/Component;)I":
		return uint32(r.addLWCChild(instance, len(r.lwcChildren[instance]), registers[2])), nil
	case "addComponent(ILorg/kwis/msp/lwc/Component;)V":
		r.addLWCChild(instance, int(int32(registers[2])), registers[3])
		return 0, nil
	case "setComponent(ILorg/kwis/msp/lwc/Component;)V":
		r.setLWCChild(instance, int(int32(registers[2])), registers[3])
		return 0, nil
	case "getComponent(I)Lorg/kwis/msp/lwc/Component;":
		children := r.lwcChildren[instance]
		index := int(int32(registers[2]))
		if index < 0 || index >= len(children) {
			return 0, nil
		}
		return children[index], nil
	case "getIndexOf(Lorg/kwis/msp/lwc/Component;)I":
		for index, child := range r.lwcChildren[instance] {
			if child == registers[2] {
				return uint32(index), nil
			}
		}
		return ^uint32(0), nil
	case "getNumberOfComponent()I":
		return uint32(len(r.lwcChildren[instance])), nil
	case "removeAllComponents()V":
		r.removeAllLWCChildren(instance)
		return 0, nil
	case "removeComponent(Lorg/kwis/msp/lwc/Component;)V":
		r.removeLWCChildValue(instance, registers[2])
		return 0, nil
	case "removeComponent(I)V":
		r.removeLWCChildIndex(instance, int(int32(registers[2])))
		return 0, nil
	case "validate()V", "layout()V", "layoutChildHorizontal()V",
		"layoutChildVertical()V":
		r.layoutLWC(instance)
		return 0, nil
	case "setPacked(Z)V":
		state.packed = registers[2] != 0
		r.invalidateLWC(instance)
		return 0, nil
	case "getPacked()Z":
		return boolWord(state.packed), nil
	case "setGab(I)V":
		state.gap = int32(registers[2])
		r.invalidateLWC(instance)
		return 0, nil
	case "getGab()I":
		return uint32(state.gap), nil
	case "scrollTo(II)Z":
		for _, child := range r.lwcChildren[instance] {
			childState := r.lwcComponent(child)
			childState.x -= int32(registers[2])
			childState.y -= int32(registers[3])
		}
		return 1, nil
	case "repaint()V", "repaint(IIII)V", "serviceRepaints()V":
		r.markLWCRepaint(instance)
		return 0, nil
	case "setTitle(Lorg/kwis/msp/lwc/Component;)V":
		state.title = registers[2]
		r.setLWCParent(registers[2], instance)
		return 0, nil
	case "getTitle()Lorg/kwis/msp/lwc/Component;":
		return state.title, nil
	case "setCommand(Lorg/kwis/msp/lwc/Component;Z)V":
		state.command = registers[2]
		r.setLWCParent(registers[2], instance)
		return 0, nil
	case "getCommand()Lorg/kwis/msp/lwc/Component;":
		return state.command, nil
	case "setWorkComponent(Lorg/kwis/msp/lwc/Component;)V":
		state.work = registers[2]
		r.setLWCParent(registers[2], instance)
		return 0, nil
	case "getWorkComponent()Lorg/kwis/msp/lwc/Component;":
		return state.work, nil
	case "setMaxLength(I)V":
		r.lwcMaxLengths[instance] = int32(registers[2])
		return 0, nil
	case "getMaxLength()I":
		return uint32(r.lwcMaxLengths[instance]), nil
	case "setString(Ljava/lang/String;)V", "setLabel(Ljava/lang/String;)V":
		state.text = registers[2]
		r.initializeLWCTextSize(
			state,
			registers[2],
			className == "org/kwis/msp/lwc/TextFieldComponent",
		)
		r.invalidateLWC(instance)
		return 0, nil
	case "getString()Ljava/lang/String;", "getLabel()Ljava/lang/String;":
		return state.text, nil
	case "setStep(I)V":
		step := int32(registers[2])
		if step <= 0 || state.progressMax > 0 && step > state.progressMax {
			return 0, r.raiseHostJavaException(
				"java/lang/IllegalArgumentException",
			)
		}
		state.progressStep = step
		state.progressValue -= state.progressValue % step
		r.invalidateLWC(instance)
		return 0, nil
	case "getStep()I":
		return uint32(max(state.progressStep, 1)), nil
	case "setMargin(II)V":
		state.progressTop = max(int32(registers[2]), 0)
		state.progressBottom = max(int32(registers[3]), 0)
		r.invalidateLWC(instance)
		return 0, nil
	case "setMaxValue(I)V":
		maximum := int32(registers[2])
		if maximum <= 0 {
			return 0, r.raiseHostJavaException(
				"java/lang/IllegalArgumentException",
			)
		}
		state.progressMax = maximum
		state.progressValue = min(state.progressValue, maximum)
		r.invalidateLWC(instance)
		return 0, nil
	case "getMaxValue()I":
		return uint32(state.progressMax), nil
	case "setValue(I)I":
		value := max(int32(registers[2]), 0)
		value = min(value, state.progressMax)
		step := max(state.progressStep, 1)
		value -= value % step
		state.progressValue = value
		r.invalidateLWC(instance)
		return uint32(value), nil
	case "getValue()I":
		return uint32(state.progressValue), nil
	case "setButtonString(ILjava/lang/String;)V":
		switch int32(registers[2]) {
		case ktfDialogOKButton:
			state.dialogOK = registers[3]
		case ktfDialogCancelButton:
			state.dialogCancel = registers[3]
		default:
			return 0, r.raiseHostJavaException(
				"java/lang/IllegalArgumentException",
			)
		}
		return 0, nil
	case "setType(I)V":
		return 0, r.setLWCDialogType(state, int32(registers[2]))
	case "getType()I":
		return uint32(state.dialogType), nil
	case "setTimeout(I)V":
		state.dialogTimeout = int32(registers[2])
		return 0, nil
	case "getTimeout()I":
		return uint32(state.dialogTimeout), nil
	case "doModal()I":
		r.setLWCShown(instance, true)
		r.markLWCRepaint(instance)
		if state.dialogType == ktfDialogTypeNone {
			state.dialogAction = ktfDialogTimeout
		} else {
			state.dialogAction = ktfDialogOK
		}
		r.setLWCShown(instance, false)
		return uint32(state.dialogAction), nil
	case "getActionState()I":
		return uint32(state.dialogAction), nil
	case "keyNotify(II)Z", "pointerNotify(III)Z",
		"processEvent(IIII)Z":
		if className == "org/kwis/msp/lwc/ProgressComponent" &&
			method == "keyNotify(II)Z" && state.progressInput {
			key := int32(registers[3])
			value := state.progressValue
			switch key {
			case -1, -3:
				value -= max(state.progressStep, 1)
			case -2, -4:
				value += max(state.progressStep, 1)
			default:
				return 0, nil
			}
			state.progressValue = min(max(value, 0), state.progressMax)
			r.invalidateLWC(instance)
			return 1, nil
		}
		return 0, nil
	case "paint(Lorg/kwis/msp/lcdui/Graphics;)V",
		"paintContent(Lorg/kwis/msp/lcdui/Graphics;)V",
		"paintFrame(Lorg/kwis/msp/lcdui/Graphics;)V",
		"controlInset(Z)V", "useFrame(Z)V",
		"setLayout(I)V", "setFont(Lorg/kwis/msp/lcdui/Font;)V",
		"setImage(Lorg/kwis/msp/lcdui/Image;)V",
		"setGrabKeyListener(Lorg/kwis/msp/lwc/GrabKeyListener;" +
			"Ljava/lang/Object;)V",
		"grabKey(I)V", "ungrabKey(I)V":
		return 0, nil
	}

	signature := className + "." + name + descriptor
	r.unimplementedJava[signature]++
	r.lastUnimplementedJava = signature
	return 0, nil
}

func (r *ktfRuntime) lwcComponent(instance uint32) *ktfLWCComponent {
	if state := r.lwcComponents[instance]; state != nil {
		return state
	}
	state := &ktfLWCComponent{
		shown:    true,
		vertical: true,
	}
	r.lwcComponents[instance] = state
	if instance == 0 {
		return state
	}
	classAddress, err := r.readU32(instance + 4)
	if err != nil {
		return state
	}
	for depth := 0; classAddress != 0 && depth < 32; depth++ {
		class, inspectErr := r.inspectJavaClass(classAddress)
		if inspectErr != nil {
			break
		}
		if class.Name == "org/kwis/msp/lwc/AnnunciatorComponent" {
			r.initializeLWCAnnunciator(state)
			break
		}
		classAddress = class.Parent
	}
	return state
}

func (r *ktfRuntime) initializeLWCShell(state *ktfLWCComponent) {
	if state == nil {
		return
	}
	width, height := int32(240), int32(320)
	if r.frame != nil {
		width = int32(r.frame.Bounds().Dx())
		height = int32(r.frame.Bounds().Dy())
	}
	if state.width == 0 {
		state.width = width
	}
	if state.height == 0 {
		state.height = height
	}
	state.preferredWidth = state.width
	state.preferredHeight = state.height
	state.valid = true
	state.shown = false
}

func (r *ktfRuntime) initializeLWCAnnunciator(state *ktfLWCComponent) {
	r.initializeLWCShell(state)
	state.annunciator = true
	state.x = 0
	state.y = 0
	state.height = ktfAnnunciatorHeight
	state.preferredHeight = ktfAnnunciatorHeight
	state.shown = false
}

func (r *ktfRuntime) setLWCDialogType(
	state *ktfLWCComponent,
	dialogType int32,
) error {
	if state == nil {
		return nil
	}
	switch dialogType {
	case ktfDialogTypeNone:
		state.dialogTimeout = 3_000
	case ktfDialogTypeOK, ktfDialogTypeOKCancel:
		state.dialogTimeout = -1
	default:
		return r.raiseHostJavaException("java/lang/IllegalArgumentException")
	}
	state.dialogType = dialogType
	state.dialogAction = -2
	return nil
}

func (r *ktfRuntime) initializeLWCTextSize(
	state *ktfLWCComponent,
	text uint32,
	field bool,
) {
	if state == nil {
		return
	}
	width := int32(len([]rune(r.javaStringValue(text)))*8 + 4)
	height := int32(16)
	if field {
		width += 4
		height = 20
	}
	if width < 4 {
		width = 4
	}
	state.preferredWidth = width
	state.preferredHeight = height
	if state.width == 0 {
		state.width = width
	}
	if state.height == 0 {
		state.height = height
	}
}

func (r *ktfRuntime) configureLWC(
	state *ktfLWCComponent,
	x, y, width, height uint32,
) {
	if state == nil {
		return
	}
	state.x = int32(x)
	state.y = int32(y)
	state.width = int32(width)
	state.height = int32(height)
	if state.preferredWidth == 0 {
		state.preferredWidth = state.width
	}
	if state.preferredHeight == 0 {
		state.preferredHeight = state.height
	}
	state.valid = true
}

func lwcPreferredWidth(state *ktfLWCComponent) int32 {
	if state == nil {
		return 0
	}
	if state.preferredWidth > 0 {
		return state.preferredWidth
	}
	return state.width
}

func lwcPreferredHeight(state *ktfLWCComponent) int32 {
	if state == nil {
		return 0
	}
	if state.preferredHeight > 0 {
		return state.preferredHeight
	}
	return state.height
}

func (r *ktfRuntime) lwcScreenPosition(instance uint32) (int32, int32) {
	var x, y int32
	seen := make(map[uint32]bool)
	for depth := 0; instance != 0 && depth < 256; depth++ {
		if seen[instance] {
			break
		}
		seen[instance] = true
		state := r.lwcComponent(instance)
		x += state.x
		y += state.y
		instance = state.parent
	}
	return x, y
}

func (r *ktfRuntime) setLWCFocus(instance uint32, focused bool) {
	if instance == 0 {
		return
	}
	state := r.lwcComponent(instance)
	state.focused = focused
	if focused && state.parent != 0 {
		parent := r.lwcComponent(state.parent)
		if parent.focus != 0 && parent.focus != instance {
			r.lwcComponent(parent.focus).focused = false
		}
		parent.focus = instance
	}
}

func (r *ktfRuntime) invalidateLWC(instance uint32) {
	if instance == 0 {
		return
	}
	state := r.lwcComponent(instance)
	if !state.valid {
		return
	}
	state.valid = false
	r.invalidateLWC(state.parent)
}

func (r *ktfRuntime) lwcIsShown(instance uint32) bool {
	seen := make(map[uint32]bool)
	for depth := 0; instance != 0 && depth < 256; depth++ {
		if seen[instance] {
			return false
		}
		seen[instance] = true
		state := r.lwcComponent(instance)
		if !state.shown {
			return false
		}
		instance = state.parent
	}
	return true
}

func (r *ktfRuntime) setLWCShown(instance uint32, shown bool) {
	if instance == 0 {
		return
	}
	r.lwcComponent(instance).shown = shown
	for _, child := range r.lwcChildren[instance] {
		r.setLWCShown(child, shown)
	}
}

func (r *ktfRuntime) lwcCard(instance uint32) uint32 {
	seen := make(map[uint32]bool)
	for depth := 0; instance != 0 && depth < 256; depth++ {
		if seen[instance] {
			return 0
		}
		seen[instance] = true
		state := r.lwcComponent(instance)
		if state.card != 0 {
			return state.card
		}
		instance = state.parent
	}
	return 0
}

func (r *ktfRuntime) setLWCParent(child, parent uint32) {
	if child == 0 {
		return
	}
	r.lwcComponent(child).parent = parent
}

func (r *ktfRuntime) addLWCChild(parent uint32, index int, child uint32) int {
	children := r.lwcChildren[parent]
	if index < 0 {
		index = 0
	}
	if index > len(children) {
		index = len(children)
	}
	children = append(children, 0)
	copy(children[index+1:], children[index:])
	children[index] = child
	r.lwcChildren[parent] = children
	r.setLWCParent(child, parent)
	state := r.lwcComponent(parent)
	if state.work == 0 {
		state.work = child
	}
	r.invalidateLWC(parent)
	return index
}

func (r *ktfRuntime) setLWCChild(parent uint32, index int, child uint32) {
	children := r.lwcChildren[parent]
	if index < 0 || index >= len(children) {
		return
	}
	old := children[index]
	if old != 0 {
		r.lwcComponent(old).parent = 0
	}
	children[index] = child
	r.lwcChildren[parent] = children
	r.setLWCParent(child, parent)
	r.invalidateLWC(parent)
}

func (r *ktfRuntime) removeAllLWCChildren(parent uint32) {
	for _, child := range r.lwcChildren[parent] {
		if child != 0 {
			r.lwcComponent(child).parent = 0
		}
	}
	delete(r.lwcChildren, parent)
	state := r.lwcComponent(parent)
	state.focus = 0
	state.work = 0
	r.invalidateLWC(parent)
}

func (r *ktfRuntime) removeLWCChildValue(parent, child uint32) {
	for index, candidate := range r.lwcChildren[parent] {
		if candidate == child {
			r.removeLWCChildIndex(parent, index)
			return
		}
	}
}

func (r *ktfRuntime) removeLWCChildIndex(parent uint32, index int) {
	children := r.lwcChildren[parent]
	if index < 0 || index >= len(children) {
		return
	}
	child := children[index]
	copy(children[index:], children[index+1:])
	children = children[:len(children)-1]
	r.lwcChildren[parent] = children
	if child != 0 {
		r.lwcComponent(child).parent = 0
	}
	state := r.lwcComponent(parent)
	if state.focus == child {
		state.focus = 0
	}
	if state.work == child {
		state.work = 0
	}
	r.invalidateLWC(parent)
}

func (r *ktfRuntime) layoutLWC(instance uint32) {
	state := r.lwcComponent(instance)
	children := r.lwcChildren[instance]
	var cursor int32
	var cross int32
	for _, child := range children {
		childState := r.lwcComponent(child)
		width := lwcPreferredWidth(childState)
		height := lwcPreferredHeight(childState)
		if state.vertical {
			childState.x = 0
			childState.y = cursor
			if state.packed && state.width > 0 {
				width = state.width
			}
			cursor += height + state.gap
			if width > cross {
				cross = width
			}
		} else {
			childState.x = cursor
			childState.y = 0
			if state.packed && state.height > 0 {
				height = state.height
			}
			cursor += width + state.gap
			if height > cross {
				cross = height
			}
		}
		childState.width = width
		childState.height = height
		childState.valid = true
	}
	if len(children) > 0 {
		cursor -= state.gap
	}
	if state.vertical {
		state.preferredWidth = cross
		state.preferredHeight = cursor
	} else {
		state.preferredWidth = cursor
		state.preferredHeight = cross
	}
	state.valid = true
}

func (r *ktfRuntime) markLWCRepaint(instance uint32) {
	if card := r.lwcCard(instance); card != 0 {
		r.dirtyCards[card] = true
	}
}

func boolWord(value bool) uint32 {
	if value {
		return 1
	}
	return 0
}

func (r *ktfRuntime) javaArrayCopy(
	source uint32,
	sourcePosition uint32,
	target uint32,
	targetPosition uint32,
	count uint32,
) error {
	if source == 0 || target == 0 {
		// Several shipping KTF applications use a null buffer to represent an
		// unavailable optional resource. The handset VM treated that copy as a
		// no-op instead of aborting the application.
		return nil
	}
	sourceWords, err := r.readWords(source, 2)
	if err != nil {
		return err
	}
	targetWords, err := r.readWords(target, 2)
	if err != nil {
		return err
	}
	sourceClass, err := r.inspectJavaClass(sourceWords[1])
	if err != nil {
		return err
	}
	targetClass, err := r.inspectJavaClass(targetWords[1])
	if err != nil {
		return err
	}
	sourceSize, err := ktfJavaArrayElementSize(sourceClass.Name)
	if err != nil {
		return err
	}
	targetSize, err := ktfJavaArrayElementSize(targetClass.Name)
	if err != nil {
		return err
	}
	if sourceSize != targetSize {
		r.tracef(
			"java_arraycopy_type_mismatch:source=0x%08x[%d]:%s:"+
				"target=0x%08x[%d]:%s:count=%d",
			source,
			sourcePosition,
			sourceClass.Name,
			target,
			targetPosition,
			targetClass.Name,
			count,
		)
		return r.raiseHostJavaException("java/lang/ArrayStoreException")
	}
	sourceLength, err := r.readU32(sourceWords[0] + 4)
	if err != nil {
		return err
	}
	targetLength, err := r.readU32(targetWords[0] + 4)
	if err != nil {
		return err
	}
	if uint64(sourcePosition)+uint64(count) > uint64(sourceLength) ||
		uint64(targetPosition)+uint64(count) > uint64(targetLength) {
		r.tracef(
			"java_arraycopy_bounds:source=0x%08x[%d:%d]/%d:"+
				"target=0x%08x[%d:%d]/%d",
			source,
			sourcePosition,
			uint64(sourcePosition)+uint64(count),
			sourceLength,
			target,
			targetPosition,
			uint64(targetPosition)+uint64(count),
			targetLength,
		)
		return r.raiseHostJavaException(
			"java/lang/ArrayIndexOutOfBoundsException",
		)
	}
	byteCount := uint64(count) * uint64(sourceSize)
	if byteCount > uint64(^uint32(0)) {
		return errors.New("KTF Java arraycopy byte count overflows")
	}
	data := make([]byte, uint32(byteCount))
	sourceAddress := sourceWords[0] + 8 + sourcePosition*sourceSize
	targetAddress := targetWords[0] + 8 + targetPosition*targetSize
	if err := r.cpu.ReadMemory(sourceAddress, data); err != nil {
		return err
	}
	return r.cpu.WriteMemory(targetAddress, data)
}

func (r *ktfRuntime) raiseHostJavaException(name string) error {
	r.snapshotJavaThrow()
	r.rememberJavaThrowName(name)
	_, err := r.raiseJavaException(name, 0)
	return err
}

func ktfJavaArrayElementSize(className string) (uint32, error) {
	if !strings.HasPrefix(className, "[") || len(className) < 2 {
		return 0, fmt.Errorf("KTF Java object %q is not an array", className)
	}
	switch className[1] {
	case 'Z', 'B':
		return 1, nil
	case 'C', 'S':
		return 2, nil
	case 'J', 'D':
		return 8, nil
	default:
		return 4, nil
	}
}

func (r *ktfRuntime) deferCardPaint(
	task *ktfTask,
	card uint32,
	show bool,
) {
	if task == nil || card == 0 {
		return
	}
	if show {
		cards := r.deferredShownCards[task]
		if cards == nil {
			cards = make(map[uint32]bool)
			r.deferredShownCards[task] = cards
		}
		cards[card] = true
	}
	for _, queued := range r.deferredPaintCards[task] {
		if queued == card {
			return
		}
	}
	r.deferredPaintCards[task] = append(r.deferredPaintCards[task], card)
}

func (r *ktfRuntime) releaseDeferredCardPaints(
	ctx context.Context,
	task *ktfTask,
) error {
	cards := r.deferredPaintCards[task]
	shownCards := r.deferredShownCards[task]
	delete(r.deferredPaintCards, task)
	delete(r.deferredShownCards, task)
	for _, card := range cards {
		if shownCards[card] {
			if err := r.notifyCardShown(ctx, card, true); err != nil {
				return err
			}
		}
		if err := r.paintCard(ctx, card); err != nil {
			return err
		}
	}
	return nil
}

func (r *ktfRuntime) notifyCardShown(
	ctx context.Context,
	card uint32,
	shown bool,
) error {
	if card == 0 {
		return nil
	}
	value := uint32(0)
	if shown {
		value = 1
	}
	if r.deferThreads {
		_, err := r.queueJavaVirtualTask(card, "showNotify", "(Z)V", value)
		return err
	}
	_, err := r.invokeJavaVirtual(ctx, card, "showNotify", "(Z)V", value)
	return err
}

func (r *ktfRuntime) paintCard(ctx context.Context, card uint32) error {
	if card == 0 || !r.dirtyCards[card] {
		return nil
	}
	if task := r.pendingKeyTask(card); task != nil {
		r.deferCardPaint(task, card, false)
		r.tracef("java_paint_defer_key:card=0x%08x", card)
		return nil
	}
	if task := r.paintTasks[card]; task != nil && !task.done {
		delete(r.dirtyCards, card)
		if task.wakeAtMS > r.tickMS {
			// The card's paint is mid-flight inside a guest Thread.sleep and
			// virtual time only advances between presentation quanta, so
			// nothing else the guest runs in this quantum can wake it or
			// produce a frame.
			r.paintStalled = true
		}
		r.tracef("java_paint_coalesce:card=0x%08x", card)
		return nil
	}
	delete(r.paintTasks, card)
	delete(r.dirtyCards, card)
	graphics, err := r.ensureScreenGraphics()
	if err != nil {
		return err
	}
	r.resetScreenGraphics(graphics)
	if r.deferThreads {
		task, err := r.queueJavaVirtualTask(
			card,
			"paint",
			"(Lorg/kwis/msp/lcdui/Graphics;)V",
			graphics,
		)
		if err != nil {
			return err
		}
		task.presentOnReturn = true
		task.paintCard = card
		task.bestEffortPaint = !r.paintInitializedCards[card]
		if task.bestEffortPaint {
			r.tracef("java_initial_paint_arm:card=0x%08x", card)
		}
		r.paintTasks[card] = task
		return nil
	}
	if _, err := r.invokeJavaVirtual(
		ctx,
		card,
		"paint",
		"(Lorg/kwis/msp/lcdui/Graphics;)V",
		graphics,
	); err != nil {
		return err
	}
	if err := r.recordPresentation(); err != nil {
		return err
	}
	r.paintInitializedCards[card] = true
	return nil
}

func (r *ktfRuntime) serviceCardRepaints(
	ctx context.Context,
	card uint32,
) error {
	if card == 0 || !r.dirtyCards[card] {
		return nil
	}
	if task := r.paintTasks[card]; task != nil && !task.done {
		task.done = true
		delete(r.paintTasks, card)
		r.tracef("java_paint_force_cancel:card=0x%08x", card)
	}
	delete(r.dirtyCards, card)
	graphics, err := r.ensureScreenGraphics()
	if err != nil {
		return err
	}
	r.resetScreenGraphics(graphics)
	_, err = r.invokeJavaVirtual(
		ctx,
		card,
		"paint",
		"(Lorg/kwis/msp/lcdui/Graphics;)V",
		graphics,
	)
	if err != nil {
		var unhandled *ktfUnhandledJavaException
		if r.paintInitializedCards[card] || !errors.As(err, &unhandled) {
			return err
		}
		r.tracef(
			"java_initial_paint_discard:%s:card=0x%08x",
			unhandled.name,
			card,
		)
		return nil
	}
	r.paintInitializedCards[card] = true
	return r.recordPresentation()
}

func (r *ktfRuntime) recordPresentation() error {
	if r.screenGraphics != 0 {
		if err := r.syncKTFGraphics(r.screenGraphics); err != nil {
			return err
		}
		if serviceID := r.graphicsServices[r.screenGraphics]; serviceID != 0 {
			if r.services.Graphics.Screen() != serviceID {
				if err := r.services.Graphics.SetScreen(
					r.serviceOwner,
					serviceID,
				); err != nil {
					return err
				}
			}
			if _, err := r.services.Graphics.Present(
				r.serviceOwner,
				serviceID,
				shared.Rectangle{},
			); err != nil {
				return err
			}
		}
	}
	r.presentCount++
	r.tracef("java_present:%d", r.presentCount)
	if r.activeTask != nil {
		// A handset display update is a scheduler boundary. Yield after the
		// host call returns so a paint loop cannot submit many invisible
		// intermediate frames inside one StepFrame.
		r.yieldRequested = true
	}
	return nil
}

func (r *ktfRuntime) handleFontMethod(name, descriptor string) (uint32, error) {
	switch name + descriptor {
	case "getDefaultFont()Lorg/kwis/msp/lcdui/Font;",
		"getFont(III)Lorg/kwis/msp/lcdui/Font;":
		return r.ensureDefaultFont()
	case "getHeight()I":
		instance, err := r.parameter(1)
		if err != nil {
			return 0, err
		}
		fontID, err := r.ensureKTFFontService(instance)
		if err != nil {
			return 0, err
		}
		metrics, err := r.services.Text.Metrics(r.serviceOwner, fontID)
		return uint32(metrics.Height), err
	case "charWidth(C)I":
		instance, err := r.parameter(1)
		if err != nil {
			return 0, err
		}
		character, err := r.parameter(2)
		if err != nil {
			return 0, err
		}
		fontID, err := r.ensureKTFFontService(instance)
		if err != nil {
			return 0, err
		}
		glyph, err := r.services.Text.Glyph(
			r.serviceOwner,
			fontID,
			rune(character),
		)
		return uint32(glyph.Advance), err
	case "stringWidth(Ljava/lang/String;)I":
		instance, err := r.parameter(1)
		if err != nil {
			return 0, err
		}
		value, err := r.parameter(2)
		if err != nil {
			return 0, err
		}
		fontID, err := r.ensureKTFFontService(instance)
		if err != nil {
			return 0, err
		}
		width, err := r.services.Text.Measure(
			r.serviceOwner,
			fontID,
			r.javaStringValue(value),
		)
		return uint32(width), err
	case "substringWidth(Ljava/lang/String;II)I":
		instance, err := r.parameter(1)
		if err != nil {
			return 0, err
		}
		value, err := r.parameter(2)
		if err != nil {
			return 0, err
		}
		offset, err := r.parameter(3)
		if err != nil {
			return 0, err
		}
		count, err := r.parameter(4)
		if err != nil {
			return 0, err
		}
		runes := []rune(r.javaStringValue(value))
		if offset > uint32(len(runes)) ||
			count > uint32(len(runes))-offset {
			return 0, nil
		}
		fontID, err := r.ensureKTFFontService(instance)
		if err != nil {
			return 0, err
		}
		width, err := r.services.Text.Measure(
			r.serviceOwner,
			fontID,
			string(runes[offset:offset+count]),
		)
		return uint32(width), err
	default:
		return 0, nil
	}
}

func (r *ktfRuntime) ensureDefaultFont() (uint32, error) {
	if r.defaultFont != 0 {
		return r.defaultFont, nil
	}
	classAddress, err := r.ensureJavaClass("org/kwis/msp/lcdui/Font")
	if err != nil {
		return 0, err
	}
	class, err := r.inspectJavaClass(classAddress)
	if err != nil {
		return 0, err
	}
	r.defaultFont, err = r.newJavaInstanceForClass(class)
	if err != nil {
		return 0, err
	}
	fontID, err := r.services.Text.CreateFont(
		r.serviceOwner,
		shared.FontDescriptor{
			Family: "aram-fallback",
			Size:   12,
		},
	)
	if err != nil {
		return 0, err
	}
	r.fontServices[r.defaultFont] = fontID
	return r.defaultFont, nil
}

func (r *ktfRuntime) ensureKTFFontService(
	instance uint32,
) (shared.ServiceID, error) {
	if serviceID := r.fontServices[instance]; serviceID != 0 {
		return serviceID, nil
	}
	serviceID, err := r.services.Text.CreateFont(
		r.serviceOwner,
		shared.FontDescriptor{
			Family: "aram-fallback",
			Size:   12,
		},
	)
	if err != nil {
		return 0, err
	}
	r.fontServices[instance] = serviceID
	return serviceID, nil
}

func (r *ktfRuntime) handleImageMethod(name, descriptor string) (uint32, error) {
	switch name + descriptor {
	case "createImage(II)Lorg/kwis/msp/lcdui/Image;":
		width, err := r.parameter(1)
		if err != nil {
			return 0, err
		}
		height, err := r.parameter(2)
		if err != nil {
			return 0, err
		}
		if width == 0 || height == 0 || width > 4096 || height > 4096 {
			return 0, fmt.Errorf("invalid KTF image size %dx%d", width, height)
		}
		return r.newJavaImage(image.NewRGBA(image.Rect(
			0,
			0,
			int(width),
			int(height),
		)))
	case "createImage(Ljava/lang/String;)Lorg/kwis/msp/lcdui/Image;":
		nameAddress, err := r.parameter(1)
		if err != nil {
			return 0, err
		}
		resourceName := strings.TrimPrefix(
			strings.ReplaceAll(r.javaStringValue(nameAddress), `\`, "/"),
			"/",
		)
		resourceName = path.Clean(resourceName)
		data, ok := r.findKTFResource(resourceName)
		if !ok {
			r.trace("java_image_missing:" + resourceName)
			return r.newJavaImage(image.NewRGBA(image.Rect(0, 0, 1, 1)))
		}
		instance, decodeErr := r.newJavaEncodedImage(data)
		if decodeErr != nil {
			return r.newJavaImage(image.NewRGBA(image.Rect(0, 0, 1, 1)))
		}
		return instance, nil
	case "createImage([BII)Lorg/kwis/msp/lcdui/Image;":
		array, err := r.parameter(1)
		if err != nil {
			return 0, err
		}
		offset, err := r.parameter(2)
		if err != nil {
			return 0, err
		}
		count, err := r.parameter(3)
		if err != nil {
			return 0, err
		}
		data, err := r.readJavaByteArrayRange(array, offset, count)
		if err != nil {
			return 0, err
		}
		instance, decodeErr := r.newJavaEncodedImage(data)
		if decodeErr != nil {
			return r.newJavaImage(image.NewRGBA(image.Rect(0, 0, 1, 1)))
		}
		return instance, nil
	case "getWidth()I":
		instance, err := r.parameter(1)
		if err != nil {
			return 0, err
		}
		if source := r.images[instance]; source != nil {
			return uint32(source.Bounds().Dx()), nil
		}
		return 0, nil
	case "getHeight()I":
		instance, err := r.parameter(1)
		if err != nil {
			return 0, err
		}
		if source := r.images[instance]; source != nil {
			return uint32(source.Bounds().Dy()), nil
		}
		return 0, nil
	case "getGraphics()Lorg/kwis/msp/lcdui/Graphics;":
		instance, err := r.parameter(1)
		if err != nil {
			return 0, err
		}
		target, ok := r.images[instance].(draw.Image)
		if !ok {
			return 0, nil
		}
		graphics, err := r.newJavaInstance(
			"org/kwis/msp/lcdui/Graphics",
			4,
		)
		if err != nil {
			return 0, err
		}
		r.graphics[graphics] = &ktfGraphics{
			target: target,
			clip:   target.Bounds(),
			color:  color.RGBA{A: 0xff},
		}
		r.graphicsServices[graphics] = r.imageServices[instance]
		return graphics, nil
	default:
		return 0, nil
	}
}

func (r *ktfRuntime) newJavaImage(source image.Image) (uint32, error) {
	instance, err := r.newJavaInstance("org/kwis/msp/lcdui/Image", 8)
	if err != nil {
		return 0, err
	}
	r.images[instance] = source
	bounds := source.Bounds()
	surface, err := r.services.Graphics.CreateSurface(
		r.serviceOwner,
		shared.SurfaceDescriptor{
			Width:  int32(bounds.Dx()),
			Height: int32(bounds.Dy()),
			Format: shared.PixelRGBA8888,
		},
	)
	if err != nil {
		return 0, err
	}
	if err := r.services.Graphics.ReplacePixels(
		r.serviceOwner,
		surface,
		ktfRGBABytes(source),
	); err != nil {
		_ = r.services.Graphics.DestroySurface(r.serviceOwner, surface)
		return 0, err
	}
	r.imageServices[instance] = surface
	return instance, nil
}

func (r *ktfRuntime) newJavaEncodedImage(data []byte) (uint32, error) {
	asset, err := r.services.Assets.Decode(
		r.serviceOwner,
		data,
		shared.DecodeOptions{},
	)
	if err != nil {
		return 0, err
	}
	info, err := r.services.Assets.Info(r.serviceOwner, asset)
	if err != nil || len(info.Frames) == 0 {
		_ = r.services.Assets.Release(r.serviceOwner, asset)
		if err == nil {
			err = fmt.Errorf("decoded KTF image has no frames")
		}
		return 0, err
	}
	pixels, err := r.services.Graphics.RGBA(
		r.serviceOwner,
		info.Frames[0].Surface,
	)
	if err != nil {
		_ = r.services.Assets.Release(r.serviceOwner, asset)
		return 0, err
	}
	// Assets exposes straight-alpha RGBA bytes. Keep them in NRGBA form:
	// image.RGBA expects premultiplied channels, and storing transparent
	// magenta there makes draw.Over leak the RGB color through alpha zero.
	source := image.NewNRGBA(image.Rect(
		0,
		0,
		int(info.Width),
		int(info.Height),
	))
	copy(source.Pix, pixels)
	instance, err := r.newJavaInstance("org/kwis/msp/lcdui/Image", 8)
	if err != nil {
		_ = r.services.Assets.Release(r.serviceOwner, asset)
		return 0, err
	}
	r.images[instance] = source
	r.imageServices[instance] = info.Frames[0].Surface
	r.javaAssetServices[instance] = asset
	return instance, nil
}

func ktfRGBABytes(source image.Image) []byte {
	bounds := source.Bounds()
	pixels := make([]byte, bounds.Dx()*bounds.Dy()*4)
	offset := 0
	for y := bounds.Min.Y; y < bounds.Max.Y; y++ {
		for x := bounds.Min.X; x < bounds.Max.X; x++ {
			red, green, blue, alpha := source.At(x, y).RGBA()
			pixels[offset+0] = uint8(red >> 8)
			pixels[offset+1] = uint8(green >> 8)
			pixels[offset+2] = uint8(blue >> 8)
			pixels[offset+3] = uint8(alpha >> 8)
			offset += 4
		}
	}
	return pixels
}

func (r *ktfRuntime) findKTFResource(name string) ([]byte, bool) {
	if name == "." || name == ".." || strings.HasPrefix(name, "../") {
		return nil, false
	}
	if data, err := r.services.Storage.ReadFile(
		shared.NamespacePackage,
		name,
	); err == nil {
		return data, true
	}
	if data, ok := r.pkg.Resources[name]; ok {
		return data, true
	}
	for candidate, data := range r.pkg.Resources {
		if strings.EqualFold(candidate, name) ||
			strings.EqualFold(path.Base(candidate), path.Base(name)) {
			if mounted, err := r.services.Storage.ReadFile(
				shared.NamespacePackage,
				candidate,
			); err == nil {
				return mounted, true
			}
			return data, true
		}
	}
	return nil, false
}

func (r *ktfRuntime) ensureScreenGraphics() (uint32, error) {
	if r.screenGraphics != 0 {
		return r.screenGraphics, nil
	}
	if r.frame == nil {
		r.frame = image.NewRGBA(image.Rect(0, 0, 240, 320))
	}
	classAddress, err := r.ensureJavaClass("org/kwis/msp/lcdui/Graphics")
	if err != nil {
		return 0, err
	}
	class, err := r.inspectJavaClass(classAddress)
	if err != nil {
		return 0, err
	}
	instance, err := r.newJavaInstanceForClass(class)
	if err != nil {
		return 0, err
	}
	r.screenGraphics = instance
	r.graphics[instance] = &ktfGraphics{
		target:      r.frame,
		clip:        r.frame.Bounds(),
		color:       color.RGBA{A: 0xff},
		pixelsDirty: true,
	}
	surface, err := r.services.Graphics.CreateSurface(
		r.serviceOwner,
		shared.SurfaceDescriptor{
			Width:  int32(r.frame.Bounds().Dx()),
			Height: int32(r.frame.Bounds().Dy()),
			Format: shared.PixelRGBA8888,
		},
	)
	if err != nil {
		return 0, err
	}
	if err := r.services.Graphics.SetScreen(
		r.serviceOwner,
		surface,
	); err != nil {
		_ = r.services.Graphics.DestroySurface(r.serviceOwner, surface)
		return 0, err
	}
	r.graphicsServices[instance] = surface
	if err := r.syncKTFGraphics(instance); err != nil {
		return 0, err
	}
	return instance, nil
}

func (r *ktfRuntime) resetScreenGraphics(instance uint32) {
	state := r.graphics[instance]
	if state == nil {
		return
	}
	state.clip = state.target.Bounds()
	state.translate = image.Point{}
	state.color = color.RGBA{A: 0xff}
}

func (r *ktfRuntime) syncKTFGraphics(instance uint32) error {
	state := r.graphics[instance]
	serviceID := r.graphicsServices[instance]
	if state == nil || serviceID == 0 {
		return nil
	}
	bounds := state.target.Bounds()
	descriptor, err := r.services.Graphics.Descriptor(
		r.serviceOwner,
		serviceID,
	)
	if err != nil {
		return err
	}
	if descriptor.Width != int32(bounds.Dx()) ||
		descriptor.Height != int32(bounds.Dy()) ||
		descriptor.Format != shared.PixelRGBA8888 {
		return fmt.Errorf(
			"KTF graphics 0x%08x service geometry differs",
			instance,
		)
	}
	if state.pixelsDirty {
		if err := r.services.Graphics.ReplacePixels(
			r.serviceOwner,
			serviceID,
			ktfRGBABytes(state.target),
		); err != nil {
			return err
		}
		state.pixelsDirty = false
	}
	if state.translate.X < -(1<<31) || state.translate.X > 1<<31-1 ||
		state.translate.Y < -(1<<31) || state.translate.Y > 1<<31-1 {
		return fmt.Errorf("KTF graphics translation overflows service state")
	}
	return r.services.Graphics.SetDrawState(
		r.serviceOwner,
		serviceID,
		shared.SurfaceDrawState{
			Clip: shared.Rectangle{
				X:      int32(state.clip.Min.X),
				Y:      int32(state.clip.Min.Y),
				Width:  int32(state.clip.Dx()),
				Height: int32(state.clip.Dy()),
			},
			TranslateX:  int32(state.translate.X),
			TranslateY:  int32(state.translate.Y),
			Raster:      shared.RasterCopy,
			GlobalAlpha: state.color.A,
		},
	)
}

func (r *ktfRuntime) handleGraphicsMethod(
	name, descriptor string,
) (uint32, error) {
	instance, err := r.parameter(1)
	if err != nil {
		return 0, err
	}
	state := r.graphics[instance]
	switch name + descriptor {
	case "<init>(Lorg/kwis/msp/lcdui/Display;)V",
		"<init>(Ljavax/microedition/lcdui/Graphics;)V":
		if state == nil {
			if r.frame == nil {
				r.frame = image.NewRGBA(image.Rect(0, 0, 240, 320))
			}
			r.graphics[instance] = &ktfGraphics{
				target: r.frame,
				clip:   r.frame.Bounds(),
				color:  color.RGBA{A: 0xff},
			}
			screen, screenErr := r.ensureScreenGraphics()
			if screenErr != nil {
				return 0, screenErr
			}
			r.graphicsServices[instance] = r.graphicsServices[screen]
		}
		return 0, nil
	case "getFont()Lorg/kwis/msp/lcdui/Font;":
		return r.ensureDefaultFont()
	case "setColor(I)V":
		if state == nil {
			return 0, nil
		}
		value, valueErr := r.parameter(2)
		if valueErr != nil {
			return 0, valueErr
		}
		state.color.R = uint8(value >> 16)
		state.color.G = uint8(value >> 8)
		state.color.B = uint8(value)
		return 0, nil
	case "setColor(III)V":
		if state == nil {
			return 0, nil
		}
		red, valueErr := r.parameter(2)
		if valueErr != nil {
			return 0, valueErr
		}
		green, valueErr := r.parameter(3)
		if valueErr != nil {
			return 0, valueErr
		}
		blue, valueErr := r.parameter(4)
		if valueErr != nil {
			return 0, valueErr
		}
		state.color.R = uint8(red)
		state.color.G = uint8(green)
		state.color.B = uint8(blue)
		return 0, nil
	case "setAlpha(I)V":
		if state != nil {
			alpha, valueErr := r.parameter(2)
			if valueErr != nil {
				return 0, valueErr
			}
			state.color.A = uint8(alpha)
		}
		return 0, nil
	case "fillRect(IIII)V", "fillRoundRect(IIIIII)V", "fillArc(IIIIII)V":
		if state == nil {
			return 0, nil
		}
		rect, valueErr := r.graphicsRectangle(state, 2)
		if valueErr != nil {
			return 0, valueErr
		}
		if name == "fillRect" && r.menuForegroundCompat != nil {
			bounds := state.target.Bounds()
			if rect.Min.X <= bounds.Min.X && rect.Min.Y <= bounds.Min.Y &&
				rect.Max.X >= bounds.Max.X && rect.Max.Y >= bounds.Max.Y-20 {
				r.menuForegroundCompat.pending = nil
			}
		}
		draw.Draw(state.target, rect.Intersect(state.clip), image.NewUniform(state.color), image.Point{}, draw.Src)
		state.pixelsDirty = true
		return 0, nil
	case "drawLine(IIII)V":
		if state == nil {
			return 0, nil
		}
		x1, valueErr := r.signedParameter(2)
		if valueErr != nil {
			return 0, valueErr
		}
		y1, valueErr := r.signedParameter(3)
		if valueErr != nil {
			return 0, valueErr
		}
		x2, valueErr := r.signedParameter(4)
		if valueErr != nil {
			return 0, valueErr
		}
		y2, valueErr := r.signedParameter(5)
		if valueErr != nil {
			return 0, valueErr
		}
		r.drawGraphicsLine(
			state,
			x1+state.translate.X,
			y1+state.translate.Y,
			x2+state.translate.X,
			y2+state.translate.Y,
		)
		state.pixelsDirty = true
		return 0, nil
	case "drawRect(IIII)V", "drawRoundRect(IIIIII)V", "drawArc(IIIIII)V":
		if state == nil {
			return 0, nil
		}
		rect, valueErr := r.graphicsRectangle(state, 2)
		if valueErr != nil {
			return 0, valueErr
		}
		r.drawGraphicsRectangle(state, rect)
		state.pixelsDirty = true
		return 0, nil
	case "drawChar(CIII)V":
		character, valueErr := r.parameter(2)
		if valueErr != nil {
			return 0, valueErr
		}
		return 0, r.drawGraphicsTextParameters(
			state,
			string(rune(character)),
			3,
		)
	case "drawChars([CIIIII)V":
		array, valueErr := r.parameter(2)
		if valueErr != nil {
			return 0, valueErr
		}
		offset, valueErr := r.parameter(3)
		if valueErr != nil {
			return 0, valueErr
		}
		count, valueErr := r.parameter(4)
		if valueErr != nil {
			return 0, valueErr
		}
		text, valueErr := r.readJavaCharArrayRange(array, offset, count)
		if valueErr != nil {
			return 0, valueErr
		}
		return 0, r.drawGraphicsTextParameters(state, text, 5)
	case "drawString(Ljava/lang/String;III)V":
		value, valueErr := r.parameter(2)
		if valueErr != nil {
			return 0, valueErr
		}
		return 0, r.drawGraphicsTextParameters(
			state,
			r.javaStringValue(value),
			3,
		)
	case "drawSubstring(Ljava/lang/String;IIIII)V":
		value, valueErr := r.parameter(2)
		if valueErr != nil {
			return 0, valueErr
		}
		offset, valueErr := r.parameter(3)
		if valueErr != nil {
			return 0, valueErr
		}
		count, valueErr := r.parameter(4)
		if valueErr != nil {
			return 0, valueErr
		}
		runes := []rune(r.javaStringValue(value))
		if offset > uint32(len(runes)) ||
			count > uint32(len(runes))-offset {
			return 0, nil
		}
		return 0, r.drawGraphicsTextParameters(
			state,
			string(runes[offset:offset+count]),
			5,
		)
	case "drawImage(Lorg/kwis/msp/lcdui/Image;III)V":
		if state == nil {
			return 0, nil
		}
		imageAddress, valueErr := r.parameter(2)
		if valueErr != nil {
			return 0, valueErr
		}
		source := r.images[imageAddress]
		if source == nil {
			return 0, nil
		}
		x, valueErr := r.signedParameter(3)
		if valueErr != nil {
			return 0, valueErr
		}
		y, valueErr := r.signedParameter(4)
		if valueErr != nil {
			return 0, valueErr
		}
		anchor, valueErr := r.parameter(5)
		if valueErr != nil {
			return 0, valueErr
		}
		r.drawKTFJavaImage(state, imageAddress, source, x, y, anchor)
		return 0, nil
	case "setClip(IIII)V":
		if state == nil {
			return 0, nil
		}
		rect, valueErr := r.graphicsRectangle(state, 2)
		if valueErr != nil {
			return 0, valueErr
		}
		state.clip = rect.Intersect(state.target.Bounds())
		return 0, nil
	case "clipRect(IIII)V":
		if state == nil {
			return 0, nil
		}
		rect, valueErr := r.graphicsRectangle(state, 2)
		if valueErr != nil {
			return 0, valueErr
		}
		state.clip = state.clip.Intersect(rect)
		return 0, nil
	case "getColor()I":
		if state == nil {
			return 0, nil
		}
		return uint32(state.color.R)<<16 |
			uint32(state.color.G)<<8 |
			uint32(state.color.B), nil
	case "getAlpha()I":
		if state == nil {
			return 0xff, nil
		}
		return uint32(state.color.A), nil
	case "getRedComponent()I":
		if state == nil {
			return 0, nil
		}
		return uint32(state.color.R), nil
	case "getGreenComponent()I":
		if state == nil {
			return 0, nil
		}
		return uint32(state.color.G), nil
	case "getBlueComponent()I":
		if state == nil {
			return 0, nil
		}
		return uint32(state.color.B), nil
	case "getGrayScale()I":
		if state == nil {
			return 0, nil
		}
		return (uint32(state.color.R)*77 +
			uint32(state.color.G)*150 +
			uint32(state.color.B)*29) >> 8, nil
	case "getPixel(II)I":
		if state == nil {
			return 0, nil
		}
		x, valueErr := r.signedParameter(2)
		if valueErr != nil {
			return 0, valueErr
		}
		y, valueErr := r.signedParameter(3)
		if valueErr != nil {
			return 0, valueErr
		}
		point := image.Pt(x+state.translate.X, y+state.translate.Y)
		if !point.In(state.target.Bounds()) {
			return 0, nil
		}
		red, green, blue, _ := state.target.At(point.X, point.Y).RGBA()
		return uint32(red>>8)<<16 |
			uint32(green>>8)<<8 |
			uint32(blue>>8), nil
	case "getPixels(IIII[BII)V":
		return 0, r.copyGraphicsPixelsToByteArray(state)
	case "getClipX()I":
		if state == nil {
			return 0, nil
		}
		return uint32(int32(state.clip.Min.X - state.translate.X)), nil
	case "getClipY()I":
		if state == nil {
			return 0, nil
		}
		return uint32(int32(state.clip.Min.Y - state.translate.Y)), nil
	case "getClipWidth()I":
		if state == nil {
			return 0, nil
		}
		return uint32(state.clip.Dx()), nil
	case "getClipHeight()I":
		if state == nil {
			return 0, nil
		}
		return uint32(state.clip.Dy()), nil
	case "getTranslateX()I":
		if state == nil {
			return 0, nil
		}
		return uint32(int32(state.translate.X)), nil
	case "getTranslateY()I":
		if state == nil {
			return 0, nil
		}
		return uint32(int32(state.translate.Y)), nil
	case "translate(II)V":
		if state == nil {
			return 0, nil
		}
		x, valueErr := r.signedParameter(2)
		if valueErr != nil {
			return 0, valueErr
		}
		y, valueErr := r.signedParameter(3)
		if valueErr != nil {
			return 0, valueErr
		}
		state.translate = state.translate.Add(image.Pt(x, y))
		return 0, nil
	case "setPixel(II)V":
		if state == nil {
			return 0, nil
		}
		x, valueErr := r.signedParameter(2)
		if valueErr != nil {
			return 0, valueErr
		}
		y, valueErr := r.signedParameter(3)
		if valueErr != nil {
			return 0, valueErr
		}
		point := image.Pt(x+state.translate.X, y+state.translate.Y)
		if point.In(state.clip) {
			state.target.Set(point.X, point.Y, state.color)
			state.pixelsDirty = true
		}
		return 0, nil
	case "setGrayScale(I)V":
		if state != nil {
			value, valueErr := r.parameter(2)
			if valueErr != nil {
				return 0, valueErr
			}
			gray := uint8(value)
			state.color.R, state.color.G, state.color.B = gray, gray, gray
		}
		return 0, nil
	case "encodeImage(IIII)[B":
		return r.newJavaByteArray(nil)
	default:
		return 0, nil
	}
}

func (r *ktfRuntime) drawGraphicsTextParameters(
	state *ktfGraphics,
	text string,
	firstPositionParameter uint32,
) error {
	if state == nil || text == "" {
		return nil
	}
	x, err := r.signedParameter(firstPositionParameter)
	if err != nil {
		return err
	}
	y, err := r.signedParameter(firstPositionParameter + 1)
	if err != nil {
		return err
	}
	anchor, err := r.parameter(firstPositionParameter + 2)
	if err != nil {
		return err
	}
	return r.drawGraphicsTextShared(state, text, x, y, anchor)
}

func (r *ktfRuntime) drawGraphicsTextShared(
	state *ktfGraphics,
	text string,
	x, y int,
	anchor uint32,
) error {
	var serviceID shared.ServiceID
	var graphicsInstance uint32
	for instance, candidate := range r.graphics {
		if candidate == state {
			graphicsInstance = instance
			serviceID = r.graphicsServices[instance]
			break
		}
	}
	if serviceID == 0 {
		return fmt.Errorf("KTF graphics text target has no shared surface")
	}
	if err := r.syncKTFGraphics(graphicsInstance); err != nil {
		return err
	}
	font, err := r.ensureDefaultFont()
	if err != nil {
		return err
	}
	fontID, err := r.ensureKTFFontService(font)
	if err != nil {
		return err
	}
	textAnchor := shared.AnchorLeft | shared.AnchorTop
	switch {
	case anchor&8 != 0:
		textAnchor = textAnchor&^shared.AnchorLeft | shared.AnchorRight
	case anchor&1 != 0:
		textAnchor = textAnchor&^shared.AnchorLeft |
			shared.AnchorHorizontalCenter
	}
	switch {
	case anchor&32 != 0:
		textAnchor = textAnchor&^shared.AnchorTop | shared.AnchorBottom
	case anchor&2 != 0:
		textAnchor = textAnchor&^shared.AnchorTop |
			shared.AnchorVerticalCenter
	case anchor&64 != 0:
		textAnchor = textAnchor&^shared.AnchorTop | shared.AnchorBaseline
	}
	if err := r.services.Text.Draw(
		r.serviceOwner,
		fontID,
		serviceID,
		text,
		int32(x),
		int32(y),
		textAnchor,
		shared.Color{
			R: state.color.R,
			G: state.color.G,
			B: state.color.B,
			A: 0xff,
		},
	); err != nil {
		return err
	}
	pixels, err := r.services.Graphics.RGBA(r.serviceOwner, serviceID)
	if err != nil {
		return err
	}
	bounds := state.target.Bounds()
	if len(pixels) != bounds.Dx()*bounds.Dy()*4 {
		return fmt.Errorf("KTF text surface geometry changed")
	}
	source := image.NewRGBA(image.Rect(0, 0, bounds.Dx(), bounds.Dy()))
	copy(source.Pix, pixels)
	draw.Draw(state.target, bounds, source, image.Point{}, draw.Src)
	state.pixelsDirty = false
	return nil
}

// drawGraphicsText is retained as a compatibility reference for the handset
// bitmap metrics; active drawing goes through the shared Text service above.
func (r *ktfRuntime) drawGraphicsText(
	state *ktfGraphics,
	text string,
	x, y int,
	anchor uint32,
) {
	const (
		glyphAdvance = 6
		fontHeight   = 12
		glyphTop     = 2
	)
	runes := []rune(text)
	width := len(runes) * glyphAdvance
	switch {
	case anchor&8 != 0:
		x -= width
	case anchor&1 != 0:
		x -= width / 2
	}
	switch {
	case anchor&32 != 0:
		y -= fontHeight
	case anchor&2 != 0:
		y -= fontHeight / 2
	case anchor&64 != 0:
		y -= 10
	}
	x += state.translate.X
	y += state.translate.Y
	for _, character := range runes {
		rows := ktfBasicGlyph(character)
		for row, bits := range rows {
			for column := 0; column < 5; column++ {
				if bits&(1<<uint(4-column)) == 0 {
					continue
				}
				point := image.Pt(x+column, y+glyphTop+row)
				if point.In(state.clip) &&
					point.In(state.target.Bounds()) {
					state.target.Set(point.X, point.Y, state.color)
				}
			}
		}
		x += glyphAdvance
	}
}

func ktfBasicGlyph(character rune) [7]uint8 {
	if character >= 'a' && character <= 'z' {
		character = unicode.ToUpper(character)
	}
	if glyph, ok := ktfBasicGlyphs[character]; ok {
		return glyph
	}
	if unicode.IsSpace(character) {
		return [7]uint8{}
	}
	// A deterministic outlined fallback keeps non-ASCII handset text visible
	// without depending on a host font or proprietary device font.
	middle := uint8((uint32(character) ^ uint32(character>>5)) & 0x0e)
	return [7]uint8{0x1f, 0x11, 0x11 | middle, 0x11, 0x11 | middle, 0x11, 0x1f}
}

var ktfBasicGlyphs = map[rune][7]uint8{
	' ':  {0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00},
	'!':  {0x04, 0x04, 0x04, 0x04, 0x04, 0x00, 0x04},
	'"':  {0x0a, 0x0a, 0x0a, 0x00, 0x00, 0x00, 0x00},
	'#':  {0x0a, 0x1f, 0x0a, 0x0a, 0x1f, 0x0a, 0x00},
	'%':  {0x19, 0x19, 0x02, 0x04, 0x08, 0x13, 0x13},
	'&':  {0x0c, 0x12, 0x14, 0x08, 0x15, 0x12, 0x0d},
	'\'': {0x04, 0x04, 0x08, 0x00, 0x00, 0x00, 0x00},
	'(':  {0x02, 0x04, 0x08, 0x08, 0x08, 0x04, 0x02},
	')':  {0x08, 0x04, 0x02, 0x02, 0x02, 0x04, 0x08},
	'*':  {0x00, 0x0a, 0x04, 0x1f, 0x04, 0x0a, 0x00},
	'+':  {0x00, 0x04, 0x04, 0x1f, 0x04, 0x04, 0x00},
	',':  {0x00, 0x00, 0x00, 0x00, 0x04, 0x04, 0x08},
	'-':  {0x00, 0x00, 0x00, 0x1f, 0x00, 0x00, 0x00},
	'.':  {0x00, 0x00, 0x00, 0x00, 0x00, 0x0c, 0x0c},
	'/':  {0x01, 0x02, 0x02, 0x04, 0x08, 0x08, 0x10},
	'0':  {0x0e, 0x11, 0x13, 0x15, 0x19, 0x11, 0x0e},
	'1':  {0x04, 0x0c, 0x14, 0x04, 0x04, 0x04, 0x1f},
	'2':  {0x0e, 0x11, 0x01, 0x02, 0x04, 0x08, 0x1f},
	'3':  {0x1e, 0x01, 0x01, 0x0e, 0x01, 0x01, 0x1e},
	'4':  {0x02, 0x06, 0x0a, 0x12, 0x1f, 0x02, 0x02},
	'5':  {0x1f, 0x10, 0x10, 0x1e, 0x01, 0x01, 0x1e},
	'6':  {0x0e, 0x10, 0x10, 0x1e, 0x11, 0x11, 0x0e},
	'7':  {0x1f, 0x01, 0x02, 0x04, 0x08, 0x08, 0x08},
	'8':  {0x0e, 0x11, 0x11, 0x0e, 0x11, 0x11, 0x0e},
	'9':  {0x0e, 0x11, 0x11, 0x0f, 0x01, 0x01, 0x0e},
	':':  {0x00, 0x0c, 0x0c, 0x00, 0x0c, 0x0c, 0x00},
	';':  {0x00, 0x0c, 0x0c, 0x00, 0x04, 0x04, 0x08},
	'<':  {0x02, 0x04, 0x08, 0x10, 0x08, 0x04, 0x02},
	'=':  {0x00, 0x00, 0x1f, 0x00, 0x1f, 0x00, 0x00},
	'>':  {0x08, 0x04, 0x02, 0x01, 0x02, 0x04, 0x08},
	'?':  {0x0e, 0x11, 0x01, 0x02, 0x04, 0x00, 0x04},
	'@':  {0x0e, 0x11, 0x17, 0x15, 0x17, 0x10, 0x0e},
	'A':  {0x0e, 0x11, 0x11, 0x1f, 0x11, 0x11, 0x11},
	'B':  {0x1e, 0x11, 0x11, 0x1e, 0x11, 0x11, 0x1e},
	'C':  {0x0f, 0x10, 0x10, 0x10, 0x10, 0x10, 0x0f},
	'D':  {0x1e, 0x11, 0x11, 0x11, 0x11, 0x11, 0x1e},
	'E':  {0x1f, 0x10, 0x10, 0x1e, 0x10, 0x10, 0x1f},
	'F':  {0x1f, 0x10, 0x10, 0x1e, 0x10, 0x10, 0x10},
	'G':  {0x0f, 0x10, 0x10, 0x13, 0x11, 0x11, 0x0f},
	'H':  {0x11, 0x11, 0x11, 0x1f, 0x11, 0x11, 0x11},
	'I':  {0x0e, 0x04, 0x04, 0x04, 0x04, 0x04, 0x0e},
	'J':  {0x01, 0x01, 0x01, 0x01, 0x11, 0x11, 0x0e},
	'K':  {0x11, 0x12, 0x14, 0x18, 0x14, 0x12, 0x11},
	'L':  {0x10, 0x10, 0x10, 0x10, 0x10, 0x10, 0x1f},
	'M':  {0x11, 0x1b, 0x15, 0x15, 0x11, 0x11, 0x11},
	'N':  {0x11, 0x19, 0x15, 0x13, 0x11, 0x11, 0x11},
	'O':  {0x0e, 0x11, 0x11, 0x11, 0x11, 0x11, 0x0e},
	'P':  {0x1e, 0x11, 0x11, 0x1e, 0x10, 0x10, 0x10},
	'Q':  {0x0e, 0x11, 0x11, 0x11, 0x15, 0x12, 0x0d},
	'R':  {0x1e, 0x11, 0x11, 0x1e, 0x14, 0x12, 0x11},
	'S':  {0x0f, 0x10, 0x10, 0x0e, 0x01, 0x01, 0x1e},
	'T':  {0x1f, 0x04, 0x04, 0x04, 0x04, 0x04, 0x04},
	'U':  {0x11, 0x11, 0x11, 0x11, 0x11, 0x11, 0x0e},
	'V':  {0x11, 0x11, 0x11, 0x11, 0x11, 0x0a, 0x04},
	'W':  {0x11, 0x11, 0x11, 0x15, 0x15, 0x15, 0x0a},
	'X':  {0x11, 0x11, 0x0a, 0x04, 0x0a, 0x11, 0x11},
	'Y':  {0x11, 0x11, 0x0a, 0x04, 0x04, 0x04, 0x04},
	'Z':  {0x1f, 0x01, 0x02, 0x04, 0x08, 0x10, 0x1f},
	'[':  {0x0e, 0x08, 0x08, 0x08, 0x08, 0x08, 0x0e},
	'\\': {0x10, 0x08, 0x08, 0x04, 0x02, 0x02, 0x01},
	']':  {0x0e, 0x02, 0x02, 0x02, 0x02, 0x02, 0x0e},
	'^':  {0x04, 0x0a, 0x11, 0x00, 0x00, 0x00, 0x00},
	'_':  {0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x1f},
}

func (r *ktfRuntime) copyGraphicsPixelsToByteArray(
	state *ktfGraphics,
) error {
	x, err := r.signedParameter(2)
	if err != nil {
		return err
	}
	y, err := r.signedParameter(3)
	if err != nil {
		return err
	}
	width, err := r.signedParameter(4)
	if err != nil {
		return err
	}
	height, err := r.signedParameter(5)
	if err != nil {
		return err
	}
	array, err := r.parameter(6)
	if err != nil {
		return err
	}
	offsetValue, err := r.parameter(7)
	if err != nil {
		return err
	}
	bytesPerLineValue, err := r.parameter(8)
	if err != nil {
		return err
	}
	offset := int64(int32(offsetValue))
	bytesPerLine := int64(int32(bytesPerLineValue))
	if width < 0 || height < 0 || offset < 0 ||
		bytesPerLine < int64(width) {
		return fmt.Errorf(
			"invalid KTF Graphics.getPixels rectangle %dx%d "+
				"offset=%d bytes-per-line=%d",
			width,
			height,
			offset,
			bytesPerLine,
		)
	}
	length, err := r.javaArrayLength(array)
	if err != nil {
		return err
	}
	required := offset
	if height > 0 {
		required += int64(height-1)*bytesPerLine + int64(width)
	}
	if required > int64(length) {
		return fmt.Errorf(
			"KTF Graphics.getPixels destination requires %d bytes, has %d",
			required,
			length,
		)
	}
	fields, err := r.readU32(array)
	if err != nil {
		return err
	}
	row := make([]byte, width)
	for rowIndex := 0; rowIndex < height; rowIndex++ {
		clear(row)
		if state != nil {
			sourceY := y + rowIndex + state.translate.Y
			for column := 0; column < width; column++ {
				sourceX := x + column + state.translate.X
				point := image.Pt(sourceX, sourceY)
				if !point.In(state.target.Bounds()) {
					continue
				}
				red, green, blue, _ := state.target.At(
					point.X,
					point.Y,
				).RGBA()
				row[column] = uint8((uint32(red>>8)*77 +
					uint32(green>>8)*150 +
					uint32(blue>>8)*29) >> 8)
			}
		}
		destination := fields + 8 + uint32(
			offset+int64(rowIndex)*bytesPerLine,
		)
		if err := r.cpu.WriteMemory(destination, row); err != nil {
			return err
		}
	}
	return nil
}

func (r *ktfRuntime) signedParameter(index uint32) (int, error) {
	value, err := r.parameter(index)
	return int(int32(value)), err
}

func (r *ktfRuntime) graphicsRectangle(
	state *ktfGraphics,
	firstParameter uint32,
) (image.Rectangle, error) {
	x, err := r.signedParameter(firstParameter)
	if err != nil {
		return image.Rectangle{}, err
	}
	y, err := r.signedParameter(firstParameter + 1)
	if err != nil {
		return image.Rectangle{}, err
	}
	width, err := r.signedParameter(firstParameter + 2)
	if err != nil {
		return image.Rectangle{}, err
	}
	height, err := r.signedParameter(firstParameter + 3)
	if err != nil {
		return image.Rectangle{}, err
	}
	x += state.translate.X
	y += state.translate.Y
	return image.Rect(x, y, x+width, y+height), nil
}

func (r *ktfRuntime) drawGraphicsRectangle(
	state *ktfGraphics,
	rect image.Rectangle,
) {
	if rect.Empty() {
		return
	}
	r.drawGraphicsLine(state, rect.Min.X, rect.Min.Y, rect.Max.X-1, rect.Min.Y)
	r.drawGraphicsLine(state, rect.Min.X, rect.Max.Y-1, rect.Max.X-1, rect.Max.Y-1)
	r.drawGraphicsLine(state, rect.Min.X, rect.Min.Y, rect.Min.X, rect.Max.Y-1)
	r.drawGraphicsLine(state, rect.Max.X-1, rect.Min.Y, rect.Max.X-1, rect.Max.Y-1)
}

func (r *ktfRuntime) drawGraphicsLine(
	state *ktfGraphics,
	x1, y1, x2, y2 int,
) {
	dx := abs(x2 - x1)
	stepX := -1
	if x1 < x2 {
		stepX = 1
	}
	dy := -abs(y2 - y1)
	stepY := -1
	if y1 < y2 {
		stepY = 1
	}
	lineError := dx + dy
	for {
		point := image.Pt(x1, y1)
		if point.In(state.clip) && point.In(state.target.Bounds()) {
			state.target.Set(x1, y1, state.color)
		}
		if x1 == x2 && y1 == y2 {
			return
		}
		doubleError := lineError * 2
		if doubleError >= dy {
			lineError += dy
			x1 += stepX
		}
		if doubleError <= dx {
			lineError += dx
			y1 += stepY
		}
	}
}

func (r *ktfRuntime) handleInputStreamMethod(
	ctx context.Context,
	name, descriptor string,
) (uint32, error) {
	instance, err := r.parameter(1)
	if err != nil {
		return 0, err
	}
	streamInstance := instance
	for depth := 0; depth < 64; depth++ {
		redirected := r.inputTargets[streamInstance]
		if redirected == 0 || redirected == streamInstance {
			break
		}
		streamInstance = redirected
	}
	stream := r.inputStreams[streamInstance]
	readBytes := func(count uint32) ([]byte, bool, error) {
		if delegated, valueErr := r.shouldDelegateInputRead(streamInstance); valueErr != nil {
			return nil, false, valueErr
		} else if delegated {
			data := make([]byte, count)
			for index := range data {
				value, valueErr := r.invokeJavaVirtual(
					ctx,
					streamInstance,
					"read",
					"()I",
				)
				if valueErr != nil {
					return nil, false, valueErr
				}
				if value == ^uint32(0) {
					return nil, false, nil
				}
				data[index] = byte(value)
			}
			return data, true, nil
		}
		if stream == nil ||
			stream.position > uint32(len(stream.data)) ||
			count > uint32(len(stream.data))-stream.position {
			return nil, false, nil
		}
		data := stream.data[stream.position : stream.position+count]
		stream.position += count
		return data, true, nil
	}
	switch name + descriptor {
	case "<init>()V":
		if stream == nil {
			r.inputStreams[instance] = &ktfInputStream{}
		}
		return 0, nil
	case "<init>([B)V", "<init>([BII)V":
		array, valueErr := r.parameter(2)
		if valueErr != nil {
			return 0, valueErr
		}
		var data []byte
		if array == 0 {
			data = nil
		} else if descriptor == "([BII)V" {
			offset, valueErr := r.parameter(3)
			if valueErr != nil {
				return 0, valueErr
			}
			count, valueErr := r.parameter(4)
			if valueErr != nil {
				return 0, valueErr
			}
			data, valueErr = r.readJavaByteArrayRange(array, offset, count)
			if valueErr != nil {
				return 0, valueErr
			}
		} else {
			data, valueErr = r.readJavaByteArray(array)
			if valueErr != nil {
				return 0, valueErr
			}
		}
		r.inputStreams[instance] = &ktfInputStream{data: data}
		return 0, nil
	case "<init>(Ljava/io/InputStream;)V":
		source, valueErr := r.parameter(2)
		if valueErr != nil {
			return 0, valueErr
		}
		r.inputTargets[instance] = source
		return 0, nil
	case "available()I":
		if stream == nil || stream.position >= uint32(len(stream.data)) {
			return 0, nil
		}
		return uint32(len(stream.data)) - stream.position, nil
	case "read()I":
		if stream == nil || stream.position >= uint32(len(stream.data)) {
			return ^uint32(0), nil
		}
		value := stream.data[stream.position]
		stream.position++
		return uint32(value), nil
	case "read([B)I":
		array, valueErr := r.parameter(2)
		if valueErr != nil {
			return 0, valueErr
		}
		if array == 0 {
			return ^uint32(0), nil
		}
		length, valueErr := r.javaArrayLength(array)
		if valueErr != nil {
			return 0, valueErr
		}
		return r.readInputStreamInto(stream, array, 0, length)
	case "read([BII)I":
		array, valueErr := r.parameter(2)
		if valueErr != nil {
			return 0, valueErr
		}
		if array == 0 {
			return ^uint32(0), nil
		}
		offset, valueErr := r.parameter(3)
		if valueErr != nil {
			return 0, valueErr
		}
		count, valueErr := r.parameter(4)
		if valueErr != nil {
			return 0, valueErr
		}
		return r.readInputStreamInto(stream, array, offset, count)
	case "close()V":
		delete(r.inputStreams, streamInstance)
		delete(r.inputTargets, instance)
		return 0, nil
	case "skip(J)J":
		if stream == nil {
			return 0, nil
		}
		low, valueErr := r.parameter(2)
		if valueErr != nil {
			return 0, valueErr
		}
		high, valueErr := r.parameter(3)
		if valueErr != nil {
			return 0, valueErr
		}
		requested := uint64(high)<<32 | uint64(low)
		remaining := uint64(len(stream.data)) - uint64(stream.position)
		if requested > remaining {
			requested = remaining
		}
		stream.position += uint32(requested)
		return r.javaLongResult(requested), nil
	case "mark(I)V":
		// Streams are fully buffered in host memory, so the read-ahead limit
		// carries no obligation and every mark stays valid.
		if stream != nil {
			stream.mark = stream.position
		}
		return 0, nil
	case "markSupported()Z":
		return 1, nil
	case "reset()V":
		// reset returns to the last mark, not to the start. Rewinding to zero
		// silently desynchronised every subsequent read for titles that scan
		// a resource with mark/reset, which then decoded record lengths from
		// the wrong offset.
		if stream != nil {
			stream.position = stream.mark
		}
		return 0, nil
	case "readBoolean()Z", "readUnsignedByte()I":
		data, ok, valueErr := readBytes(1)
		if valueErr != nil {
			return 0, valueErr
		}
		if !ok {
			return r.raiseJavaException("java/io/EOFException", 0)
		}
		return uint32(data[0]), nil
	case "readByte()B":
		data, ok, valueErr := readBytes(1)
		if valueErr != nil {
			return 0, valueErr
		}
		if !ok {
			return r.raiseJavaException("java/io/EOFException", 0)
		}
		return uint32(int32(int8(data[0]))), nil
	case "readShort()S", "readUnsignedShort()I", "readChar()C":
		data, ok, valueErr := readBytes(2)
		if valueErr != nil {
			return 0, valueErr
		}
		if !ok {
			return r.raiseJavaException("java/io/EOFException", 0)
		}
		value := binary.BigEndian.Uint16(data)
		if name == "readShort" {
			return uint32(int32(int16(value))), nil
		}
		return uint32(value), nil
	case "readInt()I", "readLong()J":
		count := uint32(4)
		if name == "readLong" {
			count = 8
		}
		data, ok, valueErr := readBytes(count)
		if valueErr != nil {
			return 0, valueErr
		}
		if !ok {
			return r.raiseJavaException("java/io/EOFException", 0)
		}
		if count == 8 {
			return r.javaLongResult(binary.BigEndian.Uint64(data)), nil
		}
		return binary.BigEndian.Uint32(data), nil
	case "readUTF()Ljava/lang/String;":
		header, ok, valueErr := readBytes(2)
		if valueErr != nil {
			return 0, valueErr
		}
		if !ok {
			return r.raiseJavaException("java/io/EOFException", 0)
		}
		data, ok, valueErr := readBytes(
			uint32(binary.BigEndian.Uint16(header)),
		)
		if valueErr != nil {
			return 0, valueErr
		}
		if !ok {
			return r.raiseJavaException("java/io/EOFException", 0)
		}
		value, valueErr := decodeKTFModifiedUTF8(data)
		if valueErr != nil {
			return r.raiseJavaException("java/io/UTFDataFormatException", 0)
		}
		return r.newJavaString(value)
	case "readFully([B)V", "readFully([BII)V":
		array, valueErr := r.parameter(2)
		if valueErr != nil {
			return 0, valueErr
		}
		offset := uint32(0)
		count, valueErr := r.javaArrayLength(array)
		if valueErr != nil {
			return 0, valueErr
		}
		if descriptor == "([BII)V" {
			offset, valueErr = r.parameter(3)
			if valueErr != nil {
				return 0, valueErr
			}
			count, valueErr = r.parameter(4)
			if valueErr != nil {
				return 0, valueErr
			}
		}
		if count == 0 {
			return 0, nil
		}
		read, valueErr := r.readInputStreamInto(stream, array, offset, count)
		if valueErr != nil {
			return 0, valueErr
		}
		if read != count {
			return r.raiseJavaException("java/io/EOFException", 0)
		}
		return 0, nil
	case "skipBytes(I)I":
		requested, valueErr := r.parameter(2)
		if valueErr != nil {
			return 0, valueErr
		}
		if stream == nil {
			return 0, nil
		}
		remaining := uint32(len(stream.data)) - stream.position
		if requested > remaining {
			requested = remaining
		}
		stream.position += requested
		return requested, nil
	default:
		return 0, nil
	}
}

func decodeKTFModifiedUTF8(data []byte) (string, error) {
	units := make([]uint16, 0, len(data))
	for offset := 0; offset < len(data); {
		first := data[offset]
		switch {
		case first&0x80 == 0:
			units = append(units, uint16(first))
			offset++
		case first&0xe0 == 0xc0:
			if offset+1 >= len(data) || data[offset+1]&0xc0 != 0x80 {
				return "", fmt.Errorf(
					"malformed modified UTF-8 at byte %d",
					offset,
				)
			}
			units = append(
				units,
				uint16(first&0x1f)<<6|uint16(data[offset+1]&0x3f),
			)
			offset += 2
		case first&0xf0 == 0xe0:
			if offset+2 >= len(data) ||
				data[offset+1]&0xc0 != 0x80 ||
				data[offset+2]&0xc0 != 0x80 {
				return "", fmt.Errorf(
					"malformed modified UTF-8 at byte %d",
					offset,
				)
			}
			units = append(
				units,
				uint16(first&0x0f)<<12|
					uint16(data[offset+1]&0x3f)<<6|
					uint16(data[offset+2]&0x3f),
			)
			offset += 3
		default:
			return "", fmt.Errorf(
				"malformed modified UTF-8 at byte %d",
				offset,
			)
		}
	}
	return string(utf16.Decode(units)), nil
}

func (r *ktfRuntime) shouldDelegateInputRead(instance uint32) (bool, error) {
	if instance == 0 {
		return false, nil
	}
	words, err := r.readWords(instance, 2)
	if err != nil {
		return false, err
	}
	methodAddress, err := r.resolveJavaMethod(words[1], "read", "()I")
	if err != nil {
		return false, nil
	}
	method, err := r.inspectJavaMethod(methodAddress)
	if err != nil {
		return false, err
	}
	if method.Body == 0 {
		return false, nil
	}
	_, isHostMethod := r.hostCalls[method.Body&^1]
	return !isHostMethod, nil
}

func (r *ktfRuntime) readInputStreamInto(
	stream *ktfInputStream,
	array, offset, count uint32,
) (uint32, error) {
	if stream == nil || stream.position >= uint32(len(stream.data)) {
		return ^uint32(0), nil
	}
	length, err := r.javaArrayLength(array)
	if err != nil {
		return 0, err
	}
	if offset > length || count > length-offset {
		return 0, fmt.Errorf(
			"KTF Java byte array range [%d,%d) exceeds length %d",
			offset,
			offset+count,
			length,
		)
	}
	remaining := uint32(len(stream.data)) - stream.position
	if count > remaining {
		count = remaining
	}
	fields, err := r.readU32(array)
	if err != nil {
		return 0, err
	}
	if err := r.cpu.WriteMemory(
		fields+8+offset,
		stream.data[stream.position:stream.position+count],
	); err != nil {
		return 0, err
	}
	stream.position += count
	return count, nil
}

func (r *ktfRuntime) handleInputStreamReaderMethod(
	name, descriptor string,
) (uint32, error) {
	instance, err := r.parameter(1)
	if err != nil {
		return 0, err
	}
	switch name + descriptor {
	case "<init>(Ljava/io/InputStream;)V":
		source, valueErr := r.parameter(2)
		if valueErr != nil {
			return 0, valueErr
		}
		r.inputTargets[instance] = source
		return 0, nil
	case "read()I":
		source := r.inputReaderSource(instance)
		stream := r.inputStreams[source]
		if stream == nil || stream.position >= uint32(len(stream.data)) {
			return ^uint32(0), nil
		}
		characters, next, valueErr := r.decodeInputStreamReaderChars(stream, 1)
		if valueErr != nil {
			return 0, valueErr
		}
		if len(characters) == 0 {
			return ^uint32(0), nil
		}
		stream.position = next
		return uint32(characters[0]), nil
	case "read([C)I":
		array, valueErr := r.parameter(2)
		if valueErr != nil {
			return 0, valueErr
		}
		length, valueErr := r.javaArrayLength(array)
		if valueErr != nil {
			return 0, valueErr
		}
		return r.readInputStreamReaderChars(instance, array, 0, length)
	case "read([CII)I":
		array, valueErr := r.parameter(2)
		if valueErr != nil {
			return 0, valueErr
		}
		offset, valueErr := r.parameter(3)
		if valueErr != nil {
			return 0, valueErr
		}
		count, valueErr := r.parameter(4)
		if valueErr != nil {
			return 0, valueErr
		}
		return r.readInputStreamReaderChars(instance, array, offset, count)
	case "ready()Z":
		stream := r.inputStreams[r.inputReaderSource(instance)]
		return boolWord(
			stream != nil && stream.position < uint32(len(stream.data)),
		), nil
	case "skip(J)J":
		low, valueErr := r.parameter(2)
		if valueErr != nil {
			return 0, valueErr
		}
		high, valueErr := r.parameter(3)
		if valueErr != nil {
			return 0, valueErr
		}
		requested := int64(uint64(high)<<32 | uint64(low))
		if requested <= 0 {
			return r.javaLongResult(0), nil
		}
		stream := r.inputStreams[r.inputReaderSource(instance)]
		if stream == nil {
			return r.javaLongResult(0), nil
		}
		remaining := uint64(len(stream.data)) - uint64(stream.position)
		count := uint64(requested)
		if count > remaining {
			count = remaining
		}
		characters, next, valueErr := r.decodeInputStreamReaderChars(
			stream,
			uint32(count),
		)
		if valueErr != nil {
			return 0, valueErr
		}
		stream.position = next
		return r.javaLongResult(uint64(len(characters))), nil
	case "close()V":
		delete(r.inputTargets, instance)
		return 0, nil
	default:
		return 0, nil
	}
}

func (r *ktfRuntime) inputReaderSource(instance uint32) uint32 {
	source := r.inputTargets[instance]
	for depth := 0; source != 0 && depth < 64; depth++ {
		redirected := r.inputTargets[source]
		if redirected == 0 || redirected == source {
			break
		}
		source = redirected
	}
	return source
}

func (r *ktfRuntime) readInputStreamReaderChars(
	instance, array, offset, count uint32,
) (uint32, error) {
	length, err := r.javaArrayLength(array)
	if err != nil {
		return 0, err
	}
	if offset > length || count > length-offset {
		return 0, fmt.Errorf(
			"KTF Java char array range [%d,%d) exceeds length %d",
			offset,
			offset+count,
			length,
		)
	}
	if count == 0 {
		return 0, nil
	}
	stream := r.inputStreams[r.inputReaderSource(instance)]
	if stream == nil || stream.position >= uint32(len(stream.data)) {
		return ^uint32(0), nil
	}
	characters, next, err := r.decodeInputStreamReaderChars(stream, count)
	if err != nil {
		return 0, err
	}
	fields, err := r.readU32(array)
	if err != nil {
		return 0, err
	}
	encoded := make([]byte, len(characters)*2)
	for index, character := range characters {
		binary.LittleEndian.PutUint16(
			encoded[index*2:],
			character,
		)
	}
	if err := r.cpu.WriteMemory(fields+8+offset*2, encoded); err != nil {
		return 0, err
	}
	stream.position = next
	return uint32(len(characters)), nil
}

func (r *ktfRuntime) decodeInputStreamReaderChars(
	stream *ktfInputStream,
	count uint32,
) ([]uint16, uint32, error) {
	if stream == nil {
		return nil, 0, nil
	}
	if count == 0 || stream.position >= uint32(len(stream.data)) {
		return nil, stream.position, nil
	}
	remaining := uint32(len(stream.data)) - stream.position
	characters := make([]uint16, 0, min(count, remaining))
	position := stream.position
	for uint32(len(characters)) < count && position < uint32(len(stream.data)) {
		encodedSize := uint32(1)
		if stream.data[position]&0x80 != 0 {
			encodedSize = 2
		}
		if encodedSize > uint32(len(stream.data))-position {
			return nil, stream.position, fmt.Errorf("KTF Java InputStreamReader has truncated EUC-KR input")
		}
		value, err := r.services.Text.Decode(
			stream.data[position:position+encodedSize],
			shared.EncodingEUCKR,
		)
		if err != nil {
			return nil, stream.position, err
		}
		decoded := []rune(value)
		if len(decoded) != 1 || decoded[0] > math.MaxUint16 {
			return nil, stream.position, fmt.Errorf("KTF Java InputStreamReader decoded an invalid character")
		}
		characters = append(characters, uint16(decoded[0]))
		position += encodedSize
	}
	return characters, position, nil
}

func (r *ktfRuntime) handleByteArrayOutputStreamMethod(
	name, descriptor string,
) (uint32, error) {
	instance, err := r.parameter(1)
	if err != nil {
		return 0, err
	}
	switch name + descriptor {
	case "<init>()V", "<init>(I)V":
		r.outputStreams[instance] = nil
		return 0, nil
	case "write(I)V":
		value, valueErr := r.parameter(2)
		if valueErr != nil {
			return 0, valueErr
		}
		r.outputStreams[instance] = append(
			r.outputStreams[instance],
			byte(value),
		)
		return 0, nil
	case "write([B)V":
		array, valueErr := r.parameter(2)
		if valueErr != nil {
			return 0, valueErr
		}
		data, valueErr := r.readJavaByteArray(array)
		if valueErr != nil {
			return 0, valueErr
		}
		r.outputStreams[instance] = append(r.outputStreams[instance], data...)
		return 0, nil
	case "write([BII)V":
		array, valueErr := r.parameter(2)
		if valueErr != nil {
			return 0, valueErr
		}
		offset, valueErr := r.parameter(3)
		if valueErr != nil {
			return 0, valueErr
		}
		count, valueErr := r.parameter(4)
		if valueErr != nil {
			return 0, valueErr
		}
		data, valueErr := r.readJavaByteArrayRange(array, offset, count)
		if valueErr != nil {
			return 0, valueErr
		}
		r.outputStreams[instance] = append(r.outputStreams[instance], data...)
		return 0, nil
	case "toByteArray()[B":
		return r.newJavaByteArray(r.outputStreams[instance])
	case "size()I":
		return uint32(len(r.outputStreams[instance])), nil
	case "close()V":
		return 0, nil
	default:
		return 0, nil
	}
}

func (r *ktfRuntime) handleOutputStreamMethod(
	name, descriptor string,
) (uint32, error) {
	instance, err := r.parameter(1)
	if err != nil {
		return 0, err
	}
	target := instance
	if redirected := r.outputTargets[instance]; redirected != 0 {
		target = redirected
	}
	appendBytes := func(data []byte) error {
		r.outputStreams[target] = append(r.outputStreams[target], data...)
		if fileInstance := r.fileStreamTargets[target]; fileInstance != 0 {
			_, err := r.writeKTFFile(fileInstance, data)
			return err
		}
		return nil
	}
	switch name + descriptor {
	case "<init>()V":
		r.outputStreams[instance] = nil
		return 0, nil
	case "<init>(Ljava/io/OutputStream;)V":
		redirected, valueErr := r.parameter(2)
		if valueErr != nil {
			return 0, valueErr
		}
		if redirected == 0 {
			redirected = instance
		}
		r.outputTargets[instance] = redirected
		return 0, nil
	case "write(I)V", "writeByte(I)V":
		value, valueErr := r.parameter(2)
		if valueErr != nil {
			return 0, valueErr
		}
		return 0, appendBytes([]byte{byte(value)})
	case "writeBoolean(Z)V":
		value, valueErr := r.parameter(2)
		if valueErr != nil {
			return 0, valueErr
		}
		if value != 0 {
			value = 1
		}
		return 0, appendBytes([]byte{byte(value)})
	case "writeShort(I)V", "writeChar(I)V":
		value, valueErr := r.parameter(2)
		if valueErr != nil {
			return 0, valueErr
		}
		var encoded [2]byte
		binary.BigEndian.PutUint16(encoded[:], uint16(value))
		return 0, appendBytes(encoded[:])
	case "writeInt(I)V":
		value, valueErr := r.parameter(2)
		if valueErr != nil {
			return 0, valueErr
		}
		var encoded [4]byte
		binary.BigEndian.PutUint32(encoded[:], value)
		return 0, appendBytes(encoded[:])
	case "writeLong(J)V":
		low, valueErr := r.parameter(2)
		if valueErr != nil {
			return 0, valueErr
		}
		high, valueErr := r.parameter(3)
		if valueErr != nil {
			return 0, valueErr
		}
		var encoded [8]byte
		binary.BigEndian.PutUint64(encoded[:], uint64(high)<<32|uint64(low))
		return 0, appendBytes(encoded[:])
	case "write([B)V":
		array, valueErr := r.parameter(2)
		if valueErr != nil {
			return 0, valueErr
		}
		data, valueErr := r.readJavaByteArray(array)
		if valueErr != nil {
			return 0, valueErr
		}
		return 0, appendBytes(data)
	case "write([BII)V":
		array, valueErr := r.parameter(2)
		if valueErr != nil {
			return 0, valueErr
		}
		offset, valueErr := r.parameter(3)
		if valueErr != nil {
			return 0, valueErr
		}
		count, valueErr := r.parameter(4)
		if valueErr != nil {
			return 0, valueErr
		}
		data, valueErr := r.readJavaByteArrayRange(array, offset, count)
		if valueErr != nil {
			return 0, valueErr
		}
		return 0, appendBytes(data)
	case "flush()V", "close()V":
		return 0, nil
	default:
		return 0, nil
	}
}

func (r *ktfRuntime) handleIntegerMethod(
	name, descriptor string,
) (uint32, error) {
	switch name + descriptor {
	case "<init>(I)V":
		instance, err := r.parameter(1)
		if err != nil {
			return 0, err
		}
		value, err := r.parameter(2)
		if err != nil {
			return 0, err
		}
		r.integerValues[instance] = int32(value)
		return 0, nil
	case "byteValue()B":
		instance, err := r.parameter(1)
		if err != nil {
			return 0, err
		}
		return uint32(int32(int8(r.integerValues[instance]))), nil
	case "shortValue()S":
		instance, err := r.parameter(1)
		if err != nil {
			return 0, err
		}
		return uint32(int32(int16(r.integerValues[instance]))), nil
	case "intValue()I", "longValue()J":
		instance, err := r.parameter(1)
		if err != nil {
			return 0, err
		}
		if name == "longValue" {
			return r.javaLongResult(
				uint64(int64(r.integerValues[instance])),
			), nil
		}
		return uint32(r.integerValues[instance]), nil
	case "toString()Ljava/lang/String;":
		instance, err := r.parameter(1)
		if err != nil {
			return 0, err
		}
		return r.newJavaString(strconv.FormatInt(int64(r.integerValues[instance]), 10))
	case "parseInt(Ljava/lang/String;)I", "parseInt(Ljava/lang/String;I)I":
		text, err := r.parameter(1)
		if err != nil {
			return 0, err
		}
		radix := uint32(10)
		if descriptor == "(Ljava/lang/String;I)I" {
			radix, err = r.parameter(2)
			if err != nil {
				return 0, err
			}
		}
		if radix < 2 || radix > 36 {
			return 0, nil
		}
		value, parseErr := strconv.ParseInt(
			strings.TrimSpace(r.javaStringValue(text)),
			int(radix),
			32,
		)
		if parseErr != nil {
			return 0, nil
		}
		return uint32(int32(value)), nil
	case "toString(I)Ljava/lang/String;":
		value, err := r.parameter(1)
		if err != nil {
			return 0, err
		}
		return r.newJavaString(strconv.FormatInt(int64(int32(value)), 10))
	case "toString(II)Ljava/lang/String;":
		value, err := r.parameter(1)
		if err != nil {
			return 0, err
		}
		radix, err := r.parameter(2)
		if err != nil {
			return 0, err
		}
		if radix < 2 || radix > 36 {
			radix = 10
		}
		return r.newJavaString(strconv.FormatInt(
			int64(int32(value)),
			int(radix),
		))
	case "toHexString(I)Ljava/lang/String;",
		"toOctalString(I)Ljava/lang/String;",
		"toBinaryString(I)Ljava/lang/String;":
		value, err := r.parameter(1)
		if err != nil {
			return 0, err
		}
		radix := 16
		switch name {
		case "toOctalString":
			radix = 8
		case "toBinaryString":
			radix = 2
		}
		return r.newJavaString(strconv.FormatUint(uint64(value), radix))
	default:
		return 0, nil
	}
}

func (r *ktfRuntime) handleLongMethod(name, descriptor string) (uint32, error) {
	switch name + descriptor {
	case "<init>(J)V":
		instance, err := r.parameter(1)
		if err != nil {
			return 0, err
		}
		low, err := r.parameter(2)
		if err != nil {
			return 0, err
		}
		high, err := r.parameter(3)
		if err != nil {
			return 0, err
		}
		r.longValues[instance] = int64(uint64(high)<<32 | uint64(low))
		return 0, nil
	case "longValue()J":
		instance, err := r.parameter(1)
		if err != nil {
			return 0, err
		}
		return r.javaLongResult(uint64(r.longValues[instance])), nil
	case "toString()Ljava/lang/String;":
		instance, err := r.parameter(1)
		if err != nil {
			return 0, err
		}
		return r.newJavaString(strconv.FormatInt(r.longValues[instance], 10))
	case "parseLong(Ljava/lang/String;)J",
		"parseLong(Ljava/lang/String;I)J":
		text, err := r.parameter(1)
		if err != nil {
			return 0, err
		}
		radix := uint32(10)
		if descriptor == "(Ljava/lang/String;I)J" {
			radix, err = r.parameter(2)
			if err != nil {
				return 0, err
			}
		}
		if radix < 2 || radix > 36 {
			return r.javaLongResult(0), nil
		}
		value, parseErr := strconv.ParseInt(
			strings.TrimSpace(r.javaStringValue(text)),
			int(radix),
			64,
		)
		if parseErr != nil {
			return r.javaLongResult(0), nil
		}
		return r.javaLongResult(uint64(value)), nil
	case "toString(J)Ljava/lang/String;",
		"toString(JI)Ljava/lang/String;":
		low, err := r.parameter(1)
		if err != nil {
			return 0, err
		}
		high, err := r.parameter(2)
		if err != nil {
			return 0, err
		}
		radix := uint32(10)
		if descriptor == "(JI)Ljava/lang/String;" {
			radix, err = r.parameter(3)
			if err != nil {
				return 0, err
			}
			if radix < 2 || radix > 36 {
				radix = 10
			}
		}
		value := int64(uint64(high)<<32 | uint64(low))
		return r.newJavaString(strconv.FormatInt(value, int(radix)))
	default:
		return 0, nil
	}
}

func (r *ktfRuntime) handleThrowableMethod(
	name, descriptor string,
) (uint32, error) {
	instance, err := r.parameter(1)
	if err != nil {
		return 0, err
	}
	switch name + descriptor {
	case "<init>()V":
		delete(r.throwableMessages, instance)
		return 0, nil
	case "<init>(Ljava/lang/String;)V":
		message, valueErr := r.parameter(2)
		if valueErr != nil {
			return 0, valueErr
		}
		r.throwableMessages[instance] = message
		return 0, nil
	case "getMessage()Ljava/lang/String;":
		return r.throwableMessages[instance], nil
	case "printStackTrace()V":
		message := r.javaStringValue(r.throwableMessages[instance])
		r.tracef(
			"java_stack_trace:instance=0x%08x:message=%q",
			instance,
			message,
		)
		return 0, nil
	case "toString()Ljava/lang/String;":
		className := "java/lang/Throwable"
		if instance != 0 {
			if classAddress, readErr := r.readU32(instance + 4); readErr == nil {
				if class, inspectErr := r.inspectJavaClass(classAddress); inspectErr == nil {
					className = class.Name
				}
			}
		}
		text := strings.ReplaceAll(className, "/", ".")
		if message := r.javaStringValue(r.throwableMessages[instance]); message != "" {
			text += ": " + message
		}
		return r.newJavaString(text)
	default:
		return 0, nil
	}
}

func (r *ktfRuntime) handleByteMethod(name, descriptor string) (uint32, error) {
	switch name + descriptor {
	case "parseByte(Ljava/lang/String;)B", "parseByte(Ljava/lang/String;I)B":
		text, err := r.parameter(1)
		if err != nil {
			return 0, err
		}
		radix := uint32(10)
		if descriptor == "(Ljava/lang/String;I)B" {
			radix, err = r.parameter(2)
			if err != nil {
				return 0, err
			}
		}
		if radix < 2 || radix > 36 {
			return 0, nil
		}
		value, parseErr := strconv.ParseInt(
			strings.TrimSpace(r.javaStringValue(text)),
			int(radix),
			8,
		)
		if parseErr != nil {
			return 0, nil
		}
		return uint32(int32(int8(value))), nil
	default:
		return 0, nil
	}
}

func (r *ktfRuntime) handleMathMethod(name, descriptor string) (uint32, error) {
	left, err := r.signedParameter(1)
	if err != nil {
		return 0, err
	}
	switch name + descriptor {
	case "abs(I)I":
		if left < 0 {
			left = -left
		}
		return uint32(left), nil
	case "max(II)I", "min(II)I":
		right, valueErr := r.signedParameter(2)
		if valueErr != nil {
			return 0, valueErr
		}
		if name == "max" {
			if right > left {
				left = right
			}
		} else if right < left {
			left = right
		}
		return uint32(left), nil
	default:
		return 0, nil
	}
}

func (r *ktfRuntime) handleRandomMethod(
	name, descriptor string,
) (uint32, error) {
	instance, err := r.parameter(1)
	if err != nil {
		return 0, err
	}
	setSeed := func(seed uint64) {
		r.randomSeeds[instance] = shared.JavaRandomSeed(int64(seed))
	}
	next := func(bits uint8) (uint32, error) {
		state, ok := r.randomSeeds[instance]
		if !ok {
			state = shared.JavaRandomSeed(int64(instance))
		}
		value, err := shared.JavaRandomBits(&state, bits)
		if err == nil {
			r.randomSeeds[instance] = state
		}
		return value, err
	}
	switch name + descriptor {
	case "<init>()V":
		setSeed(uint64(instance))
		return 0, nil
	case "<init>(J)V", "setSeed(J)V":
		low, valueErr := r.parameter(2)
		if valueErr != nil {
			return 0, valueErr
		}
		high, valueErr := r.parameter(3)
		if valueErr != nil {
			return 0, valueErr
		}
		setSeed(uint64(high)<<32 | uint64(low))
		return 0, nil
	case "nextInt()I":
		return next(32)
	case "nextInt(I)I":
		bound, valueErr := r.parameter(2)
		if valueErr != nil {
			return 0, valueErr
		}
		if int32(bound) <= 0 {
			return 0, nil
		}
		value, valueErr := next(31)
		if valueErr != nil {
			return 0, valueErr
		}
		return uint32(uint64(value) * uint64(bound) >> 31), nil
	case "nextBoolean()Z":
		return next(1)
	default:
		return 0, nil
	}
}

func (r *ktfRuntime) handleDateMethod(name, descriptor string) (uint32, error) {
	instance, err := r.parameter(1)
	if err != nil {
		return 0, err
	}
	switch name + descriptor {
	case "<init>()V":
		r.dates[instance] = int64(r.tickMS)
		return 0, nil
	case "<init>(J)V", "setTime(J)V":
		low, valueErr := r.parameter(2)
		if valueErr != nil {
			return 0, valueErr
		}
		high, valueErr := r.parameter(3)
		if valueErr != nil {
			return 0, valueErr
		}
		r.dates[instance] = int64(uint64(high)<<32 | uint64(low))
		return 0, nil
	case "getTime()J":
		return r.javaLongResult(uint64(r.dates[instance])), nil
	default:
		return 0, nil
	}
}

func (r *ktfRuntime) handleVectorMethod(
	name, descriptor string,
) (uint32, error) {
	instance, err := r.parameter(1)
	if err != nil {
		return 0, err
	}
	values := r.vectors[instance]
	switch name + descriptor {
	case "<init>()V", "<init>(I)V", "<init>(II)V":
		r.vectors[instance] = nil
		return 0, nil
	case "addElement(Ljava/lang/Object;)V":
		value, valueErr := r.parameter(2)
		if valueErr != nil {
			return 0, valueErr
		}
		r.vectors[instance] = append(values, value)
		return 0, nil
	case "insertElementAt(Ljava/lang/Object;I)V":
		value, valueErr := r.parameter(2)
		if valueErr != nil {
			return 0, valueErr
		}
		index, valueErr := r.parameter(3)
		if valueErr != nil {
			return 0, valueErr
		}
		if index > uint32(len(values)) {
			return 0, nil
		}
		values = append(values, 0)
		copy(values[index+1:], values[index:])
		values[index] = value
		r.vectors[instance] = values
		return 0, nil
	case "push(Ljava/lang/Object;)Ljava/lang/Object;":
		value, valueErr := r.parameter(2)
		if valueErr != nil {
			return 0, valueErr
		}
		r.vectors[instance] = append(values, value)
		return value, nil
	case "elementAt(I)Ljava/lang/Object;":
		index, valueErr := r.parameter(2)
		if valueErr != nil {
			return 0, valueErr
		}
		if index >= uint32(len(values)) {
			return 0, nil
		}
		return values[index], nil
	case "setElementAt(Ljava/lang/Object;I)V":
		value, valueErr := r.parameter(2)
		if valueErr != nil {
			return 0, valueErr
		}
		index, valueErr := r.parameter(3)
		if valueErr != nil {
			return 0, valueErr
		}
		if index < uint32(len(values)) {
			values[index] = value
		}
		return 0, nil
	case "removeElementAt(I)V":
		index, valueErr := r.parameter(2)
		if valueErr != nil {
			return 0, valueErr
		}
		if index < uint32(len(values)) {
			r.vectors[instance] = append(values[:index:index], values[index+1:]...)
		}
		return 0, nil
	case "removeElement(Ljava/lang/Object;)Z":
		target, valueErr := r.parameter(2)
		if valueErr != nil {
			return 0, valueErr
		}
		for index, value := range values {
			if value == target {
				r.vectors[instance] = append(
					values[:index:index],
					values[index+1:]...,
				)
				return 1, nil
			}
		}
		return 0, nil
	case "removeAllElements()V":
		r.vectors[instance] = nil
		return 0, nil
	case "size()I", "capacity()I":
		return uint32(len(values)), nil
	case "isEmpty()Z", "empty()Z":
		if len(values) == 0 {
			return 1, nil
		}
		return 0, nil
	case "contains(Ljava/lang/Object;)Z":
		target, valueErr := r.parameter(2)
		if valueErr != nil {
			return 0, valueErr
		}
		for _, value := range values {
			if value == target {
				return 1, nil
			}
		}
		return 0, nil
	case "indexOf(Ljava/lang/Object;)I":
		target, valueErr := r.parameter(2)
		if valueErr != nil {
			return 0, valueErr
		}
		for index, value := range values {
			if value == target {
				return uint32(index), nil
			}
		}
		return ^uint32(0), nil
	case "copyInto([Ljava/lang/Object;)V":
		array, valueErr := r.parameter(2)
		if valueErr != nil {
			return 0, valueErr
		}
		if array == 0 {
			return 0, r.raiseHostJavaException("java/lang/NullPointerException")
		}
		length, valueErr := r.javaArrayLength(array)
		if valueErr != nil {
			return 0, valueErr
		}
		if uint32(len(values)) > length {
			return 0, r.raiseHostJavaException(
				"java/lang/ArrayIndexOutOfBoundsException",
			)
		}
		fields, valueErr := r.readU32(array)
		if valueErr != nil {
			return 0, valueErr
		}
		if len(values) != 0 {
			if valueErr = r.writeWords(fields+8, values); valueErr != nil {
				return 0, valueErr
			}
		}
		return 0, nil
	case "pop()Ljava/lang/Object;":
		if len(values) == 0 {
			return 0, nil
		}
		value := values[len(values)-1]
		r.vectors[instance] = values[:len(values)-1]
		return value, nil
	case "peek()Ljava/lang/Object;":
		if len(values) == 0 {
			return 0, nil
		}
		return values[len(values)-1], nil
	case "elements()Ljava/util/Enumeration;":
		return r.newJavaEnumeration(values)
	default:
		return 0, nil
	}
}

func (r *ktfRuntime) javaHashtableKey(instance uint32) string {
	if value, ok := r.javaStrings[instance]; ok {
		return "string:" + value
	}
	return fmt.Sprintf("object:%08x", instance)
}

func (r *ktfRuntime) handleHashtableMethod(
	name, descriptor string,
) (uint32, error) {
	instance, err := r.parameter(1)
	if err != nil {
		return 0, err
	}
	table := r.hashtables[instance]
	switch name + descriptor {
	case "<init>()V", "<init>(I)V":
		r.hashtables[instance] = make(map[string]ktfHashtableEntry)
		return 0, nil
	case "put(Ljava/lang/Object;Ljava/lang/Object;)Ljava/lang/Object;":
		key, valueErr := r.parameter(2)
		if valueErr != nil {
			return 0, valueErr
		}
		value, valueErr := r.parameter(3)
		if valueErr != nil {
			return 0, valueErr
		}
		if table == nil {
			table = make(map[string]ktfHashtableEntry)
			r.hashtables[instance] = table
		}
		normalized := r.javaHashtableKey(key)
		previous := table[normalized].value
		table[normalized] = ktfHashtableEntry{key: key, value: value}
		return previous, nil
	case "get(Ljava/lang/Object;)Ljava/lang/Object;",
		"remove(Ljava/lang/Object;)Ljava/lang/Object;",
		"containsKey(Ljava/lang/Object;)Z":
		key, valueErr := r.parameter(2)
		if valueErr != nil {
			return 0, valueErr
		}
		normalized := r.javaHashtableKey(key)
		entry, ok := table[normalized]
		if name == "containsKey" {
			if ok {
				return 1, nil
			}
			return 0, nil
		}
		if name == "remove" {
			delete(table, normalized)
		}
		return entry.value, nil
	case "contains(Ljava/lang/Object;)Z":
		target, valueErr := r.parameter(2)
		if valueErr != nil {
			return 0, valueErr
		}
		for _, entry := range table {
			if entry.value == target {
				return 1, nil
			}
		}
		return 0, nil
	case "size()I":
		return uint32(len(table)), nil
	case "isEmpty()Z":
		if len(table) == 0 {
			return 1, nil
		}
		return 0, nil
	case "clear()V":
		clear(table)
		return 0, nil
	case "keys()Ljava/util/Enumeration;",
		"elements()Ljava/util/Enumeration;":
		values := make([]uint32, 0, len(table))
		for _, entry := range table {
			value := entry.value
			if name == "keys" {
				value = entry.key
			}
			values = append(values, value)
		}
		return r.newJavaEnumeration(values)
	default:
		return 0, nil
	}
}

func (r *ktfRuntime) newJavaEnumeration(values []uint32) (uint32, error) {
	instance, err := r.newHostJavaObject("java/util/Enumeration")
	if err != nil {
		return 0, err
	}
	r.enumerations[instance] = &ktfEnumeration{
		values: append([]uint32(nil), values...),
	}
	return instance, nil
}

func (r *ktfRuntime) handleEnumerationMethod(
	name, descriptor string,
) (uint32, error) {
	instance, err := r.parameter(1)
	if err != nil {
		return 0, err
	}
	enumeration := r.enumerations[instance]
	switch name + descriptor {
	case "hasMoreElements()Z":
		if enumeration != nil && enumeration.index < uint32(len(enumeration.values)) {
			return 1, nil
		}
		return 0, nil
	case "nextElement()Ljava/lang/Object;":
		if enumeration == nil || enumeration.index >= uint32(len(enumeration.values)) {
			return 0, nil
		}
		value := enumeration.values[enumeration.index]
		enumeration.index++
		return value, nil
	default:
		return 0, nil
	}
}

func (r *ktfRuntime) handleTimerMethod(
	ctx context.Context,
	name, descriptor string,
) (uint32, error) {
	switch name + descriptor {
	case "<init>()V":
		return 0, nil
	case "cancel()V":
		timer, err := r.parameter(1)
		if err != nil {
			return 0, err
		}
		cancelled := 0
		for instance, task := range r.javaTimerTasks {
			if task == nil || task.timerOwner != timer {
				continue
			}
			task.done = true
			delete(r.javaTimerTasks, instance)
			r.javaTimerTaskStates[instance] = ktfJavaTimerCancelled
			cancelled++
		}
		r.tracef(
			"java_timer_cancel:timer=0x%08x:tasks=%d",
			timer,
			cancelled,
		)
		return 0, nil
	case "cancel()Z":
		instance, err := r.parameter(1)
		if err != nil {
			return 0, err
		}
		result := uint32(0)
		if r.javaTimerTaskStates[instance] == ktfJavaTimerScheduled {
			result = 1
		}
		if task := r.javaTimerTasks[instance]; task != nil {
			task.done = true
			delete(r.javaTimerTasks, instance)
		}
		r.javaTimerTaskStates[instance] = ktfJavaTimerCancelled
		r.tracef(
			"java_timer_task_cancel:task=0x%08x:scheduled=%t",
			instance,
			result != 0,
		)
		return result, nil
	case "schedule(Ljava/util/TimerTask;J)V",
		"schedule(Ljava/util/TimerTask;JJ)V",
		"scheduleAtFixedRate(Ljava/util/TimerTask;JJ)V":
		timer, err := r.parameter(1)
		if err != nil {
			return 0, err
		}
		task, err := r.parameter(2)
		if err != nil {
			return 0, err
		}
		if task == 0 {
			return r.raiseJavaException("java/lang/NullPointerException", 0)
		}
		delay, err := r.javaTimerLongParameter(3)
		if err != nil {
			return 0, err
		}
		period := int64(0)
		if descriptor != "(Ljava/util/TimerTask;J)V" {
			period, err = r.javaTimerLongParameter(5)
			if err != nil {
				return 0, err
			}
		}
		if delay < 0 || period < 0 ||
			descriptor != "(Ljava/util/TimerTask;J)V" && period == 0 {
			return r.raiseJavaException("java/lang/IllegalArgumentException", 0)
		}
		if r.javaTimerTaskStates[task] != 0 {
			return r.raiseJavaException("java/lang/IllegalStateException", 0)
		}
		if !r.deferThreads {
			return r.invokeJavaVirtual(ctx, task, "run", "()V")
		}
		queued, err := r.queueJavaVirtualTask(task, "run", "()V")
		if err != nil {
			return 0, err
		}
		deadline := r.javaTimerDeadline(uint64(delay))
		queued.timerTask = task
		queued.timerOwner = timer
		queued.timerPeriodMS = uint64(period)
		queued.timerDeadlineMS = deadline
		queued.timerFixedRate = name == "scheduleAtFixedRate"
		if delay != 0 {
			queued.wakeAtMS = deadline
		}
		r.javaTimerTasks[task] = queued
		r.javaTimerTaskStates[task] = ktfJavaTimerScheduled
		r.tracef(
			"java_timer_schedule:timer=0x%08x:task=0x%08x:"+
				"delay_ms=%d:period_ms=%d:fixed_rate=%t",
			timer,
			task,
			delay,
			period,
			queued.timerFixedRate,
		)
		return 0, nil
	default:
		return 0, nil
	}
}

func (r *ktfRuntime) javaTimerLongParameter(index uint32) (int64, error) {
	low, err := r.parameter(index)
	if err != nil {
		return 0, err
	}
	high, err := r.parameter(index + 1)
	if err != nil {
		return 0, err
	}
	return int64(uint64(high)<<32 | uint64(low)), nil
}

func (r *ktfRuntime) javaTimerDeadline(delay uint64) uint64 {
	if delay > ^uint64(0)-r.tickMS {
		return ^uint64(0)
	}
	return r.tickMS + delay
}

func (r *ktfRuntime) beginJavaTimerTask(task *ktfTask) {
	if task == nil || task.timerTask == 0 || task.timerPeriodMS != 0 {
		return
	}
	if r.javaTimerTasks[task.timerTask] == task &&
		r.javaTimerTaskStates[task.timerTask] == ktfJavaTimerScheduled {
		delete(r.javaTimerTasks, task.timerTask)
		r.javaTimerTaskStates[task.timerTask] = ktfJavaTimerExecuted
	}
}

func (r *ktfRuntime) completeJavaTimerTask(task *ktfTask) error {
	if task == nil || task.timerTask == 0 {
		return nil
	}
	instance := task.timerTask
	if task.timerPeriodMS == 0 {
		if r.javaTimerTasks[instance] == task {
			delete(r.javaTimerTasks, instance)
			r.javaTimerTaskStates[instance] = ktfJavaTimerExecuted
		}
		return nil
	}
	if r.javaTimerTasks[instance] != task ||
		r.javaTimerTaskStates[instance] != ktfJavaTimerScheduled {
		return nil
	}
	deadline := r.javaTimerDeadline(task.timerPeriodMS)
	if task.timerFixedRate {
		deadline = task.timerDeadlineMS
		if task.timerPeriodMS > ^uint64(0)-deadline {
			deadline = ^uint64(0)
		} else {
			deadline += task.timerPeriodMS
		}
	}
	queued, err := r.queueJavaVirtualTask(instance, "run", "()V")
	if err != nil {
		return fmt.Errorf("reschedule KTF Java TimerTask: %w", err)
	}
	queued.wakeAtMS = deadline
	queued.timerTask = instance
	queued.timerOwner = task.timerOwner
	queued.timerPeriodMS = task.timerPeriodMS
	queued.timerDeadlineMS = deadline
	queued.timerFixedRate = task.timerFixedRate
	r.javaTimerTasks[instance] = queued
	r.tracef(
		"java_timer_reschedule:timer=0x%08x:task=0x%08x:wake_at_ms=%d",
		queued.timerOwner,
		instance,
		deadline,
	)
	return nil
}

func (r *ktfRuntime) handleMediaMethod(
	name, descriptor string,
) (uint32, error) {
	switch name + descriptor {
	case "<init>(Ljava/lang/String;)V",
		"<init>(Ljava/lang/String;I)V",
		"<init>(Ljava/lang/String;Ljava/lang/String;)V",
		"<init>(Ljava/lang/String;[B)V":
		instance, err := r.parameter(1)
		if err != nil {
			return 0, err
		}
		clip := &ktfClip{volume: 5}
		if descriptor == "(Ljava/lang/String;[B)V" {
			array, valueErr := r.parameter(3)
			if valueErr != nil {
				return 0, valueErr
			}
			if array != 0 {
				clip.data, valueErr = r.readJavaByteArray(array)
				if valueErr != nil {
					return 0, valueErr
				}
			}
		} else {
			resource, found, valueErr := r.ktfClipConstructorResource(
				descriptor,
			)
			if valueErr != nil {
				return 0, valueErr
			}
			if found {
				clip.data = resource
			}
		}
		r.clips[instance] = clip
		return 0, r.syncKTFClip(instance)
	case "availableDataSize()I":
		instance, err := r.parameter(1)
		if err != nil {
			return 0, err
		}
		return uint32(len(r.ensureKTFClip(instance).data)), nil
	case "clearData()V":
		instance, err := r.parameter(1)
		if err != nil {
			return 0, err
		}
		r.ensureKTFClip(instance).data = nil
		return 0, r.syncKTFClip(instance)
	case "putData([BII)I":
		instance, err := r.parameter(1)
		if err != nil {
			return 0, err
		}
		array, err := r.parameter(2)
		if err != nil {
			return 0, err
		}
		offset, err := r.parameter(3)
		if err != nil {
			return 0, err
		}
		count, err := r.parameter(4)
		if err != nil {
			return 0, err
		}
		data, err := r.readJavaByteArrayRange(array, offset, count)
		if err != nil {
			return 0, err
		}
		clip := r.ensureKTFClip(instance)
		clip.data = append(clip.data, data...)
		return count, r.syncKTFClip(instance)
	case "getData([BII)I":
		instance, err := r.parameter(1)
		if err != nil {
			return 0, err
		}
		array, err := r.parameter(2)
		if err != nil {
			return 0, err
		}
		offset, err := r.parameter(3)
		if err != nil {
			return 0, err
		}
		count, err := r.parameter(4)
		if err != nil {
			return 0, err
		}
		clip := r.ensureKTFClip(instance)
		if count > uint32(len(clip.data)) {
			count = uint32(len(clip.data))
		}
		if err := r.writeJavaByteArrayRange(
			array,
			offset,
			clip.data[:count],
		); err != nil {
			return 0, err
		}
		clip.data = append(clip.data[:0], clip.data[count:]...)
		return count, r.syncKTFClip(instance)
	case "setBuffer([BI)Z":
		instance, err := r.parameter(1)
		if err != nil {
			return 0, err
		}
		array, err := r.parameter(2)
		if err != nil {
			return 0, err
		}
		count, err := r.parameter(3)
		if err != nil {
			return 0, err
		}
		data, err := r.readJavaByteArrayRange(array, 0, count)
		if err != nil {
			return 0, err
		}
		r.ensureKTFClip(instance).data = data
		return 1, r.syncKTFClip(instance)
	case "setVolume(I)Z":
		instance, err := r.parameter(1)
		if err != nil {
			return 0, err
		}
		volume, err := r.signedParameter(2)
		if err != nil {
			return 0, err
		}
		clip := r.clips[instance]
		if clip == nil {
			clip = &ktfClip{}
			r.clips[instance] = clip
		}
		clip.volume = int32(volume)
		return 1, r.syncKTFClipGain(instance)
	case "getVolume()I":
		instance, err := r.parameter(1)
		if err != nil {
			return 0, err
		}
		if clip := r.clips[instance]; clip != nil {
			return uint32(clip.volume), nil
		}
		return 0, nil
	case "setListener(Lorg/kwis/msp/media/PlayListener;)V":
		instance, err := r.parameter(1)
		if err != nil {
			return 0, err
		}
		listener, err := r.parameter(2)
		if err != nil {
			return 0, err
		}
		clip := r.clips[instance]
		if clip == nil {
			clip = &ktfClip{}
			r.clips[instance] = clip
		}
		clip.listener = listener
		return 0, nil
	case "play(Lorg/kwis/msp/media/Clip;Z)Z",
		"stop(Lorg/kwis/msp/media/Clip;)Z":
		instance, err := r.parameter(1)
		if err != nil {
			return 0, err
		}
		clip := r.clips[instance]
		if clip == nil {
			clip = &ktfClip{}
			r.clips[instance] = clip
		}
		clip.playing = name == "play"
		serviceID, serviceErr := r.ensureKTFClipService(instance)
		if serviceErr != nil {
			return 0, serviceErr
		}
		if clip.playing {
			plays := int32(1)
			if repeat, valueErr := r.parameter(2); valueErr == nil && repeat != 0 {
				plays = -1
			}
			serviceErr = r.services.Media.Play(
				r.serviceOwner,
				serviceID,
				plays,
			)
		} else {
			serviceErr = r.services.Media.Stop(r.serviceOwner, serviceID)
		}
		return 1, serviceErr
	default:
		return 0, nil
	}
}

func (r *ktfRuntime) ktfClipConstructorResource(
	descriptor string,
) ([]byte, bool, error) {
	stringParameters := []uint32{2}
	if descriptor == "(Ljava/lang/String;Ljava/lang/String;)V" {
		stringParameters = append(stringParameters, 3)
	}
	for _, parameter := range stringParameters {
		address, err := r.parameter(parameter)
		if err != nil {
			return nil, false, err
		}
		name := strings.TrimPrefix(
			strings.ReplaceAll(r.javaStringValue(address), `\`, "/"),
			"/",
		)
		name = path.Clean(name)
		if name == "." || name == ".." || strings.HasPrefix(name, "../") {
			continue
		}
		if data, ok := r.findKTFResource(name); ok {
			return append([]byte(nil), data...), true, nil
		}
	}
	return nil, false, nil
}

func (r *ktfRuntime) ensureKTFClip(instance uint32) *ktfClip {
	clip := r.clips[instance]
	if clip == nil {
		clip = &ktfClip{volume: 5}
		r.clips[instance] = clip
	}
	return clip
}

func (r *ktfRuntime) ensureKTFClipService(
	instance uint32,
) (shared.ServiceID, error) {
	if serviceID := r.clipServices[instance]; serviceID != 0 {
		return serviceID, nil
	}
	serviceID, err := r.services.Media.CreateClip(
		r.serviceOwner,
		"",
		0,
	)
	if errors.Is(err, shared.ErrLimitExceeded) && r.recycleKTFClipService() {
		serviceID, err = r.services.Media.CreateClip(
			r.serviceOwner,
			"",
			0,
		)
	}
	if err != nil {
		return 0, err
	}
	r.clipServices[instance] = serviceID
	return serviceID, nil
}

// recycleKTFClipService frees the host media service backing the oldest Java
// clip and reports whether it freed one. The KTF runtime has no Java
// collector, so a title that constructs a Clip per sound effect would
// otherwise exhaust the bounded media pool and fault. Instances are numbered
// in allocation order, so the lowest handle is the oldest clip and the choice
// stays deterministic.
//
// Idle clips are retired first. When every clip is playing the oldest one is
// stopped and taken anyway, which is what a handset mixer does when a title
// asks for more simultaneous voices than the device has. The Java-side sample
// data lives in ktfClip, so a recycled clip reallocates and refills its
// service the next time the guest touches it.
func (r *ktfRuntime) recycleKTFClipService() bool {
	idle, playing := uint32(0), uint32(0)
	for instance, serviceID := range r.clipServices {
		if serviceID == 0 {
			continue
		}
		if clip := r.clips[instance]; clip != nil && clip.playing {
			if playing == 0 || instance < playing {
				playing = instance
			}
			continue
		}
		if idle == 0 || instance < idle {
			idle = instance
		}
	}
	victim := idle
	if victim == 0 {
		victim = playing
	}
	if victim == 0 {
		return false
	}
	if err := r.services.Media.DestroyClip(
		r.serviceOwner,
		r.clipServices[victim],
		r.services.Events,
	); err != nil {
		return false
	}
	if clip := r.clips[victim]; clip != nil {
		clip.playing = false
	}
	delete(r.clipServices, victim)
	return true
}

func (r *ktfRuntime) syncKTFClip(instance uint32) error {
	clip := r.ensureKTFClip(instance)
	serviceID, err := r.ensureKTFClipService(instance)
	if err != nil {
		return err
	}
	if err := r.services.Media.Clear(r.serviceOwner, serviceID); err != nil {
		return err
	}
	if _, err := r.services.Media.Append(
		r.serviceOwner,
		serviceID,
		clip.data,
	); err != nil {
		return err
	}
	return r.syncKTFClipGain(instance)
}

func (r *ktfRuntime) syncKTFClipGain(instance uint32) error {
	clip := r.ensureKTFClip(instance)
	serviceID, err := r.ensureKTFClipService(instance)
	if err != nil {
		return err
	}
	volume := max(int32(0), min(int32(100), clip.volume*20))
	return r.services.Media.SetClipGain(
		r.serviceOwner,
		serviceID,
		uint8(volume),
		false,
		0,
	)
}

func (r *ktfRuntime) handleCalendarMethod(
	name, descriptor string,
) (uint32, error) {
	instance, err := r.parameter(1)
	if err != nil {
		return 0, err
	}
	switch name + descriptor {
	case "getInstance()Ljava/util/Calendar;":
		calendar, valueErr := r.newHostJavaObject("java/util/Calendar")
		if valueErr == nil {
			r.dates[calendar] = int64(r.tickMS)
		}
		return calendar, valueErr
	case "get(I)I":
		return 0, nil
	case "set(II)V":
		return 0, nil
	case "setTime(Ljava/util/Date;)V":
		date, valueErr := r.parameter(2)
		if valueErr != nil {
			return 0, valueErr
		}
		r.dates[instance] = r.dates[date]
		return 0, nil
	case "getTime()Ljava/util/Date;":
		date, valueErr := r.newHostJavaObject("java/util/Date")
		if valueErr != nil {
			return 0, valueErr
		}
		r.dates[date] = r.dates[instance]
		return date, nil
	case "setTimeInMillis(J)V":
		low, valueErr := r.parameter(2)
		if valueErr != nil {
			return 0, valueErr
		}
		high, valueErr := r.parameter(3)
		if valueErr != nil {
			return 0, valueErr
		}
		r.dates[instance] = int64(uint64(high)<<32 | uint64(low))
		return 0, nil
	case "getTimeInMillis()J":
		return r.javaLongResult(uint64(r.dates[instance])), nil
	default:
		return 0, nil
	}
}

func (r *ktfRuntime) handleFileMethod(
	name, descriptor string,
) (uint32, error) {
	switch name + descriptor {
	case "<init>(Ljava/lang/String;I)V",
		"<init>(Ljava/lang/String;II)V",
		"<init>(Ljava/lang/String;)V":
		instance, err := r.parameter(1)
		if err != nil {
			return 0, err
		}
		nameAddress, err := r.parameter(2)
		if err != nil {
			return 0, err
		}
		mode := uint32(1)
		namespace := shared.NamespacePrivate
		if descriptor != "(Ljava/lang/String;)V" {
			mode, err = r.parameter(3)
			if err != nil {
				return 0, err
			}
		}
		if descriptor == "(Ljava/lang/String;II)V" {
			flag, flagErr := r.parameter(4)
			if flagErr != nil {
				return 0, flagErr
			}
			namespace, err = r.ktfStorageNamespace(flag)
			if err != nil {
				return 0, err
			}
		}
		filename := normalizeKTFFileName(r.javaStringValue(nameAddress))
		file := &ktfFile{namespace: namespace, name: filename, mode: mode}
		legacyData, legacyExists := r.fileData[filename]
		if namespace == shared.NamespacePrivate && legacyExists {
			if _, statErr := r.services.Storage.Stat(
				namespace,
				filename,
			); statErr != nil {
				if writeErr := r.services.Storage.WriteFile(
					namespace,
					filename,
					legacyData,
				); writeErr != nil {
					return 0, writeErr
				}
			}
		}
		_, statErr := r.services.Storage.Stat(namespace, filename)
		exists := statErr == nil
		openMode := shared.OpenMode(0)
		switch mode {
		case ktfFileReadOnly:
			openMode = shared.OpenRead
			if !exists {
				r.tracef("java_file_open_missing:%s", filename)
				return 0, r.raiseHostJavaException("java/io/IOException")
			}
		case ktfFileWrite:
			openMode = shared.OpenRead | shared.OpenWrite |
				shared.OpenCreate | shared.OpenAppend
		case ktfFileWriteTrunc:
			openMode = shared.OpenRead | shared.OpenWrite |
				shared.OpenCreate | shared.OpenTruncate
		case ktfFileReadWrite:
			openMode = shared.OpenRead | shared.OpenWrite | shared.OpenCreate
		default:
			r.tracef(
				"java_file_open_invalid_mode:%s:mode=%d",
				filename,
				mode,
			)
			return 0, r.raiseHostJavaException("java/io/IOException")
		}
		serviceID, serviceErr := r.services.Storage.Open(
			r.serviceOwner,
			namespace,
			filename,
			openMode,
		)
		if serviceErr != nil {
			return 0, r.raiseHostJavaException("java/io/IOException")
		}
		data, serviceErr := r.services.Storage.ReadFile(
			namespace,
			filename,
		)
		if serviceErr != nil {
			_ = r.services.Storage.Close(r.serviceOwner, serviceID)
			return 0, serviceErr
		}
		if namespace == shared.NamespacePrivate {
			r.fileData[filename] = data
		}
		if mode == ktfFileWrite {
			file.position = uint32(len(data))
		}
		r.files[instance] = file
		r.fileServices[instance] = serviceID
		r.tracef("java_file_open:%s:mode=%d", filename, mode)
		return 0, nil
	case "close()V":
		instance, err := r.parameter(1)
		if err != nil {
			return 0, err
		}
		if file := r.files[instance]; file != nil {
			if serviceID := r.fileServices[instance]; serviceID != 0 {
				if err := r.services.Storage.Close(
					r.serviceOwner,
					serviceID,
				); err != nil {
					return 0, err
				}
				delete(r.fileServices, instance)
			}
			file.closed = true
		}
		return 0, nil
	case "openInputStream()Ljava/io/InputStream;",
		"openDataInputStream()Ljava/io/DataInputStream;":
		fileInstance, err := r.parameter(1)
		if err != nil {
			return 0, err
		}
		className := "java/io/InputStream"
		if strings.HasPrefix(name, "openData") {
			className = "java/io/DataInputStream"
		}
		instance, err := r.newHostJavaObject(className)
		if err != nil {
			return 0, err
		}
		var data []byte
		if file := r.files[fileInstance]; file != nil {
			data, err = r.services.Storage.ReadFile(
				ktfFileNamespace(file),
				file.name,
			)
			if err != nil {
				return 0, err
			}
		}
		r.inputStreams[instance] = &ktfInputStream{data: data}
		return instance, nil
	case "openOutputStream()Ljava/io/OutputStream;",
		"openDataOutputStream()Ljava/io/DataOutputStream;":
		fileInstance, err := r.parameter(1)
		if err != nil {
			return 0, err
		}
		className := "java/io/OutputStream"
		if strings.HasPrefix(name, "openData") {
			className = "java/io/DataOutputStream"
		}
		instance, err := r.newHostJavaObject(className)
		if err != nil {
			return 0, err
		}
		r.outputStreams[instance] = nil
		r.fileStreamTargets[instance] = fileInstance
		return instance, nil
	case "sizeOf()I":
		instance, err := r.parameter(1)
		if err != nil {
			return 0, err
		}
		if file := r.files[instance]; file != nil {
			info, err := r.services.Storage.Stat(
				ktfFileNamespace(file),
				file.name,
			)
			if err != nil {
				return 0, err
			}
			return uint32(min(info.Size, uint64(^uint32(0)))), nil
		}
		return 0, nil
	case "seek(I)V":
		instance, err := r.parameter(1)
		if err != nil {
			return 0, err
		}
		position, err := r.parameter(2)
		if err != nil {
			return 0, err
		}
		if file := r.files[instance]; file != nil {
			serviceID, serviceErr := r.ensureKTFFileService(instance)
			if serviceErr != nil {
				return 0, serviceErr
			}
			if _, serviceErr := r.services.Storage.Seek(
				r.serviceOwner,
				serviceID,
				int64(position),
				shared.SeekStart,
			); serviceErr != nil {
				return 0, serviceErr
			}
			file.position = position
		}
		return 0, nil
	case "tell()I":
		instance, err := r.parameter(1)
		if err != nil {
			return 0, err
		}
		if file := r.files[instance]; file != nil {
			return file.position, nil
		}
		return 0, nil
	case "read()I":
		instance, err := r.parameter(1)
		if err != nil {
			return 0, err
		}
		data, err := r.readKTFFileBytes(instance, 1)
		if err != nil {
			return 0, err
		}
		if len(data) == 0 {
			return ^uint32(0), nil
		}
		return uint32(data[0]), nil
	case "read([B)I", "read([BII)I":
		instance, err := r.parameter(1)
		if err != nil {
			return 0, err
		}
		array, err := r.parameter(2)
		if err != nil {
			return 0, err
		}
		offset := uint32(0)
		count, err := r.javaArrayLength(array)
		if err != nil {
			return 0, err
		}
		if descriptor == "([BII)I" {
			offset, err = r.parameter(3)
			if err != nil {
				return 0, err
			}
			count, err = r.parameter(4)
			if err != nil {
				return 0, err
			}
		}
		return r.readKTFFile(instance, array, offset, count)
	case "write(I)I", "write(I)V":
		instance, err := r.parameter(1)
		if err != nil {
			return 0, err
		}
		value, err := r.parameter(2)
		if err != nil {
			return 0, err
		}
		return r.writeKTFFile(instance, []byte{byte(value)})
	case "write([B)I", "write([BII)I",
		"write([B)V", "write([BII)V":
		instance, err := r.parameter(1)
		if err != nil {
			return 0, err
		}
		array, err := r.parameter(2)
		if err != nil {
			return 0, err
		}
		offset := uint32(0)
		count, err := r.javaArrayLength(array)
		if err != nil {
			return 0, err
		}
		if strings.Contains(descriptor, "BII") {
			offset, err = r.parameter(3)
			if err != nil {
				return 0, err
			}
			count, err = r.parameter(4)
			if err != nil {
				return 0, err
			}
		}
		data, err := r.readJavaByteArrayRange(array, offset, count)
		if err != nil {
			return 0, err
		}
		return r.writeKTFFile(instance, data)
	default:
		r.recordUnimplementedJava("org/kwis/msp/io/File", name, descriptor)
		return 0, nil
	}
}

func normalizeKTFFileName(filename string) string {
	filename = strings.ReplaceAll(strings.TrimSpace(filename), `\`, "/")
	if filename == "" {
		return "/"
	}
	if !strings.HasPrefix(filename, "/") {
		filename = "/" + filename
	}
	return path.Clean(filename)
}

func (r *ktfRuntime) readKTFFile(
	instance, array, offset, count uint32,
) (uint32, error) {
	length, err := r.javaArrayLength(array)
	if err != nil {
		return 0, err
	}
	if offset > length || count > length-offset {
		return 0, fmt.Errorf(
			"KTF File.read range [%d,%d) exceeds byte array length %d",
			offset,
			uint64(offset)+uint64(count),
			length,
		)
	}
	if count == 0 {
		return 0, nil
	}
	data, err := r.readKTFFileBytes(instance, count)
	if err != nil {
		return 0, err
	}
	if len(data) == 0 {
		return ^uint32(0), nil
	}
	count = uint32(len(data))
	fields, err := r.readU32(array)
	if err != nil {
		return 0, err
	}
	if err := r.cpu.WriteMemory(
		fields+8+offset,
		data,
	); err != nil {
		return 0, err
	}
	return count, nil
}

func (r *ktfRuntime) readKTFFileBytes(
	instance, count uint32,
) ([]byte, error) {
	file := r.files[instance]
	if file == nil || count == 0 {
		return nil, nil
	}
	serviceID, err := r.ensureKTFFileService(instance)
	if err != nil {
		return nil, err
	}
	data, err := r.services.Storage.Read(
		r.serviceOwner,
		serviceID,
		uint64(count),
	)
	if err != nil {
		return nil, err
	}
	file.position += uint32(len(data))
	r.tracef("java_file_read:%s:%d", file.name, len(data))
	return data, nil
}

func (r *ktfRuntime) writeKTFFile(
	instance uint32,
	data []byte,
) (uint32, error) {
	file := r.files[instance]
	if file == nil {
		file = &ktfFile{
			namespace: shared.NamespacePrivate,
			name:      fmt.Sprintf("/unnamed-%08x", instance),
		}
		r.files[instance] = file
	}
	end := uint64(file.position) + uint64(len(data))
	if end > uint64(^uint32(0)) {
		return 0, errors.New("KTF File.write range overflows uint32")
	}
	serviceID, err := r.ensureKTFFileService(instance)
	if err != nil {
		return 0, err
	}
	written, err := r.services.Storage.Write(
		r.serviceOwner,
		serviceID,
		data,
	)
	if err != nil {
		return 0, err
	}
	if written != len(data) {
		return 0, fmt.Errorf(
			"shared KTF file wrote %d bytes, want %d",
			written,
			len(data),
		)
	}
	stored, err := r.services.Storage.ReadFile(
		ktfFileNamespace(file),
		file.name,
	)
	if err != nil {
		return 0, err
	}
	if ktfFileNamespace(file) == shared.NamespacePrivate {
		r.fileData[file.name] = stored
	}
	file.position = uint32(end)
	r.tracef("java_file_write:%s:%d", file.name, len(data))
	return uint32(len(data)), nil
}

func (r *ktfRuntime) ensureKTFFileService(
	instance uint32,
) (shared.ServiceID, error) {
	if serviceID := r.fileServices[instance]; serviceID != 0 {
		return serviceID, nil
	}
	file := r.files[instance]
	if file == nil {
		return 0, fmt.Errorf("KTF file object 0x%08x is missing", instance)
	}
	namespace := ktfFileNamespace(file)
	if _, err := r.services.Storage.Stat(
		namespace,
		file.name,
	); err != nil {
		if data, ok := r.fileData[file.name]; namespace == shared.NamespacePrivate && ok {
			if err := r.services.Storage.WriteFile(
				namespace,
				file.name,
				data,
			); err != nil {
				return 0, err
			}
		}
	}
	mode := shared.OpenRead
	if file.mode != ktfFileReadOnly {
		mode |= shared.OpenWrite | shared.OpenCreate
	}
	serviceID, err := r.services.Storage.Open(
		r.serviceOwner,
		namespace,
		file.name,
		mode,
	)
	if err != nil {
		return 0, err
	}
	if _, err := r.services.Storage.Seek(
		r.serviceOwner,
		serviceID,
		int64(file.position),
		shared.SeekStart,
	); err != nil {
		_ = r.services.Storage.Close(r.serviceOwner, serviceID)
		return 0, err
	}
	r.fileServices[instance] = serviceID
	return serviceID, nil
}

func ktfFileNamespace(file *ktfFile) shared.Namespace {
	if file != nil && file.namespace.Valid() {
		return file.namespace
	}
	return shared.NamespacePrivate
}

func (r *ktfRuntime) ktfStorageNamespace(
	flag uint32,
) (shared.Namespace, error) {
	switch flag {
	case 1:
		return shared.NamespacePrivate, nil
	case 2:
		return shared.NamespaceShared, nil
	case 3:
		return shared.NamespacePackage, nil
	default:
		return 0, r.raiseHostJavaException("java/lang/SecurityException")
	}
}

func (r *ktfRuntime) handleFileSystemMethod(
	name, descriptor string,
) (uint32, error) {
	switch name + descriptor {
	case "<init>()V":
		return 0, nil
	case "getMaxFilenameLength()I":
		return uint32(min(
			r.services.Config.Limits.Storage.MaxPathBytes,
			uint32(math.MaxInt32),
		)), nil
	case "list(Ljava/lang/String;)Ljava/util/Vector;",
		"list(Ljava/lang/String;I)Ljava/util/Vector;":
		nameAddress, err := r.parameter(1)
		if err != nil {
			return 0, err
		}
		namespace, err := r.ktfFileSystemNamespace(descriptor, 2)
		if err != nil {
			return 0, err
		}
		directory := normalizeKTFFileName(r.javaStringValue(nameAddress))
		entries, err := r.services.Storage.List(namespace, directory)
		if err != nil {
			return 0, r.raiseHostJavaException("java/io/IOException")
		}
		values := make([]uint32, 0, len(entries))
		for _, entry := range entries {
			value, valueErr := r.newJavaString(entry)
			if valueErr != nil {
				return 0, valueErr
			}
			values = append(values, value)
		}
		vector, err := r.newHostJavaObject("java/util/Vector")
		if err != nil {
			return 0, err
		}
		r.vectors[vector] = values
		return vector, nil
	case "exists(Ljava/lang/String;)Z",
		"exists(Ljava/lang/String;I)Z":
		nameAddress, err := r.parameter(1)
		if err != nil {
			return 0, err
		}
		namespace, err := r.ktfFileSystemNamespace(descriptor, 2)
		if err != nil {
			return 0, err
		}
		filename := normalizeKTFFileName(r.javaStringValue(nameAddress))
		if _, err := r.services.Storage.Stat(
			namespace,
			filename,
		); err == nil {
			return 1, nil
		}
		return boolWord(r.services.Storage.DirectoryExists(namespace, filename)), nil
	case "isFile(Ljava/lang/String;)Z",
		"isFile(Ljava/lang/String;I)Z":
		nameAddress, err := r.parameter(1)
		if err != nil {
			return 0, err
		}
		namespace, err := r.ktfFileSystemNamespace(descriptor, 2)
		if err != nil {
			return 0, err
		}
		filename := normalizeKTFFileName(r.javaStringValue(nameAddress))
		if _, err := r.services.Storage.Stat(namespace, filename); err == nil {
			return 1, nil
		}
		return 0, nil
	case "isDirectory(Ljava/lang/String;)Z",
		"isDirectory(Ljava/lang/String;I)Z":
		nameAddress, err := r.parameter(1)
		if err != nil {
			return 0, err
		}
		namespace, err := r.ktfFileSystemNamespace(descriptor, 2)
		if err != nil {
			return 0, err
		}
		filename := normalizeKTFFileName(r.javaStringValue(nameAddress))
		return boolWord(
			r.services.Storage.DirectoryExists(namespace, filename),
		), nil
	case "mkdir(Ljava/lang/String;)V",
		"mkdir(Ljava/lang/String;I)V":
		nameAddress, err := r.parameter(1)
		if err != nil {
			return 0, err
		}
		namespace, err := r.ktfFileSystemNamespace(descriptor, 2)
		if err != nil {
			return 0, err
		}
		directory := normalizeKTFFileName(r.javaStringValue(nameAddress))
		if err := r.services.Storage.MakeDirectory(namespace, directory); err != nil {
			return 0, r.raiseHostJavaException("java/io/IOException")
		}
		r.tracef("java_directory_make:%s:%s", namespace, directory)
		return 0, nil
	case "rmdir(Ljava/lang/String;)V",
		"rmdir(Ljava/lang/String;I)V":
		nameAddress, err := r.parameter(1)
		if err != nil {
			return 0, err
		}
		namespace, err := r.ktfFileSystemNamespace(descriptor, 2)
		if err != nil {
			return 0, err
		}
		directory := normalizeKTFFileName(r.javaStringValue(nameAddress))
		if err := r.services.Storage.RemoveDirectory(namespace, directory); err != nil {
			return 0, r.raiseHostJavaException("java/io/IOException")
		}
		r.tracef("java_directory_remove:%s:%s", namespace, directory)
		return 0, nil
	case "remove(Ljava/lang/String;)V",
		"remove(Ljava/lang/String;I)V":
		nameAddress, err := r.parameter(1)
		if err != nil {
			return 0, err
		}
		namespace, err := r.ktfFileSystemNamespace(descriptor, 2)
		if err != nil {
			return 0, err
		}
		filename := normalizeKTFFileName(r.javaStringValue(nameAddress))
		// KTF applications commonly retain the File object after closing its
		// derived stream and then remove the path through FileSystem. The
		// handset invalidates that File object as part of the removal. Close
		// the adapter-owned handles first so shared Storage can keep its strict
		// no-delete-while-open invariant.
		var instances []uint32
		for instance, file := range r.files {
			if file != nil && ktfFileNamespace(file) == namespace &&
				file.name == filename && !file.closed {
				instances = append(instances, instance)
			}
		}
		slices.Sort(instances)
		for _, instance := range instances {
			if serviceID := r.fileServices[instance]; serviceID != 0 {
				if err := r.services.Storage.Close(
					r.serviceOwner,
					serviceID,
				); err != nil {
					return 0, err
				}
				delete(r.fileServices, instance)
			}
			r.files[instance].closed = true
		}
		if err := r.services.Storage.Delete(
			namespace,
			filename,
		); err != nil && !errors.Is(err, shared.ErrNotFound) {
			return 0, err
		}
		if namespace == shared.NamespacePrivate {
			delete(r.fileData, filename)
		}
		r.tracef(
			"java_file_remove:%s:closed=%d",
			filename,
			len(instances),
		)
		return 0, nil
	case "toCString(Ljava/lang/String;)[B":
		nameAddress, err := r.parameter(1)
		if err != nil {
			return 0, err
		}
		value := append([]byte(r.javaStringValue(nameAddress)), 0)
		return r.newJavaByteArray(value)
	case "getCreationTime(Ljava/lang/String;)I",
		"getCreationTime(Ljava/lang/String;I)I":
		nameAddress, err := r.parameter(1)
		if err != nil {
			return 0, err
		}
		namespace, err := r.ktfFileSystemNamespace(descriptor, 2)
		if err != nil {
			return 0, err
		}
		filename := normalizeKTFFileName(r.javaStringValue(nameAddress))
		info, err := r.services.Storage.Stat(namespace, filename)
		if err != nil {
			return ^uint32(0), nil
		}
		seconds := info.Modified / time.Second
		return uint32(min(seconds, time.Duration(math.MaxInt32))), nil
	case "rename(Ljava/lang/String;Ljava/lang/String;)V",
		"rename(Ljava/lang/String;Ljava/lang/String;I)V":
		oldAddress, err := r.parameter(1)
		if err != nil {
			return 0, err
		}
		newAddress, err := r.parameter(2)
		if err != nil {
			return 0, err
		}
		namespace, err := r.ktfFileSystemNamespace(descriptor, 3)
		if err != nil {
			return 0, err
		}
		oldName := normalizeKTFFileName(r.javaStringValue(oldAddress))
		newName := normalizeKTFFileName(r.javaStringValue(newAddress))
		if err := r.services.Storage.Rename(namespace, oldName, newName); err != nil {
			return 0, r.raiseHostJavaException("java/io/IOException")
		}
		for _, file := range r.files {
			if file != nil && ktfFileNamespace(file) == namespace &&
				file.name == oldName {
				file.name = newName
			}
		}
		if namespace == shared.NamespacePrivate {
			if data, ok := r.fileData[oldName]; ok {
				delete(r.fileData, oldName)
				r.fileData[newName] = data
			}
		}
		r.tracef("java_file_rename:%s:%s->%s", namespace, oldName, newName)
		return 0, nil
	case "getFreeSpace()J":
		return r.javaLongResult(r.ktfFreeStorageBytes()), nil
	case "available()I":
		// KTF titles gate startup on the int form of the free-space query and
		// abort with a "not enough space" card when it reports zero.
		return uint32(min(r.ktfFreeStorageBytes(), math.MaxInt32)), nil
	default:
		r.recordUnimplementedJava("org/kwis/msp/io/FileSystem", name, descriptor)
		return 0, nil
	}
}

func (r *ktfRuntime) ktfFileSystemNamespace(
	descriptor string,
	flagParameter uint32,
) (shared.Namespace, error) {
	if !strings.Contains(descriptor, ";I") {
		return shared.NamespacePrivate, nil
	}
	flag, err := r.parameter(flagParameter)
	if err != nil {
		return 0, err
	}
	return r.ktfStorageNamespace(flag)
}

func (r *ktfRuntime) ktfFreeStorageBytes() uint64 {
	limit := r.services.Config.Limits.Storage.MaxStorageBytes
	used := r.services.Storage.Used(shared.NamespacePrivate)
	if used >= limit {
		return 0
	}
	return limit - used
}

// recordUnimplementedJava keeps modeled-class methods that fall through their
// handler visible in diagnostics. Without it a silent zero looks like a real
// answer, which is how a missing free-space query reads as an empty disk.
func (r *ktfRuntime) recordUnimplementedJava(className, name, descriptor string) {
	signature := className + "." + name + descriptor
	r.unimplementedJava[signature]++
	r.lastUnimplementedJava = signature
	r.tracef("java_unimplemented:%s", signature)
}

func (r *ktfRuntime) newHostJavaObject(className string) (uint32, error) {
	classAddress, err := r.ensureJavaClass(className)
	if err != nil {
		return 0, err
	}
	class, err := r.inspectJavaClass(classAddress)
	if err != nil {
		return 0, err
	}
	return r.newJavaInstanceForClass(class)
}

func (r *ktfRuntime) javaArrayLength(instance uint32) (uint32, error) {
	if instance == 0 {
		return 0, errors.New("KTF Java array is null")
	}
	fields, err := r.readU32(instance)
	if err != nil {
		return 0, err
	}
	return r.readU32(fields + 4)
}

func (r *ktfRuntime) javaLongResult(value uint64) uint32 {
	r.javaReturnHigh = uint32(value >> 32)
	return uint32(value)
}

func (r *ktfRuntime) handleStringMethod(
	name, descriptor string,
) (uint32, error) {
	instance, err := r.parameter(1)
	if err != nil {
		return 0, err
	}
	value := r.javaStrings[instance]
	codeUnits := utf16.Encode([]rune(value))
	switch name + descriptor {
	case "valueOf(I)Ljava/lang/String;":
		return r.newJavaString(strconv.FormatInt(int64(int32(instance)), 10))
	case "valueOf(J)Ljava/lang/String;":
		high, valueErr := r.parameter(2)
		if valueErr != nil {
			return 0, valueErr
		}
		number := int64(uint64(high)<<32 | uint64(instance))
		return r.newJavaString(strconv.FormatInt(number, 10))
	case "valueOf(Z)Ljava/lang/String;":
		if instance == 0 {
			return r.newJavaString("false")
		}
		return r.newJavaString("true")
	case "valueOf(C)Ljava/lang/String;":
		return r.newJavaString(string(rune(uint16(instance))))
	case "valueOf(Ljava/lang/Object;)Ljava/lang/String;":
		return r.newJavaString(r.javaObjectString(instance))
	case "valueOf([C)Ljava/lang/String;":
		if instance == 0 {
			return r.newJavaString("null")
		}
		length, valueErr := r.javaArrayLength(instance)
		if valueErr != nil {
			return 0, valueErr
		}
		text, valueErr := r.readJavaCharArrayRange(instance, 0, length)
		if valueErr != nil {
			return 0, valueErr
		}
		return r.newJavaString(text)
	case "valueOf([CII)Ljava/lang/String;":
		offset, valueErr := r.parameter(2)
		if valueErr != nil {
			return 0, valueErr
		}
		count, valueErr := r.parameter(3)
		if valueErr != nil {
			return 0, valueErr
		}
		text, valueErr := r.readJavaCharArrayRange(instance, offset, count)
		if valueErr != nil {
			return 0, valueErr
		}
		return r.newJavaString(text)
	case "<init>()V":
		r.javaStrings[instance] = ""
		return 0, nil
	case "<init>(Ljava/lang/String;)V":
		source, valueErr := r.parameter(2)
		if valueErr != nil {
			return 0, valueErr
		}
		r.javaStrings[instance] = r.javaStringValue(source)
		return 0, nil
	case "<init>([B)V":
		array, valueErr := r.parameter(2)
		if valueErr != nil {
			return 0, valueErr
		}
		if array == 0 {
			r.javaStrings[instance] = ""
			return 0, nil
		}
		data, valueErr := r.readJavaByteArray(array)
		if valueErr != nil {
			return 0, valueErr
		}
		value, valueErr = r.services.Text.Decode(data, shared.EncodingEUCKR)
		if valueErr != nil {
			return 0, valueErr
		}
		r.javaStrings[instance] = value
		return 0, nil
	case "<init>([BII)V":
		array, valueErr := r.parameter(2)
		if valueErr != nil {
			return 0, valueErr
		}
		if array == 0 {
			r.javaStrings[instance] = ""
			return 0, nil
		}
		offset, valueErr := r.parameter(3)
		if valueErr != nil {
			return 0, valueErr
		}
		count, valueErr := r.parameter(4)
		if valueErr != nil {
			return 0, valueErr
		}
		data, valueErr := r.readJavaByteArrayRange(array, offset, count)
		if valueErr != nil {
			return 0, valueErr
		}
		value, valueErr = r.services.Text.Decode(data, shared.EncodingEUCKR)
		if valueErr != nil {
			return 0, valueErr
		}
		r.javaStrings[instance] = value
		return 0, nil
	case "<init>([C)V":
		array, valueErr := r.parameter(2)
		if valueErr != nil {
			return 0, valueErr
		}
		length, valueErr := r.javaArrayLength(array)
		if valueErr != nil {
			return 0, valueErr
		}
		text, valueErr := r.readJavaCharArrayRange(array, 0, length)
		if valueErr != nil {
			return 0, valueErr
		}
		r.javaStrings[instance] = text
		return 0, nil
	case "<init>([CII)V":
		array, valueErr := r.parameter(2)
		if valueErr != nil {
			return 0, valueErr
		}
		offset, valueErr := r.parameter(3)
		if valueErr != nil {
			return 0, valueErr
		}
		count, valueErr := r.parameter(4)
		if valueErr != nil {
			return 0, valueErr
		}
		text, valueErr := r.readJavaCharArrayRange(array, offset, count)
		if valueErr != nil {
			return 0, valueErr
		}
		r.javaStrings[instance] = text
		return 0, nil
	case "length()I":
		return uint32(len(codeUnits)), nil
	case "hashCode()I":
		var hash int32
		for _, codeUnit := range codeUnits {
			hash = hash*31 + int32(codeUnit)
		}
		return uint32(hash), nil
	case "charAt(I)C":
		index, valueErr := r.parameter(2)
		if valueErr != nil {
			return 0, valueErr
		}
		if index >= uint32(len(codeUnits)) {
			return 0, nil
		}
		return uint32(codeUnits[index]), nil
	case "substring(I)Ljava/lang/String;":
		start, valueErr := r.parameter(2)
		if valueErr != nil {
			return 0, valueErr
		}
		if start > uint32(len(codeUnits)) {
			return 0, nil
		}
		return r.newJavaString(string(utf16.Decode(codeUnits[start:])))
	case "substring(II)Ljava/lang/String;":
		start, valueErr := r.parameter(2)
		if valueErr != nil {
			return 0, valueErr
		}
		end, valueErr := r.parameter(3)
		if valueErr != nil {
			return 0, valueErr
		}
		if start > end || end > uint32(len(codeUnits)) {
			return 0, nil
		}
		return r.newJavaString(string(utf16.Decode(codeUnits[start:end])))
	case "trim()Ljava/lang/String;":
		return r.newJavaString(strings.TrimSpace(value))
	case "getBytes()[B":
		encoded, valueErr := r.services.Text.Encode(
			value,
			shared.EncodingEUCKR,
		)
		if valueErr != nil {
			return 0, valueErr
		}
		return r.newJavaByteArray(encoded)
	case "toCharArray()[C":
		return r.newJavaCharArray(value)
	case "equals(Ljava/lang/Object;)Z":
		other, valueErr := r.parameter(2)
		if valueErr != nil {
			return 0, valueErr
		}
		if instance == other {
			return 1, nil
		}
		if otherValue, ok := r.javaStrings[other]; ok && value == otherValue {
			return 1, nil
		}
		return 0, nil
	case "compareTo(Ljava/lang/String;)I":
		other, valueErr := r.parameter(2)
		if valueErr != nil {
			return 0, valueErr
		}
		otherValue := r.javaStrings[other]
		otherCodeUnits := utf16.Encode([]rune(otherValue))
		limit := min(len(codeUnits), len(otherCodeUnits))
		for index := range limit {
			if codeUnits[index] == otherCodeUnits[index] {
				continue
			}
			return uint32(
				int32(codeUnits[index]) - int32(otherCodeUnits[index]),
			), nil
		}
		return uint32(int32(len(codeUnits) - len(otherCodeUnits))), nil
	case "concat(Ljava/lang/String;)Ljava/lang/String;":
		other, valueErr := r.parameter(2)
		if valueErr != nil {
			return 0, valueErr
		}
		return r.newJavaString(value + r.javaStringValue(other))
	case "startsWith(Ljava/lang/String;)Z":
		prefix, valueErr := r.parameter(2)
		if valueErr != nil {
			return 0, valueErr
		}
		if strings.HasPrefix(value, r.javaStringValue(prefix)) {
			return 1, nil
		}
		return 0, nil
	case "endsWith(Ljava/lang/String;)Z":
		suffix, valueErr := r.parameter(2)
		if valueErr != nil {
			return 0, valueErr
		}
		if strings.HasSuffix(value, r.javaStringValue(suffix)) {
			return 1, nil
		}
		return 0, nil
	case "indexOf(I)I", "indexOf(II)I":
		target, valueErr := r.parameter(2)
		if valueErr != nil {
			return 0, valueErr
		}
		targetUnits, valid := javaCodePointUnits(target)
		if !valid {
			return ^uint32(0), nil
		}
		fromIndex := 0
		if descriptor == "(II)I" {
			from, parameterErr := r.parameter(3)
			if parameterErr != nil {
				return 0, parameterErr
			}
			fromIndex = int(int32(from))
		}
		return uint32(int32(indexJavaCodeUnitsFrom(
			codeUnits,
			targetUnits,
			fromIndex,
		))), nil
	case "indexOf(Ljava/lang/String;)I",
		"indexOf(Ljava/lang/String;I)I":
		target, valueErr := r.parameter(2)
		if valueErr != nil {
			return 0, valueErr
		}
		targetUnits := utf16.Encode([]rune(r.javaStringValue(target)))
		fromIndex := 0
		if descriptor == "(Ljava/lang/String;I)I" {
			from, parameterErr := r.parameter(3)
			if parameterErr != nil {
				return 0, parameterErr
			}
			fromIndex = int(int32(from))
		}
		return uint32(int32(indexJavaCodeUnitsFrom(
			codeUnits,
			targetUnits,
			fromIndex,
		))), nil
	case "toLowerCase()Ljava/lang/String;":
		return r.newJavaString(strings.ToLower(value))
	case "toUpperCase()Ljava/lang/String;":
		return r.newJavaString(strings.ToUpper(value))
	default:
		return 0, nil
	}
}

const unicodeMaxRune = uint32(0x10ffff)

func indexJavaCodeUnits(value, target []uint16) int {
	return indexJavaCodeUnitsFrom(value, target, 0)
}

func indexJavaCodeUnitsFrom(value, target []uint16, fromIndex int) int {
	if fromIndex < 0 {
		fromIndex = 0
	}
	if len(target) == 0 {
		if fromIndex > len(value) {
			return len(value)
		}
		return fromIndex
	}
	if fromIndex >= len(value) || len(target) > len(value)-fromIndex {
		return -1
	}
	for start := fromIndex; start <= len(value)-len(target); start++ {
		matched := true
		for offset := range target {
			if value[start+offset] != target[offset] {
				matched = false
				break
			}
		}
		if matched {
			return start
		}
	}
	return -1
}

func javaCodePointUnits(value uint32) ([]uint16, bool) {
	if value > unicodeMaxRune {
		return nil, false
	}
	if value < 0x10000 {
		return []uint16{uint16(value)}, true
	}
	return utf16.Encode([]rune{rune(value)}), true
}

func (r *ktfRuntime) newJavaCharArray(value string) (uint32, error) {
	codeUnits := utf16.Encode([]rune(value))
	array, err := r.newJavaArray("[C", uint32(len(codeUnits)), 2)
	if err != nil {
		return 0, err
	}
	fields, err := r.readU32(array)
	if err != nil {
		return 0, err
	}
	encoded := make([]byte, len(codeUnits)*2)
	for index, codeUnit := range codeUnits {
		binary.LittleEndian.PutUint16(encoded[index*2:], codeUnit)
	}
	if err := r.cpu.WriteMemory(fields+8, encoded); err != nil {
		return 0, err
	}
	return array, nil
}

func (r *ktfRuntime) handleStringBufferMethod(
	name, descriptor string,
) (uint32, error) {
	instance, err := r.parameter(1)
	if err != nil {
		return 0, err
	}
	switch name + descriptor {
	case "<init>()V", "<init>(I)V":
		r.stringBuffers[instance] = ""
		return 0, nil
	case "<init>(Ljava/lang/String;)V":
		value, valueErr := r.parameter(2)
		if valueErr != nil {
			return 0, valueErr
		}
		r.stringBuffers[instance] = r.javaStringValue(value)
		return 0, nil
	case "append(Ljava/lang/String;)Ljava/lang/StringBuffer;":
		value, valueErr := r.parameter(2)
		if valueErr != nil {
			return 0, valueErr
		}
		r.stringBuffers[instance] += r.javaStringValue(value)
		return instance, nil
	case "append(Ljava/lang/Object;)Ljava/lang/StringBuffer;":
		value, valueErr := r.parameter(2)
		if valueErr != nil {
			return 0, valueErr
		}
		r.stringBuffers[instance] += r.javaObjectString(value)
		return instance, nil
	case "append(Z)Ljava/lang/StringBuffer;":
		value, valueErr := r.parameter(2)
		if valueErr != nil {
			return 0, valueErr
		}
		if value == 0 {
			r.stringBuffers[instance] += "false"
		} else {
			r.stringBuffers[instance] += "true"
		}
		return instance, nil
	case "append(I)Ljava/lang/StringBuffer;":
		value, valueErr := r.parameter(2)
		if valueErr != nil {
			return 0, valueErr
		}
		r.stringBuffers[instance] += fmt.Sprintf("%d", int32(value))
		return instance, nil
	case "append(J)Ljava/lang/StringBuffer;":
		low, valueErr := r.parameter(2)
		if valueErr != nil {
			return 0, valueErr
		}
		high, valueErr := r.parameter(3)
		if valueErr != nil {
			return 0, valueErr
		}
		value := int64(uint64(high)<<32 | uint64(low))
		r.stringBuffers[instance] += fmt.Sprintf("%d", value)
		return instance, nil
	case "append(C)Ljava/lang/StringBuffer;":
		value, valueErr := r.parameter(2)
		if valueErr != nil {
			return 0, valueErr
		}
		r.stringBuffers[instance] += string(rune(uint16(value)))
		return instance, nil
	case "append([CII)Ljava/lang/StringBuffer;":
		array, valueErr := r.parameter(2)
		if valueErr != nil {
			return 0, valueErr
		}
		offset, valueErr := r.parameter(3)
		if valueErr != nil {
			return 0, valueErr
		}
		count, valueErr := r.parameter(4)
		if valueErr != nil {
			return 0, valueErr
		}
		value, valueErr := r.readJavaCharArrayRange(array, offset, count)
		if valueErr != nil {
			return 0, valueErr
		}
		r.stringBuffers[instance] += value
		return instance, nil
	case "delete(II)Ljava/lang/StringBuffer;":
		start, valueErr := r.parameter(2)
		if valueErr != nil {
			return 0, valueErr
		}
		end, valueErr := r.parameter(3)
		if valueErr != nil {
			return 0, valueErr
		}
		runes := []rune(r.stringBuffers[instance])
		if start > end || start > uint32(len(runes)) {
			return 0, fmt.Errorf(
				"KTF StringBuffer delete range [%d,%d) exceeds %d",
				start,
				end,
				len(runes),
			)
		}
		if end > uint32(len(runes)) {
			end = uint32(len(runes))
		}
		r.stringBuffers[instance] = string(
			append(runes[:start:start], runes[end:]...),
		)
		return instance, nil
	case "toString()Ljava/lang/String;":
		return r.newJavaString(r.stringBuffers[instance])
	case "setLength(I)V":
		length, valueErr := r.parameter(2)
		if valueErr != nil {
			return 0, valueErr
		}
		runes := []rune(r.stringBuffers[instance])
		switch {
		case length < uint32(len(runes)):
			runes = runes[:length]
		case length > uint32(len(runes)):
			runes = append(runes, make([]rune, length-uint32(len(runes)))...)
		}
		r.stringBuffers[instance] = string(runes)
		return 0, nil
	case "length()I":
		return uint32(len([]rune(r.stringBuffers[instance]))), nil
	case "capacity()I":
		return uint32(len([]rune(r.stringBuffers[instance]))), nil
	case "ensureCapacity(I)V":
		return 0, nil
	case "charAt(I)C":
		index, valueErr := r.parameter(2)
		if valueErr != nil {
			return 0, valueErr
		}
		runes := []rune(r.stringBuffers[instance])
		if index >= uint32(len(runes)) {
			return 0, nil
		}
		return uint32(runes[index]), nil
	case "setCharAt(IC)V":
		index, valueErr := r.parameter(2)
		if valueErr != nil {
			return 0, valueErr
		}
		character, valueErr := r.parameter(3)
		if valueErr != nil {
			return 0, valueErr
		}
		runes := []rune(r.stringBuffers[instance])
		if index >= uint32(len(runes)) {
			return 0, fmt.Errorf(
				"KTF StringBuffer setCharAt index %d exceeds %d",
				index,
				len(runes),
			)
		}
		runes[index] = rune(uint16(character))
		r.stringBuffers[instance] = string(runes)
		return 0, nil
	case "deleteCharAt(I)Ljava/lang/StringBuffer;":
		index, valueErr := r.parameter(2)
		if valueErr != nil {
			return 0, valueErr
		}
		runes := []rune(r.stringBuffers[instance])
		if index >= uint32(len(runes)) {
			return 0, fmt.Errorf(
				"KTF StringBuffer deleteCharAt index %d exceeds %d",
				index,
				len(runes),
			)
		}
		r.stringBuffers[instance] = string(
			append(runes[:index:index], runes[index+1:]...),
		)
		return instance, nil
	case "reverse()Ljava/lang/StringBuffer;":
		runes := []rune(r.stringBuffers[instance])
		for left, right := 0, len(runes)-1; left < right; left, right = left+1, right-1 {
			runes[left], runes[right] = runes[right], runes[left]
		}
		r.stringBuffers[instance] = string(runes)
		return instance, nil
	case "insert(ILjava/lang/String;)Ljava/lang/StringBuffer;",
		"insert(ILjava/lang/Object;)Ljava/lang/StringBuffer;",
		"insert(IC)Ljava/lang/StringBuffer;",
		"insert(II)Ljava/lang/StringBuffer;",
		"insert(IZ)Ljava/lang/StringBuffer;",
		"insert(IJ)Ljava/lang/StringBuffer;":
		offset, valueErr := r.parameter(2)
		if valueErr != nil {
			return 0, valueErr
		}
		value, valueErr := r.parameter(3)
		if valueErr != nil {
			return 0, valueErr
		}
		var inserted string
		switch descriptor {
		case "(ILjava/lang/String;)Ljava/lang/StringBuffer;":
			inserted = r.javaStringValue(value)
		case "(ILjava/lang/Object;)Ljava/lang/StringBuffer;":
			inserted = r.javaObjectString(value)
		case "(IC)Ljava/lang/StringBuffer;":
			inserted = string(rune(uint16(value)))
		case "(II)Ljava/lang/StringBuffer;":
			inserted = fmt.Sprintf("%d", int32(value))
		case "(IZ)Ljava/lang/StringBuffer;":
			inserted = "false"
			if value != 0 {
				inserted = "true"
			}
		default: // (IJ)
			high, highErr := r.parameter(4)
			if highErr != nil {
				return 0, highErr
			}
			inserted = fmt.Sprintf(
				"%d",
				int64(uint64(high)<<32|uint64(value)),
			)
		}
		runes := []rune(r.stringBuffers[instance])
		if offset > uint32(len(runes)) {
			return 0, fmt.Errorf(
				"KTF StringBuffer insert offset %d exceeds %d",
				offset,
				len(runes),
			)
		}
		r.stringBuffers[instance] = string(runes[:offset]) +
			inserted +
			string(runes[offset:])
		return instance, nil
	default:
		r.recordUnimplementedJava("java/lang/StringBuffer", name, descriptor)
		return 0, nil
	}
}

func (r *ktfRuntime) javaStringValue(instance uint32) string {
	if instance == 0 {
		return "null"
	}
	if value, ok := r.javaStrings[instance]; ok {
		return value
	}
	if value, ok := r.readGuestJavaString(instance); ok {
		return value
	}
	return r.javaObjectString(instance)
}

// javaText returns the contents of a java/lang/String argument, or the empty
// string when the reference is null or is not a string. Unlike
// javaStringValue it never substitutes a diagnostic placeholder, so callers
// that use the result as a resource, class or database name see an absent name
// rather than a fabricated one.
func (r *ktfRuntime) javaText(instance uint32) string {
	if instance == 0 {
		return ""
	}
	if value, ok := r.javaStrings[instance]; ok {
		return value
	}
	value, _ := r.readGuestJavaString(instance)
	return value
}

// readGuestJavaString decodes a java/lang/String the guest built for itself.
// Host-created strings are memoised in javaStrings, but a title that assembles
// a name through StringBuffer, substring or concat produces an instance the
// host has never seen. Reading its value/offset/count fields is the only way
// those names reach APIs like Class.getResourceAsStream.
func (r *ktfRuntime) readGuestJavaString(instance uint32) (string, bool) {
	words, err := r.readWords(instance, 2)
	if err != nil {
		return "", false
	}
	class, err := r.inspectJavaClass(words[1])
	if err != nil || class.Name != "java/lang/String" {
		return "", false
	}
	characters, err := r.readJavaFieldWord(instance, 0)
	if err != nil || characters == 0 {
		return "", false
	}
	offset, err := r.readJavaFieldWord(instance, 4)
	if err != nil {
		return "", false
	}
	count, err := r.readJavaFieldWord(instance, 8)
	if err != nil {
		return "", false
	}
	value, err := r.readJavaCharArrayRange(characters, offset, count)
	if err != nil {
		return "", false
	}
	return value, true
}

func (r *ktfRuntime) javaObjectString(instance uint32) string {
	if instance == 0 {
		return "null"
	}
	if value, ok := r.javaStrings[instance]; ok {
		return value
	}
	words, err := r.readWords(instance, 2)
	if err != nil {
		return fmt.Sprintf("Object@%08x", instance)
	}
	class, err := r.inspectJavaClass(words[1])
	if err != nil {
		return fmt.Sprintf("Object@%08x", instance)
	}
	return fmt.Sprintf("%s@%08x", class.Name, instance)
}

func (r *ktfRuntime) readJavaCharArrayRange(
	instance, offset, count uint32,
) (string, error) {
	if instance == 0 {
		return "", errors.New("KTF Java char array is null")
	}
	fields, err := r.readU32(instance)
	if err != nil {
		return "", err
	}
	length, err := r.readU32(fields + 4)
	if err != nil {
		return "", err
	}
	if offset > length || count > length-offset {
		return "", fmt.Errorf(
			"KTF Java char array range [%d,%d) exceeds length %d",
			offset,
			offset+count,
			length,
		)
	}
	encoded := make([]byte, count*2)
	if err := r.cpu.ReadMemory(fields+8+offset*2, encoded); err != nil {
		return "", err
	}
	codeUnits := make([]uint16, count)
	for index := range codeUnits {
		codeUnits[index] = binary.LittleEndian.Uint16(encoded[index*2:])
	}
	return string(utf16.Decode(codeUnits)), nil
}

func (r *ktfRuntime) handleClassMethod(
	ctx context.Context,
	name, descriptor string,
) (uint32, error) {
	switch name + descriptor {
	case "<init>()V":
		return 0, nil
	case "getName()Ljava/lang/String;":
		classObject, err := r.parameter(1)
		if err != nil {
			return 0, err
		}
		classAddress, err := r.javaClassObjectTarget(classObject)
		if err != nil {
			return 0, err
		}
		class, err := r.inspectJavaClass(classAddress)
		if err != nil {
			return 0, err
		}
		return r.newJavaString(strings.ReplaceAll(class.Name, "/", "."))
	case "isAssignableFrom(Ljava/lang/Class;)Z":
		expectedObject, err := r.parameter(1)
		if err != nil {
			return 0, err
		}
		expected, err := r.javaClassObjectTarget(expectedObject)
		if err != nil {
			return 0, err
		}
		actualObject, err := r.parameter(2)
		if err != nil {
			return 0, err
		}
		actual, err := r.javaClassObjectTarget(actualObject)
		if err != nil {
			return 0, err
		}
		for depth := 0; actual != 0; depth++ {
			if depth >= 256 {
				return 0, errors.New("KTF Java class hierarchy exceeds limit")
			}
			if actual == expected {
				return 1, nil
			}
			class, inspectErr := r.inspectJavaClass(actual)
			if inspectErr != nil {
				return 0, inspectErr
			}
			actual = class.Parent
		}
		return 0, nil
	case "getResourceAsStream(Ljava/lang/String;)Ljava/io/InputStream;":
		classObject, err := r.parameter(1)
		if err != nil {
			return 0, err
		}
		nameAddress, err := r.parameter(2)
		if err != nil {
			return 0, err
		}
		className := ""
		if classAddress, classErr := r.javaClassObjectTarget(classObject); classErr == nil {
			if class, inspectErr := r.inspectJavaClass(classAddress); inspectErr == nil {
				className = class.Name
			}
		}
		resourceName := strings.ReplaceAll(r.javaText(nameAddress), `\`, "/")
		resourceName = strings.TrimPrefix(resourceName, "/")
		resourceName = path.Clean(resourceName)
		if resourceName == "." || resourceName == ".." ||
			strings.HasPrefix(resourceName, "../") {
			return 0, nil
		}
		data, ok := r.pkg.Resources[resourceName]
		if !ok {
			for candidate, payload := range r.pkg.Resources {
				if strings.EqualFold(candidate, resourceName) {
					resourceName = candidate
					data = payload
					ok = true
					break
				}
			}
		}
		r.tracef(
			"java_resource:%s:class=%s:found=%t:size=%d",
			resourceName,
			className,
			ok,
			len(data),
		)
		if !ok {
			return 0, nil
		}
		classAddress, err := r.ensureJavaClass("java/io/InputStream")
		if err != nil {
			return 0, err
		}
		class, err := r.inspectJavaClass(classAddress)
		if err != nil {
			return 0, err
		}
		instance, err := r.newJavaInstanceForClass(class)
		if err != nil {
			return 0, err
		}
		r.inputStreams[instance] = &ktfInputStream{data: data}
		return instance, nil
	case "forName(Ljava/lang/String;)Ljava/lang/Class;":
		nameAddress, err := r.parameter(1)
		if err != nil {
			return 0, err
		}
		className := strings.ReplaceAll(r.javaText(nameAddress), ".", "/")
		if className == "" {
			return 0, nil
		}
		classAddress := r.javaClasses[className]
		if classAddress == 0 {
			if _, hostClass := ktfHostJavaClassSpecs[className]; hostClass {
				classAddress, err = r.ensureJavaClass(className)
				if err != nil {
					return 0, err
				}
			} else {
				class, loadErr := r.loadClass(ctx, className)
				if loadErr != nil {
					r.tracef(
						"java_class_for_name:%s:found=false",
						className,
					)
					return 0, nil
				}
				classAddress = class.Address
			}
		}
		r.tracef("java_class_for_name:%s:found=true", className)
		return r.javaClassObject(classAddress)
	default:
		return 0, nil
	}
}

func (r *ktfRuntime) javaClassObject(classAddress uint32) (uint32, error) {
	if classAddress == 0 {
		return 0, nil
	}
	if object := r.javaClassObjs[classAddress]; object != 0 {
		return object, nil
	}
	classClassAddress, err := r.ensureJavaClass("java/lang/Class")
	if err != nil {
		return 0, err
	}
	classClass, err := r.inspectJavaClass(classClassAddress)
	if err != nil {
		return 0, err
	}
	object, err := r.newJavaInstanceForClass(classClass)
	if err != nil {
		return 0, err
	}
	r.javaClassObjs[classAddress] = object
	r.classObjTarget[object] = classAddress
	return object, nil
}

func (r *ktfRuntime) javaClassObjectTarget(object uint32) (uint32, error) {
	if object == 0 {
		return 0, errors.New("KTF java.lang.Class instance is null")
	}
	if target := r.classObjTarget[object]; target != 0 {
		return target, nil
	}
	return 0, fmt.Errorf("unknown KTF java.lang.Class instance 0x%08x", object)
}

func (r *ktfRuntime) handleThreadMethod(
	ctx context.Context,
	name, descriptor string,
) (uint32, error) {
	switch name + descriptor {
	case "<init>()V", "<init>(Z)V":
		thread, err := r.parameter(1)
		if err != nil {
			return 0, err
		}
		if r.currentThread == 0 {
			r.currentThread = thread
		}
		return 0, nil
	case "<init>(Ljava/lang/Runnable;)V":
		thread, err := r.parameter(1)
		if err != nil {
			return 0, err
		}
		target, err := r.parameter(2)
		if err != nil {
			return 0, err
		}
		r.threadTargets[thread] = target
		return 0, nil
	case "start()V":
		thread, err := r.parameter(1)
		if err != nil {
			return 0, err
		}
		if r.deferThreads {
			target := r.threadTargets[thread]
			if target == 0 {
				target = thread
			}
			task, err := r.queueJavaVirtualTask(target, "run", "()V")
			if err != nil {
				return 0, err
			}
			r.deferStartedThread(task)
			return 0, nil
		}
		previous := r.currentThread
		r.currentThread = thread
		_, invokeErr := r.invokeJavaVirtual(ctx, thread, "run", "()V")
		r.currentThread = previous
		return 0, invokeErr
	case "run()V":
		thread, err := r.parameter(1)
		if err != nil {
			return 0, err
		}
		target := r.threadTargets[thread]
		if target == 0 {
			return 0, nil
		}
		return r.invokeJavaVirtual(ctx, target, "run", "()V")
	case "join()V", "setPriority(I)V":
		return 0, nil
	case "sleep(J)V":
		low, err := r.parameter(1)
		if err != nil {
			return 0, err
		}
		high, err := r.parameter(2)
		if err != nil {
			return 0, err
		}
		millis := int64(uint64(high)<<32 | uint64(low))
		if millis < 0 {
			return r.raiseJavaException("java/lang/IllegalArgumentException", 0)
		}
		if r.deferThreads {
			if r.activeTask != nil && millis != 0 {
				delay := uint64(millis)
				if delay > ^uint64(0)-r.tickMS {
					r.activeTask.wakeAtMS = ^uint64(0)
				} else {
					r.activeTask.wakeAtMS = r.tickMS + delay
				}
				r.tracef(
					"java_thread_sleep:duration_ms=%d:wake_at_ms=%d",
					millis,
					r.activeTask.wakeAtMS,
				)
			}
			r.yieldRequested = true
		}
		return 0, nil
	case "yield()V":
		if r.deferThreads {
			r.yieldRequested = true
		}
		return 0, nil
	case "isAlive()Z":
		return 0, nil
	case "currentThread()Ljava/lang/Thread;":
		if r.currentThread != 0 {
			return r.currentThread, nil
		}
		classAddress, err := r.ensureJavaClass("java/lang/Thread")
		if err != nil {
			return 0, err
		}
		class, err := r.inspectJavaClass(classAddress)
		if err != nil {
			return 0, err
		}
		r.currentThread, err = r.newJavaInstanceForClass(class)
		return r.currentThread, err
	default:
		return 0, nil
	}
}

func (r *ktfRuntime) handleDisplayMethod(
	ctx context.Context,
	name, descriptor string,
) (uint32, error) {
	switch name + descriptor {
	case "<init>(Lorg/kwis/msp/lcdui/Jlet;Lorg/kwis/msp/lcdui/DisplayProxy;)V":
		instance, err := r.parameter(1)
		if err != nil {
			return 0, err
		}
		if instance == 0 {
			return 0, errors.New("initialize KTF Display: instance is null")
		}
		if r.defaultDisplay == 0 {
			r.defaultDisplay = instance
		}
		return 0, nil
	case "getDisplay(Ljava/lang/String;)Lorg/kwis/msp/lcdui/Display;",
		"getDefaultDisplay()Lorg/kwis/msp/lcdui/Display;":
		return r.ensureDefaultDisplay()
	case "isDoubleBuffered()Z":
		return 1, nil
	case "getDockedCard()Lorg/kwis/msp/lcdui/Card;":
		instance, err := r.parameter(1)
		if err != nil {
			return 0, err
		}
		return r.displayCards[instance], nil
	case "pushCard(Lorg/kwis/msp/lcdui/Card;)V":
		instance, err := r.parameter(1)
		if err != nil {
			return 0, err
		}
		card, err := r.parameter(2)
		if err != nil {
			return 0, err
		}
		wasDirty := r.dirtyCards[card]
		r.tracef(
			"java_display_push_card:card=0x%08x:dirty=%t:tasks=%d",
			card,
			wasDirty,
			len(r.tasks),
		)
		r.displayCards[instance] = card
		if card == 0 {
			return 0, nil
		}
		r.dirtyCards[card] = true
		if r.deferThreads && r.activeTask != nil {
			r.deferCardPaint(r.activeTask, card, true)
			return 0, nil
		}
		if err := r.notifyCardShown(ctx, card, true); err != nil {
			return 0, err
		}
		if err := r.paintCard(ctx, card); err != nil {
			var unhandled *ktfUnhandledJavaException
			if !errors.As(err, &unhandled) {
				return 0, err
			}
			r.paintInitializedCards[card] = true
			r.tracef(
				"java_initial_paint_discard:%s:card=0x%08x",
				unhandled.name,
				card,
			)
		}
		return 0, nil
	case "removeAllCards()V":
		instance, err := r.parameter(1)
		if err != nil {
			return 0, err
		}
		delete(r.displayCards, instance)
		return 0, nil
	case "getWidth()I":
		return r.displayWidth(), nil
	case "getHeight()I":
		return r.displayHeight(), nil
	case "callSerially(Ljava/lang/Runnable;)V",
		"callSerially(Ljava/lang/Runnable;I)V":
		runnable, err := r.parameter(2)
		if err != nil {
			return 0, err
		}
		if runnable == 0 {
			return 0, nil
		}
		if r.deferThreads {
			return 0, r.queueJavaVirtual(runnable, "run", "()V")
		}
		return r.invokeJavaVirtual(ctx, runnable, "run", "()V")
	case "getGameAction(I)I":
		key, err := r.parameter(1)
		if err != nil {
			return 0, err
		}
		return ktfGameAction(int32(key)), nil
	case "getKeyCode(I)I":
		action, err := r.parameter(1)
		if err != nil {
			return 0, err
		}
		return uint32(ktfGameKeyCode(int32(action))), nil
	case "getKeyName(I)Ljava/lang/String;":
		key, err := r.parameter(1)
		if err != nil {
			return 0, err
		}
		return r.newJavaString(ktfKeyName(int32(key)))
	default:
		return 0, nil
	}
}

func ktfGameAction(key int32) uint32 {
	switch key {
	case -1:
		return 1 // EventQueue.UP
	case -2:
		return 6 // EventQueue.DOWN
	case -3:
		return 2 // EventQueue.LEFT
	case -4:
		return 5 // EventQueue.RIGHT
	case -5:
		return 8 // EventQueue.FIRE
	case -6:
		return 90 // EventQueue.SOFT1
	case -7:
		return 91 // EventQueue.SOFT2
	case -8:
		return 92 // EventQueue.SOFT3
	case -13:
		return 96 // EventQueue.SIDE_UP
	case -14:
		return 97 // EventQueue.SIDE_DOWN
	case -15:
		return 98 // EventQueue.SIDE_SEL
	case -16:
		return 99 // EventQueue.CLEAR
	default:
		return uint32(key)
	}
}

func ktfGameKeyCode(action int32) int32 {
	switch action {
	case 1: // EventQueue.UP
		return -1
	case 6: // EventQueue.DOWN
		return -2
	case 2: // EventQueue.LEFT
		return -3
	case 5: // EventQueue.RIGHT
		return -4
	case 8: // EventQueue.FIRE
		return -5
	case 90: // EventQueue.SOFT1
		return -6
	case 91: // EventQueue.SOFT2
		return -7
	case 92: // EventQueue.SOFT3
		return -8
	case 96: // EventQueue.SIDE_UP
		return -13
	case 97: // EventQueue.SIDE_DOWN
		return -14
	case 98: // EventQueue.SIDE_SEL
		return -15
	case 99: // EventQueue.CLEAR
		return -16
	default:
		return action
	}
}

func ktfKeyName(key int32) string {
	switch key {
	case -1:
		return "UP"
	case -2:
		return "DOWN"
	case -3:
		return "LEFT"
	case -4:
		return "RIGHT"
	case -5:
		return "FIRE"
	case -6:
		return "SOFT1"
	case -7:
		return "SOFT2"
	case -8:
		return "SOFT3"
	case -10:
		return "SEND"
	case -11:
		return "END"
	case -12:
		return "POWER"
	case -13:
		return "SIDE_UP"
	case -14:
		return "SIDE_DOWN"
	case -15:
		return "SIDE_SEL"
	case -16:
		return "CLEAR"
	case '*', '#', '0', '1', '2', '3', '4', '5', '6', '7', '8', '9':
		return string(rune(key))
	default:
		return ""
	}
}

func (r *ktfRuntime) ensureDefaultDisplay() (uint32, error) {
	if r.defaultDisplay != 0 {
		return r.defaultDisplay, nil
	}
	classAddress, err := r.ensureJavaClass("org/kwis/msp/lcdui/Display")
	if err != nil {
		return 0, err
	}
	class, err := r.inspectJavaClass(classAddress)
	if err != nil {
		return 0, err
	}
	r.defaultDisplay, err = r.newJavaInstanceForClass(class)
	if err != nil {
		return 0, err
	}
	return r.defaultDisplay, nil
}

func (r *ktfRuntime) ensureJavaRuntime() (uint32, error) {
	if r.defaultRuntime != 0 {
		return r.defaultRuntime, nil
	}
	classAddress, err := r.ensureJavaClass("java/lang/Runtime")
	if err != nil {
		return 0, err
	}
	class, err := r.inspectJavaClass(classAddress)
	if err != nil {
		return 0, err
	}
	r.defaultRuntime, err = r.newJavaInstanceForClass(class)
	if err != nil {
		return 0, err
	}
	return r.defaultRuntime, nil
}

func (r *ktfRuntime) invokeJavaVirtual(
	ctx context.Context,
	instance uint32,
	name, descriptor string,
	args ...uint32,
) (uint32, error) {
	if instance == 0 {
		return 0, fmt.Errorf("invoke Java method %s%s: instance is null", name, descriptor)
	}
	instanceWords, err := r.readWords(instance, 2)
	if err != nil {
		return 0, err
	}
	methodAddress, err := r.resolveJavaMethod(instanceWords[1], name, descriptor)
	if err != nil {
		return 0, err
	}
	method, err := r.inspectJavaMethod(methodAddress)
	if err != nil {
		return 0, err
	}
	if method.Body == 0 {
		return 0, fmt.Errorf(
			"Java class 0x%08x method %s%s has no executable body",
			instanceWords[1],
			name,
			descriptor,
		)
	}
	callArgs := make([]uint32, 0, len(args)+2)
	callArgs = append(callArgs, 0, instance)
	callArgs = append(callArgs, args...)
	result, value, err := r.call(
		ctx,
		method.Body,
		callArgs,
		ktfBootstrapInstructionMax,
	)
	if err != nil {
		return 0, fmt.Errorf(
			"invoke Java method %s%s at PC 0x%08x after %d instructions: %w",
			name,
			descriptor,
			result.PC,
			result.Instructions,
			err,
		)
	}
	return value, nil
}

func (r *ktfRuntime) handleDataBaseMethod(name, descriptor string) (uint32, error) {
	switch name + descriptor {
	case "<init>(Ljavax/microedition/rms/RecordStore;)V",
		"closeDataBase()V":
		return 0, nil
	case "openDataBase(Ljava/lang/String;IZ)Lorg/kwis/msp/db/DataBase;",
		"openDataBase(Ljava/lang/String;IZI)Lorg/kwis/msp/db/DataBase;":
		nameAddress, err := r.parameter(1)
		if err != nil {
			return 0, err
		}
		recordSize, err := r.parameter(2)
		if err != nil {
			return 0, err
		}
		create, err := r.parameter(3)
		if err != nil {
			return 0, err
		}
		databaseName := r.javaText(nameAddress)
		if databaseName == "" {
			databaseName = fmt.Sprintf("database-%08x", nameAddress)
		}
		store := r.databaseStores[databaseName]
		r.tracef(
			"java_database_open:%s:create=%t:exists=%t",
			databaseName,
			create != 0,
			store != nil,
		)
		if store == nil {
			if create == 0 {
				return r.raiseJavaException(
					"org/kwis/msp/db/DataBaseException",
					0,
				)
			}
			store = &ktfDatabase{name: databaseName, recordSize: recordSize}
			r.databaseStores[databaseName] = store
			serviceID, serviceErr := r.services.Storage.CreateRecordStore(
				r.serviceOwner,
				databaseName,
			)
			if serviceErr != nil {
				return 0, serviceErr
			}
			r.databaseServices[databaseName] = serviceID
		}
		classAddress, err := r.ensureJavaClass("org/kwis/msp/db/DataBase")
		if err != nil {
			return 0, err
		}
		class, err := r.inspectJavaClass(classAddress)
		if err != nil {
			return 0, err
		}
		instance, err := r.newJavaInstanceForClass(class)
		if err != nil {
			return 0, err
		}
		r.databases[instance] = store
		return instance, nil
	case "getNumberOfRecords()I":
		store, err := r.databaseParameter(1)
		if err != nil {
			return 0, err
		}
		return uint32(len(store.records)), nil
	case "insertRecord([B)I":
		store, err := r.databaseParameter(1)
		if err != nil {
			return 0, err
		}
		array, err := r.parameter(2)
		if err != nil {
			return 0, err
		}
		var data []byte
		if array != 0 {
			data, err = r.readJavaByteArray(array)
			if err != nil {
				return 0, err
			}
		}
		store.records = append(store.records, data)
		if err := r.syncKTFDatabase(store); err != nil {
			return 0, err
		}
		return uint32(len(store.records) - 1), nil
	case "insertRecord([BII)I":
		store, err := r.databaseParameter(1)
		if err != nil {
			return 0, err
		}
		array, err := r.parameter(2)
		if err != nil {
			return 0, err
		}
		offset, err := r.parameter(3)
		if err != nil {
			return 0, err
		}
		count, err := r.parameter(4)
		if err != nil {
			return 0, err
		}
		var data []byte
		if array != 0 {
			data, err = r.readJavaByteArrayRange(array, offset, count)
			if err != nil {
				return 0, err
			}
		}
		store.records = append(store.records, data)
		if err := r.syncKTFDatabase(store); err != nil {
			return 0, err
		}
		return uint32(len(store.records) - 1), nil
	case "selectRecord(I)[B":
		store, err := r.databaseParameter(1)
		if err != nil {
			return 0, err
		}
		recordID, err := r.parameter(2)
		if err != nil {
			return 0, err
		}
		if recordID >= uint32(len(store.records)) {
			return r.newJavaByteArray(nil)
		}
		return r.newJavaByteArray(store.records[recordID])
	case "updateRecord(I[B)V", "updateRecord(I[BII)V":
		store, err := r.databaseParameter(1)
		if err != nil {
			return 0, err
		}
		recordID, err := r.parameter(2)
		if err != nil {
			return 0, err
		}
		array, err := r.parameter(3)
		if err != nil {
			return 0, err
		}
		var data []byte
		if array == 0 {
			data = nil
		} else if descriptor == "(I[BII)V" {
			offset, err := r.parameter(4)
			if err != nil {
				return 0, err
			}
			count, err := r.parameter(5)
			if err != nil {
				return 0, err
			}
			data, err = r.readJavaByteArrayRange(array, offset, count)
			if err != nil {
				return 0, err
			}
		} else {
			data, err = r.readJavaByteArray(array)
			if err != nil {
				return 0, err
			}
		}
		if recordID >= uint32(len(store.records)) {
			if recordID > 65535 {
				return 0, fmt.Errorf(
					"KTF database record ID %d exceeds compatibility limit",
					recordID,
				)
			}
			store.records = append(
				store.records,
				make([][]byte, int(recordID)+1-len(store.records))...,
			)
		}
		store.records[recordID] = data
		return 0, r.syncKTFDatabase(store)
	case "deleteDataBase(Ljava/lang/String;)V":
		nameAddress, err := r.parameter(1)
		if err != nil {
			return 0, err
		}
		databaseName := r.javaText(nameAddress)
		if serviceID := r.databaseServices[databaseName]; serviceID != 0 {
			if err := r.services.Storage.DeleteRecordStore(
				r.serviceOwner,
				serviceID,
			); err != nil {
				return 0, err
			}
			delete(r.databaseServices, databaseName)
		}
		delete(r.databaseStores, databaseName)
		return 0, nil
	default:
		return 0, nil
	}
}

func (r *ktfRuntime) syncKTFDatabase(store *ktfDatabase) error {
	if store == nil || store.name == "" {
		return fmt.Errorf("KTF database metadata is invalid")
	}
	serviceID := r.databaseServices[store.name]
	if serviceID == 0 {
		var err error
		serviceID, err = r.services.Storage.CreateRecordStore(
			r.serviceOwner,
			store.name,
		)
		if err != nil {
			return err
		}
		r.databaseServices[store.name] = serviceID
	}
	records := make(map[uint32][]byte, len(store.records))
	for recordID, data := range store.records {
		records[uint32(recordID)] = data
	}
	return r.services.Storage.ReplaceRecords(
		r.serviceOwner,
		serviceID,
		max(uint32(1), uint32(len(records))),
		records,
	)
}

func (r *ktfRuntime) databaseParameter(index uint32) (*ktfDatabase, error) {
	instance, err := r.parameter(index)
	if err != nil {
		return nil, err
	}
	store := r.databases[instance]
	if store == nil {
		return nil, fmt.Errorf("KTF database instance 0x%08x is unknown", instance)
	}
	return store, nil
}

func (r *ktfRuntime) readJavaByteArray(instance uint32) ([]byte, error) {
	if instance == 0 {
		return nil, errors.New("KTF Java byte array is null")
	}
	fields, err := r.readU32(instance)
	if err != nil {
		return nil, err
	}
	length, err := r.readU32(fields + 4)
	if err != nil {
		return nil, err
	}
	return r.readJavaByteArrayRange(instance, 0, length)
}

func (r *ktfRuntime) readJavaByteArrayRange(
	instance, offset, count uint32,
) ([]byte, error) {
	if instance == 0 {
		return nil, errors.New("KTF Java byte array is null")
	}
	fields, err := r.readU32(instance)
	if err != nil {
		return nil, err
	}
	length, err := r.readU32(fields + 4)
	if err != nil {
		return nil, err
	}
	if offset > length || count > length-offset {
		return nil, fmt.Errorf(
			"KTF Java byte array range %d..%d exceeds length %d",
			offset,
			uint64(offset)+uint64(count),
			length,
		)
	}
	data := make([]byte, count)
	if err := r.cpu.ReadMemory(fields+8+offset, data); err != nil {
		return nil, err
	}
	return data, nil
}

func (r *ktfRuntime) writeJavaByteArrayRange(
	instance, offset uint32,
	data []byte,
) error {
	if instance == 0 {
		return errors.New("KTF Java byte array is null")
	}
	fields, err := r.readU32(instance)
	if err != nil {
		return err
	}
	length, err := r.readU32(fields + 4)
	if err != nil {
		return err
	}
	if offset > length ||
		uint64(offset)+uint64(len(data)) > uint64(length) {
		return fmt.Errorf(
			"KTF Java byte array range %d..%d exceeds length %d",
			offset,
			uint64(offset)+uint64(len(data)),
			length,
		)
	}
	return r.cpu.WriteMemory(fields+8+offset, data)
}

func (r *ktfRuntime) newJavaByteArray(data []byte) (uint32, error) {
	if uint64(len(data)) > uint64(^uint32(0)) {
		return 0, errors.New("KTF Java byte array exceeds uint32")
	}
	instance, err := r.newJavaArray("[B", uint32(len(data)), 1)
	if err != nil {
		return 0, err
	}
	fields, err := r.readU32(instance)
	if err != nil {
		return 0, err
	}
	if err := r.cpu.WriteMemory(fields+8, data); err != nil {
		return 0, err
	}
	return instance, nil
}

func (r *ktfRuntime) initializeCard(instance, display uint32) error {
	if instance == 0 {
		return errors.New("initialize WIPI Card: instance is null")
	}
	if display == 0 {
		var err error
		display, err = r.ensureDefaultDisplay()
		if err != nil {
			return err
		}
	}
	if err := r.writeJavaFieldWord(instance, 4, display); err != nil {
		return err
	}
	if err := r.writeJavaFieldWord(instance, 16, r.displayWidth()); err != nil {
		return err
	}
	return r.writeJavaFieldWord(instance, 20, r.defaultCardHeight())
}

// displayWidth and displayHeight report the screen the title actually runs on.
// KTF descriptors may name a smaller handset than the default, and a Clet that
// asks for the card size has to be told the same one the framebuffer uses.
func (r *ktfRuntime) displayWidth() uint32 {
	if r.frame != nil {
		return uint32(r.frame.Bounds().Dx())
	}
	return ktfDisplayWidth
}

func (r *ktfRuntime) displayHeight() uint32 {
	if r.frame != nil {
		return uint32(r.frame.Bounds().Dy())
	}
	return ktfDisplayHeight
}

func (r *ktfRuntime) defaultCardHeight() uint32 {
	height := r.displayHeight()
	for _, state := range r.lwcComponents {
		if state.annunciator && state.shown && !state.transparent {
			if height > uint32(ktfAnnunciatorHeight) {
				return height - uint32(ktfAnnunciatorHeight)
			}
			return height
		}
	}
	return height
}

func (r *ktfRuntime) readJavaFieldWord(instance, offset uint32) (uint32, error) {
	if instance == 0 {
		return 0, errors.New("read KTF Java field: instance is null")
	}
	fields, err := r.readU32(instance)
	if err != nil {
		return 0, err
	}
	return r.readU32(fields + 4 + offset)
}

func (r *ktfRuntime) writeJavaFieldWord(instance, offset, value uint32) error {
	if instance == 0 {
		return errors.New("write KTF Java field: instance is null")
	}
	fields, err := r.readU32(instance)
	if err != nil {
		return err
	}
	return r.writeU32(fields+4+offset, value)
}

func ktfJavaJump(argumentCount uint32) ktfHostHandler {
	return func(ctx context.Context, runtime *ktfRuntime) (uint32, error) {
		args := make([]uint32, 3)
		for index := uint32(0); index < argumentCount; index++ {
			value, err := runtime.parameter(index)
			if err != nil {
				return 0, err
			}
			args[index] = value
		}
		procedure, err := runtime.parameter(argumentCount)
		if err != nil {
			return 0, err
		}
		if procedure == 0 {
			return 0, errors.New("Java jump target is null")
		}
		lr, _ := runtime.cpu.ReadRegister(cpu.RegisterLR)
		runtime.tracef(
			"java_jump_%d:target=0x%08x:args=%08x:lr=0x%08x",
			argumentCount,
			procedure,
			args,
			lr,
		)
		if host, ok := runtime.hostCalls[procedure&^1]; ok {
			runtime.trace(host.name)
			value, err := host.handler(ctx, runtime)
			if err != nil {
				return 0, fmt.Errorf(
					"jump to Java host call %s at 0x%08x: %w",
					host.name,
					procedure,
					err,
				)
			}
			if strings.HasPrefix(host.name, "java.method.") {
				runtime.lastJavaReturn = value
			}
			runtime.lastJavaJump = value
			return value, nil
		}
		result, value, err := runtime.call(
			ctx,
			procedure,
			args,
			ktfBootstrapInstructionMax,
		)
		if err != nil {
			return 0, fmt.Errorf(
				"jump to 0x%08x stopped at PC 0x%08x after %d instructions: %w",
				procedure,
				result.PC,
				result.Instructions,
				err,
			)
		}
		runtime.lastJavaJump = value
		return value, nil
	}
}

func ktfRegisterJavaClass(ctx context.Context, runtime *ktfRuntime) (uint32, error) {
	class, err := runtime.parameter(0)
	if err != nil {
		return 0, err
	}
	words, err := runtime.readWords(class, 5)
	if err != nil {
		return 0, err
	}
	descriptor, err := runtime.readWords(words[2], 9)
	if err != nil {
		return 0, err
	}
	name, err := runtime.readCString(descriptor[0], 1024)
	if err != nil {
		return 0, err
	}
	runtime.rememberRegisteredJavaClass(name, class)
	inspected, inspectErr := runtime.inspectJavaClass(class)
	if inspectErr != nil {
		return 0, inspectErr
	}
	if runtime.hostJavaClass[class] {
		if err := runtime.implementBodylessPlatformMethods(inspected); err != nil {
			return 0, err
		}
		inspected, inspectErr = runtime.inspectJavaClass(class)
		if inspectErr != nil {
			return 0, inspectErr
		}
	}
	if inspected.VTable != 0 {
		runtime.javaVTableClasses[inspected.VTable] = inspected.Address
	}
	methods := make([]string, 0, len(inspected.Methods))
	for _, method := range inspected.Methods {
		methods = append(
			methods,
			fmt.Sprintf(
				"%s%s@0x%08x#%04x",
				method.Name,
				method.Descriptor,
				method.Body,
				method.AccessFlags,
			),
		)
	}
	runtime.tracef(
		"java_register_class:%s:class=0x%08x:parent=0x%08x:fields=%d:methods=%v",
		name,
		class,
		inspected.Parent,
		inspected.FieldSize,
		methods,
	)
	// KTF AOT images sometimes register a class while another class
	// initializer is still wiring the objects that it references. Loading
	// must remain non-initializing in that case; leave the class pending so
	// new/getstatic/invokestatic can retry at the first active use.
	if err := runtime.ensureJavaClassInitialized(ctx, inspected); err != nil {
		runtime.tracef("java_class_initialization_deferred:%s:%v", inspected.Name, err)
	}
	return 0, nil
}

func (r *ktfRuntime) rememberRegisteredJavaClass(name string, class uint32) {
	r.javaClasses[name] = class
	r.javaClassGeneration++
	if strings.HasPrefix(name, "java/") ||
		strings.HasPrefix(name, "javax/") ||
		strings.HasPrefix(name, "org/kwis/") {
		// These namespaces are platform-owned. Carrier libraries frequently
		// register declarations with null bodies and expect the VM to supply
		// their concrete implementations.
		r.hostJavaClass[class] = true
	}
}

func (r *ktfRuntime) implementBodylessPlatformMethods(class ktfJavaClass) error {
	for _, method := range class.Methods {
		if method.Body != 0 || method.NativeBody != 0 {
			continue
		}
		stub := r.registerHostCall(
			fmt.Sprintf(
				"java.method.%s.%s%s",
				class.Name,
				method.Name,
				method.Descriptor,
			),
			ktfHostJavaMethod(class.Name, method.Name, method.Descriptor),
		)
		offset := uint32(0)
		if method.AccessFlags&0x0100 != 0 {
			offset = 8
		}
		if err := r.writeU32(method.Address+offset, stub); err != nil {
			return err
		}
		r.tracef(
			"java_platform_method:%s.%s%s@0x%08x",
			class.Name,
			method.Name,
			method.Descriptor,
			stub,
		)
	}
	return nil
}

func ktfRegisterJavaString(_ context.Context, runtime *ktfRuntime) (uint32, error) {
	address, err := runtime.parameter(0)
	if err != nil {
		return 0, err
	}
	length, err := runtime.parameter(1)
	if err != nil {
		return 0, err
	}
	if length == ^uint32(0) {
		var encodedLength [2]byte
		if err := runtime.cpu.ReadMemory(address, encodedLength[:]); err != nil {
			return 0, err
		}
		length = uint32(binary.LittleEndian.Uint16(encodedLength[:]))
		address += 2
	}
	if length > 1<<20 {
		return 0, fmt.Errorf("Java string length %d exceeds limit", length)
	}
	encoded := make([]byte, int(length)*2)
	if err := runtime.cpu.ReadMemory(address, encoded); err != nil {
		return 0, err
	}
	codeUnits := make([]uint16, length)
	for index := range codeUnits {
		codeUnits[index] = binary.LittleEndian.Uint16(encoded[index*2:])
	}
	value := string(utf16.Decode(codeUnits))
	instance, err := runtime.newJavaString(value)
	if err != nil {
		return 0, err
	}
	runtime.trace("java_register_string:" + value)
	return instance, nil
}

func (r *ktfRuntime) newJavaInstance(className string, fieldSize uint32) (uint32, error) {
	classAddress, err := r.ensureJavaClass(className)
	if err != nil {
		return 0, err
	}
	class, err := r.inspectJavaClass(classAddress)
	if err != nil {
		return 0, err
	}
	if fieldSize > uint32(class.FieldSize) {
		class.FieldSize = uint16(fieldSize)
	}
	return r.newJavaInstanceForClass(class)
}

func ktfNoop(context.Context, *ktfRuntime) (uint32, error) {
	return 0, nil
}

func ktfKernelSprintk(
	_ context.Context,
	runtime *ktfRuntime,
) (uint32, error) {
	destination, err := runtime.parameter(0)
	if err != nil {
		return 0, err
	}
	formatAddress, err := runtime.parameter(1)
	if err != nil {
		return 0, err
	}
	if destination == 0 || formatAddress == 0 {
		return 0, nil
	}
	format, err := runtime.readCString(formatAddress, 4096)
	if err != nil {
		return 0, err
	}
	formatted, err := runtime.formatWIPICString(format, 2)
	if err != nil {
		return 0, err
	}
	if len(formatted) > 64<<10 {
		return 0, errors.New("KTF sprintk output exceeds 64 KiB")
	}
	if err := runtime.cpu.WriteMemory(
		destination,
		append([]byte(formatted), 0),
	); err != nil {
		return 0, err
	}
	runtime.tracef("wipic_sprintk:format=%q:result=%q", format, formatted)
	return uint32(len(formatted)), nil
}

func (r *ktfRuntime) formatWIPICString(
	format string,
	argumentIndex uint32,
) (string, error) {
	var output strings.Builder
	for offset := 0; offset < len(format); {
		if format[offset] != '%' {
			output.WriteByte(format[offset])
			offset++
			continue
		}
		offset++
		if offset < len(format) && format[offset] == '%' {
			output.WriteByte('%')
			offset++
			continue
		}

		flagsStart := offset
		for offset < len(format) &&
			strings.ContainsRune("-+ #0", rune(format[offset])) {
			offset++
		}
		flags := format[flagsStart:offset]
		width := ""
		if offset < len(format) && format[offset] == '*' {
			value, err := r.parameter(argumentIndex)
			if err != nil {
				return "", err
			}
			argumentIndex++
			signed := int32(value)
			if signed < 0 {
				if !strings.Contains(flags, "-") {
					flags += "-"
				}
				signed = -signed
			}
			width = strconv.FormatInt(int64(signed), 10)
			offset++
		} else {
			widthStart := offset
			for offset < len(format) &&
				format[offset] >= '0' && format[offset] <= '9' {
				offset++
			}
			width = format[widthStart:offset]
		}
		precision := ""
		if offset < len(format) && format[offset] == '.' {
			offset++
			if offset < len(format) && format[offset] == '*' {
				value, err := r.parameter(argumentIndex)
				if err != nil {
					return "", err
				}
				argumentIndex++
				if int32(value) >= 0 {
					precision = "." + strconv.FormatUint(uint64(value), 10)
				}
				offset++
			} else {
				precisionStart := offset
				for offset < len(format) &&
					format[offset] >= '0' && format[offset] <= '9' {
					offset++
				}
				precision = "." + format[precisionStart:offset]
			}
		}
		length := ""
		if offset < len(format) && strings.ContainsRune("hljztL", rune(format[offset])) {
			length = string(format[offset])
			offset++
			if offset < len(format) &&
				(format[offset] == 'h' && length == "h" ||
					format[offset] == 'l' && length == "l") {
				length += string(format[offset])
				offset++
			}
		}
		if offset >= len(format) {
			output.WriteByte('%')
			break
		}
		verb := format[offset]
		offset++
		if verb == 'n' {
			address, err := r.parameter(argumentIndex)
			if err != nil {
				return "", err
			}
			argumentIndex++
			if address != 0 {
				if err := r.writeU32(address, uint32(output.Len())); err != nil {
					return "", err
				}
			}
			continue
		}

		if length == "ll" || length == "j" {
			if argumentIndex&1 != 0 {
				argumentIndex++
			}
			low, err := r.parameter(argumentIndex)
			if err != nil {
				return "", err
			}
			high, err := r.parameter(argumentIndex + 1)
			if err != nil {
				return "", err
			}
			argumentIndex += 2
			value := uint64(high)<<32 | uint64(low)
			goVerb := verb
			var argument any = value
			if verb == 'd' || verb == 'i' {
				goVerb = 'd'
				argument = int64(value)
			} else if verb == 'u' {
				goVerb = 'd'
			}
			output.WriteString(fmt.Sprintf(
				"%"+flags+width+precision+string(goVerb),
				argument,
			))
			continue
		}

		value, err := r.parameter(argumentIndex)
		if err != nil {
			return "", err
		}
		argumentIndex++
		goFormat := "%" + flags + width + precision
		switch verb {
		case 's':
			text := "(null)"
			if value != 0 {
				text, err = r.readCString(value, 64<<10)
				if err != nil {
					return "", err
				}
			}
			output.WriteString(fmt.Sprintf(goFormat+"s", text))
		case 'd', 'i':
			output.WriteString(fmt.Sprintf(goFormat+"d", int32(value)))
		case 'u':
			output.WriteString(fmt.Sprintf(goFormat+"d", value))
		case 'x', 'X', 'o':
			output.WriteString(fmt.Sprintf(goFormat+string(verb), value))
		case 'c':
			output.WriteString(fmt.Sprintf(goFormat+"c", rune(value)))
		case 'p':
			if !strings.Contains(flags, "#") {
				flags += "#"
			}
			output.WriteString(fmt.Sprintf(
				"%"+flags+width+precision+"x",
				value,
			))
		default:
			output.WriteByte('%')
			output.WriteString(flags)
			output.WriteString(width)
			output.WriteString(precision)
			output.WriteString(length)
			output.WriteByte(verb)
		}
	}
	return output.String(), nil
}

func ktfKernelAllocate(clear bool) ktfHostHandler {
	return func(_ context.Context, runtime *ktfRuntime) (uint32, error) {
		size, err := runtime.parameter(0)
		if err != nil {
			return 0, err
		}
		return runtime.allocateWIPICMemory(size, clear)
	}
}

// allocateWIPICMemory creates the two-level buffer shape returned by KTF's
// kernel allocator and by provider APIs such as MC_grpEncodeImage. Clet support
// code dereferences the returned ID to find the INDIRECT_BUF_HEAD, then uses
// the payload at head+8.
func (r *ktfRuntime) allocateWIPICMemory(size uint32, clear bool) (uint32, error) {
	if size == 0 {
		size = 1
	}
	if size > ^uint32(0)-8 {
		return 0, errors.New("KTF kernel allocation size overflows")
	}
	base, err := r.heap.allocate(size+8, clear)
	if err != nil || base == 0 {
		r.tracef(
			"wipic_memory_alloc_failed:size=%d:clear=%t:error=%v",
			size,
			clear,
			err,
		)
		return 0, err
	}
	data := base + 8
	memoryID, err := r.allocateWords(2)
	if err != nil {
		r.heap.release(base)
		return 0, err
	}
	if err := r.writeWords(memoryID, []uint32{base, size}); err != nil {
		r.heap.release(base)
		r.heap.release(memoryID)
		return 0, err
	}
	r.wipicMemory[memoryID] = ktfWIPICMemory{
		base: base,
		data: data,
		size: size,
	}
	r.tracef(
		"wipic_memory_alloc:id=0x%08x:base=0x%08x:data=0x%08x:size=%d:clear=%t",
		memoryID,
		base,
		data,
		size,
		clear,
	)
	return memoryID, nil
}

func ktfKernelGetDLLInterface(
	_ context.Context,
	runtime *ktfRuntime,
) (uint32, error) {
	nameAddress, err := runtime.parameter(0)
	if err != nil {
		return 0, err
	}
	if nameAddress == 0 {
		return 0, nil
	}
	name, err := runtime.readCString(nameAddress, 256)
	if err != nil {
		return 0, err
	}
	requestedMajor, err := runtime.parameter(1)
	if err != nil {
		return 0, err
	}
	requestedMinor, err := runtime.parameter(2)
	if err != nil {
		return 0, err
	}
	returnMajor, err := runtime.parameter(3)
	if err != nil {
		return 0, err
	}
	returnMinor, err := runtime.parameter(4)
	if err != nil {
		return 0, err
	}
	runtime.tracef(
		"wipic_knl_get_dll_interface:%s:%d.%d",
		name,
		int32(requestedMajor),
		int32(requestedMinor),
	)

	const (
		interfaceMajor = uint32(1)
		interfaceMinor = uint32(0)
	)
	majorMatches := int32(interfaceMajor) > int32(requestedMajor) ||
		interfaceMajor == requestedMajor &&
			int32(interfaceMinor) >= int32(requestedMinor)
	if !majorMatches {
		return 0, nil
	}
	address, err := runtime.lookupInterface(name)
	if err != nil || address == 0 {
		return address, err
	}
	if returnMajor != 0 {
		if err := runtime.writeU32(returnMajor, interfaceMajor); err != nil {
			return 0, err
		}
	}
	if returnMinor != 0 {
		if err := runtime.writeU32(returnMinor, interfaceMinor); err != nil {
			return 0, err
		}
	}
	return address, nil
}

func ktfIncrementalMemoryAdd(
	_ context.Context,
	runtime *ktfRuntime,
) (uint32, error) {
	base, err := runtime.parameter(0)
	if err != nil {
		return 0, err
	}
	size, err := runtime.parameter(1)
	if err != nil {
		return 0, err
	}
	if base == 0 || size == 0 ||
		uint64(base)+uint64(size) > uint64(^uint32(0))+1 {
		return 0, fmt.Errorf(
			"invalid KTF incremental memory region 0x%08x+0x%x",
			base,
			size,
		)
	}
	var probe [1]byte
	if err := runtime.cpu.ReadMemory(base, probe[:]); err != nil {
		return 0, fmt.Errorf("read KTF incremental memory region start: %w", err)
	}
	if err := runtime.cpu.ReadMemory(base+size-1, probe[:]); err != nil {
		return 0, fmt.Errorf("read KTF incremental memory region end: %w", err)
	}
	start, end := uint64(base), uint64(base)+uint64(size)
	for _, region := range runtime.incrementalMemory {
		if region.base == base && region.size == size {
			return 0, nil
		}
		regionStart := uint64(region.base)
		regionEnd := regionStart + uint64(region.size)
		if start < regionEnd && regionStart < end {
			return 0, fmt.Errorf(
				"KTF incremental memory region 0x%08x+0x%x overlaps 0x%08x+0x%x",
				base,
				size,
				region.base,
				region.size,
			)
		}
	}
	runtime.incrementalMemory = append(
		runtime.incrementalMemory,
		ktfIncrementalMemoryRegion{base: base, size: size},
	)
	if runtime.incrementalHeaps == nil {
		runtime.incrementalHeaps = make(map[uint32]*guestHeap)
	}
	heap := newGuestHeap(runtime.cpu, base, size)
	runtime.incrementalHeaps[base] = &heap
	runtime.tracef(
		"mx_user_mem_add:base=0x%08x:size=%d",
		base,
		size,
	)
	return 0, nil
}

func ktfIncrementalMemoryAllocate(
	_ context.Context,
	runtime *ktfRuntime,
) (uint32, error) {
	base, err := runtime.parameter(0)
	if err != nil {
		return 0, err
	}
	size, err := runtime.parameter(1)
	if err != nil {
		return 0, err
	}
	heap := runtime.incrementalHeaps[base]
	if heap == nil {
		return 0, fmt.Errorf(
			"KTF user-memory arena 0x%08x is not registered",
			base,
		)
	}
	address, err := heap.allocate(size, false)
	if err != nil {
		return 0, err
	}
	runtime.tracef(
		"mx_user_mem_alloc:base=0x%08x:size=%d:address=0x%08x",
		base,
		size,
		address,
	)
	return address, nil
}

func ktfIncrementalMemoryReallocate(
	_ context.Context,
	runtime *ktfRuntime,
) (uint32, error) {
	base, err := runtime.parameter(0)
	if err != nil {
		return 0, err
	}
	address, err := runtime.parameter(1)
	if err != nil {
		return 0, err
	}
	size, err := runtime.parameter(2)
	if err != nil {
		return 0, err
	}
	heap := runtime.incrementalHeaps[base]
	if heap == nil {
		return 0, fmt.Errorf(
			"KTF user-memory arena 0x%08x is not registered",
			base,
		)
	}
	if address == 0 {
		return heap.allocate(size, false)
	}
	if size == 0 {
		heap.release(address)
		return 0, nil
	}
	oldSize, ok := heap.allocations[address]
	if !ok {
		return 0, fmt.Errorf(
			"KTF user-memory address 0x%08x is not allocated",
			address,
		)
	}
	replacement, err := heap.allocate(size, false)
	if err != nil || replacement == 0 {
		return replacement, err
	}
	copySize := min(oldSize, size)
	data := make([]byte, copySize)
	if err := runtime.cpu.ReadMemory(address, data); err != nil {
		heap.release(replacement)
		return 0, err
	}
	if err := runtime.cpu.WriteMemory(replacement, data); err != nil {
		heap.release(replacement)
		return 0, err
	}
	heap.release(address)
	runtime.tracef(
		"mx_user_mem_realloc:base=0x%08x:address=0x%08x:"+
			"size=%d:replacement=0x%08x",
		base,
		address,
		size,
		replacement,
	)
	return replacement, nil
}

func ktfIncrementalMemoryFree(
	_ context.Context,
	runtime *ktfRuntime,
) (uint32, error) {
	base, err := runtime.parameter(0)
	if err != nil {
		return 0, err
	}
	address, err := runtime.parameter(1)
	if err != nil {
		return 0, err
	}
	heap := runtime.incrementalHeaps[base]
	if heap == nil {
		return 0, fmt.Errorf(
			"KTF user-memory arena 0x%08x is not registered",
			base,
		)
	}
	if address != 0 && !heap.release(address) {
		return 0, fmt.Errorf(
			"KTF user-memory address 0x%08x is not allocated",
			address,
		)
	}
	runtime.tracef(
		"mx_user_mem_free:base=0x%08x:address=0x%08x",
		base,
		address,
	)
	return 0, nil
}

func ktfKernelFree(_ context.Context, runtime *ktfRuntime) (uint32, error) {
	memoryID, err := runtime.parameter(0)
	if err != nil {
		return 0, err
	}
	if memoryID == 0 {
		return 0, nil
	}
	if allocation, ok := runtime.wipicMemory[memoryID]; ok {
		runtime.heap.release(allocation.base)
		runtime.heap.release(memoryID)
		delete(runtime.wipicMemory, memoryID)
		// MC_knlFree is specified as void, but KTF's ARM provider leaves a
		// non-zero allocation word in r0. Some carrier Clet support libraries
		// tail-return that value and use it as a success predicate before
		// completing their graphics initialization. Preserve the non-zero
		// memory ID rather than synthesizing zero for the void result.
		return memoryID, nil
	}
	// A few mixed Java/C clients pass a direct bridge allocation here.
	runtime.heap.release(memoryID)
	return memoryID, nil
}

func ktfTotalMemory(context.Context, *ktfRuntime) (uint32, error) {
	return guestHeapSize, nil
}

func ktfFreeMemory(_ context.Context, runtime *ktfRuntime) (uint32, error) {
	var available uint64
	for _, block := range runtime.heap.root().free {
		available += uint64(block.size)
	}
	if available > uint64(^uint32(0)) {
		available = uint64(^uint32(0))
	}
	return uint32(available), nil
}

func ktfKernelDefineTimer(
	_ context.Context,
	runtime *ktfRuntime,
) (uint32, error) {
	address, err := runtime.parameter(0)
	if err != nil {
		return 0, err
	}
	callback, err := runtime.parameter(1)
	if err != nil {
		return 0, err
	}
	if address == 0 {
		return ^uint32(7), nil
	}
	timer := runtime.wipicTimers[address]
	if timer == nil {
		timer = &ktfWIPICTimer{}
		runtime.wipicTimers[address] = timer
	}
	serviceID := runtime.wipicTimerServices[address]
	if serviceID == 0 {
		serviceID, err = runtime.services.Timers.Define(
			runtime.serviceOwner,
			fmt.Sprintf("ktf.wipic.timer.%08x", address),
		)
		if err != nil {
			return 0, err
		}
		runtime.wipicTimerServices[address] = serviceID
	} else if err := runtime.services.Timers.Cancel(
		serviceID,
		runtime.serviceOwner,
	); err != nil {
		return 0, err
	}
	timer.callback = callback
	timer.active = false
	runtime.tracef(
		"wipic_timer_define:timer=0x%08x:callback=0x%08x",
		address,
		callback,
	)
	return 0, nil
}

func ktfKernelSetTimer(
	_ context.Context,
	runtime *ktfRuntime,
) (uint32, error) {
	address, err := runtime.parameter(0)
	if err != nil {
		return 0, err
	}
	timeoutLow, err := runtime.parameter(1)
	if err != nil {
		return 0, err
	}
	timeoutHigh, err := runtime.parameter(2)
	if err != nil {
		return 0, err
	}
	parameter, err := runtime.parameter(3)
	if err != nil {
		return 0, err
	}
	timer := runtime.wipicTimers[address]
	if address == 0 || timer == nil || timer.callback == 0 {
		return ^uint32(7), nil
	}
	timeout := uint64(timeoutHigh)<<32 | uint64(timeoutLow)
	maxMillis := uint64((time.Duration(1<<63 - 1)) / time.Millisecond)
	if timeout > maxMillis || runtime.tickMS > maxMillis-timeout {
		return ^uint32(7), nil
	}
	timer.parameter = parameter
	timer.deadline = runtime.tickMS + timeout
	timer.active = true
	serviceID := runtime.wipicTimerServices[address]
	if serviceID == 0 {
		return ^uint32(7), nil
	}
	if err := runtime.services.Timers.Set(
		serviceID,
		runtime.serviceOwner,
		time.Duration(timer.deadline)*time.Millisecond,
		0,
		int64(address),
	); err != nil {
		timer.active = false
		return 0, err
	}
	runtime.tracef(
		"wipic_timer_set:timer=0x%08x:timeout=%d:parameter=0x%08x:deadline=%d",
		address,
		timeout,
		parameter,
		timer.deadline,
	)
	return 0, nil
}

func ktfKernelUnsetTimer(
	_ context.Context,
	runtime *ktfRuntime,
) (uint32, error) {
	address, err := runtime.parameter(0)
	if err != nil {
		return 0, err
	}
	if timer := runtime.wipicTimers[address]; timer != nil {
		timer.active = false
	}
	if serviceID := runtime.wipicTimerServices[address]; serviceID != 0 {
		if err := runtime.services.Timers.Cancel(
			serviceID,
			runtime.serviceOwner,
		); err != nil {
			return 0, err
		}
	}
	return 0, nil
}

func ktfKernelCurrentTime(
	_ context.Context,
	runtime *ktfRuntime,
) (uint32, error) {
	if err := runtime.cpu.WriteRegister(
		cpu.RegisterR1,
		uint32(runtime.tickMS>>32),
	); err != nil {
		return 0, err
	}
	return uint32(runtime.tickMS), nil
}

func ktfKernelGetSystemProperty(
	_ context.Context,
	runtime *ktfRuntime,
) (uint32, error) {
	keyAddress, err := runtime.parameter(0)
	if err != nil {
		return 0, err
	}
	output, err := runtime.parameter(1)
	if err != nil {
		return 0, err
	}
	capacity, err := runtime.parameter(2)
	if err != nil {
		return 0, err
	}
	if keyAddress == 0 || output == 0 || capacity == 0 {
		return ktfWIPICErrorInvalid, nil
	}
	key, err := runtime.readCString(keyAddress, 256)
	if err != nil {
		return 0, err
	}
	value, supported := runtime.wipicSystemProperty(key)
	runtime.tracef(
		"wipic_system_property_get:%s=%q:supported=%t",
		key,
		value,
		supported,
	)
	if !supported {
		return ^uint32(6), nil
	}
	if uint64(len(value))+1 > uint64(capacity) {
		return ktfWIPICErrorShortBuf, nil
	}
	if err := runtime.cpu.WriteMemory(
		output,
		append([]byte(value), 0),
	); err != nil {
		return 0, err
	}
	return 0, nil
}

func ktfKernelSetSystemProperty(
	_ context.Context,
	runtime *ktfRuntime,
) (uint32, error) {
	keyAddress, err := runtime.parameter(0)
	if err != nil {
		return 0, err
	}
	valueAddress, err := runtime.parameter(1)
	if err != nil {
		return 0, err
	}
	if keyAddress == 0 || valueAddress == 0 {
		return ktfWIPICErrorInvalid, nil
	}
	key, err := runtime.readCString(keyAddress, 256)
	if err != nil {
		return 0, err
	}
	value, err := runtime.readCString(valueAddress, 4096)
	if err != nil {
		return 0, err
	}
	runtime.wipicSystemProperties[strings.ToUpper(strings.TrimSpace(key))] = value
	runtime.tracef("wipic_system_property_set:%s=%q", key, value)
	return 0, nil
}

func (r *ktfRuntime) wipicSystemProperty(key string) (string, bool) {
	key = strings.ToUpper(strings.TrimSpace(key))
	if value, ok := r.wipicSystemProperties[key]; ok {
		return value, true
	}
	if value := r.handsetSystemProperty(key); value != "" {
		return value, true
	}
	switch key {
	case "ESN":
		return "00000000", true
	case "NID", "SID", "BASEID", "BASELAT", "BASELONG", "CURRENTCH":
		return "0", true
	case "PHONENUMBER":
		return r.services.Device.Config().PhoneNumber, true
	case "WIPIVERSION":
		return r.services.Device.Config().WIPIVersion, true
	case "RSSILEVEL":
		_, signal, _ := r.services.Device.Status()
		return strconv.Itoa(int(signal) * 5 / 100), true
	case "BATTERYLEVEL":
		battery, _, _ := r.services.Device.Status()
		return strconv.Itoa(int(battery) * 5 / 100), true
	case "MAXRSSILEVEL", "MAXBATTLEVEL":
		return "5", true
	case "MAXSERIALNUM":
		return "0", true
	case "MAXSOCKETNUM":
		return strconv.FormatUint(
			uint64(r.services.Config.Limits.Network.MaxSockets),
			10,
		), true
	case "MEDIADEVICES":
		return "audio/MIDI,audio/MP3", true
	case "DNS":
		return "127.0.0.1", true
	case "TIMEZONE":
		minutes := r.services.Device.Config().TimezoneMins
		sign := "+"
		if minutes < 0 {
			sign = "-"
			minutes = -minutes
		}
		return fmt.Sprintf(
			"GMT%s%02d:%02d",
			sign,
			minutes/60,
			minutes%60,
		), true
	case "KEYREPEAT":
		return "600:250", true
	case "VIBRATORLEVEL":
		return "1", true
	case "VOLUMELEVEL":
		return "10", true
	default:
		return "", false
	}
}

func ktfGetResourceID(
	_ context.Context,
	runtime *ktfRuntime,
) (uint32, error) {
	nameAddress, err := runtime.parameter(0)
	if err != nil {
		return 0, err
	}
	sizeAddress, err := runtime.parameter(1)
	if err != nil {
		return 0, err
	}
	if nameAddress == 0 || sizeAddress == 0 {
		return ^uint32(7), nil
	}
	name, err := runtime.readCString(nameAddress, 4096)
	if err != nil {
		return 0, err
	}
	resource, ok := runtime.findKTFResource(name)
	if !ok {
		runtime.trace("wipic_resource_missing:" + name)
		return ^uint32(1), nil
	}
	if err := runtime.writeU32(sizeAddress, uint32(len(resource))); err != nil {
		return 0, err
	}
	key := strings.ToLower(strings.ReplaceAll(path.Clean(name), `\`, "/"))
	id := runtime.wipicResourceIDs[key]
	if id == 0 {
		id = uint32(len(runtime.wipicResources) + 1)
		for runtime.wipicResources[id] != nil {
			id++
		}
		runtime.wipicResourceIDs[key] = id
		runtime.wipicResources[id] = resource
	}
	runtime.tracef(
		"wipic_resource_id:%s:id=%d:size=%d:lr=0x%08x",
		name,
		id,
		len(resource),
		mustKTFRegister(runtime, cpu.RegisterLR),
	)
	return id, nil
}

func ktfGetResource(
	_ context.Context,
	runtime *ktfRuntime,
) (uint32, error) {
	id, err := runtime.parameter(0)
	if err != nil {
		return 0, err
	}
	output, err := runtime.parameter(1)
	if err != nil {
		return 0, err
	}
	size, err := runtime.parameter(2)
	if err != nil {
		return 0, err
	}
	resource := runtime.wipicResources[id]
	if resource == nil || output == 0 {
		return ^uint32(1), nil
	}
	output, size, err = runtime.resolveKTFResourceOutput(output, size)
	if err != nil {
		return 0, err
	}
	if uint64(size) < uint64(len(resource)) {
		return ^uint32(10), nil
	}
	if err := runtime.cpu.WriteMemory(output, resource); err != nil {
		return 0, err
	}
	runtime.tracef(
		"wipic_resource_read:id=%d:size=%d:lr=0x%08x",
		id,
		len(resource),
		mustKTFRegister(runtime, cpu.RegisterLR),
	)
	return 0, nil
}

func mustKTFRegister(runtime *ktfRuntime, register uint32) uint32 {
	value, _ := runtime.cpu.ReadRegister(register)
	return value
}

func (r *ktfRuntime) resolveKTFResourceOutput(
	output, size uint32,
) (uint32, uint32, error) {
	if allocation, ok := r.wipicMemory[output]; ok {
		if size > allocation.size {
			size = allocation.size
		}
		return allocation.data, size, nil
	}
	// KTF's C support library can suballocate an INDIRECT_BUF from the large
	// arena returned by MC_knlCalloc. In that form the resource argument is a
	// one-word handle followed immediately by the two-word buffer head:
	//
	//   handle -> head (= handle+4), head words, payload (= head+8)
	//
	// These handles are invisible to wipicMemory because the guest allocator
	// creates them without another kernel call. A plain resource destination
	// remains valid, so only resolve the carrier library's exact self-relative
	// layout instead of treating every unknown pointer as an indirect handle.
	if output <= ^uint32(0)-4 {
		head, err := r.readU32(output)
		if err != nil {
			return 0, 0, err
		}
		if head == output+4 {
			if head > ^uint32(0)-8 {
				return 0, 0, errors.New(
					"KTF resource indirect buffer address overflows",
				)
			}
			return head + 8, size, nil
		}
	}
	return output, size, nil
}

func ktfGetWIPICInterface(_ context.Context, runtime *ktfRuntime) (uint32, error) {
	return runtime.buildWIPICInterface()
}

func (r *ktfRuntime) buildWIPICInterface() (uint32, error) {
	if r.wipicInterface != 0 {
		return r.wipicInterface, nil
	}
	const (
		// The kernel reserved1 getter returns the 17-entry master vector, not
		// the 18-entry named registration array. Its order is util, misc,
		// graphic, im, db, plugin, fs, serial, uic, media, net, phn, ann,
		// ioDev, termRes, math, ssl.
		interfaceCount = 17
		slotsPerTable  = 64
	)
	interfaces := make([]uint32, interfaceCount)
	for table := range interfaces {
		slots := make([]uint32, slotsPerTable)
		for index := range slots {
			slots[index] = r.registerHostCall(
				fmt.Sprintf("wipic.%d.%d", table, index),
				ktfWIPICHandler(table, index),
			)
		}
		address, err := r.allocateWords(slotsPerTable)
		if err != nil {
			return 0, err
		}
		if err := r.writeWords(address, slots); err != nil {
			return 0, err
		}
		interfaces[table] = address
	}
	address, err := r.allocateWords(interfaceCount)
	if err != nil {
		return 0, err
	}
	if err := r.writeWords(address, interfaces); err != nil {
		return 0, err
	}
	r.wipicInterface = address
	return address, nil
}

const (
	ktfWIPICMasterGraphics = 2
	ktfWIPICMasterFS       = 6
	ktfWIPICMasterMedia    = 9
)

func ktfWIPICHandler(table, slot int) ktfHostHandler {
	if table == ktfWIPICMasterGraphics {
		switch slot {
		case 0:
			return ktfWIPICGraphicsGetImageProperty
		case 1:
			return ktfWIPICGraphicsGetImageFramebuffer
		case 2:
			return ktfWIPICGraphicsGetScreenFramebuffer
		case 3:
			return ktfWIPICGraphicsDestroyOffscreenFramebuffer
		case 4:
			return ktfWIPICGraphicsCreateOffscreenFramebuffer
		case 5:
			return ktfWIPICGraphicsInitContext
		case 6:
			return ktfWIPICGraphicsSetContext
		case 7:
			return ktfWIPICGraphicsGetContext
		case 8:
			return ktfWIPICGraphicsPutPixel
		case 9:
			return ktfWIPICGraphicsDrawLine
		case 10:
			return ktfWIPICGraphicsDrawRect
		case 11:
			return ktfWIPICGraphicsFillRect
		case 12:
			return ktfWIPICGraphicsCopyFramebuffer
		case 13:
			return ktfWIPICGraphicsDrawImage
		case 14:
			return ktfWIPICGraphicsCopyArea
		case 15:
			return ktfWIPICGraphicsDrawArc
		case 16:
			return ktfWIPICGraphicsFillArc
		case 17:
			return ktfWIPICGraphicsDrawString
		case 18:
			return ktfWIPICGraphicsDrawUnicodeString
		case 19:
			return ktfWIPICGraphicsGetRGBPixels
		case 20:
			return ktfWIPICGraphicsSetRGBPixels
		case 21:
			return ktfWIPICGraphicsFlushLCD
		case 22:
			return ktfWIPICGraphicsGetPixelFromRGB
		case 23:
			return ktfWIPICGraphicsGetRGBFromPixel
		case 24:
			return ktfWIPICGraphicsGetDisplayInfo
		case 25:
			return ktfWIPICGraphicsRepaint
		case 26:
			return ktfWIPICGraphicsGetFont
		case 27:
			return ktfWIPICGraphicsGetFontHeight
		case 28:
			return ktfWIPICGraphicsGetFontAscent
		case 29:
			return ktfWIPICGraphicsGetFontDescent
		case 30:
			return ktfWIPICGraphicsGetStringWidth
		case 31:
			return ktfWIPICGraphicsGetUnicodeStringWidth
		case 32:
			return ktfWIPICGraphicsCreateImage
		case 33:
			return ktfWIPICGraphicsDestroyImage
		case 34:
			return ktfWIPICGraphicsDecodeNextImage
		case 35:
			return ktfWIPICGraphicsEncodeImage
		case 36:
			return ktfWIPICGraphicsPostEvent
		case 37:
			return ktfWIPICGraphicsDrawPolygon
		case 38:
			return ktfWIPICGraphicsDrawFillPolygon
		}
	}
	if table == ktfWIPICMasterFS {
		switch slot {
		case 0:
			return ktfWIPICFileOpen
		case 1:
			return ktfWIPICFileRead
		case 2:
			return ktfWIPICFileWrite
		case 3:
			return ktfWIPICFileClose
		case 4:
			return ktfWIPICFileSeek
		case 5:
			return ktfWIPICFileAttribute
		case 6:
			return ktfWIPICFileRemove
		case 7:
			return ktfWIPICFileRename
		case 8:
			return ktfWIPICFileMakeDirectory
		case 9:
			return ktfWIPICFileRemoveDirectory
		case 10:
			return ktfWIPICFileList
		case 11:
			return ktfWIPICFileTotalSpace
		case 12:
			return ktfWIPICFileAvailable
		case 13:
			return ktfWIPICFileSetMode
		case 14:
			return ktfWIPICFileGetCounts
		case 15:
			return ktfWIPICFileTell
		case 16:
			return ktfWIPICFileIsExist
		}
	}
	if table == ktfWIPICMasterMedia {
		switch slot {
		case 0:
			return ktfWIPICMediaCreate
		case 3:
			return ktfWIPICMediaDestroy
		case 4:
			return ktfWIPICMediaPutData
		case 5:
			return ktfWIPICMediaGetData
		case 6:
			return ktfWIPICMediaAvailableDataSize
		case 7:
			return ktfWIPICMediaClearData
		case 8:
			return ktfWIPICMediaPlay
		case 9:
			return ktfWIPICMediaPause
		case 10:
			return ktfWIPICMediaResume
		case 11:
			return ktfWIPICMediaStop
		case 13:
			return ktfWIPICMediaSetPosition
		}
	}
	return ktfWIPICNoop(table, slot)
}

func ktfWIPICMediaCreate(
	_ context.Context,
	runtime *ktfRuntime,
) (uint32, error) {
	mediaTypeAddress, err := runtime.parameter(0)
	if err != nil {
		return 0, err
	}
	capacity, err := runtime.parameter(1)
	if err != nil {
		return 0, err
	}
	callback, err := runtime.parameter(2)
	if err != nil {
		return 0, err
	}
	if mediaTypeAddress == 0 || capacity == 0 ||
		uint64(capacity) > runtime.serviceConfig.Limits.Media.MaxSourceBytes {
		return 0, nil
	}
	mediaType, err := runtime.readCString(mediaTypeAddress, 256)
	if err != nil {
		return 0, err
	}
	serviceMediaType := ""
	switch strings.ToLower(strings.TrimSpace(mediaType)) {
	case "yamaha_ma3", "audio/x-smaf", "audio/smaf", "audio/mmf":
		serviceMediaType = "audio/x-smaf"
	case "audio/midi", "audio/sp-midi":
		serviceMediaType = "audio/midi"
	case "audio/wav", "audio/x-wav":
		serviceMediaType = "audio/wav"
	default:
		return 0, nil
	}
	handle, err := runtime.allocateWords(24)
	if err != nil {
		return 0, err
	}
	serviceID, err := runtime.services.Media.CreateClip(
		runtime.serviceOwner,
		serviceMediaType,
		uint64(capacity),
	)
	if err != nil {
		runtime.heap.release(handle)
		return 0, err
	}
	runtime.wipicMediaClips[handle] = &ktfWIPICMediaClip{
		mediaType: serviceMediaType,
		capacity:  capacity,
		callback:  callback,
		volume:    100,
	}
	runtime.wipicMediaServices[handle] = serviceID
	runtime.tracef(
		"wipic_media_create:handle=0x%08x:type=%s:capacity=%d:"+
			"callback=0x%08x",
		handle,
		mediaType,
		capacity,
		callback,
	)
	return handle, nil
}

func ktfWIPICMediaDestroy(
	_ context.Context,
	runtime *ktfRuntime,
) (uint32, error) {
	handle, clip, serviceID, err := runtime.ktfWIPICMediaParameter()
	if err != nil {
		return ktfWIPICErrorInvalid, err
	}
	if clip == nil {
		return ktfWIPICErrorInvalid, nil
	}
	if err := runtime.services.Media.DestroyClip(
		runtime.serviceOwner,
		serviceID,
		runtime.services.Events,
	); err != nil {
		return 0, err
	}
	delete(runtime.wipicMediaClips, handle)
	delete(runtime.wipicMediaServices, handle)
	runtime.heap.release(handle)
	return 0, nil
}

func ktfWIPICMediaPutData(
	_ context.Context,
	runtime *ktfRuntime,
) (uint32, error) {
	_, clip, serviceID, err := runtime.ktfWIPICMediaParameter()
	if err != nil {
		return ktfWIPICErrorInvalid, err
	}
	if clip == nil {
		return ktfWIPICErrorInvalid, nil
	}
	input, err := runtime.parameter(1)
	if err != nil {
		return 0, err
	}
	count, err := runtime.parameter(2)
	if err != nil {
		return 0, err
	}
	if input == 0 || count > clip.capacity ||
		uint64(len(clip.data))+uint64(count) > uint64(clip.capacity) {
		return ktfWIPICErrorInvalid, nil
	}
	data := make([]byte, count)
	if err := runtime.cpu.ReadMemory(input, data); err != nil {
		return 0, err
	}
	if _, err := runtime.services.Media.Append(
		runtime.serviceOwner,
		serviceID,
		data,
	); err != nil {
		return 0, err
	}
	clip.data = append(clip.data, data...)
	return 0, nil
}

func ktfWIPICMediaGetData(
	_ context.Context,
	runtime *ktfRuntime,
) (uint32, error) {
	_, clip, serviceID, err := runtime.ktfWIPICMediaParameter()
	if err != nil {
		return ktfWIPICErrorInvalid, err
	}
	if clip == nil {
		return ktfWIPICErrorInvalid, nil
	}
	output, err := runtime.parameter(1)
	if err != nil {
		return 0, err
	}
	count, err := runtime.parameter(2)
	if err != nil {
		return 0, err
	}
	count = min(count, uint32(len(clip.data)))
	if output == 0 && count != 0 {
		return ktfWIPICErrorInvalid, nil
	}
	if err := runtime.cpu.WriteMemory(output, clip.data[:count]); err != nil {
		return 0, err
	}
	clip.data = append(clip.data[:0], clip.data[count:]...)
	if err := runtime.services.Media.Clear(
		runtime.serviceOwner,
		serviceID,
	); err != nil {
		return 0, err
	}
	if _, err := runtime.services.Media.Append(
		runtime.serviceOwner,
		serviceID,
		clip.data,
	); err != nil {
		return 0, err
	}
	return count, nil
}

func ktfWIPICMediaAvailableDataSize(
	_ context.Context,
	runtime *ktfRuntime,
) (uint32, error) {
	_, clip, _, err := runtime.ktfWIPICMediaParameter()
	if err != nil {
		return ktfWIPICErrorInvalid, err
	}
	if clip == nil {
		return ktfWIPICErrorInvalid, nil
	}
	return uint32(len(clip.data)), nil
}

func ktfWIPICMediaClearData(
	_ context.Context,
	runtime *ktfRuntime,
) (uint32, error) {
	_, clip, serviceID, err := runtime.ktfWIPICMediaParameter()
	if err != nil {
		return ktfWIPICErrorInvalid, err
	}
	if clip == nil {
		return ktfWIPICErrorInvalid, nil
	}
	if err := runtime.services.Media.Clear(
		runtime.serviceOwner,
		serviceID,
	); err != nil {
		return 0, err
	}
	clip.data = nil
	clip.state = 0
	clip.repeat = false
	return 0, nil
}

func ktfWIPICMediaPlay(
	_ context.Context,
	runtime *ktfRuntime,
) (uint32, error) {
	handle, clip, serviceID, err := runtime.ktfWIPICMediaParameter()
	if err != nil {
		return ktfWIPICErrorInvalid, err
	}
	if clip == nil || len(clip.data) == 0 {
		return ktfWIPICErrorInvalid, nil
	}
	repeat, err := runtime.parameter(1)
	if err != nil {
		return 0, err
	}
	plays := int32(1)
	if repeat != 0 {
		plays = -1
	}
	if err := runtime.services.Media.Play(
		runtime.serviceOwner,
		serviceID,
		plays,
	); err != nil {
		return 0, err
	}
	clip.state = 1
	clip.repeat = repeat != 0
	runtime.tracef(
		"wipic_media_play:handle=0x%08x:size=%d:repeat=%t",
		handle,
		len(clip.data),
		clip.repeat,
	)
	return 0, nil
}

func ktfWIPICMediaPause(
	_ context.Context,
	runtime *ktfRuntime,
) (uint32, error) {
	_, clip, serviceID, err := runtime.ktfWIPICMediaParameter()
	if err != nil {
		return ktfWIPICErrorInvalid, err
	}
	if clip == nil {
		return ktfWIPICErrorInvalid, nil
	}
	if err := runtime.services.Media.Pause(
		runtime.serviceOwner,
		serviceID,
	); err != nil {
		return 0, err
	}
	clip.state = 2
	return 0, nil
}

func ktfWIPICMediaResume(
	_ context.Context,
	runtime *ktfRuntime,
) (uint32, error) {
	_, clip, serviceID, err := runtime.ktfWIPICMediaParameter()
	if err != nil {
		return ktfWIPICErrorInvalid, err
	}
	if clip == nil {
		return ktfWIPICErrorInvalid, nil
	}
	if err := runtime.services.Media.Resume(
		runtime.serviceOwner,
		serviceID,
	); err != nil {
		return 0, err
	}
	clip.state = 1
	return 0, nil
}

func ktfWIPICMediaStop(
	_ context.Context,
	runtime *ktfRuntime,
) (uint32, error) {
	_, clip, serviceID, err := runtime.ktfWIPICMediaParameter()
	if err != nil {
		return ktfWIPICErrorInvalid, err
	}
	if clip == nil {
		return ktfWIPICErrorInvalid, nil
	}
	if err := runtime.services.Media.Stop(
		runtime.serviceOwner,
		serviceID,
	); err != nil {
		return 0, err
	}
	clip.state = 0
	clip.repeat = false
	return 0, nil
}

func ktfWIPICMediaSetPosition(
	_ context.Context,
	runtime *ktfRuntime,
) (uint32, error) {
	_, clip, serviceID, err := runtime.ktfWIPICMediaParameter()
	if err != nil {
		return ktfWIPICErrorInvalid, err
	}
	if clip == nil {
		return ktfWIPICErrorInvalid, nil
	}
	position, err := runtime.parameter(1)
	if err != nil {
		return 0, err
	}
	if err := runtime.services.Media.Seek(
		runtime.serviceOwner,
		serviceID,
		time.Duration(position)*time.Millisecond,
	); err != nil {
		return ktfWIPICErrorInvalid, nil
	}
	return 0, nil
}

func (r *ktfRuntime) ktfWIPICMediaParameter() (
	uint32,
	*ktfWIPICMediaClip,
	shared.ServiceID,
	error,
) {
	handle, err := r.parameter(0)
	if err != nil {
		return 0, nil, 0, err
	}
	return handle, r.wipicMediaClips[handle], r.wipicMediaServices[handle], nil
}

const (
	ktfWIPICFileReadOnly  = uint32(1)
	ktfWIPICFileWriteOnly = uint32(2)
	ktfWIPICFileTruncate  = uint32(4)
	ktfWIPICFileReadWrite = uint32(8)

	ktfWIPICError          = ^uint32(0)
	ktfWIPICErrorBadSeek   = ^uint32(3)
	ktfWIPICErrorInvalid   = ^uint32(8)
	ktfWIPICErrorNoEntry   = ^uint32(11)
	ktfWIPICErrorShortBuf  = ^uint32(17)
	ktfWIPICErrorEOF       = ^uint32(22)
	ktfWIPICErrorBadHandle = ^uint32(100)
)

func ktfWIPICFileOpen(
	_ context.Context,
	runtime *ktfRuntime,
) (uint32, error) {
	nameAddress, err := runtime.parameter(0)
	if err != nil {
		return 0, err
	}
	flag, err := runtime.parameter(1)
	if err != nil {
		return 0, err
	}
	name, err := runtime.readCString(nameAddress, 1024)
	if err != nil {
		return 0, err
	}
	name = normalizeKTFFileName(name)
	openMode := shared.OpenMode(0)
	switch flag {
	case ktfWIPICFileReadOnly:
		openMode = shared.OpenRead
		if _, err := runtime.services.Storage.Stat(
			shared.NamespacePrivate,
			name,
		); err != nil {
			runtime.tracef(
				"wipic_file_open_missing:%s:flag=%d",
				name,
				flag,
			)
			return ktfWIPICErrorNoEntry, nil
		}
	case ktfWIPICFileWriteOnly:
		openMode = shared.OpenWrite | shared.OpenCreate | shared.OpenAppend
	case ktfWIPICFileTruncate:
		openMode = shared.OpenWrite | shared.OpenCreate | shared.OpenTruncate
	case ktfWIPICFileReadWrite:
		openMode = shared.OpenRead | shared.OpenWrite | shared.OpenCreate
	default:
		return ktfWIPICErrorInvalid, nil
	}
	serviceID, serviceErr := runtime.services.Storage.Open(
		runtime.serviceOwner,
		shared.NamespacePrivate,
		name,
		openMode,
	)
	if serviceErr != nil {
		return ktfWIPICError, nil
	}
	data, serviceErr := runtime.services.Storage.ReadFile(
		shared.NamespacePrivate,
		name,
	)
	if serviceErr != nil {
		_ = runtime.services.Storage.Close(runtime.serviceOwner, serviceID)
		return 0, serviceErr
	}
	runtime.fileData[name] = data
	handle := runtime.nextWIPICFile
	for handle == 0 || runtime.wipicFiles[handle] != nil {
		handle++
	}
	runtime.nextWIPICFile = handle + 1
	file := &ktfFile{
		namespace: shared.NamespacePrivate,
		name:      name,
		mode:      flag,
	}
	if flag == ktfWIPICFileWriteOnly {
		file.position = uint32(len(data))
	}
	runtime.wipicFiles[handle] = file
	runtime.wipicFileServices[handle] = serviceID
	runtime.tracef(
		"wipic_file_open:%s:flag=%d:fd=%d",
		name,
		flag,
		handle,
	)
	return handle, nil
}

func ktfWIPICFileRead(
	_ context.Context,
	runtime *ktfRuntime,
) (uint32, error) {
	handle, err := runtime.parameter(0)
	if err != nil {
		return 0, err
	}
	output, err := runtime.parameter(1)
	if err != nil {
		return 0, err
	}
	count, err := runtime.parameter(2)
	if err != nil {
		return 0, err
	}
	file := runtime.wipicFiles[handle]
	if file == nil || file.closed {
		return ktfWIPICErrorBadHandle, nil
	}
	if output == 0 || file.mode != ktfWIPICFileReadOnly &&
		file.mode != ktfWIPICFileReadWrite {
		return ktfWIPICErrorInvalid, nil
	}
	if count == 0 {
		return 0, nil
	}
	serviceID := runtime.wipicFileServices[handle]
	data, serviceErr := runtime.services.Storage.Read(
		runtime.serviceOwner,
		serviceID,
		uint64(count),
	)
	if serviceErr != nil {
		return ktfWIPICError, nil
	}
	if len(data) == 0 {
		return ktfWIPICErrorEOF, nil
	}
	if err := runtime.cpu.WriteMemory(output, data); err != nil {
		return 0, err
	}
	file.position += uint32(len(data))
	return uint32(len(data)), nil
}

func ktfWIPICFileWrite(
	_ context.Context,
	runtime *ktfRuntime,
) (uint32, error) {
	handle, err := runtime.parameter(0)
	if err != nil {
		return 0, err
	}
	input, err := runtime.parameter(1)
	if err != nil {
		return 0, err
	}
	count, err := runtime.parameter(2)
	if err != nil {
		return 0, err
	}
	file := runtime.wipicFiles[handle]
	if file == nil || file.closed {
		return ktfWIPICErrorBadHandle, nil
	}
	if input == 0 || file.mode != ktfWIPICFileWriteOnly &&
		file.mode != ktfWIPICFileTruncate &&
		file.mode != ktfWIPICFileReadWrite {
		return ktfWIPICErrorInvalid, nil
	}
	const maxFileSize = uint32(8 * 1024 * 1024)
	if count > maxFileSize || file.position > maxFileSize-count {
		return ktfWIPICError, nil
	}
	inputData := make([]byte, count)
	if err := runtime.cpu.ReadMemory(input, inputData); err != nil {
		return 0, err
	}
	serviceID := runtime.wipicFileServices[handle]
	written, serviceErr := runtime.services.Storage.Write(
		runtime.serviceOwner,
		serviceID,
		inputData,
	)
	if serviceErr != nil || written != len(inputData) {
		return ktfWIPICError, nil
	}
	stored, serviceErr := runtime.services.Storage.ReadFile(
		shared.NamespacePrivate,
		file.name,
	)
	if serviceErr != nil {
		return 0, serviceErr
	}
	runtime.fileData[file.name] = stored
	file.position += uint32(written)
	return count, nil
}

func ktfWIPICFileClose(
	_ context.Context,
	runtime *ktfRuntime,
) (uint32, error) {
	handle, err := runtime.parameter(0)
	if err != nil {
		return 0, err
	}
	file := runtime.wipicFiles[handle]
	if file == nil || file.closed {
		return ktfWIPICErrorBadHandle, nil
	}
	if serviceID := runtime.wipicFileServices[handle]; serviceID != 0 {
		if err := runtime.services.Storage.Close(
			runtime.serviceOwner,
			serviceID,
		); err != nil {
			return ktfWIPICError, nil
		}
		delete(runtime.wipicFileServices, handle)
	}
	file.closed = true
	delete(runtime.wipicFiles, handle)
	return 0, nil
}

func ktfWIPICFileSeek(
	_ context.Context,
	runtime *ktfRuntime,
) (uint32, error) {
	handle, err := runtime.parameter(0)
	if err != nil {
		return 0, err
	}
	rawPosition, err := runtime.parameter(1)
	if err != nil {
		return 0, err
	}
	origin, err := runtime.parameter(2)
	if err != nil {
		return 0, err
	}
	file := runtime.wipicFiles[handle]
	if file == nil || file.closed {
		return ktfWIPICErrorBadHandle, nil
	}
	position := int64(int32(rawPosition))
	whence := shared.SeekStart
	switch origin {
	case 0:
	case 1:
		whence = shared.SeekCurrent
	case 2:
		whence = shared.SeekEnd
	default:
		return ktfWIPICErrorInvalid, nil
	}
	servicePosition, serviceErr := runtime.services.Storage.Seek(
		runtime.serviceOwner,
		runtime.wipicFileServices[handle],
		position,
		whence,
	)
	if serviceErr != nil || servicePosition > 8*1024*1024 {
		return ktfWIPICErrorBadSeek, nil
	}
	file.position = uint32(servicePosition)
	return file.position, nil
}

func ktfWIPICFileAttribute(
	_ context.Context,
	runtime *ktfRuntime,
) (uint32, error) {
	name, err := runtime.wipicFileNameParameter(0)
	if err != nil {
		return 0, err
	}
	output, err := runtime.parameter(1)
	if err != nil {
		return 0, err
	}
	info, serviceErr := runtime.services.Storage.Stat(
		shared.NamespacePrivate,
		name,
	)
	if serviceErr != nil {
		return ktfWIPICErrorNoEntry, nil
	}
	if output == 0 {
		return ktfWIPICErrorInvalid, nil
	}
	if err := runtime.writeWords(
		output,
		[]uint32{0, uint32(info.Modified / time.Second), uint32(info.Size)},
	); err != nil {
		return 0, err
	}
	return 0, nil
}

func ktfWIPICFileRemove(
	_ context.Context,
	runtime *ktfRuntime,
) (uint32, error) {
	name, err := runtime.wipicFileNameParameter(0)
	if err != nil {
		return 0, err
	}
	if _, ok := runtime.fileData[name]; !ok {
		return ktfWIPICErrorNoEntry, nil
	}
	if err := runtime.services.Storage.Delete(
		shared.NamespacePrivate,
		name,
	); err != nil {
		return ktfWIPICError, nil
	}
	delete(runtime.fileData, name)
	return 0, nil
}

func ktfWIPICFileRename(
	_ context.Context,
	runtime *ktfRuntime,
) (uint32, error) {
	oldName, err := runtime.wipicFileNameParameter(0)
	if err != nil {
		return 0, err
	}
	newName, err := runtime.wipicFileNameParameter(1)
	if err != nil {
		return 0, err
	}
	data, ok := runtime.fileData[oldName]
	if !ok {
		return ktfWIPICErrorNoEntry, nil
	}
	if _, exists := runtime.fileData[newName]; exists {
		return ^uint32(4), nil
	}
	if err := runtime.services.Storage.Rename(
		shared.NamespacePrivate,
		oldName,
		newName,
	); err != nil {
		return ktfWIPICError, nil
	}
	runtime.fileData[newName] = data
	delete(runtime.fileData, oldName)
	for _, file := range runtime.files {
		if file != nil && file.name == oldName {
			file.name = newName
		}
	}
	for _, file := range runtime.wipicFiles {
		if file != nil && file.name == oldName {
			file.name = newName
		}
	}
	return 0, nil
}

func ktfWIPICFileMakeDirectory(
	_ context.Context,
	runtime *ktfRuntime,
) (uint32, error) {
	name, err := runtime.wipicFileNameParameter(0)
	if err != nil {
		return 0, err
	}
	if err := runtime.services.Storage.MakeDirectory(
		shared.NamespacePrivate,
		name,
	); err != nil {
		return ktfWIPICError, nil
	}
	return 0, nil
}

func ktfWIPICFileRemoveDirectory(
	_ context.Context,
	runtime *ktfRuntime,
) (uint32, error) {
	name, err := runtime.wipicFileNameParameter(0)
	if err != nil {
		return 0, err
	}
	if err := runtime.services.Storage.RemoveDirectory(
		shared.NamespacePrivate,
		name,
	); err != nil {
		return ktfWIPICError, nil
	}
	return 0, nil
}

func ktfWIPICFileList(
	_ context.Context,
	runtime *ktfRuntime,
) (uint32, error) {
	name, err := runtime.wipicFileNameParameter(0)
	if err != nil {
		return 0, err
	}
	output, err := runtime.parameter(1)
	if err != nil {
		return 0, err
	}
	size, err := runtime.parameter(2)
	if err != nil {
		return 0, err
	}
	if output == 0 || size < 2 {
		return ktfWIPICErrorShortBuf, nil
	}
	entries, serviceErr := runtime.services.Storage.List(
		shared.NamespacePrivate,
		name,
	)
	if serviceErr != nil {
		return ktfWIPICErrorNoEntry, nil
	}
	encoded := make([]byte, 0)
	for _, entry := range entries {
		encoded = append(encoded, entry...)
		encoded = append(encoded, 0)
	}
	encoded = append(encoded, 0)
	if uint32(len(encoded)) > size {
		return ktfWIPICErrorShortBuf, nil
	}
	if err := runtime.cpu.WriteMemory(output, encoded); err != nil {
		return 0, err
	}
	return 0, nil
}

func ktfWIPICFileTotalSpace(
	context.Context,
	*ktfRuntime,
) (uint32, error) {
	return 16 * 1024 * 1024, nil
}

func ktfWIPICFileAvailable(
	_ context.Context,
	runtime *ktfRuntime,
) (uint32, error) {
	used := runtime.services.Storage.Used(shared.NamespacePrivate)
	const total = uint64(16 * 1024 * 1024)
	if used >= total {
		return 0, nil
	}
	return uint32(total - used), nil
}

func ktfWIPICFileSetMode(
	context.Context,
	*ktfRuntime,
) (uint32, error) {
	return 0, nil
}

func ktfWIPICFileGetCounts(
	_ context.Context,
	runtime *ktfRuntime,
) (uint32, error) {
	name, err := runtime.wipicFileNameParameter(0)
	if err != nil {
		return 0, err
	}
	entries, err := runtime.services.Storage.List(
		shared.NamespacePrivate,
		name,
	)
	if err != nil {
		return ktfWIPICErrorNoEntry, nil
	}
	return uint32(len(entries)), nil
}

func ktfWIPICFileIsExist(
	_ context.Context,
	runtime *ktfRuntime,
) (uint32, error) {
	name, err := runtime.wipicFileNameParameter(0)
	if err != nil {
		return 0, err
	}
	if _, err := runtime.services.Storage.Stat(
		shared.NamespacePrivate,
		name,
	); err != nil && !runtime.services.Storage.DirectoryExists(
		shared.NamespacePrivate,
		name,
	) {
		return ktfWIPICErrorNoEntry, nil
	}
	return 0, nil
}

func ktfWIPICFileTell(
	_ context.Context,
	runtime *ktfRuntime,
) (uint32, error) {
	handle, err := runtime.parameter(0)
	if err != nil {
		return 0, err
	}
	file := runtime.wipicFiles[handle]
	if file == nil || file.closed {
		return ktfWIPICErrorBadHandle, nil
	}
	return file.position, nil
}

func (r *ktfRuntime) wipicFileNameParameter(index uint32) (string, error) {
	address, err := r.parameter(index)
	if err != nil {
		return "", err
	}
	name, err := r.readCString(address, 1024)
	if err != nil {
		return "", err
	}
	return normalizeKTFFileName(name), nil
}

func ktfWIPICNoop(table, slot int) ktfHostHandler {
	return func(_ context.Context, runtime *ktfRuntime) (uint32, error) {
		parameters := make([]uint32, 6)
		for index := range parameters {
			parameters[index], _ = runtime.parameter(uint32(index))
		}
		link, _ := runtime.cpu.ReadRegister(cpu.RegisterLR)
		runtime.tracef(
			"wipic_call:%d.%d:args=%08x:lr=0x%08x",
			table,
			slot,
			parameters,
			link,
		)
		return 0, nil
	}
}

func ktfWIPICGraphicsGetImageProperty(
	_ context.Context,
	runtime *ktfRuntime,
) (uint32, error) {
	object, err := runtime.parameter(0)
	if err != nil {
		return 0, err
	}
	index, err := runtime.parameter(1)
	if err != nil {
		return 0, err
	}
	imageState := runtime.wipicImages[object]
	if imageState == nil {
		return 0, nil
	}
	framebuffer := runtime.wipicFramebuffers[imageState.framebuffer]
	if framebuffer == nil {
		return 0, nil
	}
	switch index {
	case 1:
		return 0, nil
	case 2:
		return 0, nil
	case 3:
		return 1, nil
	case 4:
		return uint32(framebuffer.width), nil
	case 5:
		return uint32(framebuffer.height), nil
	case 6:
		return uint32(framebuffer.bits), nil
	default:
		return 0, nil
	}
}

func ktfWIPICGraphicsGetImageFramebuffer(
	_ context.Context,
	runtime *ktfRuntime,
) (uint32, error) {
	object, err := runtime.parameter(0)
	if err != nil {
		return 0, err
	}
	imageState := runtime.wipicImages[object]
	if imageState == nil {
		return 0, nil
	}
	return imageState.framebuffer, nil
}

func ktfWIPICGraphicsCreateImage(
	_ context.Context,
	runtime *ktfRuntime,
) (uint32, error) {
	output, err := runtime.parameter(0)
	if err != nil {
		return 0, err
	}
	memoryID, err := runtime.parameter(1)
	if err != nil {
		return 0, err
	}
	offset, err := runtime.parameter(2)
	if err != nil {
		return 0, err
	}
	length, err := runtime.parameter(3)
	if err != nil {
		return 0, err
	}
	if output == 0 {
		return ktfWIPICErrorInvalid, nil
	}
	if err := runtime.writeU32(output, 0); err != nil {
		return 0, err
	}
	allocation, ok := runtime.wipicMemory[memoryID]
	if !ok || length == 0 ||
		uint64(offset)+uint64(length) > uint64(allocation.size) ||
		length > 16<<20 {
		return ^uint32(15), nil
	}
	encoded := make([]byte, length)
	if err := runtime.cpu.ReadMemory(allocation.data+offset, encoded); err != nil {
		return 0, err
	}
	assetID, err := runtime.services.Assets.Decode(
		runtime.serviceOwner,
		encoded,
		shared.DecodeOptions{},
	)
	if err != nil {
		return ^uint32(15), nil
	}
	asset, err := runtime.services.Assets.Info(runtime.serviceOwner, assetID)
	if err != nil {
		_ = runtime.services.Assets.Release(runtime.serviceOwner, assetID)
		return 0, err
	}
	width, height := int(asset.Width), int(asset.Height)
	if width <= 0 || height <= 0 || width > 4096 || height > 4096 {
		_ = runtime.services.Assets.Release(runtime.serviceOwner, assetID)
		return ^uint32(15), nil
	}
	framebufferObject, err := runtime.createWIPICFramebuffer(
		width,
		height,
		false,
	)
	if err != nil {
		_ = runtime.services.Assets.Release(runtime.serviceOwner, assetID)
		return 0, err
	}
	if err := runtime.paintWIPICImageFrame(
		framebufferObject,
		asset.Frames[0].Surface,
	); err != nil {
		_ = runtime.services.Assets.Release(runtime.serviceOwner, assetID)
		return 0, err
	}
	// KTF's provider-private MC_GrpImage is a pointer wrapper around an image
	// body. Native Clets dereference the wrapper, then read the framebuffer at
	// body+0x08 and the optional mask framebuffer at body+0x0c.
	body, err := runtime.allocateWords(4)
	if err != nil {
		return 0, err
	}
	if err := runtime.writeWords(body, []uint32{
		0,
		0,
		framebufferObject,
		0,
	}); err != nil {
		return 0, err
	}
	object, err := runtime.allocateWords(1)
	if err != nil {
		return 0, err
	}
	if err := runtime.writeU32(object, body); err != nil {
		return 0, err
	}
	runtime.wipicImages[object] = &ktfWIPICImage{
		object:      object,
		body:        body,
		framebuffer: framebufferObject,
		source:      memoryID,
	}
	runtime.wipicAssetServices[object] = assetID
	if err := runtime.writeU32(output, object); err != nil {
		return 0, err
	}
	runtime.tracef(
		"wipic_graphics_image:object=0x%08x:framebuffer=0x%08x:%dx%d",
		object,
		framebufferObject,
		width,
		height,
	)
	return 1, nil
}

func ktfWIPICGraphicsDestroyImage(
	_ context.Context,
	runtime *ktfRuntime,
) (uint32, error) {
	object, err := runtime.parameter(0)
	if err != nil {
		return 0, err
	}
	imageState := runtime.wipicImages[object]
	if imageState == nil {
		return 0, nil
	}
	if framebuffer := runtime.wipicFramebuffers[imageState.framebuffer]; framebuffer != nil {
		if serviceID := runtime.wipicSurfaceServices[framebuffer.object]; serviceID != 0 {
			_ = runtime.services.Graphics.DestroySurface(
				runtime.serviceOwner,
				serviceID,
			)
			delete(runtime.wipicSurfaceServices, framebuffer.object)
		}
		runtime.heap.release(framebuffer.pixelHeader)
		runtime.heap.release(framebuffer.pixelObject)
		runtime.heap.release(framebuffer.body)
		runtime.heap.release(framebuffer.object)
		delete(runtime.wipicFramebuffers, framebuffer.object)
	}
	if assetID := runtime.wipicAssetServices[object]; assetID != 0 {
		_ = runtime.services.Assets.Release(runtime.serviceOwner, assetID)
		delete(runtime.wipicAssetServices, object)
	}
	if allocation, ok := runtime.wipicMemory[imageState.source]; ok {
		runtime.heap.release(allocation.base)
		runtime.heap.release(imageState.source)
		delete(runtime.wipicMemory, imageState.source)
	}
	runtime.heap.release(imageState.body)
	runtime.heap.release(imageState.object)
	delete(runtime.wipicImages, object)
	return 0, nil
}

// ktfWIPICGraphicsEncodeImage returns a KTF indirect-memory ID containing a
// BMP representation of the requested framebuffer rectangle. Although public
// WIPI references describe a small status return, KTF Clet support code treats
// this provider slot as an allocating call: it dereferences the returned ID and
// consumes the payload at the resulting buffer head+8.
func ktfWIPICGraphicsEncodeImage(
	_ context.Context,
	runtime *ktfRuntime,
) (uint32, error) {
	values := make([]uint32, 6)
	for index := range values {
		value, err := runtime.parameter(uint32(index))
		if err != nil {
			return 0, err
		}
		values[index] = value
	}
	lengthAddress := values[5]
	if lengthAddress != 0 {
		if err := runtime.writeU32(lengthAddress, 0); err != nil {
			return 0, err
		}
	}
	framebuffer := runtime.wipicFramebuffers[values[0]]
	x, y := int64(int32(values[1])), int64(int32(values[2]))
	width, height := int64(int32(values[3])), int64(int32(values[4]))
	if framebuffer == nil || x < 0 || y < 0 || width <= 0 || height <= 0 ||
		x+width > int64(framebuffer.width) ||
		y+height > int64(framebuffer.height) {
		return 0, nil
	}
	if err := runtime.syncKTFWIPICFramebuffer(values[0]); err != nil {
		return 0, err
	}
	surface := runtime.wipicSurfaceServices[values[0]]
	if surface == 0 {
		return 0, nil
	}
	encoded, err := runtime.services.Assets.EncodeSurface(
		runtime.serviceOwner,
		surface,
		"image/bmp",
		shared.Rectangle{
			X:      int32(x),
			Y:      int32(y),
			Width:  int32(width),
			Height: int32(height),
		},
	)
	if err != nil || len(encoded) == 0 || uint64(len(encoded)) > 32<<20 {
		return 0, nil
	}
	memoryID, err := runtime.allocateWIPICMemory(uint32(len(encoded)), false)
	if err != nil || memoryID == 0 {
		return 0, err
	}
	allocation := runtime.wipicMemory[memoryID]
	release := func() {
		runtime.heap.release(allocation.base)
		runtime.heap.release(memoryID)
		delete(runtime.wipicMemory, memoryID)
	}
	if err := runtime.cpu.WriteMemory(allocation.data, encoded); err != nil {
		release()
		return 0, err
	}
	if lengthAddress != 0 {
		if err := runtime.writeU32(lengthAddress, uint32(len(encoded))); err != nil {
			release()
			return 0, err
		}
	}
	runtime.tracef(
		"wipic_graphics_encode:framebuffer=0x%08x:memory=0x%08x:"+
			"rect=%d,%d,%d,%d:size=%d",
		values[0],
		memoryID,
		x,
		y,
		width,
		height,
		len(encoded),
	)
	return memoryID, nil
}

func ktfWIPICGraphicsGetScreenFramebuffer(
	_ context.Context,
	runtime *ktfRuntime,
) (uint32, error) {
	display, err := runtime.parameter(0)
	if err != nil {
		return 0, err
	}
	if display > 1 {
		return 0, nil
	}
	return runtime.ensureWIPICScreenFramebuffer()
}

func (r *ktfRuntime) ensureWIPICScreenFramebuffer() (uint32, error) {
	if r.wipicScreenFramebuffer != 0 {
		return r.wipicScreenFramebuffer, nil
	}
	if r.frame == nil {
		r.frame = image.NewRGBA(image.Rect(0, 0, 240, 320))
	}
	width, height := r.frame.Bounds().Dx(), r.frame.Bounds().Dy()
	object, err := r.createWIPICFramebuffer(width, height, true)
	if err != nil {
		return 0, err
	}
	r.wipicScreenFramebuffer = object
	return object, nil
}

func ktfWIPICGraphicsCreateOffscreenFramebuffer(
	_ context.Context,
	runtime *ktfRuntime,
) (uint32, error) {
	width, err := runtime.parameter(0)
	if err != nil {
		return 0, err
	}
	height, err := runtime.parameter(1)
	if err != nil {
		return 0, err
	}
	if width == 0 || height == 0 || width > 4096 || height > 4096 {
		return 0, nil
	}
	return runtime.createWIPICFramebuffer(int(width), int(height), false)
}

func ktfWIPICGraphicsDestroyOffscreenFramebuffer(
	_ context.Context,
	runtime *ktfRuntime,
) (uint32, error) {
	object, err := runtime.parameter(0)
	if err != nil {
		return 0, err
	}
	framebuffer := runtime.wipicFramebuffers[object]
	if framebuffer == nil || framebuffer.screen {
		return 0, nil
	}
	if serviceID := runtime.wipicSurfaceServices[object]; serviceID != 0 {
		if err := runtime.services.Graphics.DestroySurface(
			runtime.serviceOwner,
			serviceID,
		); err != nil {
			return 0, err
		}
		delete(runtime.wipicSurfaceServices, object)
	}
	runtime.heap.release(framebuffer.pixelHeader)
	runtime.heap.release(framebuffer.pixelObject)
	runtime.heap.release(framebuffer.body)
	runtime.heap.release(framebuffer.object)
	delete(runtime.wipicFramebuffers, object)
	return 0, nil
}

func (r *ktfRuntime) createWIPICFramebuffer(
	width int,
	height int,
	screen bool,
) (uint32, error) {
	const bits = 16
	stride := width * (bits / 8)
	pixelBytes := uint64(stride) * uint64(height)
	if pixelBytes+8 > uint64(^uint32(0)) {
		return 0, errors.New("KTF WIPI-C framebuffer size overflows")
	}
	pixelHeader, err := r.heap.allocate(uint32(pixelBytes)+8, true)
	if err != nil || pixelHeader == 0 {
		return 0, err
	}
	pixelObject, err := r.allocateWords(1)
	if err != nil {
		return 0, err
	}
	if err := r.writeU32(pixelObject, pixelHeader); err != nil {
		return 0, err
	}
	body, err := r.allocateWords(8)
	if err != nil {
		return 0, err
	}
	// The first nested surface is sufficient for the metrics read directly by
	// Clets. Point it back to this body so +0x08..+0x18 have one canonical
	// representation. body+0x18 is the handset's nested pixel-array object.
	if err := r.writeWords(body, []uint32{
		body,
		pixelObject,
		uint32(width),
		uint32(height),
		uint32(stride),
		bits,
		pixelObject,
		1,
	}); err != nil {
		return 0, err
	}
	object, err := r.allocateWords(5)
	if err != nil {
		return 0, err
	}
	if err := r.writeU32(object, body); err != nil {
		return 0, err
	}
	framebuffer := &ktfWIPICFramebuffer{
		object:      object,
		body:        body,
		pixelObject: pixelObject,
		pixelHeader: pixelHeader,
		pixels:      pixelHeader + 8,
		width:       width,
		height:      height,
		stride:      stride,
		bits:        bits,
		screen:      screen,
	}
	surface, err := r.services.Graphics.CreateSurface(
		r.serviceOwner,
		shared.SurfaceDescriptor{
			Width:  int32(width),
			Height: int32(height),
			Stride: int32(stride),
			Format: shared.PixelRGB565,
		},
	)
	if err != nil {
		return 0, err
	}
	if screen {
		if err := r.services.Graphics.SetScreen(r.serviceOwner, surface); err != nil {
			_ = r.services.Graphics.DestroySurface(r.serviceOwner, surface)
			return 0, err
		}
	}
	r.wipicFramebuffers[object] = framebuffer
	r.wipicSurfaceServices[object] = surface
	r.tracef(
		"wipic_graphics_framebuffer:object=0x%08x:body=0x%08x:pixels=0x%08x:%dx%dx%d:screen=%t",
		object,
		body,
		framebuffer.pixels,
		width,
		height,
		bits,
		screen,
	)
	return object, nil
}

func ktfWIPICGraphicsInitContext(
	_ context.Context,
	runtime *ktfRuntime,
) (uint32, error) {
	address, err := runtime.parameter(0)
	if err != nil || address == 0 {
		return 0, err
	}
	values := [...]uint32{
		0, 0, 0x7fffffff, 0x7fffffff, 0,
		0, 0xffff, 255, 0, 0, 0, 0, 0, 0, 0,
	}
	return 0, runtime.writeWords(address, values[:])
}

func ktfWIPICGraphicsSetContext(
	_ context.Context,
	runtime *ktfRuntime,
) (uint32, error) {
	address, err := runtime.parameter(0)
	if err != nil {
		return 0, err
	}
	index, err := runtime.parameter(1)
	if err != nil {
		return 0, err
	}
	value, err := runtime.parameter(2)
	if err != nil || address == 0 {
		return 0, err
	}
	offsets := map[uint32]uint32{
		1: 20, 2: 24, 4: 28, 5: 32, 6: 36,
		7: 40, 8: 44, 9: 48,
	}
	if index == 0 {
		if value == 0 {
			return 0, nil
		}
		clip := make([]byte, 16)
		if err := runtime.cpu.ReadMemory(value, clip); err != nil {
			return 0, err
		}
		if err := runtime.cpu.WriteMemory(address, clip); err != nil {
			return 0, err
		}
		return 0, runtime.writeU32(address+16, 1)
	}
	if index == 10 {
		if value == 0 {
			return 0, nil
		}
		offset := make([]byte, 8)
		if err := runtime.cpu.ReadMemory(value, offset); err != nil {
			return 0, err
		}
		return 0, runtime.cpu.WriteMemory(address+52, offset)
	}
	if offset, ok := offsets[index]; ok {
		return 0, runtime.writeU32(address+offset, value)
	}
	return 0, nil
}

func ktfWIPICGraphicsGetContext(
	_ context.Context,
	runtime *ktfRuntime,
) (uint32, error) {
	address, err := runtime.parameter(0)
	if err != nil {
		return 0, err
	}
	index, err := runtime.parameter(1)
	if err != nil {
		return 0, err
	}
	output, err := runtime.parameter(2)
	if err != nil || address == 0 || output == 0 {
		return 0, err
	}
	offsets := map[uint32]struct {
		offset uint32
		size   uint32
	}{
		0: {0, 16}, 1: {20, 4}, 2: {24, 4}, 4: {28, 4},
		5: {32, 4}, 6: {36, 4}, 7: {40, 4}, 8: {44, 4},
		9: {48, 4}, 10: {52, 8},
	}
	field, ok := offsets[index]
	if !ok {
		return 0, nil
	}
	data := make([]byte, field.size)
	if err := runtime.cpu.ReadMemory(address+field.offset, data); err != nil {
		return 0, err
	}
	return 0, runtime.cpu.WriteMemory(output, data)
}

type ktfWIPICGraphicsContext struct {
	left, top, right, bottom int
	clipEnabled              bool
	foreground               uint16
	font                     uint32
	offsetX, offsetY         int
}

func (r *ktfRuntime) wipicGraphicsContext(
	address uint32,
) (ktfWIPICGraphicsContext, error) {
	state := ktfWIPICGraphicsContext{
		right:  int(^uint32(0) >> 1),
		bottom: int(^uint32(0) >> 1),
	}
	if address == 0 {
		return state, nil
	}
	var encoded [60]byte
	if err := r.cpu.ReadMemory(address, encoded[:]); err != nil {
		return state, fmt.Errorf(
			"read KTF WIPI-C graphics context at 0x%08x: %w",
			address,
			err,
		)
	}
	state.left = int(int32(binary.LittleEndian.Uint32(encoded[0:4])))
	state.top = int(int32(binary.LittleEndian.Uint32(encoded[4:8])))
	state.right = int(int32(binary.LittleEndian.Uint32(encoded[8:12])))
	state.bottom = int(int32(binary.LittleEndian.Uint32(encoded[12:16])))
	state.clipEnabled = binary.LittleEndian.Uint32(encoded[16:20]) != 0
	state.foreground = uint16(binary.LittleEndian.Uint32(encoded[20:24]))
	state.font = binary.LittleEndian.Uint32(encoded[40:44])
	state.offsetX = int(int32(binary.LittleEndian.Uint32(encoded[52:56])))
	state.offsetY = int(int32(binary.LittleEndian.Uint32(encoded[56:60])))
	return state, nil
}

func ktfWIPICGraphicsPutPixel(
	_ context.Context,
	runtime *ktfRuntime,
) (uint32, error) {
	framebuffer, err := runtime.parameter(0)
	if err != nil {
		return 0, err
	}
	x, err := runtime.parameter(1)
	if err != nil {
		return 0, err
	}
	y, err := runtime.parameter(2)
	if err != nil {
		return 0, err
	}
	contextAddress, err := runtime.parameter(3)
	if err != nil {
		return 0, err
	}
	state, err := runtime.wipicGraphicsContext(contextAddress)
	if err != nil {
		return 0, err
	}
	if err := runtime.writeWIPICPixel(
		framebuffer,
		int(int32(x))+state.offsetX,
		int(int32(y))+state.offsetY,
		state,
	); err != nil {
		return 0, err
	}
	return 0, runtime.syncKTFWIPICFramebuffer(framebuffer)
}

func ktfWIPICGraphicsDrawLine(
	_ context.Context,
	runtime *ktfRuntime,
) (uint32, error) {
	values := make([]uint32, 6)
	for index := range values {
		value, err := runtime.parameter(uint32(index))
		if err != nil {
			return 0, fmt.Errorf(
				"read KTF WIPI-C line parameter %d: %w",
				index,
				err,
			)
		}
		values[index] = value
	}
	state, err := runtime.wipicGraphicsContext(values[5])
	if err != nil {
		return 0, err
	}
	x1 := int(int32(values[1])) + state.offsetX
	y1 := int(int32(values[2])) + state.offsetY
	x2 := int(int32(values[3])) + state.offsetX
	y2 := int(int32(values[4])) + state.offsetY
	if err := runtime.drawWIPICLine(values[0], x1, y1, x2, y2, state); err != nil {
		return 0, err
	}
	return 0, runtime.syncKTFWIPICFramebuffer(values[0])
}

func (r *ktfRuntime) drawWIPICLine(
	handle uint32,
	x1, y1, x2, y2 int,
	state ktfWIPICGraphicsContext,
) error {
	var visible bool
	x1, y1, x2, y2, visible = r.clipWIPICLine(
		handle,
		x1,
		y1,
		x2,
		y2,
		state,
	)
	if !visible {
		return nil
	}
	dx := abs(x2 - x1)
	dy := -abs(y2 - y1)
	stepX, stepY := -1, -1
	if x1 < x2 {
		stepX = 1
	}
	if y1 < y2 {
		stepY = 1
	}
	difference := dx + dy
	for {
		if err := r.writeWIPICPixel(handle, x1, y1, state); err != nil {
			return err
		}
		if x1 == x2 && y1 == y2 {
			break
		}
		twice := difference * 2
		if twice >= dy {
			difference += dy
			x1 += stepX
		}
		if twice <= dx {
			difference += dx
			y1 += stepY
		}
	}
	return nil
}

func ktfWIPICGraphicsDrawRect(
	_ context.Context,
	runtime *ktfRuntime,
) (uint32, error) {
	values := make([]uint32, 6)
	for index := range values {
		value, err := runtime.parameter(uint32(index))
		if err != nil {
			return 0, err
		}
		values[index] = value
	}
	width, height := int(int32(values[3])), int(int32(values[4]))
	if width <= 0 || height <= 0 {
		return 0, nil
	}
	state, err := runtime.wipicGraphicsContext(values[5])
	if err != nil {
		return 0, err
	}
	x := int(int32(values[1])) + state.offsetX
	y := int(int32(values[2])) + state.offsetY
	lines := [][4]int{
		{x, y, x + width - 1, y},
		{x, y + height - 1, x + width - 1, y + height - 1},
		{x, y, x, y + height - 1},
		{x + width - 1, y, x + width - 1, y + height - 1},
	}
	for _, line := range lines {
		if err := runtime.drawWIPICLine(
			values[0],
			line[0],
			line[1],
			line[2],
			line[3],
			state,
		); err != nil {
			return 0, err
		}
	}
	return 0, runtime.syncKTFWIPICFramebuffer(values[0])
}

func ktfWIPICGraphicsFillRect(
	_ context.Context,
	runtime *ktfRuntime,
) (uint32, error) {
	values := make([]uint32, 6)
	for index := range values {
		value, err := runtime.parameter(uint32(index))
		if err != nil {
			return 0, fmt.Errorf(
				"read KTF WIPI-C fill-rectangle parameter %d: %w",
				index,
				err,
			)
		}
		values[index] = value
	}
	framebuffer := runtime.wipicFramebuffers[values[0]]
	if framebuffer == nil {
		return 0, nil
	}
	state, err := runtime.wipicGraphicsContext(values[5])
	if err != nil {
		return 0, err
	}
	x := int(int32(values[1])) + state.offsetX
	y := int(int32(values[2])) + state.offsetY
	width := int(int32(values[3]))
	height := int(int32(values[4]))
	left, top := max(0, x), max(0, y)
	right := min(framebuffer.width, x+width)
	bottom := min(framebuffer.height, y+height)
	if state.clipEnabled {
		left = max(left, state.left)
		top = max(top, state.top)
		right = min(right, state.right)
		bottom = min(bottom, state.bottom)
	}
	if left >= right || top >= bottom {
		return 0, nil
	}
	row := make([]byte, (right-left)*2)
	for offset := 0; offset < len(row); offset += 2 {
		binary.LittleEndian.PutUint16(row[offset:], state.foreground)
	}
	for currentY := top; currentY < bottom; currentY++ {
		address := framebuffer.pixels +
			uint32(currentY*framebuffer.stride+left*2)
		if err := runtime.cpu.WriteMemory(address, row); err != nil {
			return 0, fmt.Errorf(
				"fill KTF WIPI-C framebuffer 0x%08x pixels=0x%08x "+
					"row=%d address=0x%08x: %w",
				values[0],
				framebuffer.pixels,
				currentY,
				address,
				err,
			)
		}
	}
	return 0, runtime.syncKTFWIPICFramebuffer(values[0])
}

func ktfWIPICGraphicsCopyFramebuffer(
	_ context.Context,
	runtime *ktfRuntime,
) (uint32, error) {
	values := make([]uint32, 9)
	for index := range values {
		value, err := runtime.parameter(uint32(index))
		if err != nil {
			return 0, err
		}
		values[index] = value
	}
	state, err := runtime.wipicGraphicsContext(values[8])
	if err != nil {
		return 0, err
	}
	if err := runtime.blitWIPICFramebuffer(
		values[0],
		values[5],
		int64(int32(values[1]))+int64(state.offsetX),
		int64(int32(values[2]))+int64(state.offsetY),
		int64(int32(values[3])),
		int64(int32(values[4])),
		int64(int32(values[6])),
		int64(int32(values[7])),
		state,
	); err != nil {
		return 0, err
	}
	return 0, runtime.syncKTFWIPICFramebuffer(values[0])
}

// ktfWIPICGraphicsDrawImage blits an MC_GrpImage into a framebuffer. The image
// wrapper carries its pixels in a framebuffer of its own, so the copy is the
// framebuffer-to-framebuffer one with the source resolved through the image and
// the destination rectangle clipped by the graphics context.
func ktfWIPICGraphicsDrawImage(
	_ context.Context,
	runtime *ktfRuntime,
) (uint32, error) {
	values := make([]uint32, 9)
	for index := range values {
		value, err := runtime.parameter(uint32(index))
		if err != nil {
			return 0, fmt.Errorf(
				"read KTF WIPI-C draw-image parameter %d: %w",
				index,
				err,
			)
		}
		values[index] = value
	}
	image := runtime.wipicImages[values[5]]
	if image == nil {
		return 0, nil
	}
	state, err := runtime.wipicGraphicsContext(values[8])
	if err != nil {
		return 0, err
	}
	if err := runtime.blitWIPICFramebuffer(
		values[0],
		image.framebuffer,
		int64(int32(values[1]))+int64(state.offsetX),
		int64(int32(values[2]))+int64(state.offsetY),
		int64(int32(values[3])),
		int64(int32(values[4])),
		int64(int32(values[6])),
		int64(int32(values[7])),
		state,
	); err != nil {
		return 0, err
	}
	return 0, runtime.syncKTFWIPICFramebuffer(values[0])
}

// blitWIPICFramebuffer copies a rectangle between two 16bpp framebuffers,
// clamping it into both and against the context clip.
func (r *ktfRuntime) blitWIPICFramebuffer(
	destinationHandle, sourceHandle uint32,
	dx, dy, width, height, sx, sy int64,
	state ktfWIPICGraphicsContext,
) error {
	destination := r.wipicFramebuffers[destinationHandle]
	source := r.wipicFramebuffers[sourceHandle]
	if destination == nil || source == nil || width <= 0 || height <= 0 {
		return nil
	}
	if sx < 0 {
		width += sx
		dx -= sx
		sx = 0
	}
	if sy < 0 {
		height += sy
		dy -= sy
		sy = 0
	}
	left, top := dx, dy
	right, bottom := dx+width, dy+height
	if state.clipEnabled {
		left = max(left, int64(state.left))
		top = max(top, int64(state.top))
		right = min(right, int64(state.right))
		bottom = min(bottom, int64(state.bottom))
	}
	left = max(left, 0)
	top = max(top, 0)
	right = min(right, int64(destination.width), dx+int64(source.width)-sx)
	bottom = min(bottom, int64(destination.height), dy+int64(source.height)-sy)
	if left >= right || top >= bottom {
		return nil
	}
	rowBytes := int(right-left) * 2
	rowCount := int(bottom - top)
	byteCount := int64(rowBytes) * int64(rowCount)
	if byteCount > 32<<20 {
		return errors.New("KTF WIPI-C framebuffer copy exceeds 32 MiB")
	}
	// Read the entire source rectangle before writing any destination row.
	// CopyArea commonly moves pixels inside one framebuffer and must have
	// memmove semantics for vertical as well as horizontal overlap.
	data := make([]byte, int(byteCount))
	for y := top; y < bottom; y++ {
		sourceAddress := source.pixels +
			uint32((sy+y-dy)*int64(source.stride)+(sx+left-dx)*2)
		rowOffset := int(y-top) * rowBytes
		if err := r.cpu.ReadMemory(
			sourceAddress,
			data[rowOffset:rowOffset+rowBytes],
		); err != nil {
			return err
		}
	}
	for y := top; y < bottom; y++ {
		destinationAddress := destination.pixels +
			uint32(y*int64(destination.stride)+left*2)
		rowOffset := int(y-top) * rowBytes
		if err := r.cpu.WriteMemory(
			destinationAddress,
			data[rowOffset:rowOffset+rowBytes],
		); err != nil {
			return err
		}
	}
	return nil
}

func (r *ktfRuntime) writeWIPICPixel(
	handle uint32,
	x, y int,
	state ktfWIPICGraphicsContext,
) error {
	return r.writeWIPICPixelValue(handle, x, y, state, state.foreground)
}

func (r *ktfRuntime) writeWIPICPixelValue(
	handle uint32,
	x, y int,
	state ktfWIPICGraphicsContext,
	value uint16,
) error {
	framebuffer := r.wipicFramebuffers[handle]
	if framebuffer == nil ||
		x < 0 || y < 0 ||
		x >= framebuffer.width || y >= framebuffer.height {
		return nil
	}
	if state.clipEnabled &&
		(x < state.left || y < state.top ||
			x >= state.right || y >= state.bottom) {
		return nil
	}
	var encoded [2]byte
	binary.LittleEndian.PutUint16(encoded[:], value)
	return r.cpu.WriteMemory(
		framebuffer.pixels+uint32(y*framebuffer.stride+x*2),
		encoded[:],
	)
}

func (r *ktfRuntime) writeWIPICPixelAlpha(
	handle uint32,
	x, y int,
	state ktfWIPICGraphicsContext,
	alpha byte,
) error {
	if alpha == 0 {
		return nil
	}
	if alpha == 0xff {
		return r.writeWIPICPixel(handle, x, y, state)
	}
	framebuffer := r.wipicFramebuffers[handle]
	if framebuffer == nil ||
		x < 0 || y < 0 ||
		x >= framebuffer.width || y >= framebuffer.height {
		return nil
	}
	if state.clipEnabled &&
		(x < state.left || y < state.top ||
			x >= state.right || y >= state.bottom) {
		return nil
	}
	address := framebuffer.pixels + uint32(y*framebuffer.stride+x*2)
	var encoded [2]byte
	if err := r.cpu.ReadMemory(address, encoded[:]); err != nil {
		return err
	}
	destination := binary.LittleEndian.Uint16(encoded[:])
	binary.LittleEndian.PutUint16(
		encoded[:],
		blendKTFWIPICRGB565(destination, state.foreground, alpha),
	)
	return r.cpu.WriteMemory(address, encoded[:])
}

func blendKTFWIPICRGB565(destination, source uint16, alpha byte) uint16 {
	blend := func(destination, source uint16) uint16 {
		coverage := uint32(alpha)
		return uint16(
			(uint32(source)*coverage +
				uint32(destination)*(0xff-coverage) + 127) / 0xff,
		)
	}
	return blend(destination>>11, source>>11)<<11 |
		blend(destination>>5&0x3f, source>>5&0x3f)<<5 |
		blend(destination&0x1f, source&0x1f)
}

// KTF hands WIPI-C text to the same shared fallback font the Java surface
// draws with, so a Clet that measures a run and then paints it observes one
// set of advances. Glyphs are blitted straight into the guest framebuffer
// because Clets read those pixels back through the framebuffer object.
func ktfWIPICGraphicsDrawString(
	_ context.Context,
	runtime *ktfRuntime,
) (uint32, error) {
	return runtime.drawWIPICString(false)
}

func ktfWIPICGraphicsDrawUnicodeString(
	_ context.Context,
	runtime *ktfRuntime,
) (uint32, error) {
	return runtime.drawWIPICString(true)
}

func (r *ktfRuntime) drawWIPICString(unicode bool) (uint32, error) {
	values := make([]uint32, 6)
	for index := range values {
		value, err := r.parameter(uint32(index))
		if err != nil {
			return 0, fmt.Errorf(
				"read KTF WIPI-C string parameter %d: %w",
				index,
				err,
			)
		}
		values[index] = value
	}
	if r.wipicFramebuffers[values[0]] == nil {
		return 0, nil
	}
	state, err := r.wipicGraphicsContext(values[5])
	if err != nil {
		return 0, err
	}
	text, err := r.wipicText(values[3], int32(values[4]), unicode)
	if err != nil || text == "" {
		return 0, err
	}
	fontID, err := r.ensureWIPICFontService(state.font)
	if err != nil {
		return 0, err
	}
	if err := r.drawWIPICGlyphs(
		values[0],
		int(int32(values[1]))+state.offsetX,
		int(int32(values[2]))+state.offsetY,
		text,
		fontID,
		state,
	); err != nil {
		return 0, err
	}
	return 0, r.syncKTFWIPICFramebuffer(values[0])
}

// drawWIPICGlyphs places the run with the top-left origin WIPI-C uses, so the
// glyph bearings stay relative to the requested y rather than a baseline.
func (r *ktfRuntime) drawWIPICGlyphs(
	handle uint32,
	x, y int,
	text string,
	fontID shared.ServiceID,
	state ktfWIPICGraphicsContext,
) error {
	cursor := x
	for _, character := range text {
		glyph, err := r.services.Text.Glyph(r.serviceOwner, fontID, character)
		if err != nil {
			return fmt.Errorf(
				"rasterize KTF WIPI-C glyph %q: %w",
				character,
				err,
			)
		}
		for row := int32(0); row < glyph.Height; row++ {
			for column := int32(0); column < glyph.Width; column++ {
				alpha := glyph.Alpha[row*glyph.Width+column]
				if alpha == 0 {
					continue
				}
				if err := r.writeWIPICPixelAlpha(
					handle,
					cursor+int(glyph.BearingX+column),
					y+int(glyph.BearingY+row),
					state,
					alpha,
				); err != nil {
					return err
				}
			}
		}
		cursor += int(glyph.Advance)
	}
	return nil
}

func ktfWIPICGraphicsGetStringWidth(
	_ context.Context,
	runtime *ktfRuntime,
) (uint32, error) {
	return runtime.measureWIPICString(false)
}

func ktfWIPICGraphicsGetUnicodeStringWidth(
	_ context.Context,
	runtime *ktfRuntime,
) (uint32, error) {
	return runtime.measureWIPICString(true)
}

func (r *ktfRuntime) measureWIPICString(unicode bool) (uint32, error) {
	font, err := r.parameter(0)
	if err != nil {
		return 0, err
	}
	address, err := r.parameter(1)
	if err != nil {
		return 0, err
	}
	length, err := r.parameter(2)
	if err != nil {
		return 0, err
	}
	text, err := r.wipicText(address, int32(length), unicode)
	if err != nil || text == "" {
		return 0, err
	}
	fontID, err := r.ensureWIPICFontService(font)
	if err != nil {
		return 0, err
	}
	width, err := r.services.Text.Measure(r.serviceOwner, fontID, text)
	if err != nil {
		return 0, err
	}
	return uint32(max(int32(0), width)), nil
}

func ktfWIPICGraphicsGetFont(
	_ context.Context,
	runtime *ktfRuntime,
) (uint32, error) {
	face, err := runtime.parameter(0)
	if err != nil {
		return 0, err
	}
	size, err := runtime.parameter(1)
	if err != nil {
		return 0, err
	}
	style, err := runtime.parameter(2)
	if err != nil {
		return 0, err
	}
	return face&0xe0 | style<<8 | size&0x1f, nil
}

func ktfWIPICGraphicsGetFontHeight(
	_ context.Context,
	runtime *ktfRuntime,
) (uint32, error) {
	metrics, err := runtime.wipicFontMetrics()
	if err != nil {
		return 0, err
	}
	return uint32(metrics.Height), nil
}

func ktfWIPICGraphicsGetFontAscent(
	_ context.Context,
	runtime *ktfRuntime,
) (uint32, error) {
	metrics, err := runtime.wipicFontMetrics()
	if err != nil {
		return 0, err
	}
	return uint32(metrics.Ascent), nil
}

func ktfWIPICGraphicsGetFontDescent(
	_ context.Context,
	runtime *ktfRuntime,
) (uint32, error) {
	metrics, err := runtime.wipicFontMetrics()
	if err != nil {
		return 0, err
	}
	return uint32(metrics.Descent), nil
}

func (r *ktfRuntime) wipicFontMetrics() (shared.FontMetrics, error) {
	font, err := r.parameter(0)
	if err != nil {
		return shared.FontMetrics{}, err
	}
	fontID, err := r.ensureWIPICFontService(font)
	if err != nil {
		return shared.FontMetrics{}, err
	}
	return r.services.Text.Metrics(r.serviceOwner, fontID)
}

// WIPI-C font handles are plain integers rather than guest objects, so their
// shared text services are cached under a reserved key range that no guest
// allocation can produce.
const ktfWIPICFontServiceKey = uint32(0xffff0000)

func (r *ktfRuntime) ensureWIPICFontService(
	font uint32,
) (shared.ServiceID, error) {
	height := int32(fontHeight(font))
	var style shared.FontStyle
	if font&0x0100 != 0 {
		style |= shared.FontBold
	}
	if font&0x0200 != 0 {
		style |= shared.FontItalic
	}
	if font&0x0400 != 0 {
		style |= shared.FontUnderlined
	}
	key := ktfWIPICFontServiceKey | uint32(height)<<8 | uint32(style)
	if serviceID := r.fontServices[key]; serviceID != 0 {
		return serviceID, nil
	}
	serviceID, err := r.services.Text.CreateFont(
		r.serviceOwner,
		shared.FontDescriptor{
			Family: "aram-fallback",
			Size:   height,
			Style:  style,
		},
	)
	if err != nil {
		return 0, err
	}
	r.fontServices[key] = serviceID
	return serviceID, nil
}

// KTF passes M_Char runs in the handset's EUC-KR encoding and M_UCode runs as
// UTF-16LE. A negative length means the run is terminated instead of counted.
const ktfWIPICStringLimit = uint32(4096)

func (r *ktfRuntime) wipicText(
	address uint32,
	length int32,
	unicode bool,
) (string, error) {
	if address == 0 {
		return "", nil
	}
	unit, encoding := uint32(1), shared.EncodingEUCKR
	if unicode {
		unit, encoding = 2, shared.EncodingUTF16LE
	}
	limit := ktfWIPICStringLimit / unit
	count := uint32(0)
	if length >= 0 {
		count = uint32(length)
		if count > limit {
			return "", fmt.Errorf(
				"KTF WIPI-C string at 0x%08x spans %d units",
				address,
				count,
			)
		}
	} else {
		// Truncating an unterminated run would hand the decoder a partial
		// multi-byte sequence, so report the bad pointer instead.
		var element [2]byte
		for ; count < limit; count++ {
			if err := r.cpu.ReadMemory(
				address+count*unit,
				element[:unit],
			); err != nil {
				return "", fmt.Errorf(
					"read KTF WIPI-C string at 0x%08x: %w",
					address+count*unit,
					err,
				)
			}
			if element[0] == 0 && (unit == 1 || element[1] == 0) {
				break
			}
		}
		if count == limit {
			return "", fmt.Errorf(
				"KTF WIPI-C string at 0x%08x is not terminated within %d units",
				address,
				limit,
			)
		}
	}
	if count == 0 {
		return "", nil
	}
	data := make([]byte, count*unit)
	if err := r.cpu.ReadMemory(address, data); err != nil {
		return "", fmt.Errorf(
			"read KTF WIPI-C string at 0x%08x: %w",
			address,
			err,
		)
	}
	return r.services.Text.Decode(data, encoding)
}

func ktfWIPICGraphicsGetPixelFromRGB(
	_ context.Context,
	runtime *ktfRuntime,
) (uint32, error) {
	red, err := runtime.parameter(0)
	if err != nil {
		return 0, err
	}
	green, err := runtime.parameter(1)
	if err != nil {
		return 0, err
	}
	blue, err := runtime.parameter(2)
	if err != nil {
		return 0, err
	}
	return (red&0xff)>>3<<11 |
		(green&0xff)>>2<<5 |
		(blue&0xff)>>3, nil
}

func ktfWIPICGraphicsGetRGBFromPixel(
	_ context.Context,
	runtime *ktfRuntime,
) (uint32, error) {
	pixel, err := runtime.parameter(0)
	if err != nil {
		return 0, err
	}
	red := pixel >> 11 & 0x1f
	green := pixel >> 5 & 0x3f
	blue := pixel & 0x1f
	values := [...]uint32{
		red<<3 | red>>2,
		green<<2 | green>>4,
		blue<<3 | blue>>2,
	}
	for index, value := range values {
		output, parameterErr := runtime.parameter(uint32(index + 1))
		if parameterErr != nil {
			return 0, parameterErr
		}
		if output != 0 {
			if err := runtime.writeU32(output, value); err != nil {
				return 0, err
			}
		}
	}
	return pixel, nil
}

func ktfWIPICGraphicsGetDisplayInfo(
	_ context.Context,
	runtime *ktfRuntime,
) (uint32, error) {
	display, err := runtime.parameter(0)
	if err != nil {
		return 0, err
	}
	output, err := runtime.parameter(1)
	if err != nil || output == 0 || display > 1 {
		return 0, err
	}
	if runtime.frame == nil {
		runtime.frame = image.NewRGBA(image.Rect(0, 0, 240, 320))
	}
	width, height := runtime.frame.Bounds().Dx(), runtime.frame.Bounds().Dy()
	values := [...]uint32{
		16, 16, uint32(width), uint32(height), uint32(width * 2),
		1, 0xf800, 0x001f, 0x07e0,
	}
	if err := runtime.writeWords(output, values[:]); err != nil {
		return 0, err
	}
	return 1, nil
}

func ktfWIPICGraphicsFlushLCD(
	_ context.Context,
	runtime *ktfRuntime,
) (uint32, error) {
	framebuffer, err := runtime.parameter(1)
	if err != nil {
		return 0, err
	}
	if framebuffer == 0 {
		framebuffer = runtime.wipicScreenFramebuffer
	}
	return 0, runtime.presentWIPICFramebuffer(framebuffer)
}

func ktfWIPICGraphicsRepaint(
	_ context.Context,
	runtime *ktfRuntime,
) (uint32, error) {
	if _, err := runtime.parameter(0); err != nil {
		return 0, err
	}
	return 0, runtime.presentWIPICFramebuffer(runtime.wipicScreenFramebuffer)
}

func (r *ktfRuntime) presentWIPICFramebuffer(handle uint32) error {
	framebuffer := r.wipicFramebuffers[handle]
	if framebuffer == nil || r.frame == nil {
		return nil
	}
	if err := r.syncKTFWIPICFramebuffer(handle); err != nil {
		return err
	}
	surface := r.wipicSurfaceServices[handle]
	if surface == 0 {
		return fmt.Errorf("KTF WIPI-C framebuffer 0x%08x has no shared surface", handle)
	}
	presentationSurface := surface
	if !framebuffer.screen {
		screenHandle, err := r.ensureWIPICScreenFramebuffer()
		if err != nil {
			return err
		}
		screen := r.wipicFramebuffers[screenHandle]
		presentationSurface = r.wipicSurfaceServices[screenHandle]
		if screen == nil || presentationSurface == 0 {
			return errors.New("KTF WIPI-C screen framebuffer is unavailable")
		}
		width := min(framebuffer.width, screen.width)
		height := min(framebuffer.height, screen.height)
		if width > 0 && height > 0 {
			if err := r.services.Graphics.Blit(
				r.serviceOwner,
				presentationSurface,
				surface,
				0,
				0,
				shared.Rectangle{
					Width:  int32(width),
					Height: int32(height),
				},
			); err != nil {
				return fmt.Errorf(
					"flush KTF WIPI-C framebuffer 0x%08x to screen: %w",
					handle,
					err,
				)
			}
		}
	}
	if r.services.Graphics.Screen() != presentationSurface {
		if err := r.services.Graphics.SetScreen(
			r.serviceOwner,
			presentationSurface,
		); err != nil {
			return err
		}
	}
	frame, err := r.services.Graphics.Present(
		r.serviceOwner,
		presentationSurface,
		shared.Rectangle{},
	)
	if err != nil {
		return err
	}
	bounds := r.frame.Bounds()
	width := min(int(frame.Width), bounds.Dx())
	height := min(int(frame.Height), bounds.Dy())
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			offset := (y*int(frame.Width) + x) * 4
			r.frame.SetRGBA(
				bounds.Min.X+x,
				bounds.Min.Y+y,
				color.RGBA{
					R: frame.RGBA[offset+0],
					G: frame.RGBA[offset+1],
					B: frame.RGBA[offset+2],
					A: frame.RGBA[offset+3],
				},
			)
		}
	}
	r.presentCount++
	return nil
}

func (r *ktfRuntime) syncKTFWIPICFramebuffer(handle uint32) error {
	framebuffer := r.wipicFramebuffers[handle]
	serviceID := r.wipicSurfaceServices[handle]
	if framebuffer == nil || serviceID == 0 {
		return nil
	}
	data := make([]byte, framebuffer.stride*framebuffer.height)
	if err := r.cpu.ReadMemory(framebuffer.pixels, data); err != nil {
		return err
	}
	if err := r.services.Graphics.ReplacePixels(
		r.serviceOwner,
		serviceID,
		data,
	); err != nil {
		return fmt.Errorf(
			"sync KTF WIPI-C framebuffer 0x%08x: %w",
			handle,
			err,
		)
	}
	return nil
}

func modeStatus(procedure uint32) uint32 {
	if procedure&1 != 0 {
		return cpu.StatusThumb
	}
	return 0
}

func (r *ktfRuntime) inspectExecutable(address uint32) (ktfExecutable, error) {
	if !r.imagePointer(address, 40) {
		return ktfExecutable{}, fmt.Errorf(
			"KTF WipiExe pointer 0x%08x is outside client image",
			address,
		)
	}
	words, err := r.readWords(address, 10)
	if err != nil {
		return ktfExecutable{}, err
	}
	interfaceAddress := words[0]
	if !r.imagePointer(interfaceAddress, 32) {
		return ktfExecutable{}, fmt.Errorf(
			"KTF ExeInterface pointer 0x%08x is outside client image",
			interfaceAddress,
		)
	}
	interfaceWords, err := r.readWords(interfaceAddress, 8)
	if err != nil {
		return ktfExecutable{}, err
	}
	functionsAddress := interfaceWords[0]
	if !r.imagePointer(functionsAddress, 28) {
		return ktfExecutable{}, fmt.Errorf(
			"KTF ExeInterfaceFunctions pointer 0x%08x is outside client image",
			functionsAddress,
		)
	}
	functions, err := r.readWords(functionsAddress, 7)
	if err != nil {
		return ktfExecutable{}, err
	}
	nameAddress := words[1]
	if nameAddress == 0 {
		nameAddress = interfaceWords[1]
	}
	name, err := r.readImageString(nameAddress, 256)
	if err != nil {
		return ktfExecutable{}, err
	}
	executable := ktfExecutable{
		WipiExeAddress:      address,
		ExeInterfaceAddress: interfaceAddress,
		FunctionsAddress:    functionsAddress,
		Name:                name,
		ExecutableInit:      words[5],
		InterfaceInit:       functions[2],
		GetDefaultDLL:       functions[3],
		GetClass:            functions[4],
		InterfaceUnknown2:   functions[5],
		InterfaceUnknown3:   functions[6],
	}
	for label, procedure := range map[string]uint32{
		"WipiExe.fn_init":             executable.ExecutableInit,
		"ExeInterfaceFunctions.init":  executable.InterfaceInit,
		"ExeInterfaceFunctions.class": executable.GetClass,
	} {
		if procedure != 0 && !r.imagePointer(procedure&^1, 2) {
			return ktfExecutable{}, fmt.Errorf(
				"KTF %s pointer 0x%08x is outside client image",
				label,
				procedure,
			)
		}
	}
	return executable, nil
}

func (r *ktfRuntime) imagePointer(address, size uint32) bool {
	if address < ktfImageBase {
		return false
	}
	return uint64(address)+uint64(size) <=
		uint64(ktfImageBase)+uint64(r.imageSz)
}

func (r *ktfRuntime) readWords(address uint32, count int) ([]uint32, error) {
	data := make([]byte, count*4)
	if err := r.cpu.ReadMemory(address, data); err != nil {
		return nil, fmt.Errorf("read KTF structure at 0x%08x: %w", address, err)
	}
	words := make([]uint32, count)
	for index := range words {
		words[index] = binary.LittleEndian.Uint32(data[index*4 : index*4+4])
	}
	return words, nil
}

func (r *ktfRuntime) readU32(address uint32) (uint32, error) {
	var data [4]byte
	if err := r.cpu.ReadMemory(address, data[:]); err != nil {
		return 0, fmt.Errorf("read KTF word at 0x%08x: %w", address, err)
	}
	return binary.LittleEndian.Uint32(data[:]), nil
}

func (r *ktfRuntime) writeU32(address, value uint32) error {
	var data [4]byte
	binary.LittleEndian.PutUint32(data[:], value)
	if err := r.cpu.WriteMemory(address, data[:]); err != nil {
		return fmt.Errorf("write KTF word at 0x%08x: %w", address, err)
	}
	return nil
}

func (r *ktfRuntime) readImageString(address, limit uint32) (string, error) {
	if address == 0 {
		return "", nil
	}
	if !r.imagePointer(address, 1) {
		return "", fmt.Errorf("KTF string pointer 0x%08x is outside client image", address)
	}
	end := min(uint64(ktfImageBase)+uint64(r.imageSz), uint64(address)+uint64(limit))
	data := make([]byte, int(end-uint64(address)))
	if err := r.cpu.ReadMemory(address, data); err != nil {
		return "", err
	}
	if index := bytes.IndexByte(data, 0); index >= 0 {
		return string(data[:index]), nil
	}
	return "", fmt.Errorf("KTF string at 0x%08x is not terminated within %d bytes", address, limit)
}

func (r *ktfRuntime) readCString(address, limit uint32) (string, error) {
	if address == 0 {
		return "", errors.New("KTF string pointer is null")
	}
	data := make([]byte, 0, min(limit, 64))
	var chunk [64]byte
	for offset := uint32(0); offset < limit; {
		// Read in blocks. A per-byte ReadMemory takes the backend lock and
		// resolves the mapped region once per character, which dominated
		// Java method-name resolution on KTF titles.
		size := min(uint32(len(chunk)), limit-offset)
		var err error
		for size > 0 {
			if err = r.cpu.ReadMemory(address+offset, chunk[:size]); err == nil {
				break
			}
			// The block may straddle the end of a mapped region. Narrow it
			// until it fits so a string that terminates before the boundary
			// still reads, and only report the fault once nothing fits.
			size /= 2
		}
		if size == 0 {
			return "", fmt.Errorf(
				"read KTF string at 0x%08x: %w",
				address+offset,
				err,
			)
		}
		if index := bytes.IndexByte(chunk[:size], 0); index >= 0 {
			return string(append(data, chunk[:index]...)), nil
		}
		data = append(data, chunk[:size]...)
		offset += size
	}
	return "", fmt.Errorf("KTF string at 0x%08x is not terminated within %d bytes", address, limit)
}
