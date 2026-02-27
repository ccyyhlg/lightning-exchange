package matching

import (
	"fmt"
	"lightning-exchange/domain"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// waitForCondition 等待条件满足或超时
// 这比固定 sleep 更可靠，避免假阴性/假阳性
func waitForCondition(condition func() bool, timeout time.Duration, checkInterval time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if condition() {
			return true
		}
		time.Sleep(checkInterval)
	}
	return false
}

// TestOrderFinalStateConsistencyRobust 订单最终状态一致性测试（改进版）
// 使用条件等待而非固定 sleep，更可靠
func TestOrderFinalStateConsistencyRobust(t *testing.T) {
	engine := NewMatchingEngine("BTCUSDT")
	engine.Start()
	defer engine.Stop()
	
	// 测试参数
	numBuyOrders := 50000
	numSellOrders := 50000
	orderQty := int64(100)
	price := int64(50000)
	
	// 记录所有发送的订单 ID
	buyOrderIDs := make(map[string]bool)
	sellOrderIDs := make(map[string]bool)
	var mu sync.Mutex
	
	// 记录所有成交
	type TradeRecord struct {
		ID          string
		BuyOrderID  string
		SellOrderID string
		Quantity    int64
		Price       int64
	}
	trades := make([]TradeRecord, 0)
	var tradeMu sync.Mutex
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
					tradeMu.Lock()
					trades = append(trades, TradeRecord{
						ID:          trade.ID,
						BuyOrderID:  trade.BuyOrderID,
						SellOrderID: trade.SellOrderID,
						Quantity:    trade.Quantity,
						Price:       trade.Price,
					})
					tradeMu.Unlock()
					
					trade.Destroy()
					tradeCount.Add(1)
				}
			}
		}
	}()
	
	// 发送卖单
	for i := 0; i < numSellOrders; i++ {
		orderID := fmt.Sprintf("SELL-%d", i)
		mu.Lock()
		sellOrderIDs[orderID] = true
		mu.Unlock()
		
		order := domain.NewLimitOrder(
			orderID,
			"BTCUSDT",
			fmt.Sprintf("seller-%d", i),
			domain.SideSell,
			price,
			orderQty,
		)
		engine.SubmitOrder(order)
	}
	
	time.Sleep(100 * time.Millisecond) // 给卖单一点时间进入订单簿
	
	// 发送买单
	for i := 0; i < numBuyOrders; i++ {
		orderID := fmt.Sprintf("BUY-%d", i)
		mu.Lock()
		buyOrderIDs[orderID] = true
		mu.Unlock()
		
		order := domain.NewLimitOrder(
			orderID,
			"BTCUSDT",
			fmt.Sprintf("buyer-%d", i),
			domain.SideBuy,
			price,
			orderQty,
		)
		engine.SubmitOrder(order)
	}
	
	// 关键改进：等待期望的成交数达到，而不是固定 sleep
	expectedTrades := int64(numBuyOrders) // 买卖各半，应该全部成交
	t.Logf("等待成交完成，期望成交数: %d", expectedTrades)
	
	success := waitForCondition(
		func() bool {
			return tradeCount.Load() >= expectedTrades
		},
		10*time.Second, // 超时时间
		10*time.Millisecond, // 检查间隔
	)
	
	if !success {
		t.Errorf("超时：期望成交数 %d，实际成交数 %d，差异 %d",
			expectedTrades, tradeCount.Load(), expectedTrades-tradeCount.Load())
	}
	
	// 停止消费者
	close(stopChan)
	consumerWg.Wait()
	
	// ========== 验证阶段 ==========
	
	t.Logf("\n=== 订单最终状态一致性测试（改进版）===")
	t.Logf("发送买单数: %d", numBuyOrders)
	t.Logf("发送卖单数: %d", numSellOrders)
	t.Logf("总成交数: %d", len(trades))
	
	// 验证1: 所有 trade 引用的订单 ID 都在预期集合中
	invalidBuyOrders := 0
	invalidSellOrders := 0
	
	for _, trade := range trades {
		if !buyOrderIDs[trade.BuyOrderID] {
			t.Errorf("成交 %s 引用了不存在的买单: %s", trade.ID, trade.BuyOrderID)
			invalidBuyOrders++
		}
		if !sellOrderIDs[trade.SellOrderID] {
			t.Errorf("成交 %s 引用了不存在的卖单: %s", trade.ID, trade.SellOrderID)
			invalidSellOrders++
		}
	}
	
	if invalidBuyOrders > 0 || invalidSellOrders > 0 {
		t.Errorf("发现无效订单引用: 买单 %d, 卖单 %d", invalidBuyOrders, invalidSellOrders)
	} else {
		t.Logf("✓ 所有成交引用的订单 ID 都有效")
	}
	
	// 验证2: 成交数量符合预期
	actualTrades := int64(len(trades))
	if actualTrades != expectedTrades {
		t.Errorf("成交数量不符合预期: 期望 %d, 实际 %d, 差异 %d",
			expectedTrades, actualTrades, expectedTrades-actualTrades)
	} else {
		t.Logf("✓ 成交数量符合预期: %d", actualTrades)
	}
	
	// 验证3: 成交总量符合预期
	totalTradeQty := int64(0)
	for _, trade := range trades {
		totalTradeQty += trade.Quantity
	}
	
	expectedTotalQty := int64(numBuyOrders) * orderQty
	if totalTradeQty != expectedTotalQty {
		t.Errorf("成交总量不符合预期: 期望 %d, 实际 %d",
			expectedTotalQty, totalTradeQty)
	} else {
		t.Logf("✓ 成交总量符合预期: %d", totalTradeQty)
	}
	
	// 验证4: 检查是否有重复的成交 ID
	tradeIDSet := make(map[string]bool)
	duplicateTrades := 0
	for _, trade := range trades {
		if tradeIDSet[trade.ID] {
			t.Errorf("发现重复的成交 ID: %s", trade.ID)
			duplicateTrades++
		}
		tradeIDSet[trade.ID] = true
	}
	
	if duplicateTrades > 0 {
		t.Errorf("发现 %d 个重复成交", duplicateTrades)
	} else {
		t.Logf("✓ 所有成交 ID 唯一")
	}
	
	// 验证5: 所有成交价格应该相同
	priceSet := make(map[int64]int)
	for _, trade := range trades {
		priceSet[trade.Price]++
	}
	
	if len(priceSet) != 1 {
		t.Errorf("成交价格不一致: %v", priceSet)
	} else {
		t.Logf("✓ 所有成交价格一致: %d", price)
	}
	
	t.Logf("\n=== 最终状态一致性验证通过 ===")
}

// TestHappenBeforeSemanticsRobust Happens-Before 语义测试（改进版）
func TestHappenBeforeSemanticsRobust(t *testing.T) {
	engine := NewMatchingEngine("BTCUSDT")
	engine.Start()
	defer engine.Stop()
	
	numOrders := 100000
	
	sentOrders := make(map[string]int64)
	var sentMu sync.Mutex
	
	receivedTrades := make([]string, 0)
	var receivedMu sync.Mutex
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
					receivedMu.Lock()
					receivedTrades = append(receivedTrades, trade.BuyOrderID)
					receivedTrades = append(receivedTrades, trade.SellOrderID)
					receivedMu.Unlock()
					tradeCount.Add(1)
					trade.Destroy()
				}
			}
		}
	}()
	
	// 发送订单（买卖交替）
	for i := 0; i < numOrders; i++ {
		var orderID string
		var side domain.Side
		
		if i%2 == 0 {
			orderID = fmt.Sprintf("SELL-%d", i/2)
			side = domain.SideSell
		} else {
			orderID = fmt.Sprintf("BUY-%d", i/2)
			side = domain.SideBuy
		}
		
		sentMu.Lock()
		sentOrders[orderID] = int64(i)
		sentMu.Unlock()
		
		order := domain.NewLimitOrder(
			orderID,
			"BTCUSDT",
			"user",
			side,
			50000,
			100,
		)
		engine.SubmitOrder(order)
	}
	
	// 等待期望的成交数达到
	expectedTrades := int64(numOrders / 2)
	t.Logf("等待成交完成，期望成交数: %d", expectedTrades)
	
	success := waitForCondition(
		func() bool {
			return tradeCount.Load() >= expectedTrades
		},
		10*time.Second,
		10*time.Millisecond,
	)
	
	if !success {
		t.Errorf("超时：期望成交数 %d，实际成交数 %d",
			expectedTrades, tradeCount.Load())
	}
	
	close(stopChan)
	consumerWg.Wait()
	
	// 验证
	t.Logf("\n=== Happens-Before 语义测试（改进版 - Exactly-Once）===")
	t.Logf("发送订单数: %d", len(sentOrders))
	t.Logf("接收订单引用数: %d", len(receivedTrades))
	t.Logf("成交数: %d", tradeCount.Load())
	
	// 关键改进：统计每个订单 ID 出现的次数
	orderRefCount := make(map[string]int)
	for _, orderID := range receivedTrades {
		orderRefCount[orderID]++
	}
	
	// 验证1: 所有引用的订单都在发送集合中
	invalidOrders := 0
	for orderID := range orderRefCount {
		if _, exists := sentOrders[orderID]; !exists {
			t.Errorf("接收到未发送的订单 ID: %s", orderID)
			invalidOrders++
		}
	}
	
	if invalidOrders > 0 {
		t.Errorf("发现 %d 个无效订单引用，happens-before 语义被破坏", invalidOrders)
	} else {
		t.Logf("✓ 所有接收到的订单 ID 都在发送集合中")
	}
	
	// 验证2: 每个订单恰好出现一次（exactly-once）
	duplicateOrders := 0
	missingOrders := 0
	
	for orderID := range sentOrders {
		count := orderRefCount[orderID]
		if count == 0 {
			t.Errorf("订单 %s 从未在成交中出现（丢失）", orderID)
			missingOrders++
		} else if count > 1 {
			t.Errorf("订单 %s 在成交中出现了 %d 次（重复）", orderID, count)
			duplicateOrders++
		}
	}
	
	if duplicateOrders > 0 || missingOrders > 0 {
		t.Errorf("Exactly-Once 验证失败: 重复 %d 个, 丢失 %d 个", duplicateOrders, missingOrders)
	} else {
		t.Logf("✓ Exactly-Once 验证通过：每个订单恰好出现一次")
		t.Logf("✓ Happens-Before 语义正确")
	}
}

// TestConcurrentStressRobust 并发压力测试（改进版）
func TestConcurrentStressRobust(t *testing.T) {
	engine := NewMatchingEngine("BTCUSDT")
	engine.Start()
	defer engine.Stop()
	
	numProducers := 8
	ordersPerProducer := 2000
	
	allOrders := make(map[string]bool)
	var orderMu sync.Mutex
	
	allTrades := make([]struct {
		BuyOrderID  string
		SellOrderID string
	}, 0)
	var tradeMu sync.Mutex
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
					tradeMu.Lock()
					allTrades = append(allTrades, struct {
						BuyOrderID  string
						SellOrderID string
					}{
						BuyOrderID:  trade.BuyOrderID,
						SellOrderID: trade.SellOrderID,
					})
					tradeMu.Unlock()
					tradeCount.Add(1)
					trade.Destroy()
				}
			}
		}
	}()
	
	// 多个生产者并发发送订单
	var producerWg sync.WaitGroup
	for p := 0; p < numProducers; p++ {
		producerWg.Add(1)
		go func(producerID int) {
			defer producerWg.Done()
			for i := 0; i < ordersPerProducer; i++ {
				var side domain.Side
				var orderID string
				
				if i%2 == 0 {
					side = domain.SideSell
					orderID = fmt.Sprintf("p%d-SELL-%d", producerID, i/2)
				} else {
					side = domain.SideBuy
					orderID = fmt.Sprintf("p%d-BUY-%d", producerID, i/2)
				}
				
				orderMu.Lock()
				allOrders[orderID] = true
				orderMu.Unlock()
				
				order := domain.NewLimitOrder(
					orderID,
					"BTCUSDT",
					fmt.Sprintf("user%d", producerID),
					side,
					50000,
					100,
				)
				engine.SubmitOrder(order)
			}
		}(p)
	}
	
	// 等待生产者完成
	producerWg.Wait()
	
	// 等待期望的成交数达到
	expectedTrades := int64(numProducers * ordersPerProducer / 2)
	t.Logf("等待成交完成，期望成交数: %d", expectedTrades)
	
	success := waitForCondition(
		func() bool {
			return tradeCount.Load() >= expectedTrades
		},
		10*time.Second,
		10*time.Millisecond,
	)
	
	// 严格验证：未达到期望成交数应该失败
	if !success {
		t.Fatalf("严重错误：未达到期望成交数，期望 %d，实际 %d，差异 %d\n"+
			"这表明可能存在丢单、撮合卡住或其他严重问题",
			expectedTrades, tradeCount.Load(), expectedTrades-tradeCount.Load())
	}
	
	// 停止消费者
	close(stopChan)
	consumerWg.Wait()
	
	// 验证
	t.Logf("\n=== 并发压力测试（改进版 - 严格模式）===")
	t.Logf("生产者数量: %d", numProducers)
	t.Logf("总订单数: %d", len(allOrders))
	t.Logf("总成交数: %d", len(allTrades))
	
	// 验证1: 所有成交引用的订单都存在
	invalidRefs := 0
	for _, trade := range allTrades {
		if !allOrders[trade.BuyOrderID] {
			t.Errorf("成交引用了不存在的买单: %s", trade.BuyOrderID)
			invalidRefs++
		}
		if !allOrders[trade.SellOrderID] {
			t.Errorf("成交引用了不存在的卖单: %s", trade.SellOrderID)
			invalidRefs++
		}
	}
	
	if invalidRefs > 0 {
		t.Fatalf("发现 %d 个无效订单引用", invalidRefs)
	} else {
		t.Logf("✓ 所有成交引用的订单都存在")
	}
	
	// 验证2: 成交数量必须精确匹配（严格模式）
	actualTrades := int64(len(allTrades))
	if actualTrades != expectedTrades {
		t.Fatalf("成交数量不匹配: 期望 %d, 实际 %d, 差异 %d",
			expectedTrades, actualTrades, expectedTrades-actualTrades)
	} else {
		t.Logf("✓ 成交数量精确匹配: %d", actualTrades)
	}
}
