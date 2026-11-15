package binlog

import "testing"

// TC-BLD-3: the logger relies on string literals living in the const data range of the
// executable, each at its own address. Fail loudly if a Go release breaks that.
func TestStringAddressGuard(t *testing.T) {
	base, size := GetSelfTextAddressSize()
	a, b := "guard literal A %d", "guard literal B %d"
	addrA, addrB := getStringAddress(a), getStringAddress(b)
	if addrA == addrB {
		t.Fatalf("two different literals share the address %x: Go now deduplicates strings", addrA)
	}
	for _, addr := range []uint{addrA, addrB} {
		if addr < base || addr >= base+size {
			t.Fatalf("literal at %x is outside the const data range %x-%x: the L1 cache cannot work", addr, base, base+size)
		}
	}
}
