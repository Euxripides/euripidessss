package sqd

import (
	"context"
	"errors"

	"github.com/etl/backend/internal/chain"
)

func (c *Client) StreamTransactions(
	ctx context.Context,
	network chain.EVM,
	blockRange BlockRange,
	addresses []string,
	handle func(Block) error,
) error {
	request := streamRequest{
		Type:      "evm",
		FromBlock: blockRange.From,
		ToBlock:   blockRange.To,
		Fields: map[string]map[string]bool{
			"block": {"number": true, "timestamp": true},
			"transaction": {
				"hash": true, "transactionIndex": true, "from": true, "to": true,
				"value": true, "input": true, "sighash": true, "status": true,
				"gasUsed": true, "gasPrice": true,
			},
		},
		Transactions: []map[string]any{
			{"from": addresses},
			{"to": addresses},
		},
	}
	return c.stream(ctx, network, request, validateTransactionBlock, handle)
}

func validateTransactionBlock(block Block) error {
	if block.Header.Number == 0 || block.Header.Timestamp == 0 {
		return errors.New("缺少 header.number/header.timestamp")
	}
	for _, item := range block.Transactions {
		if item.Hash == "" || item.From == "" {
			return errors.New("Transaction 缺少 hash/from")
		}
	}
	return nil
}
