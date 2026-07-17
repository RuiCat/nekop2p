//go:build cosmos

// Package vrg 虚拟根网图子系统（Cosmos SDK 适配层, Phase 7+）。
//
// VRG 子系统在 Cosmos 模式下提供以下桩类型，供 app_v2.go 引用：
//   - NamespaceID / NamespaceRouter
//   - TrustEdgeManager / CrossDomainRouter
//   - 完整实现推迟到 Phase 7 (CometBFT 集成后)
//
// Package vrg 提供 VRG 类型桩。
package vrg

// NamespaceID 命名空间标识符。
type NamespaceID string

// NamespaceRouter 命名空间路由器。
type NamespaceRouter struct {
	Namespaces map[NamespaceID]*NamespaceConfig
}

// NamespaceConfig 命名空间配置。
type NamespaceConfig struct {
	ID       NamespaceID
	Isolated bool
}

// TrustEdgeManager 信任边管理器。
type TrustEdgeManager struct {
	Edges map[NamespaceID][]NamespaceID
}

// CrossDomainRouter 跨域路由器。
type CrossDomainRouter struct {
	Routes map[NamespaceID]map[NamespaceID]bool
}

// CommunitySlashingManager 社区罚没管理器。
type CommunitySlashingManager struct {
	SlashedCommunities map[NamespaceID]bool
}

// EpochCounter 纪元计数器。
type EpochCounter struct {
	Current int64
}

// DoubleSpendDetector 双花检测器。
type DoubleSpendDetector struct {
	Collisions map[string]bool
}

// ShadowReceipt 影子收据（跨域交易凭证）。
type ShadowReceipt struct {
	ReceiptID     string
	SourceNS      NamespaceID
	TargetNS      NamespaceID
	Amount        uint64
	LockTxHash    []byte
	Status        int32
}
