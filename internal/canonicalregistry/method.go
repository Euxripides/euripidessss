package canonicalregistry

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"
)

func (r *Repository) UpsertMethod(ctx context.Context, record MethodRecord) error {
	record.MethodID = strings.ToLower(strings.TrimSpace(record.MethodID))
	if !selectorRE.MatchString(record.MethodID) {
		return fmt.Errorf("%w: invalid method_id", ErrInvalidInput)
	}
	var err error
	if record.CanonicalSignature, err = requiredText("canonical_signature", record.CanonicalSignature, 2048); err != nil ||
		!strings.Contains(record.CanonicalSignature, "(") || !strings.HasSuffix(record.CanonicalSignature, ")") {
		return fmt.Errorf("%w: invalid canonical_signature", ErrInvalidInput)
	}
	if record.DisplayName, err = requiredText("display_name", record.DisplayName, 256); err != nil {
		return err
	}
	if record.Source, err = requiredText("source", record.Source, 128); err != nil {
		return err
	}
	if record.Confidence, err = normalizeConfidence(record.Confidence); err != nil {
		return err
	}
	if record.UpdatedAt.IsZero() {
		record.UpdatedAt = time.Now().UTC()
	} else if record.UpdatedAt, err = requireTime("updated_at", record.UpdatedAt); err != nil {
		return err
	}
	return r.insert(ctx, "onchain.method_registry",
		[]string{"method_id", "canonical_signature", "display_name", "source", "confidence", "is_verified", "updated_at"},
		[]string{record.MethodID, record.CanonicalSignature, record.DisplayName, record.Source, record.Confidence, boolCSV(record.Verified), formatTime(record.UpdatedAt)})
}

func (r *Repository) ResolveMethod(ctx context.Context, methodID string) (MethodResolution, error) {
	methodID = strings.ToLower(strings.TrimSpace(methodID))
	if !selectorRE.MatchString(methodID) {
		return MethodResolution{}, fmt.Errorf("%w: invalid method_id", ErrInvalidInput)
	}
	rows, err := r.query(ctx, fmt.Sprintf(`SELECT method_id, canonical_signature, display_name, source, confidence, is_verified, updated_at
FROM onchain.method_registry FINAL
WHERE method_id = '%s'
ORDER BY is_verified DESC, multiIf(confidence = 'HIGH', 4, confidence = 'MEDIUM', 3, confidence = 'LOW', 2, 1) DESC, canonical_signature ASC, source ASC`, methodID))
	if err != nil {
		return MethodResolution{}, err
	}
	if len(rows) == 0 {
		return MethodResolution{}, ErrNotFound
	}
	candidates := make([]MethodRecord, 0, len(rows))
	signatures := make(map[string]struct{}, len(rows))
	for _, row := range rows {
		candidate, decodeErr := decodeMethod(row)
		if decodeErr != nil || candidate.MethodID != methodID {
			return MethodResolution{}, fmt.Errorf("%w: malformed method row", ErrQueryFailed)
		}
		candidates = append(candidates, candidate)
		signatures[candidate.CanonicalSignature] = struct{}{}
	}
	unique := make([]string, 0, len(signatures))
	for signature := range signatures {
		unique = append(unique, signature)
	}
	sort.Strings(unique)
	resolution := MethodResolution{MethodID: methodID, CandidateSignatures: unique, Candidates: candidates}
	if len(unique) > 1 {
		resolution.Name = "ambiguous"
		resolution.DisplayName = "Ambiguous"
		resolution.Confidence = "UNKNOWN"
		resolution.Ambiguous = true
		return resolution, nil
	}
	best := candidates[0]
	resolution.Name = canonicalMethodName(best.CanonicalSignature)
	resolution.DisplayName = best.DisplayName
	resolution.Confidence = best.Confidence
	return resolution, nil
}

func decodeMethod(row map[string]any) (MethodRecord, error) {
	var result MethodRecord
	var err error
	if result.MethodID, err = stringValue(row["method_id"]); err != nil {
		return result, err
	}
	if result.CanonicalSignature, err = stringValue(row["canonical_signature"]); err != nil {
		return result, err
	}
	if result.DisplayName, err = stringValue(row["display_name"]); err != nil {
		return result, err
	}
	if result.Source, err = stringValue(row["source"]); err != nil {
		return result, err
	}
	if result.Confidence, err = stringValue(row["confidence"]); err != nil {
		return result, err
	}
	if result.Verified, err = boolValue(row["is_verified"]); err != nil {
		return result, err
	}
	result.UpdatedAt, err = timeValue(row["updated_at"])
	return result, err
}
