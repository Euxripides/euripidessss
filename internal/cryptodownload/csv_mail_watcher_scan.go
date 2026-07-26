package cryptodownload

import (
	"context"
	"errors"
	"fmt"
	"io"
	"sort"
	"time"

	"github.com/emersion/go-imap"
)

type csvMailObservation struct {
	Status        csvMailStatus
	Link          string
	FolderAlias   string
	UIDHash       string
	MessageIDHash string
	MatchReason   string
	SkipReasons   []csvMailSkipReason
}

func (w *csvMailWatcher) Scan(ctx context.Context, request csvMailRequest) (csvMailObservation, error) {
	if !request.RequestSent {
		return csvMailObservation{Status: csvMailRequestNotSent}, nil
	}
	var lastErr error
	for attempt := range 3 {
		if err := w.connect(ctx); err != nil {
			var mailErr *csvMailError
			if errors.As(err, &mailErr) && mailErr.Status == csvMailLoginConfigFailure {
				return csvMailObservation{Status: csvMailLoginConfigFailure}, err
			}
			lastErr = err
		} else {
			observation, err := w.scanConnected(ctx, request)
			if err == nil {
				return observation, nil
			}
			lastErr = err
			w.disconnect()
		}
		if attempt < 2 {
			if err := w.wait(ctx, csvMailReconnectDelay(attempt)); err != nil {
				return csvMailObservation{Status: csvMailReconnecting}, err
			}
		}
	}
	return csvMailObservation{Status: csvMailReconnecting}, &csvMailError{Status: csvMailReconnecting, Op: "scan", Err: lastErr}
}

func (w *csvMailWatcher) scanConnected(ctx context.Context, request csvMailRequest) (csvMailObservation, error) {
	if len(w.folders) == 0 {
		return csvMailObservation{Status: csvMailRequestNotSent}, &csvMailError{Status: csvMailRequestNotSent, Op: "baseline", Err: fmt.Errorf("UID baseline not captured")}
	}
	all := make([]csvMailCorrelation, 0)
	lifecycle := csvMailLifecycle{RequestSent: true}
	pendingBaselines := make(map[string]uint32, len(w.folders))
	for _, folder := range w.folders {
		candidates, latestUID, err := w.scanFolder(ctx, folder)
		if err != nil {
			return csvMailObservation{}, fmt.Errorf("scan %s: %w", folder.Alias, err)
		}
		pendingBaselines[folder.Name] = latestUID
		if len(candidates) > 0 {
			lifecycle.MailArrived = true
		}
		for _, candidate := range candidates {
			correlation := correlateCSVEmailCandidate(request, candidate)
			stale := hasCSVSkipReason(correlation.SkipReasons, csvMailSkipOldUID) || hasCSVSkipReason(correlation.SkipReasons, csvMailSkipSeenLink)
			wrongDataset := hasCSVSkipReason(correlation.SkipReasons, csvMailSkipWrongDataset)
			if !stale && !wrongDataset {
				lifecycle.MailMatched = lifecycle.MailMatched || correlation.MailMatched
				lifecycle.LinkReady = lifecycle.LinkReady || correlation.LinkReady
			}
			all = append(all, correlation)
		}
	}
	for folder, latestUID := range pendingBaselines {
		if latestUID > w.baselines[folder] {
			w.baselines[folder] = latestUID
		}
	}
	sort.SliceStable(all, func(i, j int) bool { return all[i].Score > all[j].Score })
	for _, correlation := range all {
		if correlation.Eligible {
			return observationFromCorrelation(csvMailMatched, correlation), nil
		}
	}
	status := classifyCSVEmailLifecycle(lifecycle)
	if len(all) == 0 {
		return csvMailObservation{Status: status}, nil
	}
	return observationFromCorrelation(status, all[0]), nil
}

func (w *csvMailWatcher) scanFolder(ctx context.Context, folder csvMailFolder) ([]csvMailCandidate, uint32, error) {
	if err := w.command(ctx, func() error {
		_, selectErr := w.client.Select(folder.Name, true)
		return selectErr
	}); err != nil {
		return nil, 0, err
	}
	baseline := w.baselines[folder.Name]
	uids, err := w.searchNewUIDs(ctx, baseline)
	if err != nil || len(uids) == 0 {
		return nil, baseline, err
	}
	candidates, err := w.fetchCandidates(ctx, folder, baseline, uids)
	if err != nil {
		return nil, baseline, err
	}
	return candidates, maxCSVUID(uids), nil
}

func (w *csvMailWatcher) searchNewUIDs(ctx context.Context, baseline uint32) ([]uint32, error) {
	criteria := csvNewUIDSearchCriteria(baseline)
	var uids []uint32
	err := w.command(ctx, func() error {
		var searchErr error
		uids, searchErr = w.client.UidSearch(criteria)
		return searchErr
	})
	return uids, err
}

func csvNewUIDSearchCriteria(baseline uint32) *imap.SearchCriteria {
	criteria := imap.NewSearchCriteria()
	criteria.Uid = new(imap.SeqSet)
	criteria.Uid.AddRange(baseline+1, 0)
	return criteria
}

func (w *csvMailWatcher) fetchCandidates(ctx context.Context, folder csvMailFolder, baseline uint32, uids []uint32) ([]csvMailCandidate, error) {
	set := new(imap.SeqSet)
	set.AddNum(uids...)
	section := &imap.BodySectionName{Peek: true}
	items := []imap.FetchItem{imap.FetchUid, imap.FetchEnvelope, section.FetchItem()}
	messages := make(chan *imap.Message)
	done := make(chan error, 1)
	go func() { done <- w.client.UidFetch(set, items, messages) }()
	candidates := make([]csvMailCandidate, 0, len(uids))
	for {
		select {
		case message, ok := <-messages:
			if !ok {
				return candidates, <-done
			}
			body := message.GetBody(section)
			if body == nil {
				continue
			}
			raw, err := io.ReadAll(body)
			if err != nil {
				return nil, fmt.Errorf("read message: %w", err)
			}
			candidate := csvMailCandidate{UID: message.Uid, BaselineUID: baseline, FolderAlias: folder.Alias, Links: extractLinks(string(raw))}
			if message.Envelope != nil {
				candidate.ReceivedAt = message.Envelope.Date
				candidate.MessageID = message.Envelope.MessageId
			}
			candidates = append(candidates, candidate)
		case <-ctx.Done():
			w.disconnect()
			<-done
			return nil, ctx.Err()
		}
	}
}

func observationFromCorrelation(status csvMailStatus, correlation csvMailCorrelation) csvMailObservation {
	return csvMailObservation{
		Status: status, Link: correlation.Link, FolderAlias: correlation.FolderAlias,
		UIDHash: correlation.UIDHash, MessageIDHash: correlation.MessageIDHash,
		MatchReason: correlation.MatchReason, SkipReasons: correlation.SkipReasons,
	}
}

func csvMailReconnectDelay(attempt int) time.Duration {
	delays := [...]time.Duration{100 * time.Millisecond, 250 * time.Millisecond, 500 * time.Millisecond}
	if attempt < 0 || attempt >= len(delays) {
		return delays[len(delays)-1]
	}
	return delays[attempt]
}
