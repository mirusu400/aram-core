// Package unicornbackend provides a development-only, runtime-loaded Unicorn
// implementation of ARAM's application-mode cpu.Backend contract.
//
// It lives in a nested module so its opt-in cgo loader and native validation do
// not participate in ARAM's default pure-Go build or dependency graph.
package unicornbackend
