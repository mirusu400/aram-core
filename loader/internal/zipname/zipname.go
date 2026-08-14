// Package zipname holds the ZIP member name sanitizer shared by the
// loader front-ends (zip-slip guard). The three per-loader copies were
// byte-identical; PHASE 4 consolidated them here (AUDIT §5 D11).
package zipname

import (
	"path"
	"strings"
)

func SafeName(name string) (string, bool) {
	name = strings.ReplaceAll(name, `\`, "/")
	if name == "" || strings.IndexByte(name, 0) >= 0 || strings.HasPrefix(name, "/") {
		return "", false
	}
	cleaned := path.Clean(name)
	if cleaned == "." || cleaned == ".." || strings.HasPrefix(cleaned, "../") {
		return "", false
	}
	if strings.Contains(strings.Split(cleaned, "/")[0], ":") {
		return "", false
	}
	return cleaned, cleaned == name
}
