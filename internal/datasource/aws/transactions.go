package aws

import (
	"context"
	"encoding/xml"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/etl/backend/internal/chain"
	"github.com/etl/backend/internal/datasource"
)

const (
	DefaultEndpoint    = "https://aws-public-blockchain.s3.us-east-2.amazonaws.com"
	bscTransactionRoot = "v1.1/bnb/transactions/"
)

type Adapter struct {
	Client   *http.Client
	Endpoint string
}

type listResult struct {
	IsTruncated           bool   `xml:"IsTruncated"`
	NextContinuationToken string `xml:"NextContinuationToken"`
	Contents              []struct {
		Key          string `xml:"Key"`
		LastModified string `xml:"LastModified"`
		ETag         string `xml:"ETag"`
		Size         int64  `xml:"Size"`
	} `xml:"Contents"`
}

func New(client *http.Client) *Adapter {
	if client == nil {
		client = &http.Client{Timeout: 45 * time.Second}
	}
	return &Adapter{Client: client, Endpoint: DefaultEndpoint}
}

func (a *Adapter) DiscoverTransactions(ctx context.Context, network chain.EVM, startDate, endDate string) ([]datasource.Object, error) {
	if network.Key != "bsc" {
		return nil, fmt.Errorf("AWS 公共 Parquet transactions 当前仅支持 BSC，%s 已完成链适配但尚未配置交易数据源", network.Name)
	}
	start, err := time.Parse("2006-01-02", startDate)
	if err != nil {
		return nil, fmt.Errorf("开始日期格式错误: %w", err)
	}
	end, err := time.Parse("2006-01-02", endDate)
	if err != nil {
		return nil, fmt.Errorf("结束日期格式错误: %w", err)
	}
	if end.Before(start) {
		return nil, fmt.Errorf("结束日期不能早于开始日期")
	}
	if end.Sub(start) > 366*24*time.Hour {
		return nil, fmt.Errorf("单次日期范围不能超过 366 天")
	}
	return a.listRange(ctx, startDate, endDate)
}

func (a *Adapter) listRange(ctx context.Context, startDate, endDate string) ([]datasource.Object, error) {
	var result []datasource.Object
	token := ""
	startAfter := bscTransactionRoot + "date=" + startDate + "/"
	endBoundary := bscTransactionRoot + "date=" + endDate + "/\uffff"
	for {
		query := url.Values{"list-type": {"2"}, "prefix": {bscTransactionRoot}}
		if token != "" {
			query.Set("continuation-token", token)
		} else {
			query.Set("start-after", startAfter)
		}
		request, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(a.Endpoint, "/")+"/?"+query.Encode(), nil)
		if err != nil {
			return nil, err
		}
		response, err := a.Client.Do(request)
		if err != nil {
			return nil, err
		}
		var payload listResult
		decodeErr := xml.NewDecoder(response.Body).Decode(&payload)
		response.Body.Close()
		if response.StatusCode != http.StatusOK {
			return nil, fmt.Errorf("S3 列目录 HTTP %d", response.StatusCode)
		}
		if decodeErr != nil {
			return nil, fmt.Errorf("解析 S3 目录: %w", decodeErr)
		}
		for _, item := range payload.Contents {
			if item.Key > endBoundary {
				return result, nil
			}
			sourceDate := SourceDateFromKey(item.Key)
			if sourceDate < startDate || sourceDate > endDate || !strings.HasSuffix(strings.ToLower(item.Key), ".parquet") {
				continue
			}
			result = append(result, datasource.Object{
				Key:          item.Key,
				URI:          "s3://aws-public-blockchain/" + item.Key,
				DataType:     "transactions",
				SourceDate:   sourceDate,
				SizeBytes:    item.Size,
				ETag:         strings.Trim(item.ETag, `"`),
				LastModified: item.LastModified,
			})
		}
		if !payload.IsTruncated || payload.NextContinuationToken == "" {
			return result, nil
		}
		token = payload.NextContinuationToken
	}
}

func SourceDateFromKey(key string) string {
	index := strings.Index(key, "date=")
	if index < 0 || len(key) < index+15 {
		return ""
	}
	value := key[index+5 : index+15]
	if _, err := time.Parse("2006-01-02", value); err != nil {
		return ""
	}
	return value
}

func HTTPURL(endpoint string, object datasource.Object) string {
	segments := strings.Split(object.Key, "/")
	for index, segment := range segments {
		segments[index] = url.PathEscape(segment)
	}
	return strings.TrimRight(endpoint, "/") + "/" + strings.Join(segments, "/")
}
