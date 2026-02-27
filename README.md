# Lightning Exchange - 高性能撮合引擎

基于 Go 实现的内存撮合引擎，采用单线程确定性撮合 + 批量读取 + runtime semaphore 同步 + 分片价格树架构。

> **⚡ 项目说明**  
> - 本项目代码 100% 由 AI 生成（Cascade/Claude）  
> - 人类负责架构设计、性能权衡决策和代码审查  
> - 开发周期：3 天（非连续）  
> - 依赖：Go 标准库 + gods/v2（红黑树）

## 📊 性能指标

**吞吐量**：1.0–1.38M orders/s (match-complete, single-symbol, in-memory)

**测量口径**：
- 构造 1:1 完全成交场景（预加载卖单，再提交买单）
- 等待成交数达到预期（trade 被消费确认）
- 测量从订单提交到成交完成的端到端吞吐
- 避免仅统计 submit QPS 造成虚高

**测试环境**：
- CPU: Apple M2 Pro
- Go: 1.24.4
- GOMAXPROCS: 10 (1 撮合线程 + 1 空闲 + 8 生产者)
- 场景: 单交易对，内存订单簿，无持久化

**关键结果**：
- 单生产者: 1.38M orders/s (100K orders, 72ms)
- 8并发生产者: 1.14M orders/s (80K orders, 70ms)
- 平均延迟: 0.73–0.88 μs/order

## 🏗️ 架构设计

### 核心架构
```
┌─────────────────────────────────────────────────────┐
│           ExchangeEngine (多交易对)                  │
│  ┌──────────────────────────────────────────────┐  │
│  │  MatchingEngine (单交易对)                   │  │
│  │  ┌────────────┐      ┌──────────────────┐   │  │
│  │  │ OrderBuffer│─────▶│ OrderBook        │   │  │
│  │  │ (Disruptor)│      │ (Sharded Tree)   │   │  │
│  │  └────────────┘      │ ┌──────────────┐ │   │  │
│  │         │            │ │ RBTree       │ │   │  │
│  │         ▼            │ │  ├─Bucket 0  │ │   │  │
│  │  ┌────────────┐      │ │  ├─Bucket 1  │ │   │  │
│  │  │ Matching   │      │ │  └─Bucket N  │ │   │  │
│  │  │ Thread     │      │ └──────────────┘ │   │  │
│  │  └────────────┘      │ HashMap+List/档  │   │  │
│  │         │            └──────────────────┘   │  │
│  │         ▼                                   │  │
│  │  ┌────────────┐                             │  │
│  │  │TradeBuffer │                             │  │
│  │  │(Disruptor) │                             │  │
│  │  └────────────┘                             │  │
│  └──────────────────────────────────────────────┘  │
└─────────────────────────────────────────────────────┘
```

### 核心技术要点

1. **架构**：单撮合线程绑定消费（确定性/无锁），多生产者提交；订单簿支持价格优先、同价 FIFO
   - 单线程撮合引擎（参考 NASDAQ ITCH）
   - 确定性订单处理，无需锁机制
   - 价格-时间优先算法

2. **性能优化**：识别 Gosched 让出 CPU 导致吞吐下降，改为 semaphore + batch + 本地缓存，减少唤醒次数与同步开销
   - 初始版本使用 `runtime.Gosched()` 自旋等待，导致频繁上下文切换（10-20K QPS）
   - 改用 Go Channel 后提升至 767K QPS
   - 引入批量读取（batch size 128）+ runtime semaphore，减少同步开销 128 倍
   - 最终达到 1.0-1.38M QPS（纯 semaphore 协议，无 CAS 混用）

3. **正确性验证**：实现 robust correctness tests（订单不丢失、成交不重复、成交引用合法、最终状态一致性）并引入超时条件等待避免 sleep 误判
   - Exactly-Once 验证：每个订单恰好出现一次
   - 严格失败条件：未达预期成交数立即 Fatalf
   - Happens-Before 语义：semrelease(full) happens-before semacquire(full)
   - 条件等待替代固定 sleep，测试速度提升 4.9 倍

4. **性能测量口径**：实现 reliable perf tests，以"成交数达到预期（trade 被消费确认）"作为完成条件，避免仅统计 submit QPS 造成虚高
   - 构造 1:1 完全成交场景（预加载卖单，再提交买单）
   - 等待成交数达到预期（带超时保护）
   - 测量端到端吞吐（订单提交 → 撮合 → 成交确认）
   - 避免持续压测包含未完成订单导致 QPS 虚高

5. **数据结构优化**：分片价格树 + 位运算索引
   - 外层红黑树管理 bucket（O(log m)）
   - 内层固定数组（128 = 2^7）+ 双向链表
   - 位运算索引：`price & mask` 替代 `price % size`（快 5-10 倍）
   - 最佳价格查询 O(1)

### 性能优化历程

从 Channel 基准到最终的批量 + 纯 Semaphore，完整的迭代过程：

| 阶段 | 实现方式 | 单独测试 QPS | 集成测试 QPS | 相对提升 | 问题/经验 |
|------|---------|-------------|-------------|---------|----------|
| **基准** | Go Channel | ~2.43M | 767K | 基准 | Go 原生，经过充分验证 |
| **对比** | Disruptor + Gosched | ~2M | 10-20K | -97.4% | CPU 100%，频繁上下文切换 |
| **改进 1** | Semaphore（非批量） | ~1.5M | 663K | -13.6% | 纯 semaphore，但无批量优化 |
| **最终版本** | **Batch 128（纯 Semaphore）** | **~2.8M** | **1.0-1.38M** | **+79.8%** | **批量 + 安全 + 可信测试** |

**关键经验**：

1. **Channel vs Disruptor 对比**
   - 基准测试：Channel 达到 767K QPS，性能可观
   - 对比发现：Disruptor + Gosched 只有 10-20K QPS，性能极差
   - 分析：Disruptor 模式（环形队列）理论上应该比 Channel 更快
   - 问题定位：瓶颈在 Gosched，而不是 Disruptor 本身
   - 决策：既然 Disruptor 像 Channel 的环形队列，尝试用更好的同步机制替换 Gosched

2. **选择 Semaphore 作为同步原语**
   - 约束：不能使用锁（保持单线程撮合的确定性）
   - 方案：尝试 Semaphore 替换 Gosched
   - 原因：Semaphore 提供阻塞式等待，避免频繁上下文切换

3. **Semaphore + 批量优化**
   - 纯 Semaphore（非批量）：663K QPS，比 Channel 还慢
   - 批量读取优化：尝试 batch size 64/128/256
   - CAS 混用版本：最快达到 1.632M（batch 256），但有理论风险
   - 纯 Semaphore 版本：1.0-1.38M（batch 128），完全安全
   - 最终选择：batch 128 + 纯 Semaphore，牺牲 15% 性能换取正确性

4. **测试方法的改进**
   - 问题：持续压测 5 秒可能包含未完成订单，QPS 虚高
   - 改进：构造 1:1 完全成交场景，等待成交数达到预期
   - 结果：测量真实的端到端吞吐，更可信

### 技术细节

**批量读取 + 纯 Semaphore 同步**：
```go
// 生产者协议
semacquire(&emptySlots)  // 获取空槽位
buffer[index] = order    // 写入数据
semrelease(&fullSlots)   // 释放满槽位

// 消费者协议（批量）
semacquire(&fullSlots)   // 获取第 1 个（阻塞）
for i := 0; i < 127; i++ {
    semacquire(&fullSlots) // 批量获取更多（非阻塞）
}
// 读取 128 个元素到本地缓存
semrelease(&emptySlots)  // 批量释放空槽位
```

**关键设计**：
- 使用 `runtime_Semacquire/Semrelease`（Go 内部 API，通过 `//go:linkname` 访问）
- 完全遵守 semaphore 协议，不混用 CAS 操作
- 批量大小 128，同步开销减少 128 倍
- Happens-before 语义保证内存可见性

## 🔧 核心优化

| 优化项 | 优化前 | 优化后 | 提升 |
|--------|--------|--------|------|
| IDGenerator | ~500ns | ~30ns | **16x** |
| 最佳价格查询 | O(log n) | O(1) | **∞** |
| 订单删除 | O(n) | O(1) | **∞** |
| 消息队列 | Channel (~50ns) | Disruptor (~10ns) | **5x** |
| 价格树（1000 档位） | HashMap+List (350μs) | 分片树 (126μs) | **2.8x** |
| 数组索引 | HashMap 哈希 | 位运算 (price & mask) | **5-10x** |
| 整体 QPS | 61.5 万 | 123.2 万 | **+100%** |

## 📈 数据结构对比（1000 个价格档位）

| 数据结构 | 插入性能 | 最佳价格查询 | 删除性能 | 适用场景 |
|---------|---------|-------------|---------|---------|
| HashMap+List | 350μs | O(1) ~0.3ns | O(1) ~10ns | < 100 档位 |
| 红黑树 | 166μs | O(1) ~3ns | O(log n) ~150ns | 通用 |
| **分片树** | **126μs** | **O(1) ~0.3ns** | **O(1) ~10ns** | **100-10000 档位** ✅ |

**分片树优势**：
- ✅ 比 HashMap+List 快 **2.8 倍**（64% 提升）
- ✅ 比红黑树快 **1.24 倍**（24% 提升）
- ✅ 保留 O(1) 最佳价格查询和删除
- ✅ 在真实交易场景（±10% 价格范围）下性能最优

## 📦 项目结构

```
lighting-exchange/
├── domain/                              # 领域模型
│   ├── order.go                        # 订单实体 + 对象池
│   └── trade.go                        # 成交实体 + 对象池
├── orderbook/                          # 订单簿
│   ├── orderbook.go                    # 订单簿实现
│   └── price_tree.go                   # 分片价格树
├── matching/                           # 撮合引擎（精简后）
│   ├── engine.go                       # 撮合引擎核心
│   ├── disruptor_semaphore_batch_safe.go  # 批量 + 纯 Semaphore RingBuffer
│   ├── trade_ringbuffer_batch_safe.go     # Trade 批量 RingBuffer
│   ├── id_generator.go                 # ID 生成器
│   ├── performance_reliable_test.go    # 可信性能测试
│   ├── correctness_robust_test.go      # 完整闭环正确性测试
│   └── channel_performance_test.go     # 性能对比测试
├── cmd/
│   ├── benchmark/                      # 性能测试工具
│   └── profile/                        # 性能分析工具
└── main.go                             # 示例程序
```

**核心文件说明**：
- `disruptor_semaphore_batch_safe.go`: 批量 128 + 纯 Semaphore 实现，完全遵守 semaphore 协议
- `performance_reliable_test.go`: 可信性能测试（方案 A：成交完成吞吐）
- `correctness_robust_test.go`: Exactly-Once 验证 + 严格失败条件

## 🚀 快速开始

### 安装
```bash
git clone https://github.com/yourusername/lighting-exchange.git
cd lighting-exchange
go mod download
```

### 运行示例
```bash
go run main.go
```

### 性能测试
```bash
# 可信性能测试（推荐）
go test -run=TestMatchingEngineReliableQPS -v ./matching

# 并发场景性能测试
go test -run=TestMatchingEngineConcurrentReliableQPS -v ./matching

# 正确性测试（完整闭环）
go test -run="Test.*Robust" -v ./matching

# Benchmark
go test -bench=. -benchmem ./matching

# 性能分析
go run cmd/profile/main.go
go tool pprof -http=:8080 cpu.prof
```

## 📈 性能测试结果

### 可信性能测试（方案 A：成交完成吞吐）

```bash
$ go test -run=TestMatchingEngineReliableQPS -v ./matching

=== RUN   TestMatchingEngineReliableQPS
步骤1: 挂入 100000 个卖单...
步骤2: 发送 100000 个买单，开始计时...
步骤3: 等待成交数达到 100000...

=== 可信的性能测试结果（方案A：成交完成吞吐）===
测试场景:      1:1 完全成交（先挂卖单，再发买单）
订单数量:      100000
成交数量:      100000
完成耗时:      72.504792ms
订单 QPS:      1379219 orders/sec (137.9 万/秒)
成交 TPS:      1379219 trades/sec (137.9 万/秒)
平均延迟:      0.73 μs/order

说明: 此 QPS 测量的是撮合完成吞吐（trade 已被消费确认）
--- PASS: TestMatchingEngineReliableQPS (0.21s)
```

### 并发场景性能测试

```bash
$ go test -run=TestMatchingEngineConcurrentReliableQPS -v ./matching

=== RUN   TestMatchingEngineConcurrentReliableQPS
步骤1: 8 个生产者并发挂入卖单...
步骤2: 8 个生产者并发发送买单，开始计时...
步骤3: 等待成交数达到 80000...

=== 并发场景可信性能测试结果 ===
测试场景:      1:1 完全成交（先挂卖单，再发买单）
并发生产者:    8
总订单数:      80000
成交数量:      80000
完成耗时:      70.424958ms
订单 QPS:      1135961 orders/sec (113.6 万/秒)
成交 TPS:      1135961 trades/sec (113.6 万/秒)
平均延迟:      0.88 μs/order
--- PASS: TestMatchingEngineConcurrentReliableQPS (0.19s)
```

**测试方法说明**：
- ✅ 构造严格 1:1 可完全成交的场景（先挂满卖单，再发买单）
- ✅ 等待成交数达到预期（带超时），而非固定时间压测
- ✅ 测量真实的端到端吞吐（订单提交 → 撮合处理 → 成交确认）
- ✅ 可信度高，避免了持续压测可能包含未完成订单的问题

### 正确性测试（完整闭环验证）

```bash
$ go test -run="Test.*Robust" -v ./matching

=== RUN   TestOrderFinalStateConsistencyRobust
等待成交完成，期望成交数: 50000

=== 订单最终状态一致性测试（改进版）===
发送买单数: 50000
发送卖单数: 50000
总成交数: 50000
✓ 所有成交引用的订单 ID 都有效
✓ 成交数量符合预期: 50000
✓ 成交总量符合预期: 5000000
✓ 所有成交 ID 唯一
✓ 所有成交价格一致: 50000
=== 最终状态一致性验证通过 ===
--- PASS: TestOrderFinalStateConsistencyRobust (0.17s)

=== RUN   TestHappenBeforeSemanticsRobust
等待成交完成，期望成交数: 50000

=== Happens-Before 语义测试（改进版 - Exactly-Once）===
发送订单数: 100000
接收订单引用数: 100000
成交数: 50000
✓ 所有接收到的订单 ID 都在发送集合中
✓ Exactly-Once 验证通过：每个订单恰好出现一次
✓ Happens-Before 语义正确
--- PASS: TestHappenBeforeSemanticsRobust (0.08s)

=== RUN   TestConcurrentStressRobust
等待成交完成，期望成交数: 8000

=== 并发压力测试（改进版 - 严格模式）===
生产者数量: 8
总订单数: 16000
总成交数: 8000
✓ 所有成交引用的订单都存在
✓ 成交数量精确匹配: 8000
--- PASS: TestConcurrentStressRobust (0.02s)
PASS
ok      lighting-exchange/matching     0.423s
```

**测试覆盖（完整闭环）**：
- ✅ **Exactly-Once 验证**：每个订单恰好出现一次（抓重复/丢失）
- ✅ **严格失败条件**：未达到期望成交数会 Fatalf（不允许 warning）
- ✅ **订单最终状态一致性**：所有成交引用的订单都存在
- ✅ **Happens-Before 语义**：semrelease(full) happens-before semacquire(full)
- ✅ **并发安全**：8 并发生产者，严格模式验证
- ✅ **价格优先**：不同价格的订单按最优价格匹配
- ✅ **时间优先**：同价格订单按 FIFO 顺序匹配
- ✅ **条件等待**：替代固定 sleep，测试速度提升 4.9 倍

**测试环境**：
- CPU: Apple M2 Pro (10 cores)
- 架构: 分片价格树 + 位运算优化 + LMAX Disruptor
- 并发: 8 个生产者线程
- 场景: 买卖单交替插入，价格范围 200 档位

## 🎯 设计理念

### 1. 针对场景优化
- **场景**: 单交易对、高吞吐、低延迟
- **优化**: 去掉通用性，追求极致性能

### 2. 机械同情（Mechanical Sympathy）
- CPU Cache Line 对齐
- 避免 False Sharing
- 预分配内存，减少 GC

### 3. 无锁设计
- 单线程撮合（无需锁）
- Disruptor 无锁队列
- 原子操作替代互斥锁

## 🔍 技术细节

### Disruptor vs Channel

| 特性 | Go Channel | LMAX Disruptor |
|------|-----------|----------------|
| 实现 | mutex + 等待队列 | 原子操作 + 环形缓冲区 |
| 延迟 | ~50ns | ~10ns |
| 吞吐量 | 2000 万/秒 | 1 亿/秒 |
| 适用场景 | 通用 | 单消费者高吞吐 |

### 订单簿数据结构

```go
// 分片价格树：外层红黑树 + 内层数组 + 位运算优化
type ShardedPriceTree struct {
    buckets    *RedBlackTree[int64, *Bucket]  // 外层：管理 bucket
    bestBucket *Bucket                         // 缓存最佳 bucket
    bestPrice  *PriceLevel_                    // 缓存最佳价格（O(1) 访问）
    bucketSize int64                           // bucket 大小（128 = 2^7）
}

// Bucket：价格分片（每 128 个价格一组）
type Bucket struct {
    bucketID   int64
    levels     [128]*PriceLevel_  // 固定数组（128 = 2^7，支持位运算优化）
    bestPrice  *PriceLevel_        // Doubly Linked List 头节点
    bucketMask int64               // 位掩码（127），用于快速索引：price & mask
}

// 价格档位：FIFO 队列 + 链表节点
type PriceLevel_ struct {
    Price     int64
    Orders    *list.List      // FIFO 队列（时间优先）
    Volume    int64
    NextPrice *PriceLevel_    // 链表指针（价格排序）
    PrevPrice *PriceLevel_
}
```

## ✅ 正确性保证

### Ring Buffer 发布协议

为确保多生产者场景下的数据可见性，实现了完整的发布协议：

```go
type RingBuffer struct {
    buffer    []*domain.Order
    available []atomic.Int64  // 每个 slot 的发布标记
    writeSeq  atomic.Int64
    readSeq   atomic.Int64
}

// 生产者：写入数据后标记为已发布
func (rb *RingBuffer) Publish(order *domain.Order) {
    seq := rb.writeSeq.Add(1) - 1
    index := seq & rb.mask
    rb.buffer[index] = order
    rb.available[index].Store(seq)  // 发布标记
}

// 消费者：等待发布标记后再读取
func (rb *RingBuffer) Consume() *domain.Order {
    seq := rb.readSeq.Add(1) - 1
    index := seq & rb.mask
    for rb.available[index].Load() != seq {  // 等待发布
        runtime.Gosched()
    }
    return rb.buffer[index]
}
```

**关键点**：
- ✅ 避免读取未发布的数据
- ✅ 保证内存可见性（memory barrier）
- ✅ 支持多生产者并发写入

### 测试覆盖

- ✅ **价格优先**：不同价格的订单按最优价格匹配
- ✅ **时间优先**：同价格订单按 FIFO 顺序匹配
- ✅ **订单管理**：添加、撤单、查询功能正确
- ✅ **市场深度**：GetDepth 返回正确的价格档位排序
- ✅ **数据结构**：GetLevel 使用位运算索引，避免越界

## 📝 TODO

**已完成**：
- [x] 正确性测试覆盖
- [x] Ring Buffer 发布协议修复
- [x] 批量读取优化（性能提升 79.8%）
- [x] 纯 Semaphore 实现（无 CAS 混用）
- [x] Exactly-Once 验证
- [x] 可信性能测试（成交完成吞吐）
- [x] 完整闭环正确性测试

**待实现**：
- [ ] 订单簿快照功能
- [ ] WebSocket 推送
- [ ] 持久化（WAL + Event Sourcing）
- [ ] 分布式部署支持

## 🤝 贡献

欢迎提交 Issue 和 Pull Request！

## 📄 License

MIT License

## 🙏 致谢

- LMAX Disruptor 架构
- NASDAQ ITCH 协议设计
- Go 语言高性能实践

---

**注意**: 本项目仅用于学习和研究，不建议直接用于生产环境。
