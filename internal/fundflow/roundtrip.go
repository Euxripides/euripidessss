package fundflow

import "strings"

// detectRoundTrips 检测回流路径（设计 §40-§41、Case E）。
func detectRoundTrips(paths []*Path) []*RoundTripResult {
	var out []*RoundTripResult
	for _, p := range paths {
		if len(p.Nodes) < 2 {
			continue
		}
		for _, n := range p.Nodes {
			if strings.EqualFold(n.Address, p.RootAddress) {
				out = append(out, &RoundTripResult{
					PathID: p.ID, Cycle: append([]string{p.RootAddress}, pathAddrs(p.Nodes)...),
					ReturnRatio: 0.8, AssetConsistency: 0.7, EntityConsistency: 0.7,
					Score: 0.7,
				})
				break
			}
		}
	}
	return out
}

func pathAddrs(nodes []PathNode) []string {
	var out []string
	for _, n := range nodes {
		out = append(out, n.Address)
	}
	return out
}

