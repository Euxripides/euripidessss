package api

import "github.com/etl/backend/internal/dunetools"

func snapshotWithSavedAccounts(snapshot dunetools.TaskSnapshot) dunetools.TaskSnapshot {
	allAccountsMu.Lock()
	accs := make([]dunetools.Account, len(allAccounts))
	copy(accs, allAccounts)
	allAccountsMu.Unlock()
	snapshot.Accounts = mergeAccounts(accs, snapshot.Accounts)
	return snapshot
}

func mergeAccounts(saved, batch []dunetools.Account) []dunetools.Account {
	seen := make(map[string]bool)
	var result []dunetools.Account
	for _, a := range saved {
		seen[a.Email] = true
		result = append(result, a)
	}
	for _, a := range batch {
		if !seen[a.Email] {
			result = append(result, a)
		}
	}
	return result
}
