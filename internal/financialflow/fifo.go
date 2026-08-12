package financialflow

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"math/big"
	"sort"
	"strings"
	"time"
)

var (
	ErrInvalidEvent    = errors.New("invalid financial flow event")
	ErrInvalidSnapshot = errors.New("invalid financial flow snapshot")
)

var analysisWindows = []struct {
	name string
	d    time.Duration
}{
	{"5m", 5 * time.Minute},
	{"30m", 30 * time.Minute},
	{"1h", time.Hour},
	{"6h", 6 * time.Hour},
	{"24h", 24 * time.Hour},
	{"7d", 7 * 24 * time.Hour},
	{"30d", 30 * 24 * time.Hour},
}

type consumption struct {
	time   time.Time
	amount *big.Rat
	kind   EventKind
}

type lot struct {
	event        Event
	amount       *big.Rat
	remaining    *big.Rat
	usd          *big.Rat
	consumptions []consumption
}

type parsedEvent struct {
	event  Event
	amount *big.Rat
	usd    *big.Rat
	order  int
}

// Analyze performs strict FIFO matching independently for each address/token.
// Native gas fees consume native lots for retention, but are never counted as
// counterparty pass-through transfers.
func Analyze(events []Event, snapshot Snapshot) (Report, error) {
	if snapshot.AsOf.IsZero() || strings.TrimSpace(snapshot.ID) == "" || strings.TrimSpace(snapshot.PriceVersion) == "" {
		return Report{}, ErrInvalidSnapshot
	}
	groups := make(map[string][]Event)
	for _, event := range events {
		if event.Time.After(snapshot.AsOf) {
			continue
		}
		address := strings.ToLower(strings.TrimSpace(event.Address))
		token := strings.ToLower(strings.TrimSpace(event.Token))
		if token == "" {
			token = NativeAssetID
		}
		if !addressRE.MatchString(address) || (token != NativeAssetID && !addressRE.MatchString(token)) {
			return Report{}, ErrInvalidEvent
		}
		event.Address, event.Token = address, token
		groups[address+"\x00"+token] = append(groups[address+"\x00"+token], event)
	}
	keys := make([]string, 0, len(groups))
	for key := range groups {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	report := Report{Results: make([]TokenResult, 0, len(keys))}
	for _, key := range keys {
		result, skipped, err := analyzeGroup(groups[key], snapshot)
		if err != nil {
			return Report{}, err
		}
		report.SkippedZeroAmountEvents += skipped
		report.Results = append(report.Results, result)
	}
	return report, nil
}

func analyzeGroup(events []Event, snapshot Snapshot) (TokenResult, int, error) {
	parsed := make([]parsedEvent, 0, len(events))
	skipped := 0
	for i, event := range events {
		if event.Time.IsZero() || (event.Direction != DirectionIn && event.Direction != DirectionOut) {
			return TokenResult{}, 0, fmt.Errorf("%w at event %d", ErrInvalidEvent, i)
		}
		amount, err := positiveDecimal(event.Amount)
		if err != nil {
			// 零金额/负金额事件不构成资金流动：跳过并计数，避免整组分析失败。
			if rat, parseErr := decimal(event.Amount); parseErr == nil && rat.Sign() <= 0 {
				skipped++
				continue
			}
			return TokenResult{}, 0, fmt.Errorf("%w: invalid amount at event %d", ErrInvalidEvent, i)
		}
		if event.Kind == "" {
			event.Kind = EventTransfer
		}
		if event.Kind != EventTransfer && event.Kind != EventGasFee {
			return TokenResult{}, 0, fmt.Errorf("%w: unsupported event kind", ErrInvalidEvent)
		}
		if event.Kind == EventGasFee && (event.Token != NativeAssetID || event.Direction != DirectionOut) {
			return TokenResult{}, 0, fmt.Errorf("%w: gas fee must be an outgoing native-asset event", ErrInvalidEvent)
		}
		var usd *big.Rat
		if strings.TrimSpace(event.USDValue) != "" {
			usd, err = decimal(event.USDValue)
			if err != nil || usd.Sign() < 0 {
				return TokenResult{}, 0, fmt.Errorf("%w: invalid USD value", ErrInvalidEvent)
			}
		}
		parsed = append(parsed, parsedEvent{event: event, amount: amount, usd: usd, order: i})
	}
	if len(parsed) == 0 {
		return TokenResult{
			Address:                     strings.ToLower(strings.TrimSpace(events[0].Address)),
			Token:                       strings.ToLower(strings.TrimSpace(events[0].Token)),
			NativeAsset:                 strings.ToLower(strings.TrimSpace(events[0].Token)) == NativeAssetID,
			RetentionAlgorithmVersion:   RetentionAlgorithmVersion,
			PassThroughAlgorithmVersion: PassThroughAlgorithmVersion,
			Snapshot:                    snapshot,
			Interpretation:              "behavioral timing metrics only; they do not establish crime, ownership, collection, or laundering",
			InputDigestSHA256:           digest(parsed, snapshot),
		}, skipped, nil
	}
	sort.SliceStable(parsed, func(i, j int) bool {
		a, b := parsed[i], parsed[j]
		if !a.event.Time.Equal(b.event.Time) {
			return a.event.Time.Before(b.event.Time)
		}
		if a.event.BlockNumber != b.event.BlockNumber {
			return a.event.BlockNumber < b.event.BlockNumber
		}
		if a.event.TransactionIndex != b.event.TransactionIndex {
			return a.event.TransactionIndex < b.event.TransactionIndex
		}
		if a.event.TxHash != b.event.TxHash {
			return a.event.TxHash < b.event.TxHash
		}
		if a.event.EventIndex != b.event.EventIndex {
			return a.event.EventIndex < b.event.EventIndex
		}
		return a.order < b.order
	})

	result := TokenResult{
		Address: parsed[0].event.Address, Token: parsed[0].event.Token,
		NativeAsset:                 parsed[0].event.Token == NativeAssetID,
		RetentionAlgorithmVersion:   RetentionAlgorithmVersion,
		PassThroughAlgorithmVersion: PassThroughAlgorithmVersion,
		Snapshot:                    snapshot,
		Interpretation:              "behavioral timing metrics only; they do not establish crime, ownership, collection, or laundering",
	}
	lots := make([]*lot, 0)
	queueHead := 0
	openingTransfer, openingGas := zero(), zero()
	gasAmount, gasUSD := zero(), zero()
	inAmount, inPriced, outAmount, outPriced := zero(), zero(), zero(), zero()
	var inEvents, pricedInEvents, outEvents, pricedOutEvents uint64
	for _, item := range parsed {
		if item.event.Direction == DirectionIn {
			inEvents++
			inAmount.Add(inAmount, item.amount)
			if item.usd != nil {
				pricedInEvents++
				inPriced.Add(inPriced, item.amount)
			}
			lots = append(lots, &lot{event: item.event, amount: clone(item.amount), remaining: clone(item.amount), usd: cloneOrNil(item.usd)})
			continue
		}
		outEvents++
		outAmount.Add(outAmount, item.amount)
		if item.usd != nil {
			pricedOutEvents++
			outPriced.Add(outPriced, item.amount)
		}
		if item.event.Kind == EventGasFee {
			gasAmount.Add(gasAmount, item.amount)
			if item.usd != nil {
				gasUSD.Add(gasUSD, item.usd)
			}
		}
		remaining := clone(item.amount)
		for remaining.Sign() > 0 && queueHead < len(lots) {
			current := lots[queueHead]
			used := minRat(remaining, current.remaining)
			current.remaining.Sub(current.remaining, used)
			remaining.Sub(remaining, used)
			current.consumptions = append(current.consumptions, consumption{time: item.event.Time, amount: used, kind: item.event.Kind})
			if current.remaining.Sign() == 0 {
				queueHead++
			}
		}
		if remaining.Sign() > 0 {
			if item.event.Kind == EventGasFee {
				openingGas.Add(openingGas, remaining)
			} else {
				openingTransfer.Add(openingTransfer, remaining)
			}
		}
	}
	result.Coverage = Coverage{
		IncomingAmount: formatRat(inAmount), IncomingPricedAmount: formatRat(inPriced), IncomingUSDCoverage: ratio(inPriced, inAmount),
		OutgoingAmount: formatRat(outAmount), OutgoingPricedAmount: formatRat(outPriced), OutgoingUSDCoverage: ratio(outPriced, outAmount),
		IncomingEvents: inEvents, PricedIncomingEvents: pricedInEvents, OutgoingEvents: outEvents, PricedOutgoingEvents: pricedOutEvents,
	}
	result.OpeningBalanceOutAmount = formatRat(openingTransfer)
	result.OpeningBalanceGasAmount = formatRat(openingGas)
	result.GasFeeAmount = formatRat(gasAmount)
	if gasUSD.Sign() > 0 {
		result.GasFeeUSD = formatRat(gasUSD)
	}
	result.RetentionWindows = make([]WindowMetric, 0, 5)
	result.PassThroughWindows = make([]WindowMetric, 0, 5)
	for _, window := range analysisWindows {
		metric := calculateWindow(lots, snapshot.AsOf, window.name, window.d)
		switch window.name {
		case "1h", "6h", "24h", "7d", "30d":
			result.RetentionWindows = append(result.RetentionWindows, metric)
		}
		switch window.name {
		case "5m", "30m", "1h", "6h", "24h":
			result.PassThroughWindows = append(result.PassThroughWindows, metric)
		}
	}
	for _, metric := range result.RetentionWindows {
		if metric.Window == "30d" {
			result.SettlementRatio30D = metric.RetentionRatio
			if metric.MaturedReceivedUSD != "" {
				result.SettlementRatio30DUSD = ratioString(metric.RetainedUSD, metric.MaturedReceivedUSD)
			}
		}
	}
	result.InputDigestSHA256 = digest(parsed, snapshot)
	return result, skipped, nil
}

func calculateWindow(lots []*lot, asOf time.Time, name string, duration time.Duration) WindowMetric {
	received, retained, transferred, gas := zero(), zero(), zero(), zero()
	receivedUSD, retainedUSD, transferredUSD, gasUSD := zero(), zero(), zero(), zero()
	pricedAmount := zero()
	var maturedLots, valuedLots uint64
	for _, item := range lots {
		deadline := item.event.Time.Add(duration)
		if deadline.After(asOf) {
			continue
		}
		maturedLots++
		received.Add(received, item.amount)
		remaining := clone(item.amount)
		transferForLot, gasForLot := zero(), zero()
		for _, use := range item.consumptions {
			if use.time.After(deadline) {
				continue
			}
			remaining.Sub(remaining, use.amount)
			if use.kind == EventGasFee {
				gasForLot.Add(gasForLot, use.amount)
			} else {
				transferForLot.Add(transferForLot, use.amount)
			}
		}
		retained.Add(retained, remaining)
		transferred.Add(transferred, transferForLot)
		gas.Add(gas, gasForLot)
		if item.usd != nil {
			valuedLots++
			pricedAmount.Add(pricedAmount, item.amount)
			receivedUSD.Add(receivedUSD, item.usd)
			unit := new(big.Rat).Quo(item.usd, item.amount)
			retainedUSD.Add(retainedUSD, new(big.Rat).Mul(remaining, unit))
			transferredUSD.Add(transferredUSD, new(big.Rat).Mul(transferForLot, unit))
			gasUSD.Add(gasUSD, new(big.Rat).Mul(gasForLot, unit))
		}
	}
	metric := WindowMetric{
		Window: name, Seconds: int64(duration / time.Second),
		MaturedReceivedAmount: formatRat(received), RetainedAmount: formatRat(retained),
		MatchedTransferAmount: formatRat(transferred), GasConsumedAmount: formatRat(gas),
		PassThroughRatio: ratio(transferred, received), RetentionRatio: ratio(retained, received),
		AttributedUSDBasis: "incoming_lot_historical_usd_pro_rata",
		USDAmountCoverage:  ratio(pricedAmount, received), MaturedIncomingLots: maturedLots,
		USDValuedIncomingLots: valuedLots, USDValuesComplete: received.Sign() > 0 && pricedAmount.Cmp(received) == 0,
	}
	if pricedAmount.Sign() > 0 {
		metric.MaturedReceivedUSD = formatRat(receivedUSD)
		metric.RetainedUSD = formatRat(retainedUSD)
		metric.MatchedTransferUSD = formatRat(transferredUSD)
		metric.GasConsumedUSD = formatRat(gasUSD)
	}
	return metric
}

func cloneOrNil(v *big.Rat) *big.Rat {
	if v == nil {
		return nil
	}
	return clone(v)
}

func ratioString(numerator, denominator string) string {
	n, errN := decimal(numerator)
	d, errD := decimal(denominator)
	if errN != nil || errD != nil {
		return ""
	}
	return ratio(n, d)
}

func digest(events []parsedEvent, snapshot Snapshot) string {
	h := sha256.New()
	fmt.Fprintf(h, "%s\x00%s\x00%s\n", snapshot.ID, snapshot.AsOf.UTC().Format(time.RFC3339Nano), snapshot.PriceVersion)
	for _, item := range events {
		fmt.Fprintf(h, "%s\x00%s\x00%s\x00%s\x00%s\x00%d\x00%d\x00%s\x00%s\x00%s\x00%s\x00%s\x00%s\x00%d\n",
			item.event.Address, item.event.Token, item.event.Direction, item.event.Kind,
			item.event.Time.UTC().Format(time.RFC3339Nano), item.event.BlockNumber, item.event.TransactionIndex, item.event.TxHash,
			item.event.EventIndex, formatRat(item.amount), formatRat(item.usd), item.event.PriceSource,
			item.event.PriceTime, item.event.SchemaVersion)
	}
	return hex.EncodeToString(h.Sum(nil))
}
