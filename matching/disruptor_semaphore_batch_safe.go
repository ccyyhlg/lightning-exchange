package matching

import (
	"lightning-exchange/domain"
	"sync/atomic"
	_ "unsafe" // for go:linkname
)

//go:linkname semacquireSafe sync.runtime_Semacquire
func semacquireSafe(s *uint32)

//go:linkname semreleaseSafe sync.runtime_Semrelease
func semreleaseSafe(s *uint32, handoff bool, skipframes int)

// RingBufferSemaphoreBatchSafe 批量读取 + 纯 semaphore 语义
// 核心原则：
// 1. 批量优化：消费者持有本地缓存，减少同步开销
// 2. 纯 semaphore：每个元素都通过 semacquire/semrelease，不用 CAS
// 3. 严格的 happens-before 语义
//
// 批量获取策略（纯 semaphore）：
// 1. 先 semacquire(full) 获取第 1 个（阻塞，保证不空）
// 2. 再循环调用 semacquire(full) 获取更多（最多 127 个）
// 3. 所有操作都通过 semaphore，不使用 CAS
type RingBufferSemaphoreBatchSafe struct {
	buffer     []*domain.Order
	mask       int64
	writeSeq   atomic.Int64
	readSeq    atomic.Int64
	emptySlots uint32
	fullSlots  uint32
}

// ConsumerBatchSafe 消费者批量读取缓存
type ConsumerBatchSafe struct {
	rb         *RingBufferSemaphoreBatchSafe
	localCache [128]*domain.Order // 本地缓存，128 个元素
	cacheStart int                // 当前读取位置
	cacheEnd   int                // 缓存中有效元素的结束位置
}

// NewRingBufferSemaphoreBatchSafe 创建批量 + 安全的 RingBuffer
func NewRingBufferSemaphoreBatchSafe(size int) *RingBufferSemaphoreBatchSafe {
	if size&(size-1) != 0 {
		panic("RingBuffer size must be power of 2")
	}

	rb := &RingBufferSemaphoreBatchSafe{
		buffer:     make([]*domain.Order, size),
		mask:       int64(size - 1),
		emptySlots: 0,
		fullSlots:  0,
	}

	// 初始化 emptySlots
	for i := 0; i < size; i++ {
		semreleaseSafe(&rb.emptySlots, false, 0)
	}

	return rb
}

// NewConsumerBatchSafe 创建消费者批量读取器
func (rb *RingBufferSemaphoreBatchSafe) NewConsumerBatchSafe() *ConsumerBatchSafe {
	return &ConsumerBatchSafe{
		rb:         rb,
		cacheStart: 0,
		cacheEnd:   0,
	}
}

// Publish 发布单个元素（生产者使用）
func (rb *RingBufferSemaphoreBatchSafe) Publish(order *domain.Order) {
	semacquireSafe(&rb.emptySlots)

	seq := rb.writeSeq.Add(1) - 1
	index := seq & rb.mask
	rb.buffer[index] = order

	semreleaseSafe(&rb.fullSlots, false, 0)
}

// Consume 批量读取优化的阻塞消费
func (cb *ConsumerBatchSafe) Consume() *domain.Order {
	// 如果本地缓存还有数据，直接返回
	if cb.cacheStart < cb.cacheEnd {
		order := cb.localCache[cb.cacheStart]
		cb.cacheStart++
		return order
	}

	// 本地缓存耗尽，批量填充
	cb.fillCacheSafe()

	// 从新填充的缓存中读取
	order := cb.localCache[cb.cacheStart]
	cb.cacheStart++
	return order
}

// fillCacheSafe 批量填充（纯 semaphore 语义）
// 关键改进：不使用 CAS，每个元素都通过 semacquire
func (cb *ConsumerBatchSafe) fillCacheSafe() {
	rb := cb.rb

	// 步骤1: 先获取第 1 个，确保不空（阻塞等待）
	semacquireSafe(&rb.fullSlots)

	// 读取第 1 个元素
	seq := rb.readSeq.Add(1) - 1
	index := seq & rb.mask
	cb.localCache[0] = rb.buffer[index]

	// 释放对应的空位
	semreleaseSafe(&rb.emptySlots, false, 0)

	acquired := 1

	// 步骤2: 尝试获取更多（最多 127 个）
	// 关键：通过估算可用数量来决定尝试次数，避免阻塞
	maxBatch := 128
	currentWrite := rb.writeSeq.Load()
	currentRead := rb.readSeq.Load()
	available := int(currentWrite - currentRead)

	// 限制批量大小
	if available > maxBatch-1 {
		available = maxBatch - 1
	}

	// 批量获取（每次都调用 semacquire，但我们知道有数据所以不会阻塞）
	for i := 0; i < available; i++ {
		// 纯 semaphore 操作：获取 token
		semacquireSafe(&rb.fullSlots)

		// 读取数据
		seq := rb.readSeq.Add(1) - 1
		index := seq & rb.mask
		cb.localCache[acquired] = rb.buffer[index]

		// 释放空位
		semreleaseSafe(&rb.emptySlots, false, 0)

		acquired++
	}

	// 更新缓存指针
	cb.cacheStart = 0
	cb.cacheEnd = acquired
}
