package main

import (
	"fmt"
	"lightning-exchange/domain"
	"lightning-exchange/matching"
	"time"
)

func main() {
	// Initialize exchange engine
	exchange := matching.NewExchangeEngine()

	// Pre-create and start matching engine for BTCUSDT
	// This ensures the engine is ready before any orders arrive
	btcEngine := exchange.GetEngine("BTCUSDT")

	fmt.Println("Exchange engine started")
	fmt.Printf("BTCUSDT matching engine initialized: %v\n", btcEngine != nil)

	// Example: Submit some test orders
	go func() {
		// Wait a bit for engine to fully start
		time.Sleep(100 * time.Millisecond)

		// Create a sell order: Sell 1 BTC at 50000 USDT
		sellOrder := domain.NewLimitOrder("order-1", "BTCUSDT", "user-1", domain.SideSell, 50000, 100000000) // 1 BTC in satoshis
		exchange.SubmitOrder(sellOrder)
		fmt.Println("Submitted sell order: 1 BTC @ 50000 USDT")

		// Create a buy order: Buy 0.5 BTC at 50000 USDT (should match)
		buyOrder := domain.NewLimitOrder("order-2", "BTCUSDT", "user-2", domain.SideBuy, 50000, 50000000) // 0.5 BTC
		exchange.SubmitOrder(buyOrder)
		fmt.Println("Submitted buy order: 0.5 BTC @ 50000 USDT")

		// Listen for trades (using batch + safe semaphore)
		tradeBuffer := btcEngine.GetTradeBuffer()
		tradeConsumer := tradeBuffer.NewTradeConsumerBatchSafe()
		for {
			trade, ok := tradeConsumer.TryConsume()
			if ok && trade != nil {
				fmt.Printf("Trade executed: %s - Price: %d, Quantity: %d, Buyer: %s, Seller: %s\n",
					trade.ID, trade.Price, trade.Quantity, trade.BuyUserID, trade.SellUserID)
				trade.Destroy() // Return to pool
			}
		}
	}()

	// Keep main goroutine alive
	select {}
}
