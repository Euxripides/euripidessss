package fundflow

import "fmt"

func fmtSscanf(s string, dst *float64) (int, error) {
	return fmt.Sscanf(s, "%f", dst)
}

func fmtSscanfUint(s string, dst *uint64) (int, error) {
	return fmt.Sscanf(s, "%d", dst)
}
