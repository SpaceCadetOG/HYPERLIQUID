package execution

import (
	"fmt"
	"math"
	"sync"

	"hyperliquid/internal/market"
)

type Paper struct {
	mu        sync.Mutex
	startUSD  float64
	available float64
	reserved  float64
	realized  float64
	manager   *Manager
	positions map[string]market.Position
	orders    []market.Order
}

type ManageInput struct {
	Symbol       string
	MarkPrice    float64
	WeakFlow     bool
	NearFriction bool
	LiqSpike     bool
	Stale        bool
	Warning      string
	ForceExit    bool
	ExitReason   string
}

type Receipt struct {
	Symbol      string
	Side        market.Side
	Qty         float64
	FillPrice   float64
	Realized    float64
	RealizedPct float64
	Reason      string
}

type Snapshot struct {
	Equity           float64
	Balance          float64
	Available        float64
	Reserved         float64
	DeployedNotional float64
	OpenPnL          float64
	DayRealized      float64
	OpenCount        int
}

func NewPaper(startUSD float64) *Paper {
	return &Paper{
		startUSD:  startUSD,
		available: startUSD,
		manager:   NewManager(Config{}),
		positions: map[string]market.Position{},
	}
}

func (p *Paper) Balance() (market.AccountSnapshot, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	openPnL := 0.0
	for _, pos := range p.positions {
		openPnL += pos.Unrealized
	}
	return market.AccountSnapshot{
		Equity:       p.available + p.reserved + openPnL,
		AvailableUSD: p.available,
		ReservedUSD:  p.reserved,
	}, nil
}

func (p *Paper) Positions() ([]market.Position, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := make([]market.Position, 0, len(p.positions))
	for _, pos := range p.positions {
		out = append(out, pos)
	}
	return out, nil
}

func (p *Paper) PlaceOrder(req market.OrderRequest) (market.OrderResult, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if req.Symbol == "" || req.Size <= 0 || req.Price <= 0 {
		return market.OrderResult{}, fmt.Errorf("invalid paper order")
	}
	leverage := req.Leverage
	if leverage <= 0 {
		leverage = 1
	}
	entryNotional := req.Price * req.Size
	reservedMargin := entryNotional / leverage
	if reservedMargin <= 0 {
		return market.OrderResult{}, fmt.Errorf("invalid paper margin")
	}
	if reservedMargin > p.available {
		return market.OrderResult{}, fmt.Errorf("insufficient free capital: need %.2f have %.2f", reservedMargin, p.available)
	}
	id := fmt.Sprintf("paper-%s-%d", req.Symbol, len(p.orders)+1)
	p.orders = append(p.orders, market.Order{
		ID:          id,
		ClientOrder: req.ClientOrder,
		Symbol:      req.Symbol,
		Side:        req.Side,
		Size:        req.Size,
		Price:       req.Price,
		Status:      "filled",
	})
	side := req.Side
	if req.ReduceOnly {
		if pos, ok := p.positions[req.Symbol]; ok {
			released := pos.ReservedMargin
			p.available += released + pos.Unrealized
			p.reserved -= released
			p.realized += pos.Unrealized
		}
		delete(p.positions, req.Symbol)
		return market.OrderResult{OrderID: id, Status: "filled"}, nil
	}
	p.available -= reservedMargin
	p.reserved += reservedMargin
	p.positions[req.Symbol] = market.Position{
		Symbol:         req.Symbol,
		Side:           side,
		Size:           req.Size,
		InitialSize:    req.Size,
		EntryPrice:     req.Price,
		MarkPrice:      req.Price,
		StopPrice:      req.StopPrice,
		TargetPrice:    req.TargetPrice,
		BestPrice:      req.Price,
		WorstPrice:     req.Price,
		EntryNotional:  entryNotional,
		ReservedMargin: reservedMargin,
		Leverage:       leverage,
	}
	return market.OrderResult{OrderID: id, Status: "filled"}, nil
}

func (p *Paper) MarkPrices(prices map[string]float64) {
	p.mu.Lock()
	defer p.mu.Unlock()
	for symbol, pos := range p.positions {
		mark, ok := prices[symbol]
		if !ok || mark <= 0 {
			continue
		}
		pos.MarkPrice = mark
		pos.BarsHeld++
		if pos.BestPrice <= 0 {
			pos.BestPrice = pos.EntryPrice
		}
		if pos.WorstPrice <= 0 {
			pos.WorstPrice = pos.EntryPrice
		}
		if pos.Side == market.SideLong {
			pos.BestPrice = math.Max(pos.BestPrice, mark)
			if pos.WorstPrice == 0 {
				pos.WorstPrice = mark
			} else {
				pos.WorstPrice = math.Min(pos.WorstPrice, mark)
			}
		} else {
			if pos.BestPrice == 0 {
				pos.BestPrice = mark
			} else {
				pos.BestPrice = math.Min(pos.BestPrice, mark)
			}
			pos.WorstPrice = math.Max(pos.WorstPrice, mark)
		}
		if pos.Side == market.SideLong {
			pos.Unrealized = (mark - pos.EntryPrice) * pos.Size
		} else {
			pos.Unrealized = (pos.EntryPrice - mark) * pos.Size
		}
		p.positions[symbol] = pos
	}
}

func (p *Paper) Manage(updates map[string]ManageInput) []Receipt {
	p.mu.Lock()
	defer p.mu.Unlock()
	receipts := make([]Receipt, 0)
	for symbol, pos := range p.positions {
		upd, ok := updates[symbol]
		if !ok {
			continue
		}
		if upd.Stale {
			if upd.ForceExit && pos.MarkPrice > 0 {
				receipts = append(receipts, p.closePosition(symbol, pos, pos.MarkPrice, defaultString(upd.ExitReason, "STALE_DATA_EXIT")))
			}
			continue
		}
		if upd.MarkPrice <= 0 {
			continue
		}
		pos.MarkPrice = upd.MarkPrice
		if pos.Side == market.SideLong {
			pos.BestPrice = math.Max(pos.BestPrice, upd.MarkPrice)
			pos.WorstPrice = math.Min(nonZeroOr(pos.WorstPrice, pos.EntryPrice), upd.MarkPrice)
			pos.Unrealized = (upd.MarkPrice - pos.EntryPrice) * pos.Size
		} else {
			pos.BestPrice = minNonZero(pos.BestPrice, upd.MarkPrice)
			pos.WorstPrice = math.Max(pos.WorstPrice, upd.MarkPrice)
			pos.Unrealized = (pos.EntryPrice - upd.MarkPrice) * pos.Size
		}
		if shouldStop(pos, upd.MarkPrice) {
			receipts = append(receipts, p.closePosition(symbol, pos, pos.StopPrice, "STOP_LOSS"))
			continue
		}
		if shouldTarget(pos, upd.MarkPrice) {
			if !pos.TargetHit {
				receipt, keepOpen := p.takeProfit(&pos, upd.MarkPrice, "TARGET_HIT")
				if receipt.Qty > 0 {
					receipts = append(receipts, receipt)
				}
				if keepOpen {
					p.positions[symbol] = pos
					continue
				}
			}
			receipts = append(receipts, p.closePosition(symbol, pos, pos.TargetPrice, "TARGET_HIT"))
			continue
		}
		dec := p.manager.EvaluateProtect(ProtectInput{
			Side:          string(pos.Side),
			Entry:         pos.EntryPrice,
			Stop:          pos.StopPrice,
			Mark:          upd.MarkPrice,
			MFER:          mfeR(pos),
			MAER:          maeR(pos),
			BarsHeld:      pos.BarsHeld,
			StallBars:     pos.BarsHeld,
			WeakFlow:      upd.WeakFlow,
			NearFriction:  upd.NearFriction,
			LiqSpike:      upd.LiqSpike,
			UnrealizedPct: unrealizedPct(pos),
		})
		if dec.MoveStopToBE {
			pos.StopPrice = pos.EntryPrice
		}
		if dec.TightenStop && isSaferStop(pos.Side, pos.StopPrice, dec.TightenToPrice) {
			pos.StopPrice = dec.TightenToPrice
		}
		if dec.PartialExitPct > 0 && pos.Size > 0 {
			receipts = append(receipts, p.reducePosition(&pos, upd.MarkPrice, dec.PartialExitPct, dec.Reason))
		}
		if dec.FullExit {
			receipts = append(receipts, p.closePosition(symbol, pos, upd.MarkPrice, dec.Reason))
			continue
		}
		p.positions[symbol] = pos
	}
	return receipts
}

func (p *Paper) Snapshot() Snapshot {
	p.mu.Lock()
	defer p.mu.Unlock()
	openPnL := 0.0
	for _, pos := range p.positions {
		openPnL += pos.Unrealized
	}
	return Snapshot{
		Equity:           p.available + p.reserved + openPnL,
		Balance:          p.available + p.reserved,
		Available:        p.available,
		Reserved:         p.reserved,
		DeployedNotional: deployedNotional(p.positions),
		OpenPnL:          openPnL,
		DayRealized:      p.realized,
		OpenCount:        len(p.positions),
	}
}

func (p *Paper) CancelOrder(symbol string, orderID string) error {
	return nil
}

func (p *Paper) OpenOrders(symbol string) ([]market.Order, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := make([]market.Order, 0, len(p.orders))
	for _, order := range p.orders {
		if symbol == "" || order.Symbol == symbol {
			out = append(out, order)
		}
	}
	return out, nil
}

func (p *Paper) closePosition(symbol string, pos market.Position, fillPrice float64, reason string) Receipt {
	realized := realizedPnL(pos, pos.Size, fillPrice)
	p.realized += realized
	p.available += pos.ReservedMargin + realized
	p.reserved -= pos.ReservedMargin
	if p.reserved < 0 {
		p.reserved = 0
	}
	delete(p.positions, symbol)
	return Receipt{
		Symbol:      symbol,
		Side:        pos.Side,
		Qty:         pos.Size,
		FillPrice:   fillPrice,
		Realized:    realized,
		RealizedPct: realizedPct(pos, fillPrice),
		Reason:      reason,
	}
}

func (p *Paper) reducePosition(pos *market.Position, fillPrice float64, frac float64, reason string) Receipt {
	if frac <= 0 {
		return Receipt{}
	}
	if frac > 1 {
		frac = 1
	}
	qty := pos.Size * frac
	if qty <= 0 {
		return Receipt{}
	}
	realized := realizedPnL(*pos, qty, fillPrice)
	p.realized += realized
	releasedMargin := pos.ReservedMargin * frac
	p.available += releasedMargin + realized
	p.reserved -= releasedMargin
	if p.reserved < 0 {
		p.reserved = 0
	}
	pos.ReservedMargin -= releasedMargin
	pos.EntryNotional -= pos.EntryPrice * qty
	pos.Realized += realized
	pos.Size -= qty
	if pos.Size < 0 {
		pos.Size = 0
	}
	return Receipt{
		Symbol:      pos.Symbol,
		Side:        pos.Side,
		Qty:         qty,
		FillPrice:   fillPrice,
		Realized:    realized,
		RealizedPct: realizedPct(*pos, fillPrice),
		Reason:      reason,
	}
}

func (p *Paper) takeProfit(pos *market.Position, fillPrice float64, reason string) (Receipt, bool) {
	if pos.TargetHit || pos.Size <= 0 {
		return Receipt{}, false
	}
	if pos.Size <= 1 {
		return Receipt{}, false
	}
	pos.TargetHit = true
	pos.StopPrice = pos.EntryPrice
	receipt := p.reducePosition(pos, fillPrice, 0.50, reason+"_PARTIAL")
	return receipt, pos.Size > 0
}

func shouldStop(pos market.Position, mark float64) bool {
	if pos.StopPrice <= 0 {
		return false
	}
	if pos.Side == market.SideLong {
		return mark <= pos.StopPrice
	}
	return mark >= pos.StopPrice
}

func shouldTarget(pos market.Position, mark float64) bool {
	if pos.TargetPrice <= 0 {
		return false
	}
	if pos.Side == market.SideLong {
		return mark >= pos.TargetPrice
	}
	return mark <= pos.TargetPrice
}

func mfeR(pos market.Position) float64 {
	risk := math.Abs(pos.EntryPrice - pos.StopPrice)
	if risk <= 0 {
		return 0
	}
	if pos.Side == market.SideLong {
		return math.Max(0, pos.BestPrice-pos.EntryPrice) / risk
	}
	return math.Max(0, pos.EntryPrice-pos.BestPrice) / risk
}

func maeR(pos market.Position) float64 {
	risk := math.Abs(pos.EntryPrice - pos.StopPrice)
	if risk <= 0 {
		return 0
	}
	if pos.Side == market.SideLong {
		return math.Max(0, pos.EntryPrice-pos.WorstPrice) / risk
	}
	return math.Max(0, pos.WorstPrice-pos.EntryPrice) / risk
}

func unrealizedPct(pos market.Position) float64 {
	notional := pos.EntryPrice * pos.Size
	if notional <= 0 {
		return 0
	}
	return (pos.Unrealized / notional) * 100
}

func realizedPnL(pos market.Position, qty, fillPrice float64) float64 {
	if pos.Side == market.SideLong {
		return (fillPrice - pos.EntryPrice) * qty
	}
	return (pos.EntryPrice - fillPrice) * qty
}

func realizedPct(pos market.Position, fillPrice float64) float64 {
	if pos.EntryPrice <= 0 {
		return 0
	}
	if pos.Side == market.SideLong {
		return ((fillPrice - pos.EntryPrice) / pos.EntryPrice) * 100
	}
	return ((pos.EntryPrice - fillPrice) / pos.EntryPrice) * 100
}

func isSaferStop(side market.Side, current, next float64) bool {
	if current <= 0 || next <= 0 {
		return false
	}
	if side == market.SideLong {
		return next > current
	}
	return next < current
}

func nonZeroOr(v, fallback float64) float64 {
	if v != 0 {
		return v
	}
	return fallback
}

func minNonZero(a, b float64) float64 {
	if a == 0 {
		return b
	}
	return math.Min(a, b)
}

func deployedNotional(positions map[string]market.Position) float64 {
	total := 0.0
	for _, pos := range positions {
		total += pos.MarkPrice * pos.Size
	}
	return total
}

func defaultString(v, fallback string) string {
	if v != "" {
		return v
	}
	return fallback
}
