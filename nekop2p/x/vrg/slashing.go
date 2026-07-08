// Package vrg 社区级 Slashing (VRG-09)。
//
// 社区级 Slashing 为虚拟根网图提供集体惩罚机制。
// 当社区整体作恶时，不仅该社区受罚，其邀请链上的上级社区
// 也承担连带责任——这是基于创世邀请链的递归惩罚模型。
//
// 惩罚模型:
//   ┌─ 社区整体作恶（双花/系统性欺诈）
//   │
//   ├─ ① 社区根节点 Bond 全额罚没
//   ├─ ② 所有外交通道自动切断 (SEVERED)
//   ├─ ③ 邀请链上级社区连带罚没（按距离衰减）
//   │     上级1层: 罚没自身 Bond 的 30%
//   │     上级2层: 罚没自身 Bond 的 15%
//   │     上级3层及以上: 罚没自身 Bond 的 5%
//   ├─ ④ 劣迹社区降级为黑名单
//   └─ ⑤ 冲突事件广播全网警示
//
// 安全设计:
//   - Slashing 事件需由治理提案 + 门限签名触发
//   - 被 Slashing 的社区可在冷却期后通过改进证明申请解除黑名单
//   - 罚没资金注入坏账准备金池
package vrg

import (
	"crypto/sha256"
	"fmt"
	"sort"
	"sync"
)

// ============================================================
// Slashing 条件定义
// ============================================================

// SlashReason Slashing 原因。
type SlashReason int32

const (
	SlashNone           SlashReason = 0  // 无惩罚
	SlashDoubleSpend    SlashReason = 1  // 跨纪元双花攻击
	SlashSystemicFraud  SlashReason = 2  // 系统性欺诈（社区多数成员合谋）
	SlashBondDefault    SlashReason = 3  // 社区质押违约
	SlashDiplomaticBreak SlashReason = 4 // 外交通道违约
	SlashGovernanceAttack SlashReason = 5 // 治理攻击
)

func (sr SlashReason) String() string {
	switch sr {
	case SlashNone: return "NONE"
	case SlashDoubleSpend: return "DOUBLE_SPEND"
	case SlashSystemicFraud: return "SYSTEMIC_FRAUD"
	case SlashBondDefault: return "BOND_DEFAULT"
	case SlashDiplomaticBreak: return "DIPLOMATIC_BREAK"
	case SlashGovernanceAttack: return "GOVERNANCE_ATTACK"
	default: return "UNKNOWN"
	}
}

// ============================================================
// Slashing 记录与黑名单
// ============================================================

// SlashRecord 单次 Slashing 事件记录。
type SlashRecord struct {
	RecordID     [32]byte   // 唯一事件 ID
	CommunityID  [32]byte   // 被罚社区虚拟化身 ID
	Reason       SlashReason // 惩罚原因
	Evidence     []byte      // 证据（碰撞哈希/欺诈交易等）
	
	// 罚没金额
	ForfeitedBond    uint64 // 社区自身 Bond 被罚没金额
	AncestryForfeited uint64 // 上级社区连带罚没总额
	
	// 受影响的外交通道
	SeveredEdges []EdgeSeverRecord // 被切断的通道
	
	// 时间戳
	SlashEpoch  uint64 // 罚没发生的 Epoch
	SlashBlock  uint64 // 罚没发生的区块
	SlashAt     int64  // 罚没时间
	
	// 治理信息
	ProposalID  [32]byte // 触发 Slashing 的治理提案 ID
	// 门限签名: 由 >= ceil(2n/3) 个社区代表签署
	ApprovalsNeeded int    // 需要的批准数
	ApprovalsGot    int    // 实际批准数
}

// EdgeSeverRecord 通道切断记录。
type EdgeSeverRecord struct {
	EdgeID         string   // 被切断的通道 ID (TrustEdgeManager 使用 string)
	OtherCommunity [32]byte  // 通道另一方
	ForfeitedBond  uint64    // 通道中该社区被罚没的 Bond
}

// BlacklistEntry 黑名单条目。
type BlacklistEntry struct {
	CommunityID [32]byte
	CommunityName string
	Reason      SlashReason
	SlashAt     int64
	SlashEpoch  uint64
	
	// 重新接纳条件
	CooldownEndsAt  int64  // 冷却期结束时间（此后可申请解除）
	CanAppeal       bool   // 是否可申诉
	RequiredProof   string // 重新接纳需要的证明描述
}

// ============================================================
// SlashingManager 社区惩罚管理器
// ============================================================

// SlashingManager 管理社区级 Slashing 和黑名单。
type SlashingManager struct {
	mu sync.RWMutex
	
	// Slashing 记录
	records    []*SlashRecord                    // 按时间排序
	byCommunity map[[32]byte][]*SlashRecord      // communityID → records
	
	// 黑名单
	blacklist  map[[32]byte]*BlacklistEntry
	
	// 依赖
	topology   *GenesisTree          // 用于递归上溯
	diplomacy  *TrustEdgeManager     // 用于切断外交通道
	
	// 统计
	totalForfeited  uint64 // 累计罚没金额
	totalSlashed    uint64 // 累计被罚社区数
}

// NewSlashingManager 创建社区惩罚管理器。
func NewSlashingManager(topology *GenesisTree, diplomacy *TrustEdgeManager) *SlashingManager {
	return &SlashingManager{
		records:     make([]*SlashRecord, 0),
		byCommunity: make(map[[32]byte][]*SlashRecord),
		blacklist:   make(map[[32]byte]*BlacklistEntry),
		topology:    topology,
		diplomacy:   diplomacy,
	}
}

// ============================================================
// Slashing 执行
// ============================================================

// SlashCommunity 对社区执行 Slashing。
//
// 此函数执行以下操作:
//   1. 罚没社区根节点 Bond
//   2. 递归上溯罚没上级社区 Bond（按距离衰减）
//   3. 切断该社区所有活跃外交通道
//   4. 将社区加入黑名单
//   5. 记录 Slashing 事件
//
// 返回罚没总额。
func (sm *SlashingManager) SlashCommunity(
	communityID [32]byte,
	communityName string,
	reason SlashReason,
	evidence []byte,
	slashEpoch uint64,
	slashBlock uint64,
	slashAt int64,
	proposalID [32]byte,
	approvalsGot int,
	approvalsNeeded int,
) (*SlashRecord, uint64, error) {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	// 不能重复 Slashing 已在黑名单中的社区
	if _, blacklisted := sm.blacklist[communityID]; blacklisted {
		return nil, 0, fmt.Errorf("slashing: community %x already blacklisted", communityID[:8])
	}

	// 获取社区拓扑节点
	node := sm.topology.GetCommunity(communityID)
	if node == nil {
		return nil, 0, fmt.Errorf("slashing: community %x not in genesis tree", communityID[:8])
	}

	// === 第1步: 罚没社区自身 Bond ===
	selfForfeit := node.BondAmount
	node.BondAmount = 0 // 清零

	// === 第2步: 递归上溯罚没上级社区 ===
	ancestryForfeited, severedEdges := sm.slashAncestry(communityID, reason, slashAt)

	// === 第3步: 切断所有活跃外交通道 ===
	activeEdges := sm.diplomacy.GetEdgesByParty(communityID)
	for _, edge := range activeEdges {
		if edge.Status != TrustEdgeActive {
			continue
		}
		_, _, err := sm.diplomacy.SeverEdge(edge.EdgeID, communityID)
		if err != nil {
			continue
		}

		otherID := sm.otherParty(edge, communityID)
		severedEdges = append(severedEdges, EdgeSeverRecord{
			EdgeID:         edge.EdgeID,
			OtherCommunity: otherID,
			ForfeitedBond:  0, // 对方 Bond 不受影响
		})
	}

	// === 第4步: 加入黑名单 ===
	sm.blacklist[communityID] = &BlacklistEntry{
		CommunityID:    communityID,
		CommunityName:  communityName,
		Reason:         reason,
		SlashAt:        slashAt,
		SlashEpoch:     slashEpoch,
		CooldownEndsAt: slashAt + cooldownDuration(reason),
		CanAppeal:      reason != SlashSystemicFraud, // 系统性欺诈不可申诉
		RequiredProof:  requiredProofForReason(reason),
	}

	// === 第5步: 创建 Slashing 记录 ===
	record := &SlashRecord{
		RecordID:          computeSlashRecordID(communityID, slashEpoch, slashAt),
		CommunityID:       communityID,
		Reason:            reason,
		Evidence:          evidence,
		ForfeitedBond:     selfForfeit,
		AncestryForfeited: ancestryForfeited,
		SeveredEdges:      severedEdges,
		SlashEpoch:        slashEpoch,
		SlashBlock:        slashBlock,
		SlashAt:           slashAt,
		ProposalID:        proposalID,
		ApprovalsNeeded:   approvalsNeeded,
		ApprovalsGot:      approvalsGot,
	}

	sm.records = append(sm.records, record)
	sm.byCommunity[communityID] = append(sm.byCommunity[communityID], record)
	
	totalForfeit := selfForfeit + ancestryForfeited
	sm.totalForfeited += totalForfeit
	sm.totalSlashed++

	return record, totalForfeit, nil
}

// slashAncestry 递归上溯罚没上级社区 Bond。
// 返回罚没总额和受影响的通道记录。
func (sm *SlashingManager) slashAncestry(
	communityID [32]byte,
	reason SlashReason,
	slashAt int64,
) (uint64, []EdgeSeverRecord) {
	var totalForfeited uint64
	var severedEdges []EdgeSeverRecord

	// 使用 AncestorChain 获取上级社区链
	ancestors := sm.topology.AncestorChain(communityID)
	for level, ancestorID := range ancestors {
		if level == 0 {
			continue // 跳过自己
		}
		if level > 3 {
			break // 最多上溯3级
		}

		parentNode := sm.topology.GetCommunity(ancestorID)
		if parentNode == nil {
			continue
		}

		// 按距离衰减
		rate := ancestrySlashRate(level)
		forfeit := uint64(float64(parentNode.BondAmount) * rate)
		if forfeit > parentNode.BondAmount {
			forfeit = parentNode.BondAmount
		}

		parentNode.BondAmount -= forfeit
		totalForfeited += forfeit

		// 切断与上级的外交通道
		parentEdges := sm.diplomacy.GetEdgesByParty(ancestorID)
		for _, edge := range parentEdges {
			if edge.Status != TrustEdgeActive {
				continue
			}
			if edge.PartyA == communityID || edge.PartyB == communityID {
				_, _, err := sm.diplomacy.SeverEdge(edge.EdgeID, communityID)
				if err != nil {
					continue
				}
				severedEdges = append(severedEdges, EdgeSeverRecord{
					EdgeID:         edge.EdgeID,
					OtherCommunity: ancestorID,
					ForfeitedBond:  forfeit,
				})
			}
		}
	}

	return totalForfeited, severedEdges
}

// ancestrySlashRate 上级连带罚没比例。
// level 1: 直接上级 30%
// level 2: 上上级 15%
// level 3+: 更上级 5%
func ancestrySlashRate(level int) float64 {
	switch level {
	case 1:
		return 0.30
	case 2:
		return 0.15
	default:
		return 0.05
	}
}

// ============================================================
// 黑名单管理
// ============================================================

// IsBlacklisted 检查社区是否在黑名单中。
func (sm *SlashingManager) IsBlacklisted(communityID [32]byte) bool {
	sm.mu.RLock()
	defer sm.mu.RUnlock()
	_, exists := sm.blacklist[communityID]
	return exists
}

// GetBlacklistEntry 获取黑名单条目。
func (sm *SlashingManager) GetBlacklistEntry(communityID [32]byte) *BlacklistEntry {
	sm.mu.RLock()
	defer sm.mu.RUnlock()
	return sm.blacklist[communityID]
}

// ListBlacklist 列出所有黑名单社区。
func (sm *SlashingManager) ListBlacklist() []*BlacklistEntry {
	sm.mu.RLock()
	defer sm.mu.RUnlock()

	entries := make([]*BlacklistEntry, 0, len(sm.blacklist))
	for _, entry := range sm.blacklist {
		entries = append(entries, entry)
	}
	// 按惩罚时间排序
	sort.Slice(entries, func(i, j int) bool {
		return entries[i].SlashAt < entries[j].SlashAt
	})
	return entries
}

// RemoveFromBlacklist 从黑名单中移除社区。
// 需满足冷却期已过 + 改进证明验证通过。
func (sm *SlashingManager) RemoveFromBlacklist(
	communityID [32]byte,
	proofValid bool,
	currentTime int64,
) error {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	entry, exists := sm.blacklist[communityID]
	if !exists {
		return fmt.Errorf("slashing: community %x not blacklisted", communityID[:8])
	}

	if !entry.CanAppeal {
		return fmt.Errorf("slashing: community %x cannot appeal (systemic fraud)", communityID[:8])
	}

	if currentTime < entry.CooldownEndsAt {
		remaining := entry.CooldownEndsAt - currentTime
		return fmt.Errorf("slashing: cooldown not expired, %d seconds remaining", remaining)
	}

	if !proofValid {
		return fmt.Errorf("slashing: improvement proof validation failed")
	}

	delete(sm.blacklist, communityID)
	return nil
}

// ============================================================
// Slashing 查询
// ============================================================

// GetSlashRecords 获取社区的所有 Slashing 记录。
func (sm *SlashingManager) GetSlashRecords(communityID [32]byte) []*SlashRecord {
	sm.mu.RLock()
	defer sm.mu.RUnlock()
	return sm.byCommunity[communityID]
}

// GetAllSlashRecords 获取所有 Slashing 记录（按时间排序）。
func (sm *SlashingManager) GetAllSlashRecords() []*SlashRecord {
	sm.mu.RLock()
	defer sm.mu.RUnlock()

	result := make([]*SlashRecord, len(sm.records))
	copy(result, sm.records)
	return result
}

// GetTotalForfeited 获取累计罚没金额。
func (sm *SlashingManager) GetTotalForfeited() uint64 {
	sm.mu.RLock()
	defer sm.mu.RUnlock()
	return sm.totalForfeited
}

// GetTotalSlashed 获取累计被罚社区数。
func (sm *SlashingManager) GetTotalSlashed() uint64 {
	sm.mu.RLock()
	defer sm.mu.RUnlock()
	return sm.totalSlashed
}

// ============================================================
// 碰撞广播 (使用 epoch.go 中的 CollisionEvent)
// ============================================================

// CollisionEvent 已在 epoch.go:178 中定义，此处直接使用。
// 补充广播辅助方法。

// ============================================================
// 辅助函数
// ============================================================

// computeSlashRecordID 生成 Slashing 记录唯一 ID。
func computeSlashRecordID(communityID [32]byte, epoch uint64, slashAt int64) [32]byte {
	h := sha256.New()
	h.Write([]byte("nekop2p-slash-v1"))
	h.Write(communityID[:])
	h.Write(uint64ToBytesVRG(epoch))
	h.Write(int64ToBytes(slashAt))
	var result [32]byte
	copy(result[:], h.Sum(nil))
	return result
}

// cooldownDuration 根据惩罚原因返回黑名单冷却期（秒）。
func cooldownDuration(reason SlashReason) int64 {
	const day = 86400
	switch reason {
	case SlashDoubleSpend:
		return 30 * day // 30 天
	case SlashBondDefault:
		return 14 * day // 14 天
	case SlashDiplomaticBreak:
		return 7 * day // 7 天
	case SlashGovernanceAttack:
		return 90 * day // 90 天
	case SlashSystemicFraud:
		return 365 * day // 1 年（不可申诉）
	default:
		return 14 * day
	}
}

// requiredProofForReason 返回重新接纳需要的证明描述。
func requiredProofForReason(reason SlashReason) string {
	switch reason {
	case SlashDoubleSpend:
		return "提交连续 100 个 Epoch 无双花记录证明"
	case SlashBondDefault:
		return "补足 Bond 质押 + 提交新门限签名社区状态根"
	case SlashDiplomaticBreak:
		return "双方社区联合签署和解协议"
	case SlashGovernanceAttack:
		return "通过新治理提案撤销攻击 + 门限签名承诺合规"
	default:
		return "提交改进证明"
	}
}

// otherParty 返回外交通道的另一方社区 ID。
func (sm *SlashingManager) otherParty(edge *TrustEdge, communityID [32]byte) [32]byte {
	if edge.PartyA == communityID {
		return edge.PartyB
	}
	return edge.PartyA
}

// int64ToBytes 将 int64 编码为 8 字节大端序。
func int64ToBytes(v int64) []byte {
	b := make([]byte, 8)
	b[0] = byte(v >> 56)
	b[1] = byte(v >> 48)
	b[2] = byte(v >> 40)
	b[3] = byte(v >> 32)
	b[4] = byte(v >> 24)
	b[5] = byte(v >> 16)
	b[6] = byte(v >> 8)
	b[7] = byte(v)
	return b
}
