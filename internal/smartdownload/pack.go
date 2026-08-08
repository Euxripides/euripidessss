package smartdownload

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
)

// BatchPack 大批量创建的打包存储：一个批次一个 JSON，避免数万个小文件拖慢创建。
// 运行期单个 Job 变化时仍写独立文件；加载时独立文件覆盖 Pack 条目。
type BatchPack struct {
	Batch     *BatchJob     `json:"batch"`
	Addresses []*AddressJob `json:"addresses"`
	Datasets  []*DatasetJob `json:"datasets"`
	Ranges    []*RangeJob   `json:"ranges"`
}

// packThreshold 超过该 Job 总数改用 Pack 创建（10K 地址验收：30K jobs → Pack）。
const packThreshold = 2000

// usePack 判断是否走 Pack 快速创建。
func usePack(addresses, datasets, ranges int) bool {
	return addresses+datasets+ranges > packThreshold
}

func (s *Store) SaveBatchPack(pack *BatchPack) error {
	if pack == nil || pack.Batch == nil {
		return nil
	}
	payload, err := json.MarshalIndent(pack, "", "  ")
	if err != nil {
		return err
	}
	dir := filepath.Join(s.root, "packs")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	path := filepath.Join(dir, pack.Batch.ID+".json")
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, payload, 0o644); err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, v := range pack.Addresses {
		if v != nil {
			s.addresses[v.ID] = cloneJSON(v).(*AddressJob)
		}
	}
	for _, v := range pack.Datasets {
		if v != nil {
			s.datasets[v.ID] = cloneJSON(v).(*DatasetJob)
		}
	}
	for _, v := range pack.Ranges {
		if v != nil {
			s.ranges[v.ID] = cloneJSON(v).(*RangeJob)
		}
	}
	s.batches[pack.Batch.ID] = cloneJSON(pack.Batch).(*BatchJob)
	return nil
}

// loadPacks 扫描 Pack 文件（在独立文件之前加载，独立文件覆盖）。
func (s *Store) loadPacks() error {
	dir := filepath.Join(s.root, "packs")
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		payload, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			continue
		}
		var pack BatchPack
		if json.Unmarshal(payload, &pack) != nil || pack.Batch == nil {
			continue
		}
		s.mu.Lock()
		s.batches[pack.Batch.ID] = cloneJSON(pack.Batch).(*BatchJob)
		for _, v := range pack.Addresses {
			if v != nil {
				s.addresses[v.ID] = cloneJSON(v).(*AddressJob)
			}
		}
		for _, v := range pack.Datasets {
			if v != nil {
				s.datasets[v.ID] = cloneJSON(v).(*DatasetJob)
			}
		}
		for _, v := range pack.Ranges {
			if v != nil {
				s.ranges[v.ID] = cloneJSON(v).(*RangeJob)
			}
		}
		s.mu.Unlock()
	}
	return nil
}
