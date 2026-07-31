package main

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/csv"
	"encoding/hex"
	"flag"
	"fmt"
	"math/big"
	"os"
	"strings"
)

// ── BSC 已知活跃合约地址（来自BscScan已验证合约）──
var knownBSCContracts = []string{
	"0x55d398326f99059ff775485246999027b3197955", // USDT
	"0x2170ed0880ac9a755fd29b2688956bd959f933f8", // ETH
	"0x7130d2a12b9bcbfae4f2634d864a1ee1ce3ead9c", // BTCB
	"0xe9e7cea3dedca5984780bafc599bd69add087d56", // BUSD
	"0x0e09fabb73bd3ade0a17ecc321fd13a19e81ce82", // CAKE
	"0xbb4cdb9cbd36b01bd1cbaebf2de08d9173bc095c", // WBNB
	"0x8ac76a51cc950d9822d68b83fe1ad97b32cd580d", // USDC
	"0xcf6bb5389c92bdda8a3747ddb454cb7a64626c63", // XVS
	"0x1af3f329e8be154074d8769d1ffa4ee058b1dbc3", // DAI
	"0xbf5140a22578168fd562dccf235e5d43a02ce9b1", // UNI
	"0xf8a0bf9cf54bb92f17374df9eed3215978b52158", // LINK
	"0x52ce071bd9b1c4b00a0b92d298c512478cad67e8", // COMP
	"0x250632378e573c6be1ac2f97fcdf00515d0aa91b", // BETH
	"0x3ee2200efb3400fabb9aacf31297cbdd1d435d47", // ADA
	"0x7083609fce4d1d8dc0c979aab8c869ea2c873402", // DOT
	"0x4338665cbb7b2485a8855a139b75d5e34ad0ff94", // LTC
	"0x1d2f0da169ceb9fc7b3144628db156f3f6c60dbe", // XRP
	"0xba2ae424d960c26247dd6c32edc70b295c744c43", // DOGE
	"0x2859e4544c4bb03966803b044a93528bd2d6e624", // SHIB
	"0xa2b726b1145a4773f68593cf171187d8ebe4d495", // INJ
	// 高流动性 DEX/借贷
	"0x10ed43c718714eb63d5aa57b78b54704e256024e", // PancakeSwap Router v2
	"0x05ff2b0db69458a0750badebc4f9e13add608c7f", // PancakeSwap Factory
	"0xcA143Ce32Fe78f1f7019d7d551a6402fC5350c73", // PancakeSwap LP
}

// ── ETH 已知活跃地址 ──
var knownETHContracts = []string{
	"0xdac17f958d2ee523a2206206994597c13d831ec7", // USDT
	"0xa0b86991c6218b36c1d19d4a2e9eb0ce3606eb48", // USDC
	"0x6b175474e89094c44da98b954eedeac495271d0f", // DAI
	"0xc02aaa39b223fe8d0a0e5c4f27ead9083c756cc2", // WETH
	"0x7a250d5630b4cf539739df2c5dacb4c659f2488d", // Uniswap V2 Router
	"0x1f9840a85d5af5bf1d1762f925bdaddc4201f984", // UNI
	"0x514910771af9ca656af840dff83e8264ecf986ca", // LINK
	"0x2260fac5e5542a773aa44fbcfedf7c193bc2c599", // WBTC
	"0x7fc66500c84a76ad7e9c93437bfc5ac33e2ddae9", // AAVE
	"0x4fabb145d64652a948d72533023f6e7a623c7c53", // BUSD
}

// genBSCAddress generates a valid BSC EOA-style address.
func genBSCAddress() string {
	bytes := make([]byte, 20)
	_, _ = rand.Read(bytes)
	return "0x" + hex.EncodeToString(bytes)
}

// genETHAddress generates a valid ETH EOA-style address.
func genETHAddress() string {
	bytes := make([]byte, 20)
	_, _ = rand.Read(bytes)
	return "0x" + hex.EncodeToString(bytes)
}

// genDuplicate returns a previously generated address (simulated by reusing known contracts).
func genDuplicate(known []string) string {
	idx, _ := rand.Int(rand.Reader, big.NewInt(int64(len(known))))
	return known[idx.Int64()]
}

// genInvalid generates an intentionally invalid address for edge case testing.
func genInvalid() string {
	switch mustRandInt(4) {
	case 0:
		return "0x" + strings.Repeat("g", 40) // hex only
	case 1:
		return "0x" + strings.Repeat("0", 39) // too short
	case 2:
		return "bc1" + strings.Repeat("a", 39) // BTC format
	case 3:
		return "" // empty
	default:
		return "not_an_address"
	}
}

func mustRandInt(n int) int {
	v, _ := rand.Int(rand.Reader, big.NewInt(int64(n)))
	return int(v.Int64())
}

// toChecksumAddress converts a lowercase hex addr to EIP-55 checksummed (approximate).
func toChecksumAddress(addr string) string {
	if len(addr) != 42 || addr[:2] != "0x" {
		return addr
	}
	hash := sha256.Sum256([]byte(strings.ToLower(addr[2:])))
	hashHex := hex.EncodeToString(hash[:])
	var result strings.Builder
	result.WriteString("0x")
	for i := 0; i < 40; i++ {
		c := addr[2+i]
		if c >= '0' && c <= '9' {
			result.WriteByte(c)
		} else {
			// "checksum" = uppercase if hash nibble >= 8
			h := hashHex[i]
			if h >= '8' {
				result.WriteByte(c - 32) // uppercase
			} else {
				result.WriteByte(c)
			}
		}
	}
	return result.String()
}

func main() {
	count := flag.Int("count", 10000, "number of addresses to generate")
	chain := flag.String("chain", "bsc", "chain: bsc or eth")
	mode := flag.String("mode", "synthetic", "synthetic or real")
	output := flag.String("output", "addresses.csv", "output CSV file")
	dupRate := flag.Float64("duplicate-rate", 0.05, "duplicate rate 0-1")
	invalidRate := flag.Float64("invalid-rate", 0.03, "invalid rate 0-1")
	flag.Parse()

	total := *count
	dupCount := int(float64(total) * *dupRate)
	invalidCount := int(float64(total) * *invalidRate)
	// 70% normal + 20% high-activity = 90% valid synthetic
	highActivityCount := int(float64(total) * 0.20)
	normalCount := total - dupCount - invalidCount - highActivityCount
	if normalCount < 0 {
		normalCount = total / 2
		highActivityCount = total / 4
		dupCount = total - normalCount - highActivityCount - invalidCount
	}

	var knownContracts []string
	if *chain == "eth" {
		knownContracts = knownETHContracts
	} else {
		knownContracts = knownBSCContracts
	}

	file, err := os.Create(*output)
	if err != nil {
		fmt.Fprintf(os.Stderr, "create: %v\n", err)
		os.Exit(1)
	}
	defer file.Close()
	w := csv.NewWriter(file)

	gen := map[string]func() string{
		"bsc": genBSCAddress,
		"eth": genETHAddress,
	}[*chain]
	if gen == nil {
		gen = genBSCAddress
	}

	seen := make(map[string]bool)
	written := 0

	// 1. Normal EOA (70%)
	for i := 0; i < normalCount; i++ {
		addr := gen()
		if *mode == "real" && i < len(knownContracts) {
			addr = knownContracts[i%len(knownContracts)]
		}
		if err := w.Write([]string{addr}); err != nil {
			fmt.Fprintf(os.Stderr, "write: %v\n", err)
			os.Exit(1)
		}
		seen[addr] = true
		written++
	}

	// 2. High-activity (20%) — reuse known contracts with checksum variants
	for i := 0; i < highActivityCount; i++ {
		addr := knownContracts[i%len(knownContracts)]
		if i%3 == 0 {
			addr = toChecksumAddress(addr)
		} else if i%3 == 1 {
			addr = strings.ToUpper(addr) // all uppercase variant
		}
		if err := w.Write([]string{addr}); err != nil {
			fmt.Fprintf(os.Stderr, "write: %v\n", err)
			os.Exit(1)
		}
		seen[addr] = true
		written++
	}

	// 3. Duplicates (5%) — from seen set
	dupAddrs := make([]string, 0, len(seen))
	for a := range seen {
		dupAddrs = append(dupAddrs, a)
	}
	for i := 0; i < dupCount && len(dupAddrs) > 0; i++ {
		addr := dupAddrs[mustRandInt(len(dupAddrs))]
		_ = w.Write([]string{addr})
		written++
	}

	// 4. Invalid (3%)
	for i := 0; i < invalidCount; i++ {
		addr := genInvalid()
		if addr != "" {
			_ = w.Write([]string{addr})
			written++
		}
	}

	w.Flush()
	fmt.Printf("Generated %d addresses → %s (mode=%s chain=%s dup=%.0f%% invalid=%.0f%%)\n",
		written, *output, *mode, *chain, *dupRate*100, *invalidRate*100)
}
