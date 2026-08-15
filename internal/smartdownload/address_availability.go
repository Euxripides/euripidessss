package smartdownload

import (
	"sort"
	"strings"
)

// AddressAvailability summarizes task and certified-result state for the
// shared address library without treating a completed task as proof of data.
type AddressAvailability struct {
	Jobs          int   `json:"jobs"`
	CompletedJobs int   `json:"completed_jobs"`
	PartialJobs   int   `json:"partial_jobs"`
	FailedJobs    int   `json:"failed_jobs"`
	RunningJobs   int   `json:"running_jobs"`
	CertifiedSets int   `json:"certified_datasets"`
	IndexedRows   int64 `json:"indexed_rows"`
	DBWriteFailed int   `json:"db_write_failed"`
}

type KnownAddress struct {
	ChainKey string
	ChainID  int64
	Address  string
}

func (s *Service) KnownAddresses() []KnownAddress {
	if s == nil {
		return nil
	}
	seen := map[string]KnownAddress{}
	for _, job := range s.store.ListAddresses() {
		key := job.ChainKey + ":" + job.Address
		seen[key] = KnownAddress{ChainKey: job.ChainKey, ChainID: job.ChainID, Address: job.Address}
	}
	items := make([]KnownAddress, 0, len(seen))
	for _, item := range seen {
		items = append(items, item)
	}
	sort.Slice(items, func(i, j int) bool {
		if items[i].ChainKey == items[j].ChainKey {
			return items[i].Address < items[j].Address
		}
		return items[i].ChainKey < items[j].ChainKey
	})
	return items
}

func (s *Service) AddressAvailability(chainKey, address string) AddressAvailability {
	var out AddressAvailability
	if s == nil {
		return out
	}
	chainKey = strings.ToLower(strings.TrimSpace(chainKey))
	address = strings.ToLower(strings.TrimSpace(address))
	for _, job := range s.store.ListAddresses() {
		if job.ChainKey != chainKey || job.Address != address {
			continue
		}
		out.Jobs++
		switch job.Status {
		case AddressCompleted:
			out.CompletedJobs++
		case AddressPartial:
			out.PartialJobs++
		case AddressFailed, AddressCanceled:
			out.FailedJobs++
		default:
			out.RunningJobs++
		}
	}
	for _, result := range s.results.List() {
		if result.ChainKey != chainKey || result.Address != address {
			continue
		}
		if result.Certification == "CERTIFIED" {
			out.CertifiedSets++
		}
		if result.Writer != nil && result.WriteError == "" {
			out.IndexedRows += result.Writer.VerifiedRows
		}
		if result.Certification == "DB_WRITE_FAILED" || result.WriteError != "" {
			out.DBWriteFailed++
		}
	}
	return out
}
