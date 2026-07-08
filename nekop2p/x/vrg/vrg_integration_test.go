// Package vrg_test VRG 持久化与端到端场景测试。
package vrg_test

import (
	"os"
	"testing"

	"github.com/nekop2p/nekop2p/crypto"
	"github.com/nekop2p/nekop2p/store"
	"github.com/nekop2p/nekop2p/x/vrg"
)

// ============================================================
// VRG 持久化测试
// ============================================================

func newTestStore(t *testing.T) (*store.ChainStore, func()) {
	t.Helper()
	tmpDir, err := os.MkdirTemp("", "vrg-test-*")
	if err != nil {
		t.Fatalf("create temp dir: %v", err)
	}
	cs, err := store.New(tmpDir)
	if err != nil {
		os.RemoveAll(tmpDir)
		t.Fatalf("create store: %v", err)
	}
	cleanup := func() {
		cs.Close()
		os.RemoveAll(tmpDir)
	}
	return cs, cleanup
}

func TestVRGStoreGenesisTreeRoundtrip(t *testing.T) {
	cs, cleanup := newTestStore(t)
	defer cleanup()

	vs, err := vrg.NewVRGStore(cs)
	if err != nil {
		t.Fatalf("new vrg store: %v", err)
	}

	// 创建并填充创世树
	rootID := [32]byte{0x00}
	tree := vrg.NewGenesisTree(rootID)
	tree.GetCommunity(rootID).BondAmount = 100000
	a := [32]byte{0x01}
	b := [32]byte{0x02}
	tree.AddCommunity(a, rootID, 5000, "alice", 100)
	tree.AddCommunity(b, a, 2000, "bob", 200)

	// 保存
	if err := vs.SaveGenesisTree(tree); err != nil {
		t.Fatalf("save genesis tree: %v", err)
	}

	// 加载
	loaded, err := vs.LoadGenesisTree()
	if err != nil {
		t.Fatalf("load genesis tree: %v", err)
	}

	if loaded.TotalCommunities() != 3 {
		t.Errorf("communities: got %d, want 3", loaded.TotalCommunities())
	}
	if loaded.MaxDepth() != 2 {
		t.Errorf("max depth: got %d, want 2", loaded.MaxDepth())
	}

	// 验证 LCA
	lca, _ := loaded.LCA(a, b)
	if lca != a {
		t.Errorf("LCA(a,b) should be a, got %x", lca[:4])
	}
}

func TestVRGStoreEpochRoundtrip(t *testing.T) {
	cs, cleanup := newTestStore(t)
	defer cleanup()

	vs, _ := vrg.NewVRGStore(cs)

	ec := vrg.NewEpochCounter(100)
	for i := 0; i < 555; i++ {
		ec.AdvanceBlock(int64(i))
	}
	ec.MergeEpoch(10, 600)

	// 保存
	if err := vs.SaveEpoch(ec); err != nil {
		t.Fatalf("save epoch: %v", err)
	}

	// 加载
	loaded, err := vs.LoadEpoch(100)
	if err != nil {
		t.Fatalf("load epoch: %v", err)
	}

	if loaded.CurrentEpoch() != ec.CurrentEpoch() {
		t.Errorf("epoch: got %d, want %d", loaded.CurrentEpoch(), ec.CurrentEpoch())
	}
	if loaded.IsolationCount() != ec.IsolationCount() {
		t.Errorf("isolation: got %d, want %d", loaded.IsolationCount(), ec.IsolationCount())
	}
}

func TestVRGStoreTrustEdgeRoundtrip(t *testing.T) {
	cs, cleanup := newTestStore(t)
	defer cleanup()

	vs, _ := vrg.NewVRGStore(cs)

	tem := vrg.NewTrustEdgeManager()
	edge, _ := tem.ProposeEdge([32]byte{0xAA}, [32]byte{0xBB}, 1, 2, 500, 600)
	tem.ActivateEdge(edge.EdgeID)

	// 保存
	if err := vs.SaveTrustEdge(edge); err != nil {
		t.Fatalf("save edge: %v", err)
	}

	// 加载
	edges, err := vs.LoadAllTrustEdges()
	if err != nil {
		t.Fatalf("load edges: %v", err)
	}
	if len(edges) != 1 {
		t.Fatalf("edges count: got %d, want 1", len(edges))
	}
	if edges[0].Status != vrg.TrustEdgeActive {
		t.Errorf("edge status: got %s, want ACTIVE", edges[0].Status)
	}
	if edges[0].TotalBond != 1100 {
		t.Errorf("total bond: got %d, want 1100", edges[0].TotalBond)
	}
}

func TestVRGStoreCollisionRoundtrip(t *testing.T) {
	cs, cleanup := newTestStore(t)
	defer cleanup()

	vs, _ := vrg.NewVRGStore(cs)

	dsd := vrg.NewDoubleSpendDetector()
	collision, _ := dsd.RegisterReceipt("asset-test", 5, [32]byte{0x01}, [32]byte{0xAA})
	_ = collision // 第一次无冲突
	collision, _ = dsd.RegisterReceipt("asset-test", 5, [32]byte{0x02}, [32]byte{0xBB})

	// 保存
	if err := vs.SaveCollision(collision); err != nil {
		t.Fatalf("save collision: %v", err)
	}

	// 加载
	events, err := vs.LoadAllCollisions()
	if err != nil {
		t.Fatalf("load collisions: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("collisions count: got %d, want 1", len(events))
	}
	if events[0].AssetKey != "asset-test" {
		t.Errorf("asset key: got %s", events[0].AssetKey)
	}
}

// ============================================================
// 端到端场景测试: 完整跨社区交易流程
// ============================================================

func TestEndToEndCrossCommunityFlow(t *testing.T) {
	// === 场景: 两份孤立社区通过虚拟根网图完成跨域清算 ===

	// 1. 创建创世树 (模拟真实邀请链)
	rootID := [32]byte{0x00}
	tree := vrg.NewGenesisTree(rootID)
	tree.GetCommunity(rootID).BondAmount = 100000

	genesis := [32]byte{0x01} // 创世社区
	commA := [32]byte{0x0A}   // 社区 A (Alice 所在)
	commB := [32]byte{0x0B}   // 社区 B (Bob 所在)
	commC := [32]byte{0x0C}   // 社区 C (由 A 分裂)

	tree.AddCommunity(genesis, rootID, 50000, "genesis_member", 0)
	tree.AddCommunity(commA, genesis, 20000, "alice", 100)
	tree.AddCommunity(commB, commA, 10000, "bob", 200)
	tree.AddCommunity(commC, commA, 8000, "charlie", 150)

	// 2. 创建社区虚拟化身
	keysA := make([][32]byte, 3)
	keysB := make([][32]byte, 3)
	for i := range keysA {
		keysA[i] = [32]byte{0x0A, byte(i)}
		keysB[i] = [32]byte{0x0B, byte(i)}
	}
	avatarA := crypto.NewCommunityAvatar(keysA, []byte("chain-A"), 100)
	avatarB := crypto.NewCommunityAvatar(keysB, []byte("chain-B"), 200)

	_ = avatarA
	_ = avatarB

	// 3. 创建命名空间
	nr := vrg.NewNamespaceRouter()
	nsA, _ := nr.RegisterNamespace("CommunityA-App", commA)
	nsB, _ := nr.RegisterNamespace("CommunityB-App", commB)

	// 4. 建立外交通道
	tem := vrg.NewTrustEdgeManager()
	edge, err := tem.ProposeEdge(commA, commB, nsA.ID, nsB.ID, 5000, 5000)
	if err != nil {
		t.Fatalf("propose edge: %v", err)
	}
	if err := tem.ActivateEdge(edge.EdgeID); err != nil {
		t.Fatalf("activate edge: %v", err)
	}

	// 5. 计算拓扑安全系数
	d, _ := tree.Distance(commA, commB)
	s, _ := tree.SecurityCoefficient(commA, commB)
	threshold := vrg.DefaultTrustThreshold()
	canClear, extra, _ := tree.CanClearMacro(commA, commB, threshold)

	t.Logf("Topology: d(A,B)=%d S=%.1f canClear=%v extraBond=%d", d, s, canClear, extra)

	// 6. 纪元管理
	ec := vrg.NewEpochCounter(100)
	for i := 0; i < 300; i++ {
		ec.AdvanceBlock(int64(i))
	}
	epoch := ec.CurrentEpoch()

	// 7. 跨域转移 (模拟)
	cdr := vrg.NewCrossDomainRouter()
	transfer, err := cdr.InitiateTransfer(nsA.ID, nsB.ID, 1000, []byte("zk-lock-proof"), nil)
	if err != nil {
		t.Fatalf("initiate transfer: %v", err)
	}
	_ = transfer

	// 8. 双花检测
	dsd := vrg.NewDoubleSpendDetector()
	receiptA := [32]byte{0xAA}
	receiptB := [32]byte{0xBB}
	_, err = dsd.RegisterReceipt("xfer-001", epoch, receiptA, commA)
	if err != nil {
		t.Fatalf("register receipt A: %v", err)
	}
	collision, err := dsd.RegisterReceipt("xfer-001", epoch, receiptB, commA)
	if err == nil {
		t.Fatal("expected double spend collision")
	}

	// 9. 社区 Slashing (双花处罚)
	csm := vrg.NewCommunitySlashingManager(tree)
	slashing, err := csm.SlashCommunity(commA, "double_spend_xfer-001", 0.40, true)
	if err != nil {
		t.Fatalf("slash: %v", err)
	}

	// 10. 验证结果
	t.Logf("=== End-to-End VRG Scenario Results ===")
	t.Logf("  Communities:     %d (depth=%d)", tree.TotalCommunities(), tree.MaxDepth())
	t.Logf("  Active Edges:    %d", len(tem.ActiveEdges()))
	t.Logf("  Topology:        d=%d S=%.1f", d, s)
	t.Logf("  Epoch:           %d (isolation=%d)", epoch, ec.IsolationCount())
	t.Logf("  Namespaces:      %d", 2)
	t.Logf("  Chaos Pool(A):   %d", cdr.GetChaosPoolBalance(nsA.ID))
	t.Logf("  Collisions:      %d", dsd.TotalCollisions())
	t.Logf("  Slashed:         commA=%d upstream=%d blacklisted=%v",
		slashing.SlashAmount, slashing.UpstreamSlashAmount, slashing.DemotedToBlacklist)

	// 断言
	if tree.TotalCommunities() != 5 {
		t.Errorf("expected 5 communities, got %d", tree.TotalCommunities())
	}
	if len(tem.ActiveEdges()) != 1 {
		t.Errorf("expected 1 active edge, got %d", len(tem.ActiveEdges()))
	}
	if dsd.TotalCollisions() != 1 {
		t.Errorf("expected 1 collision, got %d", dsd.TotalCollisions())
	}
	if !csm.IsBlacklisted(commA) {
		t.Error("commA should be blacklisted after double spend")
	}
	if collision == nil {
		t.Error("collision event should not be nil")
	}
}

// ============================================================
// 摆渡节点端到端测试
// ============================================================

func TestFerryNodeEndToEnd(t *testing.T) {
	// 创建摆渡节点
	nodeID := [32]byte{0xFE, 0x01}
	communityID := [32]byte{0xC0, 0x01}
	fn, err := vrg.NewFerryNodeFull(nodeID, communityID, "/tmp/ferry-e2e")
	if err != nil {
		t.Fatalf("new ferry: %v", err)
	}

	// 创建测试收据
	sourceComm := [32]byte{0xAA}
	targetComm := [32]byte{0xBB}
	receipt := vrg.NewShadowReceipt(
		sourceComm, targetComm,
		"credit", 5000,
		[]byte("zk-asset-proof"),
		10,
		nil,
	)

	// 标记为待发
	if receipt.Status != vrg.ReceiptPendingOutbound {
		t.Fatal("new receipt should be PENDING_OUTBOUND")
	}

	// 摆渡节点拾取
	if err := fn.PickupReceipt(receipt); err != nil {
		t.Fatalf("pickup: %v", err)
	}
	if receipt.Status != vrg.ReceiptInTransit {
		t.Error("receipt should be IN_TRANSIT after pickup")
	}

	// 存储到本地
	if err := fn.StoreReceipt(receipt); err != nil {
		t.Fatalf("store: %v", err)
	}

	// 加载验证
	loaded, err := fn.LoadReceipt(receipt.ReceiptID)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if loaded.AssetAmount != 5000 {
		t.Errorf("amount: got %d", loaded.AssetAmount)
	}

	// 投递到目标社区
	accepted, rejected, err := fn.InjectReceipts(targetComm, nil)
	if err != nil {
		t.Fatalf("inject: %v", err)
	}
	t.Logf("injection: accepted=%d rejected=%d", accepted, rejected)

	// 清理已投递
	purged := fn.PurgeDelivered()
	t.Logf("purged: %d receipts", purged)

	// 工资计算
	cfg := vrg.DefaultFerryWageConfig()
	wages := fn.CalculateWages(cfg)
	t.Logf("ferry wages: %d", wages)
}
