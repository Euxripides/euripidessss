package canonicalregistry

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"
)

func (r *Repository) UpsertTokenPrice(ctx context.Context, price TokenPrice) error {
	address, err := normalizeAddress(price.ChainID, price.TokenAddress)
	if err != nil {
		return err
	}
	price.TokenAddress = address
	price.PriceUSD = strings.TrimSpace(price.PriceUSD)
	if !decimalRE.MatchString(price.PriceUSD) {
		return fmt.Errorf("%w: invalid price_usd", ErrInvalidInput)
	}
	if price.Source, err = requiredText("source", price.Source, 128); err != nil {
		return err
	}
	if price.Confidence, err = normalizeConfidence(price.Confidence); err != nil {
		return err
	}
	if price.TimestampBucket, err = requireTime("timestamp_bucket", price.TimestampBucket); err != nil {
		return err
	}
	if price.ObservedAt, err = requireTime("observed_at", price.ObservedAt); err != nil {
		return err
	}
	if price.UpdatedAt.IsZero() {
		price.UpdatedAt = time.Now().UTC()
	} else if price.UpdatedAt, err = requireTime("updated_at", price.UpdatedAt); err != nil {
		return err
	}
	return r.insert(ctx, "onchain.token_prices", []string{"chain_id", "token_address", "timestamp_bucket", "price_usd", "source", "confidence", "observed_at", "updated_at"},
		[]string{strconv.FormatUint(uint64(price.ChainID), 10), price.TokenAddress, formatTime(price.TimestampBucket), price.PriceUSD, price.Source, price.Confidence, formatTime(price.ObservedAt), formatTime(price.UpdatedAt)})
}

func (r *Repository) GetTokenPriceAsOf(ctx context.Context, chainID uint32, token string, asOf time.Time) (TokenPrice, error) {
	address, err := normalizeAddress(chainID, token)
	if err != nil {
		return TokenPrice{}, err
	}
	if asOf, err = requireTime("as_of", asOf); err != nil {
		return TokenPrice{}, err
	}
	rows, err := r.query(ctx, fmt.Sprintf(`SELECT chain_id, token_address, timestamp_bucket, toString(price_usd) AS price_usd, source, confidence, observed_at, updated_at
FROM onchain.token_prices FINAL WHERE chain_id = %d AND token_address = '%s' AND timestamp_bucket <= parseDateTime64BestEffort('%s', 3, 'UTC')
ORDER BY timestamp_bucket DESC, multiIf(confidence = 'HIGH', 4, confidence = 'MEDIUM', 3, confidence = 'LOW', 2, 1) DESC, source ASC LIMIT 1`, chainID, address, asOf.Format(time.RFC3339Nano)))
	if err != nil {
		return TokenPrice{}, err
	}
	if len(rows) == 0 {
		return TokenPrice{}, ErrNotFound
	}
	price, err := decodePrice(rows[0])
	if err != nil || price.ChainID != chainID || strings.ToLower(price.TokenAddress) != address {
		return TokenPrice{}, fmt.Errorf("%w: malformed price row", ErrQueryFailed)
	}
	price.TokenAddress = address
	return price, nil
}

func decodePrice(row map[string]any) (TokenPrice, error) {
	var out TokenPrice
	chain, err := uintValue(row["chain_id"], 32)
	if err != nil {
		return out, err
	}
	out.ChainID = uint32(chain)
	out.TokenAddress, err = stringValue(row["token_address"])
	if err != nil {
		return out, err
	}
	out.TimestampBucket, err = timeValue(row["timestamp_bucket"])
	if err != nil {
		return out, err
	}
	out.PriceUSD, err = stringValue(row["price_usd"])
	if err != nil {
		return out, err
	}
	out.Source, _ = stringValue(row["source"])
	out.Confidence, _ = stringValue(row["confidence"])
	out.ObservedAt, err = timeValue(row["observed_at"])
	if err != nil {
		return out, err
	}
	out.UpdatedAt, err = timeValue(row["updated_at"])
	return out, err
}
