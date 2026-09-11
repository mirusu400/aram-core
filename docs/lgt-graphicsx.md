# LGT GraphicsX identity and alpha

Public behavioral source read on 2026-09-11:
https://nikita36078.github.io/J2ME_Docs/docs/LG_MMPP_API/mmpp/microedition/lcdui/GraphicsX.html

## Documented contract and policy scope

The Javadoc declares `mmpp.microedition.lcdui.GraphicsX` as a subclass of
`javax.microedition.lcdui.Graphics`. All Graphics objects in that implementation
are GraphicsX instances, obtained by casting an existing Graphics object.
Under explicit `NativePolicyLGT`, screen/Canvas, mutable Image, and GameCanvas
backing graphics therefore have that exact class and registered superclass.
Generic J2ME and SKT retain their Graphics identities and native registries.
No checkcast, assignability, title-specific dispatch, or namespace rule changes.

`setAlpha(I)V` sets the current context's alpha, with integer values 0 through
256 inclusive. Zero is transparent, 256 is opaque, and `DEFAULT_ALPHA` is
initialized to 256. Values outside the range throw Java
`IllegalArgumentException` without changing the current alpha or color.
The public field is not declared final, so guest writes use ordinary static
field state. New graphics contexts and Canvas paint reset use the documented
initial value 256, not a mutable field lookup.

## Rendering scope

Alpha is stored per Graphics object, independently of color and of other
Graphics objects targeting the same image. Inherited base-class drawing
entries temporarily install the context's factor in the shared surface draw
state, then restore the original state on success or error. This covers:

- lines, rectangles, arcs, rounded-rectangle entries, and filled triangles;
- strings, substrings, characters, and character arrays through shared Text;
- image blits (including the optimized opaque-copy path guard), transformed
  regions, packed RGB with either processAlpha value, and overlapping copyArea;
- Sprite and TiledLayer direct region calls, including LayerManager's nested
  clipping/translation scope;
- GameCanvas.paint drawing into a Graphics context. GameCanvas.flushGraphics
  copies already-rendered backing pixels, without applying backing or screen
  context alpha again.

The runtime service has an optional `GlobalTransparency256` factor. Its zero
value leaves all existing 255-scale alpha/raster behavior unchanged. Only the
LGT adapter opts into the extra factor here. Translucent rectangle outlines
write each covered pixel once, including corners and degenerate dimensions.
The existing legacy outline path remains unchanged for other clients.

### Deliberate emulator choices, not verified handset rounding

The Javadoc does not specify intermediate rounding or source-alpha combination.
The emulator retains the exact 257-level factor through the final blend, rather
than mapping it to uint8. With source alpha `sA` in 0..255 and context alpha `a`
in 0..256, `p = sA * a` and `D = 255 * 256`. For each RGB channel it uses
`floor((source*p + destination*(D-p) + D/2) / D)`. Output alpha uses the same
formula with source alpha-channel value 255. Thus white on opaque black at
context alpha 128 is exactly 128, while context alpha 255 produces 254 and
context alpha 256 produces 255. A half-alpha white source at context alpha128
produces 64 on opaque black. Ties round upward. Fully transparent source texels
remain transparent. These are deterministic emulator choices, not a claim of
bit-exact LG handset rendering.

The existing shared `GlobalAlpha` byte is applied as before if a service client
also sets it. LGT GraphicsX does not expose that separate legacy control.

## Save, replay, and reset

Graphics native state stores inverse alpha (`256-alpha`) in its previously
unused `Offset` scalar. Zero means opaque, so old graphics payloads keep their
original rendering. The decoder rejects values outside 0..256. Explicit alpha
zero is stored as 256 and survives restore without being mistaken for absent
state. The added service field is zero-default and omitted from JSON when zero.

Old LGT snapshots also lack the newly registered `DEFAULT_ALPHA` host static.
Only that missing field is migrated to 256 before the existing strict host
static shape/type validation. No other missing host statics are accepted.
Canvas paint reset restores opaque alpha. Image and GameCanvas contexts retain
their own alpha until changed or recreated. Temporary draw scope never persists
into another context or presentation operation.

## Verification and limitations

Synthetic tests first failed on the missing native. Exact literal pixel tests
cover 16 inherited drawing entries at alpha0,1,64,128,255,256. Additional tests
cover source alpha, clipping/translation, repeated image contexts, invalid
bounds, error cleanup, rectangle corner/degenerate coverage, game layers,
GameCanvas paint/flush, old snapshots, explicit transparent roundtrip, malformed
state, and byte-identical save/replay. A synthetic guest class enters through
public `VM.InvokeStatic`, executes real checkcast/invokevirtual (including
inherited methodrefs naming GraphicsX), catches invalid alpha in Java bytecode,
and checks the public framebuffer. Shared service tests preserve legacy alpha
and raster output and reject malformed draw state transactionally.

This is alpha support across the existing inherited renderer, not full GraphicsX
or exact MIDP geometry conformance. Existing `drawRoundRect`/`fillRoundRect`
render ordinary rectangles and ignore corner radii. DOTTED stroke is remembered
but not rasterized. Existing fallback fonts and arc rasterization are unchanged.
GraphicsX capture, pixel access, polygons, XOR and paint-mode extension methods
remain unsupported and fail explicitly. No proprietary input, title patch, or
private reference bytes are used by these tests. Passing this API does not
establish gameplay, playability, or complete title support.

The coordinator's ordinary probe of the same authorized original under explicit
LGT policy moved past the former post-OK setAlpha fault at instruction 13,743.
The short 128-slice OK response reached instruction 16,299 with a changed frame
and no fault. A longer 4,096-slice observation reached instruction 43,907 and
576 presentations, then stopped at a MediaPlayer fault (43,965 with repeated
OK input). This bounds the improvement to this alpha/input-response transition.
It is not a reference-screen comparison or proof of sustained gameplay. The
remaining media failure is outside this graphics change.
