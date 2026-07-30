package parquetdownload

import (
	"context"
	"encoding/xml"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

const (
	awsBucketEndpoint  = "https://aws-public-blockchain.s3.us-east-2.amazonaws.com"
	bscTransactionRoot = "v1.1/bnb/transactions/"
)

type s3ListResult struct {
	IsTruncated           bool   `xml:"IsTruncated"`
	NextContinuationToken string `xml:"NextContinuationToken"`
	Contents              []struct {
		Key          string `xml:"Key"`
		LastModified string `xml:"LastModified"`
		ETag         string `xml:"ETag"`
		Size         int64  `xml:"Size"`
	} `xml:"Contents"`
}

type discoverer struct {
	client   *http.Client
	endpoint string
}

func newDiscoverer(client *http.Client) *discoverer {
	if client == nil {
		client = &http.Client{Timeout: 45 * time.Second}
	}
	return &discoverer{client: client, endpoint: awsBucketEndpoint}
}

func (d *discoverer) discover(ctx context.Context, chainKey, startDate, endDate string) ([]SourceObject, error) {
	if strings.ToLower(chainKey) != "bsc" {
		return nil, fmt.Errorf("当前 AWS Parquet 首发源仅启用 BSC，收到 chain_key=%s", chainKey)
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

	return d.listTransactionRange(ctx, start.Format("2006-01-02"), end.Format("2006-01-02"))
}

func (d *discoverer) listTransactionRange(ctx context.Context, startDate, endDate string) ([]SourceObject, error) {
	var result []SourceObject
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
		requestURL := strings.TrimRight(d.endpoint, "/") + "/?" + query.Encode()
		request, err := http.NewRequestWithContext(ctx, http.MethodGet, requestURL, nil)
		if err != nil {
			return nil, err
		}
		response, err := d.client.Do(request)
		if err != nil {
			return nil, err
		}
		var payload s3ListResult
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
			sourceDate := sourceDateFromKey(item.Key)
			if sourceDate < startDate || sourceDate > endDate || !strings.HasSuffix(strings.ToLower(item.Key), ".parquet") {
				continue
			}
			result = append(result, SourceObject{
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
			break
		}
		token = payload.NextContinuationToken
	}
	return result, nil
}

func sourceDateFromKey(key string) string {
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

func sourceHTTPURL(object SourceObject) string {
	segments := strings.Split(object.Key, "/")
	for index, segment := range segments {
		segments[index] = url.PathEscape(segment)
	}
	return awsBucketEndpoint + "/" + strings.Join(segments, "/")
}

func totalSourceBytes(files []SourceObject) int64 {
	var total int64
	for _, file := range files {
		total += file.SizeBytes
	}
	return total
}

func sourceObjectID(object SourceObject) string {
	return object.SourceDate + "-" + object.DataType + "-" + strconv.FormatInt(object.SizeBytes, 10)
}
