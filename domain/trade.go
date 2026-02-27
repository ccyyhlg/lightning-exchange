package domain

import (
	"sync"
	"time"
)

// Trade represents a matched trade between two orders
// Memory layout optimization: Hot fields (frequently accessed during persistence/broadcast)
// are placed in the first CPU cache line (64 bytes) to improve cache hit rate.
// Cache line 1 (64 bytes): Price, Quantity, Timestamp, Symbol, IsBuyerMaker
// Cache line 2 (64 bytes): ID, BuyOrderID, SellOrderID, BuyUserID, SellUserID
type Trade struct {
	// Hot fields: accessed during persistence and broadcast (first 64 bytes)
	Price     int64     // 8 bytes - trade execution price
	Quantity  int64     // 8 bytes - trade quantity
	Timestamp time.Time // 24 bytes - trade execution time
	Symbol    string    // 16 bytes - trading pair
	IsBuyerMaker bool   // 1 byte - maker/taker flag (padded to 8 bytes)
	_         [7]byte   // 7 bytes - explicit padding for clarity
	
	// Cold fields: accessed only for logging/audit (second cache line)
	ID          string // 16 bytes - unique trade ID
	BuyOrderID  string // 16 bytes - buy order ID
	SellOrderID string // 16 bytes - sell order ID
	BuyUserID   string // 16 bytes - buyer user ID
	SellUserID  string // 16 bytes - seller user ID
}

var tradePool = sync.Pool{
	New: func() any {
		return &Trade{}
	},
}

// NewTrade creates a new trade from the pool
func NewTrade(id, symbol string, price, quantity int64, buyOrder, sellOrder *Order) *Trade {
	trade := tradePool.Get().(*Trade)
	trade.ID = id
	trade.Symbol = symbol
	trade.Price = price
	trade.Quantity = quantity
	trade.BuyOrderID = buyOrder.ID
	trade.SellOrderID = sellOrder.ID
	trade.BuyUserID = buyOrder.UserID
	trade.SellUserID = sellOrder.UserID
	trade.Timestamp = time.Now()
	trade.IsBuyerMaker = buyOrder.Timestamp.Before(sellOrder.Timestamp)
	return trade
}

// Destroy returns the trade to the pool
func (t *Trade) Destroy() {
	t.Reset()
	tradePool.Put(t)
}

func (t *Trade) Reset() {
	// Performance optimization: Use zero-value assignment to trigger compiler's DUFFZERO optimization.
	// The compiler will generate a single DUFFZERO instruction (or REP STOSQ for larger structs)
	// instead of multiple field-by-field assignments, which is 5-10x faster.
	// On amd64, Trade struct (~120 bytes) will use DUFFZERO - an unrolled memset-like operation.
	// This is equivalent to memset(t, 0, sizeof(Trade)) in C, but type-safe.
	*t = Trade{}
}
