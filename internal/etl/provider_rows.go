package etl

import "github.com/etl/backend/internal/model"

func unifiedRowsToTransactions(rows [][]string, columns []string) []model.TransactionRow {
	txns := make([]model.TransactionRow, 0, len(rows))
	for _, row := range rows {
		txn := make(model.TransactionRow)
		for i, cell := range row {
			if i < len(columns) {
				column := columns[i]
				if column == "来源表" {
					column = "数据来源"
				}
				txn[column] = cell
			}
		}
		txns = append(txns, txn)
	}
	return txns
}
