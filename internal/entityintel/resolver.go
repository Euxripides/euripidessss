package entityintel

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
)

// ChainResolver 将链名解析为链 ID。
type ChainResolver func(chainKey string) (int64, error)

// Resolver 是 Address Label Resolver V2（设计 §17-§27、§43-§44、§47-§48、§53-§54）。
type Resolver struct {
	store     *Store
	src       FeatureSource
	extractor *FeatureExtractor
	known     map[string]KnownSeed
	resolveChain ChainResolver
	now       func() time.Time
	cacheHits   int64
	cacheMisses int64
	knownHits   int64
}

// NewResolver 创建解析器并应用已知实体种子。
func NewResolver(store *Store, src FeatureSource, resolveChain ChainResolver, seeds []KnownSeed) (*Resolver, error) {
	r := &Resolver{
		store: store, src: src, extractor: NewFeatureExtractor(src),
		known: map[string]KnownSeed{}, resolveChain: resolveChain, now: time.Now,
	}
	for _, seed := range seeds {
		r.known[strings.ToLower(strings.TrimSpace(seed.Address))] = seed
	}
	if err := ApplyKnownSeeds(store, seeds); err != nil {
		return nil, err
	}
	return r, nil
}

// Resolve 解析单个地址（Case A/B/C 核心路径）。
func (r *Resolver) Resolve(ctx context.Context, chainKey, address, investigationID string) (*Resolution, error) {
	chainKey = strings.ToLower(strings.TrimSpace(chainKey))
	address = strings.ToLower(strings.TrimSpace(address))
	if r.resolveChain == nil {
		return nil, fmt.Errorf("entityintel: 链解析器未配置")
	}
	chainID, err := r.resolveChain(chainKey)
	if err != nil {
		return nil, err
	}
	now := r.now().UTC()
	res := &Resolution{Address: address, ChainKey: chainKey, ChainID: chainID, ResolvedAt: now}

	entry := r.store.GetAddressEntry(chainID, address)
	manual := []*ManualLabel{}
	if investigationID != "" {
		manual = r.store.ListManualLabels(investigationID)
	}
	if entry != nil && len(entry.Labels) > 0 && now.Sub(entry.UpdatedAt) < 5*time.Minute {
		res = r.fromEntry(entry)
		res.CacheHit = true
		r.cacheHits++
	} else {
		r.cacheMisses++
		feat, err := r.extractor.Extract(ctx, address)
		if err != nil {
			feat = &AddressFeature{}
		}
		res.Feature = feat
		if entry == nil {
			entry = &AddressIntelligenceEntry{ChainID: chainID, ChainKey: chainKey, Address: address}
		}
		entry.Feature = feat

		// 1) 已知实体映射（静态标签，最高优先级）
		if seed, ok := r.known[address]; ok {
			r.knownHits++
			r.applyKnown(entry, seed, res)
		}
		// 2) 合约解析
		if feat.IsContract && len(entry.Labels) == 0 {
			r.applyContract(entry, res)
		}
		// 3) 行为模式解析（Deposit / Hot Wallet / Collector / Dormancy）
		//    仅在没有全局标签时运行，避免行为推断覆盖已知实体（设计 §18、§48）
		if len(entry.Labels) == 0 {
			r.applyPatterns(ctx, entry, res, chainKey, investigationID)
		}
		// 4) 聚类（Common Sweep）
		r.applyCluster(ctx, entry, res, chainKey)
		// 5) 冲突检查（不静默覆盖）
		r.applyConflicts(entry, res)
		if err := r.store.SaveAddressEntry(chainID, chainKey, address, entry); err != nil {
			return nil, err
		}
		res = r.fromEntry(entry)
	}
	// 案件自定义标签（作用域 INVESTIGATION，不写全局实体）
	for _, m := range manual {
		if !strings.EqualFold(m.Address, address) || !strings.EqualFold(m.ChainKey, chainKey) {
			continue
		}
		res.Labels = append(res.Labels, AddressLabel{
			ChainID: chainID, Address: address, Label: m.Label,
			Scope: ScopeInvestigation, Confidence: m.Confidence,
			EvidenceIDs: []string{"ev_manual_" + m.ID},
			ResolverVersion: "v1.0", CreatedAt: m.CreatedAt, UpdatedAt: m.UpdatedAt,
		})
		res.Evidence = append(res.Evidence, EvidenceRef{
			EvidenceID: "ev_manual_" + m.ID, SourceType: string(SourceUserManual),
			SourceName: "案件人工标注", Observation: m.Reason,
			CollectedAt: m.CreatedAt, Confidence: m.Confidence,
		})
	}
	res.Evidence = append(res.Evidence, r.store.GetEvidences(evidenceIDsOf(res.Labels))...)
	res.Evidence = uniqueEvidence(res.Evidence)
	res.Confidence = resolutionConfidence(res.Labels)
	res.ConfidenceTier = string(TierFor(res.Confidence))
	res.Conflicts = r.store.ListConflicts(address)
	return res, nil
}

// Stats 返回实体智能指标（设计 §71）。
func (r *Resolver) Stats() map[string]any {
	entities := r.store.ListEntities()
	clusters := r.store.ListClusters()
	addressCount, evidenceCount, leadCount := 0, 0, 0
	_ = filepath.WalkDir(filepath.Join(r.store.root, "addresses"), func(_ string, d os.DirEntry, err error) error {
		if err == nil && !d.IsDir() && strings.HasSuffix(d.Name(), ".json") {
			addressCount++
		}
		return nil
	})
	_ = filepath.WalkDir(filepath.Join(r.store.root, "evidence"), func(_ string, d os.DirEntry, err error) error {
		if err == nil && !d.IsDir() && strings.HasSuffix(d.Name(), ".json") {
			evidenceCount++
		}
		return nil
	})
	_ = filepath.WalkDir(filepath.Join(r.store.root, "leads"), func(_ string, d os.DirEntry, err error) error {
		if err == nil && !d.IsDir() && strings.HasSuffix(d.Name(), ".json") {
			leadCount++
		}
		return nil
	})
	total := r.cacheHits + r.cacheMisses
	hitRate := 0.0
	if total > 0 {
		hitRate = float64(r.cacheHits) / float64(total)
	}
	return map[string]any{
		"entities": len(entities), "clusters": len(clusters),
		"addresses": addressCount, "evidence": evidenceCount, "leads": leadCount,
		"cache_hits": r.cacheHits, "cache_misses": r.cacheMisses,
		"cache_hit_rate": hitRate, "known_label_hits": r.knownHits,
	}
}

// ResolveBatch 批量解析（设计 §54：10K/100K，不逐地址 HTTP）。
func (r *Resolver) ResolveBatch(ctx context.Context, chainKey string, addresses []string, investigationID string, limit int) []*Resolution {
	if limit <= 0 || limit > 10000 {
		limit = 10000
	}
	if len(addresses) > limit {
		addresses = addresses[:limit]
	}
	out := make([]*Resolution, 0, len(addresses))
	for _, addr := range addresses {
		res, err := r.Resolve(ctx, chainKey, addr, investigationID)
		if err != nil {
			res = &Resolution{Address: strings.ToLower(strings.TrimSpace(addr)), ChainKey: chainKey,
				Confidence: 0, ConfidenceTier: string(TierUnverified), ResolvedAt: r.now().UTC()}
		}
		out = append(out, res)
	}
	return out
}

// AddManualLabel 添加案件自定义标签（设计 §45-§46 Case F）。
func (r *Resolver) AddManualLabel(investigationID, chainKey, address, label, reason string) (*ManualLabel, error) {
	if strings.TrimSpace(investigationID) == "" {
		investigationID = "default"
	}
	address = strings.ToLower(strings.TrimSpace(address))
	label = strings.TrimSpace(label)
	if label == "" || len(label) > 40 {
		return nil, fmt.Errorf("标签不能为空且不超过 40 字符")
	}
	now := r.now().UTC()
	m := &ManualLabel{
		ID: uuid.NewString(), InvestigationID: investigationID, ChainKey: chainKey,
		Address: address, Label: label, Reason: reason,
		Source: SourceUserManual, Confidence: 0.95, CreatedAt: now, UpdatedAt: now,
	}
	if err := r.store.SaveManualLabel(m); err != nil {
		return nil, err
	}
	return m, nil
}

// ImportLabelEntry 是外部标签数据集导入条目（§51、§64-2）。
type ImportLabelEntry struct {
	ChainID    int64       `json:"chain_id"`
	Address    string      `json:"address"`
	Label      string      `json:"label"`
	EntityName string      `json:"entity_name"`
	EntityType EntityType  `json:"entity_type"`
	Source     LabelSource `json:"source"`
	SourceName string      `json:"source_name"`
	Confidence float64     `json:"confidence"`
	Observation string     `json:"observation,omitempty"`
}

// ImportLabels 批量导入外部标签（写实体/证据/地址条目，scope=GLOBAL）。
func (r *Resolver) ImportLabels(entries []ImportLabelEntry) (int, error) {
	imported := 0
	for i, e := range entries {
		addr := strings.ToLower(strings.TrimSpace(e.Address))
		if len(addr) != 42 || !strings.HasPrefix(addr, "0x") || e.Label == "" || e.EntityName == "" {
			continue
		}
		conf := e.Confidence
		if conf <= 0 {
			conf = 0.6
		}
		if conf > 1 {
			conf = 1
		}
		now := r.now().UTC()
		evID := fmt.Sprintf("ev_import_%d_%s", i, addr[2:10])
		entityID := fmt.Sprintf("entity_import_%d_%s", i, addr[2:10])
		ev := &EvidenceRef{
			EvidenceID: evID, SourceType: string(e.Source),
			SourceName: e.SourceName, Observation: e.Observation,
			CollectedAt: now, Confidence: conf,
		}
		if ev.SourceName == "" {
			ev.SourceName = "外部标签数据集"
		}
		if err := r.store.SaveEvidence(ev); err != nil {
			return imported, err
		}
		entity := &Entity{
			ID: entityID, Name: e.EntityName, EntityType: e.EntityType,
			ChainIDs: []int64{e.ChainID}, Addresses: []string{addr},
			Confidence: conf, EvidenceIDs: []string{evID}, Source: e.Source,
			Version: 1, CreatedAt: now, UpdatedAt: now,
		}
		if err := r.store.SaveEntity(entity); err != nil {
			return imported, err
		}
		entry := r.store.GetAddressEntry(e.ChainID, addr)
		if entry == nil {
			entry = &AddressIntelligenceEntry{ChainID: e.ChainID, Address: addr}
		}
		entry.Labels = upsertLabel(entry.Labels, AddressLabel{
			ChainID: e.ChainID, Address: addr, Label: e.Label,
			EntityID: entityID, Scope: ScopeGlobal, Confidence: conf,
			EvidenceIDs: []string{evID}, ResolverVersion: "v1.1",
			CreatedAt: now, UpdatedAt: now,
		})
		entry.EntityIDs = appendUnique(entry.EntityIDs, entityID)
		if err := r.store.SaveAddressEntry(e.ChainID, "", addr, entry); err != nil {
			return imported, err
		}
		imported++
	}
	return imported, nil
}

// Clusters 返回全部聚类。
func (r *Resolver) Clusters() []*AddressCluster {
	return r.store.ListClusters()
}

// SearchEntities 按名称/ID 搜索实体（设计 §29 搜索增强）。
func (r *Resolver) SearchEntities(q string) []*Entity {
	q = strings.ToLower(strings.TrimSpace(q))
	if q == "" {
		return nil
	}
	var out []*Entity
	for _, e := range r.store.ListEntities() {
		if strings.Contains(strings.ToLower(e.Name), q) || strings.Contains(strings.ToLower(e.ID), q) {
			out = append(out, e)
		}
		if len(out) >= 20 {
			break
		}
	}
	return out
}

// LabelHistory 返回地址标签历史版本（设计 §49-§50）。
func (r *Resolver) LabelHistory(chainID int64, address string) []LabelHistoryItem {
	return r.store.LabelHistory(chainID, address)
}

// MergeCrossChainEntities 合并跨链同名实体（设计 P2：Cross-chain Entity Mapping）。
// 保留首个实体为主实体，其余实体保留为别名（AddressLabel 仍可解析到旧 ID）。
func (r *Resolver) MergeCrossChainEntities() (map[string]any, error) {
	entities := r.store.ListEntities()
	byName := map[string][]*Entity{}
	for _, e := range entities {
		name := strings.ToLower(strings.TrimSpace(e.Name))
		if name == "" {
			continue
		}
		byName[name] = append(byName[name], e)
	}
	merged := 0
	var aliases []string
	for _, list := range byName {
		if len(list) < 2 {
			continue
		}
		first := list[0]
		chains := map[int64]bool{}
		addrs := map[string]bool{}
		for _, e := range list {
			for _, c := range e.ChainIDs {
				chains[c] = true
			}
			for _, a := range e.Addresses {
				addrs[strings.ToLower(strings.TrimSpace(a))] = true
			}
		}
		if len(chains) < 2 {
			continue // 仅合并跨链实体
		}
		chainList := make([]int64, 0, len(chains))
		for c := range chains {
			chainList = append(chainList, c)
		}
		sort.Slice(chainList, func(i, j int) bool { return chainList[i] < chainList[j] })
		addrList := make([]string, 0, len(addrs))
		for a := range addrs {
			addrList = append(addrList, a)
		}
		sort.Strings(addrList)
		first.ChainIDs = chainList
		first.Addresses = addrList
		first.UpdatedAt = r.now().UTC()
		if err := r.store.SaveEntity(first); err != nil {
			return nil, err
		}
		for _, e := range list[1:] {
			aliases = append(aliases, e.ID)
		}
		merged++
	}
	return map[string]any{"merged": merged, "aliases": aliases}, nil
}

// EntityGraph 返回实体图（实体地址 + 聚类成员 + 外部实体关联）。
func (r *Resolver) EntityGraph(ctx context.Context, chainKey, entityID string) (map[string]any, error) {
	entity := r.store.GetEntity(entityID)
	if entity == nil {
		return nil, fmt.Errorf("实体不存在: %s", entityID)
	}
	addresses := make([]map[string]any, 0, len(entity.Addresses))
	clusterIDs := []string{}
	for _, addr := range entity.Addresses {
		entry := r.store.GetAddressEntry(firstInt(entity.ChainIDs), addr)
		item := map[string]any{"address": addr, "labels": entryLabels(entry)}
		if entry != nil {
			clusterIDs = append(clusterIDs, entry.ClusterIDs...)
		}
		res, err := r.Resolve(ctx, chainKey, addr, "")
		if err == nil {
			item["resolution"] = res
		}
		addresses = append(addresses, item)
	}
	return map[string]any{
		"entity": entity, "addresses": addresses,
		"clusters": clusterIDs, "evidence": r.store.GetEvidences(entity.EvidenceIDs),
	}, nil
}

// Leads 返回调查的实体线索（设计 §56）。
func (r *Resolver) Leads(investigationID string) []*InvestigationLead {
	if strings.TrimSpace(investigationID) == "" {
		investigationID = "default"
	}
	return r.store.ListLeads(investigationID)
}

func (r *Resolver) applyKnown(entry *AddressIntelligenceEntry, seed KnownSeed, res *Resolution) {
	evID := "ev_known_" + strings.ToLower(seed.Address)
	entityID := "entity_" + sanitizeID(seed.EntityName)
	now := r.now().UTC()
	entry.Labels = upsertLabel(entry.Labels, AddressLabel{
		ChainID: seed.ChainID, Address: strings.ToLower(seed.Address), Label: seed.Label,
		EntityID: entityID, Scope: ScopeGlobal, Confidence: seed.Confidence,
		EvidenceIDs: []string{evID}, ResolverVersion: "v1.0", CreatedAt: now, UpdatedAt: now,
	})
	entry.EntityIDs = appendUnique(entry.EntityIDs, entityID)
	res.Labels = append(res.Labels, entry.Labels[len(entry.Labels)-1])
	res.Entity = &Entity{ID: entityID, Name: seed.EntityName, EntityType: seed.EntityType,
		ChainIDs: []int64{seed.ChainID}, Addresses: []string{strings.ToLower(seed.Address)},
		Confidence: seed.Confidence, EvidenceIDs: []string{evID}, Source: seed.Source}
}

func (r *Resolver) applyContract(entry *AddressIntelligenceEntry, res *Resolution) {
	now := r.now().UTC()
	label := "contract"
	entityType := EntityContract
	entityName := "链上合约"
	conf := 0.85
	if entry.Feature != nil && entry.Feature.TokenDiversity > 0 {
		label = "token_contract"
		entityType = EntityTokenContract
		entityName = "Token 合约"
		conf = 0.90
	}
	entityID := "entity_" + sanitizeID(entityName) + "_" + entry.Address[2:8]
	evID := "ev_contract_" + entry.Address
	ev := &EvidenceRef{
		EvidenceID: evID, SourceType: string(SourceContractMetadata),
		SourceName: "链上合约判定", Observation: "Profile 合约事件计数>0，判定为合约地址。",
		CollectedAt: now, Confidence: conf,
	}
	_ = r.store.SaveEvidence(ev)
	entry.Labels = upsertLabel(entry.Labels, AddressLabel{
		ChainID: entry.ChainID, Address: entry.Address, Label: label,
		EntityID: entityID, Scope: ScopeGlobal, Confidence: conf,
		EvidenceIDs: []string{evID}, ResolverVersion: "v1.0", CreatedAt: now, UpdatedAt: now,
	})
	entry.EntityIDs = appendUnique(entry.EntityIDs, entityID)
	res.Labels = append(res.Labels, entry.Labels[len(entry.Labels)-1])
	res.Entity = &Entity{ID: entityID, Name: entityName, EntityType: entityType,
		ChainIDs: []int64{entry.ChainID}, Addresses: []string{entry.Address},
		Confidence: conf, EvidenceIDs: []string{evID}, Source: SourceContractMetadata}
}

func (r *Resolver) applyPatterns(ctx context.Context, entry *AddressIntelligenceEntry, res *Resolution, chainKey, investigationID string) {
	f := entry.Feature
	if f == nil {
		return
	}
	now := r.now().UTC()
	if f.TxCount == 0 {
		return
	}
	dest, token, amount, _, _ := SweepDestination(ctx, r.src, entry.Address)
	// Deposit / Collector / Settlement（行为推断，不绑定具体服务名）
	if f.SweepRatio >= 0.6 && dest != "" && f.CounterpartyCount > 0 {
		label := "collector_settlement_candidate"
		entityType := EntityUnknownService
		entityName := "归集/结算候选"
		conf := 0.6
		if f.Recent30d > 0 && f.SweepRatio >= 0.8 {
			label = "exchange_deposit_candidate"
			entityType = EntityCEXDeposit
			entityName = "交易所入金候选"
			conf = 0.65
		}
		// Sweep 目标命中已知实体 → 可绑定实体名称（HIGH/MEDIUM）
		knownDest := ""
		if seed, ok := r.known[dest]; ok {
			knownDest = seed.EntityName
			label = "cex_deposit"
			entityType = EntityCEXDeposit
			entityName = seed.EntityName + " 入金候选"
			conf = 0.85
		}
		evID := "ev_pattern_" + entry.Address + "_" + dest
		obs := fmt.Sprintf("链上模式：稳定归集至 %s（Top1 去向占比 %.0f%%）", dest, f.SweepRatio*100)
		if knownDest != "" {
			obs += "；归集目标命中公开标签实体：" + knownDest
		}
		_ = r.store.SaveEvidence(&EvidenceRef{
			EvidenceID: evID, SourceType: string(SourceOnchainPattern),
			SourceName: "链上行为模式", Observation: obs, CollectedAt: now, Confidence: conf,
		})
		entityID := "entity_" + sanitizeID(entityName) + "_" + entry.Address[2:8]
		_ = r.store.SaveEntity(&Entity{
			ID: entityID, Name: entityName, EntityType: entityType,
			ChainIDs: []int64{entry.ChainID}, Addresses: []string{entry.Address},
			Confidence: conf, EvidenceIDs: []string{evID}, Source: SourceOnchainPattern,
			Version: 1, CreatedAt: now, UpdatedAt: now,
		})
		entry.Labels = upsertLabel(entry.Labels, AddressLabel{
			ChainID: entry.ChainID, Address: entry.Address, Label: label,
			EntityID: entityID, Scope: ScopeGlobal, Confidence: conf,
			EvidenceIDs: []string{evID}, ResolverVersion: "v1.0", CreatedAt: now, UpdatedAt: now,
		})
		entry.EntityIDs = appendUnique(entry.EntityIDs, entityID)
		res.Labels = append(res.Labels, entry.Labels[len(entry.Labels)-1])
		res.Entity = &Entity{ID: entityID, Name: entityName, EntityType: entityType,
			ChainIDs: []int64{entry.ChainID}, Addresses: []string{entry.Address},
			Confidence: conf, EvidenceIDs: []string{evID}, Source: SourceOnchainPattern}
		// Cashout Candidate / Investigation Lead（Case B）
		if knownDest != "" {
			lead := &InvestigationLead{
				ID: uuid.NewString(), InvestigationID: investigationID, Address: entry.Address,
				EntityID: entityID, EntityName: entityName, LeadType: "EXCHANGE_DEPOSIT",
				TransactionHash: "", BlockNumber: 0, Timestamp: now,
				Token: token, Amount: amount, EvidenceIDs: []string{evID}, Confidence: conf,
			}
			if investigationID != "" {
				_ = r.store.SaveLead(lead)
			}
		}
	}
	// Hot Wallet 候选（高频双向）
	if f.Recent24h > 0 && f.CounterpartyCount >= 10 && f.SweepRatio < 0.6 {
		evID := "ev_hotwallet_" + entry.Address
		_ = r.store.SaveEvidence(&EvidenceRef{
			EvidenceID: evID, SourceType: string(SourceOnchainPattern),
			SourceName: "链上行为模式", Observation: "高频双向交互、对手方多、24h 活跃。",
			CollectedAt: now, Confidence: 0.5,
		})
		entityID := "entity_hot_wallet_candidate_" + entry.Address[2:8]
		_ = r.store.SaveEntity(&Entity{
			ID: entityID, Name: "热钱包候选", EntityType: EntityCEXHotWallet,
			ChainIDs: []int64{entry.ChainID}, Addresses: []string{entry.Address},
			Confidence: 0.5, EvidenceIDs: []string{evID}, Source: SourceOnchainPattern,
			Version: 1, CreatedAt: now, UpdatedAt: now,
		})
		entry.Labels = upsertLabel(entry.Labels, AddressLabel{
			ChainID: entry.ChainID, Address: entry.Address, Label: "hot_wallet_candidate",
			EntityID: entityID, Scope: ScopeGlobal, Confidence: 0.5,
			EvidenceIDs: []string{evID}, ResolverVersion: "v1.0", CreatedAt: now, UpdatedAt: now,
		})
	}
	// 沉淀候选（Dormancy）
	if f.DormancyScore >= 0.5 {
		evID := "ev_dormancy_" + entry.Address
		_ = r.store.SaveEvidence(&EvidenceRef{
			EvidenceID: evID, SourceType: string(SourceOnchainPattern),
			SourceName: "沉淀模式", Observation: fmt.Sprintf("沉淀分数 %.2f：净留存+长期未出金+近期不活跃。", f.DormancyScore),
			CollectedAt: now, Confidence: 0.6,
		})
		entityID := "entity_settlement_candidate_" + entry.Address[2:8]
		_ = r.store.SaveEntity(&Entity{
			ID: entityID, Name: "资金沉淀候选", EntityType: EntityUnknownService,
			ChainIDs: []int64{entry.ChainID}, Addresses: []string{entry.Address},
			Confidence: 0.6, EvidenceIDs: []string{evID}, Source: SourceOnchainPattern,
			Version: 1, CreatedAt: now, UpdatedAt: now,
		})
		entry.Labels = upsertLabel(entry.Labels, AddressLabel{
			ChainID: entry.ChainID, Address: entry.Address, Label: "settlement_candidate",
			EntityID: entityID, Scope: ScopeGlobal, Confidence: 0.6,
			EvidenceIDs: []string{evID}, ResolverVersion: "v1.0", CreatedAt: now, UpdatedAt: now,
		})
	}
}

func (r *Resolver) applyCluster(ctx context.Context, entry *AddressIntelligenceEntry, res *Resolution, chainKey string) {
	if entry.Feature == nil || entry.Feature.SweepRatio < 0.6 {
		return
	}
	dest, _, _, _, _ := SweepDestination(ctx, r.src, entry.Address)
	if dest == "" {
		return
	}
	clusterID := "cluster_sweep_" + dest[2:10]
	cluster := r.store.GetCluster(clusterID)
	now := r.now().UTC()
	if cluster == nil {
		cluster = &AddressCluster{
			ID: clusterID, ClusterType: "COMMON_SWEEP",
			Confidence: 0.6, FalsePositiveRisk: 0.3, MinEvidenceCount: 1,
			UpdatedAt: now,
		}
	}
	cluster.Addresses = appendUnique(cluster.Addresses, entry.Address)
	if len(cluster.Addresses) >= 2 {
		cluster.Confidence = 0.75
		cluster.FalsePositiveRisk = 0.2
	}
	_ = r.store.SaveCluster(cluster)
	entry.ClusterIDs = appendUnique(entry.ClusterIDs, cluster.ID)
	res.ClusterIDs = appendUnique(res.ClusterIDs, cluster.ID)
}

func (r *Resolver) applyConflicts(entry *AddressIntelligenceEntry, res *Resolution) {
	seen := map[string]string{}
	for _, l := range entry.Labels {
		if l.EntityID == "" {
			continue
		}
		if prev, ok := seen[l.Label]; ok && prev != l.EntityID && l.Confidence >= 0.6 {
			_ = r.store.SaveConflict(&ConflictEntry{
				ID: "conflict_" + entry.Address[2:10] + "_" + l.Label,
				Address: entry.Address, SourceA: prev, SourceB: l.EntityID,
				EntityA: prev, EntityB: l.EntityID, CreatedAt: r.now().UTC(),
			})
		}
		seen[l.Label] = l.EntityID
	}
}

func (r *Resolver) fromEntry(e *AddressIntelligenceEntry) *Resolution {
	res := &Resolution{
		Address: e.Address, ChainKey: e.ChainKey, ChainID: e.ChainID,
		Labels: e.Labels, ClusterIDs: e.ClusterIDs, Feature: e.Feature,
		ResolvedAt: r.now().UTC(),
	}
	if len(e.EntityIDs) > 0 {
		// 选择置信度最高的全局标签对应实体
		best := ""
		bestConf := -1.0
		for _, l := range e.Labels {
			if l.Scope != ScopeGlobal || l.EntityID == "" {
				continue
			}
			if l.Confidence > bestConf {
				bestConf = l.Confidence
				best = l.EntityID
			}
		}
		if best != "" {
			ent := r.store.GetEntity(best)
			if ent == nil {
				// 兼容旧数据：模式实体未落盘时按标签回填占位实体（不改变置信度来源）
				ent = &Entity{
					ID: best, Name: best, EntityType: EntityUnknownService,
					ChainIDs: []int64{e.ChainID}, Addresses: []string{e.Address},
					Confidence: bestConf, Source: SourceOnchainPattern, Version: 1,
					CreatedAt: e.UpdatedAt, UpdatedAt: e.UpdatedAt,
				}
				_ = r.store.SaveEntity(ent)
			}
			res.Entity = ent
		}
	}
	for _, l := range e.Labels {
		res.Evidence = append(res.Evidence, r.store.GetEvidences(l.EvidenceIDs)...)
	}
	res.Evidence = uniqueEvidence(res.Evidence)
	res.Confidence = resolutionConfidence(e.Labels)
	res.ConfidenceTier = string(TierFor(res.Confidence))
	res.Conflicts = r.store.ListConflicts(e.Address)
	return res
}

func resolutionConfidence(labels []AddressLabel) float64 {
	conf := 0.0
	for _, l := range labels {
		if l.Scope == ScopeGlobal && l.Confidence > conf {
			conf = l.Confidence
		}
	}
	return conf
}

func evidenceIDsOf(labels []AddressLabel) []string {
	var out []string
	for _, l := range labels {
		out = append(out, l.EvidenceIDs...)
	}
	return out
}

func uniqueEvidence(in []EvidenceRef) []EvidenceRef {
	seen := map[string]bool{}
	var out []EvidenceRef
	for _, e := range in {
		if e.EvidenceID == "" || seen[e.EvidenceID] {
			continue
		}
		seen[e.EvidenceID] = true
		out = append(out, e)
	}
	return out
}

func entryLabels(e *AddressIntelligenceEntry) []AddressLabel {
	if e == nil {
		return nil
	}
	return e.Labels
}

func firstInt(v []int64) int64 {
	if len(v) == 0 {
		return 0
	}
	return v[0]
}
