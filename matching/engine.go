package matching

import (
	"lightning-exchange/domain"
	"lightning-exchange/orderbook"
	"runtime"
	"sync"
	"sync/atomic"
)

// IMatchingEngine defines the interface for a matching engine
type IMatchingEngine interface {
	// SubmitOrder submits an order to the matching engine (non-blocking)
	SubmitOrder(order *domain.Order)

	// Start starts the matching loop in a dedicated goroutine
	Start()

	// Stop stops the matching engine gracefully
	Stop()

	// GetOrderBook returns the order book
	GetOrderBook() orderbook.IOrderBook
}

// MatchingEngine implements a single-threaded matching engine for ONE trading pair
// Architecture:
//   - Each MatchingEngine handles ONLY ONE symbol (e.g., "BTCUSDT")
//   - Runs in a dedicated goroutine with runtime.LockOSThread() to reduce context switches
//   - Uses channel-based order queue for lock-free submission
//   - Single-threaded matching ensures deterministic order execution without locks
type MatchingEngine struct {
	symbol      string                        // Trading pair this engine handles
	orderBook   *orderbook.OrderBook          // Order book for this symbol
	orderBuffer *RingBufferSemaphoreBatchSafe // Incoming order queue (batch + safe semaphore)
	cancelChan  chan string                   // Cancel order requests (by order ID)
	tradeBuffer *TradeRingBufferBatchSafe     // Outgoing trade queue (batch + safe semaphore)
	tradeIDGen  *IDGenerator                  // Trade ID generator
	stopChan    chan struct{}                 // Signal to stop the engine
}

// NewMatchingEngine creates a new matching engine for a specific symbol
// Performance: Uses batch + safe semaphore RingBuffer (fast + safe)
func NewMatchingEngine(symbol string) *MatchingEngine {
	return &MatchingEngine{
		symbol:      symbol,
		orderBook:   orderbook.NewOrderBook(symbol),
		orderBuffer: NewRingBufferSemaphoreBatchSafe(65536), // Order queue (64K buffer)
		cancelChan:  make(chan string, 1000),                // Cancel requests (low frequency)
		tradeBuffer: NewTradeRingBufferBatchSafe(65536),     // Trade queue (64K buffer)
		tradeIDGen:  NewIDGenerator("T"),
		stopChan:    make(chan struct{}),
	}
}

// ExchangeEngine manages multiple MatchingEngines (one per symbol)
// Performance optimization: Uses atomic.Value for lock-free reads
//   - atomic.Value stores an immutable map[string]*MatchingEngine
//   - Read path is completely lock-free (~5ns vs ~10ns for RWMutex)
//   - Write path uses copy-on-write (rare, only when adding new symbols)
//   - Trade-off: Write is slower (must copy entire map), but writes are <0.01% of operations
//
// Performance comparison:
//   - RWMutex.RLock(): ~10ns (2 atomic ops: acquire + release)
//   - atomic.Value.Load(): ~5ns (1 atomic op)
//   - 2x faster on read-heavy workload (99.99% reads)
type ExchangeEngine struct {
	engines atomic.Value // Stores map[string]*MatchingEngine (immutable, copy-on-write)
	mu      sync.Mutex   // Only used during writes (creating new engines)
}

// NewExchangeEngine creates a new exchange engine
func NewExchangeEngine() *ExchangeEngine {
	e := &ExchangeEngine{}
	// Initialize with empty map
	e.engines.Store(make(map[string]*MatchingEngine))
	return e
}

// GetEngine returns the matching engine for a symbol (creates if not exists)
func (e *ExchangeEngine) GetEngine(symbol string) *MatchingEngine {
	// Fast path: completely lock-free read (99.99% of calls)
	// atomic.Value.Load() is a single atomic operation (~5ns)
	engines := e.engines.Load().(map[string]*MatchingEngine)
	if engine, ok := engines[symbol]; ok {
		return engine
	}

	// Slow path: create new engine (0.01% of calls)
	e.mu.Lock()
	defer e.mu.Unlock()

	// Double-check: another goroutine might have created it
	engines = e.engines.Load().(map[string]*MatchingEngine)
	if engine, ok := engines[symbol]; ok {
		return engine
	}

	// Create new engine
	engine := NewMatchingEngine(symbol)
	engine.Start()

	// Copy-on-write: create new map with all existing engines + new one
	// This is expensive but rare (only happens when adding new trading pairs)
	newEngines := make(map[string]*MatchingEngine, len(engines)+1)
	for k, v := range engines {
		newEngines[k] = v
	}
	newEngines[symbol] = engine

	// Atomically replace the entire map
	e.engines.Store(newEngines)

	return engine
}

// SubmitOrder submits an order to the appropriate matching engine
func (e *ExchangeEngine) SubmitOrder(order *domain.Order) {
	engine := e.GetEngine(order.Symbol)
	engine.SubmitOrder(order)
}

// CancelOrder submits a cancel request to the appropriate matching engine
func (e *ExchangeEngine) CancelOrder(symbol, orderID string) {
	engine := e.GetEngine(symbol)
	engine.CancelOrder(orderID)
}

// Start starts the matching loop in a dedicated goroutine
func (me *MatchingEngine) Start() {
	go func() {
		// Lock this goroutine to an OS thread to reduce context switches
		// This improves CPU cache locality and reduces scheduling overhead
		runtime.LockOSThread()
		defer runtime.UnlockOSThread()

		// Create batch consumer for orders
		orderConsumer := me.orderBuffer.NewConsumerBatchSafe()

		// Main matching loop - single-threaded with batch + safe semaphore
		for {
			// Check for cancel/stop signals first (non-blocking)
			select {
			case orderID := <-me.cancelChan:
				me.orderBook.CancelOrder(orderID)
				continue
			case <-me.stopChan:
				return
			default:
			}

			// Consume order from batch RingBuffer (blocking wait)
			order := orderConsumer.Consume()

			// Process order and generate trades
			trades := me.processOrder(order)

			// Publish trades to batch RingBuffer
			for _, trade := range trades {
				me.tradeBuffer.Publish(trade)
			}
		}
	}()
}

// SubmitOrder submits an order to the matching engine (non-blocking)
func (me *MatchingEngine) SubmitOrder(order *domain.Order) {
	me.orderBuffer.Publish(order)
}

// CancelOrder submits a cancel request to the matching engine (non-blocking)
// The cancel is processed in the matching thread to ensure thread safety
func (me *MatchingEngine) CancelOrder(orderID string) {
	me.cancelChan <- orderID
}

// Stop stops the matching engine gracefully
func (me *MatchingEngine) Stop() {
	close(me.stopChan)
}

// GetOrderBook returns the order book
func (me *MatchingEngine) GetOrderBook() orderbook.IOrderBook {
	return me.orderBook
}

// GetTradeBuffer returns the trade RingBuffer for consuming trades
func (me *MatchingEngine) GetTradeBuffer() *TradeRingBufferBatchSafe {
	return me.tradeBuffer
}

// processOrder processes an incoming order (internal, runs in matching goroutine)
func (me *MatchingEngine) processOrder(order *domain.Order) []*domain.Trade {
	var trades []*domain.Trade

	// Try to match the order against existing orders
	if order.Side == domain.SideBuy {
		trades = me.matchBuyOrder(order)
	} else {
		trades = me.matchSellOrder(order)
	}

	// If order is not fully filled, add remaining to order book
	if !order.IsFilled() && order.Type == domain.OrderTypeLimit {
		me.orderBook.AddOrder(order)
	}

	return trades
}

// matchBuyOrder matches a buy order against sell orders
func (me *MatchingEngine) matchBuyOrder(buyOrder *domain.Order) []*domain.Trade {
	var trades []*domain.Trade

	for !buyOrder.IsFilled() {
		bestAsk := me.orderBook.GetBestAsk()

		// No matching sell orders
		if bestAsk == 0 || (buyOrder.Type == domain.OrderTypeLimit && buyOrder.Price < bestAsk) {
			break
		}

		// Get best sell price level (O(1) - no allocation)
		bestLevel := me.orderBook.GetBestSellLevel()
		if bestLevel == nil || bestLevel.Orders.Len() == 0 {
			break
		}

		// Get first sell order (FIFO) - O(1)
		sellOrder := bestLevel.Orders.Front().Value.(*domain.Order)
		trade := me.executeTrade(buyOrder, sellOrder, bestAsk)
		trades = append(trades, trade)

		// Remove fully filled sell order
		if sellOrder.IsFilled() {
			me.orderBook.CancelOrder(sellOrder.ID)
		}
	}

	return trades
}

// matchSellOrder matches a sell order against buy orders
func (me *MatchingEngine) matchSellOrder(sellOrder *domain.Order) []*domain.Trade {
	var trades []*domain.Trade

	for !sellOrder.IsFilled() {
		bestBid := me.orderBook.GetBestBid()

		// No matching buy orders
		if bestBid == 0 || (sellOrder.Type == domain.OrderTypeLimit && sellOrder.Price > bestBid) {
			break
		}

		// Get best buy price level (O(1) - no allocation)
		bestLevel := me.orderBook.GetBestBuyLevel()
		if bestLevel == nil || bestLevel.Orders.Len() == 0 {
			break
		}

		// Get first buy order (FIFO) - O(1)
		buyOrder := bestLevel.Orders.Front().Value.(*domain.Order)
		trade := me.executeTrade(buyOrder, sellOrder, bestBid)
		trades = append(trades, trade)

		// Remove fully filled buy order
		if buyOrder.IsFilled() {
			me.orderBook.CancelOrder(buyOrder.ID)
		}
	}

	return trades
}

// executeTrade executes a trade between two orders
func (me *MatchingEngine) executeTrade(buyOrder, sellOrder *domain.Order, price int64) *domain.Trade {
	// Calculate trade quantity (minimum of remaining quantities)
	quantity := min(buyOrder.RemainingQuantity(), sellOrder.RemainingQuantity())

	// Update orders
	buyOrder.Fill(quantity)
	sellOrder.Fill(quantity)

	// Create trade
	tradeID := me.tradeIDGen.Next()
	trade := domain.NewTrade(tradeID, buyOrder.Symbol, price, quantity, buyOrder, sellOrder)

	return trade
}
