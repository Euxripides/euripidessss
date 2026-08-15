package api

import (
	"context"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"

	"github.com/etl/backend/internal/chain"
	"github.com/etl/backend/internal/smartdownload"
	"github.com/etl/backend/internal/storage/control"
	"github.com/gin-gonic/gin"
)

const maxAddressLibraryImport = 50_000

type addressLibraryItem struct {
	control.AddressAsset
	Availability smartdownload.AddressAvailability `json:"download"`
	ActivityRows int64                             `json:"activity_rows"`
	State        string                            `json:"state"`
}

type addressLibraryImportRequest struct {
	ChainKey   string   `json:"chain_key"`
	Addresses  []string `json:"addresses"`
	Source     string   `json:"source,omitempty"`
	SourceName string   `json:"source_name,omitempty"`
}

func registerAddressLibraryRoutes(api *gin.RouterGroup) {
	api.GET("/address-library", handleAddressLibraryList)
	api.POST("/address-library/import", handleAddressLibraryImport)
}

func normalizeAddressLibraryInput(values []string) (valid []string, invalid []string, duplicates int) {
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		address := strings.ToLower(strings.TrimSpace(value))
		if !explorerSearchAddress.MatchString(address) {
			invalid = append(invalid, value)
			continue
		}
		if _, ok := seen[address]; ok {
			duplicates++
			continue
		}
		seen[address] = struct{}{}
		valid = append(valid, address)
	}
	return valid, invalid, duplicates
}

func persistAddressLibrary(_ context.Context, chainKey, sourceName string, addresses []string) (int, error) {
	if controlStore == nil {
		return 0, fmt.Errorf("control store unavailable")
	}
	if len(addresses) > maxAddressLibraryImport {
		return 0, fmt.Errorf("address import exceeds %d items", maxAddressLibraryImport)
	}
	network, err := chain.Resolve(chainKey)
	if err != nil {
		return 0, err
	}
	valid, invalid, _ := normalizeAddressLibraryInput(addresses)
	if len(invalid) > 0 || len(valid) != len(addresses) {
		return 0, fmt.Errorf("address importer received unvalidated addresses")
	}
	return controlStore.UpsertAddressAssets(network.Key, network.ID, valid, "file", sourceName)
}

func backfillAddressLibrary(service *smartdownload.Service) error {
	if controlStore == nil || service == nil {
		return nil
	}
	groups := map[string][]string{}
	chainIDs := map[string]int64{}
	for _, item := range service.KnownAddresses() {
		groups[item.ChainKey] = append(groups[item.ChainKey], item.Address)
		chainIDs[item.ChainKey] = item.ChainID
	}
	for chainKey, addresses := range groups {
		if _, err := controlStore.EnsureAddressAssets(chainKey, chainIDs[chainKey], addresses, "smart-download-history"); err != nil {
			return err
		}
	}
	return nil
}

func handleAddressLibraryImport(c *gin.Context) {
	if controlStore == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"detail": "地址资产库不可用"})
		return
	}
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 8<<20)
	var request addressLibraryImportRequest
	if err := c.ShouldBindJSON(&request); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"detail": "请求格式错误: " + err.Error()})
		return
	}
	if len(request.Addresses) == 0 || len(request.Addresses) > maxAddressLibraryImport {
		c.JSON(http.StatusBadRequest, gin.H{"detail": fmt.Sprintf("地址数量必须为 1..%d", maxAddressLibraryImport)})
		return
	}
	network, err := chain.Resolve(request.ChainKey)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"detail": err.Error()})
		return
	}
	valid, invalid, duplicates := normalizeAddressLibraryInput(request.Addresses)
	if len(valid) == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"detail": "没有有效 EVM 地址", "invalid": len(invalid)})
		return
	}
	source := strings.TrimSpace(request.Source)
	if source == "" {
		source = "manual"
	}
	upserted, err := controlStore.UpsertAddressAssets(network.Key, network.ID, valid, source, request.SourceName)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"detail": "保存地址资产失败: " + err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"persisted": upserted, "valid": len(valid), "invalid": invalid, "duplicates": duplicates, "chain_key": network.Key})
}

func handleAddressLibraryList(c *gin.Context) {
	if controlStore == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"detail": "地址资产库不可用"})
		return
	}
	chainKey := strings.ToLower(strings.TrimSpace(c.Query("chain_key")))
	if chainKey != "" {
		if _, err := chain.Resolve(chainKey); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"detail": err.Error()})
			return
		}
	}
	query := strings.TrimSpace(c.Query("q"))
	if len(query) > 128 {
		c.JSON(http.StatusBadRequest, gin.H{"detail": "查询内容过长"})
		return
	}
	limit := 50
	if parsed, err := strconv.Atoi(c.Query("limit")); err == nil && parsed > 0 {
		limit = parsed
	}
	if limit > 5000 {
		limit = 5000
	}
	offset := 0
	if parsed, err := strconv.Atoi(c.Query("offset")); err == nil && parsed >= 0 {
		offset = parsed
	}
	assets, total, err := controlStore.ListAddressAssets(chainKey, query, limit, offset)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"detail": "读取地址资产失败: " + err.Error()})
		return
	}
	items := make([]addressLibraryItem, len(assets))
	for i, asset := range assets {
		items[i] = addressLibraryItem{AddressAsset: asset, State: "IMPORTED"}
		if smartDownloadService != nil {
			items[i].Availability = smartDownloadService.AddressAvailability(asset.ChainKey, asset.Address)
		}
		if items[i].Availability.RunningJobs > 0 {
			items[i].State = "DOWNLOADING"
		} else if items[i].Availability.CertifiedSets > 0 {
			items[i].State = "CERTIFIED"
		} else if items[i].Availability.PartialJobs > 0 {
			items[i].State = "PARTIAL"
		} else if items[i].Availability.FailedJobs > 0 {
			items[i].State = "FAILED"
		}
	}
	if c.Query("include_status") != "false" && len(items) <= 200 {
		enrichAddressLibraryActivity(c, items)
	}
	c.JSON(http.StatusOK, gin.H{"items": items, "total": total, "limit": limit, "offset": offset})
}

func enrichAddressLibraryActivity(c *gin.Context, items []addressLibraryItem) {
	if clickHouseClient == nil || len(items) == 0 {
		return
	}
	byChain := map[int64][]string{}
	for _, item := range items {
		byChain[item.ChainID] = append(byChain[item.ChainID], item.Address)
	}
	rowsByKey := map[string]int64{}
	for chainID, addresses := range byChain {
		sort.Strings(addresses)
		quoted := make([]string, 0, len(addresses))
		for _, address := range addresses {
			if explorerSearchAddress.MatchString(address) {
				quoted = append(quoted, "'"+address+"'")
			}
		}
		if len(quoted) == 0 {
			continue
		}
		query := fmt.Sprintf(`SELECT address,count() rows FROM onchain.address_activity FINAL WHERE chain_id=%d AND address IN (%s) GROUP BY address`, chainID, strings.Join(quoted, ","))
		rows, err := clickHouseClient.QueryJSON(c.Request.Context(), query)
		if err != nil {
			continue
		}
		for _, row := range rows {
			address := strings.ToLower(fmt.Sprint(row["address"]))
			count, _ := strconv.ParseInt(fmt.Sprint(row["rows"]), 10, 64)
			rowsByKey[fmt.Sprintf("%d:%s", chainID, address)] = count
		}
	}
	for i := range items {
		items[i].ActivityRows = rowsByKey[fmt.Sprintf("%d:%s", items[i].ChainID, items[i].Address)]
		if items[i].ActivityRows > 0 {
			items[i].State = "AVAILABLE"
		}
	}
}
