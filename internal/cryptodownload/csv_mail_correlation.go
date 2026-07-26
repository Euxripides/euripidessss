package cryptodownload

import (
	"crypto/sha256"
	"fmt"
	"slices"
	"strings"
	"time"
)

type csvMailStatus string

const (
	csvMailRequestNotSent     csvMailStatus = "request_not_sent"
	csvMailNotArrived         csvMailStatus = "mail_not_arrived"
	csvMailNotMatched         csvMailStatus = "mail_not_matched"
	csvMailLinkNotReady       csvMailStatus = "link_not_ready"
	csvMailLoginConfigFailure csvMailStatus = "login_config_failure"
	csvMailReconnecting       csvMailStatus = "reconnecting"
	csvMailMatched            csvMailStatus = "matched"
)

type csvMailSkipReason string

const (
	csvMailSkipOldUID        csvMailSkipReason = "old_uid"
	csvMailSkipSeenLink      csvMailSkipReason = "seen_link"
	csvMailSkipWrongAddress  csvMailSkipReason = "wrong_address"
	csvMailSkipWrongDataset  csvMailSkipReason = "wrong_dataset"
	csvMailSkipLinkNotReady  csvMailSkipReason = "link_not_ready"
	csvMailSkipTimestampSkew csvMailSkipReason = "timestamp_skew"
)

type csvMailLifecycle struct {
	RequestSent bool
	MailArrived bool
	MailMatched bool
	LinkReady   bool
}

func classifyCSVEmailLifecycle(state csvMailLifecycle) csvMailStatus {
	switch {
	case !state.RequestSent:
		return csvMailRequestNotSent
	case !state.MailArrived:
		return csvMailNotArrived
	case !state.MailMatched:
		return csvMailNotMatched
	case !state.LinkReady:
		return csvMailLinkNotReady
	default:
		return csvMailMatched
	}
}

type csvMailRequest struct {
	RequestedAt time.Time
	Kind        string
	Address     string
	RequestSent bool
	SeenLinks   map[string]bool
}

type csvMailCandidate struct {
	UID         uint32
	BaselineUID uint32
	FolderAlias string
	MessageID   string
	ReceivedAt  time.Time
	Links       []string
}

type csvMailCorrelation struct {
	Eligible      bool
	Score         int
	Link          string
	FolderAlias   string
	UIDHash       string
	MessageIDHash string
	MatchReason   string
	SkipReasons   []csvMailSkipReason
	MailMatched   bool
	LinkReady     bool
}

func correlateCSVEmailCandidate(request csvMailRequest, candidate csvMailCandidate) csvMailCorrelation {
	result := csvMailCorrelation{
		FolderAlias:   candidate.FolderAlias,
		UIDHash:       hashCSVIdentifier(fmt.Sprintf("%s:%d", candidate.FolderAlias, candidate.UID)),
		MessageIDHash: hashCSVIdentifier(candidate.MessageID),
	}
	if candidate.UID <= candidate.BaselineUID {
		result.SkipReasons = append(result.SkipReasons, csvMailSkipOldUID)
	} else {
		result.Score += 30
	}
	result.Score += csvMailFolderScore(candidate.FolderAlias)
	if candidate.MessageID != "" {
		result.Score += 5
	}
	if !candidate.ReceivedAt.IsZero() {
		delta := candidate.ReceivedAt.Sub(request.RequestedAt)
		if delta >= -15*time.Minute && delta <= 30*time.Minute {
			result.Score += 10
		} else {
			result.SkipReasons = append(result.SkipReasons, csvMailSkipTimestampSkew)
		}
	}
	wantAddress := strings.ToLower(strings.TrimSpace(request.Address))
	for _, link := range candidate.Links {
		searchText := strings.ToLower(linkSearchText(link))
		addressMatches := wantAddress != "" && strings.Contains(searchText, wantAddress)
		kind := csvClassifyLink(link)
		if !addressMatches {
			result.SkipReasons = append(result.SkipReasons, csvMailSkipWrongAddress)
			continue
		}
		result.MailMatched = true
		result.Score += 40
		if kind == "" {
			result.SkipReasons = append(result.SkipReasons, csvMailSkipLinkNotReady)
			continue
		}
		result.LinkReady = true
		result.Score += 20
		if kind != request.Kind {
			result.SkipReasons = append(result.SkipReasons, csvMailSkipWrongDataset)
			continue
		}
		result.Score += 20
		if request.SeenLinks[link] {
			result.SkipReasons = append(result.SkipReasons, csvMailSkipSeenLink)
			continue
		}
		if candidate.UID > candidate.BaselineUID {
			result.Eligible = true
			result.Link = link
			result.MatchReason = "uid_address_dataset_ready_link"
			break
		}
	}
	result.SkipReasons = uniqueCSVSkipReasons(result.SkipReasons)
	if result.MatchReason == "" {
		result.MatchReason = rejectedCSVMatchReason(result.SkipReasons)
	}
	return result
}

func rejectedCSVMatchReason(reasons []csvMailSkipReason) string {
	if len(reasons) == 0 {
		return "no_candidate_link"
	}
	parts := make([]string, len(reasons))
	for index, reason := range reasons {
		parts[index] = string(reason)
	}
	return "rejected:" + strings.Join(parts, ",")
}

func csvMailFolderScore(alias string) int {
	switch alias {
	case "inbox":
		return 10
	case "spam", "junk":
		return 5
	case "all_mail":
		return 3
	default:
		return 0
	}
}

func hashCSVIdentifier(value string) string {
	if value == "" {
		return ""
	}
	digest := sha256.Sum256([]byte(value))
	return fmt.Sprintf("%x", digest[:8])
}

func uniqueCSVSkipReasons(reasons []csvMailSkipReason) []csvMailSkipReason {
	seen := make(map[csvMailSkipReason]bool, len(reasons))
	result := make([]csvMailSkipReason, 0, len(reasons))
	for _, reason := range reasons {
		if !seen[reason] {
			seen[reason] = true
			result = append(result, reason)
		}
	}
	slices.Sort(result)
	return result
}

func hasCSVSkipReason(reasons []csvMailSkipReason, want csvMailSkipReason) bool {
	return slices.Contains(reasons, want)
}
