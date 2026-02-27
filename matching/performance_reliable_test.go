package matching

import (
	"fmt"
	"lightning-exchange/domain"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// TestMatchingEngineReliableQPS 可信的性能测试
// 方案 A：使用"成交数达到预期"作为完成条件
// 测量的是真实的撮合完成吞吐（并且 trade 已被消费确认）
func TestMatchingEngineReliableQPS(t *testing.T) {
	engine := NewMatchingEngine("BTCUSDT")
	engine.Start()
	defer engine.Stop()
	
	// 测试参数
	numOrders := 100000 // 10万订单
	orderQty := int64(100)
	price := int64(50000)
	
	var tradeCount atomic.Int64
	stopChan := make(chan struct{})
	var consumerWg sync.WaitGroup
	
	// Trade 消费者
	consumerWg.Add(1)
	go func() {
		defer consumerWg.Done()
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
	
	// 步骤1: 先挂满卖单（构造严格 1:1 可完全成交的场景）
	t.Logf("步骤1: 挂入 %d 个卖单...", numOrders)
	for i := 0; i < numOrders; i++ {
		order := domain.NewLimitOrder(
			fmt.Sprintf("SELL-%d", i),
			"BTCUSDT",
			fmt.Sprintf("seller-%d", i),
			domain.SideSell,
			price,
			orderQty,
		)
		engine.SubmitOrder(order)
	}
	
	// 等待卖单进入订单簿
	time.Sleep(100 * time.Millisecond)
	
	// 步骤2: 发送买单，开始计时
	t.Logf("步骤2: 发送 %d 个买单，开始计时...", numOrders)
	startTime := time.Now()
	
	for i := 0; i < numOrders; i++ {
		order := domain.NewLimitOrder(
			fmt.Sprintf("BUY-%d", i),
			"BTCUSDT",
			fmt.Sprintf("buyer-%d", i),
			domain.SideBuy,
			price,
			orderQty,
		)
		engine.SubmitOrder(order)
	}
	
	// 步骤3: 等待成交数达到预期（带超时）
	expectedTrades := int64(numOrders)
	t.Logf("步骤3: 等待成交数达到 %d...", expectedTrades)
	
	success := waitForCondition(
		func() bool {
			return tradeCount.Load() >= expectedTrades
		},
		30*time.Second,
		10*time.Millisecond,
	)
	
	// 结束计时
	elapsed := time.Since(startTime)
	
	if !success {
		t.Fatalf("超时：期望成交数 %d，实际成交数 %d，差异 %d",
			expectedTrades, tradeCount.Load(), expectedTrades-tradeCount.Load())
	}
	
	// 停止消费者
	close(stopChan)
	consumerWg.Wait()
	
	// 计算可信的 QPS/TPS
	actualTrades := tradeCount.Load()
	ordersProcessed := actualTrades // 买单数量 = 成交数量（1:1 场景）
	
	qps := float64(ordersProcessed) / elapsed.Seconds()
	tps := float64(actualTrades) / elapsed.Seconds()
	
	t.Logf("\n=== 可信的性能测试结果（方案A：成交完成吞吐）===")
	t.Logf("测试场景:      1:1 完全成交（先挂卖单，再发买单）")
	t.Logf("订单数量:      %d", numOrders)
	t.Logf("成交数量:      %d", actualTrades)
	t.Logf("完成耗时:      %v", elapsed)
	t.Logf("订单 QPS:      %.0f orders/sec (%.1f 万/秒)", qps, qps/10000)
	t.Logf("成交 TPS:      %.0f trades/sec (%.1f 万/秒)", tps, tps/10000)
	t.Logf("平均延迟:      %.2f μs/order", float64(elapsed.Microseconds())/float64(ordersProcessed))
	t.Logf("\n说明: 此 QPS 测量的是撮合完成吞吐（trade 已被消费确认）")
}

// TestMatchingEngineReliableQPSLarge 大规模可信性能测试
func TestMatchingEngineReliableQPSLarge(t *testing.T) {
	if testing.Short() {
		t.Skip("跳过大规模测试")
	}
	
	engine := NewMatchingEngine("BTCUSDT")
	engine.Start()
	defer engine.Stop()
	
	// 测试参数（更大规模）
	numOrders := 500000 // 50万订单
	orderQty := int64(100)
	price := int64(50000)
	
	var tradeCount atomic.Int64
	stopChan := make(chan struct{})
	var consumerWg sync.WaitGroup
	
	// Trade 消费者
	consumerWg.Add(1)
	go func() {
		defer consumerWg.Done()
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
	
	// 步骤1: 先挂满卖单
	t.Logf("步骤1: 挂入 %d 个卖单...", numOrders)
	for i := 0; i < numOrders; i++ {
		order := domain.NewLimitOrder(
			fmt.Sprintf("SELL-%d", i),
			"BTCUSDT",
			fmt.Sprintf("seller-%d", i),
			domain.SideSell,
			price,
			orderQty,
		)
		engine.SubmitOrder(order)
	}
	
	time.Sleep(200 * time.Millisecond)
	
	// 步骤2: 发送买单，开始计时
	t.Logf("步骤2: 发送 %d 个买单，开始计时...", numOrders)
	startTime := time.Now()
	
	for i := 0; i < numOrders; i++ {
		order := domain.NewLimitOrder(
			fmt.Sprintf("BUY-%d", i),
			"BTCUSDT",
			fmt.Sprintf("buyer-%d", i),
			domain.SideBuy,
			price,
			orderQty,
		)
		engine.SubmitOrder(order)
	}
	
	// 步骤3: 等待成交数达到预期
	expectedTrades := int64(numOrders)
	t.Logf("步骤3: 等待成交数达到 %d...", expectedTrades)
	
	success := waitForCondition(
		func() bool {
			return tradeCount.Load() >= expectedTrades
		},
		60*time.Second,
		10*time.Millisecond,
	)
	
	elapsed := time.Since(startTime)
	
	if !success {
		t.Fatalf("超时：期望成交数 %d，实际成交数 %d",
			expectedTrades, tradeCount.Load())
	}
	
	close(stopChan)
	consumerWg.Wait()
	
	actualTrades := tradeCount.Load()
	ordersProcessed := actualTrades
	
	qps := float64(ordersProcessed) / elapsed.Seconds()
	tps := float64(actualTrades) / elapsed.Seconds()
	
	t.Logf("\n=== 大规模可信性能测试结果 ===")
	t.Logf("测试场景:      1:1 完全成交（先挂卖单，再发买单）")
	t.Logf("订单数量:      %d", numOrders)
	t.Logf("成交数量:      %d", actualTrades)
	t.Logf("完成耗时:      %v", elapsed)
	t.Logf("订单 QPS:      %.0f orders/sec (%.1f 万/秒)", qps, qps/10000)
	t.Logf("成交 TPS:      %.0f trades/sec (%.1f 万/秒)", tps, tps/10000)
	t.Logf("平均延迟:      %.2f μs/order", float64(elapsed.Microseconds())/float64(ordersProcessed))
}

// TestMatchingEngineConcurrentReliableQPS 并发场景下的可信性能测试
func TestMatchingEngineConcurrentReliableQPS(t *testing.T) {
	engine := NewMatchingEngine("BTCUSDT")
	engine.Start()
	defer engine.Stop()
	
	// 测试参数
	numProducers := 8
	ordersPerProducer := 10000 // 每个生产者1万订单，总共8万
	orderQty := int64(100)
	price := int64(50000)
	
	var tradeCount atomic.Int64
	stopChan := make(chan struct{})
	var consumerWg sync.WaitGroup
	
	// Trade 消费者
	consumerWg.Add(1)
	go func() {
		defer consumerWg.Done()
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
	
	// 步骤1: 并发挂入卖单
	t.Logf("步骤1: %d 个生产者并发挂入卖单...", numProducers)
	var sellWg sync.WaitGroup
	for p := 0; p < numProducers; p++ {
		sellWg.Add(1)
		go func(producerID int) {
			defer sellWg.Done()
			for i := 0; i < ordersPerProducer; i++ {
				order := domain.NewLimitOrder(
					fmt.Sprintf("p%d-SELL-%d", producerID, i),
					"BTCUSDT",
					fmt.Sprintf("seller-%d", producerID),
					domain.SideSell,
					price,
					orderQty,
				)
				engine.SubmitOrder(order)
			}
		}(p)
	}
	sellWg.Wait()
	time.Sleep(100 * time.Millisecond)
	
	// 步骤2: 并发发送买单，开始计时
	t.Logf("步骤2: %d 个生产者并发发送买单，开始计时...", numProducers)
	startTime := time.Now()
	
	var buyWg sync.WaitGroup
	for p := 0; p < numProducers; p++ {
		buyWg.Add(1)
		go func(producerID int) {
			defer buyWg.Done()
			for i := 0; i < ordersPerProducer; i++ {
				order := domain.NewLimitOrder(
					fmt.Sprintf("p%d-BUY-%d", producerID, i),
					"BTCUSDT",
					fmt.Sprintf("buyer-%d", producerID),
					domain.SideBuy,
					price,
					orderQty,
				)
				engine.SubmitOrder(order)
			}
		}(p)
	}
	buyWg.Wait()
	
	// 步骤3: 等待成交数达到预期
	expectedTrades := int64(numProducers * ordersPerProducer)
	t.Logf("步骤3: 等待成交数达到 %d...", expectedTrades)
	
	success := waitForCondition(
		func() bool {
			return tradeCount.Load() >= expectedTrades
		},
		30*time.Second,
		10*time.Millisecond,
	)
	
	elapsed := time.Since(startTime)
	
	if !success {
		t.Fatalf("超时：期望成交数 %d，实际成交数 %d",
			expectedTrades, tradeCount.Load())
	}
	
	close(stopChan)
	consumerWg.Wait()
	
	actualTrades := tradeCount.Load()
	ordersProcessed := actualTrades
	
	qps := float64(ordersProcessed) / elapsed.Seconds()
	tps := float64(actualTrades) / elapsed.Seconds()
	
	t.Logf("\n=== 并发场景可信性能测试结果 ===")
	t.Logf("测试场景:      1:1 完全成交（先挂卖单，再发买单）")
	t.Logf("并发生产者:    %d", numProducers)
	t.Logf("总订单数:      %d", numProducers*ordersPerProducer)
	t.Logf("成交数量:      %d", actualTrades)
	t.Logf("完成耗时:      %v", elapsed)
	t.Logf("订单 QPS:      %.0f orders/sec (%.1f 万/秒)", qps, qps/10000)
	t.Logf("成交 TPS:      %.0f trades/sec (%.1f 万/秒)", tps, tps/10000)
	t.Logf("平均延迟:      %.2f μs/order", float64(elapsed.Microseconds())/float64(ordersProcessed))
}
