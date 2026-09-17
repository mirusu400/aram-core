package runtime

import (
	"fmt"
	"math"
	"sort"
)

func (g *Graphics) Line(owner OwnerID, id ServiceID, x0, y0, x1, y1 int32, color Color) error {
	return g.line(owner, id, x0, y0, x1, y1, color, nil)
}

// line allows Polygon to share coverage across its inclusive outline edges
// without changing the line rasterization or the public Line contract.
func (g *Graphics) line(owner OwnerID, id ServiceID, x0, y0, x1, y1 int32, color Color, plot func(int32, int32) error) error {
	current, err := g.get(id, owner)
	if err != nil {
		return err
	}
	if plot == nil {
		plot = func(x, y int32) error { return drawSurfacePixel(current, x, y, color) }
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
		if err := plot(int32(currentX), int32(currentY)); err != nil {
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
	if current.state.GlobalTransparency256 != 0 {
		// Top/bottom already include the corners. Keep the old raster path
		// unchanged for legacy clients, but never compound 256-scale alpha
		// merely because an outline consists of multiple inclusive lines.
		if rectangle.Height <= 2 {
			return nil
		}
		if err := g.Line(owner, id, rectangle.X, rectangle.Y+1, rectangle.X, int32(bottom)-1, color); err != nil {
			return err
		}
		if rectangle.Width > 1 {
			return g.Line(owner, id, int32(right), rectangle.Y+1, int32(right), int32(bottom)-1, color)
		}
		return nil
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
			if inside && PointInArc(dx, dy, startDegrees, sweepDegrees) {
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
	plot := func(x, y int32) error { return drawSurfacePixel(current, x, y, color) }
	if current.state.GlobalTransparency256 != 0 {
		// Scanline endpoints, outline edges and vertices can all overlap. Keep
		// their full union (outlines also add pixels not in the fill), but blend
		// each pixel only once for the opt-in 256-scale alpha contract. Legacy
		// drawing retains its original repeated coverage, including raster ops.
		left := max(minimumX+int64(current.state.TranslateX), int64(current.state.Clip.X), 0)
		top := max(minimumY+int64(current.state.TranslateY), int64(current.state.Clip.Y), 0)
		right := min(maximumX+int64(current.state.TranslateX)+1, current.state.Clip.Right(), int64(current.descriptor.Width))
		bottom := min(maximumY+int64(current.state.TranslateY)+1, current.state.Clip.Bottom(), int64(current.descriptor.Height))
		if left >= right || top >= bottom {
			return nil
		}
		// A bit per visible bounding-box pixel bounds scratch storage by the
		// already validated surface size, even for mostly offscreen polygons.
		width := uint64(right - left)
		covered := make([]byte, (width*uint64(bottom-top)+7)/8)
		plot = func(x, y int32) error {
			tx := int64(x) + int64(current.state.TranslateX)
			ty := int64(y) + int64(current.state.TranslateY)
			if tx < left || tx >= right || ty < top || ty >= bottom {
				return nil
			}
			index := uint64(ty-top)*width + uint64(tx-left)
			bit := byte(1 << (index % 8))
			if covered[index/8]&bit != 0 {
				return nil
			}
			covered[index/8] |= bit
			return drawSurfacePixel(current, x, y, color)
		}
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
					if err := plot(
						int32(column),
						int32(row),
					); err != nil {
						return err
					}
				}
			}
		}
	}
	if len(points) == 1 {
		return plot(points[0].X, points[0].Y)
	}
	for index, point := range points {
		next := points[(index+1)%len(points)]
		if err := g.line(
			owner,
			id,
			point.X,
			point.Y,
			next.X,
			next.Y,
			color,
			plot,
		); err != nil {
			return err
		}
	}
	return nil
}
