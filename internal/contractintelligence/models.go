package contractintelligence

import (
	"context"
	"errors"
	"time"
)

var (
	ErrInvalidInput = errors.New("invalid contract intelligence input")
	ErrNotFound     = errors.New("contract not found")
	ErrQueryFailed  = errors.New("contract intelligence query failed")
	ErrInvalidData  = errors.New("invalid contract intelligence data")
	ErrEvidenceRead = errors.New("contract evidence unavailable")
)

type CreationType string

const (
	CreationCreate  CreationType = "CREATE"
	CreationCreate2 CreationType = "CREATE2"
	CreationFactory CreationType = "FACTORY"
	CreationProxy   CreationType = "PROXY"
	CreationToken   CreationType = "TOKEN"
)

type ProxyType string

const (
	ProxyNone        ProxyType = ""
	ProxyEIP1967     ProxyType = "EIP-1967"
	ProxyTransparent ProxyType = "TRANSPARENT"
	ProxyUUPS        ProxyType = "UUPS"
	ProxyBeacon      ProxyType = "BEACON"
	ProxyMinimal1167 ProxyType = "EIP-1167"
)

// CanonicalContract is the stable Explorer/investigation representation of a
// deployed contract. It deliberately excludes raw ABI and bytecode payloads.
type CanonicalContract struct {
	ChainID               uint32       `json:"chain_id"`
	ContractAddress       string       `json:"contract_address"`
	CreatorAddress        string       `json:"creator_address,omitempty"`
	FactoryAddress        string       `json:"factory_address,omitempty"`
	CreationTx            string       `json:"creation_tx,omitempty"`
	CreationBlock         uint64       `json:"creation_block,omitempty"`
	CreationTime          time.Time    `json:"creation_time,omitempty"`
	CreationType          CreationType `json:"creation_type"`
	BytecodeHash          string       `json:"bytecode_hash,omitempty"`
	RuntimeBytecodeHash   string       `json:"runtime_bytecode_hash,omitempty"`
	ContractName          string       `json:"contract_name,omitempty"`
	Verified              bool         `json:"verified"`
	IsProxy               bool         `json:"is_proxy"`
	ProxyType             ProxyType    `json:"proxy_type,omitempty"`
	ImplementationAddress string       `json:"implementation_address,omitempty"`
	BeaconAddress         string       `json:"beacon_address,omitempty"`
	ABISource             string       `json:"abi_source,omitempty"`
}

type ContractSummary struct {
	ChainID             uint32       `json:"chain_id"`
	ContractAddress     string       `json:"contract_address"`
	CreatorAddress      string       `json:"creator_address,omitempty"`
	FactoryAddress      string       `json:"factory_address,omitempty"`
	RuntimeBytecodeHash string       `json:"runtime_bytecode_hash,omitempty"`
	ContractName        string       `json:"contract_name,omitempty"`
	CreationType        CreationType `json:"creation_type"`
}

type ContractFamily struct {
	ChainID              uint32            `json:"chain_id"`
	ContractAddress      string            `json:"contract_address"`
	RuntimeBytecodeHash  string            `json:"runtime_bytecode_hash,omitempty"`
	SameRuntimeHashCount uint64            `json:"same_runtime_hash_count"`
	SameRuntimeHash      []ContractSummary `json:"same_runtime_hash"`
	SameCreatorCount     uint64            `json:"same_creator_count"`
	SameCreator          []ContractSummary `json:"same_creator"`
	SameFactoryCount     uint64            `json:"same_factory_count"`
	SameFactory          []ContractSummary `json:"same_factory"`
}

type StorageSlot string

const (
	ImplementationSlot StorageSlot = "0x360894a13ba1a3210667c828492db98dca3e2076cc3735a920a3ca505d382bbc"
	BeaconSlot         StorageSlot = "0xa3f0ad74e5423aebfd80d3ef4346578335a9a72aeaee59ff6cb3582b35133d50"
	AdminSlot          StorageSlot = "0xb53127684a568b3173ae13b9f8a6016e0197a9f7a6e8ee1178d6a717850b5d6103"
)

type ProxyEvidence struct {
	RuntimeCode               []byte
	ImplementationRuntimeCode []byte
	Storage                   map[StorageSlot][32]byte
}

type ProxyDetection struct {
	IsProxy               bool      `json:"is_proxy"`
	ProxyType             ProxyType `json:"proxy_type,omitempty"`
	ImplementationAddress string    `json:"implementation_address,omitempty"`
	BeaconAddress         string    `json:"beacon_address,omitempty"`
}

// EvidenceReader is intentionally small so RPC, archive-node, or captured
// evidence implementations can be injected without coupling this package.
type EvidenceReader interface {
	RuntimeCode(ctx context.Context, chainID uint32, address string) ([]byte, error)
	StorageAt(ctx context.Context, chainID uint32, address string, slot StorageSlot) ([32]byte, error)
}

// BeaconResolver is optional. Evidence readers that can perform the beacon's
// implementation() lookup may expose it in addition to raw storage evidence.
type BeaconResolver interface {
	BeaconImplementation(ctx context.Context, chainID uint32, beaconAddress string) (string, error)
}
