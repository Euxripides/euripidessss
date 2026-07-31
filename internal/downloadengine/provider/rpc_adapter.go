package provider

import (
	"context"
	"fmt"
	"time"

	"github.com/etl/backend/internal/downloadengine"
	"github.com/etl/backend/internal/rpcmanager"
)

// RPCAdapter wraps rpcmanager.Manager to satisfy downloadengine.LookupProvider
type RPCAdapter struct {
	manager *rpcmanager.Manager
}

func NewRPCAdapter(manager *rpcmanager.Manager) *RPCAdapter {
	return &RPCAdapter{manager: manager}
}

func (r *RPCAdapter) Name() string { return "RPC" }
func (r *RPCAdapter) Capabilities() downloadengine.ProviderCapabilities {
	return downloadengine.ProviderCapabilities{
		Name:           "RPC",
		SupportsLookup: true,
		DatasetTypes:   []string{"block_time", "contract_code", "address_type", "balance"},
	}
}
func (r *RPCAdapter) Health(ctx context.Context) downloadengine.ProviderHealth {
	return downloadengine.ProviderHealth{
		Name:      "RPC",
		Status:    downloadengine.ProviderHealthy,
		LastCheck: time.Now(),
	}
}

func (r *RPCAdapter) ExecuteLookup(ctx context.Context, req downloadengine.LookupRequest) (*downloadengine.LookupResult, error) {
	switch req.LookupType {
	case "address_type":
		result, err := r.manager.Address(ctx, req.ChainID, req.Address, false)
		if err != nil {
			return &downloadengine.LookupResult{Found: false}, fmt.Errorf("RPC address_type: %w", err)
		}
		return &downloadengine.LookupResult{
			Found: true,
			Payload: map[string]any{
				"address_type": result.AddressType,
				"reason":       result.Reason,
			},
		}, nil

	case "balance":
		result, err := r.manager.Address(ctx, req.ChainID, req.Address, false)
		if err != nil {
			return &downloadengine.LookupResult{Found: false}, err
		}
		return &downloadengine.LookupResult{
			Found: true,
			Payload: map[string]any{
				"native_balance_raw": result.NativeBalanceRaw,
				"native_balance":     result.NativeBalance,
				"native_symbol":      result.NativeSymbol,
			},
		}, nil

	default:
		return &downloadengine.LookupResult{Found: false},
			fmt.Errorf("RPC: unsupported lookup type: %s", req.LookupType)
	}
}
