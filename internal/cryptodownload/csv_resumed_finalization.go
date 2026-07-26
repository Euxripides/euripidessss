package cryptodownload

import (
	"sort"
	"strings"
)

func finalizeResumedRawCSV(data ExportData, address, chain string) ExportData {
	transactionKind := csvExportKind{Name: "transactions", Sheet: "transaction"}
	tokenKind := csvExportKind{Name: "token_transfers", Sheet: "token"}
	data.RawTxHeaders, data.RawTransactions = finalizeResumedRawRecords(
		data.RawTxHeaders,
		data.RawTransactions,
		address,
		chain,
		transactionKind,
	)
	data.RawTokenHeaders, data.RawTokenTransfers = finalizeResumedRawRecords(
		data.RawTokenHeaders,
		data.RawTokenTransfers,
		address,
		chain,
		tokenKind,
	)
	return data
}

func finalizeResumedRawRecords(headers []string, records []map[string]string, address, chain string, kind csvExportKind) ([]string, []map[string]string) {
	seenHeaders := make(map[string]bool, len(headers))
	mergedHeaders := make([]string, 0, len(headers))
	for _, header := range headers {
		header = strings.TrimSpace(header)
		if header == "" || seenHeaders[header] {
			continue
		}
		seenHeaders[header] = true
		mergedHeaders = append(mergedHeaders, header)
	}
	var extraHeaders []string
	for _, record := range records {
		for header := range record {
			if !seenHeaders[header] {
				seenHeaders[header] = true
				extraHeaders = append(extraHeaders, header)
			}
		}
	}
	sort.Strings(extraHeaders)
	mergedHeaders = append(mergedHeaders, extraHeaders...)

	seenRows := make(map[string]bool, len(records))
	unique := make([]map[string]string, 0, len(records))
	for _, record := range records {
		row := csvRecordToExportRow(address, chain, kind, record)
		key := csvRecordDedupeKey(kind, row, record)
		if seenRows[key] {
			continue
		}
		seenRows[key] = true
		unique = append(unique, record)
	}
	return mergedHeaders, unique
}
