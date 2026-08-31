package unicornbackend

import (
	"errors"
	"fmt"
	"os"
	"runtime"
	"strings"
	"unsafe"
)

const LibraryEnvironment = "ARAM_UNICORN_LIBRARY"

type unicornAPI struct {
	handle unsafe.Pointer
	path   string
	major  uint32
	minor  uint32
}

func loadUnicornAPI(options Options) (*unicornAPI, error) {
	paths := unicornLibraryPaths(options.LibraryPath)
	if len(paths) == 0 {
		return nil, fmt.Errorf("%w: no library candidates for %s", ErrUnavailable, runtime.GOOS)
	}
	var attempts []error
	for _, path := range paths {
		api, err := openUnicornAPI(path)
		if err == nil {
			return api, nil
		}
		attempts = append(attempts, fmt.Errorf("%s: %w", path, err))
	}
	return nil, fmt.Errorf("%w: %w", ErrUnavailable, errors.Join(attempts...))
}

func unicornLibraryPaths(explicit string) []string {
	if explicit = strings.TrimSpace(explicit); explicit != "" {
		return []string{explicit}
	}
	defaults := platformLibraryCandidates()
	paths := make([]string, 0, len(defaults)+1)
	if configured := strings.TrimSpace(os.Getenv(LibraryEnvironment)); configured != "" {
		paths = append(paths, configured)
	}
	paths = append(paths, defaults...)
	seen := make(map[string]struct{}, len(paths))
	unique := paths[:0]
	for _, path := range paths {
		if _, ok := seen[path]; ok {
			continue
		}
		seen[path] = struct{}{}
		unique = append(unique, path)
	}
	return unique
}

func platformLibraryCandidates() []string {
	switch runtime.GOOS {
	case "windows":
		return []string{"unicorn.dll", "libunicorn.dll"}
	case "darwin":
		return []string{"libunicorn.2.dylib", "libunicorn.dylib"}
	case "freebsd", "linux":
		return []string{"libunicorn.so.2", "libunicorn.so"}
	default:
		return nil
	}
}

func (api *unicornAPI) callError(operation string, code int32) error {
	if code == ucErrOK {
		return nil
	}
	name, ok := unicornErrorNames[code]
	if !ok {
		name = fmt.Sprintf("UC_ERR_%d", code)
	}
	return &unicornCallError{Operation: operation, Code: code, Name: name}
}

type unicornCallError struct {
	Operation string
	Code      int32
	Name      string
}

func (err *unicornCallError) Error() string {
	return fmt.Sprintf("Unicorn %s: %s", err.Operation, err.Name)
}
