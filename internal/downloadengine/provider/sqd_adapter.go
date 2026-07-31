package provider

import (
	"context"
	"fmt"
	"time"

	"github.com/etl/backend/internal/chain"
	"github.com/etl/backend/internal/datasource/sqd"
	"github.com/etl/backend/internal/downloadengine"
)

// SQDAdapter wraps existing sqd.Client to satisfy downloadengine.StreamingProvider.
// Converts sqd.Client's callback-based streaming API to channel-based.
type SQDAdapter struct {
	client *sqd.Client
	caps   downloadengine.ProviderCapabilities
}

func NewSQDAdapter(client *sqd.Client) *SQDAdapter {
	return &SQDAdapter{
		client: client,
		caps: downloadengine.ProviderCapabilities{
			Name:              "SQD",
			SupportsStreaming: true,
			DatasetTypes:      []string{"transactions", "logs", "traces"},
			MaxBlockRange:     100000,
			SupportsResume:    true,
		},
	}
}

func (s *SQDAdapter) Name() string                                      { return "SQD" }
func (s *SQDAdapter) Capabilities() downloadengine.ProviderCapabilities   { return s.caps }

func (s *SQDAdapter) Health(ctx context.Context) downloadengine.ProviderHealth {
	status := downloadengine.ProviderHealthy
	if s.client.IsInCooldown() {
		status = downloadengine.ProviderDegraded
	}
	if s.client.Consecutive503() > 3 {
		status = downloadengine.ProviderNoWorker
	}
	if s.client.Breaker().State() == sqd.CircuitOpen {
		status = downloadengine.ProviderUnavailable
	}
	return downloadengine.ProviderHealth{
		Name:      "SQD",
		Status:    status,
		LastCheck: time.Now(),
	}
}

func (s *SQDAdapter) Estimate(ctx context.Context, req downloadengine.StreamEstimateRequest) (*downloadengine.EstimateResult, error) {
	network, err := chain.Resolve(req.ChainID)
	if err != nil {
		return nil, fmt.Errorf("SQD estimate: %w", err)
	}
	metadata, err := s.client.Metadata(ctx, network)
	if err != nil {
		return &downloadengine.EstimateResult{SupportsRequest: false}, fmt.Errorf("SQD estimate metadata: %w", err)
	}
	chunks := int((req.EndBlock-req.StartBlock+50000-1)/50000)
	if chunks < 1 {
		chunks = 1
	}
	_ = metadata.StartBlock // available for range validation
	return &downloadengine.EstimateResult{
		EstimatedChunks: chunks,
		SupportsRequest: true,
	}, nil
}

func (s *SQDAdapter) ExecuteStream(ctx context.Context, req downloadengine.StreamRequest) (<-chan downloadengine.StreamRecord, <-chan error) {
	records := make(chan downloadengine.StreamRecord, 100)
	errs := make(chan error, 1)

	go func() {
		defer close(records)
		defer close(errs)

		network, networkErr := chain.Resolve(req.ChainID)
		if networkErr != nil {
			errs <- fmt.Errorf("SQD ExecuteStream: chain resolve: %w", networkErr)
			return
		}

		blockRange := sqd.BlockRange{From: req.StartBlock, To: req.EndBlock}
		addresses := make([]string, 0, len(req.Addresses))
		for _, a := range req.Addresses {
			if a != "" {
				addresses = append(addresses, a)
			}
		}

		switch req.DatasetType {
		case "logs":
			err := s.client.StreamLogs(ctx, network, blockRange, addresses, func(block sqd.Block) error {
				select {
				case records <- downloadengine.StreamRecord{Data: sqdBlockToMap(block)}:
				case <-ctx.Done():
					return ctx.Err()
				}
				return nil
			})
			if err != nil && ctx.Err() == nil {
				errs <- err
			}

		case "traces":
			err := s.client.StreamTraces(ctx, network, blockRange, addresses, func(block sqd.Block) error {
				select {
				case records <- downloadengine.StreamRecord{Data: sqdBlockToMap(block)}:
				case <-ctx.Done():
					return ctx.Err()
				}
				return nil
			})
			if err != nil && ctx.Err() == nil {
				errs <- err
			}

		default:
			errs <- fmt.Errorf("SQD: unsupported dataset type: %s (expected logs or traces)", req.DatasetType)
		}
	}()

	return records, errs
}

func sqdBlockToMap(block sqd.Block) map[string]any {
	m := map[string]any{
		"number":    block.Header.Number,
		"timestamp": block.Header.Timestamp,
	}
	if len(block.Transactions) > 0 {
		txs := make([]map[string]any, len(block.Transactions))
		for i, tx := range block.Transactions {
			txs[i] = map[string]any{
				"hash":   tx.Hash,
				"from":   tx.From,
				"to":     tx.To,
				"value":  tx.Value,
				"status": tx.Status,
			}
		}
		m["transactions"] = txs
	}
	if len(block.Logs) > 0 {
		logs := make([]map[string]any, len(block.Logs))
		for i, l := range block.Logs {
			logs[i] = map[string]any{
				"address":  l.Address,
				"topics":   l.Topics,
				"tx_hash":  l.TransactionHash,
			}
		}
		m["logs"] = logs
	}
	if len(block.Traces) > 0 {
		traces := make([]map[string]any, len(block.Traces))
		for i, tr := range block.Traces {
			traces[i] = map[string]any{
				"type":   tr.Type,
				"from":   tr.Action.From,
				"to":     tr.Action.To,
			}
		}
		m["traces"] = traces
	}
	return m
}
