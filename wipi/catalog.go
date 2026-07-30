// Package wipi defines the recovered WIPI 1.2.1 public C API ABI and guest
// import layout. Runtime behavior belongs here only when it is standard WIPI
// behavior; carrier, vendor, device, and title services remain separate.
package wipi

import "sort"

// API is one recovered public WIPI-C selector.
type API struct {
	Ordinal       int
	Family        string
	Name          string
	Slot          uint32
	Prototype     string
	SelectorKind  string
	Confidence    string
	SelectorState string
}

var (
	apiByName   map[string]API
	apiByFamily map[string]map[uint32]API
)

func init() {
	apiByName = make(map[string]API, len(generatedAPIs))
	apiByFamily = make(map[string]map[uint32]API)
	for _, api := range generatedAPIs {
		apiByName[api.Name] = api
		family := apiByFamily[api.Family]
		if family == nil {
			family = make(map[uint32]API)
			apiByFamily[api.Family] = family
		}
		family[api.Slot] = api
	}
}

// APIs returns the public selectors in recovered ordinal order.
func APIs() []API {
	result := make([]API, len(generatedAPIs))
	copy(result, generatedAPIs[:])
	return result
}

// Lookup returns a public selector by C function name.
func Lookup(name string) (API, bool) {
	api, ok := apiByName[name]
	return api, ok
}

// Resolve returns the selector bound to a package family and byte slot.
func Resolve(family string, slot uint32) (API, bool) {
	api, ok := apiByFamily[family][slot]
	return api, ok
}

// Families returns the recovered package names in lexical order.
func Families() []string {
	result := make([]string, 0, len(apiByFamily))
	for family := range apiByFamily {
		result = append(result, family)
	}
	sort.Strings(result)
	return result
}

// FamilyCounts returns a fresh count map so callers cannot mutate the catalog.
func FamilyCounts() map[string]int {
	result := make(map[string]int, len(apiByFamily))
	for family, selectors := range apiByFamily {
		result[family] = len(selectors)
	}
	return result
}
