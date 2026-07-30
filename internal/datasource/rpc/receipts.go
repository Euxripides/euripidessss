package rpc

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/etl/backend/internal/chain"
	"github.com/etl/backend/internal/normalize"
)

type Client struct {
	endpoint string
	client   *http.Client
}

type rpcRequest struct {
	JSONRPC string `json:"jsonrpc"`
	ID      int    `json:"id"`
	Method  string `json:"method"`
	Params  []any  `json:"params"`
}

type rpcResponse struct {
	ID     int             `json:"id"`
	Result json.RawMessage `json:"result"`
	Error  *struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	} `json:"error,omitempty"`
}

func New(endpoint string, client *http.Client) (*Client, error) {
	endpoint = strings.TrimSpace(endpoint)
	if endpoint == "" {
		return nil, errors.New("Receipt RPC 地址为空")
	}
	if client == nil {
		client = &http.Client{Timeout: 45 * time.Second}
	}
	return &Client{endpoint: endpoint, client: client}, nil
}

func (c *Client) Probe(ctx context.Context, network chain.EVM, sampleTxHash string) error {
	chainResult, err := c.call(ctx, []rpcRequest{{
		JSONRPC: "2.0", ID: 1, Method: "eth_chainId", Params: []any{},
	}})
	if err != nil {
		return fmt.Errorf("探测 RPC chain_id: %w", err)
	}
	var chainHex string
	if err := json.Unmarshal(chainResult[1], &chainHex); err != nil {
		return fmt.Errorf("RPC chain_id 格式错误: %w", err)
	}
	chainID, err := parseHexUint64(chainHex)
	if err != nil || int64(chainID) != network.ID {
		return fmt.Errorf("RPC 网络不匹配：期望 chain_id=%d，实际=%s", network.ID, chainHex)
	}
	if strings.TrimSpace(sampleTxHash) == "" {
		return nil
	}
	results, err := c.call(ctx, []rpcRequest{{
		JSONRPC: "2.0", ID: 1, Method: "eth_getTransactionReceipt", Params: []any{sampleTxHash},
	}})
	if err != nil {
		return fmt.Errorf("探测 Receipt Schema: %w", err)
	}
	_, err = decodeReceipt(network, results[1])
	if err != nil {
		return fmt.Errorf("Receipt Schema 探测失败: %w", err)
	}
	return nil
}

func (c *Client) Receipts(ctx context.Context, network chain.EVM, txHashes []string) ([]normalize.TransactionReceipt, error) {
	requests := make([]rpcRequest, 0, len(txHashes))
	for index, hash := range txHashes {
		requests = append(requests, rpcRequest{
			JSONRPC: "2.0",
			ID:      index + 1,
			Method:  "eth_getTransactionReceipt",
			Params:  []any{hash},
		})
	}
	results, err := c.call(ctx, requests)
	if err != nil {
		return nil, err
	}
	receipts := make([]normalize.TransactionReceipt, 0, len(txHashes))
	for index := range txHashes {
		item, err := decodeReceipt(network, results[index+1])
		if err != nil {
			return nil, fmt.Errorf("Receipt %s: %w", txHashes[index], err)
		}
		receipts = append(receipts, item)
	}
	return receipts, nil
}

func (c *Client) call(ctx context.Context, requests []rpcRequest) (map[int]json.RawMessage, error) {
	results, responseErrors, err := c.callPartial(ctx, requests)
	if err != nil {
		return nil, err
	}
	for _, request := range requests {
		if responseErr := responseErrors[request.ID]; responseErr != nil {
			return nil, responseErr
		}
	}
	return results, nil
}

func (c *Client) callPartial(ctx context.Context, requests []rpcRequest) (map[int]json.RawMessage, map[int]error, error) {
	if len(requests) == 0 {
		return map[int]json.RawMessage{}, map[int]error{}, nil
	}
	body, err := json.Marshal(requests)
	if err != nil {
		return nil, nil, err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, nil, err
	}
	request.Header.Set("Content-Type", "application/json")
	response, err := c.client.Do(request)
	if err != nil {
		return nil, nil, err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return nil, nil, fmt.Errorf("RPC HTTP %d", response.StatusCode)
	}
	var payload []rpcResponse
	if err := json.NewDecoder(response.Body).Decode(&payload); err != nil {
		return nil, nil, fmt.Errorf("解析 RPC 响应: %w", err)
	}
	result := make(map[int]json.RawMessage, len(payload))
	responseErrors := make(map[int]error)
	for _, item := range payload {
		if item.Error != nil {
			responseErrors[item.ID] = fmt.Errorf("RPC %d: %s", item.Error.Code, item.Error.Message)
			continue
		}
		result[item.ID] = item.Result
	}
	for _, item := range requests {
		if _, ok := result[item.ID]; !ok && responseErrors[item.ID] == nil {
			responseErrors[item.ID] = fmt.Errorf("RPC 响应缺少 id=%d", item.ID)
		}
	}
	return result, responseErrors, nil
}

func decodeReceipt(network chain.EVM, raw json.RawMessage) (normalize.TransactionReceipt, error) {
	if len(raw) == 0 || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return normalize.TransactionReceipt{}, errors.New("交易回执不存在")
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		return normalize.TransactionReceipt{}, err
	}
	required := []string{"transactionHash", "status", "gasUsed", "effectiveGasPrice", "contractAddress", "logs"}
	for _, name := range required {
		if _, ok := fields[name]; !ok {
			return normalize.TransactionReceipt{}, fmt.Errorf("缺少字段 %s", name)
		}
	}
	var txHash, statusHex, gasUsed, effectiveGasPrice string
	var contractAddress *string
	var logs []json.RawMessage
	if err := json.Unmarshal(fields["transactionHash"], &txHash); err != nil {
		return normalize.TransactionReceipt{}, err
	}
	if err := json.Unmarshal(fields["status"], &statusHex); err != nil {
		return normalize.TransactionReceipt{}, err
	}
	if err := json.Unmarshal(fields["gasUsed"], &gasUsed); err != nil {
		return normalize.TransactionReceipt{}, err
	}
	if err := json.Unmarshal(fields["effectiveGasPrice"], &effectiveGasPrice); err != nil {
		return normalize.TransactionReceipt{}, err
	}
	if err := json.Unmarshal(fields["contractAddress"], &contractAddress); err != nil {
		return normalize.TransactionReceipt{}, err
	}
	if err := json.Unmarshal(fields["logs"], &logs); err != nil {
		return normalize.TransactionReceipt{}, err
	}
	status, err := parseHexUint64(statusHex)
	if err != nil {
		return normalize.TransactionReceipt{}, fmt.Errorf("status: %w", err)
	}
	contract := ""
	if contractAddress != nil {
		contract = strings.ToLower(*contractAddress)
	}
	return normalize.TransactionReceipt{
		ChainKey:          network.Key,
		ChainID:           network.ID,
		TxHash:            strings.ToLower(txHash),
		Status:            status,
		GasUsed:           gasUsed,
		EffectiveGasPrice: effectiveGasPrice,
		ContractAddress:   contract,
		LogsCount:         len(logs),
	}, nil
}

func parseHexUint64(value string) (uint64, error) {
	value = strings.TrimSpace(strings.ToLower(value))
	if !strings.HasPrefix(value, "0x") || len(value) < 3 {
		return 0, fmt.Errorf("无效十六进制值 %q", value)
	}
	return strconv.ParseUint(strings.TrimPrefix(value, "0x"), 16, 64)
}
