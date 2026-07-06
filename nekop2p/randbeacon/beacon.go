// Package randbeacon 实现分布式随机信标，
// 采用提交-揭示协议。
//
// 多个参与者各自提交一个随机种子的承诺，
// 然后揭示他们的种子。最终随机输出是所有
// 已揭示种子的拼接哈希。
//
// 安全性：只要至少有一个参与者是诚实的，
// 最终输出就是不可预测的。
package randbeacon

import (
	"crypto/sha256"
	"fmt"
	"sort"
	"sync"
	"time"
)

// Round 表示随机信标协议的一轮。
type Round struct {
	Number       uint64
	CommitPhase  time.Time
	RevealPhase  time.Time
	CommitWindow time.Duration // e.g., 10s
	RevealWindow time.Duration // e.g., 10s
	Deposit      uint64        // required deposit per participant

	commitments map[string]*Commitment // 按参与者 ID 索引
	reveals     map[string]*Reveal     // 按参与者 ID 索引
	finalValue  [32]byte
	complete    bool
	mu          sync.RWMutex
}

// Commitment 在提交阶段提交。
type Commitment struct {
	Participant string
	CommitHash  [32]byte // SHA256(seed || nonce)
	SubmittedAt time.Time
}

// Reveal 在揭示阶段提交。
type Reveal struct {
	Participant string
	Seed        [32]byte
	Nonce       [32]byte
	SubmittedAt time.Time
}

// NewRound 创建新一轮随机信标。
func NewRound(number uint64, commitWindow, revealWindow time.Duration, deposit uint64) *Round {
	if commitWindow <= 0 {
		commitWindow = 10 * time.Second
	}
	if revealWindow <= 0 {
		revealWindow = 10 * time.Second
	}
	return &Round{
		Number:       number,
		CommitWindow: commitWindow,
		RevealWindow: revealWindow,
		Deposit:      deposit,
		commitments:  make(map[string]*Commitment),
		reveals:      make(map[string]*Reveal),
	}
}

// SubmitCommitment 为本轮提交一个承诺。
func (r *Round) SubmitCommitment(participant string, seed, nonce [32]byte) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.complete {
		return fmt.Errorf("round %d already complete", r.Number)
	}
	if _, exists := r.commitments[participant]; exists {
		return fmt.Errorf("participant %s already committed", participant)
	}

	commitHash := sha256.Sum256(append(seed[:], nonce[:]...))
	r.commitments[participant] = &Commitment{
		Participant: participant,
		CommitHash:  commitHash,
		SubmittedAt: time.Now(),
	}
	return nil
}

// SubmitReveal 揭示先前承诺的种子和随机数。
func (r *Round) SubmitReveal(participant string, seed, nonce [32]byte) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	cmt, exists := r.commitments[participant]
	if !exists {
		return fmt.Errorf("no commitment from %s", participant)
	}

	// 验证揭示与承诺匹配
	expected := sha256.Sum256(append(seed[:], nonce[:]...))
	if cmt.CommitHash != expected {
		return fmt.Errorf("reveal does not match commitment for %s", participant)
	}

	r.reveals[participant] = &Reveal{
		Participant: participant,
		Seed:        seed,
		Nonce:       nonce,
		SubmittedAt: time.Now(),
	}
	return nil
}

// Finalize 根据所有已揭示的种子计算最终随机值。
// 必须在揭示窗口关闭后调用。
func (r *Round) Finalize() ([32]byte, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.complete {
		return r.finalValue, nil
	}

	if len(r.reveals) == 0 {
		return [32]byte{}, fmt.Errorf("no reveals in round %d", r.Number)
	}

	// 拼接所有已揭示的种子（按参与者排序以保证确定性）
	var combined []byte
	for _, participant := range sortedKeys(r.reveals) {
		combined = append(combined, r.reveals[participant].Seed[:]...)
	}

	r.finalValue = sha256.Sum256(combined)
	r.complete = true
	return r.finalValue, nil
}

// IsComplete 返回本轮是否已完成最终化。
func (r *Round) IsComplete() bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.complete
}

// Value 返回最终随机值（仅在 Finalize 之后有效）。
func (r *Round) Value() [32]byte {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.finalValue
}

// ParticipantCount 返回已揭示的参与者数量。
func (r *Round) ParticipantCount() int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return len(r.reveals)
}

// DefaultParticipants 返回未能揭示的参与者列表
// （他们的押金将被没收）。
func (r *Round) DefaultParticipants() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()

	var defaults []string
	for participant := range r.commitments {
		if _, ok := r.reveals[participant]; !ok {
			defaults = append(defaults, participant)
		}
	}
	return defaults
}

// sortedKeys 返回排序后的参与者 ID（用于确定性聚合）。
func sortedKeys(m map[string]*Reveal) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
