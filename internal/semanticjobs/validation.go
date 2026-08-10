package semanticjobs

import (
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"
)

var (
	chainPattern   = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,31}$`)
	versionPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._+-]{0,63}$`)

	datasetWhitelist = map[string]struct{}{
		"transactions": {}, "token_transfers": {}, "internal_transactions": {},
		"logs": {}, "traces": {}, "contracts": {}, "address_activity": {},
		"parsed_events": {}, "token_metadata_registry": {}, "address_labels": {},
		"token_prices": {}, "abi_registry": {}, "entity_registry": {},
	}
	enrichmentWhitelist = map[string]struct{}{
		EnrichmentTokenMetadata: {}, EnrichmentEntityLabels: {}, EnrichmentPrices: {},
		EnrichmentContractABI: {}, EnrichmentContractMeta: {},
	}
)

func NormalizeAndValidate(req Request) (Request, error) {
	req.Chain = strings.ToLower(strings.TrimSpace(req.Chain))
	req.Dataset = strings.ToLower(strings.TrimSpace(req.Dataset))
	req.ParserVersion = strings.TrimSpace(req.ParserVersion)
	if req.Type != JobTypeReparse && req.Type != JobTypeReenrich {
		return Request{}, fmt.Errorf("unsupported job type %q", req.Type)
	}
	if !chainPattern.MatchString(req.Chain) {
		return Request{}, errors.New("chain must match [a-z0-9][a-z0-9_-]{0,31}")
	}
	if req.EndBlock < req.StartBlock {
		return Request{}, errors.New("end_block must be greater than or equal to start_block")
	}
	if req.StartBlock == 0 && req.EndBlock == ^uint64(0) {
		return Request{}, errors.New("block range is too large")
	}
	if _, ok := datasetWhitelist[req.Dataset]; !ok {
		return Request{}, fmt.Errorf("dataset %q is not allowed", req.Dataset)
	}

	switch req.Type {
	case JobTypeReparse:
		if !versionPattern.MatchString(req.ParserVersion) {
			return Request{}, errors.New("parser_version is required and contains invalid characters")
		}
		if len(req.Enrichments) != 0 {
			return Request{}, errors.New("enrichments are not allowed for REPARSE")
		}
	case JobTypeReenrich:
		if req.ParserVersion != "" {
			return Request{}, errors.New("parser_version is not allowed for REENRICH")
		}
		if len(req.Enrichments) == 0 {
			return Request{}, errors.New("at least one enrichment is required for REENRICH")
		}
		seen := make(map[string]struct{}, len(req.Enrichments))
		normalized := make([]string, 0, len(req.Enrichments))
		for _, value := range req.Enrichments {
			value = strings.ToLower(strings.TrimSpace(value))
			if _, ok := enrichmentWhitelist[value]; !ok {
				return Request{}, fmt.Errorf("enrichment %q is not allowed", value)
			}
			if _, duplicate := seen[value]; !duplicate {
				seen[value] = struct{}{}
				normalized = append(normalized, value)
			}
		}
		sort.Strings(normalized)
		req.Enrichments = normalized
	}
	return req, nil
}
