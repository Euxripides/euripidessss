package canonicalapi

import (
	"encoding/json"
	"fmt"
	"strings"
)

func decodeTransaction(row map[string]any) (CanonicalTransaction, error) {
	var out CanonicalTransaction
	var err error
	if value, e := uint64Value(row, "chain_id"); e != nil || value == 0 || value > uint64(^uint32(0)) {
		return out, ErrInvalidData
	} else {
		out.ChainID = uint32(value)
	}
	if out.BlockNumber, err = uint64Value(row, "block_number"); err != nil {
		return out, err
	}
	out.BlockHash = strings.ToLower(textValue(row, "block_hash"))
	if out.BlockTime, err = timeValue(row, "block_time"); err != nil {
		return out, err
	}
	out.TxHash = strings.ToLower(textValue(row, "tx_hash"))
	if !txHashRE.MatchString(out.TxHash) {
		return out, ErrInvalidData
	}
	if value, e := uint64Value(row, "transaction_index"); e != nil || value > uint64(^uint32(0)) {
		return out, ErrInvalidData
	} else {
		out.TxIndex = uint32(value)
	}
	from := strings.ToLower(textValue(row, "from_address"))
	if !evmAddressRE.MatchString(from) {
		return out, ErrInvalidData
	}
	out.From = AddressDTO{Address: from}
	to := strings.ToLower(textValue(row, "to_address"))
	if to != "" {
		if !evmAddressRE.MatchString(to) {
			return out, ErrInvalidData
		}
		value := AddressDTO{Address: to}
		out.To = &value
	}
	if out.Nonce, err = uint64Value(row, "nonce"); err != nil {
		return out, err
	}
	if out.Value, err = amount(textValue(row, "value_raw"), textValue(row, "value_decimal")); err != nil {
		return out, err
	}
	out.Input = textValue(row, "input")
	out.Method = MethodDTO{ID: strings.ToLower(textValue(row, "method_id"))}
	out.TxType = textValue(row, "tx_type")
	if out.GasLimit, err = uint64Value(row, "gas_limit"); err != nil {
		return out, err
	}
	if out.GasUsed, err = uint64Value(row, "gas_used"); err != nil {
		return out, err
	}
	out.GasPrice = textValue(row, "gas_price")
	out.EffectiveGasPrice = textValue(row, "effective_gas_price")
	fee := textValue(row, "fee_native")
	if out.FeeNative, err = amount(fee, fee); err != nil {
		return out, err
	}
	if value := textValue(row, "fee_usd"); value != "" {
		out.FeeUSD = &USDDTO{USDValue: value, PriceSource: "UNKNOWN", PriceConfidence: "UNKNOWN"}
	}
	out.RawStatus = textValue(row, "raw_status")
	out.StatusSource = strings.ToUpper(textValue(row, "status_source"))
	if out.StatusSource == "RECEIPT" {
		out.Status = canonicalStatus(textValue(row, "status"))
	} else {
		out.Status = "UNKNOWN"
	}
	if out.IsContractCreation, err = boolValue(row, "is_contract_creation"); err != nil {
		return out, err
	}
	created := strings.ToLower(textValue(row, "created_contract_address"))
	if created != "" {
		if !evmAddressRE.MatchString(created) {
			return out, ErrInvalidData
		}
		value := AddressDTO{Address: created}
		out.CreatedContract = &value
	}
	out.ErrorMessage = textValue(row, "error_message")
	out.Provenance, err = decodeProvenance(row, "TRANSACTION")
	if err != nil {
		return out, err
	}
	out.TokenTransfers = []CanonicalTokenTransfer{}
	out.InternalTransactions = []CanonicalInternalTransaction{}
	out.ParsedEvents = []CanonicalParsedEvent{}
	return out, nil
}

func decodeTokenTransfer(chainID uint32, row map[string]any) (CanonicalTokenTransfer, error) {
	var out CanonicalTokenTransfer
	var err error
	if value, e := uint64Value(row, "log_index"); e != nil || value > uint64(^uint32(0)) {
		return out, fmt.Errorf("log index: %w", ErrInvalidData)
	} else {
		out.LogIndex = uint32(value)
	}
	from := strings.ToLower(textValue(row, "from_address"))
	to := strings.ToLower(textValue(row, "to_address"))
	if !evmAddressRE.MatchString(from) || !evmAddressRE.MatchString(to) {
		return out, fmt.Errorf("participant address: %w", ErrInvalidData)
	}
	out.From = AddressDTO{Address: from}
	out.To = AddressDTO{Address: to}
	contract := strings.ToLower(textValue(row, "token_address"))
	if !evmAddressRE.MatchString(contract) {
		return out, fmt.Errorf("token address %q: %w", contract, ErrInvalidData)
	}
	out.Token, err = decodeToken(chainID, contract, row)
	if err != nil {
		return out, fmt.Errorf("token metadata: %w", err)
	}
	out.Standard = textValue(row, "registry_standard")
	if out.Standard == "" {
		out.Standard = textValue(row, "token_standard")
	}
	out.Amount, err = amount(textValue(row, "raw_value"), textValue(row, "value_decimal"))
	if err != nil {
		return out, fmt.Errorf("amount: %w", err)
	}
	out.USD, err = decodeUSD(row)
	if err != nil {
		return out, fmt.Errorf("historical USD: %w", err)
	}
	out.Provenance, err = decodeProvenance(row, "TOKEN_TRANSFER")
	if err != nil {
		return out, fmt.Errorf("provenance: %w", err)
	}
	return out, nil
}

func decodeToken(chainID uint32, contract string, row map[string]any) (TokenDTO, error) {
	var out TokenDTO
	out.ChainID = chainID
	out.Contract = contract
	out.Name = textValue(row, "token_name")
	out.Symbol = textValue(row, "token_symbol")
	if out.Name == "" {
		out.Name = textValue(row, "fact_token_name")
	}
	if out.Symbol == "" {
		out.Symbol = textValue(row, "fact_token_symbol")
	}
	value, err := uint64Value(row, "token_decimals")
	if err != nil {
		return out, err
	}
	if value == 0 && textValue(row, "token_decimals") == "" {
		value, err = uint64Value(row, "fact_token_decimals")
		if err != nil {
			return out, err
		}
	}
	if value > 255 {
		return out, ErrInvalidData
	}
	out.Decimals = uint8(value)
	out.Standard = textValue(row, "registry_standard")
	if out.Standard == "" {
		out.Standard = textValue(row, "token_standard")
	}
	out.LogoURI = textValue(row, "logo_uri")
	out.LogoHash = textValue(row, "logo_hash")
	out.LogoSource = textValue(row, "logo_source")
	out.Verified, _ = boolValue(row, "is_verified")
	out.Spam, _ = boolValue(row, "is_spam")
	out.OfficialWebsite = textValue(row, "official_website")
	out.MetadataSource = textValue(row, "metadata_source")
	out.MetadataConfidence = canonicalConfidence(textValue(row, "metadata_confidence"))
	out.MetadataUpdatedAt, _ = timeValue(row, "metadata_updated_at")
	if out.MetadataSource == "" {
		out.MetadataSource = "FALLBACK_ADDRESS"
		out.MetadataConfidence = "LOW"
	}
	return out, nil
}

func decodeUSD(row map[string]any) (*USDDTO, error) {
	value := textValue(row, "usd_value")
	if value == "" {
		return nil, nil
	}
	if !decimalRE.MatchString(value) {
		return nil, ErrInvalidData
	}
	price := textValue(row, "price_usd")
	if price != "" && !decimalRE.MatchString(price) {
		return nil, ErrInvalidData
	}
	when, err := timeValue(row, "price_time")
	if err != nil {
		return nil, err
	}
	source := textValue(row, "price_source")
	confidence := canonicalConfidence(textValue(row, "price_confidence"))
	if source == "" {
		source = "UNKNOWN"
	}
	return &USDDTO{USDValue: value, PriceUSD: price, PriceTime: when, PriceSource: source, PriceConfidence: confidence}, nil
}

func decodeInternal(row map[string]any) (CanonicalInternalTransaction, error) {
	var out CanonicalInternalTransaction
	var err error
	out.TraceAddress = textValue(row, "trace_address")
	if value, e := uint64Value(row, "trace_index"); e != nil || value > uint64(^uint32(0)) {
		return out, ErrInvalidData
	} else {
		out.TraceIndex = uint32(value)
	}
	out.CallType = textValue(row, "call_type")
	if value, e := uint64Value(row, "depth"); e != nil || value > 65535 {
		return out, ErrInvalidData
	} else {
		out.Depth = uint16(value)
	}
	if textValue(row, "parent_trace_index") != "" {
		value, e := uint64Value(row, "parent_trace_index")
		if e != nil || value > uint64(^uint32(0)) {
			return out, ErrInvalidData
		}
		typed := uint32(value)
		out.ParentTraceIndex = &typed
	}
	from := strings.ToLower(textValue(row, "from_address"))
	to := strings.ToLower(textValue(row, "to_address"))
	if !evmAddressRE.MatchString(from) || !evmAddressRE.MatchString(to) {
		return out, ErrInvalidData
	}
	out.From = AddressDTO{Address: from}
	out.To = AddressDTO{Address: to}
	out.Amount, err = amount(textValue(row, "value_raw"), textValue(row, "value_decimal"))
	if err != nil {
		return out, err
	}
	out.Input = textValue(row, "input")
	out.Output = textValue(row, "output")
	if out.Gas, err = uint64Value(row, "gas"); err != nil {
		return out, err
	}
	if out.GasUsed, err = uint64Value(row, "gas_used"); err != nil {
		return out, err
	}
	if out.Success, err = boolValue(row, "success"); err != nil {
		return out, err
	}
	out.Error = textValue(row, "error")
	out.Provenance, err = decodeProvenance(row, "INTERNAL_TRANSACTION")
	return out, err
}

func decodeCreation(row map[string]any) (CanonicalContractCreation, error) {
	var out CanonicalContractCreation
	var err error
	creator := strings.ToLower(textValue(row, "creator_address"))
	contract := strings.ToLower(textValue(row, "contract_address"))
	if !evmAddressRE.MatchString(creator) || !evmAddressRE.MatchString(contract) {
		return out, ErrInvalidData
	}
	out.Creator = AddressDTO{Address: creator}
	out.Contract = AddressDTO{Address: contract}
	if value := strings.ToLower(textValue(row, "factory_address")); value != "" {
		if !evmAddressRE.MatchString(value) {
			return out, ErrInvalidData
		}
		typed := AddressDTO{Address: value}
		out.Factory = &typed
	}
	out.CreationType = textValue(row, "creation_type")
	out.InitCodeHash = textValue(row, "init_code_hash")
	out.RuntimeBytecodeHash = textValue(row, "runtime_code_hash")
	out.IsProxy, _ = boolValue(row, "is_proxy")
	out.ProxyType = textValue(row, "proxy_type")
	if value := strings.ToLower(textValue(row, "implementation_address")); value != "" {
		if !evmAddressRE.MatchString(value) {
			return out, ErrInvalidData
		}
		typed := AddressDTO{Address: value}
		out.ImplementationAddress = &typed
	}
	out.TokenDetected, _ = boolValue(row, "token_detected")
	out.TokenStandard = textValue(row, "token_standard")
	out.Provenance, err = decodeProvenance(row, "CONTRACT_CREATION")
	return out, err
}

func decodeEvent(row map[string]any) (CanonicalParsedEvent, error) {
	var out CanonicalParsedEvent
	if value, e := uint64Value(row, "log_index"); e != nil || value > uint64(^uint32(0)) {
		return out, ErrInvalidData
	} else {
		out.LogIndex = uint32(value)
	}
	contract := strings.ToLower(textValue(row, "contract_address"))
	if !evmAddressRE.MatchString(contract) {
		return out, ErrInvalidData
	}
	out.Contract = AddressDTO{Address: contract}
	out.Topic0 = strings.ToLower(textValue(row, "topic0"))
	out.Name = textValue(row, "event_name")
	out.Signature = textValue(row, "event_signature")
	decoded := textValue(row, "decoded_fields")
	if decoded != "" {
		if !json.Valid([]byte(decoded)) {
			return out, ErrInvalidData
		}
		out.DecodedFields = json.RawMessage(decoded)
	}
	out.DecoderSource = textValue(row, "decoder_source")
	out.DecoderConfidence = canonicalConfidence(textValue(row, "decoder_confidence"))
	out.ParserVersion = textValue(row, "parser_version")
	value, e := uint64Value(row, "schema_version")
	if e != nil || value > uint64(^uint32(0)) {
		return out, ErrInvalidData
	}
	out.SchemaVersion = uint32(value)
	return out, nil
}

func decodeActivity(row map[string]any) (CanonicalActivity, error) {
	var out CanonicalActivity
	var err error
	if value, e := uint64Value(row, "chain_id"); e != nil || value == 0 || value > uint64(^uint32(0)) {
		return out, ErrInvalidData
	} else {
		out.ChainID = uint32(value)
	}
	address := strings.ToLower(textValue(row, "address"))
	if !evmAddressRE.MatchString(address) {
		return out, fmt.Errorf("activity address %q: %w", address, ErrInvalidData)
	}
	out.Address = AddressDTO{Address: address}
	counterparty := strings.ToLower(textValue(row, "counterparty_address"))
	if counterparty != "" {
		if !evmAddressRE.MatchString(counterparty) {
			return out, fmt.Errorf("activity counterparty %q: %w", counterparty, ErrInvalidData)
		}
		value := AddressDTO{Address: counterparty}
		out.Counterparty = &value
	}
	out.Direction = strings.ToUpper(textValue(row, "direction"))
	switch out.Direction {
	case "IN", "OUT", "SELF", "CALL":
	default:
		return out, fmt.Errorf("activity direction %q: %w", out.Direction, ErrInvalidData)
	}
	out.ActivityType = strings.ToUpper(textValue(row, "activity_type"))
	if out.BlockNumber, err = uint64Value(row, "block_number"); err != nil {
		return out, err
	}
	if out.BlockTime, err = timeValue(row, "block_time"); err != nil {
		return out, err
	}
	out.TxHash = strings.ToLower(textValue(row, "tx_hash"))
	if !txHashRE.MatchString(out.TxHash) {
		return out, fmt.Errorf("activity tx hash %q: %w", out.TxHash, ErrInvalidData)
	}
	out.EventIndex = textValue(row, "event_index")
	if tokenAddress := strings.ToLower(textValue(row, "token_address")); tokenAddress != "" {
		if !evmAddressRE.MatchString(tokenAddress) {
			return out, fmt.Errorf("activity token %q: %w", tokenAddress, ErrInvalidData)
		}
		value, e := decodeToken(out.ChainID, tokenAddress, row)
		if e != nil {
			return out, e
		}
		out.Token = &value
	}
	raw := textValue(row, "amount_text")
	out.Amount, err = amount(raw, raw)
	if err != nil {
		return out, fmt.Errorf("activity amount: %w", err)
	}
	out.USD, err = decodeUSD(row)
	if err != nil {
		return out, fmt.Errorf("activity USD: %w", err)
	}
	out.Method = MethodDTO{ID: strings.ToLower(textValue(row, "method_id")), Name: textValue(row, "method_name"), Display: textValue(row, "method_name"), Confidence: "UNKNOWN", Source: "ADDRESS_ACTIVITY"}
	if strings.EqualFold(textValue(row, "status_source"), "RECEIPT") {
		out.Status = canonicalStatus(textValue(row, "tx_status"))
	} else {
		out.Status = "UNKNOWN"
	}
	out.Provenance, err = decodeProvenance(row, "ADDRESS_ACTIVITY")
	if err != nil {
		return out, fmt.Errorf("activity provenance: %w", err)
	}
	return out, nil
}

func decodeProvenance(row map[string]any, sourceType string) (ProvenanceDTO, error) {
	var out ProvenanceDTO
	out.SourceProvider = textValue(row, "source_provider")
	out.SourceType = sourceType
	out.DownloadJobID = textValue(row, "ingest_job_id")
	out.SourceRangeID = textValue(row, "source_range_id")
	out.ParserVersion = textValue(row, "parser_version")
	out.NormalizerVersion = textValue(row, "normalizer_version")
	value, err := uint64Value(row, "schema_version")
	if err != nil || value > uint64(^uint32(0)) {
		return out, ErrInvalidData
	}
	out.SchemaVersion = uint32(value)
	out.IngestedAt, err = timeValue(row, "ingested_at")
	return out, err
}

func transactionAddresses(tx CanonicalTransaction, transfers []CanonicalTokenTransfer, internal []CanonicalInternalTransaction, creation *CanonicalContractCreation, events []CanonicalParsedEvent) []string {
	out := []string{tx.From.Address}
	if tx.To != nil {
		out = append(out, tx.To.Address)
	}
	if tx.CreatedContract != nil {
		out = append(out, tx.CreatedContract.Address)
	}
	for _, item := range transfers {
		out = append(out, item.From.Address, item.To.Address, item.Token.Contract)
	}
	for _, item := range internal {
		out = append(out, item.From.Address, item.To.Address)
	}
	if creation != nil {
		out = append(out, creation.Creator.Address, creation.Contract.Address)
		if creation.Factory != nil {
			out = append(out, creation.Factory.Address)
		}
		if creation.ImplementationAddress != nil {
			out = append(out, creation.ImplementationAddress.Address)
		}
	}
	for _, item := range events {
		out = append(out, item.Contract.Address)
	}
	return out
}
func labeled(address string, labels map[string]AddressDTO) AddressDTO {
	if value, ok := labels[address]; ok {
		return value
	}
	return AddressDTO{Address: address}
}
func applyLabels(tx *CanonicalTransaction, transfers []CanonicalTokenTransfer, internal []CanonicalInternalTransaction, creation *CanonicalContractCreation, events []CanonicalParsedEvent, labels map[string]AddressDTO) {
	tx.From = labeled(tx.From.Address, labels)
	if tx.To != nil {
		value := labeled(tx.To.Address, labels)
		tx.To = &value
	}
	if tx.CreatedContract != nil {
		value := labeled(tx.CreatedContract.Address, labels)
		tx.CreatedContract = &value
	}
	for i := range transfers {
		transfers[i].From = labeled(transfers[i].From.Address, labels)
		transfers[i].To = labeled(transfers[i].To.Address, labels)
	}
	for i := range internal {
		internal[i].From = labeled(internal[i].From.Address, labels)
		internal[i].To = labeled(internal[i].To.Address, labels)
	}
	if creation != nil {
		creation.Creator = labeled(creation.Creator.Address, labels)
		creation.Contract = labeled(creation.Contract.Address, labels)
		if creation.Factory != nil {
			value := labeled(creation.Factory.Address, labels)
			creation.Factory = &value
		}
		if creation.ImplementationAddress != nil {
			value := labeled(creation.ImplementationAddress.Address, labels)
			creation.ImplementationAddress = &value
		}
	}
	for i := range events {
		events[i].Contract = labeled(events[i].Contract.Address, labels)
	}
}
