package contractintelligence

import "strings"

// NormalizeCreationType gives semantic classifications precedence over the
// raw opcode because those classifications are more useful to investigators.
func NormalizeCreationType(raw, factoryAddress string, isProxy, tokenDetected bool) CreationType {
	if tokenDetected {
		return CreationToken
	}
	if isProxy {
		return CreationProxy
	}
	if strings.TrimSpace(factoryAddress) != "" {
		return CreationFactory
	}
	normalized := strings.NewReplacer("-", "", "_", "", " ", "").Replace(strings.ToUpper(strings.TrimSpace(raw)))
	switch normalized {
	case "CREATE2":
		return CreationCreate2
	case "FACTORY", "FACTORYCREATED":
		return CreationFactory
	case "PROXY", "PROXYCREATED":
		return CreationProxy
	case "TOKEN", "TOKENCREATED":
		return CreationToken
	default:
		return CreationCreate
	}
}
