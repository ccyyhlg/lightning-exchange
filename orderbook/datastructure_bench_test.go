package orderbook

import (
	"container/list"
	"math/rand"
	"testing"

	rbt "github.com/emirpasic/gods/v2/trees/redblacktree"
)

// 测试 HashMap+List vs 红黑树（用 Go 标准库的 map 模拟）
// 场景：交易所订单簿（价格档位 < 100）

// HashMap + Linked List 实现（当前）
type HashMapList struct {
	levels    map[int64]*PriceLevel_
	bestPrice *PriceLevel_
	isBuy     bool
}

func NewHashMapList(isBuy bool) *HashMapList {
	return &HashMapList{
		levels: make(map[int64]*PriceLevel_),
		isBuy:  isBuy,
	}
}

func (h *HashMapList) Insert(price int64) {
	if _, exists := h.levels[price]; exists {
		return
	}

	newLevel := &PriceLevel_{
		Price:  price,
		Orders: list.New(),
		Volume: 0,
	}
	h.levels[price] = newLevel

	// O(n) 插入到链表
	if h.bestPrice == nil {
		h.bestPrice = newLevel
		return
	}

	if h.isBetterPrice(newLevel.Price, h.bestPrice.Price) {
		newLevel.NextPrice = h.bestPrice
		h.bestPrice.PrevPrice = newLevel
		h.bestPrice = newLevel
		return
	}

	current := h.bestPrice
	for current.NextPrice != nil {
		if h.isBetterPrice(newLevel.Price, current.NextPrice.Price) {
			break
		}
		current = current.NextPrice
	}

	newLevel.NextPrice = current.NextPrice
	newLevel.PrevPrice = current
	if current.NextPrice != nil {
		current.NextPrice.PrevPrice = newLevel
	}
	current.NextPrice = newLevel
}

func (h *HashMapList) GetBest() int64 {
	if h.bestPrice == nil {
		return 0
	}
	return h.bestPrice.Price
}

func (h *HashMapList) Remove(price int64) {
	level, exists := h.levels[price]
	if !exists {
		return
	}

	delete(h.levels, price)

	if level.PrevPrice != nil {
		level.PrevPrice.NextPrice = level.NextPrice
	} else {
		h.bestPrice = level.NextPrice
	}

	if level.NextPrice != nil {
		level.NextPrice.PrevPrice = level.PrevPrice
	}
}

func (h *HashMapList) isBetterPrice(newPrice, existingPrice int64) bool {
	if h.isBuy {
		return newPrice > existingPrice
	}
	return newPrice < existingPrice
}

// 模拟红黑树（用 sorted slice，性能类似）
type SortedSlice struct {
	prices []int64
	isBuy  bool
}

func NewSortedSlice(isBuy bool) *SortedSlice {
	return &SortedSlice{
		prices: make([]int64, 0, 100),
		isBuy:  isBuy,
	}
}

func (s *SortedSlice) Insert(price int64) {
	// 二分查找插入位置 O(log n)
	left, right := 0, len(s.prices)
	for left < right {
		mid := (left + right) / 2
		if s.isBetterPrice(price, s.prices[mid]) {
			right = mid
		} else {
			left = mid + 1
		}
	}

	// 插入 O(n)（模拟红黑树的旋转开销）
	s.prices = append(s.prices, 0)
	copy(s.prices[left+1:], s.prices[left:])
	s.prices[left] = price
}

func (s *SortedSlice) GetBest() int64 {
	if len(s.prices) == 0 {
		return 0
	}
	return s.prices[0]
}

func (s *SortedSlice) Remove(price int64) {
	// 二分查找 O(log n)
	left, right := 0, len(s.prices)
	for left < right {
		mid := (left + right) / 2
		if s.prices[mid] < price {
			left = mid + 1
		} else {
			right = mid
		}
	}

	if left < len(s.prices) && s.prices[left] == price {
		s.prices = append(s.prices[:left], s.prices[left+1:]...)
	}
}

func (s *SortedSlice) isBetterPrice(newPrice, existingPrice int64) bool {
	if s.isBuy {
		return newPrice > existingPrice
	}
	return newPrice < existingPrice
}

// Benchmark: HashMap+List vs 红黑树（模拟）
// 场景：100 个价格档位，随机插入/删除

func BenchmarkHashMapList_Insert_100(b *testing.B) {
	prices := generatePrices(100)
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		h := NewHashMapList(true)
		for _, price := range prices {
			h.Insert(price)
		}
	}
}

func BenchmarkHashMapList_Insert_1000(b *testing.B) {
	prices := generatePrices(1000)
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		h := NewHashMapList(true)
		for _, price := range prices {
			h.Insert(price)
		}
	}
}

func BenchmarkHashMapList_Insert_10000(b *testing.B) {
	prices := generatePrices(10000)
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		h := NewHashMapList(true)
		for _, price := range prices {
			h.Insert(price)
		}
	}
}

func BenchmarkSortedSlice_Insert(b *testing.B) {
	prices := generatePrices(100)
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		s := NewSortedSlice(true)
		for _, price := range prices {
			s.Insert(price)
		}
	}
}

func BenchmarkHashMapList_GetBest(b *testing.B) {
	h := NewHashMapList(true)
	prices := generatePrices(100)
	for _, price := range prices {
		h.Insert(price)
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = h.GetBest()
	}
}

func BenchmarkSortedSlice_GetBest(b *testing.B) {
	s := NewSortedSlice(true)
	prices := generatePrices(100)
	for _, price := range prices {
		s.Insert(price)
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = s.GetBest()
	}
}

func BenchmarkHashMapList_Remove(b *testing.B) {
	prices := generatePrices(100)
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		b.StopTimer()
		h := NewHashMapList(true)
		for _, price := range prices {
			h.Insert(price)
		}
		b.StartTimer()

		for _, price := range prices {
			h.Remove(price)
		}
	}
}

func BenchmarkSortedSlice_Remove(b *testing.B) {
	prices := generatePrices(100)
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		b.StopTimer()
		s := NewSortedSlice(true)
		for _, price := range prices {
			s.Insert(price)
		}
		b.StartTimer()

		for _, price := range prices {
			s.Remove(price)
		}
	}
}

func generatePrices(n int) []int64 {
	prices := make([]int64, n)
	// 生成唯一的价格序列（避免重复）
	for i := 0; i < n; i++ {
		prices[i] = 50000 + int64(i)
	}
	// 打乱顺序（模拟随机插入）
	rand.Shuffle(n, func(i, j int) {
		prices[i], prices[j] = prices[j], prices[i]
	})
	return prices
}

// ============ 真实红黑树实现 (gods 库) ============

type RedBlackTree struct {
	tree  *rbt.Tree[int64, *PriceLevel_]
	isBuy bool
}

func NewRedBlackTree(isBuy bool) *RedBlackTree {
	var comparator func(a, b int64) int
	if isBuy {
		// 买单：价格从高到低
		comparator = func(a, b int64) int {
			if a > b {
				return -1
			} else if a < b {
				return 1
			}
			return 0
		}
	} else {
		// 卖单：价格从低到高
		comparator = func(a, b int64) int {
			if a < b {
				return -1
			} else if a > b {
				return 1
			}
			return 0
		}
	}

	return &RedBlackTree{
		tree:  rbt.NewWith[int64, *PriceLevel_](comparator),
		isBuy: isBuy,
	}
}

func (r *RedBlackTree) Insert(price int64) {
	if _, found := r.tree.Get(price); found {
		return
	}

	newLevel := &PriceLevel_{
		Price:  price,
		Orders: list.New(),
		Volume: 0,
	}
	r.tree.Put(price, newLevel)
}

func (r *RedBlackTree) GetBest() int64 {
	if r.tree.Empty() {
		return 0
	}
	// 红黑树的第一个元素就是最佳价格
	node := r.tree.Left()
	if node != nil {
		return node.Key
	}
	return 0
}

func (r *RedBlackTree) Remove(price int64) {
	r.tree.Remove(price)
}

// ============ 分片价格树实现 ============

type ShardedPriceTreeWrapper struct {
	tree *ShardedPriceTree
}

func NewShardedPriceTreeWrapper(isBuy bool) *ShardedPriceTreeWrapper {
	return &ShardedPriceTreeWrapper{
		tree: NewShardedPriceTree(isBuy, 100), // bucket size = 100
	}
}

func (s *ShardedPriceTreeWrapper) Insert(price int64) {
	level := &PriceLevel_{
		Price:  price,
		Orders: list.New(),
		Volume: 0,
	}
	s.tree.Insert(price, level)
}

func (s *ShardedPriceTreeWrapper) GetBest() int64 {
	best := s.tree.GetBestPrice()
	if best == nil {
		return 0
	}
	return best.Price
}

func (s *ShardedPriceTreeWrapper) Remove(price int64) {
	s.tree.Remove(price)
}

// ============ Benchmark: HashMap+List vs 红黑树 vs 分片树 ============
// 测试不同规模：100, 1000, 10000 个价格档位

func BenchmarkRedBlackTree_Insert_100(b *testing.B) {
	prices := generatePrices(100)
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		r := NewRedBlackTree(true)
		for _, price := range prices {
			r.Insert(price)
		}
	}
}

func BenchmarkRedBlackTree_Insert_1000(b *testing.B) {
	prices := generatePrices(1000)
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		r := NewRedBlackTree(true)
		for _, price := range prices {
			r.Insert(price)
		}
	}
}

func BenchmarkRedBlackTree_Insert_10000(b *testing.B) {
	prices := generatePrices(10000)
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		r := NewRedBlackTree(true)
		for _, price := range prices {
			r.Insert(price)
		}
	}
}

func BenchmarkRedBlackTree_GetBest(b *testing.B) {
	r := NewRedBlackTree(true)
	prices := generatePrices(100)
	for _, price := range prices {
		r.Insert(price)
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = r.GetBest()
	}
}

func BenchmarkRedBlackTree_Remove(b *testing.B) {
	prices := generatePrices(100)
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		b.StopTimer()
		r := NewRedBlackTree(true)
		for _, price := range prices {
			r.Insert(price)
		}
		b.StartTimer()

		for _, price := range prices {
			r.Remove(price)
		}
	}
}

// ============ 分片树 Benchmark ============

func BenchmarkShardedTree_Insert_100(b *testing.B) {
	prices := generatePrices(100)
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		s := NewShardedPriceTreeWrapper(true)
		for _, price := range prices {
			s.Insert(price)
		}
	}
}

func BenchmarkShardedTree_Insert_1000(b *testing.B) {
	prices := generatePrices(1000)
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		s := NewShardedPriceTreeWrapper(true)
		for _, price := range prices {
			s.Insert(price)
		}
	}
}

func BenchmarkShardedTree_GetBest(b *testing.B) {
	s := NewShardedPriceTreeWrapper(true)
	prices := generatePrices(100)
	for _, price := range prices {
		s.Insert(price)
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = s.GetBest()
	}
}

func BenchmarkShardedTree_Remove(b *testing.B) {
	prices := generatePrices(100)
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		b.StopTimer()
		s := NewShardedPriceTreeWrapper(true)
		for _, price := range prices {
			s.Insert(price)
		}
		b.StartTimer()

		for _, price := range prices {
			s.Remove(price)
		}
	}
}
