package gnex32

import (
	"bytes"
	"fmt"
	"image"
	"image/color"
	"time"

	"github.com/mirusu400/aram-core/loader/gnex"
	shared "github.com/mirusu400/aram-core/runtime"
	"golang.org/x/text/encoding/korean"
	"golang.org/x/text/transform"
)

// Identity supplies the two handset strings read during GNEX startup.
type Identity struct {
	MIN    string
	UserID string
}

// Runtime hosts the observed version-4 services and preserves VM and media
// state across event dispatches.
type Runtime struct {
	vm          *VM
	display     *Display
	identity    Identity
	tick        uint64
	timers      [3]runtimeTimer
	presents    uint64
	nv          [16]uint32
	randomState uint32
	paint       color.RGBA
	fontSize    uint32
	fontColor   color.RGBA
	fontAlign   uint32
	fontBack    color.RGBA
	text        *shared.Text
}

type runtimeTimer struct {
	due    uint64 // units of 1/60 millisecond
	period uint32
	repeat bool
	active bool
}

func NewRuntime(image gnex.GNEX32Image, identity Identity) (*Runtime, error) {
	if identity.MIN == "" || identity.UserID == "" {
		return nil, fmt.Errorf("gnex32: handset identity is incomplete")
	}
	services, err := shared.NewServices(shared.DefaultConfig())
	if err != nil {
		return nil, err
	}
	runtime := &Runtime{display: NewDisplay(120, 149), identity: identity, text: services.Text,
		fontColor: monoColor(3), fontBack: monoColor(4)}
	vm, err := NewWithServices(image, runtime.service)
	if err != nil {
		return nil, err
	}
	runtime.vm = vm
	// GNEX reserves symbols 4 and 5 for swWidth and swHeight. The handset
	// supplies these values before main; zero leaves guest layout arithmetic
	// outside the visible LCD even when the host framebuffer is sized correctly.
	if err := vm.memory.WriteWord(4, 0, 120); err != nil {
		return nil, err
	}
	if err := vm.memory.WriteWord(5, 0, 149); err != nil {
		return nil, err
	}
	vm.copyImage = runtime.copyImage
	vm.copyImageDir = runtime.copyImageDir
	vm.copyImageEx = runtime.copyImageEx
	return runtime, nil
}

func (r *Runtime) RunMain(budget uint64) error {
	if err := r.vm.Run(budget); err != nil {
		return err
	}
	if !r.vm.Halted() {
		return ErrBudget
	}
	return nil
}

// StepFrame advances the host's 60 Hz clock and delivers one due GNEX timer.
func (r *Runtime) StepFrame(budget uint64) error {
	r.tick++
	for slot := range r.timers {
		timer := &r.timers[slot]
		if !timer.active || r.tick*1000 < timer.due {
			continue
		}
		if timer.repeat {
			for timer.due <= r.tick*1000 {
				timer.due += uint64(timer.period) * 60
			}
		} else {
			timer.active = false
		}
		if err := r.vm.BeginEvent(2, uint32(slot)); err != nil {
			return err
		}
		if err := r.vm.Run(budget); err != nil {
			return err
		}
	}
	return nil
}

func (r *Runtime) KeyPressed(code uint16, budget uint64) error {
	if err := r.vm.BeginEvent(3, uint32(code)); err != nil {
		return err
	}
	return r.vm.Run(budget)
}

func (r *Runtime) Frame() image.Image   { return r.display.Frame() }
func (r *Runtime) PresentCount() uint64 { return r.presents }
func (r *Runtime) PC() int              { return r.vm.PC() }

func (r *Runtime) advanceSystemFrame() error {
	r.presents++
	for symbol, value := range []uint32{uint32(r.presents), uint32(r.presents % 2), uint32(r.presents % 3), uint32(r.presents % 6)} {
		if err := r.vm.memory.WriteWord(6+symbol, 0, value); err != nil {
			return err
		}
	}
	return nil
}

func (r *Runtime) copyImage(x, y int32, index int) error {
	resource, _, err := r.vm.media.Read(index)
	if err != nil {
		return err
	}
	return r.display.CopyImage(x, y, resource)
}

func (r *Runtime) copyImageDir(x, y int32, index int, direction uint32) error {
	resource, _, err := r.vm.media.Read(index)
	if err != nil {
		return err
	}
	return r.display.CopyImageDir(x, y, resource, direction)
}

func (r *Runtime) copyImageEx(x, y int32, index int, alpha, direction uint32) error {
	resource, _, err := r.vm.media.Read(index)
	if err != nil {
		return err
	}
	return r.display.CopyImageEx(x, y, resource, alpha, direction)
}

func terminated(value string) []byte { return append([]byte(value), 0) }

func (r *Runtime) drawStr(x, y int32, mediaIndex int, solid bool) error {
	data, _, err := r.vm.media.Read(mediaIndex)
	if err != nil {
		return err
	}
	if end := bytes.IndexByte(data, 0); end >= 0 {
		data = data[:end]
	}
	if len(data) == 0 {
		return nil
	}
	decoded, _, err := transform.Bytes(korean.EUCKR.NewDecoder(), data)
	if err != nil {
		return err
	}
	sizes := [...]int32{6, 8, 12, 24, 16, 32}
	if r.fontSize >= uint32(len(sizes)) {
		return fmt.Errorf("gnex32: unsupported font size %d", r.fontSize)
	}
	const textOwner shared.OwnerID = 1
	fontID, err := r.text.EnsureFont(textOwner, shared.FontDescriptor{Family: "aram-fallback", Size: sizes[r.fontSize]})
	if err != nil {
		return err
	}
	glyphs := make([]shared.Glyph, 0, len(decoded))
	width := int32(0)
	for _, character := range string(decoded) {
		glyph, err := r.text.Glyph(textOwner, fontID, character)
		if err != nil {
			return err
		}
		glyphs = append(glyphs, glyph)
		width += glyph.Advance
	}
	if r.fontAlign == 1 {
		x -= width / 2
	} else if r.fontAlign == 2 {
		x -= width
	}
	if solid && r.fontBack.A != 0 && width > 0 {
		r.display.FillRect(x, y, x+width-1, y+sizes[r.fontSize]-1, r.fontBack)
	}
	cursor := x
	for _, glyph := range glyphs {
		for row := int32(0); row < glyph.Height; row++ {
			for column := int32(0); column < glyph.Width; column++ {
				alpha := glyph.Alpha[row*glyph.Width+column]
				if alpha == 0 {
					continue
				}
				px, py := int(cursor+glyph.BearingX+column), int(y+glyph.BearingY+row)
				if image.Pt(px, py).In(r.display.clip) {
					r.display.working.SetRGBA(px, py, r.fontColor)
				}
			}
		}
		cursor += glyph.Advance
	}
	return nil
}

func (r *Runtime) service(id uint16, stack []uint32) (ServiceResult, error) {
	last := len(stack) - 1
	switch id {
	case 1: // GetSysMin(string)
		if last < 0 {
			return ServiceResult{}, ErrStackUnderflow
		}
		if err := r.vm.media.Replace(int(stack[last]), terminated(r.identity.MIN)); err != nil {
			return ServiceResult{}, err
		}
		return ServiceResult{Pop: 1}, nil
	case 2: // GetSysUserID(string)
		if last < 0 {
			return ServiceResult{}, ErrStackUnderflow
		}
		if err := r.vm.media.Replace(int(stack[last]), terminated(r.identity.UserID)); err != nil {
			return ServiceResult{}, err
		}
		return ServiceResult{Pop: 1, Push: []uint32{0}}, nil
	case 4, 5: // GetDate/GetTime(int[4])
		if last < 0 {
			return ServiceResult{}, ErrStackUnderflow
		}
		ref, ok := r.vm.refs[last]
		if !ok || ref.element != 0 {
			return ServiceResult{}, ErrInvalidLValue
		}
		now := time.Now()
		values := []uint32{uint32(now.Year()), uint32(now.Month()), uint32(now.Day()), uint32(now.Weekday())}
		if id == 5 {
			values = []uint32{uint32(now.Hour()), uint32(now.Minute()), uint32(now.Second()), uint32(now.Nanosecond() / 1_000_000)}
		}
		for i, value := range values {
			if err := r.vm.memory.WriteWord(ref.symbol, i, value); err != nil {
				return ServiceResult{}, err
			}
		}
		return ServiceResult{Pop: 1}, nil
	case 0x9f: // StrInit(string, size)
		if last < 1 {
			return ServiceResult{}, ErrStackUnderflow
		}
		if stack[last] > 1<<20 {
			return ServiceResult{}, fmt.Errorf("gnex32: StrInit size exceeds limit")
		}
		if err := r.vm.media.Replace(int(stack[last-1]), make([]byte, int(stack[last])+1)); err != nil {
			return ServiceResult{}, err
		}
		return ServiceResult{Pop: 2}, nil
	case 0xa0: // StrLen(string)
		if last < 0 {
			return ServiceResult{}, ErrStackUnderflow
		}
		data, _, err := r.vm.media.Read(int(stack[last]))
		if err != nil {
			return ServiceResult{}, err
		}
		length := bytes.IndexByte(data, 0)
		if length < 0 {
			length = len(data)
		}
		return ServiceResult{Pop: 1, Push: []uint32{uint32(length)}}, nil
	case 0xa1: // StrCpy(destination, source)
		if last < 1 {
			return ServiceResult{}, ErrStackUnderflow
		}
		data, _, err := r.vm.media.Read(int(stack[last]))
		if err != nil {
			return ServiceResult{}, err
		}
		if err := r.vm.media.Replace(int(stack[last-1]), data); err != nil {
			return ServiceResult{}, err
		}
		return ServiceResult{Pop: 2}, nil
	case 0xa2: // StrSub(destination, source, start, length)
		if last < 3 {
			return ServiceResult{}, ErrStackUnderflow
		}
		data, _, err := r.vm.media.Read(int(stack[last-2]))
		if err != nil {
			return ServiceResult{}, err
		}
		if end := bytes.IndexByte(data, 0); end >= 0 {
			data = data[:end]
		}
		start, length := int32(stack[last-1]), int32(stack[last])
		if start < 0 || length < 0 {
			return ServiceResult{}, fmt.Errorf("gnex32: negative StrSub range %d:%d", start, length)
		}
		begin := min(int(start), len(data))
		end := begin + min(int(length), len(data)-begin)
		if err := r.vm.media.Replace(int(stack[last-3]), terminated(string(data[begin:end]))); err != nil {
			return ServiceResult{}, err
		}
		return ServiceResult{Pop: 4}, nil
	case 0xa4: // StrCmp(string, string)
		if last < 1 {
			return ServiceResult{}, ErrStackUnderflow
		}
		left, _, err := r.vm.media.Read(int(stack[last-1]))
		if err != nil {
			return ServiceResult{}, err
		}
		right, _, err := r.vm.media.Read(int(stack[last]))
		if err != nil {
			return ServiceResult{}, err
		}
		if end := bytes.IndexByte(left, 0); end >= 0 {
			left = left[:end]
		}
		if end := bytes.IndexByte(right, 0); end >= 0 {
			right = right[:end]
		}
		return ServiceResult{Pop: 2, Push: []uint32{uint32(int32(bytes.Compare(left, right)))}}, nil
	case 0xa5: // GetChar(media, index)
		if last < 1 {
			return ServiceResult{}, ErrStackUnderflow
		}
		// The virus menu reads its zero-initialized sentinel at an index
		// below the start of a lookup table. Native GNEX leaves this read
		// unspecified; return zero while keeping the media store bounds-safe.
		if int32(stack[last]) < 0 {
			return ServiceResult{Pop: 2, Push: []uint32{0}}, nil
		}
		if resource, flags, err := r.vm.media.Read(int(stack[last-1])); err == nil &&
			flags&1 != 0 && int(stack[last]) == len(resource) {
			return ServiceResult{Pop: 2, Push: []uint32{0}}, nil
		}
		value, err := r.vm.media.MediaByte(int(stack[last-1]), int(stack[last]))
		if err != nil {
			return ServiceResult{}, err
		}
		return ServiceResult{Pop: 2, Push: []uint32{uint32(value)}}, nil
	case 0xa6: // PutChar(media, index, value)
		if last < 2 {
			return ServiceResult{}, ErrStackUnderflow
		}
		if int32(stack[last-1]) < 0 {
			return ServiceResult{Pop: 3}, nil
		}
		if err := r.vm.media.SetMediaByte(int(stack[last-2]), int(stack[last-1]), byte(stack[last])); err != nil {
			return ServiceResult{}, err
		}
		return ServiceResult{Pop: 3}, nil
	case 0xdd: // ArrayToVar(int[], value, count, operation)
		if last < 3 {
			return ServiceResult{}, ErrStackUnderflow
		}
		ref, ok := r.vm.refs[last-3]
		if !ok {
			return ServiceResult{}, ErrInvalidLValue
		}
		value, count, operation := stack[last-2], stack[last-1], stack[last]
		if count > 4096 || operation > 3 {
			return ServiceResult{}, fmt.Errorf("gnex32: unsupported ArrayToVar count or operation")
		}
		for i := uint32(0); i < count; i++ {
			old, err := r.vm.memory.ReadWord(ref.symbol, ref.element+int(i))
			if err != nil {
				return ServiceResult{}, err
			}
			next := value
			switch operation {
			case 1:
				next = old + value
			case 2:
				next = old - value
			case 3:
				next = old * value
			}
			if err := r.vm.memory.WriteWord(ref.symbol, ref.element+int(i), next); err != nil {
				return ServiceResult{}, err
			}
		}
		return ServiceResult{Pop: 4}, nil
	case 0x93: // GetUserNV(int[], count)
		if last < 1 {
			return ServiceResult{}, ErrStackUnderflow
		}
		ref, ok := r.vm.refs[last-1]
		if !ok {
			return ServiceResult{}, ErrInvalidLValue
		}
		if stack[last] > uint32(len(r.nv)) {
			return ServiceResult{}, fmt.Errorf("gnex32: user NV read exceeds 16 words")
		}
		for i := uint32(0); i < stack[last]; i++ {
			if err := r.vm.memory.WriteWord(ref.symbol, ref.element+int(i), r.nv[i]); err != nil {
				return ServiceResult{}, err
			}
		}
		return ServiceResult{Pop: 2}, nil
	case 0x94: // PutUserNV(int[], count)
		if last < 1 {
			return ServiceResult{}, ErrStackUnderflow
		}
		ref, ok := r.vm.refs[last-1]
		if !ok {
			return ServiceResult{}, ErrInvalidLValue
		}
		if stack[last] > uint32(len(r.nv)) {
			return ServiceResult{}, fmt.Errorf("gnex32: user NV write exceeds 16 words")
		}
		next := r.nv
		for i := uint32(0); i < stack[last]; i++ {
			value, err := r.vm.memory.ReadWord(ref.symbol, ref.element+int(i))
			if err != nil {
				return ServiceResult{}, err
			}
			next[i] = value
		}
		r.nv = next
		return ServiceResult{Pop: 2}, nil
	case 0x3a: // ClearWhite
		r.display.Clear(color.White)
		return ServiceResult{}, nil
	case 0x3b: // ClearBlack
		r.display.Clear(color.Black)
		return ServiceResult{}, nil
	case 0x38: // ClearRGB(red, green, blue)
		if last < 2 {
			return ServiceResult{}, ErrStackUnderflow
		}
		r.display.Clear(color.RGBA{R: byte(stack[last-2]), G: byte(stack[last-1]), B: byte(stack[last]), A: 255})
		return ServiceResult{Pop: 3}, nil
	case 0x26: // SetColor(palette index)
		if last < 0 {
			return ServiceResult{}, ErrStackUnderflow
		}
		r.paint = monoColor(byte(stack[last]))
		return ServiceResult{Pop: 1}, nil
	case 0x27: // SetColorRGB(red, green, blue)
		if last < 2 {
			return ServiceResult{}, ErrStackUnderflow
		}
		r.paint = color.RGBA{R: byte(stack[last-2]), G: byte(stack[last-1]), B: byte(stack[last]), A: 255}
		return ServiceResult{Pop: 3}, nil
	case 0x28: // SetFontType(size, foreground, background, alignment)
		if last < 3 {
			return ServiceResult{}, ErrStackUnderflow
		}
		r.fontSize = stack[last-3]
		r.fontColor = monoColor(byte(stack[last-2]))
		r.fontBack = monoColor(byte(stack[last-1]))
		r.fontAlign = stack[last]
		return ServiceResult{Pop: 4}, nil
	case 0x2f: // SetPaletteColorRGB(image, index, red, green, blue)
		if last < 4 {
			return ServiceResult{}, ErrStackUnderflow
		}
		status := uint32(0)
		if !r.vm.media.SetPaletteColorRGB(int(stack[last-4]), int(int32(stack[last-3])),
			byte(stack[last-2]), byte(stack[last-1]), byte(stack[last])) {
			status = ^uint32(0)
		}
		return ServiceResult{Pop: 5, Push: []uint32{status}}, nil
	case 0x46: // FillRect(x1, y1, x2, y2)
		if last < 3 {
			return ServiceResult{}, ErrStackUnderflow
		}
		r.display.FillRect(int32(stack[last-3]), int32(stack[last-2]), int32(stack[last-1]), int32(stack[last]), r.paint)
		return ServiceResult{Pop: 4}, nil
	case 0x42: // DrawLine(x1, y1, x2, y2)
		if last < 3 {
			return ServiceResult{}, ErrStackUnderflow
		}
		r.display.DrawLine(int32(stack[last-3]), int32(stack[last-2]), int32(stack[last-1]), int32(stack[last]), r.paint)
		return ServiceResult{Pop: 4}, nil
	case 0x43: // DrawHLine(x1, x2, y)
		if last < 2 {
			return ServiceResult{}, ErrStackUnderflow
		}
		y := int32(stack[last])
		r.display.DrawLine(int32(stack[last-2]), y, int32(stack[last-1]), y, r.paint)
		return ServiceResult{Pop: 3}, nil
	case 0x44: // DrawVLine(x, y1, y2)
		if last < 2 {
			return ServiceResult{}, ErrStackUnderflow
		}
		x := int32(stack[last-2])
		r.display.DrawLine(x, int32(stack[last-1]), x, int32(stack[last]), r.paint)
		return ServiceResult{Pop: 3}, nil
	case 0x47: // FillRectEx(x1, y1, x2, y2, alpha)
		if last < 4 {
			return ServiceResult{}, ErrStackUnderflow
		}
		r.display.FillRectAlpha(int32(stack[last-4]), int32(stack[last-3]), int32(stack[last-2]), int32(stack[last-1]), r.paint, stack[last])
		return ServiceResult{Pop: 5}, nil
	case 0x48, 0x49: // DrawRectRound / FillRectRound(x1, y1, x2, y2, radius)
		if last < 4 {
			return ServiceResult{}, ErrStackUnderflow
		}
		x1, y1, x2, y2 := int32(stack[last-4]), int32(stack[last-3]), int32(stack[last-2]), int32(stack[last-1])
		if id == 0x48 {
			r.display.DrawRectRound(x1, y1, x2, y2, int32(stack[last]), r.paint)
		} else {
			r.display.FillRectRound(x1, y1, x2, y2, int32(stack[last]), r.paint)
		}
		return ServiceResult{Pop: 5}, nil
	case 0x51: // SaveLCD()
		r.display.SaveLCD()
		return ServiceResult{}, nil
	case 0x52: // RestoreLCD()
		r.display.RestoreLCD()
		return ServiceResult{}, nil
	case 0x36: // Clear(palette index)
		if last < 0 {
			return ServiceResult{}, ErrStackUnderflow
		}
		r.display.Clear(monoColor(byte(stack[last])))
		return ServiceResult{Pop: 1}, nil
	case 0x22: // SetClip(x1, y1, x2, y2)
		if last < 3 {
			return ServiceResult{}, ErrStackUnderflow
		}
		r.display.SetClip(int32(stack[last-3]), int32(stack[last-2]), int32(stack[last-1]), int32(stack[last]))
		return ServiceResult{Pop: 4}, nil
	case 0x23: // ResetClip()
		r.display.ResetClip()
		return ServiceResult{}, nil
	case 0x55, 0x56: // DrawStr/DrawStrSolid(x, y, string)
		if last < 2 {
			return ServiceResult{}, ErrStackUnderflow
		}
		if err := r.drawStr(int32(stack[last-2]), int32(stack[last-1]), int(stack[last]), id == 0x56); err != nil {
			return ServiceResult{}, err
		}
		return ServiceResult{Pop: 3}, nil
	case 0x5f: // Flush
		r.display.Flush()
		return ServiceResult{}, r.advanceSystemFrame()
	case 0x60: // FlushPartial(x1, y1, x2, y2)
		if last < 3 {
			return ServiceResult{}, ErrStackUnderflow
		}
		r.display.Flush()
		return ServiceResult{Pop: 4}, r.advanceSystemFrame()
	case 0xc8: // RandSeed(seed)
		if last < 0 {
			return ServiceResult{}, ErrStackUnderflow
		}
		r.randomState = stack[last]
		return ServiceResult{Pop: 1}, nil
	case 0xc9: // Rand(min, max), max excluded
		if last < 1 {
			return ServiceResult{}, ErrStackUnderflow
		}
		minimum, maximum := int32(stack[last-1]), int32(stack[last])
		if maximum == minimum {
			return ServiceResult{Pop: 2, Push: []uint32{uint32(minimum)}}, nil
		}
		if maximum < minimum {
			return ServiceResult{}, fmt.Errorf("gnex32: invalid Rand bounds %d..%d", minimum, maximum)
		}
		r.randomState = r.randomState*1664525 + 1013904223
		value := minimum + int32(uint32(r.randomState)%uint32(int64(maximum)-int64(minimum)))
		return ServiceResult{Pop: 2, Push: []uint32{uint32(value)}}, nil
	case 0xcb: // Abs(value)
		if last < 0 {
			return ServiceResult{}, ErrStackUnderflow
		}
		value := int32(stack[last])
		if value < 0 {
			value = -value
		}
		return ServiceResult{Pop: 1, Push: []uint32{uint32(value)}}, nil
	case 0x25, 0x29, 0x90, 0x91: // Gamma, Font, KeyTone, BackLight
		if last < 0 {
			return ServiceResult{}, ErrStackUnderflow
		}
		return ServiceResult{Pop: 1}, nil
	case 0x82, 0x83: // PlaySound(media), StopSound(); audio output pending
		if id == 0x82 {
			if last < 0 {
				return ServiceResult{}, ErrStackUnderflow
			}
			return ServiceResult{Pop: 1}, nil
		}
		return ServiceResult{}, nil
	case 0x95, 0x96, 0x97: // SetTimer0/1/2(periodMs, onceOrRepeat)
		if last < 1 {
			return ServiceResult{}, ErrStackUnderflow
		}
		period := stack[last-1]
		if period > 60000 {
			return ServiceResult{}, fmt.Errorf("gnex32: timer period exceeds limit")
		}
		timer := &r.timers[id-0x95]
		timer.period = period
		timer.repeat = stack[last] != 0
		timer.active = period != 0
		timer.due = r.tick*1000 + uint64(period)*60
		return ServiceResult{Pop: 2}, nil
	case 0xd8: // Min(int, int)
		if last < 1 {
			return ServiceResult{}, ErrStackUnderflow
		}
		value := stack[last-1]
		if int32(stack[last]) < int32(value) {
			value = stack[last]
		}
		return ServiceResult{Pop: 2, Push: []uint32{value}}, nil
	case 0x98, 0x99, 0x9a: // ResetTimer0/1/2()
		r.timers[id-0x98].active = false
		return ServiceResult{}, nil
	default:
		return ServiceResult{}, &UnsupportedServiceError{ID: id}
	}
}
