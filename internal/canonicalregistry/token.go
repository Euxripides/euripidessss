package canonicalregistry

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"
)

func (r *Repository) UpsertTokenMetadata(ctx context.Context, token TokenMetadata) error {
	address, err := normalizeAddress(token.ChainID, token.ContractAddress)
	if err != nil {
		return err
	}
	token.ContractAddress = address
	if token.Name, err = requiredText("name", token.Name, 512); err != nil {
		return err
	}
	if token.Symbol, err = requiredText("symbol", token.Symbol, 128); err != nil {
		return err
	}
	if token.TokenStandard, err = requiredText("token_standard", strings.ToUpper(token.TokenStandard), 64); err != nil {
		return err
	}
	if token.LogoURI, err = optionalLogoURI(token.LogoURI); err != nil {
		return err
	}
	if token.LogoHash != "" {
		token.LogoHash = strings.ToLower(strings.TrimSpace(token.LogoHash))
		if !hashRE.MatchString(token.LogoHash) {
			return fmt.Errorf("%w: invalid logo_hash", ErrInvalidInput)
		}
	}
	if token.LogoSource, err = requiredText("logo_source", token.LogoSource, 128); err != nil {
		return err
	}
	if token.OfficialWebsite, err = optionalURL("official_website", token.OfficialWebsite); err != nil {
		return err
	}
	if token.MetadataSource, err = requiredText("metadata_source", token.MetadataSource, 128); err != nil {
		return err
	}
	if token.MetadataConfidence, err = normalizeConfidence(token.MetadataConfidence); err != nil {
		return err
	}
	if token.FirstSeenTime, err = requireTime("first_seen_time", token.FirstSeenTime); err != nil {
		return err
	}
	if token.MetadataUpdatedAt, err = requireTime("metadata_updated_at", token.MetadataUpdatedAt); err != nil {
		return err
	}
	if token.UpdatedAt.IsZero() {
		token.UpdatedAt = time.Now().UTC()
	} else if token.UpdatedAt, err = requireTime("updated_at", token.UpdatedAt); err != nil {
		return err
	}

	columns := []string{"chain_id", "contract_address", "name", "symbol", "decimals", "token_standard", "logo_uri", "logo_hash", "logo_source", "is_verified", "is_spam", "official_website", "first_seen_block", "first_seen_time", "metadata_source", "metadata_confidence", "metadata_updated_at", "updated_at"}
	values := tokenValues(token)
	if err = r.insert(ctx, "onchain.token_metadata_registry", columns, values); err != nil {
		return err
	}
	observationID := deterministicUUID(strconv.FormatUint(uint64(token.ChainID), 10), token.ContractAddress, token.Name, token.Symbol,
		strconv.Itoa(int(token.Decimals)), token.TokenStandard, token.MetadataSource, formatTime(token.MetadataUpdatedAt))
	historyColumns := append([]string{"chain_id", "contract_address", "observation_id"}, columns[2:17]...)
	historyValues := append([]string{strconv.FormatUint(uint64(token.ChainID), 10), token.ContractAddress, observationID}, values[2:17]...)
	historyColumns = append(historyColumns, "observed_at")
	historyValues = append(historyValues, formatTime(token.MetadataUpdatedAt))
	return r.insert(ctx, "onchain.token_metadata_history", historyColumns, historyValues)
}

func tokenValues(token TokenMetadata) []string {
	return []string{strconv.FormatUint(uint64(token.ChainID), 10), token.ContractAddress, token.Name, token.Symbol,
		strconv.Itoa(int(token.Decimals)), token.TokenStandard, token.LogoURI, token.LogoHash, token.LogoSource,
		boolCSV(token.Verified), boolCSV(token.Spam), token.OfficialWebsite, strconv.FormatUint(token.FirstSeenBlock, 10),
		formatTime(token.FirstSeenTime), token.MetadataSource, token.MetadataConfidence, formatTime(token.MetadataUpdatedAt), formatTime(token.UpdatedAt)}
}

func (r *Repository) GetTokenMetadata(ctx context.Context, chainID uint32, contract string) (TokenMetadata, error) {
	return r.getTokenMetadata(ctx, chainID, contract, nil)
}

func (r *Repository) GetTokenMetadataAsOf(ctx context.Context, chainID uint32, contract string, asOf time.Time) (TokenMetadata, error) {
	if _, err := requireTime("as_of", asOf); err != nil {
		return TokenMetadata{}, err
	}
	return r.getTokenMetadata(ctx, chainID, contract, &asOf)
}

func (r *Repository) getTokenMetadata(ctx context.Context, chainID uint32, contract string, asOf *time.Time) (TokenMetadata, error) {
	address, err := normalizeAddress(chainID, contract)
	if err != nil {
		return TokenMetadata{}, err
	}
	table, timeColumn, condition := "onchain.token_metadata_registry FINAL", "updated_at", ""
	if asOf != nil {
		table, timeColumn = "onchain.token_metadata_history FINAL", "observed_at"
		condition = fmt.Sprintf(" AND observed_at <= parseDateTime64BestEffort('%s', 3, 'UTC')", asOf.UTC().Format(time.RFC3339Nano))
	}
	query := fmt.Sprintf(`SELECT chain_id, contract_address, name, symbol, decimals, token_standard, logo_uri, logo_hash, logo_source,
is_verified, is_spam, official_website, first_seen_block, first_seen_time, metadata_source, metadata_confidence,
metadata_updated_at, %s AS updated_at
FROM %s WHERE chain_id = %d AND contract_address = '%s'%s ORDER BY %s DESC LIMIT 1`, timeColumn, table, chainID, address, condition, timeColumn)
	rows, err := r.query(ctx, query)
	if err != nil {
		return TokenMetadata{}, err
	}
	if len(rows) == 0 {
		return TokenMetadata{}, ErrNotFound
	}
	token, err := decodeToken(rows[0])
	if err != nil || token.ChainID != chainID || strings.ToLower(token.ContractAddress) != address {
		return TokenMetadata{}, fmt.Errorf("%w: malformed token row", ErrQueryFailed)
	}
	token.ContractAddress = address
	return token, nil
}

func decodeToken(row map[string]any) (TokenMetadata, error) {
	var out TokenMetadata
	chain, err := uintValue(row["chain_id"], 32)
	if err != nil {
		return out, err
	}
	out.ChainID = uint32(chain)
	if out.ContractAddress, err = stringValue(row["contract_address"]); err != nil {
		return out, err
	}
	if out.Name, err = stringValue(row["name"]); err != nil {
		return out, err
	}
	if out.Symbol, err = stringValue(row["symbol"]); err != nil {
		return out, err
	}
	decimal, err := uintValue(row["decimals"], 8)
	if err != nil {
		return out, err
	}
	out.Decimals = uint8(decimal)
	out.TokenStandard, _ = stringValue(row["token_standard"])
	out.LogoURI, _ = stringValue(row["logo_uri"])
	out.LogoHash, _ = stringValue(row["logo_hash"])
	out.LogoSource, _ = stringValue(row["logo_source"])
	if out.Verified, err = boolValue(row["is_verified"]); err != nil {
		return out, err
	}
	if out.Spam, err = boolValue(row["is_spam"]); err != nil {
		return out, err
	}
	out.OfficialWebsite, _ = stringValue(row["official_website"])
	firstBlock, err := uintValue(row["first_seen_block"], 64)
	if err != nil {
		return out, err
	}
	out.FirstSeenBlock = firstBlock
	if out.FirstSeenTime, err = timeValue(row["first_seen_time"]); err != nil {
		return out, err
	}
	out.MetadataSource, _ = stringValue(row["metadata_source"])
	out.MetadataConfidence, _ = stringValue(row["metadata_confidence"])
	if out.MetadataUpdatedAt, err = timeValue(row["metadata_updated_at"]); err != nil {
		return out, err
	}
	out.UpdatedAt, err = timeValue(row["updated_at"])
	return out, err
}
