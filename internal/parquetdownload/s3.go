package parquetdownload

import (
	"context"
	"net/http"
	"strconv"

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
	return d.adapter.DiscoverTransactions(ctx, network, startDate, endDate)
}

func sourceDateFromKey(key string) string {
	return aws.SourceDateFromKey(key)
}

func sourceHTTPURL(object SourceObject) string {
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
