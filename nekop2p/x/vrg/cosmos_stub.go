//go:build cosmos

// Package vrg 虚拟根网图子系统（Cosmos SDK 适配层, Phase 7）。
//
// Cosmos 模式下提供 VRG 核心类型定义。
// 完整状态机逻辑在非 Cosmos 模式中实现，此处为类型桥接层。
//
// Package vrg 提供 VRG Cosmos 类型。
package vrg

import "sync"

// NamespaceID 命名空间标识符。
type NamespaceID string

// NamespaceRouter 命名空间路由器。
type NamespaceRouter struct {
	mu         sync.RWMutex
	Namespaces map[NamespaceID]*NamespaceConfig
}

// NamespaceConfig 命名空间配置。
type NamespaceConfig struct {
	ID       NamespaceID `json:"id"`
	Isolated bool        `json:"isolated"`
}

// NewNamespaceRouter 创建命名空间路由器。
func NewNamespaceRouter() *NamespaceRouter {
	return &NamespaceRouter{Namespaces: make(map[NamespaceID]*NamespaceConfig)}
}

// Register 注册命名空间。
func (r *NamespaceRouter) Register(id NamespaceID, isolated bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.Namespaces[id] = &NamespaceConfig{ID: id, Isolated: isolated}
}

// TrustEdgeManager 信任边管理器。
type TrustEdgeManager struct {
	mu    sync.RWMutex
	Edges map[NamespaceID][]NamespaceID
}

// NewTrustEdgeManager 创建信任边管理器。
func NewTrustEdgeManager() *TrustEdgeManager {
	return &TrustEdgeManager{Edges: make(map[NamespaceID][]NamespaceID)}
}

// AddEdge 添加信任边。
func (m *TrustEdgeManager) AddEdge(from, to NamespaceID) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.Edges[from] = append(m.Edges[from], to)
}

// CrossDomainRouter 跨域路由器。
type CrossDomainRouter struct {
	mu     sync.RWMutex
	Routes map[NamespaceID]map[NamespaceID]bool
}

// NewCrossDomainRouter 创建跨域路由器。
func NewCrossDomainRouter() *CrossDomainRouter {
	return &CrossDomainRouter{Routes: make(map[NamespaceID]map[NamespaceID]bool)}
}

// AddRoute 添加跨域路由。
func (r *CrossDomainRouter) AddRoute(from, to NamespaceID) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.Routes[from] == nil {
		r.Routes[from] = make(map[NamespaceID]bool)
	}
	r.Routes[from][to] = true
}

// CommunitySlashingManager 社区罚没管理器。
type CommunitySlashingManager struct {
	mu                  sync.RWMutex
	SlashedCommunities  map[NamespaceID]bool
	SlashHistory        []SlashRecord
}

// SlashRecord 罚没记录。
type SlashRecord struct {
	Community NamespaceID `json:"community"`
	Amount    uint64      `json:"amount"`
	Reason    string      `json:"reason"`
	Epoch     int64       `json:"epoch"`
}

// NewCommunitySlashingManager 创建罚没管理器。
func NewCommunitySlashingManager() *CommunitySlashingManager {
	return &CommunitySlashingManager{
		SlashedCommunities: make(map[NamespaceID]bool),
	}
}

// Slash 执行罚没。
func (m *CommunitySlashingManager) Slash(community NamespaceID, amount uint64, reason string, epoch int64) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.SlashedCommunities[community] = true
	m.SlashHistory = append(m.SlashHistory, SlashRecord{community, amount, reason, epoch})
}

// EpochCounter 纪元计数器。
type EpochCounter struct {
	mu      sync.Mutex
	Current int64
}

// NewEpochCounter 创建纪元计数器。
func NewEpochCounter() *EpochCounter { return &EpochCounter{} }

// Increment 递增纪元。
func (c *EpochCounter) Increment() int64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.Current++
	return c.Current
}

// DoubleSpendDetector 双花检测器。
type DoubleSpendDetector struct {
	mu          sync.RWMutex
	Collisions  map[string]bool
}

// NewDoubleSpendDetector 创建双花检测器。
func NewDoubleSpendDetector() *DoubleSpendDetector {
	return &DoubleSpendDetector{Collisions: make(map[string]bool)}
}

// Detect 记录冲突。
func (d *DoubleSpendDetector) Detect(txID string) bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.Collisions[txID] {
		return true
	}
	d.Collisions[txID] = true
	return false
}

// ShadowReceipt 影子收据（跨域交易凭证）。
type ShadowReceipt struct {
	ReceiptID  string      `json:"receipt_id"`
	SourceNS   NamespaceID `json:"source_ns"`
	TargetNS   NamespaceID `json:"target_ns"`
	Amount     uint64      `json:"amount"`
	LockTxHash []byte      `json:"lock_tx_hash"`
	Status     int32       `json:"status"`
}
