package sqd

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/etl/backend/internal/chain"
)

const (
	DefaultPortal       = "https://portal.sqd.dev/datasets"
	TransferTopic       = "0xddf252ad1be2c89b69c2b068fc378daa952ba7f163c4a11628f55a4df523b3ef"
	TransferSingleTopic = "0xc3d58168c5ae7397731d063d5bbf3d657854427343f4c083240f7aacaa2d0f62"
	TransferBatchTopic  = "0x4a39dc06d4c0dbc64b70af90fd698a233a518aa5d07e595d983b8c0526c8f7fb"
)

type Client struct {
	httpClient *http.Client
	portalRoot string
	apiKey     string
}

type Metadata struct {
	Dataset    string `json:"dataset"`
	RealTime   bool   `json:"real_time"`
	StartBlock uint64 `json:"start_block"`
}

type BlockRange struct {
	From uint64 `json:"from_block"`
	To   uint64 `json:"to_block"`
}

type Block struct {
	Header       Header        `json:"header"`
	Transactions []Transaction `json:"transactions,omitempty"`
	Logs         []Log         `json:"logs,omitempty"`
	Traces       []Trace       `json:"traces,omitempty"`
}

type Header struct {
	Number    uint64 `json:"number"`
	Timestamp int64  `json:"timestamp"`
}

type Transaction struct {
	Hash             string `json:"hash"`
	TransactionIndex uint64 `json:"transactionIndex"`
	From             string `json:"from"`
	To               string `json:"to"`
	Value            string `json:"value"`
	Input            string `json:"input"`
	Sighash          string `json:"sighash"`
	Status           uint64 `json:"status"`
	GasUsed          string `json:"gasUsed"`
	GasPrice         string `json:"gasPrice"`
}

type Log struct {
	Address          string   `json:"address"`
	Topics           []string `json:"topics"`
	Data             string   `json:"data"`
	LogIndex         uint64   `json:"logIndex"`
	TransactionIndex uint64   `json:"transactionIndex"`
	TransactionHash  string   `json:"transactionHash"`
}

type Trace struct {
	TransactionIndex uint64       `json:"transactionIndex"`
	TraceAddress     []int        `json:"traceAddress"`
	Type             string       `json:"type"`
	Error            *string      `json:"error"`
	RevertReason     *string      `json:"revertReason"`
	Action           TraceAction  `json:"action"`
	Result           *TraceResult `json:"result"`
}

type TraceAction struct {
	From    string  `json:"from"`
	To      string  `json:"to"`
	Value   *string `json:"value"`
	Gas     string  `json:"gas"`
	Input   string  `json:"input"`
	Sighash string  `json:"sighash"`
}

type TraceResult struct {
	GasUsed string `json:"gasUsed"`
	Output  string `json:"output"`
	Address string `json:"address"`
	Code    string `json:"code"`
}

type streamRequest struct {
	Type         string                     `json:"type"`
	FromBlock    uint64                     `json:"fromBlock"`
	ToBlock      uint64                     `json:"toBlock"`
	Fields       map[string]map[string]bool `json:"fields"`
	Transactions []map[string]any           `json:"transactions,omitempty"`
	Logs         []map[string]any           `json:"logs,omitempty"`
	Traces       []map[string]any           `json:"traces,omitempty"`
}

func New(client *http.Client) *Client {
	return NewConfigured(client, DefaultPortal, "")
}

func NewConfigured(client *http.Client, portalRoot, apiKey string) *Client {
	if client == nil {
		client = &http.Client{Timeout: 90 * time.Second}
	}
	portalRoot = strings.TrimRight(strings.TrimSpace(portalRoot), "/")
	if portalRoot == "" {
		portalRoot = DefaultPortal
	}
	return &Client{httpClient: client, portalRoot: portalRoot, apiKey: strings.TrimSpace(apiKey)}
}

func (c *Client) Metadata(ctx context.Context, network chain.EVM) (Metadata, error) {
	var result Metadata
	if err := c.getJSON(ctx, c.datasetURL(network)+"/metadata", &result); err != nil {
		return result, err
	}
	if result.Dataset != network.SQDDataset {
		return result, fmt.Errorf("SQD dataset 不匹配：期望 %s，实际 %s", network.SQDDataset, result.Dataset)
	}
	return result, nil
}

func (c *Client) ResolveDateRange(ctx context.Context, network chain.EVM, startDate, endDate string) (BlockRange, error) {
	start, err := time.Parse("2006-01-02", startDate)
	if err != nil {
		return BlockRange{}, fmt.Errorf("开始日期格式错误: %w", err)
	}
	end, err := time.Parse("2006-01-02", endDate)
	if err != nil {
		return BlockRange{}, fmt.Errorf("结束日期格式错误: %w", err)
	}
	if end.Before(start) {
		return BlockRange{}, errors.New("结束日期不能早于开始日期")
	}
	from, err := c.timestampBlock(ctx, network, start.Unix())
	if err != nil {
		return BlockRange{}, err
	}
	nextDay, err := c.timestampBlock(ctx, network, end.Add(24*time.Hour).Unix())
	if err != nil {
		return BlockRange{}, err
	}
	if nextDay == 0 || nextDay <= from {
		return BlockRange{}, errors.New("SQD 返回的日期区块范围无效")
	}
	return BlockRange{From: from, To: nextDay - 1}, nil
}

func (c *Client) StreamLogs(
	ctx context.Context,
	network chain.EVM,
	blockRange BlockRange,
	addresses []string,
	handle func(Block) error,
) error {
	padded := padAddresses(addresses)
	filters := make([]map[string]any, 0, 6)
	for _, item := range []struct {
		topic string
		from  string
		to    string
	}{
		{TransferTopic, "topic1", "topic2"},
		{TransferSingleTopic, "topic2", "topic3"},
		{TransferBatchTopic, "topic2", "topic3"},
	} {
		filters = append(filters,
			map[string]any{"topic0": []string{item.topic}, item.from: padded},
			map[string]any{"topic0": []string{item.topic}, item.to: padded},
		)
	}
	request := streamRequest{
		Type:      "evm",
		FromBlock: blockRange.From,
		ToBlock:   blockRange.To,
		Fields: map[string]map[string]bool{
			"block": {"number": true, "timestamp": true},
			"log": {
				"address": true, "topics": true, "data": true, "logIndex": true,
				"transactionIndex": true, "transactionHash": true,
			},
		},
		Logs: filters,
	}
	return c.stream(ctx, network, request, validateLogBlock, handle)
}

func (c *Client) StreamTraces(
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
			"block":       {"number": true, "timestamp": true},
			"transaction": {"hash": true, "transactionIndex": true},
			"trace": {
				"type": true, "transactionIndex": true, "traceAddress": true, "error": true, "revertReason": true,
				"callFrom": true, "callTo": true, "callValue": true, "callGas": true,
				"callSighash": true, "callInput": true, "callResultGasUsed": true,
				"callResultOutput": true, "createFrom": true, "createValue": true,
				"createResultAddress": true, "createResultCode": true,
			},
		},
		Traces: []map[string]any{
			{"callFrom": addresses, "transaction": true},
			{"callTo": addresses, "transaction": true},
		},
	}
	return c.stream(ctx, network, request, validateTraceBlock, handle)
}

func (c *Client) stream(
	ctx context.Context,
	network chain.EVM,
	request streamRequest,
	validate func(Block) error,
	handle func(Block) error,
) error {
	from := request.FromBlock
	probed := false
	for from <= request.ToBlock {
		request.FromBlock = from
		body, err := json.Marshal(request)
		if err != nil {
			return err
		}
		response, err := c.postWithRetry(ctx, c.datasetURL(network)+"/finalized-stream", body)
		if err != nil {
			return err
		}
		if response.StatusCode == http.StatusNoContent {
			response.Body.Close()
			return nil
		}
		var last uint64
		scanner := bufio.NewScanner(response.Body)
		scanner.Buffer(make([]byte, 64*1024), 16*1024*1024)
		for scanner.Scan() {
			var block Block
			if err := json.Unmarshal(scanner.Bytes(), &block); err != nil {
				response.Body.Close()
				return fmt.Errorf("解析 SQD NDJSON: %w", err)
			}
			if !probed {
				if err := validate(block); err != nil {
					response.Body.Close()
					return fmt.Errorf("SQD Schema 探测失败: %w", err)
				}
				probed = true
			}
			if err := handle(block); err != nil {
				response.Body.Close()
				return err
			}
			last = block.Header.Number
		}
		scanErr := scanner.Err()
		response.Body.Close()
		if scanErr != nil {
			return fmt.Errorf("读取 SQD NDJSON: %w", scanErr)
		}
		if last < from {
			return errors.New("SQD 流未返回可推进的区块")
		}
		if last >= request.ToBlock {
			return nil
		}
		from = last + 1
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(550 * time.Millisecond):
		}
	}
	return nil
}

func (c *Client) postWithRetry(ctx context.Context, endpoint string, body []byte) (*http.Response, error) {
	var last error
	for attempt := 0; attempt < 4; attempt++ {
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
		if err != nil {
			return nil, err
		}
		req.Header.Set("Content-Type", "application/json")
		c.authorize(req)
		response, err := c.httpClient.Do(req)
		if err == nil && (response.StatusCode == http.StatusOK || response.StatusCode == http.StatusNoContent) {
			return response, nil
		}
		if err == nil {
			message, _ := io.ReadAll(io.LimitReader(response.Body, 4096))
			response.Body.Close()
			err = fmt.Errorf("SQD HTTP %d: %s", response.StatusCode, strings.TrimSpace(string(message)))
			if response.StatusCode != http.StatusTooManyRequests && response.StatusCode < 500 {
				return nil, err
			}
		}
		last = err
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(time.Duration(1<<attempt) * time.Second):
		}
	}
	return nil, last
}

func (c *Client) timestampBlock(ctx context.Context, network chain.EVM, timestamp int64) (uint64, error) {
	var response struct {
		BlockNumber uint64 `json:"block_number"`
	}
	endpoint := c.datasetURL(network) + "/timestamps/" + strconv.FormatInt(timestamp, 10) + "/block"
	if err := c.getJSON(ctx, endpoint, &response); err != nil {
		return 0, err
	}
	return response.BlockNumber, nil
}

func (c *Client) getJSON(ctx context.Context, endpoint string, target any) error {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return err
	}
	c.authorize(request)
	response, err := c.httpClient.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("SQD HTTP %d", response.StatusCode)
	}
	return json.NewDecoder(response.Body).Decode(target)
}

func (c *Client) authorize(request *http.Request) {
	if c.apiKey != "" {
		request.Header.Set("Authorization", "Bearer "+c.apiKey)
	}
}

func (c *Client) datasetURL(network chain.EVM) string {
	return strings.TrimRight(c.portalRoot, "/") + "/" + network.SQDDataset
}

func padAddresses(addresses []string) []string {
	result := make([]string, 0, len(addresses))
	for _, address := range addresses {
		address = strings.TrimPrefix(strings.ToLower(address), "0x")
		result = append(result, "0x"+strings.Repeat("0", 24)+address)
	}
	return result
}

func validateLogBlock(block Block) error {
	if block.Header.Number == 0 || block.Header.Timestamp == 0 {
		return errors.New("缺少 header.number/header.timestamp")
	}
	for _, item := range block.Logs {
		if item.Address == "" || item.TransactionHash == "" || len(item.Topics) == 0 {
			return errors.New("Log 缺少 address/transactionHash/topics")
		}
	}
	return nil
}

func validateTraceBlock(block Block) error {
	if block.Header.Number == 0 || block.Header.Timestamp == 0 {
		return errors.New("缺少 header.number/header.timestamp")
	}
	for _, item := range block.Traces {
		if item.Type == "" || (item.Action.From == "" && item.Action.To == "") {
			return errors.New("Trace 缺少 type/action")
		}
	}
	return nil
}
