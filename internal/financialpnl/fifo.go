package financialpnl

import (
	"errors"
	"fmt"
	"math/big"
	"sort"
	"strings"
	"time"
)

var ErrInvalidEvent = errors.New("invalid position event")

type fifoLot struct {
	lot       Lot
	remaining *big.Rat
	cost      *big.Rat
}

type Engine struct {
	StaleAfter time.Duration
}

func (e Engine) Calculate(q Query, events []PositionEvent, current *Price) (Result, error) {
	if e.StaleAfter <= 0 {
		e.StaleAfter = time.Minute
	}
	ordered := append([]PositionEvent(nil), events...)
	sort.SliceStable(ordered, func(i, j int) bool {
		if ordered[i].Time.Equal(ordered[j].Time) {
			if ordered[i].BlockNumber == ordered[j].BlockNumber {
				return ordered[i].EventIndex < ordered[j].EventIndex
			}
			return ordered[i].BlockNumber < ordered[j].BlockNumber
		}
		return ordered[i].Time.Before(ordered[j].Time)
	})

	lots := make([]*fifoLot, 0)
	realized, coveredProceeds, realizedCost, realizedGas := new(big.Rat), new(big.Rat), new(big.Rat), new(big.Rat)
	sold, knownSold := new(big.Rat), new(big.Rat)
	priceVersions, dataVersions := make(map[string]struct{}), make(map[string]struct{})
	for _, event := range ordered {
		if event.Time.After(q.AsOf) {
			continue
		}
		amount, err := positiveRat(event.Amount)
		if err != nil {
			return Result{}, fmt.Errorf("%w: amount", ErrInvalidEvent)
		}
		if event.PriceVersion != "" {
			priceVersions[event.PriceVersion] = struct{}{}
		}
		if event.DataSnapshotVersion != "" {
			dataVersions[event.DataSnapshotVersion] = struct{}{}
		}
		switch event.Type {
		case EventDEXBuy, EventKnownBuy:
			cost, err := requiredNonNegative(event.USDValue)
			if err != nil {
				return Result{}, fmt.Errorf("%w: buy usd value", ErrInvalidEvent)
			}
			gas, err := optionalNonNegative(event.GasUSD)
			if err != nil {
				return Result{}, fmt.Errorf("%w: gas", ErrInvalidEvent)
			}
			cost.Add(cost, gas)
			lots = append(lots, newLot(event, amount, cost))
		case EventTransferIn, EventAirdrop, EventMint, EventBridgeIn, EventUnknown:
			lots = append(lots, newLot(event, amount, nil))
		case EventDEXSell, EventKnownSell:
			proceeds, err := requiredNonNegative(event.USDValue)
			if err != nil {
				return Result{}, fmt.Errorf("%w: sell usd value", ErrInvalidEvent)
			}
			gas, err := optionalNonNegative(event.GasUSD)
			if err != nil {
				return Result{}, fmt.Errorf("%w: gas", ErrInvalidEvent)
			}
			sold.Add(sold, amount)
			known, cost := consume(lots, amount)
			knownSold.Add(knownSold, known)
			if known.Sign() > 0 {
				ratio := new(big.Rat).Quo(known, amount)
				knownProceeds := new(big.Rat).Mul(proceeds, ratio)
				knownGas := new(big.Rat).Mul(gas, ratio)
				coveredProceeds.Add(coveredProceeds, knownProceeds)
				realizedCost.Add(realizedCost, cost)
				realizedGas.Add(realizedGas, knownGas)
				realized.Add(realized, new(big.Rat).Sub(new(big.Rat).Sub(knownProceeds, cost), knownGas))
			}
		case EventTransferOut, EventBurn, EventBridgeOut:
			consume(lots, amount) // Position movement only; never sale proceeds or realized PnL.
		default:
			return Result{}, fmt.Errorf("%w: event type", ErrInvalidEvent)
		}
	}

	result := Result{ChainID: q.ChainID, Address: q.Address, Token: q.Token, AsOf: q.AsOf,
		RealizedPnLUSD: decimal(realized), RealizedProceedsCoveredUSD: decimal(coveredProceeds),
		RealizedCostBasisUSD: decimal(realizedCost), RealizedGasUSD: decimal(realizedGas),
		SoldAmount: decimal(sold), KnownSoldAmount: decimal(knownSold), KnownCostBasisRatio: ratio(knownSold, sold),
		RealizedPnLScope: "KNOWN_COST_BASIS_ONLY",
		AlgorithmVersion: AlgorithmVersion, HistoricalPriceVersions: sortedKeys(priceVersions), DataSnapshotVersion: strings.Join(sortedKeys(dataVersions), ","),
		SnapshotVersion: SnapshotVersion, CurrentPriceStatus: "MISSING"}
	result.PriceVersion = "historical:" + strings.Join(result.HistoricalPriceVersions, ",") + "|current:missing"
	result.RealizedPnLStatus, result.FinancialConfidence = realizedStatus(knownSold, sold)

	position, knownPosition, remainingCost := new(big.Rat), new(big.Rat), new(big.Rat)
	for _, entry := range lots {
		if entry.remaining.Sign() == 0 {
			continue
		}
		position.Add(position, entry.remaining)
		entry.lot.RemainingAmount = decimal(entry.remaining)
		if entry.cost != nil {
			knownPosition.Add(knownPosition, entry.remaining)
			remainingCost.Add(remainingCost, entry.cost)
			value := decimal(entry.cost)
			entry.lot.RemainingCostUSD = &value
		}
		result.Lots = append(result.Lots, entry.lot)
	}
	result.PositionAmount = decimal(position)
	result.KnownPositionAmount = decimal(knownPosition)
	result.RemainingKnownCostUSD = decimal(remainingCost)
	result.UnrealizedCoverage = ratio(knownPosition, position)
	if current != nil {
		price, err := positiveRat(current.USD)
		if err != nil {
			return Result{}, fmt.Errorf("%w: current price", ErrInvalidEvent)
		}
		value := decimal(new(big.Rat).Mul(position, price))
		knownPnL := decimal(new(big.Rat).Sub(new(big.Rat).Mul(knownPosition, price), remainingCost))
		result.PositionMarketValueUSD, result.KnownUnrealizedPnLUSD = &value, &knownPnL
		p := decimal(price)
		result.CurrentPriceUSD, result.CurrentPriceTime = &p, &current.Time
		result.CurrentPriceSource, result.CurrentPriceVersion = current.Source, current.Version
		result.PriceVersion = "historical:" + strings.Join(result.HistoricalPriceVersions, ",") + "|current:" + current.Version
		result.CurrentPriceStatus = "FRESH"
		if current.Time.After(q.AsOf) || q.AsOf.Sub(current.Time) > e.StaleAfter {
			result.CurrentPriceStatus = "STALE"
		}
	}
	return result, nil
}

func sortedKeys(values map[string]struct{}) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func realizedStatus(known, sold *big.Rat) (string, string) {
	if sold.Sign() == 0 {
		return "NOT_APPLICABLE", "NOT_APPLICABLE"
	}
	if known.Sign() == 0 {
		return "UNKNOWN_COST_BASIS", "MISSING"
	}
	if known.Cmp(sold) < 0 {
		return "PARTIAL_COST_BASIS", "PARTIAL"
	}
	return "COMPLETE", "HIGH"
}

func newLot(event PositionEvent, amount, cost *big.Rat) *fifoLot {
	l := Lot{AcquiredTime: event.Time, AcquiredAmount: decimal(amount), RemainingAmount: decimal(amount), SourceTx: event.TransactionHash, SourceType: event.Type, CostBasisStatus: "UNKNOWN"}
	if cost != nil {
		v := decimal(cost)
		l.CostUSD, l.RemainingCostUSD, l.CostBasisStatus = &v, &v, "KNOWN"
	}
	return &fifoLot{lot: l, remaining: new(big.Rat).Set(amount), cost: cloneRat(cost)}
}

func consume(lots []*fifoLot, requested *big.Rat) (*big.Rat, *big.Rat) {
	left, knownAmount, knownCost := new(big.Rat).Set(requested), new(big.Rat), new(big.Rat)
	for _, lot := range lots {
		if left.Sign() == 0 {
			break
		}
		take := new(big.Rat).Set(lot.remaining)
		if take.Cmp(left) > 0 {
			take.Set(left)
		}
		before := new(big.Rat).Set(lot.remaining)
		lot.remaining.Sub(lot.remaining, take)
		left.Sub(left, take)
		if lot.cost != nil && before.Sign() > 0 {
			usedCost := new(big.Rat).Mul(lot.cost, new(big.Rat).Quo(take, before))
			lot.cost.Sub(lot.cost, usedCost)
			knownAmount.Add(knownAmount, take)
			knownCost.Add(knownCost, usedCost)
		}
	}
	return knownAmount, knownCost
}

func positiveRat(value string) (*big.Rat, error) {
	r, ok := new(big.Rat).SetString(value)
	if !ok || r.Sign() <= 0 {
		return nil, ErrInvalidEvent
	}
	return r, nil
}

func requiredNonNegative(value *string) (*big.Rat, error) {
	if value == nil {
		return nil, ErrInvalidEvent
	}
	return nonNegative(*value)
}

func optionalNonNegative(value *string) (*big.Rat, error) {
	if value == nil || *value == "" {
		return new(big.Rat), nil
	}
	return nonNegative(*value)
}

func nonNegative(value string) (*big.Rat, error) {
	r, ok := new(big.Rat).SetString(value)
	if !ok || r.Sign() < 0 {
		return nil, ErrInvalidEvent
	}
	return r, nil
}

func cloneRat(r *big.Rat) *big.Rat {
	if r == nil {
		return nil
	}
	return new(big.Rat).Set(r)
}

func ratio(numerator, denominator *big.Rat) string {
	if denominator.Sign() == 0 {
		return "0"
	}
	return decimal(new(big.Rat).Quo(numerator, denominator))
}

func decimal(r *big.Rat) string {
	if r == nil || r.Sign() == 0 {
		return "0"
	}
	s := r.FloatString(18)
	for len(s) > 0 && s[len(s)-1] == '0' {
		s = s[:len(s)-1]
	}
	if len(s) > 0 && s[len(s)-1] == '.' {
		s = s[:len(s)-1]
	}
	if s == "-0" {
		return "0"
	}
	return s
}
