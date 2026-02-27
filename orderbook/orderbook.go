package orderbook

import (
	"lightning-exchange/domain"
)

// IOrderBook defines the interface for an order book
type IOrderBook interface {
	// AddOrder adds a new order to the book
	AddOrder(order *domain.Order) error

	// CancelOrder removes an order from the book
	CancelOrder(orderID string) error

	// GetBestBid returns the highest buy price
	GetBestBid() int64

	// GetBestAsk returns the lowest sell price
	GetBestAsk() int64

	// GetDepth returns the market depth (price levels and quantities)
	GetDepth(levels int) (bids, asks []PriceLevel)
}

// PriceLevel represents a price level in the order book
type PriceLevel struct {
	Price    int64
	Quantity int64
	Orders   int // number of orders at this level
}

// OrderBook implements a price-time priority order book
// Lock-free design: Only accessed by a single matching thread, no synchronization needed
// Performance: Removes ~30-50ns overhead per operation
// Architecture: Sharded PriceTree (Ordered Map + HashMap + List) for optimal performance
type OrderBook struct {
	symbol string
	bids   PriceTreeInterface // buy orders (descending price)
	asks   PriceTreeInterface // sell orders (ascending price)
	orders map[string]*domain.Order
}

// NewOrderBook creates a new order book for a symbol
func NewOrderBook(symbol string) *OrderBook {
	return &OrderBook{
		symbol: symbol,
		bids:   NewPriceTreeWithType(ShardedType, true),  // 分片树 + 位运算优化
		asks:   NewPriceTreeWithType(ShardedType, false), // 分片树 + 位运算优化
		orders: make(map[string]*domain.Order),
	}
}

// AddOrder adds a new order to the book
// Lock-free: Only called by the matching thread
func (ob *OrderBook) AddOrder(order *domain.Order) error {
	ob.orders[order.ID] = order

	if order.Side == domain.SideBuy {
		ob.bids.Insert(order)
	} else {
		ob.asks.Insert(order)
	}

	return nil
}

// CancelOrder removes an order from the book
// Lock-free: Only called by the matching thread
func (ob *OrderBook) CancelOrder(orderID string) error {
	order, exists := ob.orders[orderID]
	if !exists {
		return nil
	}

	if order.Side == domain.SideBuy {
		ob.bids.Remove(order)
	} else {
		ob.asks.Remove(order)
	}

	delete(ob.orders, orderID)
	order.Cancel()

	return nil
}

// GetBestBid returns the highest buy price
// Lock-free: O(1) direct pointer access
func (ob *OrderBook) GetBestBid() int64 {
	return ob.bids.GetBestPrice()
}

// GetBestAsk returns the lowest sell price
// Lock-free: O(1) direct pointer access
func (ob *OrderBook) GetBestAsk() int64 {
	return ob.asks.GetBestPrice()
}

// GetDepth returns the market depth
// Lock-free: Only called by the matching thread
func (ob *OrderBook) GetDepth(levels int) (bids, asks []PriceLevel) {
	bidLevels := ob.bids.GetDepth(levels)
	askLevels := ob.asks.GetDepth(levels)

	// Convert internal PriceLevel_ to external PriceLevel
	bids = make([]PriceLevel, len(bidLevels))
	for i, level := range bidLevels {
		bids[i] = PriceLevel{
			Price:    level.Price,
			Quantity: level.Volume,
			Orders:   level.Orders.Len(),
		}
	}

	asks = make([]PriceLevel, len(askLevels))
	for i, level := range askLevels {
		asks[i] = PriceLevel{
			Price:    level.Price,
			Quantity: level.Volume,
			Orders:   level.Orders.Len(),
		}
	}

	return bids, asks
}

// GetBestBuyOrders returns orders at the best bid price
// Lock-free: Only called by the matching thread
func (ob *OrderBook) GetBestBuyOrders() []*domain.Order {
	return ob.bids.GetBestOrders()
}

// GetBestSellOrders returns orders at the best ask price
// Lock-free: Only called by the matching thread
func (ob *OrderBook) GetBestSellOrders() []*domain.Order {
	return ob.asks.GetBestOrders()
}

// GetBestBuyLevel returns the best bid price level (O(1))
// Performance: Avoids allocating slice and copying orders
func (ob *OrderBook) GetBestBuyLevel() *PriceLevel_ {
	return ob.bids.GetBestLevel()
}

// GetBestSellLevel returns the best ask price level (O(1))
// Performance: Avoids allocating slice and copying orders
func (ob *OrderBook) GetBestSellLevel() *PriceLevel_ {
	return ob.asks.GetBestLevel()
}
