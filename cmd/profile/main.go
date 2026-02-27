package main

import (
	"fmt"
	"lightning-exchange/domain"
	"lightning-exchange/matching"
	"os"
	"runtime"
	"runtime/pprof"
	"sync/atomic"
	"time"
)

func main() {
	// 创建 CPU profile 文件
	cpuFile, err := os.Create("cpu.prof")
	if err != nil {
		panic(err)
	}
	defer cpuFile.Close()

	// 启动 CPU profiling
	pprof.StartCPUProfile(cpuFile)
	defer pprof.StopCPUProfile()

	fmt.Println("=== 性能分析开始 ===")
	fmt.Println("生成 CPU profile: cpu.prof")

	// 创建撮合引擎
	engine := matching.NewMatchingEngine("BTCUSDT")
	engine.Start()
	defer engine.Stop()

	// 测试参数
	duration := 10 * time.Second
	numCPU := runtime.NumCPU()
	numWorkers := numCPU - 2
	if numWorkers < 1 {
		numWorkers = 1
	}

	var (
		orderCount atomic.Int64
		tradeCount atomic.Int64
	)

	// 消费 trades
	go func() {
		tradeBuffer := engine.GetTradeBuffer()
		consumer := tradeBuffer.NewTradeConsumerBatchSafe()
		for {
			trade, ok := consumer.TryConsume()
			if ok && trade != nil {
				trade.Destroy()
				tradeCount.Add(1)
			} else {
				runtime.Gosched()
			}
		}
	}()

	fmt.Printf("CPU 核心数: %d\n", numCPU)
	fmt.Printf("生产者数量: %d\n", numWorkers)
	fmt.Printf("测试时长: %v\n\n", duration)

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
					var side domain.Side
					var price int64
					if orderID%2 == 0 {
						side = domain.SideBuy
						price = 50000 + int64(orderID%200)
					} else {
						side = domain.SideSell
						price = 50000 + int64(orderID%200)
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

	// 等待测试时间
	time.Sleep(duration)
	close(stopChan)
	time.Sleep(500 * time.Millisecond)

	elapsed := time.Since(startTime)
	totalOrders := orderCount.Load()
	totalTrades := tradeCount.Load()

	fmt.Println("\n=== 性能测试结果 ===")
	fmt.Printf("总订单数: %d\n", totalOrders)
	fmt.Printf("总成交数: %d\n", totalTrades)
	fmt.Printf("Order QPS: %.0f orders/sec\n", float64(totalOrders)/elapsed.Seconds())
	fmt.Printf("Trade TPS: %.0f trades/sec\n", float64(totalTrades)/elapsed.Seconds())

	fmt.Println("\n分析 CPU profile:")
	fmt.Println("  go tool pprof -http=:8080 cpu.prof")
	fmt.Println("  或者: go tool pprof cpu.prof")
	fmt.Println("  然后输入: top10  (查看前 10 个热点函数)")
	fmt.Println("  然后输入: list <函数名>  (查看具体代码)")
}
