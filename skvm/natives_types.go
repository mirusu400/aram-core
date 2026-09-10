package skvm

import (
	"fmt"
	"time"

	shared "github.com/mirusu400/aram-core/runtime"
)

type stringBufferState struct {
	value string
}

type inputStreamState struct {
	data       []byte
	offset     int
	mark       int
	closed     bool
	connection uint32
}

type randomState struct {
	stream string
}

type threadState struct {
	target  uint32
	started bool
	active  bool
	wakeAt  time.Duration
	// blockedClip is the clip whose playback this thread waits on. SK-VM
	// AudioClip.play and loop block the calling thread until the clip stops,
	// which is why titles start them on a worker thread and close the clip
	// right after: the close only runs once the sound has finished.
	blockedClip  shared.ServiceID
	continuation []*frame
}

type threadYield struct {
	delay time.Duration
}

// CanvasHeightInset16Quirk models SKT handsets whose MIDP Canvas reported the
// client area without the 16-pixel system strip even though drawing still
// targeted the complete framebuffer. Some games deliberately add the strip
// back when allocating their full-screen backbuffer.
const CanvasHeightInset16Quirk = "skvm.canvas-height-inset-16"

func (e *threadYield) Error() string {
	return fmt.Sprintf("SKVM thread yielded for %s", e.delay)
}

type recordStoreState struct {
	name string
	id   shared.ServiceID
}

type xFileState struct {
	name   string
	data   []byte
	offset int
}

type xTextFieldState struct {
	text  string
	focus bool
}

type outputStreamState struct {
	data       []byte
	file       *xFileState
	name       string
	connection uint32
}

type audioClipState struct {
	clip shared.ServiceID
}

type inputStreamReaderState struct {
	stream      uint32
	encoding    shared.TextEncoding
	chars       []uint16
	offset      int
	initialized bool
	closed      bool
}

type imageState struct {
	width   int
	height  int
	asset   shared.ServiceID
	surface shared.ServiceID
}

type fontState struct {
	font shared.ServiceID
}

type graphicsState struct {
	width   int
	height  int
	surface shared.ServiceID
	font    shared.ServiceID
	color   uint32
	stroke  int32
}
