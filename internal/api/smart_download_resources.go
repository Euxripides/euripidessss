package api

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/etl/backend/internal/rpcmanager"
	"github.com/etl/backend/internal/smartdownload"
)

// smartDownloadResourceMetrics bridges production disk and RPC control-plane
// state into Smart Download without coupling the scheduler package to either
// implementation. Budget values are explicit operator limits; an unset limit
// remains unknown instead of being interpreted as zero budget.
type smartDownloadResourceMetrics struct {
	root       string
	rpcManager *rpcmanager.Manager
}

func (m *smartDownloadResourceMetrics) SmartDownloadResourceMetrics(_ context.Context, chainKey string) (smartdownload.V32ResourceMetrics, error) {
	result := smartdownload.V32ResourceMetrics{
		DiskReserveBytes:     envUint64("SMART_DOWNLOAD_DISK_RESERVE_BYTES"),
		RPCHardLimit:         envUint64("SMART_DOWNLOAD_RPC_DAILY_HARD_LIMIT"),
		CloudBudgetRemaining: envFloat64("SMART_DOWNLOAD_CLOUD_DAILY_BUDGET"),
		CloudHardLimit:       envFloat64("SMART_DOWNLOAD_CLOUD_MAX_SINGLE_JOB_COST"),
	}
	if result.DiskReserveBytes == 0 {
		result.DiskReserveBytes = 2 << 30
	}

	free, err := smartDownloadDiskFreeBytes(m.root)
	if err != nil {
		return result, fmt.Errorf("读取 Smart Download 数据卷剩余空间: %w", err)
	}
	result.DiskFreeBytes = free

	if m.rpcManager != nil {
		snapshot, snapshotErr := m.rpcManager.PoolSnapshot(strings.TrimSpace(chainKey))
		if snapshotErr != nil {
			return result, fmt.Errorf("读取 RPC Pool 指标: %w", snapshotErr)
		}
		todayRequests := uint64(0)
		if snapshot.TodayRequests > 0 {
			todayRequests = uint64(snapshot.TodayRequests)
		}
		if result.RPCHardLimit > todayRequests {
			result.RPCQuotaRemaining = result.RPCHardLimit - todayRequests
		}
	}
	return result, nil
}

func envUint64(name string) uint64 {
	v, err := strconv.ParseUint(strings.TrimSpace(os.Getenv(name)), 10, 64)
	if err != nil {
		return 0
	}
	return v
}

func envFloat64(name string) float64 {
	v, err := strconv.ParseFloat(strings.TrimSpace(os.Getenv(name)), 64)
	if err != nil || v < 0 {
		return 0
	}
	return v
}
