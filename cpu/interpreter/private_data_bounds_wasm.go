//go:build wasm

package interpreter

// The portable WASM scalar path benefits from folding redundant region checks.
// This constant is resolved at compile time, not tested on each guest access.
const foldPrivateScalarBounds = true
