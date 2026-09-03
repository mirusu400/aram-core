package runtime

import (
	"crypto/sha256"
	"fmt"
	"image"
	"math"
	"sort"
)

type PixelFormat uint8

const (
	PixelRGBA8888 PixelFormat = iota + 1
	PixelARGB8888
	PixelXRGB8888
	PixelRGB565
	PixelRGB555
	PixelGray8
	PixelIndexed8
	// PixelBGRX8888 stores a little-endian WIPI 0x00RRGGBB word as
	// B, G, R, X bytes while presenting canonical RGBA.
	PixelBGRX8888
)

func (f PixelFormat) Valid() bool {
	return f >= PixelRGBA8888 && f <= PixelBGRX8888
}

func (f PixelFormat) BytesPerPixel() int {
	switch f {
	case PixelRGBA8888, PixelARGB8888, PixelXRGB8888, PixelBGRX8888:
		return 4
	case PixelRGB565, PixelRGB555:
		return 2
	case PixelGray8, PixelIndexed8:
		return 1
	default:
		return 0
	}
}

type RasterOperation uint8

const (
	RasterCopy RasterOperation = iota
	RasterXOR
	RasterAND
	RasterOR
)

func (r RasterOperation) Valid() bool {
	return r <= RasterOR
}

type SurfaceDescriptor struct {
	Width       int32
	Height      int32
	Stride      int32
	Format      PixelFormat
	Palette     []Color
	Transparent *Color
}

func (d SurfaceDescriptor) Validate(limits GraphicsLimits) error {
	if d.Width <= 0 || d.Height <= 0 ||
		d.Width > limits.MaxWidth || d.Height > limits.MaxHeight ||
		!d.Format.Valid() {
		return fmt.Errorf(
			"%w: invalid surface geometry %dx%d or format %d",
			ErrInvalidArgument,
			d.Width,
			d.Height,
			d.Format,
		)
	}
	minimumStride := int64(d.Width) * int64(d.Format.BytesPerPixel())
	if d.Stride == 0 {
		d.Stride = int32(minimumStride)
	}
	if int64(d.Stride) < minimumStride {
		return fmt.Errorf("%w: surface stride %d is shorter than %d", ErrInvalidArgument, d.Stride, minimumStride)
	}
	pixels := uint64(d.Width) * uint64(d.Height)
	bytes := uint64(d.Stride) * uint64(d.Height)
	if pixels > limits.MaxPixels || bytes > limits.MaxBytes {
		return fmt.Errorf("%w: surface allocation exceeds graphics limits", ErrLimitExceeded)
	}
	if d.Format == PixelIndexed8 {
		if len(d.Palette) == 0 || len(d.Palette) > 256 {
			return fmt.Errorf("%w: indexed surface palette has %d entries", ErrInvalidArgument, len(d.Palette))
		}
	} else if len(d.Palette) != 0 {
		return fmt.Errorf("%w: non-indexed surface has a palette", ErrInvalidArgument)
	}
	return nil
}

type GraphicsLimits struct {
	MaxSurfaces uint32
	MaxWidth    int32
	MaxHeight   int32
	MaxPixels   uint64
	MaxBytes    uint64
}

func DefaultGraphicsLimits() GraphicsLimits {
	return GraphicsLimits{
		MaxSurfaces: 1024,
		MaxWidth:    4096,
		MaxHeight:   4096,
		MaxPixels:   16 * 1024 * 1024,
		MaxBytes:    128 << 20,
	}
}

func (l GraphicsLimits) Validate() error {
	if l.MaxSurfaces == 0 || l.MaxWidth <= 0 || l.MaxHeight <= 0 ||
		l.MaxPixels == 0 || l.MaxBytes == 0 {
		return fmt.Errorf("%w: invalid graphics limits", ErrInvalidArgument)
	}
	return nil
}

type SurfaceDrawState struct {
	Clip         Rectangle
	TranslateX   int32
	TranslateY   int32
	Raster       RasterOperation
	GlobalAlpha  uint8
	Transparency bool
}

func defaultDrawState(width, height int32) SurfaceDrawState {
	return SurfaceDrawState{
		Clip:        Rectangle{Width: width, Height: height},
		Raster:      RasterCopy,
		GlobalAlpha: 0xff,
	}
}

type surface struct {
	id         ServiceID
	owner      OwnerID
	descriptor SurfaceDescriptor
	state      SurfaceDrawState
	pixels     []byte
	dirty      Rectangle
}

type SurfaceState struct {
	ID         ServiceID
	Owner      OwnerID
	Descriptor SurfaceDescriptor
	Draw       SurfaceDrawState
	Pixels     []byte
	Dirty      Rectangle
}

type GraphicsState struct {
	Limits          GraphicsLimits
	Screen          ServiceID
	PresentSequence uint64
	Surfaces        []SurfaceState
	LastFrame       FrameSnapshot
}

type FrameSnapshot struct {
	SurfaceID ServiceID
	Sequence  uint64
	Width     int32
	Height    int32
	Dirty     Rectangle
	RGBA      []byte
	Hash      [sha256.Size]byte
}

// FramePresentation is the immutable, pixel-free identity of a committed
// frame. Runtime adapters use it when the graphics service should retain the
// pixels; public snapshot APIs continue returning detached RGBA copies.
// FramePresentation identifies a committed frame without its pixels. It
// carries no digest: Sequence already names the frame exactly, and computing a
// digest on the commit path costs more than every caller of this type saves.
// A caller that needs the digest asks LastFramePresentation, which computes it
// once for the frame on screen.
type FramePresentation struct {
	SurfaceID ServiceID
	Sequence  uint64
	Width     int32
	Height    int32
	Dirty     Rectangle
}

// Point is a guest-neutral raster coordinate.
type Point struct {
	X int32
	Y int32
}

func (f FrameSnapshot) Image() *image.RGBA {
	result := image.NewRGBA(image.Rect(0, 0, int(f.Width), int(f.Height)))
	copy(result.Pix, f.RGBA)
	return result
}

func cloneFrame(frame FrameSnapshot) FrameSnapshot {
	frame.RGBA = cloneBytes(frame.RGBA)
	return frame
}

// Graphics owns storage-format-preserving surfaces, deterministic raster
// operations, dirty tracking, and immutable RGBA presentation snapshots.
type Graphics struct {
	registry        *Registry
	limits          GraphicsLimits
	surfaces        map[ServiceID]*surface
	screen          ServiceID
	presentSequence uint64
	lastFrame       FrameSnapshot
	// lastFrameHashed records whether lastFrame.Hash has been computed for the
	// pixels it holds. Hashing is deferred because a guest presents far more
	// often than a host asks which frame it is looking at - a KTF title runs
	// its paint loop at a thousand presents a second, and hashing a 240x320
	// frame on each of them spent an eighth of the emulator's CPU on a digest
	// nobody read.
	lastFrameHashed bool
}

func NewGraphics(registry *Registry, limits GraphicsLimits) (*Graphics, error) {
	if registry == nil {
		registry = NewRegistry(0)
	}
	if limits == (GraphicsLimits{}) {
		limits = DefaultGraphicsLimits()
	}
	if err := limits.Validate(); err != nil {
		return nil, err
	}
	return &Graphics{
		registry: registry,
		limits:   limits,
		surfaces: make(map[ServiceID]*surface),
	}, nil
}

func (g *Graphics) CreateSurface(owner OwnerID, descriptor SurfaceDescriptor) (ServiceID, error) {
	if descriptor.Stride == 0 && descriptor.Format.Valid() && descriptor.Width > 0 {
		stride := int64(descriptor.Width) * int64(descriptor.Format.BytesPerPixel())
		if stride <= math.MaxInt32 {
			descriptor.Stride = int32(stride)
		}
	}
	if err := descriptor.Validate(g.limits); err != nil {
		return 0, err
	}
	if uint32(len(g.surfaces)) >= g.limits.MaxSurfaces {
		return 0, fmt.Errorf("%w: surface count reached %d", ErrLimitExceeded, g.limits.MaxSurfaces)
	}
	id, err := g.registry.Create(owner, KindSurface)
	if err != nil {
		return 0, err
	}
	byteCount := int64(descriptor.Stride) * int64(descriptor.Height)
	if byteCount > int64(math.MaxInt) {
		_ = g.registry.Destroy(id, owner, KindSurface)
		return 0, fmt.Errorf("%w: surface allocation exceeds host address space", ErrLimitExceeded)
	}
	descriptor.Palette = append([]Color(nil), descriptor.Palette...)
	if descriptor.Transparent != nil {
		color := *descriptor.Transparent
		descriptor.Transparent = &color
	}
	g.surfaces[id] = &surface{
		id:         id,
		owner:      owner,
		descriptor: descriptor,
		state:      defaultDrawState(descriptor.Width, descriptor.Height),
		pixels:     make([]byte, int(byteCount)),
	}
	return id, nil
}

func (g *Graphics) DestroySurface(owner OwnerID, id ServiceID) error {
	if _, err := g.get(id, owner); err != nil {
		return err
	}
	if id == g.screen {
		return fmt.Errorf("%w: cannot destroy active screen surface", ErrInvalidState)
	}
	if err := g.registry.Destroy(id, owner, KindSurface); err != nil {
		return err
	}
	delete(g.surfaces, id)
	return nil
}

func (g *Graphics) SetScreen(owner OwnerID, id ServiceID) error {
	if id == 0 {
		g.screen = 0
		return nil
	}
	if _, err := g.get(id, owner); err != nil {
		return err
	}
	g.screen = id
	return nil
}

func (g *Graphics) Screen() ServiceID {
	return g.screen
}

func (g *Graphics) Descriptor(owner OwnerID, id ServiceID) (SurfaceDescriptor, error) {
	current, err := g.get(id, owner)
	if err != nil {
		return SurfaceDescriptor{}, err
	}
	return cloneDescriptor(current.descriptor), nil
}

func (g *Graphics) DrawState(owner OwnerID, id ServiceID) (SurfaceDrawState, error) {
	current, err := g.get(id, owner)
	if err != nil {
		return SurfaceDrawState{}, err
	}
	return current.state, nil
}

func (g *Graphics) SetDrawState(owner OwnerID, id ServiceID, state SurfaceDrawState) error {
	current, err := g.get(id, owner)
	if err != nil {
		return err
	}
	bounds := Rectangle{Width: current.descriptor.Width, Height: current.descriptor.Height}
	if !state.Clip.Valid() || state.Clip.Intersect(bounds) != state.Clip ||
		!state.Raster.Valid() {
		return fmt.Errorf("%w: invalid surface draw state", ErrInvalidArgument)
	}
	current.state = state
	return nil
}

func (g *Graphics) Pixels(owner OwnerID, id ServiceID) ([]byte, error) {
	current, err := g.get(id, owner)
	if err != nil {
		return nil, err
	}
	return cloneBytes(current.pixels), nil
}

func (g *Graphics) ReplacePixels(owner OwnerID, id ServiceID, pixels []byte) error {
	current, err := g.get(id, owner)
	if err != nil {
		return err
	}
	if len(pixels) != len(current.pixels) {
		return fmt.Errorf(
			"%w: pixel payload is %d bytes, want %d",
			ErrInvalidArgument,
			len(pixels),
			len(current.pixels),
		)
	}
	copy(current.pixels, pixels)
	current.dirty = Rectangle{Width: current.descriptor.Width, Height: current.descriptor.Height}
	return nil
}

// ReplacePixelRows copies packed source rows into surface storage without
// first materializing a tightly packed temporary image. Source begins at the
// first pixel of row zero; sourceStride may include padding.
func (g *Graphics) ReplacePixelRows(
	owner OwnerID,
	id ServiceID,
	pixels []byte,
	sourceStride int,
) error {
	current, err := g.get(id, owner)
	if err != nil {
		return err
	}
	rowBytes64 := int64(current.descriptor.Width) *
		int64(current.descriptor.Format.BytesPerPixel())
	if rowBytes64 > int64(^uint(0)>>1) {
		return fmt.Errorf("%w: packed pixel row exceeds host limits", ErrLimitExceeded)
	}
	rowBytes := int(rowBytes64)
	height := int(current.descriptor.Height)
	if sourceStride < rowBytes || height <= 0 || len(pixels) < rowBytes ||
		(height > 1 && (sourceStride == 0 ||
			height-1 > (len(pixels)-rowBytes)/sourceStride)) {
		return fmt.Errorf("%w: invalid packed pixel rows", ErrInvalidArgument)
	}
	destinationStride := int(current.descriptor.Stride)
	for y := 0; y < height; y++ {
		copy(
			current.pixels[y*destinationStride:y*destinationStride+rowBytes],
			pixels[y*sourceStride:y*sourceStride+rowBytes],
		)
	}
	current.dirty = Rectangle{Width: current.descriptor.Width, Height: current.descriptor.Height}
	return nil
}

func (g *Graphics) ReadPixelBytes(
	owner OwnerID,
	id ServiceID,
	offset uint64,
	size uint64,
) ([]byte, error) {
	current, err := g.get(id, owner)
	if err != nil {
		return nil, err
	}
	end := offset + size
	if end < offset || end > uint64(len(current.pixels)) {
		return nil, fmt.Errorf("%w: pixel byte range is out of bounds", ErrInvalidArgument)
	}
	return cloneBytes(current.pixels[offset:end]), nil
}

func (g *Graphics) WritePixelBytes(owner OwnerID, id ServiceID, offset uint64, data []byte) error {
	current, err := g.get(id, owner)
	if err != nil {
		return err
	}
	end := offset + uint64(len(data))
	if end < offset || end > uint64(len(current.pixels)) {
		return fmt.Errorf("%w: pixel byte range is out of bounds", ErrInvalidArgument)
	}
	copy(current.pixels[offset:end], data)
	current.dirty = Rectangle{Width: current.descriptor.Width, Height: current.descriptor.Height}
	return nil
}

func (g *Graphics) Pixel(owner OwnerID, id ServiceID, x, y int32) (Color, error) {
	current, err := g.get(id, owner)
	if err != nil {
		return Color{}, err
	}
	if !surfaceContains(current, x, y) {
		return Color{}, fmt.Errorf("%w: pixel (%d,%d) is out of bounds", ErrInvalidArgument, x, y)
	}
	return decodeSurfaceColor(current, x, y), nil
}

func (g *Graphics) SetPixel(owner OwnerID, id ServiceID, x, y int32, color Color) error {
	current, err := g.get(id, owner)
	if err != nil {
		return err
	}
	return drawSurfacePixel(current, x, y, color)
}

// resolveSurface validates a surface once for a caller about to plot many
// pixels into it. Text.DrawBounds took the registry lookup per glyph pixel,
// which was a quarter of a frame on a title that draws its screen with
// drawString (issue #79).
func (g *Graphics) resolveSurface(owner OwnerID, id ServiceID) (*surface, error) {
	return g.get(id, owner)
}

// setResolvedPixel plots into an already-resolved surface.
func (g *Graphics) setResolvedPixel(
	current *surface,
	x, y int32,
	color Color,
) error {
	return drawSurfacePixel(current, x, y, color)
}

func (g *Graphics) Clear(owner OwnerID, id ServiceID, color Color) error {
	current, err := g.get(id, owner)
	if err != nil {
		return err
	}
	previous := current.state
	current.state = defaultDrawState(current.descriptor.Width, current.descriptor.Height)
	for y := int32(0); y < current.descriptor.Height; y++ {
		for x := int32(0); x < current.descriptor.Width; x++ {
			encodeSurfaceColor(current, x, y, color)
		}
	}
	current.state = previous
	current.dirty = Rectangle{Width: current.descriptor.Width, Height: current.descriptor.Height}
	return nil
}

func (g *Graphics) Blit(
	owner OwnerID,
	destinationID, sourceID ServiceID,
	destinationX, destinationY int32,
	sourceRectangle Rectangle,
) error {
	return g.ScaledBlit(
		owner,
		destinationID,
		sourceID,
		Rectangle{
			X:      destinationX,
			Y:      destinationY,
			Width:  sourceRectangle.Width,
			Height: sourceRectangle.Height,
		},
		sourceRectangle,
	)
}

func (g *Graphics) ScaledBlit(
	owner OwnerID,
	destinationID, sourceID ServiceID,
	destinationRectangle, sourceRectangle Rectangle,
) error {
	destination, err := g.get(destinationID, owner)
	if err != nil {
		return err
	}
	source, err := g.get(sourceID, owner)
	if err != nil {
		return err
	}
	if !destinationRectangle.Valid() || destinationRectangle.Empty() ||
		!sourceRectangle.Valid() || sourceRectangle.Empty() {
		return fmt.Errorf("%w: invalid blit rectangles", ErrInvalidArgument)
	}
	if destinationRectangle.Right() < math.MinInt32 ||
		destinationRectangle.Right() > math.MaxInt32 ||
		destinationRectangle.Bottom() < math.MinInt32 ||
		destinationRectangle.Bottom() > math.MaxInt32 {
		return fmt.Errorf("%w: destination blit rectangle overflows", ErrInvalidArgument)
	}
	sourceBounds := Rectangle{Width: source.descriptor.Width, Height: source.descriptor.Height}
	if sourceRectangle.Intersect(sourceBounds) != sourceRectangle {
		return fmt.Errorf("%w: source blit rectangle is out of bounds", ErrInvalidArgument)
	}
	count := uint64(destinationRectangle.Width) * uint64(destinationRectangle.Height)
	if count > g.limits.MaxPixels || count > uint64(math.MaxInt/4) {
		return fmt.Errorf("%w: blit exceeds pixel limit", ErrLimitExceeded)
	}
	colors := make([]Color, int(count))
	for y := int32(0); y < destinationRectangle.Height; y++ {
		sourceY := sourceRectangle.Y +
			int32(int64(y)*int64(sourceRectangle.Height)/int64(destinationRectangle.Height))
		for x := int32(0); x < destinationRectangle.Width; x++ {
			sourceX := sourceRectangle.X +
				int32(int64(x)*int64(sourceRectangle.Width)/int64(destinationRectangle.Width))
			colors[int64(y)*int64(destinationRectangle.Width)+int64(x)] =
				decodeSurfaceColor(source, sourceX, sourceY)
		}
	}
	for y := int32(0); y < destinationRectangle.Height; y++ {
		for x := int32(0); x < destinationRectangle.Width; x++ {
			color := colors[int64(y)*int64(destinationRectangle.Width)+int64(x)]
			if source.descriptor.Transparent != nil && color == *source.descriptor.Transparent {
				continue
			}
			if err := drawSurfacePixel(
				destination,
				destinationRectangle.X+x,
				destinationRectangle.Y+y,
				color,
			); err != nil {
				return err
			}
		}
	}
	return nil
}

func (g *Graphics) Present(owner OwnerID, id ServiceID, requested Rectangle) (FrameSnapshot, error) {
	frame, err := g.presentCommit(owner, id, requested)
	if err != nil {
		return FrameSnapshot{}, err
	}
	// Present hands the pixels out, so its snapshot carries the digest that
	// goes with them; the commit-only path leaves it to whoever asks.
	frame.Hash = g.hashLastFrame()
	return cloneFrame(frame), nil
}

// PresentCommit commits a frame while leaving its RGBA bytes owned by the
// graphics service. It is the adapter fast path; callers that need retained
// pixels use Present or LastFrame, which remain detached.
func (g *Graphics) PresentCommit(
	owner OwnerID,
	id ServiceID,
	requested Rectangle,
) (FramePresentation, error) {
	frame, err := g.presentCommit(owner, id, requested)
	if err != nil {
		return FramePresentation{}, err
	}
	return presentationOf(frame), nil
}

func (g *Graphics) presentCommit(
	owner OwnerID,
	id ServiceID,
	requested Rectangle,
) (FrameSnapshot, error) {
	current, err := g.get(id, owner)
	if err != nil {
		return FrameSnapshot{}, err
	}
	if g.screen != 0 && id != g.screen {
		return FrameSnapshot{}, fmt.Errorf("%w: surface %s is not the presentation owner", ErrWrongOwner, id)
	}
	bounds := Rectangle{Width: current.descriptor.Width, Height: current.descriptor.Height}
	if !requested.Valid() {
		return FrameSnapshot{}, fmt.Errorf("%w: invalid present rectangle", ErrInvalidArgument)
	}
	dirty := current.dirty
	if !requested.Empty() {
		dirty = requested.Intersect(bounds)
	}
	if g.presentSequence == math.MaxUint64 {
		return FrameSnapshot{}, fmt.Errorf("%w: presentation sequence exhausted", ErrLimitExceeded)
	}
	// Reuse is only sound when the surface itself is clean. dirty alone is not
	// enough: a requested rectangle that falls outside the surface intersects
	// to empty, and adopting the previous frame there would drop pixels the
	// guest had already drawn - and clear current.dirty, so nothing would ever
	// present them.
	reuse := dirty.Empty() &&
		current.dirty.Empty() &&
		g.lastFrame.SurfaceID == id &&
		g.lastFrame.Width == current.descriptor.Width &&
		g.lastFrame.Height == current.descriptor.Height
	rgba := g.lastFrame.RGBA
	hash := g.lastFrame.Hash
	hashed := g.lastFrameHashed
	if !reuse {
		rgba, err = surfaceRGBAInto(current, rgba)
		if err != nil {
			return FrameSnapshot{}, err
		}
		hash, hashed = [sha256.Size]byte{}, false
	}
	g.presentSequence++
	frame := FrameSnapshot{
		SurfaceID: id,
		Sequence:  g.presentSequence,
		Width:     current.descriptor.Width,
		Height:    current.descriptor.Height,
		Dirty:     dirty,
		RGBA:      rgba,
		Hash:      hash,
	}
	current.dirty = Rectangle{}
	g.lastFrame = frame
	g.lastFrameHashed = hashed
	return frame, nil
}

// hashLastFrame answers the digest of the frame on screen, computing it the
// first time it is asked for. Present leaves it unset: a caller that only
// commits pixels never looks at it, and the callers that do - the host asking
// whether the screen changed, and the state snapshot - run orders of magnitude
// less often than the guest presents.
func (g *Graphics) hashLastFrame() [sha256.Size]byte {
	if g.lastFrame.Sequence == 0 {
		// Nothing has been presented, and an empty snapshot has to carry an
		// empty digest for the state validator to accept it.
		return [sha256.Size]byte{}
	}
	if !g.lastFrameHashed {
		g.lastFrame.Hash = sha256.Sum256(g.lastFrame.RGBA)
		g.lastFrameHashed = true
	}
	return g.lastFrame.Hash
}

func presentationOf(frame FrameSnapshot) FramePresentation {
	return FramePresentation{
		SurfaceID: frame.SurfaceID,
		Sequence:  frame.Sequence,
		Width:     frame.Width,
		Height:    frame.Height,
		Dirty:     frame.Dirty,
	}
}

// RGBA converts a surface to a copied presentation-format buffer without
// changing presentation sequence or dirty state.
func (g *Graphics) RGBA(owner OwnerID, id ServiceID) ([]byte, error) {
	current, err := g.get(id, owner)
	if err != nil {
		return nil, err
	}
	return surfaceRGBA(current)
}

// RGBAInto converts a surface into destination, reusing its storage when it is
// large enough, and returns the slice that holds the result. A caller that
// converts the same surface many times a frame keeps one buffer instead of
// allocating and zeroing the whole surface on every call.
func (g *Graphics) RGBAInto(
	owner OwnerID,
	id ServiceID,
	destination []byte,
) ([]byte, error) {
	current, err := g.get(id, owner)
	if err != nil {
		return nil, err
	}
	return surfaceRGBAInto(current, destination)
}

// RGBARowsInto converts the surface rows in [top, bottom) into destination,
// packed from its start, reusing its storage when it is large enough.
func (g *Graphics) RGBARowsInto(
	owner OwnerID,
	id ServiceID,
	top, bottom int,
	destination []byte,
) ([]byte, error) {
	current, err := g.get(id, owner)
	if err != nil {
		return nil, err
	}
	return surfaceRGBARowsInto(current, top, bottom, destination)
}

func (g *Graphics) LastFrame() FrameSnapshot {
	g.hashLastFrame()
	return cloneFrame(g.lastFrame)
}

// LastFramePresentation identifies the most recently presented frame without
// materializing its pixels. LastFrame copies the whole surface, which is far
// too expensive for a driver that only needs to ask, once per host tick,
// whether the presented frame is the one it already holds. The hash is the one
// Present already computed, so an unchanged screen is recognized exactly rather
// than being re-copied and re-uploaded.
func (g *Graphics) LastFramePresentation() (uint64, [sha256.Size]byte) {
	return g.lastFrame.Sequence, g.hashLastFrame()
}

// CopyLastFrameRGBA copies the committed pixels into caller-owned row storage
// without allocating or exposing the service's backing slice.
func (g *Graphics) CopyLastFrameRGBA(destination []byte, stride int) error {
	frame := g.lastFrame
	if frame.Sequence == 0 || frame.Width <= 0 || frame.Height <= 0 {
		return fmt.Errorf("%w: no presented frame", ErrNotFound)
	}
	rowBytes64 := int64(frame.Width) * 4
	if rowBytes64 > int64(^uint(0)>>1) {
		return fmt.Errorf("%w: RGBA row exceeds host limits", ErrLimitExceeded)
	}
	rowBytes := int(rowBytes64)
	height := int(frame.Height)
	if stride < rowBytes || len(destination) < rowBytes ||
		(height > 1 && (stride == 0 ||
			height-1 > (len(destination)-rowBytes)/stride)) {
		return fmt.Errorf("%w: RGBA destination is too small", ErrInvalidArgument)
	}
	for y := 0; y < height; y++ {
		copy(
			destination[y*stride:y*stride+rowBytes],
			frame.RGBA[y*rowBytes:(y+1)*rowBytes],
		)
	}
	return nil
}

// LastFrameImage materializes the presented frame with a single copy, or nil
// when nothing has been presented. LastFrame().Image() copies the pixels
// twice: once to detach the snapshot from the service and again to build the
// image.
func (g *Graphics) LastFrameImage() *image.RGBA {
	if g.lastFrame.Sequence == 0 ||
		g.lastFrame.Width <= 0 ||
		g.lastFrame.Height <= 0 {
		return nil
	}
	result := image.NewRGBA(image.Rect(
		0,
		0,
		int(g.lastFrame.Width),
		int(g.lastFrame.Height),
	))
	copy(result.Pix, g.lastFrame.RGBA)
	return result
}

func (g *Graphics) Snapshot() GraphicsState {
	g.hashLastFrame()
	state := GraphicsState{
		Limits:          g.limits,
		Screen:          g.screen,
		PresentSequence: g.presentSequence,
		LastFrame:       cloneFrame(g.lastFrame),
	}
	ids := make([]ServiceID, 0, len(g.surfaces))
	for id := range g.surfaces {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	for _, id := range ids {
		current := g.surfaces[id]
		state.Surfaces = append(state.Surfaces, SurfaceState{
			ID:         current.id,
			Owner:      current.owner,
			Descriptor: cloneDescriptor(current.descriptor),
			Draw:       current.state,
			Pixels:     cloneBytes(current.pixels),
			Dirty:      current.dirty,
		})
	}
	return state
}

func (g *Graphics) Restore(state GraphicsState) error {
	if err := state.Limits.Validate(); err != nil ||
		len(state.Surfaces) > int(state.Limits.MaxSurfaces) {
		return fmt.Errorf("%w: invalid graphics state limits", ErrInvalidState)
	}
	surfaces := make(map[ServiceID]*surface, len(state.Surfaces))
	var previous ServiceID
	for index, saved := range state.Surfaces {
		descriptor := cloneDescriptor(saved.Descriptor)
		if !saved.ID.Valid() || (index != 0 && saved.ID <= previous) ||
			descriptor.Stride == 0 ||
			descriptor.Validate(state.Limits) != nil ||
			!saved.Draw.Clip.Valid() || !saved.Draw.Raster.Valid() ||
			saved.Draw.Clip.Intersect(Rectangle{
				Width:  descriptor.Width,
				Height: descriptor.Height,
			}) != saved.Draw.Clip ||
			uint64(len(saved.Pixels)) !=
				uint64(descriptor.Stride)*uint64(descriptor.Height) ||
			!saved.Dirty.Valid() ||
			saved.Dirty.Intersect(Rectangle{
				Width:  descriptor.Width,
				Height: descriptor.Height,
			}) != saved.Dirty ||
			g.registry.Validate(saved.ID, saved.Owner, KindSurface) != nil {
			return fmt.Errorf("%w: invalid surface state %d", ErrInvalidState, index)
		}
		surfaces[saved.ID] = &surface{
			id:         saved.ID,
			owner:      saved.Owner,
			descriptor: descriptor,
			state:      saved.Draw,
			pixels:     cloneBytes(saved.Pixels),
			dirty:      saved.Dirty,
		}
		previous = saved.ID
	}
	if state.Screen != 0 {
		if surfaces[state.Screen] == nil {
			return fmt.Errorf("%w: screen surface is absent", ErrInvalidState)
		}
	}
	if err := validateFrameSnapshot(
		state.LastFrame,
		surfaces,
		state.PresentSequence,
		state.Limits,
	); err != nil {
		return err
	}
	g.limits = state.Limits
	g.surfaces = surfaces
	g.screen = state.Screen
	g.presentSequence = state.PresentSequence
	g.lastFrame = cloneFrame(state.LastFrame)
	// The restored snapshot carried a validated digest for exactly these
	// pixels, so it does not have to be recomputed.
	g.lastFrameHashed = true
	return nil
}

func validateFrameSnapshot(
	frame FrameSnapshot,
	surfaces map[ServiceID]*surface,
	presentSequence uint64,
	limits GraphicsLimits,
) error {
	if frame.Sequence == 0 {
		if presentSequence != 0 ||
			frame.SurfaceID != 0 || frame.Width != 0 || frame.Height != 0 ||
			!frame.Dirty.Empty() || len(frame.RGBA) != 0 ||
			frame.Hash != [sha256.Size]byte{} {
			return fmt.Errorf("%w: incomplete empty frame snapshot", ErrInvalidState)
		}
		return nil
	}
	current := surfaces[frame.SurfaceID]
	pixels := uint64(frame.Width) * uint64(frame.Height)
	if !frame.SurfaceID.Valid() ||
		frame.Sequence != presentSequence ||
		frame.Width <= 0 || frame.Height <= 0 ||
		frame.Width > limits.MaxWidth || frame.Height > limits.MaxHeight ||
		pixels > limits.MaxPixels ||
		(current != nil &&
			(frame.Width != current.descriptor.Width ||
				frame.Height != current.descriptor.Height)) ||
		!frame.Dirty.Valid() ||
		frame.Dirty.Intersect(Rectangle{Width: frame.Width, Height: frame.Height}) != frame.Dirty ||
		uint64(len(frame.RGBA)) != pixels*4 ||
		sha256.Sum256(frame.RGBA) != frame.Hash {
		return fmt.Errorf("%w: invalid frame snapshot", ErrInvalidState)
	}
	return nil
}
