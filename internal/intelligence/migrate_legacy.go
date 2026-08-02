package intelligence

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"

	"github.com/etl/backend/internal/investigationstore"
)

// ── Legacy 数据迁移（V1 Storage Layer 迁移）──
//
// 将旧目录布局（investigation_requests/、investigation_evidence/、
// investigation_memory/knowledge.json）一次性迁移到新布局
// backend/data/investigation/{requests,evidence,memory,indexes}/。
// 幂等：目标文件已存在则跳过；迁移成功不删除旧文件（保留备份）。

// MigrateLegacyInvestigationData 迁移旧数据目录到新布局。
// dataRoot 为 backend/data 目录；返回迁移的文件数。
func MigrateLegacyInvestigationData(dataRoot string) (int, error) {
	invRoot := filepath.Join(dataRoot, "investigation")
	migrated := 0

	// 1. 调查请求：investigation_requests/req-N.json（raw JSON）→ investigation/requests/（envelope）
	n, err := migrateLegacyRequests(filepath.Join(dataRoot, "investigation_requests"), filepath.Join(invRoot, "requests"))
	if err != nil {
		return migrated, err
	}
	migrated += n

	// 2. 调查证据：investigation_evidence/{inv}.json（数组）→ investigation/evidence/{inv}/{ev}.json（单条）
	n, err = migrateLegacyEvidence(filepath.Join(dataRoot, "investigation_evidence"), filepath.Join(invRoot, "evidence"))
	if err != nil {
		return migrated, err
	}
	migrated += n

	// 3. 知识记忆：investigation_memory/knowledge.json（数组）→ investigation/memory/{address,entity,case}/（分目录）
	n, err = migrateLegacyMemory(filepath.Join(dataRoot, "investigation_memory", "knowledge.json"), filepath.Join(invRoot, "memory"))
	if err != nil {
		return migrated, err
	}
	migrated += n

	return migrated, nil
}

// migrateLegacyRequests 迁移请求（raw JSON → envelope，保留 ID）。
func migrateLegacyRequests(srcDir, dstDir string) (int, error) {
	entries, err := os.ReadDir(srcDir)
	if err != nil {
		if os.IsNotExist(err) {
			return 0, nil
		}
		return 0, err
	}
	store := investigationstore.NewJSONStore[InvestigationRequest](dstDir)
	migrated := 0
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".json" {
			continue
		}
		data, err := os.ReadFile(filepath.Join(srcDir, entry.Name()))
		if err != nil {
			continue
		}
		var req InvestigationRequest
		if err := json.Unmarshal(data, &req); err != nil || req.ID == "" || !validRequestID(req.ID) {
			continue
		}
		// 安全：要求内容 ID 与文件名一致（磁盘篡改/文件名≠ID 的旧文件跳过），
		// 幂等检查统一用内容 ID，避免文件名与 ID 不一致时覆盖新布局已存在的记录
		if req.ID != strings.TrimSuffix(entry.Name(), ".json") {
			continue
		}
		if store.Exists(req.ID) {
			continue // 目标已存在则跳过（幂等）
		}
		if req.ExpectedResult == nil {
			req.ExpectedResult = []string{}
		}
		if req.Status == "" {
			req.Status = RequestCreated
		}
		if err := store.Save(req.ID, req); err != nil {
			return migrated, err
		}
		migrated++
	}
	return migrated, nil
}

// migrateLegacyEvidence 迁移证据（数组文件 → 单条文件，保留 ID 与索引）。
func migrateLegacyEvidence(srcDir, dstDir string) (int, error) {
	entries, err := os.ReadDir(srcDir)
	if err != nil {
		if os.IsNotExist(err) {
			return 0, nil
		}
		return 0, err
	}
	store := investigationstore.NewJSONStore[Evidence](dstDir)
	indexPath := filepath.Join(filepath.Dir(dstDir), "indexes", "evidence-index.json")
	index := investigationstore.NewIndex(indexPath)
	migrated := 0
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".json" {
			continue
		}
		invID := strings.TrimSuffix(entry.Name(), ".json")
		data, err := os.ReadFile(filepath.Join(srcDir, entry.Name()))
		if err != nil {
			continue
		}
		var evs []Evidence
		if err := json.Unmarshal(data, &evs); err != nil {
			continue
		}
		for _, ev := range evs {
			if ev.ID == "" {
				continue
			}
			key := invID + "/" + ev.ID
			if store.Exists(key) {
				continue // 幂等
			}
			ev.InvestigationID = invID
			if err := store.Save(key, ev); err != nil {
				return migrated, err
			}
			if ev.Address != "" {
				_ = index.Add(ev.Address, ev.ID)
			}
			migrated++
		}
	}
	return migrated, nil
}

// migrateLegacyMemory 迁移知识记忆（knowledge.json 数组 → address/entity/case 分目录）。
func migrateLegacyMemory(knowledgePath, dstDir string) (int, error) {
	data, err := os.ReadFile(knowledgePath)
	if err != nil {
		if os.IsNotExist(err) {
			return 0, nil
		}
		return 0, err
	}
	var rels []MemoryRelation
	if err := json.Unmarshal(data, &rels); err != nil {
		return 0, nil
	}
	if len(rels) == 0 {
		return 0, nil
	}
	// 复用新 store：Record 幂等去重 + 分目录落盘
	store := NewInvestigationMemoryStore(dstDir)
	// 幂等 key 与 Record 内部规范化一致（lower + trim），避免大小写不一致时计数偏高
	normKey := func(t MemoryRelationType, from, to string) string {
		return string(t) + "|" + strings.ToLower(strings.TrimSpace(from)) + "|" + strings.ToLower(strings.TrimSpace(to))
	}
	existing := map[string]bool{}
	for _, r := range store.All() {
		existing[normKey(r.Type, r.From, r.To)] = true
	}
	migrated := 0
	for _, rel := range rels {
		if rel.ID == "" {
			continue
		}
		if existing[normKey(rel.Type, rel.From, rel.To)] {
			continue // 幂等：已存在（type+from+to）则跳过，不计入迁移数
		}
		if err := store.Record(rel); err != nil {
			return migrated, err
		}
		existing[normKey(rel.Type, rel.From, rel.To)] = true
		migrated++
	}
	return migrated, nil
}
