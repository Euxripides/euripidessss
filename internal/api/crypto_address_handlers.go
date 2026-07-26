package api

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"sort"
	"strings"
	"time"
	"unicode"

	"github.com/gin-gonic/gin"
)

const (
	defaultCryptoAddressAPIBaseURL = "https://www.oklink.com"
	maxCryptoAddressBatchSize      = 1000
)

var (
	evmAddressPattern       = regexp.MustCompile(`^0x[0-9a-fA-F]{40}$`)
	tronAddressPattern      = regexp.MustCompile(`^T[1-9A-HJ-NP-Za-km-z]{33}$`)
	btcLegacyAddressPattern = regexp.MustCompile(`^[13][1-9A-HJ-NP-Za-km-z]{25,34}$`)
	btcBech32AddressPattern = regexp.MustCompile(`^(?i:bc1)[0-9a-z]{25,90}$`)
	ltcAddressPattern       = regexp.MustCompile(`^(?i:ltc1)[0-9a-z]{25,90}$|^[LM3][1-9A-HJ-NP-Za-km-z]{25,34}$`)
	dogeAddressPattern      = regexp.MustCompile(`^D[1-9A-HJ-NP-Za-km-z]{25,34}$`)
	xrpAddressPattern       = regexp.MustCompile(`^r[1-9A-HJ-NP-Za-km-z]{24,34}$`)
	cardanoAddressPattern   = regexp.MustCompile(`^(?i:addr1)[0-9a-z]{20,120}$`)
	cosmosAddressPattern    = regexp.MustCompile(`^(cosmos|osmo|inj|terra|bnb)1[qpzry9x8gf2tvdw0s3jn54khce6mua7l]{20,100}$`)
	solanaAddressPattern    = regexp.MustCompile(`^[1-9A-HJ-NP-Za-km-z]{32,44}$`)
)

type cryptoAddressClassifyRequest struct {
	Addresses         string   `json:"addresses"`
	Chains            []string `json:"chains"`
	RPCNodes          []string `json:"rpc_nodes"`
	IncludeDuplicates bool     `json:"include_duplicates"`
}

type cryptoAddressClassifyResponse struct {
	Items    []cryptoAddressItem    `json:"items"`
	Summary  cryptoAddressSummary   `json:"summary"`
	Settings cryptoAddressAPIStatus `json:"settings"`
}

type cryptoAddressItem struct {
	Input      string                   `json:"input"`
	Address    string                   `json:"address"`
	Valid      bool                     `json:"valid"`
	Family     string                   `json:"family"`
	Kind       string                   `json:"kind"`
	Network    string                   `json:"network"`
	Confidence float64                  `json:"confidence"`
	Reason     string                   `json:"reason"`
	Candidates []cryptoAddressCandidate `json:"candidates"`
	Warnings   []string                 `json:"warnings,omitempty"`
}

type cryptoAddressCandidate struct {
	Chain      string  `json:"chain"`
	Name       string  `json:"name"`
	Family     string  `json:"family"`
	Confidence float64 `json:"confidence"`
	Source     string  `json:"source"`
	Status     string  `json:"status,omitempty"`
	Detail     string  `json:"detail,omitempty"`
}

type cryptoAddressSummary struct {
	Total        int            `json:"total"`
	Valid        int            `json:"valid"`
	Invalid      int            `json:"invalid"`
	Duplicates   int            `json:"duplicates"`
	FamilyCounts map[string]int `json:"family_counts"`
	ChainCounts  map[string]int `json:"chain_counts"`
}

type cryptoAddressAPIStatus struct {
	Provider     string `json:"provider"`
	VerifyOnline bool   `json:"verify_online"`
	BaseURL      string `json:"base_url"`
	UsedAPI      bool   `json:"used_api"`
}

// HandleCryptoAddressClassify classifies cryptocurrency address formats and
// optionally probes a configured chain API for stronger chain evidence.
func HandleCryptoAddressClassify(c *gin.Context) {
	var req cryptoAddressClassifyRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"detail": "请求格式错误: " + err.Error()})
		return
	}

	addresses := splitCryptoAddresses(req.Addresses)
	if len(addresses) == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"detail": "请至少输入一个地址"})
		return
	}
	if len(addresses) > maxCryptoAddressBatchSize {
		c.JSON(http.StatusBadRequest, gin.H{"detail": fmt.Sprintf("单次最多区分 %d 个地址", maxCryptoAddressBatchSize)})
		return
	}

	rpcPools := parseCryptoRPCNodes(req.RPCNodes)
	selectedChains := normalizeChainSet(req.Chains)

	items := make([]cryptoAddressItem, 0, len(addresses))
	seen := map[string]struct{}{}
	duplicates := 0
	apiUsed := false
	for _, input := range addresses {
		item := classifyCryptoAddress(input, selectedChains)
		key := strings.ToLower(item.Address)
		if _, exists := seen[key]; exists && key != "" {
			duplicates++
			if !req.IncludeDuplicates {
				continue
			}
			item.Warnings = append(item.Warnings, "重复地址")
		}
		seen[key] = struct{}{}

		if len(rpcPools) > 0 && item.Valid && len(item.Candidates) > 0 {
			apiUsed = true
			ctx, cancel := context.WithTimeout(c.Request.Context(), 12*time.Second)
			item.Candidates = verifyCryptoAddressWithRPCPool(ctx, rpcPools, item.Address, item.Candidates)
			cancel()
			item = recomputeCryptoAddressDecision(item)
		}
		items = append(items, item)
	}

	response := cryptoAddressClassifyResponse{
		Items:   items,
		Summary: summarizeCryptoAddressItems(items, duplicates),
		Settings: cryptoAddressAPIStatus{
			Provider: "rpc", VerifyOnline: len(rpcPools) > 0, BaseURL: "", UsedAPI: apiUsed,
		},
	}
	c.JSON(http.StatusOK, response)
}

func HandleCryptoDownload(c *gin.Context) {
	if cryptoDownload == nil {
		c.JSON(503, gin.H{"detail": "虚拟币数据下载服务不可用"})
		return
	}
	cryptoDownload.ServeHTTP(c.Writer, c.Request)
}

func splitCryptoAddresses(raw string) []string {
	fields := strings.FieldsFunc(raw, func(r rune) bool {
		return unicode.IsSpace(r) || strings.ContainsRune(",，;；|", r)
	})
	out := make([]string, 0, len(fields))
	for _, field := range fields {
		value := normalizeCryptoAddress(field)
		if value != "" {
			out = append(out, value)
		}
	}
	return out
}

func normalizeCryptoAddress(value string) string {
	return strings.Map(func(r rune) rune {
		if unicode.Is(unicode.Cf, r) {
			return -1
		}
		return r
	}, strings.TrimSpace(value))
}

func classifyCryptoAddress(input string, selectedChains map[string]struct{}) cryptoAddressItem {
	address := normalizeCryptoAddress(input)
	item := cryptoAddressItem{Input: input, Address: address}
	add := func(chain, name, family string, confidence float64, source string) {
		if len(selectedChains) > 0 {
			if _, ok := selectedChains[strings.ToUpper(chain)]; !ok {
				return
			}
		}
		item.Candidates = append(item.Candidates, cryptoAddressCandidate{
			Chain: strings.ToUpper(chain), Name: name, Family: family, Confidence: confidence, Source: source,
		})
	}

	switch {
	case address == "":
		item.Reason = "空地址"
	case evmAddressPattern.MatchString(address):
		item.Valid = true
		item.Family = "EVM"
		item.Kind = "账户/合约地址"
		for _, chain := range []struct{ code, name string }{
			{"ETH", "Ethereum"}, {"BSC", "BNB Smart Chain"}, {"POLYGON", "Polygon"},
			{"ARBITRUM", "Arbitrum One"}, {"BASE", "Base"}, {"OP", "Optimism"},
			{"AVAXC", "Avalanche C-Chain"}, {"FTM", "Fantom"}, {"LINEA", "Linea"},
			{"SCROLL", "Scroll"}, {"OPBNB", "opBNB"}, {"XLAYER", "X Layer"},
		} {
			add(chain.code, chain.name, "EVM", 0.7, "format")
		}
		item.Network = "多链候选"
		item.Confidence = 0.7
		item.Reason = "EVM 地址格式在多条链上通用，需结合链上 API 或交易来源确认具体链"
	case tronAddressPattern.MatchString(address):
		item.Valid, item.Family, item.Kind, item.Network, item.Confidence = true, "TRON", "账户地址", "TRON", 0.88
		add("TRON", "TRON", "TRON", 0.88, "format")
		item.Reason = "匹配 TRON Base58 地址格式"
	case btcBech32AddressPattern.MatchString(address) || btcLegacyAddressPattern.MatchString(address):
		item.Valid, item.Family, item.Kind, item.Network, item.Confidence = true, "Bitcoin", "账户地址", "Bitcoin", 0.86
		add("BTC", "Bitcoin", "Bitcoin", 0.86, "format")
		item.Reason = "匹配 Bitcoin 地址格式"
	case ltcAddressPattern.MatchString(address):
		item.Valid, item.Family, item.Kind, item.Network, item.Confidence = true, "Litecoin", "账户地址", "Litecoin", 0.84
		add("LTC", "Litecoin", "Litecoin", 0.84, "format")
		item.Reason = "匹配 Litecoin 地址格式"
	case dogeAddressPattern.MatchString(address):
		item.Valid, item.Family, item.Kind, item.Network, item.Confidence = true, "Dogecoin", "账户地址", "Dogecoin", 0.84
		add("DOGE", "Dogecoin", "Dogecoin", 0.84, "format")
		item.Reason = "匹配 Dogecoin 地址格式"
	case xrpAddressPattern.MatchString(address):
		item.Valid, item.Family, item.Kind, item.Network, item.Confidence = true, "XRP", "账户地址", "XRP Ledger", 0.84
		add("XRP", "XRP Ledger", "XRP", 0.84, "format")
		item.Reason = "匹配 XRP Ledger 地址格式"
	case cardanoAddressPattern.MatchString(address):
		item.Valid, item.Family, item.Kind, item.Network, item.Confidence = true, "Cardano", "账户地址", "Cardano", 0.82
		add("ADA", "Cardano", "Cardano", 0.82, "format")
		item.Reason = "匹配 Cardano Shelley 地址格式"
	case cosmosAddressPattern.MatchString(address):
		prefix := strings.ToUpper(address[:strings.IndexByte(address, '1')])
		item.Valid, item.Family, item.Kind, item.Network, item.Confidence = true, "Bech32", "账户地址", prefix, 0.8
		add(prefix, prefix, "Bech32", 0.8, "format")
		item.Reason = "匹配 Cosmos 生态 Bech32 地址格式"
	case solanaAddressPattern.MatchString(address):
		item.Valid, item.Family, item.Kind, item.Network, item.Confidence = true, "Base58", "账户地址", "Solana 候选", 0.48
		add("SOL", "Solana", "Base58", 0.48, "format")
		item.Reason = "匹配 Solana 常见 Base58 长度，但 Base58 格式存在较多误判，需要 API 复核"
	default:
		item.Reason = "未匹配常见虚拟币地址格式"
	}
	if len(selectedChains) > 0 && item.Valid && len(item.Candidates) == 0 {
		item.Valid = false
		item.Confidence = 0
		item.Reason = "地址格式有效，但不在当前选择的链范围内"
	}
	sortCandidates(item.Candidates)
	return item
}

func verifyCryptoAddressWithRPCPool(ctx context.Context, rpcPools cryptoRPCPools, address string, candidates []cryptoAddressCandidate) []cryptoAddressCandidate {
	out := make([]cryptoAddressCandidate, 0, len(candidates))
	for _, candidate := range candidates {
		nodeList := rpcPools[candidateChainKey(candidate.Chain)]
		if len(nodeList) == 0 {
			nodeList = rpcPools[""]
		}
		if len(nodeList) == 0 {
			candidate.Status = "unchecked"
			candidate.Detail = "未提供对应链的 RPC 节点"
			out = append(out, candidate)
			continue
		}
		result := probeCryptoAddressWithRPC(ctx, nodeList, address)
		candidate.Status = result.status
		candidate.Detail = result.detail
		if result.ok {
			candidate.Source = "rpc"
			candidate.Confidence = 0.98
		}
		out = append(out, candidate)
	}
	sortCandidates(out)
	return out
}

func recomputeCryptoAddressDecision(item cryptoAddressItem) cryptoAddressItem {
	sortCandidates(item.Candidates)
	if len(item.Candidates) == 0 {
		return item
	}
	best := item.Candidates[0]
	item.Confidence = best.Confidence
	if best.Status == "verified" {
		item.Network = best.Name
		item.Family = best.Family
		item.Reason = "API 校验命中 " + best.Name
	}
	return item
}

func summarizeCryptoAddressItems(items []cryptoAddressItem, duplicates int) cryptoAddressSummary {
	summary := cryptoAddressSummary{
		Total: len(items), Duplicates: duplicates,
		FamilyCounts: map[string]int{}, ChainCounts: map[string]int{},
	}
	for _, item := range items {
		if item.Valid {
			summary.Valid++
			if item.Family != "" {
				summary.FamilyCounts[item.Family]++
			}
			if len(item.Candidates) > 0 {
				summary.ChainCounts[item.Candidates[0].Chain]++
			}
		} else {
			summary.Invalid++
		}
	}
	return summary
}

func normalizeChainSet(chains []string) map[string]struct{} {
	set := map[string]struct{}{}
	for _, chain := range chains {
		value := strings.ToUpper(strings.TrimSpace(chain))
		if value != "" {
			set[value] = struct{}{}
		}
	}
	return set
}

func sortCandidates(candidates []cryptoAddressCandidate) {
	sort.SliceStable(candidates, func(i, j int) bool {
		if candidates[i].Confidence == candidates[j].Confidence {
			return candidates[i].Chain < candidates[j].Chain
		}
		return candidates[i].Confidence > candidates[j].Confidence
	})
}

type cryptoRPCPools map[string][]string

type cryptoRPCProbeResult struct {
	ok     bool
	status string
	detail string
}

func parseCryptoRPCNodes(lines []string) cryptoRPCPools {
	pools := cryptoRPCPools{}
	for _, raw := range lines {
		line := strings.TrimSpace(raw)
		if line == "" {
			continue
		}
		chain, endpoint := splitCryptoRPCNode(line)
		if endpoint == "" {
			continue
		}
		key := candidateChainKey(chain)
		pools[key] = append(pools[key], endpoint)
	}
	return pools
}

func splitCryptoRPCNode(line string) (chain, endpoint string) {
	for _, sep := range []string{"|", "=", " "} {
		if idx := strings.Index(line, sep); idx > 0 {
			left := strings.TrimSpace(line[:idx])
			right := strings.TrimSpace(line[idx+len(sep):])
			if looksLikeRPCURL(right) {
				return left, right
			}
		}
	}
	if looksLikeRPCURL(line) {
		return "", line
	}
	return "", ""
}

func looksLikeRPCURL(value string) bool {
	lower := strings.ToLower(strings.TrimSpace(value))
	return strings.HasPrefix(lower, "http://") || strings.HasPrefix(lower, "https://")
}

func candidateChainKey(chain string) string {
	return strings.ToUpper(strings.TrimSpace(chain))
}

func probeCryptoAddressWithRPC(ctx context.Context, nodes []string, address string) cryptoRPCProbeResult {
	var lastErr string
	client := &http.Client{Timeout: 12 * time.Second}
	for _, endpoint := range nodes {
		balance, code, err := probeAddressOnRPC(ctx, client, endpoint, address)
		if err == nil {
			return cryptoRPCProbeResult{
				ok:     true,
				status: "verified",
				detail: fmt.Sprintf("RPC 成功，balance=%s code=%s", balance, code),
			}
		}
		lastErr = err.Error()
		if isRPCQuotaError(err) {
			lastErr = "RPC 限流/额度耗尽，已切换下一节点: " + lastErr
			continue
		}
	}
	if lastErr == "" {
		lastErr = "RPC 校验失败"
	}
	return cryptoRPCProbeResult{status: "error", detail: lastErr}
}

func probeAddressOnRPC(ctx context.Context, client *http.Client, endpoint, address string) (balance string, code string, err error) {
	balance, err = callEthRPC(ctx, client, endpoint, "eth_getBalance", []any{address, "latest"})
	if err != nil {
		return "", "", err
	}
	code, err = callEthRPC(ctx, client, endpoint, "eth_getCode", []any{address, "latest"})
	if err != nil {
		return "", "", err
	}
	return balance, code, nil
}

func callEthRPC(ctx context.Context, client *http.Client, endpoint, method string, params []any) (string, error) {
	payload := map[string]any{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  method,
		"params":  params,
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(string(body)))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}
	if resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode == http.StatusForbidden || resp.StatusCode == http.StatusServiceUnavailable {
		return "", fmt.Errorf("rpc quota exhausted: http %d", resp.StatusCode)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("rpc http %d: %s", resp.StatusCode, truncateString(string(raw), 200))
	}
	var decoded struct {
		Result json.RawMessage `json:"result"`
		Error  *struct {
			Code    int    `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(raw, &decoded); err != nil {
		return "", fmt.Errorf("rpc decode: %w", err)
	}
	if decoded.Error != nil {
		msg := strings.ToLower(decoded.Error.Message)
		if decoded.Error.Code == -32005 || strings.Contains(msg, "quota") || strings.Contains(msg, "rate limit") || strings.Contains(msg, "too many requests") {
			return "", fmt.Errorf("rpc quota exhausted: %s", decoded.Error.Message)
		}
		return "", fmt.Errorf("rpc error: %s", decoded.Error.Message)
	}
	return string(decoded.Result), nil
}

func isRPCQuotaError(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "quota exhausted") || strings.Contains(msg, "rate limit") || strings.Contains(msg, "too many requests")
}

func truncateString(value string, limit int) string {
	if len(value) <= limit {
		return value
	}
	return value[:limit]
}

func oklinkChainSlug(chain string) string {
	switch strings.ToUpper(strings.TrimSpace(chain)) {
	case "ETH":
		return "eth"
	case "BSC":
		return "bsc"
	case "POLYGON":
		return "polygon"
	case "ARBITRUM":
		return "arbitrum"
	case "BASE":
		return "base"
	case "OP":
		return "optimism"
	case "AVAXC":
		return "avaxc"
	case "FTM":
		return "ftm"
	case "LINEA":
		return "linea"
	case "SCROLL":
		return "scroll"
	case "OPBNB":
		return "opbnb"
	case "XLAYER":
		return "xlayer"
	case "TRON":
		return "tron"
	case "BTC":
		return "btc"
	case "LTC":
		return "ltc"
	case "DOGE":
		return "doge"
	default:
		return ""
	}
}
