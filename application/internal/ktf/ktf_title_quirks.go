package ktf

import (
	"crypto/sha256"
	"image"

	"github.com/mirusu400/aram-core/application/internal/quirkdb"
	"github.com/mirusu400/aram-core/loader/ktf"
)

// EffectiveDisplaySize applies narrowly identified handset metadata
// corrections from the quirk database. Most descriptors are authoritative,
// but a small number of shipped packages carry a display size for a
// different build while their client uses a larger fixed coordinate system.
// Every correction is guarded by the declared metadata and the exact client
// digest so unrelated titles keep their descriptor-selected framebuffer.
func EffectiveDisplaySize(pkg ktf.Package) (int, int) {
	return resolveKTFDisplaySize(
		pkg.Descriptor,
		sha256.Sum256(pkg.Client),
	)
}

func resolveKTFDisplaySize(
	descriptor ktf.Descriptor,
	clientHash [sha256.Size]byte,
) (int, int) {
	for _, override := range quirkdb.DisplayOverrides {
		if override.Key.Matches(
			descriptor.AID,
			descriptor.MainClass,
			clientHash,
		) &&
			descriptor.DisplayWidth == override.DeclaredWidth &&
			descriptor.DisplayHeight == override.DeclaredHeight {
			return override.Width, override.Height
		}
	}
	return descriptor.DisplayWidth, descriptor.DisplayHeight
}

// ktfMenuForegroundCompat replays recognized menu-label draws above a
// later-drawn overlay image. It records the current coordinates rather than
// fixing them in the runtime, so menu rotation and selection animations keep
// following the guest. Which titles need this — and the digests and geometry
// that identify their images — comes from the quirk database.
type ktfMenuForegroundCompat struct {
	labelHashes    map[[sha256.Size]byte]struct{}
	labelMaxWidth  int
	labelMaxHeight int
	overlayHash    [sha256.Size]byte
	overlayX       int
	overlayY       int
	overlayAnchor  uint32
	overlayWidth   int
	overlayHeight  int
	overlayImage   uint32
	pending        []ktfJavaImageDraw
}

func newKTFMenuForegroundCompat(pkg ktf.Package) *ktfMenuForegroundCompat {
	clientHash := sha256.Sum256(pkg.Client)
	for _, entry := range quirkdb.MenuForegroundOverlays {
		if !entry.Key.Matches(
			pkg.Descriptor.AID,
			pkg.Descriptor.MainClass,
			clientHash,
		) {
			continue
		}
		labels := make(
			map[[sha256.Size]byte]struct{},
			len(entry.LabelHashes),
		)
		for _, digest := range entry.LabelHashes {
			labels[digest] = struct{}{}
		}
		return &ktfMenuForegroundCompat{
			labelHashes:    labels,
			labelMaxWidth:  entry.LabelMaxWidth,
			labelMaxHeight: entry.LabelMaxHeight,
			overlayHash:    entry.OverlayHash,
			overlayX:       entry.OverlayX,
			overlayY:       entry.OverlayY,
			overlayAnchor:  entry.OverlayAnchor,
			overlayWidth:   entry.OverlayWidth,
			overlayHeight:  entry.OverlayHeight,
		}
	}
	return nil
}

func (r *Runtime) drawKTFJavaImage(
	state *ktfGraphics,
	imageAddress uint32,
	source image.Image,
	x, y int,
	anchor uint32,
) {
	r.drawKTFJavaImageRaw(state, source, x, y, anchor)
	compat := r.menuForegroundCompat
	if compat == nil || state == nil || source == nil {
		return
	}
	bounds := source.Bounds()
	if bounds.Dx() <= compat.labelMaxWidth &&
		bounds.Dy() <= compat.labelMaxHeight {
		digest := sha256.Sum256(ktfRGBABytes(source))
		if _, ok := compat.labelHashes[digest]; ok {
			compat.pending = append(compat.pending, ktfJavaImageDraw{
				image: imageAddress,
				x:     x, y: y,
				anchor: anchor,
			})
			return
		}
	}
	if imageAddress != compat.overlayImage {
		if x != compat.overlayX || y != compat.overlayY ||
			anchor != compat.overlayAnchor ||
			bounds.Dx() != compat.overlayWidth ||
			bounds.Dy() != compat.overlayHeight ||
			sha256.Sum256(ktfRGBABytes(source)) != compat.overlayHash {
			return
		}
		compat.overlayImage = imageAddress
	}
	for _, pending := range compat.pending {
		pendingSource := r.images[pending.image]
		if pendingSource != nil {
			r.drawKTFJavaImageRaw(
				state,
				pendingSource,
				pending.x,
				pending.y,
				pending.anchor,
			)
		}
	}
	compat.pending = nil
}
