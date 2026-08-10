package contractintelligence

import (
	"context"
	"fmt"
	"strings"
)

type QueryClient interface {
	QueryJSON(ctx context.Context, query string) ([]map[string]any, error)
}

type Repository struct {
	client QueryClient
	reader EvidenceReader
}

func NewRepository(client QueryClient, reader ...EvidenceReader) *Repository {
	repository := &Repository{client: client}
	if len(reader) != 0 {
		repository.reader = reader[0]
	}
	return repository
}

func (r *Repository) GetContract(ctx context.Context, chainID uint32, address string) (CanonicalContract, error) {
	address, err := validateAddress(chainID, address)
	if err != nil {
		return CanonicalContract{}, err
	}
	rows, err := r.query(ctx, contractSQL(chainID, address))
	if err != nil {
		return CanonicalContract{}, err
	}
	if len(rows) == 0 {
		return CanonicalContract{}, ErrNotFound
	}
	contract, err := decodeContract(rows[0])
	if err != nil || contract.ChainID != chainID || contract.ContractAddress != address {
		return CanonicalContract{}, ErrInvalidData
	}
	return contract, nil
}

func (r *Repository) InspectProxy(ctx context.Context, chainID uint32, address string) (ProxyDetection, error) {
	address, err := validateAddress(chainID, address)
	if err != nil {
		return ProxyDetection{}, err
	}
	if r == nil || r.reader == nil {
		return ProxyDetection{}, ErrEvidenceRead
	}
	code, err := r.reader.RuntimeCode(ctx, chainID, address)
	if err != nil {
		return ProxyDetection{}, ErrEvidenceRead
	}
	evidence := ProxyEvidence{RuntimeCode: code, Storage: make(map[StorageSlot][32]byte, 3)}
	for _, slot := range []StorageSlot{ImplementationSlot, BeaconSlot, AdminSlot} {
		value, readErr := r.reader.StorageAt(ctx, chainID, address, slot)
		if readErr != nil {
			return ProxyDetection{}, ErrEvidenceRead
		}
		evidence.Storage[slot] = value
	}
	if implementation := slotAddress(evidence.Storage[ImplementationSlot]); implementation != "" {
		implementationCode, readErr := r.reader.RuntimeCode(ctx, chainID, implementation)
		if readErr != nil {
			return ProxyDetection{}, ErrEvidenceRead
		}
		evidence.ImplementationRuntimeCode = implementationCode
	}
	detection := DetectProxy(evidence)
	if detection.ProxyType == ProxyBeacon {
		if resolver, ok := r.reader.(BeaconResolver); ok {
			implementation, resolveErr := resolver.BeaconImplementation(ctx, chainID, detection.BeaconAddress)
			if resolveErr != nil {
				return ProxyDetection{}, ErrEvidenceRead
			}
			implementation, resolveErr = optionalAddress(implementation)
			if resolveErr != nil {
				return ProxyDetection{}, ErrInvalidData
			}
			detection.ImplementationAddress = implementation
		}
	}
	return detection, nil
}

func (r *Repository) GetFamily(ctx context.Context, chainID uint32, address string, limit int) (ContractFamily, error) {
	contract, err := r.GetContract(ctx, chainID, address)
	if err != nil {
		return ContractFamily{}, err
	}
	limit, err = normalizeLimit(limit)
	if err != nil {
		return ContractFamily{}, err
	}
	result := ContractFamily{ChainID: chainID, ContractAddress: contract.ContractAddress, RuntimeBytecodeHash: contract.RuntimeBytecodeHash}
	if contract.RuntimeBytecodeHash != "" {
		result.SameRuntimeHash, result.SameRuntimeHashCount, err = r.FindByRuntimeHash(ctx, chainID, contract.RuntimeBytecodeHash, limit)
		if err != nil {
			return ContractFamily{}, err
		}
	}
	if contract.CreatorAddress != "" {
		result.SameCreator, result.SameCreatorCount, err = r.FindByCreator(ctx, chainID, contract.CreatorAddress, limit)
		if err != nil {
			return ContractFamily{}, err
		}
	}
	if contract.FactoryAddress != "" {
		result.SameFactory, result.SameFactoryCount, err = r.FindByFactory(ctx, chainID, contract.FactoryAddress, limit)
		if err != nil {
			return ContractFamily{}, err
		}
	}
	return result, nil
}

func (r *Repository) FindByRuntimeHash(ctx context.Context, chainID uint32, hash string, limit int) ([]ContractSummary, uint64, error) {
	if chainID == 0 {
		return nil, 0, fmt.Errorf("%w: chain_id is required", ErrInvalidInput)
	}
	hash, err := validateHash(hash)
	if err != nil {
		return nil, 0, err
	}
	limit, err = normalizeLimit(limit)
	if err != nil {
		return nil, 0, err
	}
	return r.familyQuery(ctx, chainID, familyByRuntimeSQL(chainID, hash, limit))
}

func (r *Repository) FindByCreator(ctx context.Context, chainID uint32, creator string, limit int) ([]ContractSummary, uint64, error) {
	creator, err := validateAddress(chainID, creator)
	if err != nil {
		return nil, 0, err
	}
	limit, err = normalizeLimit(limit)
	if err != nil {
		return nil, 0, err
	}
	return r.familyQuery(ctx, chainID, familyByCreatorSQL(chainID, creator, limit))
}

func (r *Repository) FindByFactory(ctx context.Context, chainID uint32, factory string, limit int) ([]ContractSummary, uint64, error) {
	factory, err := validateAddress(chainID, factory)
	if err != nil {
		return nil, 0, err
	}
	limit, err = normalizeLimit(limit)
	if err != nil {
		return nil, 0, err
	}
	return r.familyQuery(ctx, chainID, familyByFactorySQL(chainID, factory, limit))
}

func (r *Repository) familyQuery(ctx context.Context, chainID uint32, query string) ([]ContractSummary, uint64, error) {
	rows, err := r.query(ctx, query)
	if err != nil {
		return nil, 0, err
	}
	items := make([]ContractSummary, 0, len(rows))
	var total uint64
	for i, row := range rows {
		item, count, decodeErr := decodeSummary(row)
		if decodeErr != nil || item.ChainID != chainID {
			return nil, 0, ErrInvalidData
		}
		if i == 0 {
			total = count
		} else if total != count {
			return nil, 0, ErrInvalidData
		}
		items = append(items, item)
	}
	return items, total, nil
}

func (r *Repository) query(ctx context.Context, query string) ([]map[string]any, error) {
	if r == nil || r.client == nil {
		return nil, ErrQueryFailed
	}
	rows, err := r.client.QueryJSON(ctx, query)
	if err != nil {
		return nil, ErrQueryFailed
	}
	return rows, nil
}

func contractSQL(chainID uint32, address string) string {
	return fmt.Sprintf(`SELECT c.chain_id,c.contract_address,c.creator_address,cc.factory_address,c.creation_tx_hash,
c.creation_block,c.creation_time,c.bytecode_hash,c.runtime_bytecode_hash,c.contract_name,c.is_verified,c.is_proxy,
c.proxy_type,c.implementation_address,if(length(c.abi_json)>0,'CONTRACTS_ABI','') AS abi_source,
cc.creation_type,cc.token_detected,cc.ingested_at
FROM onchain.contracts AS c FINAL
LEFT JOIN onchain.contract_creations AS cc FINAL
ON c.chain_id=cc.chain_id AND c.contract_address=cc.contract_address
WHERE c.chain_id=%d AND c.contract_address='%s'
ORDER BY cc.ingested_at DESC LIMIT 1`, chainID, address)
}

func familySelect(where string, limit int) string {
	return fmt.Sprintf(`SELECT c.chain_id,c.contract_address,c.creator_address,cc.factory_address,c.runtime_bytecode_hash,
c.contract_name,cc.creation_type,cc.token_detected,c.is_proxy,count() OVER () AS family_count
FROM onchain.contracts AS c FINAL
LEFT JOIN onchain.contract_creations AS cc FINAL
ON c.chain_id=cc.chain_id AND c.contract_address=cc.contract_address
WHERE %s ORDER BY c.creation_block,c.contract_address LIMIT %d`, where, limit)
}

func familyByRuntimeSQL(chainID uint32, hash string, limit int) string {
	return familySelect(fmt.Sprintf("c.chain_id=%d AND c.runtime_bytecode_hash='%s'", chainID, hash), limit)
}

func familyByCreatorSQL(chainID uint32, creator string, limit int) string {
	return familySelect(fmt.Sprintf("c.chain_id=%d AND c.creator_address='%s'", chainID, creator), limit)
}

func familyByFactorySQL(chainID uint32, factory string, limit int) string {
	return familySelect(fmt.Sprintf("c.chain_id=%d AND cc.factory_address='%s'", chainID, factory), limit)
}

func decodeContract(row map[string]any) (CanonicalContract, error) {
	chain, err := uint64Field(row, "chain_id")
	if err != nil || chain == 0 || chain > uint64(^uint32(0)) {
		return CanonicalContract{}, ErrInvalidData
	}
	address, err := optionalAddress(text(row, "contract_address"))
	if err != nil || address == "" {
		return CanonicalContract{}, ErrInvalidData
	}
	creator, err := optionalAddress(text(row, "creator_address"))
	if err != nil {
		return CanonicalContract{}, err
	}
	factory, err := optionalAddress(text(row, "factory_address"))
	if err != nil {
		return CanonicalContract{}, err
	}
	tx, err := optionalHash(text(row, "creation_tx_hash"))
	if err != nil {
		return CanonicalContract{}, err
	}
	bytecode, err := optionalHash(text(row, "bytecode_hash"))
	if err != nil {
		return CanonicalContract{}, err
	}
	runtime, err := optionalHash(text(row, "runtime_bytecode_hash"))
	if err != nil {
		return CanonicalContract{}, err
	}
	implementation, err := optionalAddress(text(row, "implementation_address"))
	if err != nil {
		return CanonicalContract{}, err
	}
	block, err := uint64Field(row, "creation_block")
	if err != nil {
		return CanonicalContract{}, err
	}
	created, err := timeField(row, "creation_time")
	if err != nil {
		return CanonicalContract{}, err
	}
	verified, err := boolField(row, "is_verified")
	if err != nil {
		return CanonicalContract{}, err
	}
	isProxy, err := boolField(row, "is_proxy")
	if err != nil {
		return CanonicalContract{}, err
	}
	token, err := boolField(row, "token_detected")
	if err != nil {
		return CanonicalContract{}, err
	}
	proxyType := normalizeProxyType(text(row, "proxy_type"), isProxy)
	return CanonicalContract{
		ChainID: uint32(chain), ContractAddress: address, CreatorAddress: creator, FactoryAddress: factory,
		CreationTx: tx, CreationBlock: block, CreationTime: created,
		CreationType: NormalizeCreationType(text(row, "creation_type"), factory, isProxy, token),
		BytecodeHash: bytecode, RuntimeBytecodeHash: runtime, ContractName: text(row, "contract_name"),
		Verified: verified, IsProxy: isProxy, ProxyType: proxyType, ImplementationAddress: implementation,
		ABISource: text(row, "abi_source"),
	}, nil
}

func decodeSummary(row map[string]any) (ContractSummary, uint64, error) {
	chain, err := uint64Field(row, "chain_id")
	if err != nil || chain == 0 || chain > uint64(^uint32(0)) {
		return ContractSummary{}, 0, ErrInvalidData
	}
	address, err := optionalAddress(text(row, "contract_address"))
	if err != nil || address == "" {
		return ContractSummary{}, 0, ErrInvalidData
	}
	creator, err := optionalAddress(text(row, "creator_address"))
	if err != nil {
		return ContractSummary{}, 0, err
	}
	factory, err := optionalAddress(text(row, "factory_address"))
	if err != nil {
		return ContractSummary{}, 0, err
	}
	runtime, err := optionalHash(text(row, "runtime_bytecode_hash"))
	if err != nil {
		return ContractSummary{}, 0, err
	}
	isProxy, err := boolField(row, "is_proxy")
	if err != nil {
		return ContractSummary{}, 0, err
	}
	token, err := boolField(row, "token_detected")
	if err != nil {
		return ContractSummary{}, 0, err
	}
	count, err := uint64Field(row, "family_count")
	if err != nil {
		return ContractSummary{}, 0, err
	}
	return ContractSummary{ChainID: uint32(chain), ContractAddress: address, CreatorAddress: creator, FactoryAddress: factory,
		RuntimeBytecodeHash: runtime, ContractName: text(row, "contract_name"),
		CreationType: NormalizeCreationType(text(row, "creation_type"), factory, isProxy, token)}, count, nil
}

func normalizeProxyType(raw string, isProxy bool) ProxyType {
	if !isProxy {
		return ProxyNone
	}
	switch strings.NewReplacer("_", "-", " ", "-").Replace(strings.ToUpper(strings.TrimSpace(raw))) {
	case "TRANSPARENT", "TRANSPARENT-PROXY":
		return ProxyTransparent
	case "UUPS":
		return ProxyUUPS
	case "BEACON", "BEACON-PROXY":
		return ProxyBeacon
	case "EIP-1167", "MINIMAL", "MINIMAL-PROXY":
		return ProxyMinimal1167
	default:
		return ProxyEIP1967
	}
}
