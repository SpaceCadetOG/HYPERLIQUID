package ta

import "hyperliquid/internal/market"

type OBWall struct {
	Price float64
	Size  float64
	Rank  int
	Side  string
}

type OBContext struct {
	Symbol     string
	Imbalance  float64
	TopBidWall *OBWall
	TopAskWall *OBWall
	BidSum     float64
	AskSum     float64
	LevelsUsed int
}

func OrderBookContext(symbol string, book market.OrderBook) OBContext {
	var bidSum, askSum float64
	var topBid, topAsk *OBWall
	for i, lvl := range book.Bids {
		bidSum += lvl.Size
		if topBid == nil || lvl.Size > topBid.Size {
			tb := OBWall{Price: lvl.Price, Size: lvl.Size, Rank: i + 1, Side: "bid"}
			topBid = &tb
		}
	}
	for i, lvl := range book.Asks {
		askSum += lvl.Size
		if topAsk == nil || lvl.Size > topAsk.Size {
			ta := OBWall{Price: lvl.Price, Size: lvl.Size, Rank: i + 1, Side: "ask"}
			topAsk = &ta
		}
	}
	imb := 0.0
	if bidSum+askSum > 0 {
		imb = (bidSum - askSum) / (bidSum + askSum)
	}
	return OBContext{
		Symbol:     symbol,
		Imbalance:  imb,
		TopBidWall: topBid,
		TopAskWall: topAsk,
		BidSum:     bidSum,
		AskSum:     askSum,
		LevelsUsed: max(len(book.Bids), len(book.Asks)),
	}
}
