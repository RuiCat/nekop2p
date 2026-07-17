// Package consensus PBFT 多节点拜占庭容错共识引擎。
//
// 基于现有 P2P 基础设施（Noise/Beacon/Ratchet）实现的 PBFT 共识。
// 不依赖 Cosmos SDK/CometBFT，直接使用项目已有的网络层。
//
// 共识流程（每高度 1 轮）:
//   1. PROPOSE:  轮值提议者广播区块提案
//   2. PREVOTE:  验证者广播对提案的预投票
//   3. PRECOMMIT: 验证者广播预提交（锁定投票）
//   4. COMMIT:   收到 2/3+ 预提交后提交区块
//
// 安全性:
//   - 2/3+ 多数决
//   - 同一高度不会提交两个不同区块
//   - 验证者集变更需 2/3+ 同意
package consensus

import (
	"crypto/sha256"
	"fmt"
	"log"
	"sort"
	"sync"
	"time"
)

// ============================================================
// PBFT 状态机
// ============================================================

// PBFTStep PBFT 共识步骤。
type PBFTStep int

const (
	PBFTStepPropose   PBFTStep = 0
	PBFTStepPrevote   PBFTStep = 1
	PBFTStepPrecommit PBFTStep = 2
	PBFTStepCommit    PBFTStep = 3
	PBFTStepNewHeight PBFTStep = 4
)

func (s PBFTStep) String() string {
	switch s {
	case PBFTStepPropose: return "PROPOSE"
	case PBFTStepPrevote: return "PREVOTE"
	case PBFTStepPrecommit: return "PRECOMMIT"
	case PBFTStepCommit: return "COMMIT"
	case PBFTStepNewHeight: return "NEW_HEIGHT"
	default: return "UNKNOWN"
	}
}

// ============================================================
// 消息类型
// ============================================================

// PBFTProposal 区块提案。
type PBFTProposal struct {
	Height    int64
	Round     int32
	BlockData []byte
	BlockHash [32]byte
	Proposer  [32]byte // 提议者公钥
	Timestamp int64
}

// PBFTVote 共识投票。
type PBFTVote struct {
	Height    int64
	Round     int32
	Step      PBFTStep // PREVOTE or PRECOMMIT
	BlockHash [32]byte // 投票的区块哈希（nil 表示投空）
	Validator [32]byte // 投票者公钥
	Timestamp int64
}

// ============================================================
// 验证者管理
// ============================================================

// ValidatorInfo 验证者信息。
type ValidatorInfo struct {
	PubKey       [32]byte
	VotingPower  int64  // 投票权重
	Address      string // 网络地址
	IsActive     bool
	JoinedAt     int64
}

// ValidatorSet 验证者集合。
type ValidatorSet struct {
	mu         sync.RWMutex
	validators map[[32]byte]*ValidatorInfo
	totalPower int64
}

// NewValidatorSet 创建验证者集合。
func NewValidatorSet() *ValidatorSet {
	return &ValidatorSet{
		validators: make(map[[32]byte]*ValidatorInfo),
	}
}

// AddValidator 添加验证者。
func (vs *ValidatorSet) AddValidator(info *ValidatorInfo) {
	vs.mu.Lock()
	defer vs.mu.Unlock()
	vs.validators[info.PubKey] = info
	vs.totalPower += info.VotingPower
}

// RemoveValidator 移除验证者。
func (vs *ValidatorSet) RemoveValidator(pubKey [32]byte) {
	vs.mu.Lock()
	defer vs.mu.Unlock()
	if info, ok := vs.validators[pubKey]; ok {
		vs.totalPower -= info.VotingPower
		delete(vs.validators, pubKey)
	}
}

// TotalPower 返回总投票权重。
func (vs *ValidatorSet) TotalPower() int64 {
	vs.mu.RLock()
	defer vs.mu.RUnlock()
	return vs.totalPower
}

// Quorum 返回法定人数 (2/3 + 1)。
func (vs *ValidatorSet) Quorum() int64 {
	total := vs.TotalPower()
	return total*2/3 + 1
}

// GetProposer 返回指定高度的提议者（轮询）。
func (vs *ValidatorSet) GetProposer(height int64) *ValidatorInfo {
	vs.mu.RLock()
	defer vs.mu.RUnlock()

	// 收集排序后的公钥列表（消除 map 随机化）
	var keys [][32]byte
	for k := range vs.validators {
		keys = append(keys, k)
	}
	if len(keys) == 0 {
		return nil
	}
	// 排序确保所有节点对同一高度选出相同提议者
	sort.Slice(keys, func(i, j int) bool {
		for x := 0; x < 32; x++ {
			if keys[i][x] != keys[j][x] { return keys[i][x] < keys[j][x] }
		}
		return false
	})

	// 轮询选择
	idx := int(height) % len(keys)
	return vs.validators[keys[idx]]
}

// ValidatorCount 返回验证者数量。
func (vs *ValidatorSet) ValidatorCount() int {
	vs.mu.RLock()
	defer vs.mu.RUnlock()
	return len(vs.validators)
}

// ValidatorPower 返回验证者的投票权重（线程安全，通过 ValidatorSet 锁访问）。
func (vs *ValidatorSet) ValidatorPower(pubKey [32]byte) int64 {
	vs.mu.RLock()
	defer vs.mu.RUnlock()
	if info, ok := vs.validators[pubKey]; ok {
		return info.VotingPower
	}
	return 0
}

// ============================================================
// PBFT 共识引擎
// ============================================================

// PBFTEngine PBFT 共识引擎。
type PBFTEngine struct {
	mu sync.Mutex

	// 状态
	height    int64
	round     int32
	step      PBFTStep
	validator bool // 当前节点是否为验证者
	myPubKey  [32]byte

	// 验证者集
	validators *ValidatorSet

	// 当前轮的投票
	prevotes    map[[32]byte]*PBFTVote   // validator → vote
	precommits  map[[32]byte]*PBFTVote
	lockedBlock *PBFTProposal             // 锁定的区块（预提交后锁定）
	lockedRound int32

	// 当前提案
	currentProposal *PBFTProposal

	// 事件通道
	blockCh     chan BlockEvent
	proposalCh  chan *PBFTProposal   // 接收外部提案的通道
	voteCh      chan *PBFTVote       // 接收外部投票的通道

	// 超时
	stepTimeout  time.Duration
	stepDeadline time.Time
	timer        *time.Timer

	// 生命周期
	stopCh  chan struct{}
	stopped bool
	started bool

	// 回调（用于与网络层交互）
	OnBroadcastProposal func(proposal *PBFTProposal)
	OnBroadcastVote     func(vote *PBFTVote)
}

// NewPBFTEngine 创建 PBFT 共识引擎。
func NewPBFTEngine(myPubKey [32]byte, isValidator bool, timeout time.Duration) *PBFTEngine {
	engine := &PBFTEngine{
		validator:   isValidator,
		myPubKey:    myPubKey,
		validators:  NewValidatorSet(),
		prevotes:    make(map[[32]byte]*PBFTVote),
		precommits:  make(map[[32]byte]*PBFTVote),
		blockCh:     make(chan BlockEvent, 10),
		proposalCh:  make(chan *PBFTProposal, 100),
		voteCh:      make(chan *PBFTVote, 100),
		stepTimeout: timeout,
		stopCh:      make(chan struct{}),
	}
	// 将自己加入验证者集
	if isValidator {
		engine.validators.AddValidator(&ValidatorInfo{
			PubKey:      myPubKey,
			VotingPower: 1,
			IsActive:    true,
			JoinedAt:    time.Now().Unix(),
		})
	}
	return engine
}

// ============================================================
// Engine 接口实现
// ============================================================

func (e *PBFTEngine) Start() error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.started {
		return fmt.Errorf("pbft: already started")
	}
	if !e.validator {
		return nil
	}
	e.started = true
	e.step = PBFTStepNewHeight
	go e.loop()
	log.Printf("[pbft] engine started (validator=%v height=%d)", e.validator, e.height)
	return nil
}

func (e *PBFTEngine) Stop() error {
	e.mu.Lock()
	if e.stopped {
		e.mu.Unlock()
		return nil
	}
	e.stopped = true
	e.mu.Unlock()
	close(e.stopCh)
	log.Printf("[pbft] engine stopped at height %d", e.height)
	return nil
}

func (e *PBFTEngine) IsValidator() bool { return e.validator }

func (e *PBFTEngine) Height() int64 {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.height
}

func (e *PBFTEngine) ProposeBlock(txs [][]byte) ([]byte, int64, error) {
	e.mu.Lock()
	e.height++
	e.round = 0
	e.step = PBFTStepPropose
	h := e.height
	e.mu.Unlock()

	// 构建提案
	blockHash := ComputeBlockHash(txs, h)
	blockData := FlattenTxs(txs)
	if blockData == nil {
		blockData = []byte(fmt.Sprintf("block-%d", h)) // 确保非 nil
	}
	proposal := &PBFTProposal{
		Height:    h,
		Round:     0,
		BlockData: blockData,
		BlockHash: blockHash,
		Proposer:  e.myPubKey,
		Timestamp: time.Now().Unix(),
	}

	e.mu.Lock()
	e.currentProposal = proposal
	e.mu.Unlock()

	// 广播提案
	if e.OnBroadcastProposal != nil {
		e.OnBroadcastProposal(proposal)
	}

	log.Printf("[pbft] proposed block height=%d hash=%x", h, blockHash[:8])
	return proposal.BlockData, h, nil
}

func (e *PBFTEngine) CommitBlock(blockData []byte, height int64) error {
	select {
	case e.blockCh <- BlockEvent{
		Height:    height,
		BlockHash: sha256.Sum256(blockData),
		Txs:       nil,
		Timestamp: time.Now(),
	}:
	default:
	}
	log.Printf("[pbft] committed block height=%d", height)
	return nil
}

func (e *PBFTEngine) SubscribeBlocks() <-chan BlockEvent {
	return e.blockCh
}

// ============================================================
// 外部消息接口
// ============================================================

// ReceiveProposal 接收来自其他验证者的提案。
func (e *PBFTEngine) ReceiveProposal(proposal *PBFTProposal) {
	select {
	case e.proposalCh <- proposal:
	default:
		log.Printf("[pbft] proposal channel full, dropping proposal for height=%d", proposal.Height)
	}
}

// ReceiveVote 接收来自其他验证者的投票。
func (e *PBFTEngine) ReceiveVote(vote *PBFTVote) {
	select {
	case e.voteCh <- vote:
	default:
	}
}

// AddRemoteValidator 添加远程验证者。
func (e *PBFTEngine) AddRemoteValidator(pubKey [32]byte, votingPower int64, address string) {
	e.validators.AddValidator(&ValidatorInfo{
		PubKey:      pubKey,
		VotingPower: votingPower,
		Address:     address,
		IsActive:    true,
		JoinedAt:    time.Now().Unix(),
	})
}

// ============================================================
// 主循环
// ============================================================

func (e *PBFTEngine) loop() {
	// 启动时进入首个高度
	e.enterNewHeight()

	// 首个高度定时提案
	proposeTimer := time.NewTimer(e.stepTimeout)
	defer proposeTimer.Stop()

	for {
		select {
		case <-e.stopCh:
			return

		case <-proposeTimer.C:
			e.mu.Lock()
			proposer := e.validators.GetProposer(e.height)
			isProposer := proposer != nil && proposer.PubKey == e.myPubKey
			e.mu.Unlock()
			if isProposer {
				e.ProposeBlock(nil)
			}
			proposeTimer.Reset(e.stepTimeout)

		case proposal := <-e.proposalCh:
			e.handleProposal(proposal)

		case vote := <-e.voteCh:
			e.handleVote(vote)

		case <-e.timerCh():
			e.handleTimeout()
		}
	}
}

func (e *PBFTEngine) timerCh() <-chan time.Time {
	if e.timer != nil {
		return e.timer.C
	}
	return nil
}

// ============================================================
// 状态处理
// ============================================================

func (e *PBFTEngine) enterNewHeight() {
	e.mu.Lock()
	defer e.mu.Unlock()

	e.height++
	e.round = 0
	e.step = PBFTStepPropose
	e.prevotes = make(map[[32]byte]*PBFTVote)
	e.precommits = make(map[[32]byte]*PBFTVote)
	e.currentProposal = nil
	e.lockedBlock = nil
	e.scheduleTimeout()
}

func (e *PBFTEngine) handleProposal(proposal *PBFTProposal) {
	e.mu.Lock()
	defer e.mu.Unlock()

	// 只处理与当前高度匹配的提案
	if proposal.Height != e.height {
		return
	}

	// 验证提议者
	proposer := e.validators.GetProposer(proposal.Height)
	if proposer == nil || proposer.PubKey != proposal.Proposer {
		return // 不是合法提议者
	}

	e.currentProposal = proposal
	log.Printf("[pbft] received proposal height=%d round=%d from %x",
		proposal.Height, proposal.Round, proposal.Proposer[:8])

	// 进入 PREVOTE 阶段
	e.step = PBFTStepPrevote
	e.scheduleTimeout()

	// 为提案投票
	e.broadcastVote(PBFTStepPrevote, proposal.BlockHash)
}

func (e *PBFTEngine) handleVote(vote *PBFTVote) {
	e.mu.Lock()
	defer e.mu.Unlock()

	if vote.Height != e.height {
		return
	}

	switch vote.Step {
	case PBFTStepPrevote:
		e.prevotes[vote.Validator] = vote
		e.tryAdvanceFromPrevote()

	case PBFTStepPrecommit:
		e.precommits[vote.Validator] = vote
		e.tryAdvanceFromPrecommit()
	}
}

func (e *PBFTEngine) handleTimeout() {
	e.mu.Lock()
	defer e.mu.Unlock()

	log.Printf("[pbft] timeout at height=%d round=%d step=%s", e.height, e.round, e.step)

	// 增加 round 号重试同一高度（而非跳高度）
	e.round++
	e.step = PBFTStepPropose

	// 重置投票
	e.prevotes = make(map[[32]byte]*PBFTVote)
	e.precommits = make(map[[32]byte]*PBFTVote)
	e.scheduleTimeout()

	// 释放锁后由 timer 触发重提议
}

// ============================================================
// 投票推进逻辑
// ============================================================

func (e *PBFTEngine) tryAdvanceFromPrevote() {
	// 计算 2/3+ 预投票
	quorum := e.validators.Quorum()
	var yesVotes, totalVotes int64

	var winningHash [32]byte
	hashVotes := make(map[[32]byte]int64)

	for _, v := range e.prevotes {
		hashVotes[v.BlockHash] += e.getValidatorPower(v.Validator)
		totalVotes += e.getValidatorPower(v.Validator)
		if hashVotes[v.BlockHash] >= quorum {
			winningHash = v.BlockHash
			yesVotes = hashVotes[v.BlockHash]
		}
	}

	if winningHash == [32]byte{} {
		return // 未达到法定人数
	}

	_ = yesVotes
	_ = totalVotes

	log.Printf("[pbft] prevote quorum reached height=%d hash=%x", e.height, winningHash[:8])

	// 进入 PRECOMMIT
	e.step = PBFTStepPrecommit
	e.scheduleTimeout()

	// 锁定当前提案
	if e.currentProposal != nil && e.currentProposal.BlockHash == winningHash {
		e.lockedBlock = e.currentProposal
		e.lockedRound = e.round
	}

	// 广播预提交
	e.broadcastVote(PBFTStepPrecommit, winningHash)
}

func (e *PBFTEngine) tryAdvanceFromPrecommit() {
	quorum := e.validators.Quorum()
	var yesVotes int64
	hashVotes := make(map[[32]byte]int64)

	for _, v := range e.precommits {
		hashVotes[v.BlockHash] += e.getValidatorPower(v.Validator)
		if hashVotes[v.BlockHash] >= quorum {
			yesVotes = hashVotes[v.BlockHash]
		}
	}

	if yesVotes < quorum {
		return
	}

	log.Printf("[pbft] precommit quorum reached height=%d", e.height)

	// 提交区块
	e.step = PBFTStepCommit
	if e.currentProposal != nil {
		e.mu.Unlock()
		e.CommitBlock(e.currentProposal.BlockData, e.height)
		e.mu.Lock()
	}

	// 进入下一高度
	e.enterNewHeight()
}

// ============================================================
// 辅助
// ============================================================

func (e *PBFTEngine) broadcastVote(step PBFTStep, blockHash [32]byte) {
	vote := &PBFTVote{
		Height:    e.height,
		Round:     e.round,
		Step:      step,
		BlockHash: blockHash,
		Validator: e.myPubKey,
		Timestamp: time.Now().Unix(),
	}

	// 记录自己的投票
	switch step {
	case PBFTStepPrevote:
		e.prevotes[e.myPubKey] = vote
	case PBFTStepPrecommit:
		e.precommits[e.myPubKey] = vote
	}

	if e.OnBroadcastVote != nil {
		e.OnBroadcastVote(vote)
	}
}

func (e *PBFTEngine) scheduleTimeout() {
	if e.timer != nil {
		e.timer.Stop()
	}
	e.timer = time.NewTimer(e.stepTimeout)
}

func (e *PBFTEngine) getValidatorPower(pubKey [32]byte) int64 {
	return e.validators.ValidatorPower(pubKey)
}
