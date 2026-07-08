// Package consensus 定义共识引擎接口。
//
// nekop2p 设计为可插拔共识：Phase 1 使用简单的轮询出块，
// 生产环境接入 Tendermint (CometBFT) BFT 共识。
package consensus

import (
	"crypto/sha256"
	"fmt"
	"sync"
	"time"
)

// Engine 是共识引擎的接口。
// 实现负责区块提议、验证和提交。
type Engine interface {
	// Start 启动共识引擎。
	Start() error

	// Stop 优雅停止共识引擎。
	Stop() error

	// IsValidator 返回当前节点是否为验证者。
	IsValidator() bool

	// Height 返回当前共识高度。
	Height() int64

	// ProposeBlock 提议一个新区块。
	// 返回：区块数据、区块高度、错误
	ProposeBlock(txs [][]byte) (blockData []byte, height int64, err error)

	// CommitBlock 提交一个已达成共识的区块。
	CommitBlock(blockData []byte, height int64) error

	// SubscribeBlocks 订阅新区块事件。
	SubscribeBlocks() <-chan BlockEvent
}

// BlockEvent 表示一个新区块事件。
type BlockEvent struct {
	Height    int64
	BlockHash [32]byte
	Txs       [][]byte
	Timestamp time.Time
}

// SimpleEngine 是一个简单的单节点共识引擎（Phase 1 用）。
// 它按固定间隔出块，不需要多节点共识。
type SimpleEngine struct {
	mu          sync.Mutex // 保护 height/stopped/started
	height      int64
	interval    time.Duration
	blockCh     chan BlockEvent
	stopCh      chan struct{}
	stopped     bool
	started     bool
	isValidator bool
}

// NewSimpleEngine 创建一个简单共识引擎。
func NewSimpleEngine(interval time.Duration, isValidator bool) *SimpleEngine {
	return &SimpleEngine{
		interval:    interval,
		isValidator: isValidator,
		blockCh:     make(chan BlockEvent, 10),
		stopCh:      make(chan struct{}),
	}
}

func (e *SimpleEngine) Start() error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.started {
		return fmt.Errorf("consensus engine already started")
	}
	if !e.isValidator {
		return nil
	}
	e.started = true
	go e.loop()
	return nil
}

func (e *SimpleEngine) Stop() error {
	e.mu.Lock()
	if e.stopped {
		e.mu.Unlock()
		return nil
	}
	e.stopped = true
	e.mu.Unlock()
	close(e.stopCh)
	return nil
}

func (e *SimpleEngine) IsValidator() bool {
	return e.isValidator
}

func (e *SimpleEngine) Height() int64 {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.height
}

func (e *SimpleEngine) ProposeBlock(txs [][]byte) ([]byte, int64, error) {
	e.mu.Lock()
	e.height++
	h := e.height
	e.mu.Unlock()
	blockData := FlattenTxs(txs)
	if blockData == nil {
		blockData = []byte(fmt.Sprintf("block-%d", h))
	}
	return blockData, h, nil
}

func (e *SimpleEngine) CommitBlock(blockData []byte, height int64) error {
	select {
	case e.blockCh <- BlockEvent{
		Height:    height,
		Timestamp: time.Now(),
	}:
	default:
		// 消费者过慢：丢弃事件而非阻塞 loop
	}
	return nil
}

func (e *SimpleEngine) SubscribeBlocks() <-chan BlockEvent {
	return e.blockCh
}

func (e *SimpleEngine) loop() {
	ticker := time.NewTicker(e.interval)
	defer ticker.Stop()

	for {
		select {
		case <-e.stopCh:
			return
		case <-ticker.C:
			blockData, height, _ := e.ProposeBlock(nil)
			e.CommitBlock(blockData, height)
		}
	}
}

// FlattenTxs 将多笔交易拼接为单段区块数据。
func FlattenTxs(txs [][]byte) []byte {
	var result []byte
	for _, tx := range txs {
		result = append(result, tx...)
	}
	return result
}

// ComputeBlockHash 计算区块的 SHA-256 哈希。
func ComputeBlockHash(txs [][]byte, height int64) [32]byte {
	h := sha256.New()
	for _, tx := range txs {
		h.Write(tx)
	}
	h.Write([]byte(fmt.Sprintf("%d", height)))
	var result [32]byte
	copy(result[:], h.Sum(nil))
	return result
}
