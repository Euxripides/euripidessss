package smartdownload

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/etl/backend/internal/chain"
	"github.com/google/uuid"
)

const (
	sharedWorkPending   = "PENDING"
	sharedWorkReady     = "READY"
	sharedWorkRunning   = "RUNNING"
	sharedWorkCompleted = "COMPLETED"
	sharedWorkFailed    = "FAILED"
	sharedWorkSplit     = "SPLIT"
	sharedWorkCanceled  = "CANCELED"
)

// GroupRangeRequest is the provider-neutral V3.3 multi-address execution unit.
type GroupRangeRequest struct {
	SharedWorkID string       `json:"shared_work_id"`
	Mode         DownloadMode `json:"mode,omitempty"`
	Priority     int          `json:"priority,omitempty"`
	ChainKey     string       `json:"chain_key"`
	ChainID      int64        `json:"chain_id"`
	Addresses    []string     `json:"addresses"`
	Datasets     []string     `json:"datasets"`
	FromBlock    uint64       `json:"from_block"`
	ToBlock      uint64       `json:"to_block"`
}

// GroupProviderAdapter is opt-in. A provider may not claim grouping or bundle
// savings unless it implements this interface and explicitly advertises them.
type GroupProviderAdapter interface {
	ProviderAdapter
	MaxAddressGroupSize(dataset string) int
	SupportedDatasetBundles() [][]string
	ExecuteGroupRange(context.Context, GroupRangeRequest) (map[string]map[string]*ProviderResult, error)
}

type SharedWorkRef struct {
	BatchID      string `json:"batch_id"`
	AddressJobID string `json:"address_job_id"`
	DatasetJobID string `json:"dataset_job_id"`
	RangeJobID   string `json:"range_job_id"`
	Address      string `json:"address"`
	Dataset      string `json:"dataset"`
	Canceled     bool   `json:"canceled,omitempty"`
	JoinExisting bool   `json:"join_existing,omitempty"`
}

type SharedWork struct {
	ID           string          `json:"id"`
	Fingerprint  string          `json:"fingerprint"`
	ChainKey     string          `json:"chain_key"`
	ChainID      int64           `json:"chain_id"`
	Datasets     []string        `json:"datasets"`
	Addresses    []string        `json:"addresses"`
	FromBlock    uint64          `json:"from_block"`
	ToBlock      uint64          `json:"to_block"`
	Status       string          `json:"status"`
	RefCount     int             `json:"ref_count"`
	Attempts     int             `json:"attempts"`
	Provider     string          `json:"provider,omitempty"`
	OwnerBatchID string          `json:"owner_batch_id,omitempty"`
	Heavy        bool            `json:"heavy,omitempty"`
	Poison       bool            `json:"poison,omitempty"`
	Split        bool            `json:"split,omitempty"`
	ParentID     string          `json:"parent_id,omitempty"`
	Refs         []SharedWorkRef `json:"refs"`
	CreatedAt    time.Time       `json:"created_at"`
	UpdatedAt    time.Time       `json:"updated_at"`
}

type AddressGroup struct {
	GroupID        string   `json:"group_id"`
	ChainKey       string   `json:"chain_key"`
	ChainID        int64    `json:"chain_id"`
	Datasets       []string `json:"datasets"`
	Addresses      []string `json:"addresses"`
	FilterHash     string   `json:"filter_hash"`
	Priority       string   `json:"priority,omitempty"`
	Classification string   `json:"classification,omitempty"`
	Heavy          bool     `json:"heavy,omitempty"`
	WorkloadIDs    []string `json:"workload_ids,omitempty"`
}

type DatasetBundle struct {
	Datasets []string `json:"datasets"`
	Provider string   `json:"provider,omitempty"`
	Bundled  bool     `json:"bundled"`
}

type AcceleratorMetrics struct {
	InputJobs             int     `json:"input_jobs"`
	MergedWorkloads       int     `json:"merged_workloads"`
	ProviderRequestsSaved int     `json:"provider_requests_saved"`
	DuplicateWorkAvoided  int     `json:"duplicate_work_avoided"`
	CoverageHits          int     `json:"coverage_hits"`
	BundleSavings         int     `json:"bundle_savings"`
	HeavyAddressCount     int     `json:"heavy_address_count"`
	SplitCount            int     `json:"split_count"`
	DownloadAmplification float64 `json:"download_amplification"`
	ReductionRatio        float64 `json:"reduction_ratio"`
}

type AcceleratorPlan struct {
	BatchID         string             `json:"batch_id,omitempty"`
	Status          string             `json:"status"`
	Groups          []AddressGroup     `json:"groups"`
	DatasetBundles  []DatasetBundle    `json:"dataset_bundles"`
	SharedWorkloads []*SharedWork      `json:"shared_workloads"`
	Metrics         AcceleratorMetrics `json:"metrics"`
	UpdatedAt       time.Time          `json:"updated_at"`
}

type v33RegistryFile struct {
	Version int                    `json:"version"`
	Works   map[string]*SharedWork `json:"works"`
}

type v33Runtime struct {
	mu    sync.Mutex
	path  string
	works map[string]*SharedWork
}

func newV33Runtime(root string) *v33Runtime {
	r := &v33Runtime{
		path:  filepath.Join(root, "v33", "active_shared_work.json"),
		works: make(map[string]*SharedWork),
	}
	_ = r.Recover()
	return r
}

func (r *v33Runtime) Recover() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	b, err := os.ReadFile(r.path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	var state v33RegistryFile
	if err := json.Unmarshal(b, &state); err != nil {
		return fmt.Errorf("decode V3.3 registry: %w", err)
	}
	if state.Works == nil {
		state.Works = make(map[string]*SharedWork)
	}
	for _, work := range state.Works {
		if work.Status == sharedWorkRunning {
			work.Status = sharedWorkReady
			work.OwnerBatchID = ""
			work.UpdatedAt = time.Now().UTC()
		}
	}
	r.works = state.Works
	return r.persistLocked()
}

func (r *v33Runtime) persistLocked() error {
	if err := os.MkdirAll(filepath.Dir(r.path), 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(r.path), ".active-shared-work-*.tmp")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)
	enc := json.NewEncoder(tmp)
	enc.SetIndent("", "  ")
	if err = enc.Encode(v33RegistryFile{Version: 1, Works: r.works}); err == nil {
		err = tmp.Sync()
	}
	if closeErr := tmp.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	return os.Rename(tmpPath, r.path)
}

func cloneSharedWork(work *SharedWork) *SharedWork {
	if work == nil {
		return nil
	}
	copyWork := *work
	copyWork.Datasets = append([]string(nil), work.Datasets...)
	copyWork.Addresses = append([]string(nil), work.Addresses...)
	copyWork.Refs = append([]SharedWorkRef(nil), work.Refs...)
	return &copyWork
}

func canonicalSharedFingerprint(chainKey string, datasets, addresses []string, from, to uint64) string {
	datasets = sortedUniqueLower(datasets)
	addresses = sortedUniqueLower(addresses)
	canonical := strings.Join([]string{
		strings.ToLower(strings.TrimSpace(chainKey)), strings.Join(datasets, ","),
		strings.Join(addresses, ","), fmt.Sprintf("%d-%d", from, to),
		"schema=2", "parser=smartdownload-v1",
	}, "|")
	sum := sha256.Sum256([]byte(canonical))
	return hex.EncodeToString(sum[:])
}

func sortedUniqueLower(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.ToLower(strings.TrimSpace(value))
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	sort.Strings(out)
	return out
}

type acceleratorRangeRef struct {
	Range   *RangeJob
	Dataset *DatasetJob
	Address *AddressJob
}

func (s *Service) batchRangeRefs(batchID string) []acceleratorRangeRef {
	var refs []acceleratorRangeRef
	for _, address := range s.store.ListAddressesByBatch(batchID) {
		for _, dataset := range s.store.ListDatasetsByAddress(address.ID) {
			for _, rangeJob := range s.store.ListRangesByDataset(dataset.ID) {
				// V3.3 grouped execution is currently implemented by the RPC
				// adapter. Cloud-owned ranges must remain on the single-range
				// scheduler so SubmitJob can deploy/queue the SQD Worker; attaching
				// them to SharedWork would silently replace the selected Cloud lane
				// with RPC.
				if (rangeJob.Status == RangePending || rangeJob.Status == RangeReady) && rangeJob.Owner != RangeOwnerCloud {
					refs = append(refs, acceleratorRangeRef{Range: rangeJob, Dataset: dataset, Address: address})
				}
			}
		}
	}
	return refs
}

func datasetsMatchBundle(datasets []string, bundle []string) bool {
	a, b := sortedUniqueLower(datasets), sortedUniqueLower(bundle)
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func (s *Service) groupAdapterFor(datasets []string, chainKey string, mode DownloadMode) (GroupProviderAdapter, int, bool) {
	for _, adapter := range s.adapters {
		group, ok := adapter.(GroupProviderAdapter)
		if !ok || !adapterAvailableForMode(group, chainKey, mode) {
			continue
		}
		bundleOK := len(datasets) == 1 && group.Supports(datasets[0])
		if len(datasets) > 1 {
			bundleOK = false
			for _, bundle := range group.SupportedDatasetBundles() {
				if datasetsMatchBundle(datasets, bundle) {
					bundleOK = true
					break
				}
			}
		}
		if !bundleOK {
			continue
		}
		maxGroup := 100
		for _, dataset := range datasets {
			if size := group.MaxAddressGroupSize(dataset); size > 0 && size < maxGroup {
				maxGroup = size
			}
		}
		if maxGroup < 1 {
			maxGroup = 1
		}
		return group, maxGroup, true
	}
	return nil, 1, false
}

func (s *Service) acceleratorBundles(datasets []string, chainKey string, mode DownloadMode) [][]string {
	remaining := make(map[string]bool)
	for _, dataset := range sortedUniqueLower(datasets) {
		remaining[dataset] = true
	}
	var bundles [][]string
	for _, adapter := range s.adapters {
		group, ok := adapter.(GroupProviderAdapter)
		if !ok || !adapterAvailableForMode(group, chainKey, mode) {
			continue
		}
		for _, candidate := range group.SupportedDatasetBundles() {
			candidate = sortedUniqueLower(candidate)
			if len(candidate) < 2 {
				continue
			}
			all := true
			for _, dataset := range candidate {
				all = all && remaining[dataset]
			}
			if all {
				bundles = append(bundles, candidate)
				for _, dataset := range candidate {
					delete(remaining, dataset)
				}
			}
		}
	}
	keys := make([]string, 0, len(remaining))
	for dataset := range remaining {
		keys = append(keys, dataset)
	}
	sort.Strings(keys)
	for _, dataset := range keys {
		bundles = append(bundles, []string{dataset})
	}
	return bundles
}

func (s *Service) attachBatchAccelerator(batchID string) error {
	if s.v33 == nil {
		return nil
	}
	batch := s.store.GetBatch(batchID)
	if batch == nil {
		return nil
	}
	// Providers must opt in to true multi-address execution. Keeping the legacy
	// Range path untouched for ordinary adapters preserves failover semantics and
	// avoids an O(addresses*jobs) registry walk for large non-group batches.
	hasGroupProvider := false
	for _, adapter := range s.adapters {
		if group, ok := adapter.(GroupProviderAdapter); ok && adapterAvailableForMode(group, batch.ChainKey, batch.Mode) {
			hasGroupProvider = true
			break
		}
	}
	if !hasGroupProvider {
		return nil
	}
	refs := s.batchRangeRefs(batchID)
	if len(refs) == 0 {
		return nil
	}
	byRange := make(map[string][]acceleratorRangeRef)
	for _, ref := range refs {
		key := fmt.Sprintf("%s|%d|%d", ref.Address.ChainKey, ref.Range.FromBlock, ref.Range.ToBlock)
		byRange[key] = append(byRange[key], ref)
	}

	type attachMutation struct {
		work *SharedWork
		refs []acceleratorRangeRef
	}
	var mutations []attachMutation
	for _, rangeRefs := range byRange {
		datasetSet := make(map[string]bool)
		for _, ref := range rangeRefs {
			datasetSet[ref.Dataset.Dataset] = true
		}
		var datasets []string
		for dataset := range datasetSet {
			datasets = append(datasets, dataset)
		}
		first := rangeRefs[0]
		for _, bundle := range s.acceleratorBundles(datasets, first.Address.ChainKey, batch.Mode) {
			_, maxGroup, grouped := s.groupAdapterFor(bundle, first.Address.ChainKey, batch.Mode)
			if !grouped {
				// No provider capability means no SharedWork ownership. These refs
				// remain on the proven single-address scheduler (group_size=1).
				continue
			}
			var bundleRefs []acceleratorRangeRef
			for _, ref := range rangeRefs {
				if containsString(bundle, ref.Dataset.Dataset) {
					bundleRefs = append(bundleRefs, ref)
				}
			}
			addressMap := make(map[string][]acceleratorRangeRef)
			for _, ref := range bundleRefs {
				addressMap[ref.Address.Address] = append(addressMap[ref.Address.Address], ref)
			}
			addresses := make([]string, 0, len(addressMap))
			for address := range addressMap {
				addresses = append(addresses, address)
			}
			sort.Strings(addresses)
			for start := 0; start < len(addresses); start += maxGroup {
				end := min(start+maxGroup, len(addresses))
				groupAddresses := addresses[start:end]
				var groupRefs []acceleratorRangeRef
				for _, address := range groupAddresses {
					groupRefs = append(groupRefs, addressMap[address]...)
				}
				first := groupRefs[0]
				fingerprint := canonicalSharedFingerprint(first.Address.ChainKey, bundle, groupAddresses, first.Range.FromBlock, first.Range.ToBlock)
				work := &SharedWork{
					ID: uuid.NewString(), Fingerprint: fingerprint, ChainKey: first.Address.ChainKey,
					ChainID: first.Address.ChainID, Datasets: sortedUniqueLower(bundle),
					Addresses: sortedUniqueLower(groupAddresses), FromBlock: first.Range.FromBlock,
					ToBlock: first.Range.ToBlock, Status: sharedWorkPending,
					Heavy:     len(groupAddresses) == 1 && first.Dataset.EstimatedRows > s.opts.TargetRowsPerShard,
					CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC(),
				}
				for _, ref := range groupRefs {
					work.Refs = append(work.Refs, SharedWorkRef{
						BatchID: batchID, AddressJobID: ref.Address.ID, DatasetJobID: ref.Dataset.ID,
						RangeJobID: ref.Range.ID, Address: ref.Address.Address, Dataset: ref.Dataset.Dataset,
					})
				}
				mutations = append(mutations, attachMutation{work: work, refs: groupRefs})
			}
		}
	}

	s.v33.mu.Lock()
	defer s.v33.mu.Unlock()
	before := make(map[string]*SharedWork, len(s.v33.works))
	for id, work := range s.v33.works {
		before[id] = cloneSharedWork(work)
	}
	oldRangeIDs := make(map[string]string)
	for _, mutation := range mutations {
		work := mutation.work
		for _, existing := range s.v33.works {
			if existing.Fingerprint == work.Fingerprint &&
				(existing.Status == sharedWorkPending || existing.Status == sharedWorkReady || existing.Status == sharedWorkRunning) {
				work = existing
				for i := range mutation.work.Refs {
					mutation.work.Refs[i].JoinExisting = true
					work.Refs = append(work.Refs, mutation.work.Refs[i])
				}
				work.UpdatedAt = time.Now().UTC()
				break
			}
		}
		if _, ok := s.v33.works[work.ID]; !ok {
			s.v33.works[work.ID] = work
		}
		work.RefCount = activeRefCount(work.Refs)
		for _, ref := range mutation.refs {
			oldRangeIDs[ref.Range.ID] = ref.Range.SharedWorkID
			ref.Range.SharedWorkID = work.ID
		}
	}
	if err := s.v33.persistLocked(); err != nil {
		s.v33.works = before
		return err
	}
	for _, mutation := range mutations {
		for _, ref := range mutation.refs {
			if err := s.store.SaveRange(ref.Range); err != nil {
				for _, rollback := range refs {
					if old, ok := oldRangeIDs[rollback.Range.ID]; ok {
						rollback.Range.SharedWorkID = old
						_ = s.store.SaveRange(rollback.Range)
					}
				}
				s.v33.works = before
				_ = s.v33.persistLocked()
				return err
			}
		}
	}
	return nil
}

func activeRefCount(refs []SharedWorkRef) int {
	count := 0
	for _, ref := range refs {
		if !ref.Canceled {
			count++
		}
	}
	return count
}

type claimedSharedWork struct {
	work     *SharedWork
	group    GroupProviderAdapter
	adapter  ProviderAdapter
	mode     DownloadMode
	priority int
}

func (s *Service) claimSharedWork(batchID string) *claimedSharedWork {
	if s.v33 == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	batch := s.store.GetBatch(batchID)
	if batch == nil || batch.Status.Terminal() || batch.CancelRequested || batch.PauseRequested {
		return nil
	}
	s.v33.mu.Lock()
	defer s.v33.mu.Unlock()
	ids := make([]string, 0, len(s.v33.works))
	for id := range s.v33.works {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		work := s.v33.works[id]
		if work.Status != sharedWorkPending && work.Status != sharedWorkReady {
			continue
		}
		belongs := false
		for _, ref := range work.Refs {
			belongs = belongs || (!ref.Canceled && ref.BatchID == batchID)
		}
		if !belongs || work.RefCount == 0 {
			continue
		}
		var group GroupProviderAdapter
		groupOK := false
		// A single-address/single-dataset workload must honor PlanDataset's
		// preferred provider (for example CSV for small AUTO downloads). Group
		// acceleration is only useful for an actual address or dataset group and
		// otherwise lets map iteration order silently override that decision.
		if len(work.Addresses) > 1 || len(work.Datasets) > 1 {
			group, _, groupOK = s.groupAdapterFor(work.Datasets, work.ChainKey, batch.Mode)
		}
		var adapter ProviderAdapter
		if groupOK {
			adapter = group
		} else if len(work.Addresses) == 1 && len(work.Datasets) == 1 {
			for _, ref := range work.Refs {
				if ref.Canceled {
					continue
				}
				ds, rj := s.store.GetDataset(ref.DatasetJobID), s.store.GetRange(ref.RangeJobID)
				if ds != nil && rj != nil {
					adapter, work.Provider, _ = s.selectProviderLocked(ds, rj)
				}
				break
			}
		}
		if adapter == nil {
			continue
		}
		work.Status = sharedWorkRunning
		work.OwnerBatchID = batchID
		work.Provider = adapter.Name()
		work.UpdatedAt = time.Now().UTC()
		for _, ref := range work.Refs {
			if ref.Canceled {
				continue
			}
			if rj := s.store.GetRange(ref.RangeJobID); rj != nil && !rj.Status.Terminal() {
				rj.Status = RangeRunning
				rj.Provider = adapter.Name()
				rj.UpdatedAt = time.Now().UTC()
				if rj.StartedAt == nil {
					now := time.Now().UTC()
					rj.StartedAt = &now
				}
				_ = s.store.SaveRange(rj)
			}
		}
		_ = s.v33.persistLocked()
		return &claimedSharedWork{work: cloneSharedWork(work), group: group, adapter: adapter, mode: batch.Mode}
	}
	return nil
}

type staticResultAdapter struct {
	name   string
	result *ProviderResult
}

func (a staticResultAdapter) Name() string         { return a.name }
func (a staticResultAdapter) Supports(string) bool { return true }
func (a staticResultAdapter) Available() bool      { return true }
func (a staticResultAdapter) Probe(context.Context, ProbeRequest) (ProbeResult, error) {
	return ProbeResult{}, nil
}
func (a staticResultAdapter) ExecuteRange(context.Context, RangeRequest) (*ProviderResult, error) {
	return a.result, nil
}

func (s *Service) executeSharedWork(claim *claimedSharedWork) {
	ctx, cancel := context.WithTimeout(s.ctx, 30*time.Minute)
	defer cancel()
	results := make(map[string]map[string]*ProviderResult)
	var err error
	if claim.group != nil {
		results, err = claim.group.ExecuteGroupRange(ctx, GroupRangeRequest{
			SharedWorkID: claim.work.ID, Mode: claim.mode, Priority: claim.priority,
			ChainKey: claim.work.ChainKey, ChainID: claim.work.ChainID,
			Addresses: claim.work.Addresses, Datasets: claim.work.Datasets,
			FromBlock: claim.work.FromBlock, ToBlock: claim.work.ToBlock,
		})
	} else {
		for _, ref := range claim.work.Refs {
			if ref.Canceled {
				continue
			}
			result, executeErr := claim.adapter.ExecuteRange(ctx, RangeRequest{
				DatasetJobID: ref.DatasetJobID, Mode: claim.mode, Address: ref.Address,
				Dataset: ref.Dataset, ChainKey: claim.work.ChainKey, ChainID: claim.work.ChainID,
				FromBlock: claim.work.FromBlock, ToBlock: claim.work.ToBlock,
			})
			if executeErr != nil {
				err = executeErr
				break
			}
			results[ref.Address] = map[string]*ProviderResult{ref.Dataset: result}
		}
	}
	if err != nil {
		s.failSharedWork(claim, err)
		return
	}
	for _, ref := range claim.work.Refs {
		if ref.Canceled {
			continue
		}
		byDataset := results[strings.ToLower(ref.Address)]
		result := byDataset[ref.Dataset]
		if result == nil {
			result = &ProviderResult{CompletedTo: claim.work.ToBlock}
		}
		s.executeRange(&claimedRange{
			rangeID: ref.RangeJobID, datasetJobID: ref.DatasetJobID, addressJobID: ref.AddressJobID,
			batchID: ref.BatchID, provider: claim.adapter.Name(), adapter: staticResultAdapter{name: claim.adapter.Name(), result: result},
			req: RangeRequest{DatasetJobID: ref.DatasetJobID, Mode: claim.mode, Address: ref.Address,
				Dataset: ref.Dataset, ChainKey: claim.work.ChainKey, ChainID: claim.work.ChainID,
				FromBlock: claim.work.FromBlock, ToBlock: claim.work.ToBlock},
		})
	}
	s.v33.mu.Lock()
	if work := s.v33.works[claim.work.ID]; work != nil {
		work.Status = sharedWorkCompleted
		work.OwnerBatchID = ""
		work.UpdatedAt = time.Now().UTC()
		_ = s.v33.persistLocked()
	}
	s.v33.mu.Unlock()
}

func (s *Service) failSharedWork(claim *claimedSharedWork, executionErr error) {
	s.v33.mu.Lock()
	work := s.v33.works[claim.work.ID]
	if work == nil {
		s.v33.mu.Unlock()
		return
	}
	work.Attempts++
	work.OwnerBatchID = ""
	work.UpdatedAt = time.Now().UTC()
	if work.Attempts <= s.opts.RetryLimit {
		work.Status = sharedWorkReady
		_ = s.v33.persistLocked()
		s.v33.mu.Unlock()
		s.resetSharedRanges(work, RangeReady, "")
		return
	}
	if len(work.Addresses) > 1 {
		s.splitSharedWorkLocked(work)
		_ = s.v33.persistLocked()
		s.v33.mu.Unlock()
		return
	}
	work.Status = sharedWorkFailed
	work.Poison = true
	_ = s.v33.persistLocked()
	s.v33.mu.Unlock()
	s.resetSharedRanges(work, RangeFailed, executionErr.Error())
}

func (s *Service) resetSharedRanges(work *SharedWork, status RangeStatus, errorText string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, ref := range work.Refs {
		if ref.Canceled {
			continue
		}
		if rj := s.store.GetRange(ref.RangeJobID); rj != nil && !rj.Status.Terminal() {
			rj.Status = status
			rj.Error = errorText
			rj.UpdatedAt = time.Now().UTC()
			if status.Terminal() {
				now := time.Now().UTC()
				rj.FinishedAt = &now
			}
			_ = s.store.SaveRange(rj)
			if status.Terminal() {
				s.finalizeDatasetIfDoneLocked(ref.DatasetJobID)
			}
		}
	}
}

func (s *Service) splitSharedWorkLocked(parent *SharedWork) {
	addresses := append([]string(nil), parent.Addresses...)
	sort.Strings(addresses)
	mid := len(addresses) / 2
	parent.Status, parent.Split = sharedWorkSplit, true
	for _, groupAddresses := range [][]string{addresses[:mid], addresses[mid:]} {
		child := &SharedWork{
			ID: uuid.NewString(), ChainKey: parent.ChainKey, ChainID: parent.ChainID,
			Datasets: append([]string(nil), parent.Datasets...), Addresses: append([]string(nil), groupAddresses...),
			FromBlock: parent.FromBlock, ToBlock: parent.ToBlock, Status: sharedWorkReady,
			ParentID: parent.ID, CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC(),
		}
		addressSet := make(map[string]bool)
		for _, address := range groupAddresses {
			addressSet[address] = true
		}
		for _, ref := range parent.Refs {
			if addressSet[ref.Address] {
				child.Refs = append(child.Refs, ref)
			}
		}
		child.RefCount = activeRefCount(child.Refs)
		child.Fingerprint = canonicalSharedFingerprint(child.ChainKey, child.Datasets, child.Addresses, child.FromBlock, child.ToBlock)
		s.v33.works[child.ID] = child
		for _, ref := range child.Refs {
			if rj := s.store.GetRange(ref.RangeJobID); rj != nil {
				rj.SharedWorkID, rj.Status = child.ID, RangeReady
				_ = s.store.SaveRange(rj)
			}
		}
	}
}

func (s *Service) releaseSharedRefs(match func(SharedWorkRef) bool) {
	if s.v33 == nil {
		return
	}
	s.v33.mu.Lock()
	defer s.v33.mu.Unlock()
	changed := false
	for _, work := range s.v33.works {
		for i := range work.Refs {
			if !work.Refs[i].Canceled && match(work.Refs[i]) {
				work.Refs[i].Canceled = true
				changed = true
			}
		}
		work.RefCount = activeRefCount(work.Refs)
		if work.RefCount == 0 && (work.Status == sharedWorkPending || work.Status == sharedWorkReady) {
			work.Status = sharedWorkCanceled
		}
	}
	if changed {
		_ = s.v33.persistLocked()
	}
}

func (s *Service) PlannerV2(ctx context.Context, req CreateBatchRequest) (*AcceleratorPlan, error) {
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	default:
	}
	var err error
	req, err = s.resolveRequestTimeRanges(ctx, req)
	if err != nil {
		return nil, err
	}
	network, err := chain.Resolve(req.ChainKey)
	if err != nil {
		return nil, err
	}
	req, addresses, datasets, err := normalizePreflightRequest(req)
	if err != nil {
		return nil, err
	}
	if req.ResourceProfile != "" && !req.ResourceProfile.Valid() {
		return nil, fmt.Errorf("非法资源档位 %q", req.ResourceProfile)
	}
	spec := RangeSpec{Mode: RangeModeFull}
	if req.DefaultRange != nil {
		spec = *req.DefaultRange
	}
	mode := req.Mode

	type plannerAddress struct {
		address string
		network chain.EVM
		spec    RangeSpec
	}
	byChain := make(map[string][]plannerAddress)
	for _, address := range addresses {
		addressNetwork := network
		if override := strings.TrimSpace(req.AddressChainOverrides[address]); override != "" {
			addressNetwork, err = chain.Resolve(override)
			if err != nil {
				return nil, fmt.Errorf("地址 %s 指定了未知链 %q", address, override)
			}
		}
		addressSpec := spec
		if override, ok := req.AddressOverrides[address]; ok && override.Mode != "" {
			addressSpec = override
		}
		for _, dataset := range datasets {
			if !s.hasExecutableProvider(addressNetwork.Key, dataset, mode) {
				return nil, fmt.Errorf("数据集 %s 在链 %s / 模式 %s 没有可执行 Provider", dataset, addressNetwork.Key, mode)
			}
		}
		byChain[addressNetwork.Key] = append(byChain[addressNetwork.Key], plannerAddress{address: address, network: addressNetwork, spec: addressSpec})
	}

	plan := &AcceleratorPlan{Status: "PREVIEW", UpdatedAt: time.Now().UTC()}
	chainKeys := make([]string, 0, len(byChain))
	for chainKey := range byChain {
		chainKeys = append(chainKeys, chainKey)
	}
	sort.Strings(chainKeys)
	for _, chainKey := range chainKeys {
		units := byChain[chainKey]
		for _, bundle := range s.acceleratorBundles(datasets, chainKey, mode) {
			adapter, maxGroup, grouped := s.groupAdapterFor(bundle, chainKey, mode)
			provider := ""
			if adapter != nil {
				provider = adapter.Name()
			}
			plan.DatasetBundles = append(plan.DatasetBundles, DatasetBundle{Datasets: bundle, Provider: provider, Bundled: len(bundle) > 1})
			if !grouped {
				maxGroup = 1
			}
			byRange := make(map[string][]plannerAddress)
			for _, unit := range units {
				requested := s.requestedBlocks(ctx, chainKey, unit.spec, bundle[0])
				key := fmt.Sprintf("%020d-%020d", requested.From, requested.To)
				byRange[key] = append(byRange[key], unit)
			}
			rangeKeys := make([]string, 0, len(byRange))
			for key := range byRange {
				rangeKeys = append(rangeKeys, key)
			}
			sort.Strings(rangeKeys)
			for _, rangeKey := range rangeKeys {
				rangeUnits := byRange[rangeKey]
				sort.Slice(rangeUnits, func(i, j int) bool { return rangeUnits[i].address < rangeUnits[j].address })
				for start := 0; start < len(rangeUnits); start += maxGroup {
					end := min(start+maxGroup, len(rangeUnits))
					groupAddresses := make([]string, 0, end-start)
					for _, unit := range rangeUnits[start:end] {
						groupAddresses = append(groupAddresses, unit.address)
					}
					requested := s.requestedBlocks(ctx, chainKey, rangeUnits[start].spec, bundle[0])
					fingerprint := canonicalSharedFingerprint(chainKey, bundle, groupAddresses, requested.From, requested.To)
					work := &SharedWork{ID: "preview-" + fingerprint[:12], Fingerprint: fingerprint, ChainKey: chainKey,
						ChainID: rangeUnits[start].network.ID, Datasets: bundle, Addresses: groupAddresses, FromBlock: requested.From,
						ToBlock: requested.To, Status: sharedWorkPending, RefCount: len(groupAddresses) * len(bundle)}
					plan.SharedWorkloads = append(plan.SharedWorkloads, work)
					plan.Groups = append(plan.Groups, AddressGroup{GroupID: work.ID, ChainKey: chainKey, ChainID: rangeUnits[start].network.ID,
						Datasets: bundle, Addresses: groupAddresses, FilterHash: fingerprint, WorkloadIDs: []string{work.ID}})
				}
			}
		}
	}
	plan.Metrics.InputJobs = len(addresses) * len(datasets)
	plan.Metrics.MergedWorkloads = len(plan.SharedWorkloads)
	plan.Metrics.ProviderRequestsSaved = max(0, plan.Metrics.InputJobs-plan.Metrics.MergedWorkloads)
	for _, bundle := range plan.DatasetBundles {
		if bundle.Bundled {
			plan.Metrics.BundleSavings += len(addresses) * (len(bundle.Datasets) - 1)
		}
	}
	if plan.Metrics.InputJobs > 0 {
		plan.Metrics.DownloadAmplification = float64(plan.Metrics.MergedWorkloads) / float64(plan.Metrics.InputJobs)
		plan.Metrics.ReductionRatio = 1 - plan.Metrics.DownloadAmplification
	}
	return plan, nil
}

func (s *Service) BatchAccelerator(batchID string) *AcceleratorPlan {
	batch := s.store.GetBatch(batchID)
	if s.v33 == nil || batch == nil {
		return nil
	}
	s.v33.mu.Lock()
	defer s.v33.mu.Unlock()
	plan := &AcceleratorPlan{BatchID: batchID, Status: string(batch.Status), UpdatedAt: time.Now().UTC()}
	bundleSeen := make(map[string]bool)
	for _, work := range s.v33.works {
		belongs := false
		inputRefs, joined := 0, 0
		for _, ref := range work.Refs {
			if ref.BatchID == batchID {
				belongs = true
				inputRefs++
				if ref.JoinExisting {
					joined++
				}
			}
		}
		if !belongs {
			continue
		}
		copyWork := cloneSharedWork(work)
		plan.SharedWorkloads = append(plan.SharedWorkloads, copyWork)
		plan.Groups = append(plan.Groups, AddressGroup{GroupID: work.ID, ChainKey: work.ChainKey, ChainID: work.ChainID,
			Datasets: work.Datasets, Addresses: work.Addresses, FilterHash: work.Fingerprint,
			Classification: map[bool]string{true: "HEAVY", false: "NORMAL"}[work.Heavy], Heavy: work.Heavy,
			WorkloadIDs: []string{work.ID}})
		bundleKey := strings.Join(work.Datasets, ",")
		if !bundleSeen[bundleKey] {
			bundleSeen[bundleKey] = true
			plan.DatasetBundles = append(plan.DatasetBundles, DatasetBundle{Datasets: work.Datasets, Provider: work.Provider, Bundled: len(work.Datasets) > 1})
		}
		plan.Metrics.InputJobs += inputRefs
		plan.Metrics.DuplicateWorkAvoided += joined
		if work.Heavy {
			plan.Metrics.HeavyAddressCount++
		}
		if work.Split {
			plan.Metrics.SplitCount++
		}
	}
	plan.Metrics.MergedWorkloads = len(plan.SharedWorkloads)
	plan.Metrics.ProviderRequestsSaved = max(0, plan.Metrics.InputJobs-plan.Metrics.MergedWorkloads)
	if plan.Metrics.InputJobs > 0 {
		plan.Metrics.DownloadAmplification = float64(plan.Metrics.MergedWorkloads) / float64(plan.Metrics.InputJobs)
		plan.Metrics.ReductionRatio = 1 - plan.Metrics.DownloadAmplification
	}
	sort.Slice(plan.SharedWorkloads, func(i, j int) bool { return plan.SharedWorkloads[i].ID < plan.SharedWorkloads[j].ID })
	return plan
}
