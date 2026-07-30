package runtime

import (
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"image"
	"math"
	"math/bits"
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

func (g *Graphics) Line(owner OwnerID, id ServiceID, x0, y0, x1, y1 int32, color Color) error {
	current, err := g.get(id, owner)
	if err != nil {
		return err
	}
	currentX, currentY := int64(x0), int64(y0)
	targetX, targetY := int64(x1), int64(y1)
	dx := abs64(targetX - currentX)
	steps := uint64(max(dx, abs64(targetY-currentY))) + 1
	if steps > g.limits.MaxPixels {
		return fmt.Errorf("%w: line exceeds raster work limit", ErrLimitExceeded)
	}
	sx := int64(-1)
	if currentX < targetX {
		sx = 1
	}
	dy := -abs64(targetY - currentY)
	sy := int64(-1)
	if currentY < targetY {
		sy = 1
	}
	lineError := dx + dy
	for {
		if err := drawSurfacePixel(current, int32(currentX), int32(currentY), color); err != nil {
			return err
		}
		if currentX == targetX && currentY == targetY {
			return nil
		}
		twice := lineError * 2
		if twice >= dy {
			lineError += dy
			currentX += sx
		}
		if twice <= dx {
			lineError += dx
			currentY += sy
		}
	}
}

func (g *Graphics) Rectangle(
	owner OwnerID,
	id ServiceID,
	rectangle Rectangle,
	color Color,
	fill bool,
) error {
	current, err := g.get(id, owner)
	if err != nil {
		return err
	}
	if !rectangle.Valid() {
		return fmt.Errorf("%w: invalid rectangle", ErrInvalidArgument)
	}
	if rectangle.Empty() {
		return nil
	}
	count := uint64(rectangle.Width) * uint64(rectangle.Height)
	if fill {
		if count > g.limits.MaxPixels {
			return fmt.Errorf("%w: rectangle exceeds raster work limit", ErrLimitExceeded)
		}
		for y := int64(rectangle.Y); y < rectangle.Bottom(); y++ {
			for x := int64(rectangle.X); x < rectangle.Right(); x++ {
				if x < math.MinInt32 || x > math.MaxInt32 ||
					y < math.MinInt32 || y > math.MaxInt32 {
					continue
				}
				if err := drawSurfacePixel(current, int32(x), int32(y), color); err != nil {
					return err
				}
			}
		}
		return nil
	}
	perimeter := uint64(rectangle.Width)*2 + uint64(rectangle.Height)*2
	if perimeter > g.limits.MaxPixels {
		return fmt.Errorf("%w: rectangle exceeds raster work limit", ErrLimitExceeded)
	}
	right := int64(rectangle.X) + int64(rectangle.Width) - 1
	bottom := int64(rectangle.Y) + int64(rectangle.Height) - 1
	if right < math.MinInt32 || right > math.MaxInt32 ||
		bottom < math.MinInt32 || bottom > math.MaxInt32 {
		return fmt.Errorf("%w: rectangle overflows coordinates", ErrInvalidArgument)
	}
	if err := g.Line(owner, id, rectangle.X, rectangle.Y, int32(right), rectangle.Y, color); err != nil {
		return err
	}
	if rectangle.Height > 1 {
		if err := g.Line(owner, id, rectangle.X, int32(bottom), int32(right), int32(bottom), color); err != nil {
			return err
		}
	}
	if err := g.Line(owner, id, rectangle.X, rectangle.Y, rectangle.X, int32(bottom), color); err != nil {
		return err
	}
	if rectangle.Width > 1 {
		return g.Line(owner, id, int32(right), rectangle.Y, int32(right), int32(bottom), color)
	}
	return nil
}

// Arc draws or fills an elliptical arc. Angles use screen coordinates:
// zero points right and positive sweeps proceed clockwise.
func (g *Graphics) Arc(
	owner OwnerID,
	id ServiceID,
	rectangle Rectangle,
	startDegrees, sweepDegrees int32,
	color Color,
	fill bool,
) error {
	current, err := g.get(id, owner)
	if err != nil {
		return err
	}
	if !rectangle.Valid() {
		return fmt.Errorf("%w: invalid arc rectangle", ErrInvalidArgument)
	}
	if rectangle.Empty() || sweepDegrees == 0 {
		return nil
	}
	right, bottom := rectangle.Right(), rectangle.Bottom()
	if right < math.MinInt32 || right > math.MaxInt32 ||
		bottom < math.MinInt32 || bottom > math.MaxInt32 ||
		uint64(rectangle.Width)*uint64(rectangle.Height) > g.limits.MaxPixels {
		return fmt.Errorf("%w: arc exceeds raster work limit", ErrLimitExceeded)
	}
	radiusX := int64(rectangle.Width / 2)
	radiusY := int64(rectangle.Height / 2)
	if radiusX == 0 || radiusY == 0 {
		return g.Rectangle(owner, id, rectangle, color, fill)
	}
	centerX := int64(rectangle.X) + radiusX
	centerY := int64(rectangle.Y) + radiusY
	radiusXSquared := uint64(radiusX * radiusX)
	radiusYSquared := uint64(radiusY * radiusY)
	for y := int64(rectangle.Y); y < bottom; y++ {
		for x := int64(rectangle.X); x < right; x++ {
			dx, dy := x-centerX, y-centerY
			inside := ellipseContains(
				dx,
				dy,
				radiusXSquared,
				radiusYSquared,
			)
			if inside && !fill {
				inside = !ellipseContains(
					dx-1,
					dy,
					radiusXSquared,
					radiusYSquared,
				) || !ellipseContains(
					dx+1,
					dy,
					radiusXSquared,
					radiusYSquared,
				) || !ellipseContains(
					dx,
					dy-1,
					radiusXSquared,
					radiusYSquared,
				) || !ellipseContains(
					dx,
					dy+1,
					radiusXSquared,
					radiusYSquared,
				)
			}
			if inside && pointInArc(dx, dy, startDegrees, sweepDegrees) {
				if err := drawSurfacePixel(
					current,
					int32(x),
					int32(y),
					color,
				); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

// Polygon draws a closed polygon and optionally fills it with an even-odd
// scanline rule. Work is bounded by GraphicsLimits.MaxPixels.
func (g *Graphics) Polygon(
	owner OwnerID,
	id ServiceID,
	points []Point,
	color Color,
	fill bool,
) error {
	current, err := g.get(id, owner)
	if err != nil {
		return err
	}
	if len(points) == 0 {
		return nil
	}
	if uint64(len(points)) > g.limits.MaxPixels {
		return fmt.Errorf("%w: polygon point count exceeds limit", ErrLimitExceeded)
	}
	minimumX, maximumX := int64(points[0].X), int64(points[0].X)
	minimumY, maximumY := int64(points[0].Y), int64(points[0].Y)
	var perimeter uint64
	for index, point := range points {
		x, y := int64(point.X), int64(point.Y)
		minimumX, maximumX = min(minimumX, x), max(maximumX, x)
		minimumY, maximumY = min(minimumY, y), max(maximumY, y)
		if len(points) > 1 {
			next := points[(index+1)%len(points)]
			steps := uint64(max(
				abs64(int64(next.X)-x),
				abs64(int64(next.Y)-y),
			)) + 1
			if steps > g.limits.MaxPixels-perimeter {
				return fmt.Errorf("%w: polygon outline exceeds raster work limit", ErrLimitExceeded)
			}
			perimeter += steps
		}
	}
	spanX := uint64(maximumX-minimumX) + 1
	spanY := uint64(maximumY-minimumY) + 1
	if fill && (spanX > g.limits.MaxPixels ||
		spanY > g.limits.MaxPixels ||
		spanX > g.limits.MaxPixels/spanY ||
		(maximumX-minimumX != 0 &&
			maximumY-minimumY > math.MaxInt64/(maximumX-minimumX))) {
		return fmt.Errorf("%w: polygon fill exceeds raster work limit", ErrLimitExceeded)
	}
	if fill && len(points) >= 3 {
		nodes := make([]int64, 0, len(points))
		for row := minimumY; row <= maximumY; row++ {
			nodes = nodes[:0]
			previous := len(points) - 1
			for index, point := range points {
				currentY := int64(point.Y)
				previousY := int64(points[previous].Y)
				if (currentY <= row && previousY > row) ||
					(previousY <= row && currentY > row) {
					numerator := (row - currentY) *
						(int64(points[previous].X) - int64(point.X))
					nodes = append(
						nodes,
						int64(point.X)+numerator/(previousY-currentY),
					)
				}
				previous = index
			}
			sort.Slice(nodes, func(i, j int) bool {
				return nodes[i] < nodes[j]
			})
			for index := 0; index+1 < len(nodes); index += 2 {
				for column := nodes[index]; column <= nodes[index+1]; column++ {
					if column < math.MinInt32 || column > math.MaxInt32 ||
						row < math.MinInt32 || row > math.MaxInt32 {
						continue
					}
					if err := drawSurfacePixel(
						current,
						int32(column),
						int32(row),
						color,
					); err != nil {
						return err
					}
				}
			}
		}
	}
	if len(points) == 1 {
		return drawSurfacePixel(current, points[0].X, points[0].Y, color)
	}
	for index, point := range points {
		next := points[(index+1)%len(points)]
		if err := g.Line(
			owner,
			id,
			point.X,
			point.Y,
			next.X,
			next.Y,
			color,
		); err != nil {
			return err
		}
	}
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
	rgba, err := surfaceRGBA(current)
	if err != nil {
		return FrameSnapshot{}, err
	}
	g.presentSequence++
	frame := FrameSnapshot{
		SurfaceID: id,
		Sequence:  g.presentSequence,
		Width:     current.descriptor.Width,
		Height:    current.descriptor.Height,
		Dirty:     dirty,
		RGBA:      rgba,
		Hash:      sha256.Sum256(rgba),
	}
	current.dirty = Rectangle{}
	g.lastFrame = cloneFrame(frame)
	return cloneFrame(frame), nil
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

func (g *Graphics) LastFrame() FrameSnapshot {
	return cloneFrame(g.lastFrame)
}

func (g *Graphics) Snapshot() GraphicsState {
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

func (g *Graphics) get(id ServiceID, owner OwnerID) (*surface, error) {
	if err := g.registry.Validate(id, owner, KindSurface); err != nil {
		return nil, err
	}
	current := g.surfaces[id]
	if current == nil {
		return nil, fmt.Errorf("%w: surface %s", ErrInvalidState, id)
	}
	return current, nil
}

func cloneDescriptor(descriptor SurfaceDescriptor) SurfaceDescriptor {
	descriptor.Palette = append([]Color(nil), descriptor.Palette...)
	if descriptor.Transparent != nil {
		color := *descriptor.Transparent
		descriptor.Transparent = &color
	}
	return descriptor
}

func surfaceContains(current *surface, x, y int32) bool {
	return x >= 0 && y >= 0 &&
		x < current.descriptor.Width && y < current.descriptor.Height
}

func drawSurfacePixel(current *surface, x, y int32, color Color) error {
	translatedX := int64(x) + int64(current.state.TranslateX)
	translatedY := int64(y) + int64(current.state.TranslateY)
	if translatedX < math.MinInt32 || translatedX > math.MaxInt32 ||
		translatedY < math.MinInt32 || translatedY > math.MaxInt32 {
		return nil
	}
	x = int32(translatedX)
	y = int32(translatedY)
	if !surfaceContains(current, x, y) ||
		x < current.state.Clip.X || y < current.state.Clip.Y ||
		int64(x) >= current.state.Clip.Right() ||
		int64(y) >= current.state.Clip.Bottom() {
		return nil
	}
	if current.state.Transparency &&
		current.descriptor.Transparent != nil &&
		color == *current.descriptor.Transparent {
		return nil
	}
	destination := decodeSurfaceColor(current, x, y)
	color = applyRaster(current.state.Raster, destination, color)
	if current.state.GlobalAlpha != 0xff {
		color.A = uint8(uint16(color.A) * uint16(current.state.GlobalAlpha) / 0xff)
	}
	if color.A != 0xff {
		color = blendColor(destination, color)
	}
	encodeSurfaceColor(current, x, y, color)
	current.dirty = current.dirty.Union(Rectangle{X: x, Y: y, Width: 1, Height: 1})
	return nil
}

func applyRaster(operation RasterOperation, destination, source Color) Color {
	switch operation {
	case RasterXOR:
		return Color{
			R: destination.R ^ source.R,
			G: destination.G ^ source.G,
			B: destination.B ^ source.B,
			A: source.A,
		}
	case RasterAND:
		return Color{
			R: destination.R & source.R,
			G: destination.G & source.G,
			B: destination.B & source.B,
			A: source.A,
		}
	case RasterOR:
		return Color{
			R: destination.R | source.R,
			G: destination.G | source.G,
			B: destination.B | source.B,
			A: source.A,
		}
	default:
		return source
	}
}

func blendColor(destination, source Color) Color {
	alpha := uint32(source.A)
	inverse := uint32(0xff) - alpha
	return Color{
		R: uint8((uint32(source.R)*alpha + uint32(destination.R)*inverse + 127) / 255),
		G: uint8((uint32(source.G)*alpha + uint32(destination.G)*inverse + 127) / 255),
		B: uint8((uint32(source.B)*alpha + uint32(destination.B)*inverse + 127) / 255),
		A: uint8(alpha + (uint32(destination.A)*inverse+127)/255),
	}
}

func pixelOffset(current *surface, x, y int32) int {
	return int(y)*int(current.descriptor.Stride) +
		int(x)*current.descriptor.Format.BytesPerPixel()
}

func surfaceRGBA(current *surface) ([]byte, error) {
	pixelCount := uint64(current.descriptor.Width) * uint64(current.descriptor.Height)
	if pixelCount > uint64(math.MaxInt/4) {
		return nil, fmt.Errorf("%w: RGBA conversion exceeds host address space", ErrLimitExceeded)
	}
	rgba := make([]byte, int(pixelCount)*4)
	for y := int32(0); y < current.descriptor.Height; y++ {
		for x := int32(0); x < current.descriptor.Width; x++ {
			color := decodeSurfaceColor(current, x, y)
			offset := (int(y)*int(current.descriptor.Width) + int(x)) * 4
			rgba[offset+0] = color.R
			rgba[offset+1] = color.G
			rgba[offset+2] = color.B
			rgba[offset+3] = color.A
		}
	}
	return rgba, nil
}

func decodeSurfaceColor(current *surface, x, y int32) Color {
	offset := pixelOffset(current, x, y)
	switch current.descriptor.Format {
	case PixelRGBA8888:
		return Color{
			R: current.pixels[offset],
			G: current.pixels[offset+1],
			B: current.pixels[offset+2],
			A: current.pixels[offset+3],
		}
	case PixelARGB8888:
		return Color{
			A: current.pixels[offset],
			R: current.pixels[offset+1],
			G: current.pixels[offset+2],
			B: current.pixels[offset+3],
		}
	case PixelXRGB8888:
		return Color{
			R: current.pixels[offset+1],
			G: current.pixels[offset+2],
			B: current.pixels[offset+3],
			A: 0xff,
		}
	case PixelBGRX8888:
		return Color{
			R: current.pixels[offset+2],
			G: current.pixels[offset+1],
			B: current.pixels[offset],
			A: 0xff,
		}
	case PixelRGB565:
		value := binary.LittleEndian.Uint16(current.pixels[offset:])
		return Color{
			R: expand5(uint8((value >> 11) & 0x1f)),
			G: expand6(uint8((value >> 5) & 0x3f)),
			B: expand5(uint8(value & 0x1f)),
			A: 0xff,
		}
	case PixelRGB555:
		value := binary.LittleEndian.Uint16(current.pixels[offset:])
		return Color{
			R: expand5(uint8((value >> 10) & 0x1f)),
			G: expand5(uint8((value >> 5) & 0x1f)),
			B: expand5(uint8(value & 0x1f)),
			A: 0xff,
		}
	case PixelGray8:
		value := current.pixels[offset]
		return Color{R: value, G: value, B: value, A: 0xff}
	case PixelIndexed8:
		index := int(current.pixels[offset])
		if index < len(current.descriptor.Palette) {
			return current.descriptor.Palette[index]
		}
	}
	return Color{}
}

func encodeSurfaceColor(current *surface, x, y int32, color Color) {
	offset := pixelOffset(current, x, y)
	switch current.descriptor.Format {
	case PixelRGBA8888:
		current.pixels[offset+0] = color.R
		current.pixels[offset+1] = color.G
		current.pixels[offset+2] = color.B
		current.pixels[offset+3] = color.A
	case PixelARGB8888:
		current.pixels[offset+0] = color.A
		current.pixels[offset+1] = color.R
		current.pixels[offset+2] = color.G
		current.pixels[offset+3] = color.B
	case PixelXRGB8888:
		current.pixels[offset+0] = 0
		current.pixels[offset+1] = color.R
		current.pixels[offset+2] = color.G
		current.pixels[offset+3] = color.B
	case PixelBGRX8888:
		current.pixels[offset+0] = color.B
		current.pixels[offset+1] = color.G
		current.pixels[offset+2] = color.R
		current.pixels[offset+3] = 0
	case PixelRGB565:
		value := uint16(color.R>>3)<<11 |
			uint16(color.G>>2)<<5 |
			uint16(color.B>>3)
		binary.LittleEndian.PutUint16(current.pixels[offset:], value)
	case PixelRGB555:
		value := uint16(color.R>>3)<<10 |
			uint16(color.G>>3)<<5 |
			uint16(color.B>>3)
		binary.LittleEndian.PutUint16(current.pixels[offset:], value)
	case PixelGray8:
		current.pixels[offset] = uint8(
			(uint32(color.R)*77 + uint32(color.G)*150 + uint32(color.B)*29 + 128) >> 8,
		)
	case PixelIndexed8:
		current.pixels[offset] = nearestPalette(current.descriptor.Palette, color)
	}
}

func nearestPalette(palette []Color, color Color) uint8 {
	best := 0
	bestDistance := uint64(math.MaxUint64)
	for index, candidate := range palette {
		red := int64(candidate.R) - int64(color.R)
		green := int64(candidate.G) - int64(color.G)
		blue := int64(candidate.B) - int64(color.B)
		alpha := int64(candidate.A) - int64(color.A)
		distance := uint64(red*red + green*green + blue*blue + alpha*alpha)
		if distance < bestDistance {
			best = index
			bestDistance = distance
		}
	}
	return uint8(best)
}

func expand5(value uint8) uint8 {
	return value<<3 | value>>2
}

func expand6(value uint8) uint8 {
	return value<<2 | value>>4
}

func abs64(value int64) int64 {
	if value < 0 {
		return -value
	}
	return value
}

func ellipseContains(
	dx, dy int64,
	radiusXSquared, radiusYSquared uint64,
) bool {
	dxSquared := uint64(dx * dx)
	dySquared := uint64(dy * dy)
	leftHigh, leftLow := bits.Mul64(dxSquared, radiusYSquared)
	rightHigh, rightLow := bits.Mul64(dySquared, radiusXSquared)
	sumLow, carry := bits.Add64(leftLow, rightLow, 0)
	sumHigh, _ := bits.Add64(leftHigh, rightHigh, carry)
	limitHigh, limitLow := bits.Mul64(radiusXSquared, radiusYSquared)
	return sumHigh < limitHigh ||
		sumHigh == limitHigh && sumLow <= limitLow
}

func pointInArc(
	dx, dy int64,
	startDegrees, sweepDegrees int32,
) bool {
	sweep := int64(sweepDegrees)
	if sweep >= 360 || sweep <= -360 {
		return true
	}
	if sweep == 0 {
		return false
	}
	if dx == 0 && dy == 0 {
		return true
	}
	absoluteX, absoluteY := abs64(dx), abs64(dy)
	angle := absoluteY * 90 / (absoluteX + absoluteY)
	switch {
	case dx < 0 && dy >= 0:
		angle = 180 - angle
	case dx < 0 && dy < 0:
		angle = 180 + angle
	case dx >= 0 && dy < 0:
		angle = 360 - angle
	}
	normalize := func(value int64) int64 {
		value %= 360
		if value < 0 {
			value += 360
		}
		return value
	}
	begin := normalize(int64(startDegrees))
	end := normalize(int64(startDegrees) + sweep)
	angle = normalize(angle)
	if sweep > 0 {
		if begin <= end {
			return angle >= begin && angle <= end
		}
		return angle >= begin || angle <= end
	}
	if end <= begin {
		return angle >= end && angle <= begin
	}
	return angle >= end || angle <= begin
}
