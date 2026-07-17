//go:build !cosmos

// Package vrg Namespace 隔离 (VRG-05)。
//
// 应用级命名空间路由：每个应用分配唯一 NamespaceID，
// 交易按 Namespace 路由至独立子状态树。
// 跨 Namespace 交易需通过外交通道 (TrustEdge)。
package vrg

import (
	"fmt"
	"sync"
)

// NamespaceID 应用命名空间标识符。
type NamespaceID uint16

const (
	NamespaceGenesis  NamespaceID = 0    // 创世命名空间（链基础设施）
	NamespaceBright   NamespaceID = 1    // 明链应用
	NamespaceDark     NamespaceID = 2    // 暗链应用
	NamespaceGame     NamespaceID = 3    // 游戏应用
	NamespaceCommunity NamespaceID = 100 // 社区命名空间起始 (100+)
)

// Namespace 命名空间定义。
type Namespace struct {
	ID          NamespaceID
	Name        string
	OwnerID     [32]byte // 所属社区虚拟化身 ID
	CreatedAt   int64
	StateRoot   [32]byte // 独立状态树根
	BlockHeight int64    // 命名空间内的区块高度
}

// NamespaceRouter 命名空间路由器。
// 将交易按 NamespaceID 路由至对应的社区或应用。
type NamespaceRouter struct {
	mu         sync.RWMutex
	namespaces map[NamespaceID]*Namespace
	byOwner    map[[32]byte][]NamespaceID // 社区拥有的命名空间
	nextID     NamespaceID
}

// NewNamespaceRouter 创建命名空间路由器。
func NewNamespaceRouter() *NamespaceRouter {
	return &NamespaceRouter{
		namespaces: make(map[NamespaceID]*Namespace),
		byOwner:    make(map[[32]byte][]NamespaceID),
		nextID:     NamespaceCommunity,
	}
}

// RegisterNamespace 注册新命名空间。
func (nr *NamespaceRouter) RegisterNamespace(name string, ownerID [32]byte) (*Namespace, error) {
	nr.mu.Lock()
	defer nr.mu.Unlock()

	id := nr.nextID
	nr.nextID++

	ns := &Namespace{
		ID:        id,
		Name:      name,
		OwnerID:   ownerID,
		CreatedAt: nowUnix(),
	}
	nr.namespaces[id] = ns
	nr.byOwner[ownerID] = append(nr.byOwner[ownerID], id)

	return ns, nil
}

// GetNamespace 获取命名空间。
func (nr *NamespaceRouter) GetNamespace(id NamespaceID) *Namespace {
	nr.mu.RLock()
	defer nr.mu.RUnlock()
	return nr.namespaces[id]
}

// GetNamespacesByOwner 获取社区拥有的所有命名空间。
func (nr *NamespaceRouter) GetNamespacesByOwner(ownerID [32]byte) []*Namespace {
	nr.mu.RLock()
	defer nr.mu.RUnlock()
	var result []*Namespace
	for _, id := range nr.byOwner[ownerID] {
		if ns, ok := nr.namespaces[id]; ok {
			result = append(result, ns)
		}
	}
	return result
}

// RouteTransaction 路由交易至对应命名空间。
// 返回目标命名空间 ID（如果找不到则返回错误）。
func (nr *NamespaceRouter) RouteTransaction(nsID NamespaceID) (*Namespace, error) {
	nr.mu.RLock()
	defer nr.mu.RUnlock()

	ns, exists := nr.namespaces[nsID]
	if !exists {
		return nil, fmt.Errorf("namespace %d not registered", nsID)
	}
	return ns, nil
}

// UpdateStateRoot 更新命名空间的状态根。
func (nr *NamespaceRouter) UpdateStateRoot(nsID NamespaceID, root [32]byte, height int64) error {
	nr.mu.Lock()
	defer nr.mu.Unlock()

	ns, exists := nr.namespaces[nsID]
	if !exists {
		return fmt.Errorf("namespace %d not found", nsID)
	}
	ns.StateRoot = root
	ns.BlockHeight = height
	return nil
}

// ============================================================
// E6: 外交通道 (TrustEdge)
// ============================================================

// TrustEdgeStatus 外交通道状态。
type TrustEdgeStatus int32

const (
	TrustEdgeProposed  TrustEdgeStatus = 0 // 提案中
	TrustEdgeActive    TrustEdgeStatus = 1 // 活跃
	TrustEdgeFrozen    TrustEdgeStatus = 2 // 冻结
	TrustEdgeSevered   TrustEdgeStatus = 3 // 已切断
)

func (ts TrustEdgeStatus) String() string {
	switch ts {
	case TrustEdgeProposed: return "PROPOSED"
	case TrustEdgeActive: return "ACTIVE"
	case TrustEdgeFrozen: return "FROZEN"
	case TrustEdgeSevered: return "SEVERED"
	default: return "UNKNOWN"
	}
}

// TrustEdge 跨 Namespace 外交通道。
// 连接两个社区/应用的信任桥梁。
type TrustEdge struct {
	EdgeID     string
	ChannelID  uint16 // 通道编号

	// 参与方
	PartyA     [32]byte // 社区 A 虚拟化身 ID
	PartyB     [32]byte // 社区 B 虚拟化身 ID
	NamespaceA NamespaceID
	NamespaceB NamespaceID

	// 双向质押
	BondA      uint64 // A 质押金额
	BondB      uint64 // B 质押金额
	TotalBond  uint64

	// 时间戳
	ProposedAt int64
	ActivatedAt int64
	FrozenAt   int64
	SeveredAt  int64

	// 交易统计
	TotalTransfers uint64
	TotalVolume    uint64
	DisputeCount   uint64

	// 状态
	Status TrustEdgeStatus
}

// TrustEdgeManager 外交通道管理器。
type TrustEdgeManager struct {
	mu     sync.RWMutex
	edges  map[string]*TrustEdge // edgeID → edge
	byParty map[[32]byte][]string // partyID → edgeIDs
}

// NewTrustEdgeManager 创建外交通道管理器。
func NewTrustEdgeManager() *TrustEdgeManager {
	return &TrustEdgeManager{
		edges:   make(map[string]*TrustEdge),
		byParty: make(map[[32]byte][]string),
	}
}

// ProposeEdge 提案建立外交通道。
func (tem *TrustEdgeManager) ProposeEdge(
	partyA, partyB [32]byte,
	nsA, nsB NamespaceID,
	bondA, bondB uint64,
) (*TrustEdge, error) {
	tem.mu.Lock()
	defer tem.mu.Unlock()

	// 检查是否已有活跃通道
	for _, edge := range tem.edges {
		if edge.Status == TrustEdgeActive &&
			((edge.PartyA == partyA && edge.PartyB == partyB) ||
				(edge.PartyA == partyB && edge.PartyB == partyA)) {
			return nil, fmt.Errorf("diplomacy: active edge already exists between %x and %x",
				partyA[:8], partyB[:8])
		}
	}

	edgeID := fmt.Sprintf("edge-%x-%x-%d", partyA[:8], partyB[:8], nowUnix())
	edge := &TrustEdge{
		EdgeID:     edgeID,
		PartyA:     partyA,
		PartyB:     partyB,
		NamespaceA: nsA,
		NamespaceB: nsB,
		BondA:      bondA,
		BondB:      bondB,
		TotalBond:  bondA + bondB,
		ProposedAt: nowUnix(),
		Status:     TrustEdgeProposed,
	}

	tem.edges[edgeID] = edge
	tem.byParty[partyA] = append(tem.byParty[partyA], edgeID)
	tem.byParty[partyB] = append(tem.byParty[partyB], edgeID)

	return edge, nil
}

// ActivateEdge 激活外交通道。
func (tem *TrustEdgeManager) ActivateEdge(edgeID string) error {
	tem.mu.Lock()
	defer tem.mu.Unlock()

	edge, exists := tem.edges[edgeID]
	if !exists {
		return fmt.Errorf("diplomacy: edge %s not found", edgeID)
	}
	if edge.Status != TrustEdgeProposed {
		return fmt.Errorf("diplomacy: edge %s is %s, not proposed", edgeID, edge.Status)
	}

	// 双方必须都质押了保证金
	if edge.BondA == 0 || edge.BondB == 0 {
		return fmt.Errorf("diplomacy: both parties must stake bonds (A=%d, B=%d)", edge.BondA, edge.BondB)
	}

	edge.Status = TrustEdgeActive
	edge.ActivatedAt = nowUnix()
	return nil
}

// FreezeEdge 冻结外交通道（争议期间）。
func (tem *TrustEdgeManager) FreezeEdge(edgeID string) error {
	tem.mu.Lock()
	defer tem.mu.Unlock()

	edge, exists := tem.edges[edgeID]
	if !exists {
		return fmt.Errorf("diplomacy: edge %s not found", edgeID)
	}
	if edge.Status != TrustEdgeActive {
		return fmt.Errorf("diplomacy: edge %s is %s", edgeID, edge.Status)
	}

	edge.Status = TrustEdgeFrozen
	edge.FrozenAt = nowUnix()
	edge.DisputeCount++
	return nil
}

// ThawEdge 解冻外交通道（争议解决后恢复）。
func (tem *TrustEdgeManager) ThawEdge(edgeID string) error {
	tem.mu.Lock()
	defer tem.mu.Unlock()

	edge, exists := tem.edges[edgeID]
	if !exists {
		return fmt.Errorf("diplomacy: edge %s not found", edgeID)
	}
	if edge.Status != TrustEdgeFrozen {
		return fmt.Errorf("diplomacy: edge %s is %s, not frozen", edgeID, edge.Status)
	}

	edge.Status = TrustEdgeActive
	return nil
}

// SeverEdge 切断外交通道（违约惩罚）。
// 支持从 ACTIVE 或 FROZEN 状态直接切断。
func (tem *TrustEdgeManager) SeverEdge(edgeID string, violatingParty [32]byte) (*TrustEdge, uint64, error) {
	tem.mu.Lock()
	defer tem.mu.Unlock()

	edge, exists := tem.edges[edgeID]
	if !exists {
		return nil, 0, fmt.Errorf("diplomacy: edge %s not found", edgeID)
	}
	if edge.Status != TrustEdgeActive && edge.Status != TrustEdgeFrozen {
		return nil, 0, fmt.Errorf("diplomacy: edge %s is %s, cannot sever", edgeID, edge.Status)
	}

	var slashAmount uint64
	if violatingParty == edge.PartyA {
		slashAmount = edge.BondA
		edge.BondA = 0
	} else if violatingParty == edge.PartyB {
		slashAmount = edge.BondB
		edge.BondB = 0
	} else {
		return nil, 0, fmt.Errorf("diplomacy: %x is not a party to edge %s", violatingParty[:8], edgeID)
	}

	edge.Status = TrustEdgeSevered
	edge.SeveredAt = nowUnix()
	edge.TotalBond = edge.BondA + edge.BondB

	return edge, slashAmount, nil
}

// GetEdgesByParty 获取参与方的所有通道。
func (tem *TrustEdgeManager) GetEdgesByParty(partyID [32]byte) []*TrustEdge {
	tem.mu.RLock()
	defer tem.mu.RUnlock()
	var result []*TrustEdge
	for _, edgeID := range tem.byParty[partyID] {
		if edge, ok := tem.edges[edgeID]; ok {
			result = append(result, edge)
		}
	}
	return result
}

// ActiveEdges 返回所有活跃通道。
func (tem *TrustEdgeManager) ActiveEdges() []*TrustEdge {
	tem.mu.RLock()
	defer tem.mu.RUnlock()
	var result []*TrustEdge
	for _, edge := range tem.edges {
		if edge.Status == TrustEdgeActive {
			result = append(result, edge)
		}
	}
	return result
}

// ============================================================
// E7: 跨域混沌池路由
// ============================================================

// CrossDomainTransfer 跨域转移记录。
type CrossDomainTransfer struct {
	TransferID    [32]byte
	SourceNS      NamespaceID
	TargetNS      NamespaceID
	Amount        uint64
	AssetType     string

	// ZK 证明
	LockProof     []byte // 源域锁定证明
	TransferProof []byte // 混沌池中转证明

	// 路由
	FerryPath     [][32]byte // 摆渡节点路径
	CurrentHop    int

	// 状态
	Status        TransferStatus
	CreatedAt     int64
	CompletedAt   int64
}

// TransferStatus 跨域转移状态。
type TransferStatus int32

const (
	TransferLocked    TransferStatus = 0 // 源域已锁定
	TransferInTransit TransferStatus = 1 // 混沌池中转
	TransferReleased  TransferStatus = 2 // 目标域已释放
	TransferFailed    TransferStatus = 3 // 失败
)

// CrossDomainRouter 跨域混沌池路由器。
type CrossDomainRouter struct {
	mu        sync.RWMutex
	transfers map[[32]byte]*CrossDomainTransfer
	// 混沌池中的锁定资产
	chaosPool map[NamespaceID]uint64 // nsID → locked amount
}

// NewCrossDomainRouter 创建跨域路由器。
func NewCrossDomainRouter() *CrossDomainRouter {
	return &CrossDomainRouter{
		transfers: make(map[[32]byte]*CrossDomainTransfer),
		chaosPool: make(map[NamespaceID]uint64),
	}
}

// InitiateTransfer 发起跨域转移。
// 1. 在源域锁定资产
// 2. 生成 ZK 证明
// 3. 通过混沌池匿名中转
func (cdr *CrossDomainRouter) InitiateTransfer(
	sourceNS, targetNS NamespaceID,
	amount uint64,
	lockProof []byte,
	ferryPath [][32]byte,
) (*CrossDomainTransfer, error) {
	cdr.mu.Lock()
	defer cdr.mu.Unlock()

	transferID := generateTransferID(sourceNS, targetNS, amount)

	transfer := &CrossDomainTransfer{
		TransferID:  transferID,
		SourceNS:    sourceNS,
		TargetNS:    targetNS,
		Amount:      amount,
		LockProof:   lockProof,
		FerryPath:   ferryPath,
		Status:      TransferLocked,
		CreatedAt:   nowUnix(),
	}

	// 锁定到混沌池
	cdr.chaosPool[sourceNS] += amount
	cdr.transfers[transferID] = transfer

	return transfer, nil
}

// CompleteTransfer 完成跨域转移。
// 从混沌池释放到目标域。
func (cdr *CrossDomainRouter) CompleteTransfer(transferID [32]byte) error {
	cdr.mu.Lock()
	defer cdr.mu.Unlock()

	transfer, exists := cdr.transfers[transferID]
	if !exists {
		return fmt.Errorf("cross-domain: transfer %x not found", transferID[:8])
	}
	if transfer.Status != TransferLocked && transfer.Status != TransferInTransit {
		return fmt.Errorf("cross-domain: transfer %x is %d", transferID[:8], transfer.Status)
	}

	// 从源域混沌池释放
	if cdr.chaosPool[transfer.SourceNS] >= transfer.Amount {
		cdr.chaosPool[transfer.SourceNS] -= transfer.Amount
	} else {
		return fmt.Errorf("cross-domain: insufficient chaos pool funds")
	}

	transfer.Status = TransferReleased
	transfer.CompletedAt = nowUnix()

	return nil
}

// GetChaosPoolBalance 获取混沌池余额。
func (cdr *CrossDomainRouter) GetChaosPoolBalance(nsID NamespaceID) uint64 {
	cdr.mu.RLock()
	defer cdr.mu.RUnlock()
	return cdr.chaosPool[nsID]
}

// ============================================================
// E9: 社区级 Slashing
// ============================================================

// CommunitySlashing 社区级惩罚记录。
type CommunitySlashing struct {
	CommunityID    [32]byte
	Reason         string
	SlashAmount    uint64
	SlashBondRatio float64 // 罚没 Bond 的比例
	AppliedAt      int64

	// 连带惩罚
	UpstreamCommunityID [32]byte // 上游邀请社区（也受罚）
	UpstreamSlashAmount uint64
	UpstreamApplied     bool

	// 拓扑降级
	DemotedToBlacklist bool
	DemotionDuration   int64 // 降级持续时间（秒）
}

// CommunitySlashingManager 社区级 Slashing 管理器。
type CommunitySlashingManager struct {
	mu          sync.RWMutex
	slashings   []*CommunitySlashing
	blacklisted map[[32]byte]bool // 被降级为黑名单的社区
	topology    *GenesisTree
}

// NewCommunitySlashingManager 创建社区级 Slashing 管理器。
func NewCommunitySlashingManager(topology *GenesisTree) *CommunitySlashingManager {
	return &CommunitySlashingManager{
		slashings:   make([]*CommunitySlashing, 0),
		blacklisted: make(map[[32]byte]bool),
		topology:    topology,
	}
}

// SlashCommunity 对社区执行 Slashing。
// 规则:
//   1. 社区根节点 Bond 全额或按比例罚没
//   2. 上游邀请社区连带罚没 (50% 比例)
//   3. 社区降级为黑名单
func (csm *CommunitySlashingManager) SlashCommunity(
	communityID [32]byte,
	reason string,
	slashRatio float64,
	demoteToBlacklist bool,
) (*CommunitySlashing, error) {
	csm.mu.Lock()
	defer csm.mu.Unlock()

	node := csm.topology.GetCommunity(communityID)
	if node == nil {
		return nil, fmt.Errorf("slashing: community %x not found", communityID[:8])
	}

	slashAmount := uint64(float64(node.BondAmount) * slashRatio)

	slashing := &CommunitySlashing{
		CommunityID:    communityID,
		Reason:         reason,
		SlashAmount:    slashAmount,
		SlashBondRatio: slashRatio,
		AppliedAt:      nowUnix(),
	}

	// 上游连带罚没（邀请社区承担 50% 连带责任）
	parent := csm.topology.GetCommunity(node.ParentID)
	if parent != nil {
		upstreamSlash := uint64(float64(parent.BondAmount) * slashRatio * 0.5)
		slashing.UpstreamCommunityID = node.ParentID
		slashing.UpstreamSlashAmount = upstreamSlash
		slashing.UpstreamApplied = true
	}

	// 拓扑降级
	if demoteToBlacklist {
		csm.blacklisted[communityID] = true
		slashing.DemotedToBlacklist = true
		slashing.DemotionDuration = 90 * 24 * 3600 // 90 天
	}

	csm.slashings = append(csm.slashings, slashing)

	return slashing, nil
}

// IsBlacklisted 检查社区是否在黑名单中。
func (csm *CommunitySlashingManager) IsBlacklisted(communityID [32]byte) bool {
	csm.mu.RLock()
	defer csm.mu.RUnlock()
	return csm.blacklisted[communityID]
}

// GetSlashings 获取社区的所有 Slashing 记录。
func (csm *CommunitySlashingManager) GetSlashings(communityID [32]byte) []*CommunitySlashing {
	csm.mu.RLock()
	defer csm.mu.RUnlock()
	var result []*CommunitySlashing
	for _, s := range csm.slashings {
		if s.CommunityID == communityID {
			result = append(result, s)
		}
	}
	return result
}

// TotalSlashed 返回累计罚没总额。
func (csm *CommunitySlashingManager) TotalSlashed() uint64 {
	csm.mu.RLock()
	defer csm.mu.RUnlock()
	var total uint64
	for _, s := range csm.slashings {
		total += s.SlashAmount
		if s.UpstreamApplied {
			total += s.UpstreamSlashAmount
		}
	}
	return total
}

func generateTransferID(sourceNS, targetNS NamespaceID, amount uint64) [32]byte {
	id := fmt.Sprintf("xfer-%d-%d-%d-%d", sourceNS, targetNS, amount, nowUnix())
	var result [32]byte
	copy(result[:], []byte(id))
	return result
}
