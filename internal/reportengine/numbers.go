package reportengine

import "strconv"

func parseFloatAmount(s string) (float64, bool) {
	if s == "" {
		return 0, false
	}
	f, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return 0, false
	}
	return f, true
}

func shortID(s string) string {
	if len(s) <= 10 {
		return s
	}
	return s[2:10]
}
