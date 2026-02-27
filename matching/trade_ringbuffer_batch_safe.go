package matching

import (
	"lightning-exchange/domain"
	"sync/atomic"
	_ "unsafe" // for go:linkname
)

//go:linkname semacquireTradeSafe sync.runtime_Semacquire
func semacquireTradeSafe(s *uint32)

//go:linkname semreleaseTradeSafe sync.runtime_Semrelease
func semreleaseTradeSafe(s *uint32, handoff bool, skipframes int)

// TradeRingBufferBatchSafe 批量读取 + 纯 semaphore 语义的 Trade RingBuffer
type TradeRingBufferBatchSafe struct {
	buffer     []*domain.Trade
	mask       int64
	writeSeq   atomic.Int64
	readSeq    atomic.Int64
	emptySlots uint32
	fullSlots  uint32
}

// TradeConsumerBatchSafe Trade 消费者批量读取缓存
type TradeConsumerBatchSafe struct {
	rb         *TradeRingBufferBatchSafe
	localCache [128]*domain.Trade
	cacheStart int
	cacheEnd   int
}

// NewTradeRingBufferBatchSafe 创建 Trade RingBuffer
func NewTradeRingBufferBatchSafe(size int) *TradeRingBufferBatchSafe {
	if size&(size-1) != 0 {
		panic("RingBuffer size must be power of 2")
	}

	rb := &TradeRingBufferBatchSafe{
		buffer:     make([]*domain.Trade, size),
		mask:       int64(size - 1),
		emptySlots: 0,
		fullSlots:  0,
	}

	for i := 0; i < size; i++ {
		semreleaseTradeSafe(&rb.emptySlots, false, 0)
	}

	return rb
}

// NewTradeConsumerBatchSafe 创建 Trade 消费者
func (rb *TradeRingBufferBatchSafe) NewTradeConsumerBatchSafe() *TradeConsumerBatchSafe {
	return &TradeConsumerBatchSafe{
		rb:         rb,
		cacheStart: 0,
		cacheEnd:   0,
	}
}

// Publish 发布 Trade
func (rb *TradeRingBufferBatchSafe) Publish(trade *domain.Trade) {
	semacquireTradeSafe(&rb.emptySlots)

	seq := rb.writeSeq.Add(1) - 1
	index := seq & rb.mask
	rb.buffer[index] = trade

	semreleaseTradeSafe(&rb.fullSlots, false, 0)
}

// TryConsume 非阻塞消费（用于测试中的 trade consumer）
func (cb *TradeConsumerBatchSafe) TryConsume() (*domain.Trade, bool) {
	// 如果本地缓存还有数据，直接返回
	if cb.cacheStart < cb.cacheEnd {
		trade := cb.localCache[cb.cacheStart]
		cb.cacheStart++
		return trade, true
	}

	// 本地缓存耗尽，尝试批量读取（非阻塞）
	if !cb.tryFillCache() {
		return nil, false
	}

	trade := cb.localCache[cb.cacheStart]
	cb.cacheStart++
	return trade, true
}

// tryFillCache 非阻塞批量填充
func (cb *TradeConsumerBatchSafe) tryFillCache() bool {
	rb := cb.rb

	// 检查是否有可用数据
	currentWrite := rb.writeSeq.Load()
	currentRead := rb.readSeq.Load()
	available := int(currentWrite - currentRead)

	if available == 0 {
		return false
	}

	maxBatch := 128
	if available > maxBatch {
		available = maxBatch
	}

	// 批量获取（纯 semaphore，每次都调用 semacquire）
	acquired := 0
	for i := 0; i < available; i++ {
		// 使用 CAS 检查是否有数据（非阻塞检查）
		slots := atomic.LoadUint32(&rb.fullSlots)
		if slots == 0 {
			break
		}

		// 尝试获取（这里仍需要 CAS 来实现非阻塞）
		// 注意：TryConsume 本身就是非关键路径，允许使用 CAS
		if !atomic.CompareAndSwapUint32(&rb.fullSlots, slots, slots-1) {
			continue
		}

		// 读取数据
		seq := rb.readSeq.Add(1) - 1
		index := seq & rb.mask
		cb.localCache[acquired] = rb.buffer[index]

		// 释放空位
		semreleaseTradeSafe(&rb.emptySlots, false, 0)

		acquired++
	}

	if acquired == 0 {
		return false
	}

	cb.cacheStart = 0
	cb.cacheEnd = acquired

	return true
}
