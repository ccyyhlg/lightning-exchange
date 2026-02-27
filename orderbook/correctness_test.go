package orderbook

import (
	"lightning-exchange/domain"
	"testing"
)

// TestAddOrder 测试添加订单
func TestAddOrder(t *testing.T) {
	ob := NewOrderBook("BTCUSDT")

	// 添加卖单
	sell := domain.NewLimitOrder("sell1", "BTCUSDT", "user1", domain.SideSell, 50000, 100000000)
	ob.AddOrder(sell)

	// 验证最佳卖价
	if ob.GetBestAsk() != 50000 {
		t.Errorf("expected best ask 50000, got %d", ob.GetBestAsk())
	}

	// 添加买单
	buy := domain.NewLimitOrder("buy1", "BTCUSDT", "user2", domain.SideBuy, 49000, 100000000)
	ob.AddOrder(buy)

	// 验证最佳买价
	if ob.GetBestBid() != 49000 {
		t.Errorf("expected best bid 49000, got %d", ob.GetBestBid())
	}
}

// TestCancelOrder 测试撤单
func TestCancelOrder(t *testing.T) {
	ob := NewOrderBook("BTCUSDT")

	// 添加订单
	order := domain.NewLimitOrder("order1", "BTCUSDT", "user1", domain.SideSell, 50000, 100000000)
	ob.AddOrder(order)

	// 验证订单在订单簿中
	if ob.GetBestAsk() != 50000 {
		t.Errorf("expected best ask 50000, got %d", ob.GetBestAsk())
	}

	// 撤单
	ob.CancelOrder("order1")

	// 验证订单已删除
	if ob.GetBestAsk() != 0 {
		t.Error("expected asks to be empty after cancel")
	}
}

// TestPricePriority 测试价格优先
func TestPricePriority(t *testing.T) {
	ob := NewOrderBook("BTCUSDT")

	// 添加不同价格的卖单
	sell1 := domain.NewLimitOrder("sell1", "BTCUSDT", "user1", domain.SideSell, 51000, 100000000)
	sell2 := domain.NewLimitOrder("sell2", "BTCUSDT", "user2", domain.SideSell, 50000, 100000000) // 最优
	sell3 := domain.NewLimitOrder("sell3", "BTCUSDT", "user3", domain.SideSell, 52000, 100000000)

	ob.AddOrder(sell1)
	ob.AddOrder(sell2)
	ob.AddOrder(sell3)

	// 验证最佳价格是 50000
	if ob.GetBestAsk() != 50000 {
		t.Errorf("expected best ask 50000, got %d", ob.GetBestAsk())
	}
}

// TestGetLevel 测试获取指定价格档位
func TestGetLevel(t *testing.T) {
	ob := NewOrderBook("BTCUSDT")

	// 添加订单
	order := domain.NewLimitOrder("order1", "BTCUSDT", "user1", domain.SideSell, 50000, 100000000)
	ob.AddOrder(order)

	// 获取价格档位
	level := ob.asks.GetLevel(50000)
	if level == nil {
		t.Fatal("expected level to exist")
	}

	if level.Price != 50000 {
		t.Errorf("expected price 50000, got %d", level.Price)
	}

	if level.Volume != 100000000 {
		t.Errorf("expected volume 100000000, got %d", level.Volume)
	}
}

// TestGetDepth 测试市场深度
func TestGetDepth(t *testing.T) {
	ob := NewOrderBook("BTCUSDT")

	// 添加多个价格档位
	ob.AddOrder(domain.NewLimitOrder("sell1", "BTCUSDT", "user1", domain.SideSell, 50000, 100000000))
	ob.AddOrder(domain.NewLimitOrder("sell2", "BTCUSDT", "user2", domain.SideSell, 50100, 100000000))
	ob.AddOrder(domain.NewLimitOrder("sell3", "BTCUSDT", "user3", domain.SideSell, 50200, 100000000))

	// 获取深度
	depth := ob.asks.GetDepth(2)

	if len(depth) != 2 {
		t.Errorf("expected 2 levels, got %d", len(depth))
	}

	// 验证顺序（最优价格在前）
	if depth[0].Price != 50000 {
		t.Errorf("expected first level at 50000, got %d", depth[0].Price)
	}
	if depth[1].Price != 50100 {
		t.Errorf("expected second level at 50100, got %d", depth[1].Price)
	}
}

// TestFIFOOrder 测试 FIFO 时间优先
func TestFIFOOrder(t *testing.T) {
	ob := NewOrderBook("BTCUSDT")

	// 同价格的三个卖单
	sell1 := domain.NewLimitOrder("sell1", "BTCUSDT", "user1", domain.SideSell, 50000, 50000000)
	sell2 := domain.NewLimitOrder("sell2", "BTCUSDT", "user2", domain.SideSell, 50000, 50000000)
	sell3 := domain.NewLimitOrder("sell3", "BTCUSDT", "user3", domain.SideSell, 50000, 50000000)

	ob.AddOrder(sell1)
	ob.AddOrder(sell2)
	ob.AddOrder(sell3)

	// 获取最佳价格档位
	level := ob.asks.GetBestLevel()
	if level == nil {
		t.Fatal("expected level to exist")
	}

	// 验证订单数量
	if level.Orders.Len() != 3 {
		t.Errorf("expected 3 orders, got %d", level.Orders.Len())
	}

	// 验证 FIFO 顺序
	orders := ob.asks.GetBestOrders()
	if len(orders) != 3 {
		t.Fatalf("expected 3 orders, got %d", len(orders))
	}

	if orders[0].ID != "sell1" {
		t.Errorf("first order should be sell1, got %s", orders[0].ID)
	}
	if orders[1].ID != "sell2" {
		t.Errorf("second order should be sell2, got %s", orders[1].ID)
	}
	if orders[2].ID != "sell3" {
		t.Errorf("third order should be sell3, got %s", orders[2].ID)
	}
}

// TestBidsDepth 测试买单的市场深度（验证 iterator 顺序从高到低）
func TestBidsDepth(t *testing.T) {
	ob := NewOrderBook("BTCUSDT")

	// 添加多个价格档位的买单（乱序添加）
	buy1 := domain.NewLimitOrder("buy1", "BTCUSDT", "user1", domain.SideBuy, 49000, 100000000)
	buy2 := domain.NewLimitOrder("buy2", "BTCUSDT", "user2", domain.SideBuy, 50000, 100000000) // 最高价
	buy3 := domain.NewLimitOrder("buy3", "BTCUSDT", "user3", domain.SideBuy, 48000, 100000000)

	ob.AddOrder(buy1)
	ob.AddOrder(buy2)
	ob.AddOrder(buy3)

	// 验证最佳买价是 50000（最高价）
	if ob.GetBestBid() != 50000 {
		t.Errorf("expected best bid 50000, got %d", ob.GetBestBid())
	}

	// 获取深度（前 3 档）
	depth := ob.bids.GetDepth(3)

	if len(depth) != 3 {
		t.Errorf("expected 3 levels, got %d", len(depth))
	}

	// 验证顺序：买单应该从高到低排序
	if depth[0].Price != 50000 {
		t.Errorf("expected first level at 50000, got %d", depth[0].Price)
	}
	if depth[1].Price != 49000 {
		t.Errorf("expected second level at 49000, got %d", depth[1].Price)
	}
	if depth[2].Price != 48000 {
		t.Errorf("expected third level at 48000, got %d", depth[2].Price)
	}

	// 验证每档的数量
	for i, level := range depth {
		if level.Volume != 100000000 {
			t.Errorf("expected level %d volume 100000000, got %d", i, level.Volume)
		}
	}
}

// TestAsksDepth 测试卖单的市场深度（验证 iterator 顺序从低到高）
func TestAsksDepth(t *testing.T) {
	ob := NewOrderBook("BTCUSDT")

	// 添加多个价格档位的卖单（乱序添加）
	sell1 := domain.NewLimitOrder("sell1", "BTCUSDT", "user1", domain.SideSell, 51000, 100000000)
	sell2 := domain.NewLimitOrder("sell2", "BTCUSDT", "user2", domain.SideSell, 50000, 100000000) // 最低价
	sell3 := domain.NewLimitOrder("sell3", "BTCUSDT", "user3", domain.SideSell, 52000, 100000000)

	ob.AddOrder(sell1)
	ob.AddOrder(sell2)
	ob.AddOrder(sell3)

	// 验证最佳卖价是 50000（最低价）
	if ob.GetBestAsk() != 50000 {
		t.Errorf("expected best ask 50000, got %d", ob.GetBestAsk())
	}

	// 获取深度（前 3 档）
	depth := ob.asks.GetDepth(3)

	if len(depth) != 3 {
		t.Errorf("expected 3 levels, got %d", len(depth))
	}

	// 验证顺序：卖单应该从低到高排序
	if depth[0].Price != 50000 {
		t.Errorf("expected first level at 50000, got %d", depth[0].Price)
	}
	if depth[1].Price != 51000 {
		t.Errorf("expected second level at 51000, got %d", depth[1].Price)
	}
	if depth[2].Price != 52000 {
		t.Errorf("expected third level at 52000, got %d", depth[2].Price)
	}

	// 验证每档的数量
	for i, level := range depth {
		if level.Volume != 100000000 {
			t.Errorf("expected level %d volume 100000000, got %d", i, level.Volume)
		}
	}
}
