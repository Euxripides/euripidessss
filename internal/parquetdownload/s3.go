package parquetdownload

import (
	"context"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/etl/backend/internal/chain"
	"github.com/etl/backend/internal/datasource/aws"
)

const awsBucketEndpoint = aws.DefaultEndpoint

type discoverer struct {
	adapter  *aws.Adapter
	endpoint string
}

func newDiscoverer(client *http.Client) *discoverer {
	return &discoverer{adapter: aws.New(client), endpoint: aws.DefaultEndpoint}
}

func (d *discoverer) discover(ctx context.Context, chainKey, startDate, endDate string) ([]SourceObject, error) {
	network, err := chain.Resolve(chainKey)
	if err != nil {
		return nil, err
	}
	d.adapter.Endpoint = d.endpoint
	// AWS discover 单次限制 366 天：超范围自动按 366 天切片，合并分区列表（2020-01-01 起的大跨度任务必需）
	var items []SourceObject
	if start, e := time.Parse("2006-01-02", startDate); e == nil {
		if end, e2 := time.Parse("2006-01-02", endDate); e2 == nil && end.Sub(start) > 366*24*time.Hour {
			segStart := start
			for segStart.Before(end) {
				segEnd := segStart.Add(365 * 24 * time.Hour)
				if segEnd.After(end) {
					segEnd = end
				}
				seg, err := d.adapter.DiscoverTransactions(ctx, network, segStart.Format("2006-01-02"), segEnd.Format("2006-01-02"))
				if err != nil {
					return nil, err
				}
				items = append(items, seg...)
				segStart = segEnd.Add(24 * time.Hour)
			}
		}
	}
	if items == nil {
		items, err = d.adapter.DiscoverTransactions(ctx, network, startDate, endDate)
		if err != nil {
			return nil, err
		}
	}
	for index := range items {
		items[index].URI = aws.HTTPURL(d.endpoint, items[index])
	}
	return items, nil
}

func sourceDateFromKey(key string) string {
	return aws.SourceDateFromKey(key)
}

func sourceHTTPURL(object SourceObject) string {
	if strings.HasPrefix(object.URI, "http://") || strings.HasPrefix(object.URI, "https://") {
		return object.URI
	}
	return aws.HTTPURL(derefEndpoint(), object)
}

func derefEndpoint() string {
	return aws.DefaultEndpoint
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
