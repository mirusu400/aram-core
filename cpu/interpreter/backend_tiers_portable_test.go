//go:build !(windows && amd64) && !((android || linux) && arm64) && !(darwin && arm64 && cgo)

package interpreter

func armExecutionTierConstructors() []backendConstructor {
	return []backendConstructor{
		{name: "precise", new: New},
		{name: "go-jit", new: NewJIT},
	}
}
