//go:build !wasm

package interpreter

// Preserve native code generation: the broader candidate did not establish
// a non-regression for the product's native/Go hybrid execution path.
const foldPrivateScalarBounds = false
