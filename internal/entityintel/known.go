package entityintel

import (
	"strings"
	"time"
)

// KnownSeed 是已知实体种子条目（公开标签/官方地址，带证据来源）。
type KnownSeed struct {
	Address     string
	ChainKey    string
	ChainID     int64
	EntityType  EntityType
	EntityName  string
	Label       string
	Source      LabelSource
	SourceName  string
	SourceURI   string
	Confidence  float64
	Observation string
}

// DefaultKnownEntities 返回系统内置的公开标签种子。
// 边界：这些是公开标签/官方公开地址关联，不构成充值/归集等现实功能结论。
func DefaultKnownEntities() []KnownSeed {
	return []KnownSeed{
		{
			Address: "0x8894e0a0c962cb723c1976a4421c95949be2d4e3",
			ChainKey: "bsc", ChainID: 56,
			EntityType: EntityExchange, EntityName: "Binance（公开标签关联地址）",
			Label: "exchange", Source: SourcePublicLabel,
			SourceName: "公开标签数据", Confidence: 0.95,
			Observation: "公开标签数据将该地址关联至 Binance 控制地址；具体功能（充值/归集/热钱包）未经调证确认。",
		},
		{
			Address: "0x55d398326f99059ff775485246999027b3197955",
			ChainKey: "bsc", ChainID: 56,
			EntityType: EntityTokenContract, EntityName: "BSC-USD（USDT）",
			Label: "token_contract", Source: SourceContractMetadata,
			SourceName: "链上合约元数据", Confidence: 0.99,
			Observation: "BSC 上广泛引用的 USDT 代币合约地址（公开链上元数据）。",
		},
		{
			Address: "0x10ed43c718714eb63d5aa57b78b54704e256024e",
			ChainKey: "bsc", ChainID: 56,
			EntityType: EntityRouter, EntityName: "PancakeSwap Router V2",
			Label: "router", Source: SourceProjectOfficial,
			SourceName: "PancakeSwap 官方文档", SourceURI: "https://docs.pancakeswap.finance",
			Confidence: 0.95,
			Observation: "PancakeSwap 官方文档公布的 BSC Router V2 合约地址。",
		},
		{
			Address: "0xbb4cdb9cbd36b01bd1cbaebf2de08d9173bc095c",
			ChainKey: "bsc", ChainID: 56,
			EntityType: EntityTokenContract, EntityName: "WBNB",
			Label: "token_contract", Source: SourceContractMetadata,
			SourceName: "链上合约元数据", Confidence: 0.98,
			Observation: "BSC Wrapped BNB 合约地址（公开链上元数据）。",
		},
	}
}

// ApplyKnownSeeds 将种子写入存储：实体 + 证据 + 地址条目。
func ApplyKnownSeeds(s *Store, seeds []KnownSeed) error {
	now := time.Now().UTC()
	for i, seed := range seeds {
		addr := strings.ToLower(strings.TrimSpace(seed.Address))
		entityID := "entity_" + sanitizeID(seed.EntityName)
		evID := "ev_known_" + addr
		ev := &EvidenceRef{
			EvidenceID:  evID,
			SourceType:  string(seed.Source),
			SourceName:  seed.SourceName,
			SourceURI:   seed.SourceURI,
			Observation: seed.Observation,
			CollectedAt: now,
			Confidence:  seed.Confidence,
		}
		if err := s.SaveEvidence(ev); err != nil {
			return err
		}
		entity := &Entity{
			ID: entityID, Name: seed.EntityName, EntityType: seed.EntityType,
			ChainIDs: []int64{seed.ChainID}, Addresses: []string{addr},
			Confidence: seed.Confidence, EvidenceIDs: []string{evID},
			Source: seed.Source, Version: 1, CreatedAt: now, UpdatedAt: now,
		}
		if err := s.SaveEntity(entity); err != nil {
			return err
		}
		label := AddressLabel{
			ChainID: seed.ChainID, Address: addr, Label: seed.Label,
			EntityID: entityID, Scope: ScopeGlobal, Confidence: seed.Confidence,
			EvidenceIDs: []string{evID}, ResolverVersion: "v1.0",
			CreatedAt: now, UpdatedAt: now,
		}
		entry := s.GetAddressEntry(seed.ChainID, addr)
		if entry == nil {
			entry = &AddressIntelligenceEntry{ChainID: seed.ChainID, ChainKey: seed.ChainKey, Address: addr}
		}
		entry.Labels = upsertLabel(entry.Labels, label)
		entry.EntityIDs = appendUnique(entry.EntityIDs, entityID)
		if err := s.SaveAddressEntry(seed.ChainID, seed.ChainKey, addr, entry); err != nil {
			return err
		}
		_ = i
	}
	return nil
}

func upsertLabel(labels []AddressLabel, l AddressLabel) []AddressLabel {
	for i := range labels {
		if labels[i].Label == l.Label && labels[i].EntityID == l.EntityID {
			labels[i] = l
			return labels
		}
	}
	return append(labels, l)
}

func appendUnique(list []string, v string) []string {
	for _, s := range list {
		if s == v {
			return list
		}
	}
	return append(list, v)
}

