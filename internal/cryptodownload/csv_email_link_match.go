package cryptodownload

import "strings"

func findCSVEmailLink(links []string, kindName, address string) string {
	wantAddress := strings.ToLower(strings.TrimSpace(address))
	if wantAddress == "" {
		return ""
	}
	for _, link := range links {
		if csvClassifyLink(link) == kindName && strings.Contains(strings.ToLower(linkSearchText(link)), wantAddress) {
			return link
		}
	}
	return ""
}
