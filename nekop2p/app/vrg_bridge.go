//go:build !cosmos

// Package app Namespace ABCI 接入 + 跨链资产转移。
//
// E5: 将 VRG NamespaceRouter 接入链执行流程。
// C2: 基于 VRG 影子收据的跨域资产转移协议（简化 IBC）。
package app

import (
	"fmt"
	"log"

	"github.com/nekop2p/nekop2p/x/vrg"
)

// ============================================================
// E5: Namespace ABCI 接入
// ============================================================

// VRGState 虚拟根网图运行时状态（整合到 NekoApp）。
type VRGState struct {
	NamespaceRouter     *vrg.NamespaceRouter
	EdgeManager         *vrg.TrustEdgeManager
	CrossDomainRouter   *vrg.CrossDomainRouter
	SlashingManager     *vrg.CommunitySlashingManager
	EpochCounter        *vrg.EpochCounter
	DoubleSpendDetector *vrg.DoubleSpendDetector
}

// InitVRG 初始化虚拟根网图子系统。
func (app *NekoApp) InitVRG() {
	if app.VRG != nil {
		return // 已初始化
	}

	app.VRG = &VRGState{
		NamespaceRouter:     vrg.NewNamespaceRouter(),
		EdgeManager:         vrg.NewTrustEdgeManager(),
		CrossDomainRouter:   vrg.NewCrossDomainRouter(),
		EpochCounter:        vrg.NewEpochCounter(14400),
		DoubleSpendDetector: vrg.NewDoubleSpendDetector(),
		// SlashingManager 在拓扑树建立后初始化
	}

	log.Printf("[vrg] initialized: epoch=%d",
		app.VRG.EpochCounter.CurrentEpoch())
}

// RouteByNamespace 按命名空间路由交易。
// 返回目标社区 ID 用于跨域转移。
func (app *NekoApp) RouteByNamespace(nsID vrg.NamespaceID) (*vrg.Namespace, error) {
	if app.VRG == nil {
		app.InitVRG()
	}
	return app.VRG.NamespaceRouter.RouteTransaction(nsID)
}

// EpochTick 每个区块调用一次，推进纪元。
func (app *NekoApp) EpochTick(blockHeight int64) {
	if app.VRG == nil {
		return
	}
	changed, epoch := app.VRG.EpochCounter.AdvanceBlock(blockHeight)
	if changed {
		log.Printf("[vrg] epoch changed to %d at block %d", epoch, blockHeight)
	}
}

// ============================================================
// C2: 跨链资产转移 (简化 IBC)
// ============================================================

// CrossChainTransfer 跨链资产转移请求。
type CrossChainTransfer struct {
	TransferID     [32]byte
	SourceChain    string   // 源链标识
	TargetChain    string   // 目标链标识
	SourceNS       vrg.NamespaceID
	TargetNS       vrg.NamespaceID
	AssetType      string
	Amount         uint64
	SenderID       string
	ReceiverID     string
	ZKProof        []byte   // ZK 锁定证明
	ShadowReceipt  *vrg.ShadowReceipt
	Status         string   // "locked" / "in_transit" / "completed" / "failed"
}

// CrossChainBridge 跨链资产桥。
type CrossChainBridge struct {
	transfers map[[32]byte]*CrossChainTransfer
}

// NewCrossChainBridge 创建跨链桥。
func NewCrossChainBridge() *CrossChainBridge {
	return &CrossChainBridge{
		transfers: make(map[[32]byte]*CrossChainTransfer),
	}
}

// InitiateCrossChainTransfer 发起跨链资产转移。
//
// 流程:
//   1. 源链锁定资产
//   2. 生成影子收据（含 ZK 证明）
//   3. 通过外交通道验证
//   4. 摆渡节点传递收据
//   5. 目标链释放资产
func (app *NekoApp) InitiateCrossChainTransfer(
	sourceNS, targetNS vrg.NamespaceID,
	assetType string, amount uint64,
	senderID, receiverID string,
	zkProof []byte,
) (*CrossChainTransfer, error) {
	if app.VRG == nil {
		app.InitVRG()
	}

	// 1. 检查外交通道是否活跃
	activeEdges := app.VRG.EdgeManager.ActiveEdges()
	hasEdge := false
	for _, edge := range activeEdges {
		if (edge.NamespaceA == sourceNS && edge.NamespaceB == targetNS) ||
			(edge.NamespaceA == targetNS && edge.NamespaceB == sourceNS) {
			hasEdge = true
			break
		}
	}
	if !hasEdge {
		return nil, fmt.Errorf("cross-chain: no active trust edge between ns %d and %d", sourceNS, targetNS)
	}

	// 2. 在源域混沌池锁定资产
	transfer, err := app.VRG.CrossDomainRouter.InitiateTransfer(
		sourceNS, targetNS, amount, zkProof, nil,
	)
	if err != nil {
		return nil, fmt.Errorf("cross-chain: initiate transfer: %w", err)
	}

	// 3. 记录跨链转移
	xfer := &CrossChainTransfer{
		TransferID: transfer.TransferID,
		SourceChain: fmt.Sprintf("ns-%d", sourceNS),
		TargetChain: fmt.Sprintf("ns-%d", targetNS),
		SourceNS:    sourceNS,
		TargetNS:    targetNS,
		AssetType:   assetType,
		Amount:      amount,
		SenderID:    senderID,
		ReceiverID:  receiverID,
		ZKProof:     zkProof,
		Status:      "locked",
	}

	log.Printf("[cross-chain] initiated transfer %x: %d %s from ns-%d to ns-%d",
		xfer.TransferID[:8], amount, assetType, sourceNS, targetNS)

	return xfer, nil
}

// CompleteCrossChainTransfer 完成跨链资产转移。
func (app *NekoApp) CompleteCrossChainTransfer(transferID [32]byte) error {
	if app.VRG == nil {
		return fmt.Errorf("vrg not initialized")
	}

	if err := app.VRG.CrossDomainRouter.CompleteTransfer(transferID); err != nil {
		return fmt.Errorf("cross-chain: complete transfer: %w", err)
	}

	log.Printf("[cross-chain] completed transfer %x", transferID[:8])
	return nil
}

// ============================================================
// NekoApp 集成
// ============================================================

// NekoApp 添加 VRG 字段（在 app.go 的 NekoApp 结构体中添加）
// VRG *VRGState
