package cryptodownload

import (
	"context"
	"strings"

	"github.com/emersion/go-imap"
)

type csvMailFolderSkipReason string

const (
	csvMailFolderNotListed     csvMailFolderSkipReason = "configured_folder_not_listed"
	csvMailFolderNotSelectable csvMailFolderSkipReason = "configured_folder_not_selectable"
)

type csvMailFolderSkip struct {
	Alias  string
	Reason csvMailFolderSkipReason
}

func csvMailFolderCandidates(raw string) []string {
	parts := strings.FieldsFunc(raw, func(r rune) bool { return r == ',' || r == ';' || r == '\n' })
	result := make([]string, 0, len(parts))
	seen := make(map[string]bool, len(parts))
	for _, part := range parts {
		name := strings.TrimSpace(part)
		key := strings.ToLower(name)
		if name != "" && !seen[key] && csvMailConfiguredFolderAlias(name) != "" {
			seen[key] = true
			result = append(result, name)
		}
	}
	return result
}

func (w *csvMailWatcher) discoverFolders(ctx context.Context) ([]csvMailFolder, error) {
	infos, err := w.listFolders(ctx)
	if err != nil {
		return nil, err
	}
	w.folderSkips = nil
	folders := []csvMailFolder{{Name: "INBOX", Alias: "inbox"}}
	seen := map[string]bool{"inbox": true}
	for _, configured := range w.config.FolderCandidates {
		alias := csvMailConfiguredFolderAlias(configured)
		if alias == "inbox" {
			continue
		}
		info, reason := resolveCSVConfiguredFolder(configured, alias, infos)
		if info == nil {
			w.folderSkips = append(w.folderSkips, csvMailFolderSkip{Alias: alias, Reason: reason})
			continue
		}
		addCSVFolder(&folders, seen, info.Name, alias)
	}
	for _, info := range infos {
		addCSVFolder(&folders, seen, info.Name, csvMailFolderAlias(info))
	}
	return folders, nil
}

func (w *csvMailWatcher) listFolders(ctx context.Context) ([]*imap.MailboxInfo, error) {
	mailboxes := make(chan *imap.MailboxInfo)
	done := make(chan error, 1)
	go func() { done <- w.client.List("", "*", mailboxes) }()
	infos := make([]*imap.MailboxInfo, 0)
	for {
		select {
		case info, ok := <-mailboxes:
			if !ok {
				if err := <-done; err != nil {
					return nil, err
				}
				return infos, nil
			}
			infos = append(infos, info)
		case <-ctx.Done():
			w.disconnect()
			<-done
			return nil, ctx.Err()
		}
	}
}

func resolveCSVConfiguredFolder(name, alias string, infos []*imap.MailboxInfo) (*imap.MailboxInfo, csvMailFolderSkipReason) {
	for _, info := range infos {
		if strings.EqualFold(strings.TrimSpace(info.Name), strings.TrimSpace(name)) {
			if !csvMailFolderSelectable(info) {
				return nil, csvMailFolderNotSelectable
			}
			return info, ""
		}
	}
	foundUnsupported := false
	for _, info := range infos {
		if csvMailConfiguredFolderAlias(info.Name) != alias {
			continue
		}
		if csvMailFolderSelectable(info) {
			return info, ""
		}
		foundUnsupported = true
	}
	if foundUnsupported {
		return nil, csvMailFolderNotSelectable
	}
	return nil, csvMailFolderNotListed
}

func addCSVFolder(folders *[]csvMailFolder, seen map[string]bool, name, alias string) {
	key := strings.ToLower(strings.TrimSpace(name))
	if key == "" || alias == "" || seen[key] {
		return
	}
	seen[key] = true
	*folders = append(*folders, csvMailFolder{Name: strings.TrimSpace(name), Alias: alias})
}

func csvMailFolderAlias(info *imap.MailboxInfo) string {
	if !csvMailFolderSelectable(info) {
		return ""
	}
	for _, attribute := range info.Attributes {
		switch strings.ToLower(attribute) {
		case `\junk`:
			return "junk"
		case `\all`:
			return "all_mail"
		}
	}
	return csvMailConfiguredFolderAlias(info.Name)
}

func csvMailFolderSelectable(info *imap.MailboxInfo) bool {
	for _, attribute := range info.Attributes {
		if strings.EqualFold(attribute, `\Noselect`) {
			return false
		}
	}
	return true
}

func csvMailConfiguredFolderAlias(name string) string {
	lower := strings.ToLower(strings.TrimSpace(name))
	switch {
	case strings.EqualFold(name, "INBOX"):
		return "inbox"
	case strings.Contains(lower, "spam"):
		return "spam"
	case strings.Contains(lower, "junk"):
		return "junk"
	case strings.Contains(lower, "all mail") || strings.Contains(lower, "all_mail"):
		return "all_mail"
	default:
		return ""
	}
}
