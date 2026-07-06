package app

import (
	"sync"
)

// Tx 表示一笔交易。
type Tx struct {
	ID        string // 交易哈希
	Type      string // 交易类型: "register"/"guarantee"/"repay"/"loan"
	Data      []byte // 序列化的交易数据
	Status    string // "pending" / "confirmed" / "failed"
	BlockNum  int64  // 确认区块号 (0=pending)
	CreatedAt int64  // 提交时间戳
}

// TxStatus 用于 API 返回
type TxStatus struct {
	ID       string `json:"id"`
	Type     string `json:"type"`
	Status   string `json:"status"`
	BlockNum int64  `json:"block_num"`
	Time     int64  `json:"time"`
	Data     string `json:"data"` // 交易数据 hex
}

// MemPool 是一个线程安全的交易内存池。
type MemPool struct {
	mu   sync.RWMutex
	txs  []*Tx
	size int // 当前交易数
}

// NewMemPool 创建新的交易池。
func NewMemPool() *MemPool {
	return &MemPool{txs: make([]*Tx, 0, 100)}
}

// Submit 提交一笔交易到池中。
func (mp *MemPool) Submit(tx *Tx) {
	mp.mu.Lock()
	defer mp.mu.Unlock()
	mp.txs = append(mp.txs, tx)
	mp.size++
}

// Drain 取出池中所有交易并清空（由共识引擎在打包区块时调用）。
func (mp *MemPool) Drain(max int) []*Tx {
	mp.mu.Lock()
	defer mp.mu.Unlock()
	if mp.size == 0 {
		return nil
	}
	count := mp.size
	if max > 0 && count > max {
		count = max
	}
	taken := mp.txs[:count]
	mp.txs = mp.txs[count:]
	mp.size -= count
	return taken
}

// Size 返回当前池中交易数。
func (mp *MemPool) Size() int {
	mp.mu.RLock()
	defer mp.mu.RUnlock()
	return mp.size
}
