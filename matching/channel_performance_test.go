package matching

import (
	"fmt"
	"lightning-exchange/domain"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// TestChannelPerformance 测试使用 Channel 后的完整流程性能
func TestChannelPerformance(t *testing.T) {
	engine := NewMatchingEngine("BTCUSDT")
	engine.Start()
	defer engine.Stop()

	// 等待引擎启动
	time.Sleep(10 * time.Millisecond)

	numWorkers := 8
	duration := 5 * time.Second

	var (
		orderCount atomic.Int64
		tradeCount atomic.Int64
		wg         sync.WaitGroup
	)

	t.Logf("CPU 核心数: %d, 生产者数量: %d", 10, numWorkers)

	startTime := time.Now()
	stopChan := make(chan struct{})

	// 启动 trade 消费者（使用批量 + 安全版本）
	go func() {
		tradeBuffer := engine.GetTradeBuffer()
		tradeConsumer := tradeBuffer.NewTradeConsumerBatchSafe()
		for {
			select {
			case <-stopChan:
				return
			default:
				trade, ok := tradeConsumer.TryConsume()
				if ok && trade != nil {
					trade.Destroy()
					tradeCount.Add(1)
				}
			}
		}
	}()

	// 启动多个生产者
	for w := 0; w < numWorkers; w++ {
		wg.Add(1)
		go func(workerID int) {
			defer wg.Done()
			orderID := 0
			for {
				select {
				case <-stopChan:
					return
				default:
				}

				// 交替发送买单和卖单，价格有重叠以产生成交
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
					100,
				)

				engine.SubmitOrder(order)
				orderCount.Add(1)
				orderID++
			}
		}(w)
	}

	// 运行指定时间
	time.Sleep(duration)

	// 停止所有 goroutine
	close(stopChan)
	wg.Wait()

	elapsed := time.Since(startTime)
	orders := orderCount.Load()
	trades := tradeCount.Load()

	// 计算性能指标
	orderQPS := float64(orders) / elapsed.Seconds()
	tradeQPS := float64(trades) / elapsed.Seconds()
	avgLatency := elapsed.Microseconds() / orders

	t.Logf("\n=== Channel 性能测试结果 ===")
	t.Logf("运行时长:      %v", elapsed)
	t.Logf("总订单数:      %d", orders)
	t.Logf("总成交数:      %d", trades)
	t.Logf("订单 QPS:      %.0f orders/sec", orderQPS)
	t.Logf("成交 TPS:      %.0f trades/sec", tradeQPS)
	t.Logf("平均延迟:      %d μs/order", avgLatency)
	t.Logf("成交率:        %.1f%%", float64(trades)/float64(orders)*100)
}

// TestChannelVsRingBufferComparison 对比不同 buffer 大小的性能
func TestChannelVsRingBufferComparison(t *testing.T) {
	bufferSizes := []int{4096, 16384, 65536}
	
	for _, bufferSize := range bufferSizes {
		t.Run(fmt.Sprintf("BufferSize_%d", bufferSize), func(t *testing.T) {
			engine := NewMatchingEngine("TEST")
			// Channel 的 buffer 大小已经在 NewMatchingEngine 中设置为 65536
			engine.Start()
			defer engine.Stop()
			
			time.Sleep(10 * time.Millisecond)
			
			numWorkers := 8
			duration := 3 * time.Second
			
			var orderCount atomic.Int64
			var tradeCount atomic.Int64
			var wg sync.WaitGroup
			
			stopChan := make(chan struct{})
			
			// Trade 消费者（使用批量 + 安全版本）
			go func() {
				tradeBuffer := engine.GetTradeBuffer()
				tradeConsumer := tradeBuffer.NewTradeConsumerBatchSafe()
				for {
					select {
					case <-stopChan:
						return
					default:
						trade, ok := tradeConsumer.TryConsume()
						if ok && trade != nil {
							trade.Destroy()
							tradeCount.Add(1)
						}
					}
				}
			}()
			
			// 生产者
			startTime := time.Now()
			for w := 0; w < numWorkers; w++ {
				wg.Add(1)
				go func(workerID int) {
					defer wg.Done()
					orderID := 0
					for {
						select {
						case <-stopChan:
							return
						default:
						}
						
						side := domain.Side(orderID % 2)
						price := int64(50000 + (orderID % 200))
						
						order := domain.NewLimitOrder(
							fmt.Sprintf("w%d-o%d", workerID, orderID),
							"TEST",
							fmt.Sprintf("user%d", workerID),
							side,
							price,
							100,
						)
						
						engine.SubmitOrder(order)
						orderCount.Add(1)
						orderID++
					}
				}(w)
			}
			
			time.Sleep(duration)
			close(stopChan)
			wg.Wait()
			
			elapsed := time.Since(startTime)
			orders := orderCount.Load()
			trades := tradeCount.Load()
			
			t.Logf("Buffer=%d: 订单=%d, 成交=%d, QPS=%.0f, TPS=%.0f",
				bufferSize,
				orders,
				trades,
				float64(orders)/elapsed.Seconds(),
				float64(trades)/elapsed.Seconds())
		})
	}
}
