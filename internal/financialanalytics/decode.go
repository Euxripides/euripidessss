package financialanalytics

import (
	"encoding/json"
	"strconv"
)

func text(row map[string]any, key string) (string, error) {
	v, ok := row[key]
	if !ok || v == nil {
		return "", ErrInvalidData
	}
	switch x := v.(type) {
	case string:
		return x, nil
	case json.Number:
		return x.String(), nil
	case float64:
		return strconv.FormatFloat(x, 'f', -1, 64), nil
	default:
		return "", ErrInvalidData
	}
}

func nullableText(row map[string]any, key string) (*string, error) {
	if row[key] == nil {
		return nil, nil
	}
	v, err := text(row, key)
	if err != nil {
		return nil, err
	}
	return &v, nil
}

func count(row map[string]any, key string) (uint64, error) {
	v, err := text(row, key)
	if err != nil {
		return 0, err
	}
	return strconv.ParseUint(v, 10, 64)
}

func decodeFlow(row map[string]any) (AddressUSDFlow, error) {
	var out AddressUSDFlow
	var err error
	for key, target := range map[string]**string{
		"total_in_usd": &out.TotalInUSD, "total_out_usd": &out.TotalOutUSD, "netflow_usd": &out.NetflowUSD,
		"native_in_usd": &out.NativeInUSD, "native_out_usd": &out.NativeOutUSD,
		"stablecoin_in_usd": &out.StablecoinInUSD, "stablecoin_out_usd": &out.StablecoinOutUSD,
		"token_in_usd": &out.TokenInUSD, "token_out_usd": &out.TokenOutUSD,
	} {
		if *target, err = nullableText(row, key); err != nil {
			return out, err
		}
	}
	return out, nil
}
