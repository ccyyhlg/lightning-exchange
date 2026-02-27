package main

import (
	"fmt"
	"lightning-exchange/domain"
	"lightning-exchange/matching"
	"runtime"
	"sync/atomic"
	"time"
)

func main() {
	fmt.Println("=== 交易所撮合系统性能测试 ===")

	// 创建撮合引擎
	engine := matching.NewMatchingEngine("BTCUSDT")
	engine.Start()
	defer engine.Stop()

	// 测试参数
	testDuration := 5 * time.Second
	numCPU := runtime.NumCPU()
	numWorkers := numCPU - 2 // 1 个给撮合线程，1 个给系统/GC
	if numWorkers < 1 {
		numWorkers = 1
	}

	var (
		orderCount atomic.Int64
		tradeCount atomic.Int64
	)

	// 消费 trades（使用 Disruptor）
	go func() {
		tradeBuffer := engine.GetTradeBuffer()
		consumer := tradeBuffer.NewTradeConsumerBatchSafe()
		for {
			trade, ok := consumer.TryConsume()
			if ok && trade != nil {
				trade.Destroy() // 归还到对象池
				tradeCount.Add(1)
			} else {
				runtime.Gosched()
			}
		}
	}()

	fmt.Printf("开始测试...\n")
	fmt.Printf("CPU 核心数: %d\n", numCPU)
	fmt.Printf("生产者数量: %d (NumCPU - 2)\n", numWorkers)
	fmt.Printf("测试时长: %v\n\n", testDuration)

	startTime := time.Now()
	stopChan := make(chan struct{})

	// 启动多个生产者
	for w := 0; w < numWorkers; w++ {
		go func(workerID int) {
			orderID := 0
			for {
				select {
				case <-stopChan:
					return
				default:
					// 交替发送买单和卖单，价格有重叠以产生成交
					var side domain.Side
					var price int64
					if orderID%2 == 0 {
						side = domain.SideBuy
						price = 50000 + int64(orderID%200) // 买单：50000-50199
					} else {
						side = domain.SideSell
						price = 50000 + int64(orderID%200) // 卖单：50000-50199（价格重叠）
					}

					order := domain.NewLimitOrder(
						fmt.Sprintf("w%d-order-%d", workerID, orderID),
						"BTCUSDT",
						fmt.Sprintf("user-%d", workerID),
						side,
						price,
						1,
					)
					engine.SubmitOrder(order)
					orderCount.Add(1)
					orderID++
				}
			}
		}(w)
	}

	// 实时显示进度
	ticker := time.NewTicker(1 * time.Second)
	go func() {
		for range ticker.C {
			elapsed := time.Since(startTime)
			orders := orderCount.Load()
			trades := tradeCount.Load()
			qps := float64(orders) / elapsed.Seconds()
			tps := float64(trades) / elapsed.Seconds()
			fmt.Printf("[%.0fs] 订单: %d (%.0f/s) | 成交: %d (%.0f/s)\n",
				elapsed.Seconds(), orders, qps, trades, tps)
		}
	}()

	// 等待测试时间
	time.Sleep(testDuration)
	close(stopChan)
	ticker.Stop()

	// 等待处理完成
	time.Sleep(500 * time.Millisecond)

	elapsed := time.Since(startTime)
	totalOrders := orderCount.Load()
	totalTrades := tradeCount.Load()

	// 计算性能指标
	qps := float64(totalOrders) / elapsed.Seconds()
	tps := float64(totalTrades) / elapsed.Seconds()
	avgLatency := elapsed.Seconds() * 1e6 / float64(totalOrders)
	matchRate := float64(totalTrades) / float64(totalOrders) * 100

	// 输出结果
	fmt.Println("\n=== 性能测试结果 ===")
	fmt.Printf("测试时长:     %v\n", elapsed)
	fmt.Printf("总订单数:     %d\n", totalOrders)
	fmt.Printf("总成交数:     %d\n", totalTrades)
	fmt.Printf("订单吞吐量:   %.0f orders/sec\n", qps)
	fmt.Printf("成交吞吐量:   %.0f trades/sec\n", tps)
	fmt.Printf("平均延迟:     %.2f μs/order\n", avgLatency)
	fmt.Printf("撮合率:       %.2f%%\n", matchRate)

	// 性能评级
	fmt.Println("\n=== 性能评级 ===")
	if qps >= 1000000 {
		fmt.Println("🚀 极致性能 (>100万 QPS)")
	} else if qps >= 500000 {
		fmt.Println("⚡ 优秀性能 (50万-100万 QPS)")
	} else if qps >= 100000 {
		fmt.Println("✅ 良好性能 (10万-50万 QPS)")
	} else if qps >= 10000 {
		fmt.Println("👍 合格性能 (1万-10万 QPS)")
	} else {
		fmt.Println("⚠️  性能较低 (<1万 QPS)")
	}

	// 订单簿状态
	orderBook := engine.GetOrderBook()
	fmt.Println("\n=== 订单簿状态 ===")
	fmt.Printf("最佳买价:     %d\n", orderBook.GetBestBid())
	fmt.Printf("最佳卖价:     %d\n", orderBook.GetBestAsk())

	bids, asks := orderBook.GetDepth(5)
	fmt.Println("\n买单深度 (前5档):")
	for i, level := range bids {
		fmt.Printf("  %d. 价格: %d, 数量: %d, 订单数: %d\n",
			i+1, level.Price, level.Quantity, level.Orders)
	}

	fmt.Println("\n卖单深度 (前5档):")
	for i, level := range asks {
		fmt.Printf("  %d. 价格: %d, 数量: %d, 订单数: %d\n",
			i+1, level.Price, level.Quantity, level.Orders)
	}
}
