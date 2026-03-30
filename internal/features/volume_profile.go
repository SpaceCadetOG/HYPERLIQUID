package features

import (
	"fmt"
	"math"
	"strings"
	"time"

	"hyperliquid/internal/market"
	"hyperliquid/internal/ta"
)

type Snapshot struct {
	Symbol                string
	Last                  float64
	WindowOpenPrice       float64
	DayUTCChangePct       float64
	VolumeUSD             float64
	OIUSD                 float64
	VolumeRatio           float64
	ProfileBins           []PriceVolume
	ProfileTotalVolume    float64
	POCShare              float64
	ValueLow              float64
	ValueHigh             float64
	POC                   float64
	AVWAP                 float64
	AVWAPDistancePct      float64
	ADX                   float64
	ATR                   float64
	ATRPct                float64
	BookSkew              float64
	SpreadBps             float64
	TopBookUSD            float64
	EstSlippageBps        float64
	LongWhalePrice        float64
	ShortWhalePrice       float64
	LongStopPrice         float64
	ShortStopPrice        float64
	LongTargetPrice       float64
	ShortTargetPrice      float64
	LongWhaleDistanceBps  float64
	ShortWhaleDistanceBps float64
	ChangePct             float64
	Change5m              float64
	Change30m             float64
	Change4h              float64
	Funding               float64
	LongScore             float64
	ShortScore            float64
	LongReason            string
	ShortReason           string
	LongConfluenceLabel   string
	ShortConfluenceLabel  string
	Signals               market.EligibilitySignals
}

type PriceVolume struct {
	Price  float64
	Volume float64
}

func BuildSnapshot(mkt market.Market, candles []market.Candle, book market.OrderBook) Snapshot {
	binSize := profileBinSize(mkt, candles)
	profile, poc := sessionVolumeProfile(candles, binSize)
	val, vah := computeValueArea(profile, poc, 0.70)
	bins := profileBins(profile)
	totalProfileVolume := 0.0
	pocVolume := 0.0
	for _, bin := range bins {
		totalProfileVolume += bin.Volume
		if bin.Price == poc {
			pocVolume = bin.Volume
		}
	}
	avwap := anchoredVWAP(candles)
	avwapDistancePct := pctDistance(mkt.MarkPrice, avwap)
	atr := averageTrueRange(candles, 14)
	adx := averageDirectionalIndex(candles, 14)
	bookSkew := topBookSkew(book)
	spreadBps := topSpreadBps(book)
	topBookUSD := topBookUSD(book)
	estSlippageBps := estimateSlippageBps(book, 1000)
	longWhalePrice, longWhaleDistanceBps := nearestWhaleBid(book, mkt.MarkPrice)
	shortWhalePrice, shortWhaleDistanceBps := nearestWhaleAsk(book, mkt.MarkPrice)
	longStopPrice := stopBehindLevel(market.SideLong, longWhalePrice, mkt.TickSize)
	shortStopPrice := stopBehindLevel(market.SideShort, shortWhalePrice, mkt.TickSize)
	longTargetPrice := scalpTarget(market.SideLong, mkt.MarkPrice, atr)
	shortTargetPrice := scalpTarget(market.SideShort, mkt.MarkPrice, atr)
	changePct := 0.0
	windowOpenPrice := 0.0
	if len(candles) > 1 && candles[0].Close > 0 {
		windowOpenPrice = candles[0].Close
		changePct = 100 * (candles[len(candles)-1].Close - candles[0].Close) / candles[0].Close
	}
	dayUTCChangePct := dayUTCChangePct(candles)
	momentum := recentMomentumPct(candles, 12)
	change5m := recentMomentumPct(candles, 1)
	change30m := recentMomentumPct(candles, 6)
	change4h := recentMomentumPct(candles, 48)
	atrPct := pctOfPrice(atr, mkt.MarkPrice)
	volumeRatio := relativeVolumeRatio(candles, 12)

	longScore := 0.0
	shortScore := 0.0
	longReasons := []string{}
	shortReasons := []string{}
	signals := market.EligibilitySignals{}

	if adx >= 25 {
		longScore += 15
		shortScore += 15
		longReasons = append(longReasons, "trend_regime")
		shortReasons = append(shortReasons, "trend_regime")
		signals.TrendStrong = true
	} else {
		longScore -= 25
		shortScore -= 25
		longReasons = append(longReasons, "adx_filter")
		shortReasons = append(shortReasons, "adx_filter")
	}

	if mkt.MarkPrice > vah {
		longScore += 35
		longReasons = append(longReasons, "acceptance_above_value")
		signals.AcceptanceAboveValue = true
	} else if mkt.MarkPrice < val {
		shortScore += 35
		shortReasons = append(shortReasons, "acceptance_below_value")
		signals.AcceptanceBelowValue = true
	} else if mkt.MarkPrice >= poc {
		longScore += 12
		longReasons = append(longReasons, "upper_value_rotation")
	} else {
		shortScore += 12
		shortReasons = append(shortReasons, "lower_value_rotation")
	}

	if avwap > 0 {
		if mkt.MarkPrice > avwap {
			longScore += 25
			longReasons = append(longReasons, "above_avwap")
			signals.AboveAVWAP = true
		}
		if mkt.MarkPrice < avwap {
			shortScore += 25
			shortReasons = append(shortReasons, "below_avwap")
			signals.BelowAVWAP = true
		}
	}

	if bookSkew > 0.15 {
		longScore += 18
		longReasons = append(longReasons, "bid_pressure")
		signals.BidPressure = true
	}
	if bookSkew < -0.15 {
		shortScore += 18
		shortReasons = append(shortReasons, "ask_pressure")
		signals.AskPressure = true
	}

	if momentum > 0.30 {
		longScore += 12
		longReasons = append(longReasons, "short_term_momentum_up")
		signals.ShortTermMomentumUp = true
	}
	if momentum < -0.30 {
		shortScore += 12
		shortReasons = append(shortReasons, "short_term_momentum_down")
		signals.ShortTermMomentumDown = true
	}

	if math.Abs(avwapDistancePct) <= 0.35 {
		if bookSkew > 0.10 && momentum >= 0 {
			longScore += 10
			longReasons = append(longReasons, "responsive_bid_near_value")
			signals.ResponsiveBidNearValue = true
		}
		if bookSkew < -0.10 && momentum <= 0 {
			shortScore += 10
			shortReasons = append(shortReasons, "responsive_offer_near_value")
			signals.ResponsiveOfferNearValue = true
		}
	}

	if longWhaleDistanceBps > 0 && longWhaleDistanceBps <= 5 && mkt.MarkPrice >= avwap {
		longScore += 18
		longReasons = append(longReasons, "whale_bid_nearby")
		signals.WhaleBidNearby = true
	}
	if shortWhaleDistanceBps > 0 && shortWhaleDistanceBps <= 5 && mkt.MarkPrice <= avwap {
		shortScore += 18
		shortReasons = append(shortReasons, "whale_offer_nearby")
		signals.WhaleOfferNearby = true
	}

	if bookSkew > 0.10 && momentum >= 0 && longWhaleDistanceBps > 0 && longWhaleDistanceBps <= 5 {
		longScore += 12
		longReasons = append(longReasons, "absorption_proxy")
	}
	if bookSkew < -0.10 && momentum <= 0 && shortWhaleDistanceBps > 0 && shortWhaleDistanceBps <= 5 {
		shortScore += 12
		shortReasons = append(shortReasons, "absorption_proxy")
	}

	if changePct > 1 {
		longScore += 6
		longReasons = append(longReasons, "positive_session")
		signals.PositiveSessionMove = true
	}
	if changePct < -1 {
		shortScore += 6
		shortReasons = append(shortReasons, "negative_session")
		signals.NegativeSessionMove = true
	}

	if mkt.FundingRate < 0 {
		longScore += 5
		longReasons = append(longReasons, "negative_funding")
	}
	if mkt.FundingRate > 0 {
		shortScore += 5
		shortReasons = append(shortReasons, "positive_funding")
	}

	if spreadBps > 12 {
		longScore -= 10
		shortScore -= 10
		longReasons = append(longReasons, "wide_spread_penalty")
		shortReasons = append(shortReasons, "wide_spread_penalty")
		signals.WideSpread = true
	}

	if atrPct < 0.20 {
		longScore -= 8
		shortScore -= 8
		longReasons = append(longReasons, "low_atr_penalty")
		shortReasons = append(shortReasons, "low_atr_penalty")
		signals.LowATR = true
	}

	vwap := ta.VWAP(candles)
	tr := ta.TrendMetrics(mkt.Symbol, candles, vwap)
	ef := ta.ComputeEffort(mkt.Symbol, candles, 20, 2.0, 1_000_000)
	ob := ta.OrderBookContext(mkt.Symbol, book)
	longConf := ta.ComputeConfluence(tr, ef, ob, "long")
	shortConf := ta.ComputeConfluence(tr, ef, ob, "short")

	return Snapshot{
		Symbol:                mkt.Symbol,
		Last:                  mkt.MarkPrice,
		WindowOpenPrice:       windowOpenPrice,
		DayUTCChangePct:       dayUTCChangePct,
		VolumeUSD:             mkt.Volume24hUSD,
		OIUSD:                 mkt.OpenInterest,
		VolumeRatio:           volumeRatio,
		ProfileBins:           bins,
		ProfileTotalVolume:    totalProfileVolume,
		POCShare:              safeRatio(pocVolume, totalProfileVolume),
		ValueLow:              val,
		ValueHigh:             vah,
		POC:                   poc,
		AVWAP:                 avwap,
		AVWAPDistancePct:      avwapDistancePct,
		ADX:                   adx,
		ATR:                   atr,
		ATRPct:                atrPct,
		BookSkew:              bookSkew,
		SpreadBps:             spreadBps,
		TopBookUSD:            topBookUSD,
		EstSlippageBps:        estSlippageBps,
		LongWhalePrice:        longWhalePrice,
		ShortWhalePrice:       shortWhalePrice,
		LongStopPrice:         longStopPrice,
		ShortStopPrice:        shortStopPrice,
		LongTargetPrice:       longTargetPrice,
		ShortTargetPrice:      shortTargetPrice,
		LongWhaleDistanceBps:  longWhaleDistanceBps,
		ShortWhaleDistanceBps: shortWhaleDistanceBps,
		ChangePct:             changePct,
		Change5m:              change5m,
		Change30m:             change30m,
		Change4h:              change4h,
		Funding:               mkt.FundingRate,
		LongScore:             squashScore(longScore),
		ShortScore:            squashScore(shortScore),
		LongReason:            strings.Join(longReasons, " + "),
		ShortReason:           strings.Join(shortReasons, " + "),
		LongConfluenceLabel:   longConf.Label,
		ShortConfluenceLabel:  shortConf.Label,
		Signals:               signals,
	}
}

func dayUTCChangePct(candles []market.Candle) float64 {
	if len(candles) < 2 {
		return 0
	}
	last := candles[len(candles)-1]
	if last.Close <= 0 {
		return 0
	}
	dayStart := time.Date(last.Time.UTC().Year(), last.Time.UTC().Month(), last.Time.UTC().Day(), 0, 0, 0, 0, time.UTC)
	open := 0.0
	for _, c := range candles {
		if c.Time.UTC().Before(dayStart) {
			continue
		}
		open = c.Open
		break
	}
	if open <= 0 {
		open = candles[0].Open
	}
	if open <= 0 {
		return 0
	}
	return 100 * (last.Close - open) / open
}

func profileBins(profile map[float64]float64) []PriceVolume {
	prices := sortedPrices(profile)
	out := make([]PriceVolume, 0, len(prices))
	for _, price := range prices {
		out = append(out, PriceVolume{Price: price, Volume: profile[price]})
	}
	return out
}

func relativeVolumeRatio(candles []market.Candle, lookback int) float64 {
	if len(candles) < 2 {
		return 0
	}
	if lookback <= 0 {
		lookback = 12
	}
	end := len(candles) - 1
	start := end - lookback
	if start < 0 {
		start = 0
	}
	sum := 0.0
	count := 0
	for i := start; i < end; i++ {
		if candles[i].Volume <= 0 {
			continue
		}
		sum += candles[i].Volume
		count++
	}
	if count == 0 || candles[end].Volume <= 0 {
		return 0
	}
	return candles[end].Volume / (sum / float64(count))
}

func safeRatio(num, den float64) float64 {
	if den == 0 {
		return 0
	}
	return num / den
}

func squashScore(raw float64) float64 {
	if raw <= 0 {
		return 0
	}
	score := 100 * (1 - math.Exp(-raw/70.0))
	if score < 0 {
		return 0
	}
	if score > 100 {
		return 100
	}
	return math.Round(score*100) / 100
}

func profileBinSize(mkt market.Market, candles []market.Candle) float64 {
	if len(candles) == 0 {
		if mkt.TickSize > 0 {
			return mkt.TickSize
		}
		return 1
	}
	high := candles[0].High
	low := candles[0].Low
	for _, c := range candles {
		if c.High > high {
			high = c.High
		}
		if c.Low < low {
			low = c.Low
		}
	}
	rng := high - low
	bin := rng / 24
	if bin <= 0 {
		bin = mkt.TickSize
	}
	if mkt.TickSize > 0 && bin < mkt.TickSize {
		bin = mkt.TickSize
	}
	if bin <= 0 {
		bin = 1
	}
	return bin
}

func sessionVolumeProfile(candles []market.Candle, binSize float64) (map[float64]float64, float64) {
	out := map[float64]float64{}
	if len(candles) == 0 {
		return out, 0
	}
	low := candles[0].Low
	high := candles[0].High
	for _, c := range candles {
		if c.Low < low {
			low = c.Low
		}
		if c.High > high {
			high = c.High
		}
	}
	start := low - math.Mod(low, binSize)
	for p := start; p <= high; p += binSize {
		out[p] = 0
	}
	for _, c := range candles {
		touched := []float64{}
		for p := range out {
			if p >= c.Low && p <= c.High {
				touched = append(touched, p)
			}
		}
		if len(touched) == 0 {
			continue
		}
		add := c.Volume / float64(len(touched))
		for _, p := range touched {
			out[p] += add
		}
	}
	poc := start
	maxVol := -1.0
	for p, v := range out {
		if v > maxVol {
			maxVol = v
			poc = p
		}
	}
	return out, poc
}

func computeValueArea(profile map[float64]float64, poc float64, valuePct float64) (float64, float64) {
	if len(profile) == 0 {
		return 0, 0
	}
	prices := sortedPrices(profile)
	total := 0.0
	for _, v := range profile {
		total += v
	}
	target := total * valuePct
	idx := 0
	for i, p := range prices {
		if p == poc {
			idx = i
			break
		}
	}
	left := idx - 1
	right := idx + 1
	low := poc
	high := poc
	acc := profile[poc]
	for acc < target && (left >= 0 || right < len(prices)) {
		leftVol := -1.0
		rightVol := -1.0
		if left >= 0 {
			leftVol = profile[prices[left]]
		}
		if right < len(prices) {
			rightVol = profile[prices[right]]
		}
		if rightVol >= leftVol {
			acc += rightVol
			high = prices[right]
			right++
		} else {
			acc += leftVol
			low = prices[left]
			left--
		}
	}
	return low, high
}

func sortedPrices(profile map[float64]float64) []float64 {
	out := make([]float64, 0, len(profile))
	for p := range profile {
		out = append(out, p)
	}
	for i := 0; i < len(out); i++ {
		for j := i + 1; j < len(out); j++ {
			if out[j] < out[i] {
				out[i], out[j] = out[j], out[i]
			}
		}
	}
	return out
}

func topBookSkew(book market.OrderBook) float64 {
	bid := 0.0
	ask := 0.0
	for i, level := range book.Bids {
		if i >= 3 {
			break
		}
		bid += level.Size
	}
	for i, level := range book.Asks {
		if i >= 3 {
			break
		}
		ask += level.Size
	}
	total := bid + ask
	if total == 0 {
		return 0
	}
	return (bid - ask) / total
}

func anchoredVWAP(candles []market.Candle) float64 {
	if len(candles) == 0 {
		return 0
	}
	totalPV := 0.0
	totalVol := 0.0
	for _, c := range candles {
		typical := (c.High + c.Low + c.Close) / 3
		totalPV += typical * c.Volume
		totalVol += c.Volume
	}
	if totalVol == 0 {
		return 0
	}
	return totalPV / totalVol
}

func recentMomentumPct(candles []market.Candle, lookback int) float64 {
	if len(candles) < 2 {
		return 0
	}
	if lookback <= 0 || lookback >= len(candles) {
		lookback = len(candles) - 1
	}
	start := candles[len(candles)-1-lookback].Close
	end := candles[len(candles)-1].Close
	if start == 0 {
		return 0
	}
	return 100 * (end - start) / start
}

func topSpreadBps(book market.OrderBook) float64 {
	if len(book.Bids) == 0 || len(book.Asks) == 0 {
		return 0
	}
	bid := book.Bids[0].Price
	ask := book.Asks[0].Price
	mid := (bid + ask) / 2
	if bid <= 0 || ask <= 0 || mid <= 0 {
		return 0
	}
	return 10000 * (ask - bid) / mid
}

func topBookUSD(book market.OrderBook) float64 {
	levels := 3
	bidUSD := 0.0
	askUSD := 0.0
	for i, level := range book.Bids {
		if i >= levels {
			break
		}
		bidUSD += level.Price * level.Size
	}
	for i, level := range book.Asks {
		if i >= levels {
			break
		}
		askUSD += level.Price * level.Size
	}
	if bidUSD <= 0 || askUSD <= 0 {
		if bidUSD > askUSD {
			return askUSD
		}
		return bidUSD
	}
	if bidUSD < askUSD {
		return bidUSD
	}
	return askUSD
}

func estimateSlippageBps(book market.OrderBook, clipUSD float64) float64 {
	if clipUSD <= 0 {
		return 0
	}
	buy := walkSlippageBps(book.Asks, clipUSD)
	sell := walkSlippageBps(book.Bids, clipUSD)
	if buy > sell {
		return buy
	}
	return sell
}

func walkSlippageBps(levels []market.BookLevel, clipUSD float64) float64 {
	if len(levels) == 0 || clipUSD <= 0 {
		return 0
	}
	best := levels[0].Price
	if best <= 0 {
		return 0
	}
	remain := clipUSD
	notional := 0.0
	qty := 0.0
	for _, level := range levels {
		if level.Price <= 0 || level.Size <= 0 || remain <= 0 {
			continue
		}
		levelNotional := level.Price * level.Size
		take := levelNotional
		if take > remain {
			take = remain
		}
		takeQty := take / level.Price
		notional += take
		qty += takeQty
		remain -= take
		if remain <= 0 {
			break
		}
	}
	if qty <= 0 || notional <= 0 {
		return 0
	}
	avg := notional / qty
	return 10000 * math.Abs(avg-best) / best
}

func pctDistance(price, anchor float64) float64 {
	if price <= 0 || anchor <= 0 {
		return 0
	}
	return 100 * (price - anchor) / anchor
}

func pctOfPrice(value, price float64) float64 {
	if value <= 0 || price <= 0 {
		return 0
	}
	return 100 * value / price
}

func averageTrueRange(candles []market.Candle, period int) float64 {
	if len(candles) < 2 {
		return 0
	}
	trs := make([]float64, 0, len(candles)-1)
	for i := 1; i < len(candles); i++ {
		curr := candles[i]
		prev := candles[i-1]
		tr := math.Max(curr.High-curr.Low, math.Max(math.Abs(curr.High-prev.Close), math.Abs(curr.Low-prev.Close)))
		trs = append(trs, tr)
	}
	if period <= 0 || period > len(trs) {
		period = len(trs)
	}
	sum := 0.0
	for _, tr := range trs[len(trs)-period:] {
		sum += tr
	}
	return sum / float64(period)
}

func averageDirectionalIndex(candles []market.Candle, period int) float64 {
	if len(candles) < 3 {
		return 0
	}
	if period <= 1 || period >= len(candles) {
		period = len(candles) - 1
	}
	if period <= 1 {
		return 0
	}
	trs := make([]float64, 0, len(candles)-1)
	plusDMs := make([]float64, 0, len(candles)-1)
	minusDMs := make([]float64, 0, len(candles)-1)
	for i := 1; i < len(candles); i++ {
		curr := candles[i]
		prev := candles[i-1]
		upMove := curr.High - prev.High
		downMove := prev.Low - curr.Low
		plusDM := 0.0
		minusDM := 0.0
		if upMove > downMove && upMove > 0 {
			plusDM = upMove
		}
		if downMove > upMove && downMove > 0 {
			minusDM = downMove
		}
		tr := math.Max(curr.High-curr.Low, math.Max(math.Abs(curr.High-prev.Close), math.Abs(curr.Low-prev.Close)))
		trs = append(trs, tr)
		plusDMs = append(plusDMs, plusDM)
		minusDMs = append(minusDMs, minusDM)
	}
	dxValues := make([]float64, 0, len(trs)-period+1)
	for end := period; end <= len(trs); end++ {
		start := end - period
		trSum := sumSlice(trs[start:end])
		if trSum == 0 {
			dxValues = append(dxValues, 0)
			continue
		}
		plusDI := 100 * sumSlice(plusDMs[start:end]) / trSum
		minusDI := 100 * sumSlice(minusDMs[start:end]) / trSum
		denom := plusDI + minusDI
		if denom == 0 {
			dxValues = append(dxValues, 0)
			continue
		}
		dxValues = append(dxValues, 100*math.Abs(plusDI-minusDI)/denom)
	}
	return sumSlice(dxValues) / float64(len(dxValues))
}

func nearestWhaleBid(book market.OrderBook, markPrice float64) (float64, float64) {
	if len(book.Bids) == 0 {
		return 0, 0
	}
	threshold := whaleThreshold(book.Bids)
	bestPrice := 0.0
	bestDistance := math.MaxFloat64
	for _, level := range book.Bids {
		if level.Size < threshold || level.Price > markPrice {
			continue
		}
		dist := 10000 * (markPrice - level.Price) / markPrice
		if dist >= 0 && dist < bestDistance {
			bestDistance = dist
			bestPrice = level.Price
		}
	}
	if bestPrice == 0 || bestDistance == math.MaxFloat64 {
		return 0, 0
	}
	return bestPrice, bestDistance
}

func nearestWhaleAsk(book market.OrderBook, markPrice float64) (float64, float64) {
	if len(book.Asks) == 0 {
		return 0, 0
	}
	threshold := whaleThreshold(book.Asks)
	bestPrice := 0.0
	bestDistance := math.MaxFloat64
	for _, level := range book.Asks {
		if level.Size < threshold || level.Price < markPrice {
			continue
		}
		dist := 10000 * (level.Price - markPrice) / markPrice
		if dist >= 0 && dist < bestDistance {
			bestDistance = dist
			bestPrice = level.Price
		}
	}
	if bestPrice == 0 || bestDistance == math.MaxFloat64 {
		return 0, 0
	}
	return bestPrice, bestDistance
}

func whaleThreshold(levels []market.BookLevel) float64 {
	if len(levels) == 0 {
		return 0
	}
	sizes := make([]float64, 0, len(levels))
	for _, level := range levels {
		sizes = append(sizes, level.Size)
	}
	mean := sumSlice(sizes) / float64(len(sizes))
	maxSize := 0.0
	for _, size := range sizes {
		if size > maxSize {
			maxSize = size
		}
	}
	threshold := math.Max(mean, maxSize*0.75)
	if threshold <= 0 {
		return maxSize
	}
	return threshold
}

func stopBehindLevel(side market.Side, levelPrice, tickSize float64) float64 {
	if levelPrice <= 0 {
		return 0
	}
	buffer := tickSize * 2
	if buffer <= 0 {
		buffer = levelPrice * 0.0002
	}
	if side == market.SideLong {
		return levelPrice - buffer
	}
	return levelPrice + buffer
}

func scalpTarget(side market.Side, entryPrice, atr float64) float64 {
	if entryPrice <= 0 {
		return 0
	}
	move := atr * 0.15
	if move <= 0 {
		move = entryPrice * 0.001
	}
	if side == market.SideLong {
		return entryPrice + move
	}
	return entryPrice - move
}

func sumSlice(values []float64) float64 {
	sum := 0.0
	for _, v := range values {
		sum += v
	}
	return sum
}

func clamp(v, low, high float64) float64 {
	if v < low {
		return low
	}
	if v > high {
		return high
	}
	return v
}

func FormatReason(prefix string, score float64, reason string) string {
	if reason == "" {
		reason = "none"
	}
	return fmt.Sprintf("%s %.1f | %s", prefix, score, reason)
}
