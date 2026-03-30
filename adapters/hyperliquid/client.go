package hyperliquid

import (
	"bytes"
	"crypto/ecdsa"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"math/big"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/vmihailenco/msgpack/v5"

	"hyperliquid/internal/market"
)

const (
	MainnetURL = "https://api.hyperliquid.xyz"
	TestnetURL = "https://api.hyperliquid-testnet.xyz"
)

var (
	secpNonce uint64

	bytes32Type = mustABIType("bytes32")
	stringType  = mustABIType("string")
	uint256Type = mustABIType("uint256")
	addressType = mustABIType("address")

	eip712DomainTypeHash = crypto.Keccak256Hash([]byte("EIP712Domain(string name,string version,uint256 chainId,address verifyingContract)"))
	agentTypeHash        = crypto.Keccak256Hash([]byte("Agent(string source,bytes32 connectionId)"))
	exchangeNameHash     = crypto.Keccak256Hash([]byte("Exchange"))
	versionHash          = crypto.Keccak256Hash([]byte("1"))
	zeroAddress          = common.HexToAddress("0x0000000000000000000000000000000000000000")
)

type Client struct {
	baseURL      string
	user         string
	http         *http.Client
	metaMu       sync.RWMutex
	metaLoaded   bool
	meta         map[string]market.Market
	assetIDs     map[string]int
	privateKey   *ecdsa.PrivateKey
	account      common.Address
	vaultAddress *common.Address
}

func New(baseURL string) *Client {
	return NewWithUser(baseURL, "")
}

func NewWithUser(baseURL, user string) *Client {
	if baseURL == "" {
		baseURL = MainnetURL
	}
	priv, acct, vault := loadSignerFromEnv()
	trimmedUser := strings.TrimSpace(user)
	if trimmedUser == "" && acct != (common.Address{}) {
		trimmedUser = acct.Hex()
	}
	return &Client{
		baseURL:      baseURL,
		user:         trimmedUser,
		http:         &http.Client{Timeout: 15 * time.Second},
		meta:         map[string]market.Market{},
		assetIDs:     map[string]int{},
		privateKey:   priv,
		account:      acct,
		vaultAddress: vault,
	}
}

func (c *Client) ConfiguredForLive() bool {
	return c.privateKey != nil
}

func (c *Client) FetchAllMarkets() ([]market.Market, error) {
	meta, ctxs, err := c.metaAndAssetCtxs()
	if err != nil {
		return nil, err
	}
	markets := make([]market.Market, 0, len(meta.Universe))
	cache := make(map[string]market.Market, len(meta.Universe))
	assetIDs := make(map[string]int, len(meta.Universe))
	for i, asset := range meta.Universe {
		if asset.IsDelisted || i >= len(ctxs) {
			continue
		}
		ctx := ctxs[i]
		item := market.Market{
			Symbol:        asset.Name,
			MarkPrice:     mustFloat(ctx.MarkPx),
			OraclePrice:   mustFloat(ctx.OraclePx),
			Volume24hUSD:  mustFloat(ctx.DayNtlVlm),
			OpenInterest:  mustFloat(ctx.OpenInterest),
			FundingRate:   mustFloat(ctx.Funding),
			MaxLeverage:   asset.MaxLeverage,
			PriceDecimals: decimalsFromTick(asset.TickSize),
			SizeDecimals:  asset.SzDecimals,
			TickSize:      mustFloat(asset.TickSize),
			MinSize:       0,
			MinNotional:   0,
		}
		markets = append(markets, item)
		cache[item.Symbol] = item
		assetIDs[item.Symbol] = i
	}
	c.metaMu.Lock()
	c.meta = cache
	c.assetIDs = assetIDs
	c.metaLoaded = true
	c.metaMu.Unlock()
	return markets, nil
}

func (c *Client) LoadCandles(symbol, tf string, n int) ([]market.Candle, error) {
	if n <= 0 {
		n = 100
	}
	end := time.Now().UTC()
	var step time.Duration
	switch tf {
	case "1m":
		step = time.Minute
	case "5m":
		step = 5 * time.Minute
	case "15m":
		step = 15 * time.Minute
	case "1h":
		step = time.Hour
	default:
		step = time.Hour
	}
	start := end.Add(-time.Duration(n) * step)
	req := map[string]any{
		"type": "candleSnapshot",
		"req": map[string]any{
			"coin":      symbol,
			"interval":  tf,
			"startTime": start.UnixMilli(),
			"endTime":   end.UnixMilli(),
		},
	}
	var out []struct {
		T int64  `json:"t"`
		O string `json:"o"`
		H string `json:"h"`
		L string `json:"l"`
		C string `json:"c"`
		V string `json:"v"`
	}
	if err := c.postInfo(req, &out); err != nil {
		return nil, err
	}
	candles := make([]market.Candle, 0, len(out))
	for _, item := range out {
		candles = append(candles, market.Candle{
			Time:   time.UnixMilli(item.T),
			Open:   mustFloat(item.O),
			High:   mustFloat(item.H),
			Low:    mustFloat(item.L),
			Close:  mustFloat(item.C),
			Volume: mustFloat(item.V),
		})
	}
	return candles, nil
}

func (c *Client) FetchOrderBook(symbol string, levels int) (market.OrderBook, error) {
	req := map[string]any{"type": "l2Book", "coin": symbol}
	var out struct {
		Levels [][]struct {
			Px string `json:"px"`
			Sz string `json:"sz"`
		} `json:"levels"`
	}
	if err := c.postInfo(req, &out); err != nil {
		return market.OrderBook{}, err
	}
	book := market.OrderBook{Symbol: symbol}
	for sideIdx, side := range out.Levels {
		for i, level := range side {
			if levels > 0 && i >= levels {
				break
			}
			item := market.BookLevel{Price: mustFloat(level.Px), Size: mustFloat(level.Sz)}
			if sideIdx == 0 {
				book.Bids = append(book.Bids, item)
			} else {
				book.Asks = append(book.Asks, item)
			}
		}
	}
	return book, nil
}

func (c *Client) Balance() (market.AccountSnapshot, error) {
	if c.user == "" {
		return market.AccountSnapshot{}, fmt.Errorf("hyperliquid user address not configured")
	}
	state, err := c.clearinghouseState(c.user)
	if err != nil {
		return market.AccountSnapshot{}, err
	}
	return market.AccountSnapshot{
		Equity:       mustFloat(state.MarginSummary.AccountValue),
		AvailableUSD: mustFloat(state.Withdrawable),
	}, nil
}

func (c *Client) Positions() ([]market.Position, error) {
	if c.user == "" {
		return nil, fmt.Errorf("hyperliquid user address not configured")
	}
	state, err := c.clearinghouseState(c.user)
	if err != nil {
		return nil, err
	}
	positions := make([]market.Position, 0, len(state.AssetPositions))
	for _, wrapped := range state.AssetPositions {
		pos := wrapped.Position
		size := mustFloat(pos.Szi)
		if size == 0 {
			continue
		}
		side := market.SideLong
		if size < 0 {
			side = market.SideShort
			size = math.Abs(size)
		}
		positions = append(positions, market.Position{
			Symbol:     pos.Coin,
			Side:       side,
			Size:       size,
			EntryPrice: mustFloat(pos.EntryPx),
			MarkPrice:  mustFloat(pos.MarkPx),
			Unrealized: mustFloat(pos.UnrealizedPnl),
		})
	}
	return positions, nil
}

func (c *Client) PlaceOrder(req market.OrderRequest) (market.OrderResult, error) {
	if !c.ConfiguredForLive() {
		return market.OrderResult{}, fmt.Errorf("hyperliquid live signing not configured; set HL_PRIVATE_KEY or HYPERLIQUID_PRIVATE_KEY")
	}
	if err := c.ValidateOrderRequest(req); err != nil {
		return market.OrderResult{}, err
	}
	entryResult, err := c.placeSingleOrder(req)
	if err != nil {
		return market.OrderResult{}, err
	}
	if req.ReduceOnly || (req.StopPrice <= 0 && req.TargetPrice <= 0) {
		return entryResult, nil
	}
	if req.StopPrice > 0 {
		stopID, err := c.placeProtectiveTrigger(req, req.StopPrice, "sl")
		if err != nil {
			return entryResult, fmt.Errorf("entry placed but stop trigger failed: %w", err)
		}
		entryResult.StopOrderID = stopID
	}
	if req.TargetPrice > 0 {
		targetID, err := c.placeProtectiveTrigger(req, req.TargetPrice, "tp")
		if err != nil {
			return entryResult, fmt.Errorf("entry placed but target trigger failed: %w", err)
		}
		entryResult.TargetOrderID = targetID
	}
	return entryResult, nil
}

func (c *Client) CancelOrder(symbol string, orderID string) error {
	if !c.ConfiguredForLive() {
		return fmt.Errorf("hyperliquid live signing not configured")
	}
	assetID, ok, err := c.AssetID(symbol)
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("unknown symbol: %s", symbol)
	}
	oid, err := strconv.ParseUint(strings.TrimSpace(orderID), 10, 64)
	if err != nil {
		return fmt.Errorf("invalid hyperliquid order id %q: %w", orderID, err)
	}
	action := hlCancelAction{
		Type: "cancel",
		Cancels: []hlCancel{
			{Asset: assetID, OID: oid},
		},
	}
	if _, err := c.postExchangeAction(action); err != nil {
		return err
	}
	return nil
}

func (c *Client) OpenOrders(symbol string) ([]market.Order, error) {
	if c.user == "" {
		return nil, fmt.Errorf("hyperliquid user address not configured")
	}
	req := map[string]any{"type": "openOrders", "user": c.user}
	var out []struct {
		Coin           string `json:"coin"`
		Side           string `json:"side"`
		LimitPx        string `json:"limitPx"`
		Sz             string `json:"sz"`
		Oid            any    `json:"oid"`
		Cloid          any    `json:"cloid"`
		ReduceOnly     bool   `json:"reduceOnly"`
		IsTrigger      bool   `json:"isTrigger"`
		TriggerPx      string `json:"triggerPx"`
		IsPositionTPSL bool   `json:"isPositionTpsl"`
		OrderType      string `json:"orderType"`
		TIF            string `json:"tif"`
	}
	if err := c.postInfo(req, &out); err != nil {
		return nil, err
	}
	orders := make([]market.Order, 0, len(out))
	for _, item := range out {
		if symbol != "" && item.Coin != symbol {
			continue
		}
		side := market.SideLong
		if item.Side == "A" {
			side = market.SideShort
		}
		orders = append(orders, market.Order{
			ID:          fmt.Sprintf("%v", item.Oid),
			ClientOrder: fmt.Sprintf("%v", item.Cloid),
			Symbol:      item.Coin,
			Side:        side,
			Size:        mustFloat(item.Sz),
			Price:       mustFloat(item.LimitPx),
			Status:      "open",
			ReduceOnly:  item.ReduceOnly,
			IsTrigger:   item.IsTrigger || item.IsPositionTPSL,
			TriggerPx:   mustFloat(item.TriggerPx),
			TriggerKind: triggerKindFromOrderType(item.OrderType),
			OrderType:   item.OrderType,
			TIF:         item.TIF,
		})
	}
	return orders, nil
}

func (c *Client) EnsureProtection(pos market.Position, stopPx, targetPx float64, openOrders []market.Order) (string, string, []string, error) {
	stopID, targetID := findProtectionOrders(openOrders)
	warnings := make([]string, 0)
	req := market.OrderRequest{
		Symbol:     pos.Symbol,
		Side:       pos.Side,
		Size:       pos.Size,
		Price:      pos.MarkPrice,
		ReduceOnly: true,
		Type:       "market",
	}
	if stopPx > 0 && stopID == "" {
		id, err := c.placeProtectiveTrigger(req, stopPx, "sl")
		if err != nil {
			return stopID, targetID, warnings, err
		}
		stopID = id
		warnings = append(warnings, fmt.Sprintf("%s repaired stop trigger", pos.Symbol))
	}
	if targetPx > 0 && targetID == "" {
		id, err := c.placeProtectiveTrigger(req, targetPx, "tp")
		if err != nil {
			return stopID, targetID, warnings, err
		}
		targetID = id
		warnings = append(warnings, fmt.Sprintf("%s repaired target trigger", pos.Symbol))
	}
	return stopID, targetID, warnings, nil
}

func (c *Client) MarketMeta(symbol string) (market.Market, bool, error) {
	if err := c.ensureMeta(); err != nil {
		return market.Market{}, false, err
	}
	c.metaMu.RLock()
	defer c.metaMu.RUnlock()
	item, ok := c.meta[symbol]
	return item, ok, nil
}

func (c *Client) AssetID(symbol string) (int, bool, error) {
	if err := c.ensureMeta(); err != nil {
		return 0, false, err
	}
	c.metaMu.RLock()
	defer c.metaMu.RUnlock()
	id, ok := c.assetIDs[symbol]
	return id, ok, nil
}

func (c *Client) RoundSize(symbol string, size float64) (float64, error) {
	meta, ok, err := c.MarketMeta(symbol)
	if err != nil {
		return 0, err
	}
	if !ok {
		return 0, fmt.Errorf("unknown symbol: %s", symbol)
	}
	pow := math.Pow10(meta.SizeDecimals)
	return math.Round(size*pow) / pow, nil
}

func (c *Client) RoundPrice(symbol string, price float64) (float64, error) {
	meta, ok, err := c.MarketMeta(symbol)
	if err != nil {
		return 0, err
	}
	if !ok {
		return 0, fmt.Errorf("unknown symbol: %s", symbol)
	}
	if meta.TickSize > 0 {
		return math.Round(price/meta.TickSize) * meta.TickSize, nil
	}
	if meta.PriceDecimals <= 0 {
		return price, nil
	}
	pow := math.Pow10(meta.PriceDecimals)
	return math.Round(price*pow) / pow, nil
}

func (c *Client) ValidateOrderRequest(req market.OrderRequest) error {
	meta, ok, err := c.MarketMeta(req.Symbol)
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("unknown symbol: %s", req.Symbol)
	}
	if req.Size <= 0 {
		return fmt.Errorf("size must be positive")
	}
	if req.Type != "market" && req.Price <= 0 {
		return fmt.Errorf("price must be positive for non-market orders")
	}
	roundedSize, err := c.RoundSize(req.Symbol, req.Size)
	if err != nil {
		return err
	}
	if roundedSize != req.Size {
		return fmt.Errorf("size %.12f exceeds precision for %s", req.Size, req.Symbol)
	}
	if req.Type != "market" {
		roundedPrice, err := c.RoundPrice(req.Symbol, req.Price)
		if err != nil {
			return err
		}
		if math.Abs(roundedPrice-req.Price) > 1e-12 {
			return fmt.Errorf("price %.12f exceeds tick/precision for %s", req.Price, req.Symbol)
		}
	}
	if meta.MinSize > 0 && req.Size < meta.MinSize {
		return fmt.Errorf("size %.8f below min size %.8f", req.Size, meta.MinSize)
	}
	if meta.MinNotional > 0 && req.Size*req.Price < meta.MinNotional {
		return fmt.Errorf("notional %.8f below min notional %.8f", req.Size*req.Price, meta.MinNotional)
	}
	return nil
}

type metaResponse struct {
	Universe []struct {
		Name         string  `json:"name"`
		SzDecimals   int     `json:"szDecimals"`
		MaxLeverage  float64 `json:"maxLeverage"`
		OnlyIsolated bool    `json:"onlyIsolated"`
		IsDelisted   bool    `json:"isDelisted"`
		TickSize     string  `json:"tickSize"`
	} `json:"universe"`
}

type assetCtx struct {
	Funding      string `json:"funding"`
	OpenInterest string `json:"openInterest"`
	MarkPx       string `json:"markPx"`
	OraclePx     string `json:"oraclePx"`
	DayNtlVlm    string `json:"dayNtlVlm"`
}

type clearinghouseStateResponse struct {
	MarginSummary struct {
		AccountValue string `json:"accountValue"`
	} `json:"marginSummary"`
	Withdrawable   string `json:"withdrawable"`
	AssetPositions []struct {
		Position struct {
			Coin          string `json:"coin"`
			Szi           string `json:"szi"`
			EntryPx       string `json:"entryPx"`
			MarkPx        string `json:"markPx"`
			UnrealizedPnl string `json:"unrealizedPnl"`
		} `json:"position"`
	} `json:"assetPositions"`
}

type hlLimit struct {
	TIF string `json:"tif" msgpack:"tif"`
}

type hlTrigger struct {
	IsMarket  bool   `json:"isMarket" msgpack:"isMarket"`
	TriggerPx string `json:"triggerPx" msgpack:"triggerPx"`
	TPSL      string `json:"tpsl" msgpack:"tpsl"`
}

type hlOrderType struct {
	Limit   *hlLimit   `json:"limit,omitempty" msgpack:"limit,omitempty"`
	Trigger *hlTrigger `json:"trigger,omitempty" msgpack:"trigger,omitempty"`
}

type hlOrder struct {
	Asset      int         `json:"a" msgpack:"a"`
	IsBuy      bool        `json:"b" msgpack:"b"`
	LimitPx    string      `json:"p" msgpack:"p"`
	Size       string      `json:"s" msgpack:"s"`
	ReduceOnly bool        `json:"r" msgpack:"r"`
	OrderType  hlOrderType `json:"t" msgpack:"t"`
	Cloid      string      `json:"c,omitempty" msgpack:"c,omitempty"`
}

type hlOrderAction struct {
	Type     string    `json:"type" msgpack:"type"`
	Orders   []hlOrder `json:"orders" msgpack:"orders"`
	Grouping string    `json:"grouping" msgpack:"grouping"`
}

type hlCancel struct {
	Asset int    `json:"a" msgpack:"a"`
	OID   uint64 `json:"o" msgpack:"o"`
}

type hlCancelAction struct {
	Type    string     `json:"type" msgpack:"type"`
	Cancels []hlCancel `json:"cancels" msgpack:"cancels"`
}

type hlSignature struct {
	R string `json:"r"`
	S string `json:"s"`
	V uint8  `json:"v"`
}

type exchangePayload struct {
	Action       any         `json:"action"`
	Nonce        uint64      `json:"nonce"`
	Signature    hlSignature `json:"signature"`
	VaultAddress *string     `json:"vaultAddress,omitempty"`
}

type exchangeEnvelope struct {
	Status   string          `json:"status"`
	Response json.RawMessage `json:"response"`
}

type exchangeOK struct {
	Type string `json:"type"`
	Data *struct {
		Statuses []json.RawMessage `json:"statuses"`
	} `json:"data"`
}

func (c *Client) metaAndAssetCtxs() (metaResponse, []assetCtx, error) {
	req := map[string]any{"type": "metaAndAssetCtxs"}
	var out []json.RawMessage
	if err := c.postInfo(req, &out); err != nil {
		return metaResponse{}, nil, err
	}
	if len(out) != 2 {
		return metaResponse{}, nil, fmt.Errorf("unexpected metaAndAssetCtxs shape")
	}
	var meta metaResponse
	var ctxs []assetCtx
	if err := json.Unmarshal(out[0], &meta); err != nil {
		return metaResponse{}, nil, err
	}
	if err := json.Unmarshal(out[1], &ctxs); err != nil {
		return metaResponse{}, nil, err
	}
	return meta, ctxs, nil
}

func (c *Client) postInfo(payload any, out any) error {
	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	var lastErr error
	for _, wait := range []time.Duration{0, 250 * time.Millisecond, 750 * time.Millisecond, 1500 * time.Millisecond} {
		if wait > 0 {
			time.Sleep(wait)
		}
		resp, err := c.http.Post(c.baseURL+"/info", "application/json", bytes.NewReader(body))
		if err != nil {
			lastErr = err
			continue
		}
		raw, readErr := io.ReadAll(resp.Body)
		resp.Body.Close()
		if readErr != nil {
			lastErr = readErr
			continue
		}
		if resp.StatusCode >= 200 && resp.StatusCode < 300 {
			if err := json.Unmarshal(raw, out); err != nil {
				return err
			}
			return nil
		}
		lastErr = fmt.Errorf("hyperliquid info http %d: %s", resp.StatusCode, string(raw))
		if resp.StatusCode != http.StatusTooManyRequests && resp.StatusCode < 500 {
			return lastErr
		}
		if !strings.Contains(lastErr.Error(), "429") && resp.StatusCode < 500 {
			return lastErr
		}
	}
	return lastErr
}

func (c *Client) ensureMeta() error {
	c.metaMu.RLock()
	loaded := c.metaLoaded
	c.metaMu.RUnlock()
	if loaded {
		return nil
	}
	_, err := c.FetchAllMarkets()
	return err
}

func (c *Client) clearinghouseState(user string) (clearinghouseStateResponse, error) {
	req := map[string]any{"type": "clearinghouseState", "user": user}
	var out clearinghouseStateResponse
	if err := c.postInfo(req, &out); err != nil {
		return clearinghouseStateResponse{}, err
	}
	return out, nil
}

func (c *Client) placeSingleOrder(req market.OrderRequest) (market.OrderResult, error) {
	assetID, ok, err := c.AssetID(req.Symbol)
	if err != nil {
		return market.OrderResult{}, err
	}
	if !ok {
		return market.OrderResult{}, fmt.Errorf("unknown symbol: %s", req.Symbol)
	}
	size, err := c.RoundSize(req.Symbol, req.Size)
	if err != nil {
		return market.OrderResult{}, err
	}
	px := req.Price
	orderType := hlOrderType{
		Limit: &hlLimit{TIF: "Gtc"},
	}
	if strings.EqualFold(req.Type, "market") {
		mktPx, err := c.marketIOCPrice(req.Symbol, req.Side == market.SideLong, px)
		if err != nil {
			return market.OrderResult{}, err
		}
		px = mktPx
		orderType = hlOrderType{
			Limit: &hlLimit{TIF: "Ioc"},
		}
	} else {
		px, err = c.RoundPrice(req.Symbol, px)
		if err != nil {
			return market.OrderResult{}, err
		}
	}
	action := hlOrderAction{
		Type:     "order",
		Grouping: "na",
		Orders: []hlOrder{{
			Asset:      assetID,
			IsBuy:      req.Side == market.SideLong,
			LimitPx:    floatToWire(px),
			Size:       floatToWire(size),
			ReduceOnly: req.ReduceOnly,
			OrderType:  orderType,
			Cloid:      normalizeCloid(req.ClientOrder),
		}},
	}
	resp, err := c.postExchangeAction(action)
	if err != nil {
		return market.OrderResult{}, err
	}
	oid, status, err := parseFirstOrderStatus(resp)
	if err != nil {
		return market.OrderResult{}, err
	}
	return market.OrderResult{OrderID: oid, Status: status}, nil
}

func (c *Client) placeProtectiveTrigger(req market.OrderRequest, triggerPx float64, tpsl string) (string, error) {
	assetID, ok, err := c.AssetID(req.Symbol)
	if err != nil {
		return "", err
	}
	if !ok {
		return "", fmt.Errorf("unknown symbol: %s", req.Symbol)
	}
	size, err := c.RoundSize(req.Symbol, req.Size)
	if err != nil {
		return "", err
	}
	triggerPx, err = c.RoundPrice(req.Symbol, triggerPx)
	if err != nil {
		return "", err
	}
	exitSide := req.Side == market.SideShort
	action := hlOrderAction{
		Type:     "order",
		Grouping: "na",
		Orders: []hlOrder{{
			Asset:      assetID,
			IsBuy:      exitSide,
			LimitPx:    floatToWire(triggerPx),
			Size:       floatToWire(size),
			ReduceOnly: true,
			OrderType: hlOrderType{
				Trigger: &hlTrigger{
					IsMarket:  true,
					TriggerPx: floatToWire(triggerPx),
					TPSL:      tpsl,
				},
			},
		}},
	}
	resp, err := c.postExchangeAction(action)
	if err != nil {
		return "", err
	}
	oid, _, err := parseFirstOrderStatus(resp)
	return oid, err
}

func (c *Client) postExchangeAction(action any) (exchangeOK, error) {
	nonce := nextNonce()
	connectionID, err := hashAction(action, nonce, c.vaultAddress)
	if err != nil {
		return exchangeOK{}, err
	}
	sig, err := signL1Action(c.privateKey, connectionID, c.baseURL == MainnetURL)
	if err != nil {
		return exchangeOK{}, err
	}
	payload := exchangePayload{
		Action:    action,
		Nonce:     nonce,
		Signature: sig,
	}
	if c.vaultAddress != nil {
		addr := c.vaultAddress.Hex()
		payload.VaultAddress = &addr
	}
	rawBody, err := json.Marshal(payload)
	if err != nil {
		return exchangeOK{}, err
	}
	var lastErr error
	for _, wait := range []time.Duration{0, 250 * time.Millisecond, 750 * time.Millisecond, 1500 * time.Millisecond} {
		if wait > 0 {
			time.Sleep(wait)
		}
		resp, err := c.http.Post(c.baseURL+"/exchange", "application/json", bytes.NewReader(rawBody))
		if err != nil {
			lastErr = err
			continue
		}
		raw, readErr := io.ReadAll(resp.Body)
		resp.Body.Close()
		if readErr != nil {
			lastErr = readErr
			continue
		}
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			lastErr = fmt.Errorf("hyperliquid exchange http %d: %s", resp.StatusCode, string(raw))
			if resp.StatusCode != http.StatusTooManyRequests && resp.StatusCode < 500 {
				return exchangeOK{}, lastErr
			}
			continue
		}
		var env exchangeEnvelope
		if err := json.Unmarshal(raw, &env); err != nil {
			return exchangeOK{}, err
		}
		if strings.EqualFold(env.Status, "err") {
			var msg string
			_ = json.Unmarshal(env.Response, &msg)
			if strings.TrimSpace(msg) == "" {
				msg = string(env.Response)
			}
			return exchangeOK{}, fmt.Errorf("hyperliquid exchange error: %s", msg)
		}
		var ok exchangeOK
		if err := json.Unmarshal(env.Response, &ok); err != nil {
			return exchangeOK{}, err
		}
		return ok, nil
	}
	return exchangeOK{}, lastErr
}

func parseFirstOrderStatus(resp exchangeOK) (string, string, error) {
	if resp.Data == nil || len(resp.Data.Statuses) == 0 {
		return "", "accepted", nil
	}
	raw := resp.Data.Statuses[0]
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(raw, &obj); err != nil {
		return "", "", err
	}
	if item, ok := obj["resting"]; ok {
		var resting struct {
			OID uint64 `json:"oid"`
		}
		if err := json.Unmarshal(item, &resting); err != nil {
			return "", "", err
		}
		return strconv.FormatUint(resting.OID, 10), "resting", nil
	}
	if item, ok := obj["filled"]; ok {
		var filled struct {
			OID uint64 `json:"oid"`
		}
		if err := json.Unmarshal(item, &filled); err != nil {
			return "", "", err
		}
		return strconv.FormatUint(filled.OID, 10), "filled", nil
	}
	if item, ok := obj["error"]; ok {
		var msg string
		_ = json.Unmarshal(item, &msg)
		return "", "", fmt.Errorf("hyperliquid exchange error: %s", msg)
	}
	if _, ok := obj["success"]; ok {
		return "", "success", nil
	}
	return "", "accepted", nil
}

func (c *Client) marketIOCPrice(symbol string, isBuy bool, current float64) (float64, error) {
	meta, ok, err := c.MarketMeta(symbol)
	if err != nil {
		return 0, err
	}
	if !ok {
		return 0, fmt.Errorf("unknown symbol: %s", symbol)
	}
	if current <= 0 {
		current = meta.MarkPrice
	}
	if current <= 0 {
		current = meta.OraclePrice
	}
	if current <= 0 {
		return 0, fmt.Errorf("missing price for %s", symbol)
	}
	slippage := 0.05
	if isBuy {
		current *= 1 + slippage
	} else {
		current *= 1 - slippage
	}
	return roundToSignificantAndDecimal(current, 5, maxInt(0, 6-meta.SizeDecimals)), nil
}

func hashAction(action any, nonce uint64, vault *common.Address) (common.Hash, error) {
	packed, err := encodeAction(action)
	if err != nil {
		return common.Hash{}, err
	}
	buf := make([]byte, 0, len(packed)+8+1+20)
	buf = append(buf, packed...)
	nonceBytes := make([]byte, 8)
	binary.BigEndian.PutUint64(nonceBytes, nonce)
	buf = append(buf, nonceBytes...)
	if vault == nil {
		buf = append(buf, 0)
	} else {
		buf = append(buf, 1)
		buf = append(buf, vault.Bytes()...)
	}
	return crypto.Keccak256Hash(buf), nil
}

func encodeAction(action any) ([]byte, error) {
	var buf bytes.Buffer
	enc := msgpack.NewEncoder(&buf)
	switch a := action.(type) {
	case hlOrderAction:
		if err := enc.EncodeMapLen(3); err != nil {
			return nil, err
		}
		if err := enc.EncodeString("type"); err != nil {
			return nil, err
		}
		if err := enc.EncodeString(a.Type); err != nil {
			return nil, err
		}
		if err := enc.EncodeString("orders"); err != nil {
			return nil, err
		}
		if err := enc.EncodeArrayLen(len(a.Orders)); err != nil {
			return nil, err
		}
		for _, order := range a.Orders {
			if err := encodeOrder(enc, order); err != nil {
				return nil, err
			}
		}
		if err := enc.EncodeString("grouping"); err != nil {
			return nil, err
		}
		if err := enc.EncodeString(a.Grouping); err != nil {
			return nil, err
		}
	case hlCancelAction:
		if err := enc.EncodeMapLen(2); err != nil {
			return nil, err
		}
		if err := enc.EncodeString("type"); err != nil {
			return nil, err
		}
		if err := enc.EncodeString(a.Type); err != nil {
			return nil, err
		}
		if err := enc.EncodeString("cancels"); err != nil {
			return nil, err
		}
		if err := enc.EncodeArrayLen(len(a.Cancels)); err != nil {
			return nil, err
		}
		for _, cancel := range a.Cancels {
			if err := enc.EncodeMapLen(2); err != nil {
				return nil, err
			}
			if err := enc.EncodeString("a"); err != nil {
				return nil, err
			}
			if err := enc.EncodeInt(int64(cancel.Asset)); err != nil {
				return nil, err
			}
			if err := enc.EncodeString("o"); err != nil {
				return nil, err
			}
			if err := enc.EncodeUint(cancel.OID); err != nil {
				return nil, err
			}
		}
	default:
		return nil, fmt.Errorf("unsupported action type %T", action)
	}
	return buf.Bytes(), nil
}

func encodeOrder(enc *msgpack.Encoder, order hlOrder) error {
	fieldCount := 6
	if strings.TrimSpace(order.Cloid) != "" {
		fieldCount++
	}
	if err := enc.EncodeMapLen(fieldCount); err != nil {
		return err
	}
	if err := enc.EncodeString("a"); err != nil {
		return err
	}
	if err := enc.EncodeInt(int64(order.Asset)); err != nil {
		return err
	}
	if err := enc.EncodeString("b"); err != nil {
		return err
	}
	if err := enc.EncodeBool(order.IsBuy); err != nil {
		return err
	}
	if err := enc.EncodeString("p"); err != nil {
		return err
	}
	if err := enc.EncodeString(order.LimitPx); err != nil {
		return err
	}
	if err := enc.EncodeString("s"); err != nil {
		return err
	}
	if err := enc.EncodeString(order.Size); err != nil {
		return err
	}
	if err := enc.EncodeString("r"); err != nil {
		return err
	}
	if err := enc.EncodeBool(order.ReduceOnly); err != nil {
		return err
	}
	if err := enc.EncodeString("t"); err != nil {
		return err
	}
	if err := encodeOrderType(enc, order.OrderType); err != nil {
		return err
	}
	if strings.TrimSpace(order.Cloid) != "" {
		if err := enc.EncodeString("c"); err != nil {
			return err
		}
		if err := enc.EncodeString(order.Cloid); err != nil {
			return err
		}
	}
	return nil
}

func encodeOrderType(enc *msgpack.Encoder, orderType hlOrderType) error {
	if orderType.Limit != nil {
		if err := enc.EncodeMapLen(1); err != nil {
			return err
		}
		if err := enc.EncodeString("limit"); err != nil {
			return err
		}
		if err := enc.EncodeMapLen(1); err != nil {
			return err
		}
		if err := enc.EncodeString("tif"); err != nil {
			return err
		}
		return enc.EncodeString(orderType.Limit.TIF)
	}
	if orderType.Trigger != nil {
		if err := enc.EncodeMapLen(1); err != nil {
			return err
		}
		if err := enc.EncodeString("trigger"); err != nil {
			return err
		}
		if err := enc.EncodeMapLen(3); err != nil {
			return err
		}
		if err := enc.EncodeString("isMarket"); err != nil {
			return err
		}
		if err := enc.EncodeBool(orderType.Trigger.IsMarket); err != nil {
			return err
		}
		if err := enc.EncodeString("triggerPx"); err != nil {
			return err
		}
		if err := enc.EncodeString(orderType.Trigger.TriggerPx); err != nil {
			return err
		}
		if err := enc.EncodeString("tpsl"); err != nil {
			return err
		}
		return enc.EncodeString(orderType.Trigger.TPSL)
	}
	return fmt.Errorf("missing order type")
}

func signL1Action(priv *ecdsa.PrivateKey, connectionID common.Hash, isMainnet bool) (hlSignature, error) {
	source := "b"
	if isMainnet {
		source = "a"
	}
	structHash, err := abiEncodeKeccak(
		abi.Arguments{{Type: bytes32Type}, {Type: bytes32Type}, {Type: bytes32Type}},
		agentTypeHash,
		crypto.Keccak256Hash([]byte(source)),
		connectionID,
	)
	if err != nil {
		return hlSignature{}, err
	}
	domainSeparator, err := abiEncodeKeccak(
		abi.Arguments{{Type: bytes32Type}, {Type: bytes32Type}, {Type: bytes32Type}, {Type: uint256Type}, {Type: addressType}},
		eip712DomainTypeHash,
		exchangeNameHash,
		versionHash,
		big.NewInt(1337),
		zeroAddress,
	)
	if err != nil {
		return hlSignature{}, err
	}
	digest := crypto.Keccak256(
		[]byte{0x19, 0x01},
		domainSeparator.Bytes(),
		structHash.Bytes(),
	)
	sig, err := crypto.Sign(digest, priv)
	if err != nil {
		return hlSignature{}, err
	}
	return hlSignature{
		R: "0x" + hex.EncodeToString(sig[:32]),
		S: "0x" + hex.EncodeToString(sig[32:64]),
		V: sig[64] + 27,
	}, nil
}

func abiEncodeKeccak(args abi.Arguments, values ...any) (common.Hash, error) {
	encoded, err := args.Pack(values...)
	if err != nil {
		return common.Hash{}, err
	}
	return crypto.Keccak256Hash(encoded), nil
}

func mustABIType(name string) abi.Type {
	t, err := abi.NewType(name, "", nil)
	if err != nil {
		panic(err)
	}
	return t
}

func loadSignerFromEnv() (*ecdsa.PrivateKey, common.Address, *common.Address) {
	keyHex := strings.TrimSpace(firstEnv("HL_PRIVATE_KEY", "HYPERLIQUID_PRIVATE_KEY"))
	if keyHex == "" {
		return nil, common.Address{}, parseAddressEnv(firstEnv("HL_VAULT_ADDRESS", "HYPERLIQUID_VAULT_ADDRESS"))
	}
	keyHex = strings.TrimPrefix(keyHex, "0x")
	priv, err := crypto.HexToECDSA(keyHex)
	if err != nil {
		return nil, common.Address{}, parseAddressEnv(firstEnv("HL_VAULT_ADDRESS", "HYPERLIQUID_VAULT_ADDRESS"))
	}
	addr := crypto.PubkeyToAddress(priv.PublicKey)
	return priv, addr, parseAddressEnv(firstEnv("HL_VAULT_ADDRESS", "HYPERLIQUID_VAULT_ADDRESS"))
}

func parseAddressEnv(raw string) *common.Address {
	raw = strings.TrimSpace(raw)
	if raw == "" || !common.IsHexAddress(raw) {
		return nil
	}
	addr := common.HexToAddress(raw)
	return &addr
}

func firstEnv(keys ...string) string {
	for _, key := range keys {
		if v := strings.TrimSpace(os.Getenv(key)); v != "" {
			return v
		}
	}
	return ""
}

func nextNonce() uint64 {
	now := uint64(time.Now().UTC().UnixMilli())
	for {
		cur := atomic.LoadUint64(&secpNonce)
		if cur == 0 {
			if atomic.CompareAndSwapUint64(&secpNonce, 0, now) {
				return now
			}
			continue
		}
		next := cur + 1
		if next+300000 < now {
			next = now
		}
		if atomic.CompareAndSwapUint64(&secpNonce, cur, next) {
			return next
		}
	}
}

func normalizeCloid(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	if strings.HasPrefix(raw, "0x") {
		return strings.ToLower(raw)
	}
	raw = strings.ReplaceAll(raw, "-", "")
	if len(raw) == 32 {
		return "0x" + strings.ToLower(raw)
	}
	return ""
}

func findProtectionOrders(orders []market.Order) (string, string) {
	var stopID, targetID string
	for _, order := range orders {
		if !order.ReduceOnly || !order.IsTrigger {
			continue
		}
		switch strings.ToLower(strings.TrimSpace(order.TriggerKind)) {
		case "sl":
			if stopID == "" {
				stopID = order.ID
			}
		case "tp":
			if targetID == "" {
				targetID = order.ID
			}
		}
	}
	return stopID, targetID
}

func triggerKindFromOrderType(orderType string) string {
	lower := strings.ToLower(strings.TrimSpace(orderType))
	switch {
	case strings.Contains(lower, "stop"), strings.Contains(lower, "sl"):
		return "sl"
	case strings.Contains(lower, "take"), strings.Contains(lower, "tp"):
		return "tp"
	default:
		return ""
	}
}

func floatToWire(x float64) string {
	s := strconv.FormatFloat(x, 'f', 8, 64)
	s = strings.TrimRight(s, "0")
	s = strings.TrimRight(s, ".")
	if s == "" || s == "-0" {
		return "0"
	}
	return s
}

func roundToSignificantAndDecimal(x float64, sigFigs int, decimals int) float64 {
	if x == 0 {
		return 0
	}
	pow := math.Pow(10, float64(sigFigs)-math.Ceil(math.Log10(math.Abs(x))))
	rounded := math.Round(x*pow) / pow
	if decimals < 0 {
		return rounded
	}
	scale := math.Pow10(decimals)
	return math.Round(rounded*scale) / scale
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func mustFloat(s string) float64 {
	v, _ := strconv.ParseFloat(s, 64)
	return v
}

func decimalsFromTick(tick string) int {
	parts := bytes.Split([]byte(tick), []byte("."))
	if len(parts) != 2 {
		return 0
	}
	return len(bytes.TrimRight(parts[1], "0"))
}
