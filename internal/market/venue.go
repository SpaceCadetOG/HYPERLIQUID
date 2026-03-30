package market

type MarketData interface {
	FetchAllMarkets() ([]Market, error)
	LoadCandles(symbol, tf string, n int) ([]Candle, error)
	FetchOrderBook(symbol string, levels int) (OrderBook, error)
}

type ExecutionVenue interface {
	Balance() (AccountSnapshot, error)
	Positions() ([]Position, error)
	PlaceOrder(req OrderRequest) (OrderResult, error)
	CancelOrder(symbol string, orderID string) error
	OpenOrders(symbol string) ([]Order, error)
}
