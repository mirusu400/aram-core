# LGT GraphicsX object identity

Public behavioral source read on 2026-09-11:
https://nikita36078.github.io/J2ME_Docs/docs/LG_MMPP_API/mmpp/microedition/lcdui/GraphicsX.html

The Javadoc declares `mmpp.microedition.lcdui.GraphicsX` as a subclass of
`javax.microedition.lcdui.Graphics`. Its introductory contract states that all
Graphics objects in that implementation are GraphicsX instances, obtained by
casting an existing Graphics object. This is sufficient evidence for object
allocation and inheritance, not an invitation to bypass Java type checks.

Under explicit `NativePolicyLGT`, screen/Canvas graphics, mutable Image graphics,
and GameCanvas backing graphics are allocated with the GraphicsX class and the
existing serialized graphics state. The exact host superclass relationship is
registered. Generic J2ME and SKT retain their existing Graphics identities and
registries. Neither `IsInstance` nor bytecode checkcast semantics change.

This implements identity only. GraphicsX extension APIs, including the documented
`setAlpha(I)V`, remain unimplemented and fail explicitly. No constructor,
extension method, field value, rendering behavior, or namespace-wide OEM support
is invented. Shared graphics services and the native state representation are
unchanged.

Synthetic tests cover all three producers across LGT/J2ME/SKT, real superclass
assignability, rejection of unrelated casts, inherited standard native resolution
and state, absence of extension methods, and byte-identical save/restore replay.
Tests were observed failing on the LGT identities before implementation.

The ordinary SHA-matched original probe moved past the previously failing cast
and stopped at the unimplemented GraphicsX setAlpha method. That establishes
only this specific allocation/cast correction, not playability or support for
GraphicsX extension rendering.
