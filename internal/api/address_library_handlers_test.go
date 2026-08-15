package api

import "testing"

func TestNormalizeAddressLibraryInput(t *testing.T) {
	valid, invalid, duplicates := normalizeAddressLibraryInput([]string{
		" 0x0000000000000000000000000000000000000011 ",
		"0X0000000000000000000000000000000000000011",
		"not-an-address",
		"0x0000000000000000000000000000000000000012",
	})
	if len(valid) != 2 || duplicates != 1 || len(invalid) != 1 {
		t.Fatalf("unexpected normalize valid=%v invalid=%v duplicates=%d", valid, invalid, duplicates)
	}
}
