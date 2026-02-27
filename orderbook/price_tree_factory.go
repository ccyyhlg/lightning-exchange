package orderbook

import (
	"container/list"
	"lightning-exchange/domain"
)

// PriceTreeType 定义价格树的实现类型
type PriceTreeType int

const (
	// HashMapListType HashMap + Doubly Linked List 实现
	// 适用场景：价格档位 < 100
	// 性能：最佳价格查询 O(1)，插入 O(n)，删除 O(1)
	HashMapListType PriceTreeType = iota
	
	// ShardedType 分片 + 位运算优化实现（推荐）
	// 适用场景：价格档位 >= 100（理论上支持任意规模）
	// 性能：最佳价格查询 O(1)，插入 O(log m)，删除 O(log m)
	// 优势：档位越多，性能优势越明显（比 HashMap+List 快 8.5%）
	ShardedType
)

// NewPriceTreeWithType 根据类型创建价格树
func NewPriceTreeWithType(treeType PriceTreeType, descending bool) PriceTreeInterface {
	switch treeType {
	case ShardedType:
		return NewShardedPriceTreeFromInterface(descending, 128) // bucket size = 128 (2^7，可用位运算优化)
	case HashMapListType:
		fallthrough
	default:
		return NewHashMapListPriceTree(descending)
	}
}

// NewShardedPriceTreeFromInterface 创建分片价格树（实现接口）
func NewShardedPriceTreeFromInterface(descending bool, bucketSize int64) PriceTreeInterface {
	return &ShardedPriceTreeAdapter{
		tree: NewShardedPriceTree(descending, bucketSize), // descending = isBuy
	}
}

// ShardedPriceTreeAdapter 适配器，让 ShardedPriceTree 实现 PriceTreeInterface
type ShardedPriceTreeAdapter struct {
	tree *ShardedPriceTree
}

// Ensure ShardedPriceTreeAdapter implements PriceTreeInterface
var _ PriceTreeInterface = (*ShardedPriceTreeAdapter)(nil)

func (s *ShardedPriceTreeAdapter) Insert(order *domain.Order) {
	bucketID := order.Price / s.tree.bucketSize
	level, exists := s.tree.buckets.Get(bucketID)
	var bucket *Bucket
	if !exists {
		bucket = NewBucket(bucketID, s.tree.isBuy, s.tree.bucketSize)
		s.tree.buckets.Put(bucketID, bucket)
	} else {
		bucket = level
	}
	
	// 创建或获取价格档位（使用位运算索引）
	index := order.Price & bucket.bucketMask
	priceLevel := bucket.levels[index]
	levelExists := priceLevel != nil
	if !levelExists {
		priceLevel = &PriceLevel_{
			Price:  order.Price,
			Orders: list.New(),
			Volume: 0,
		}
		bucket.Insert(order.Price, priceLevel)
	}
	
	// 添加订单到 FIFO 队列
	elem := priceLevel.Orders.PushBack(order)
	order.ListElement = elem
	priceLevel.Volume += order.RemainingQuantity()
	
	// 更新全局最佳价格
	if s.tree.bestBucket == nil {
		s.tree.bestBucket = bucket
		s.tree.bestPrice = bucket.bestPrice
	} else if s.tree.isBetterBucket(bucketID, s.tree.bestBucket.bucketID) {
		s.tree.bestBucket = bucket
		s.tree.bestPrice = bucket.bestPrice
	} else if bucket == s.tree.bestBucket {
		// 同一个 bucket，更新最佳价格
		s.tree.bestPrice = bucket.bestPrice
	}
}

func (s *ShardedPriceTreeAdapter) Remove(order *domain.Order) {
	level, exists := s.tree.buckets.Get(order.Price / s.tree.bucketSize)
	if !exists {
		return
	}
	
	bucket := level
	// 使用位运算索引获取价格档位
	index := order.Price & bucket.bucketMask
	priceLevel := bucket.levels[index]
	levelExists := priceLevel != nil
	if !levelExists {
		return
	}
	
	// 从 FIFO 队列删除订单
	if order.ListElement != nil {
		elem := order.ListElement.(*list.Element)
		priceLevel.Orders.Remove(elem)
		order.ListElement = nil
		priceLevel.Volume -= order.RemainingQuantity()
	}
	
	// 如果价格档位为空，删除它
	if priceLevel.Orders.Len() == 0 {
		s.tree.Remove(order.Price)
	}
}

func (s *ShardedPriceTreeAdapter) GetBestPrice() int64 {
	best := s.tree.GetBestPrice()
	if best == nil {
		return 0
	}
	return best.Price
}

func (s *ShardedPriceTreeAdapter) GetBestLevel() *PriceLevel_ {
	return s.tree.GetBestPrice()
}

func (s *ShardedPriceTreeAdapter) GetBestOrders() []*domain.Order {
	bestLevel := s.tree.GetBestPrice()
	if bestLevel == nil {
		return nil
	}
	
	orders := make([]*domain.Order, 0, bestLevel.Orders.Len())
	for e := bestLevel.Orders.Front(); e != nil; e = e.Next() {
		orders = append(orders, e.Value.(*domain.Order))
	}
	
	return orders
}

func (s *ShardedPriceTreeAdapter) GetLevel(price int64) *PriceLevel_ {
	bucket, exists := s.tree.buckets.Get(price / s.tree.bucketSize)
	if !exists {
		return nil
	}
	// Fix: use bitwise AND for indexing
	index := price & bucket.bucketMask
	return bucket.levels[index]
}

func (s *ShardedPriceTreeAdapter) GetDepth(maxLevels int) []PriceLevel_ {
	if maxLevels <= 0 || s.tree.buckets.Empty() {
		return nil
	}
	
	result := make([]PriceLevel_, 0, maxLevels)
	count := 0
	
	// Iterate through red-black tree (already sorted)
	it := s.tree.buckets.Iterator()
	for it.Next() && count < maxLevels {
		bucket := it.Value()
		
		// Iterate through bucket's linked list (already sorted)
		current := bucket.bestPrice
		for current != nil && count < maxLevels {
			result = append(result, *current)
			count++
			current = current.NextPrice
		}
	}
	
	return result
}

func (s *ShardedPriceTreeAdapter) IsEmpty() bool {
	return s.tree.buckets.Empty()
}

func (s *ShardedPriceTreeAdapter) Size() int {
	count := 0
	it := s.tree.buckets.Iterator()
	for it.Next() {
		count += len(it.Value().levels)
	}
	return count
}
