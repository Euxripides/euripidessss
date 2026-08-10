package contractintelligence

import "bytes"

var (
	minimalPrefix  = []byte{0x36, 0x3d, 0x3d, 0x37, 0x3d, 0x3d, 0x3d, 0x36, 0x3d, 0x73}
	minimalSuffix  = []byte{0x5a, 0xf4, 0x3d, 0x82, 0x80, 0x3e, 0x90, 0x3d, 0x91, 0x60, 0x2b, 0x57, 0xfd, 0x5b, 0xf3}
	uupsSelector   = []byte{0x52, 0xd1, 0x90, 0x2d} // proxiableUUID()
	adminSelectors = [][]byte{
		{0xf8, 0x51, 0xa4, 0x40}, // admin()
		{0x8f, 0x28, 0x39, 0x70}, // changeAdmin(address)
	}
)

// DetectProxy deterministically classifies captured runtime and storage
// evidence. Empty/zero storage is treated as absent evidence.
func DetectProxy(evidence ProxyEvidence) ProxyDetection {
	if implementation, ok := minimalProxyImplementation(evidence.RuntimeCode); ok {
		return ProxyDetection{IsProxy: true, ProxyType: ProxyMinimal1167, ImplementationAddress: implementation}
	}
	beacon := slotAddress(evidence.Storage[BeaconSlot])
	if beacon != "" {
		return ProxyDetection{IsProxy: true, ProxyType: ProxyBeacon, BeaconAddress: beacon}
	}
	implementation := slotAddress(evidence.Storage[ImplementationSlot])
	if implementation == "" {
		return ProxyDetection{}
	}
	admin := slotAddress(evidence.Storage[AdminSlot])
	if admin != "" || containsAny(evidence.RuntimeCode, adminSelectors) {
		return ProxyDetection{IsProxy: true, ProxyType: ProxyTransparent, ImplementationAddress: implementation}
	}
	if bytes.Contains(evidence.RuntimeCode, uupsSelector) || bytes.Contains(evidence.ImplementationRuntimeCode, uupsSelector) {
		return ProxyDetection{IsProxy: true, ProxyType: ProxyUUPS, ImplementationAddress: implementation}
	}
	return ProxyDetection{IsProxy: true, ProxyType: ProxyEIP1967, ImplementationAddress: implementation}
}

func minimalProxyImplementation(code []byte) (string, bool) {
	if len(code) != 45 || !bytes.Equal(code[:10], minimalPrefix) || !bytes.Equal(code[30:], minimalSuffix) {
		return "", false
	}
	return "0x" + lowerHex(code[10:30]), true
}

func slotAddress(value [32]byte) string {
	for _, part := range value[12:] {
		if part != 0 {
			return "0x" + lowerHex(value[12:])
		}
	}
	return ""
}

func lowerHex(value []byte) string {
	const alphabet = "0123456789abcdef"
	result := make([]byte, len(value)*2)
	for i, part := range value {
		result[i*2] = alphabet[part>>4]
		result[i*2+1] = alphabet[part&0x0f]
	}
	return string(result)
}

func containsAny(code []byte, values [][]byte) bool {
	for _, value := range values {
		if bytes.Contains(code, value) {
			return true
		}
	}
	return false
}
