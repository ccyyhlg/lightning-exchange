package orderbook

import "lightning-exchange/domain"

// PriceTreeInterface 定义价格树的接口
// 支持多种实现：HashMap+List、红黑树、分片树等
type PriceTreeInterface interface {
	// Insert 插入订单到价格树
	Insert(order *domain.Order)
	
	// Remove 从价格树删除订单
	Remove(order *domain.Order)
	
	// GetBestPrice 获取最佳价格（返回价格值）
	GetBestPrice() int64
	
	// GetBestLevel 获取最佳价格档位
	GetBestLevel() *PriceLevel_
	
	// GetBestOrders 获取最佳价格的所有订单（用于撮合）
	GetBestOrders() []*domain.Order
	
	// GetLevel 获取指定价格的档位
	GetLevel(price int64) *PriceLevel_
	
	// GetDepth 获取市场深度（前 N 档）
	GetDepth(maxLevels int) []PriceLevel_
	
	// IsEmpty 判断是否为空
	IsEmpty() bool
	
	// Size 返回价格档位数量
	Size() int
}
