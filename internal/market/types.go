package market

import "time"

type Side string

const (
	SideLong  Side = "LONG"
	SideShort Side = "SHORT"
)

type Candle struct {
	Time   time.Time
	Open   float64
	High   float64
	Low    float64
	Close  float64
	Volume float64
}

type BookLevel struct {
	Price float64
	Size  float64
}

type OrderBook struct {
	Symbol string
	Bids   []BookLevel
	Asks   []BookLevel
}

type Market struct {
	Symbol        string
	MarkPrice     float64
	OraclePrice   float64
	Volume24hUSD  float64
	OpenInterest  float64
	FundingRate   float64
	MaxLeverage   float64
	PriceDecimals int
	SizeDecimals  int
	TickSize      float64
	MinSize       float64
	MinNotional   float64
}

type AccountSnapshot struct {
	Equity       float64
	AvailableUSD float64
	ReservedUSD  float64
}

type Position struct {
	Symbol         string
	Side           Side
	Size           float64
	InitialSize    float64
	EntryPrice     float64
	MarkPrice      float64
	Unrealized     float64
	Realized       float64
	StopPrice      float64
	TargetPrice    float64
	TargetHit      bool
	BarsHeld       int
	BestPrice      float64
	WorstPrice     float64
	EntryNotional  float64
	ReservedMargin float64
	Leverage       float64
}

type OrderRequest struct {
	Symbol      string
	Side        Side
	Size        float64
	Price       float64
	StopPrice   float64
	TargetPrice float64
	Leverage    float64
	Type        string
	ReduceOnly  bool
	ClientOrder string
}

type Order struct {
	ID          string
	ClientOrder string
	Symbol      string
	Side        Side
	Size        float64
	Price       float64
	Status      string
	ReduceOnly  bool
	IsTrigger   bool
	TriggerPx   float64
	TriggerKind string
	OrderType   string
	TIF         string
}

type OrderResult struct {
	OrderID       string
	Status        string
	StopOrderID   string
	TargetOrderID string
}

type RankedMarket struct {
	Symbol            string
	UTC4hPct          float64
	UTC1hPct          float64
	Side              Side
	Score             float64
	RawScore          float64
	NormalizedScore   float64
	GradeLabel        string
	ConfluenceLabel   string
	Completeness      float64
	IntegrityPenalty  float64
	ExecutionPenalty  float64
	Last              float64
	Reason            string
	ValueLow          float64
	ValueHigh         float64
	POC               float64
	AVWAP             float64
	AVWAPDistancePct  float64
	ADX               float64
	ATR               float64
	ATRPct            float64
	BookSkew          float64
	SpreadBps         float64
	EstSlippageBps    float64
	TopBookUSD        float64
	Momentum5m        float64
	Momentum30m       float64
	Momentum4h        float64
	Momentum24h       float64
	MomentumAgreement float64
	RegimeTag         string
	Confidence        float64
	Uncertainty       float64
	DataFlags         []string
	ReliabilityAdj    float64
	TradePriority     float64
	WhalePrice        float64
	WhaleDistanceBps  float64
	StopPrice         float64
	TargetPrice       float64
	Funding           float64
	ChangePct         float64
	DayUTCChangePct   float64
	WindowOpenPrice   float64
	Eligibility       EligibilitySignals
	Generated         time.Time
}

type EligibilitySignals struct {
	TrendStrong              bool
	ShortTermMomentumUp      bool
	ShortTermMomentumDown    bool
	BidPressure              bool
	AskPressure              bool
	AcceptanceAboveValue     bool
	AcceptanceBelowValue     bool
	AboveAVWAP               bool
	BelowAVWAP               bool
	ResponsiveBidNearValue   bool
	ResponsiveOfferNearValue bool
	WhaleBidNearby           bool
	WhaleOfferNearby         bool
	PositiveSessionMove      bool
	NegativeSessionMove      bool
	WideSpread               bool
	LowATR                   bool
}

type Scored struct {
	Symbol             string
	UTC4hPct           *float64
	UTC1hPct           *float64
	Change24h          float64
	DayUTC24h          *float64
	VolumeUSD          float64
	OIUSD              *float64
	FundingRate        *float64
	OpenPrice          float64
	LastPrice          float64
	Grade              string
	Score              float64
	RawScore           float64
	NormalizedScore    float64
	Reason             string
	WindowDerived      bool
	Signals            EligibilitySignals
	Displayable        bool
	Eligible           bool
	EntryReadyHint     bool
	EligibilityReasons []string
	Completeness       float64
	IntegrityPenalty   float64
	ExecutionPenalty   float64
	SpreadPenaltyBps   float64
	EstSlippageBps     float64
	TopBookUSD         float64
	Momentum5m         float64
	Momentum30m        float64
	Momentum4h         float64
	Momentum24h        float64
	MomentumAgreement  float64
	RegimeTag          string
	Confidence         float64
	Uncertainty        float64
	DataFlags          []string
	ReliabilityAdj     float64
	TradePriority      float64
}
