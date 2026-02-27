package orderbook

import (
	rbt "github.com/emirpasic/gods/v2/trees/redblacktree"
)

// ShardedPriceTree 使用分片 + Ordered Map 架构
// 外层：红黑树管理 bucket（O(log m)）
// 内层：HashMap 存储价格档位（O(1)）
type ShardedPriceTree struct {
	buckets    *rbt.Tree[int64, *Bucket] // Ordered Map of buckets
	bestBucket *Bucket                    // 缓存最佳 bucket
	bestPrice  *PriceLevel_               // 缓存最佳价格
	isBuy      bool
	bucketSize int64 // 每个 bucket 的价格范围（例如 100）
}

// Bucket 代表一个价格分片
// 内部使用固定数组 + Doubly Linked List（用空间换时间）
type Bucket struct {
	bucketID   int64             // bucket ID (price / bucketSize)
	levels     [128]*PriceLevel_ // 固定数组（128 = 2^7，可用位运算优化）
	bestPrice  *PriceLevel_      // bucket 内最佳价格（链表头）
	size       int               // bucket 中的元素数量
	isBuy      bool
	bucketSize int64             // bucket 大小
	bucketMask int64             // 用于位运算的掩码（bucketSize - 1）
}

// NewShardedPriceTree 创建分片价格树
func NewShardedPriceTree(isBuy bool, bucketSize int64) *ShardedPriceTree {
	var comparator func(a, b int64) int
	if isBuy {
		// 买单：bucket ID 从大到小
		comparator = func(a, b int64) int {
			if a > b {
				return -1
			} else if a < b {
				return 1
			}
			return 0
		}
	} else {
		// 卖单：bucket ID 从小到大
		comparator = func(a, b int64) int {
			if a < b {
				return -1
			} else if a > b {
				return 1
			}
			return 0
		}
	}

	return &ShardedPriceTree{
		buckets:    rbt.NewWith[int64, *Bucket](comparator),
		isBuy:      isBuy,
		bucketSize: bucketSize,
	}
}

// NewBucket 创建新的 bucket
func NewBucket(bucketID int64, isBuy bool, bucketSize int64) *Bucket {
	return &Bucket{
		bucketID:   bucketID,
		isBuy:      isBuy,
		bucketSize: bucketSize,
		bucketMask: bucketSize - 1, // 用于位运算：price & mask 等价于 price % bucketSize
	}
}

// Insert 插入价格档位
// 性能：O(log m) + O(1) = O(log m)，m = bucket 数量
func (spt *ShardedPriceTree) Insert(price int64, level *PriceLevel_) {
	bucketID := price / spt.bucketSize
	
	// 查找或创建 bucket - O(log m)
	bucket, found := spt.buckets.Get(bucketID)
	if !found {
		bucket = NewBucket(bucketID, spt.isBuy, spt.bucketSize)
		spt.buckets.Put(bucketID, bucket)
	}
	
	// 在 bucket 内插入 - O(1)
	bucket.Insert(price, level)
	
	// 更新最佳价格 - O(1)
	spt.updateBestPrice(bucket)
}

// Remove 删除价格档位
// 性能：O(log m) + O(1) = O(log m)
func (spt *ShardedPriceTree) Remove(price int64) {
	bucketID := price / spt.bucketSize
	
	// 查找 bucket - O(log m)
	bucket, found := spt.buckets.Get(bucketID)
	if !found {
		return
	}
	
	// 从 bucket 删除 - O(1)
	bucket.Remove(price)
	
	// 如果 bucket 为空，删除 bucket
	if bucket.size == 0 {
		spt.buckets.Remove(bucketID)
		if spt.bestBucket == bucket {
			spt.bestBucket = nil
			spt.bestPrice = nil
			spt.updateBestPriceFromTree()
		}
	} else {
		// 更新 bucket 内最佳价格
		bucket.updateBestPrice()
		// 如果影响到全局最佳价格，更新
		if spt.bestPrice != nil && spt.bestPrice.Price == price {
			spt.updateBestPriceFromTree()
		}
	}
}

// GetBestPrice 获取最佳价格
// 性能：O(1)
func (spt *ShardedPriceTree) GetBestPrice() *PriceLevel_ {
	return spt.bestPrice
}

// updateBestPrice 更新最佳价格（当插入到可能的最佳 bucket 时）
func (spt *ShardedPriceTree) updateBestPrice(bucket *Bucket) {
	if spt.bestBucket == nil {
		spt.bestBucket = bucket
		spt.bestPrice = bucket.bestPrice
		return
	}
	
	// 检查新 bucket 是否更好
	if spt.isBetterBucket(bucket.bucketID, spt.bestBucket.bucketID) {
		spt.bestBucket = bucket
		spt.bestPrice = bucket.bestPrice
	} else if bucket == spt.bestBucket {
		// 同一个 bucket，更新最佳价格
		spt.bestPrice = bucket.bestPrice
	}
}

// updateBestPriceFromTree 从树中重新查找最佳价格
func (spt *ShardedPriceTree) updateBestPriceFromTree() {
	if spt.buckets.Empty() {
		spt.bestBucket = nil
		spt.bestPrice = nil
		return
	}
	
	// 红黑树的第一个节点就是最佳 bucket
	node := spt.buckets.Left()
	if node != nil {
		spt.bestBucket = node.Value
		spt.bestPrice = node.Value.bestPrice
	}
}

func (spt *ShardedPriceTree) isBetterBucket(newBucketID, existingBucketID int64) bool {
	if spt.isBuy {
		return newBucketID > existingBucketID
	}
	return newBucketID < existingBucketID
}

// ========== Bucket 方法 ==========

// Insert 在 bucket 内插入价格档位
// 使用数组索引（位运算优化）+ Doubly Linked List 维护顺序
func (b *Bucket) Insert(price int64, level *PriceLevel_) {
	// 使用位运算计算索引：price & mask 等价于 price % bucketSize
	// 但位运算比取模快 5-10 倍
	index := price & b.bucketMask
	b.levels[index] = level
	b.size++
	
	// 插入到链表中（维护价格顺序）
	if b.bestPrice == nil {
		b.bestPrice = level
		return
	}
	
	// 检查是否应该成为新的最佳价格
	if b.isBetterPrice(level.Price, b.bestPrice.Price) {
		level.NextPrice = b.bestPrice
		b.bestPrice.PrevPrice = level
		b.bestPrice = level
		return
	}
	
	// 在链表中找到插入位置（O(n)，但 n 很小，通常 < 100）
	current := b.bestPrice
	for current.NextPrice != nil {
		if b.isBetterPrice(level.Price, current.NextPrice.Price) {
			break
		}
		current = current.NextPrice
	}
	
	// 插入到 current 之后
	level.NextPrice = current.NextPrice
	level.PrevPrice = current
	if current.NextPrice != nil {
		current.NextPrice.PrevPrice = level
	}
	current.NextPrice = level
}

// Remove 从 bucket 删除价格档位
// 使用链表的 O(1) 删除
func (b *Bucket) Remove(price int64) {
	// 使用位运算计算索引
	index := price & b.bucketMask
	level := b.levels[index]
	if level == nil {
		return
	}
	
	b.levels[index] = nil
	b.size--
	
	// 从链表中删除（O(1)）
	if level.PrevPrice != nil {
		level.PrevPrice.NextPrice = level.NextPrice
	} else {
		// 删除的是最佳价格，更新为下一个
		b.bestPrice = level.NextPrice
	}
	
	if level.NextPrice != nil {
		level.NextPrice.PrevPrice = level.PrevPrice
	}
	
	// 清理指针
	level.NextPrice = nil
	level.PrevPrice = nil
}

// updateBestPrice 更新 bucket 内最佳价格（遍历链表）
func (b *Bucket) updateBestPrice() {
	// 链表头就是最佳价格，无需遍历
	// 这个方法保留用于兼容性，但实际上不需要了
}

func (b *Bucket) isBetterPrice(newPrice, existingPrice int64) bool {
	if b.isBuy {
		return newPrice > existingPrice
	}
	return newPrice < existingPrice
}
