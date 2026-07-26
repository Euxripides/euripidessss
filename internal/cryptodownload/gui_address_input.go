package cryptodownload

import (
	"strings"
	"unicode"
)

func normalizeGUIAddress(value string) string {
	return strings.Map(func(r rune) rune {
		if unicode.Is(unicode.Cf, r) {
			return -1
		}
		return r
	}, strings.TrimSpace(value))
}
