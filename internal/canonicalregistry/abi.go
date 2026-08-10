package canonicalregistry

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strconv"
	"strings"
	"time"
)

func (r *Repository) UpsertABI(ctx context.Context, record ABIRecord) error {
	address, err := normalizeAddress(record.ChainID, record.ContractAddress)
	if err != nil {
		return err
	}
	record.ContractAddress = address
	if record.ABIJSON, err = requireJSON("abi", record.ABIJSON); err != nil {
		return err
	}
	computed := sha256.Sum256([]byte(record.ABIJSON))
	computedHash := hex.EncodeToString(computed[:])
	if strings.TrimSpace(record.ABIHash) == "" {
		record.ABIHash = computedHash
	} else {
		record.ABIHash = strings.ToLower(strings.TrimSpace(record.ABIHash))
		if !hashRE.MatchString(record.ABIHash) || record.ABIHash != computedHash {
			return fmt.Errorf("%w: abi_hash mismatch", ErrInvalidInput)
		}
	}
	if record.Source, err = requiredText("source", record.Source, 128); err != nil {
		return err
	}
	if record.ObservedAt, err = requireTime("observed_at", record.ObservedAt); err != nil {
		return err
	}
	if record.UpdatedAt.IsZero() {
		record.UpdatedAt = time.Now().UTC()
	} else if record.UpdatedAt, err = requireTime("updated_at", record.UpdatedAt); err != nil {
		return err
	}
	return r.insert(ctx, "onchain.abi_registry",
		[]string{"chain_id", "contract_address", "abi_hash", "abi_json", "source", "is_verified", "observed_at", "updated_at"},
		[]string{strconv.FormatUint(uint64(record.ChainID), 10), record.ContractAddress, record.ABIHash, record.ABIJSON, record.Source, boolCSV(record.Verified), formatTime(record.ObservedAt), formatTime(record.UpdatedAt)})
}

func (r *Repository) GetPreferredABI(ctx context.Context, chainID uint32, contract string, asOf *time.Time) (ABIRecord, error) {
	address, err := normalizeAddress(chainID, contract)
	if err != nil {
		return ABIRecord{}, err
	}
	condition := ""
	if asOf != nil {
		if _, err = requireTime("as_of", *asOf); err != nil {
			return ABIRecord{}, err
		}
		condition = fmt.Sprintf(" AND observed_at <= parseDateTime64BestEffort('%s', 3, 'UTC')", asOf.UTC().Format(time.RFC3339Nano))
	}
	rows, err := r.query(ctx, fmt.Sprintf(`SELECT chain_id, contract_address, abi_hash, abi_json, source, is_verified, observed_at, updated_at
FROM onchain.abi_registry FINAL WHERE chain_id = %d AND contract_address = '%s'%s
ORDER BY is_verified DESC, observed_at DESC, abi_hash ASC LIMIT 1`, chainID, address, condition))
	if err != nil {
		return ABIRecord{}, err
	}
	if len(rows) == 0 {
		return ABIRecord{}, ErrNotFound
	}
	result, err := decodeABI(rows[0])
	if err != nil || result.ChainID != chainID || strings.ToLower(result.ContractAddress) != address {
		return ABIRecord{}, fmt.Errorf("%w: malformed ABI row", ErrQueryFailed)
	}
	result.ContractAddress = address
	return result, nil
}

func decodeABI(row map[string]any) (ABIRecord, error) {
	var out ABIRecord
	chain, err := uintValue(row["chain_id"], 32)
	if err != nil {
		return out, err
	}
	out.ChainID = uint32(chain)
	out.ContractAddress, err = stringValue(row["contract_address"])
	if err != nil {
		return out, err
	}
	out.ABIHash, err = stringValue(row["abi_hash"])
	if err != nil {
		return out, err
	}
	out.ABIJSON, err = stringValue(row["abi_json"])
	if err != nil {
		return out, err
	}
	out.Source, _ = stringValue(row["source"])
	out.Verified, err = boolValue(row["is_verified"])
	if err != nil {
		return out, err
	}
	out.ObservedAt, err = timeValue(row["observed_at"])
	if err != nil {
		return out, err
	}
	out.UpdatedAt, err = timeValue(row["updated_at"])
	return out, err
}
