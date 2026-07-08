// Package vrg_test 虚拟根网图集成测试。
package vrg_test

import (
	"testing"

	"github.com/nekop2p/nekop2p/crypto"
	"github.com/nekop2p/nekop2p/x/vrg"
)

// ============================================================
// E4: 拓扑计算测试
// ============================================================

func TestGenesisTreeBasic(t *testing.T) {
	rootID := [32]byte{0x00, 0x01}
	tree := vrg.NewGenesisTree(rootID)

	if tree.TotalCommunities() != 1 {
		t.Fatalf("root tree should have 1 community, got %d", tree.TotalCommunities())
	}
	if tree.MaxDepth() != 0 {
		t.Fatalf("root tree max depth should be 0, got %d", tree.MaxDepth())
	}

	root := tree.GetCommunity(rootID)
	if root == nil {
		t.Fatal("root community should not be nil")
	}
	if root.Depth != 0 {
		t.Fatalf("root depth should be 0, got %d", root.Depth)
	}
}

func TestGenesisTreeAddCommunity(t *testing.T) {
	rootID := [32]byte{0x00, 0x01}
	tree := vrg.NewGenesisTree(rootID)

	childA := [32]byte{0x10, 0x01}
	childB := [32]byte{0x10, 0x02}
	grandChild := [32]byte{0x20, 0x01}

	// 添加子社区
	err := tree.AddCommunity(childA, rootID, 1000, "inviter_a", 100)
	if err != nil {
		t.Fatalf("add child A: %v", err)
	}
	err = tree.AddCommunity(childB, rootID, 2000, "inviter_b", 200)
	if err != nil {
		t.Fatalf("add child B: %v", err)
	}
	err = tree.AddCommunity(grandChild, childA, 500, "inviter_c", 300)
	if err != nil {
		t.Fatalf("add grandchild: %v", err)
	}

	if tree.TotalCommunities() != 4 {
		t.Fatalf("expected 4 communities, got %d", tree.TotalCommunities())
	}
	if tree.MaxDepth() != 2 {
		t.Fatalf("max depth should be 2, got %d", tree.MaxDepth())
	}

	// 验证深度
	gc := tree.GetCommunity(grandChild)
	if gc.Depth != 2 {
		t.Fatalf("grandchild depth should be 2, got %d", gc.Depth)
	}
}

func TestLCA(t *testing.T) {
	rootID := [32]byte{0x00}
	tree := vrg.NewGenesisTree(rootID)

	a := [32]byte{0x01}
	b := [32]byte{0x02}
	a1 := [32]byte{0x11}
	a2 := [32]byte{0x12}
	b1 := [32]byte{0x21}

	tree.AddCommunity(a, rootID, 1000, "x", 1)
	tree.AddCommunity(b, rootID, 1000, "x", 1)
	tree.AddCommunity(a1, a, 500, "x", 2)
	tree.AddCommunity(a2, a, 500, "x", 2)
	tree.AddCommunity(b1, b, 500, "x", 2)

	// LCA(a1, a2) = a
	lca, err := tree.LCA(a1, a2)
	if err != nil {
		t.Fatalf("LCA(a1,a2): %v", err)
	}
	if lca != a {
		t.Errorf("LCA(a1,a2) should be a, got %x", lca[:4])
	}

	// LCA(a1, b1) = root
	lca, err = tree.LCA(a1, b1)
	if err != nil {
		t.Fatalf("LCA(a1,b1): %v", err)
	}
	if lca != rootID {
		t.Errorf("LCA(a1,b1) should be root, got %x", lca[:4])
	}
}

func TestDistance(t *testing.T) {
	rootID := [32]byte{0x00}
	tree := vrg.NewGenesisTree(rootID)

	a := [32]byte{0x01}
	b := [32]byte{0x02}
	a1 := [32]byte{0x11}
	b1 := [32]byte{0x21}

	tree.AddCommunity(a, rootID, 1000, "x", 1)
	tree.AddCommunity(b, rootID, 1000, "x", 1)
	tree.AddCommunity(a1, a, 500, "x", 2)
	tree.AddCommunity(b1, b, 500, "x", 2)

	// d(a1, a) = depth(a1)+depth(a)-2*depth(LCA) = 2+1-2*1 = 1
	d, err := tree.Distance(a1, a)
	if err != nil {
		t.Fatalf("distance: %v", err)
	}
	if d != 1 {
		t.Errorf("d(a1,a) should be 1, got %d", d)
	}

	// d(a1, b1) = 2+2-2*0 = 4
	d, err = tree.Distance(a1, b1)
	if err != nil {
		t.Fatalf("distance: %v", err)
	}
	if d != 4 {
		t.Errorf("d(a1,b1) should be 4, got %d", d)
	}
}

func TestSecurityCoefficient(t *testing.T) {
	rootID := [32]byte{0x00}
	tree := vrg.NewGenesisTree(rootID)

	a := [32]byte{0x01}
	b := [32]byte{0x02}
	tree.AddCommunity(a, rootID, 10000, "x", 1)
	tree.AddCommunity(b, rootID, 1000, "x", 1)

	s, err := tree.SecurityCoefficient(a, b)
	if err != nil {
		t.Fatalf("security coeff: %v", err)
	}
	// d(a,b) = 2, minBond = 1000, S = 1000/(1+2)² = 1000/9 ≈ 111.1
	if s < 100 || s > 120 {
		t.Errorf("S(a,b) ≈ 111, got %.1f", s)
	}
}

func TestCanClearMacro(t *testing.T) {
	rootID := [32]byte{0x00}
	tree := vrg.NewGenesisTree(rootID)

	a := [32]byte{0x01}
	b := [32]byte{0x02}
	c := [32]byte{0x03}
	d := [32]byte{0x04}
	e := [32]byte{0x05}

	tree.AddCommunity(a, rootID, 10000, "x", 1)
	tree.AddCommunity(b, a, 5000, "x", 2)
	tree.AddCommunity(c, b, 2500, "x", 3)
	tree.AddCommunity(d, c, 1000, "x", 4)
	tree.AddCommunity(e, a, 5000, "x", 2)

	threshold := vrg.DefaultTrustThreshold()

	// 近距离清算
	can, extra, err := tree.CanClearMacro(b, e, threshold)
	if err != nil {
		t.Fatalf("can clear: %v", err)
	}
	if !can {
		t.Error("close communities should be able to clear")
	}
	if extra != 0 {
		t.Errorf("close communities should not need extra bond, got %d", extra)
	}

	// 远距离清算
	can, extra, err = tree.CanClearMacro(a, d, threshold)
	if err != nil {
		t.Fatalf("can clear: %v", err)
	}
	t.Logf("a↔d: can=%v extra=%d (distance should be 3)", can, extra)
}

func TestAncestorChain(t *testing.T) {
	rootID := [32]byte{0x00}
	tree := vrg.NewGenesisTree(rootID)

	a := [32]byte{0x01}
	b := [32]byte{0x02}
	c := [32]byte{0x03}

	tree.AddCommunity(a, rootID, 1000, "x", 1)
	tree.AddCommunity(b, a, 500, "x", 2)
	tree.AddCommunity(c, b, 200, "x", 3)

	chain := tree.AncestorChain(c)
	if len(chain) != 4 {
		t.Fatalf("ancestor chain length should be 4, got %d", len(chain))
	}
	if chain[0] != rootID {
		t.Error("first in chain should be root")
	}
	if chain[3] != c {
		t.Error("last in chain should be c")
	}
}

// ============================================================
// E8: 纪元计数器测试
// ============================================================

func TestEpochCounterBasic(t *testing.T) {
	ec := vrg.NewEpochCounter(10) // 10 blocks per epoch

	if ec.CurrentEpoch() != 0 {
		t.Fatal("initial epoch should be 0")
	}

	// 推进 9 个区块 → 仍在 epoch 0
	for i := 0; i < 9; i++ {
		changed, epoch := ec.AdvanceBlock(int64(i))
		if changed {
			t.Fatalf("epoch should not change at block %d", i)
		}
		_ = epoch
	}

	// 第 10 个区块 → epoch 1
	changed, epoch := ec.AdvanceBlock(10)
	if !changed {
		t.Fatal("epoch should change at block 10")
	}
	if epoch != 1 {
		t.Fatalf("epoch should be 1, got %d", epoch)
	}
}

func TestEpochMerge(t *testing.T) {
	ec := vrg.NewEpochCounter(100)

	// 本地在 epoch 5
	for i := 0; i < 500; i++ {
		ec.AdvanceBlock(int64(i))
	}
	if ec.CurrentEpoch() != 5 {
		t.Fatalf("local epoch should be 5, got %d", ec.CurrentEpoch())
	}

	// 孤网对端在 epoch 10（更先进）
	newEpoch := ec.MergeEpoch(10, 600)
	if newEpoch != 11 {
		t.Fatalf("merged epoch should be 11 (max(5,10)+1), got %d", newEpoch)
	}
	if ec.IsolationCount() != 1 {
		t.Fatalf("isolation count should be 1, got %d", ec.IsolationCount())
	}
}

// ============================================================
// E10: 双花检测测试
// ============================================================

func TestDoubleSpendDetection(t *testing.T) {
	dsd := vrg.NewDoubleSpendDetector()

	assetKey := "loan-abc123"
	epoch := uint64(5)
	receiptA := [32]byte{0x01}
	receiptB := [32]byte{0x02}
	communityA := [32]byte{0xAA}
	communityB := [32]byte{0xBB}

	// 第一次注册 → 成功
	collision, err := dsd.RegisterReceipt(assetKey, epoch, receiptA, communityA)
	if err != nil {
		t.Fatalf("first register: %v", err)
	}
	if collision != nil {
		t.Fatal("no collision expected on first register")
	}

	// 第二次注册同一资产+Epoch → 冲突
	collision, err = dsd.RegisterReceipt(assetKey, epoch, receiptB, communityB)
	if err == nil {
		t.Fatal("expected double spend error")
	}
	if collision == nil {
		t.Fatal("collision event should not be nil")
	}
	if collision.ReceiptA != receiptA || collision.ReceiptB != receiptB {
		t.Error("collision should reference both receipts")
	}

	if dsd.TotalCollisions() != 1 {
		t.Fatalf("total collisions should be 1, got %d", dsd.TotalCollisions())
	}

	// 检查已双花
	if !dsd.IsDoubleSpent(assetKey, epoch) {
		t.Error("asset should be marked as double spent")
	}
}

func TestDoubleSpendDifferentEpoch(t *testing.T) {
	dsd := vrg.NewDoubleSpendDetector()

	assetKey := "loan-xyz"
	community := [32]byte{0xCC}

	// 不同 Epoch → 不冲突
	_, err := dsd.RegisterReceipt(assetKey, 1, [32]byte{0x01}, community)
	if err != nil {
		t.Fatalf("epoch 1: %v", err)
	}
	_, err = dsd.RegisterReceipt(assetKey, 2, [32]byte{0x02}, community)
	if err != nil {
		t.Fatalf("epoch 2 should not conflict: %v", err)
	}

	if dsd.TotalCollisions() != 0 {
		t.Error("no collisions expected across different epochs")
	}
}

// ============================================================
// E6: 外交通道测试
// ============================================================

func TestTrustEdgeLifecycle(t *testing.T) {
	tem := vrg.NewTrustEdgeManager()

	partyA := [32]byte{0xAA}
	partyB := [32]byte{0xBB}

	// 1. 提案
	edge, err := tem.ProposeEdge(partyA, partyB, 1, 2, 500, 500)
	if err != nil {
		t.Fatalf("propose: %v", err)
	}
	if edge.Status != vrg.TrustEdgeProposed {
		t.Error("edge should be PROPOSED")
	}

	// 2. 激活
	err = tem.ActivateEdge(edge.EdgeID)
	if err != nil {
		t.Fatalf("activate: %v", err)
	}

	// 3. 冻结
	err = tem.FreezeEdge(edge.EdgeID)
	if err != nil {
		t.Fatalf("freeze: %v", err)
	}

	// 4. 切断（B 违约）
	updatedEdge, slashAmount, err := tem.SeverEdge(edge.EdgeID, partyB)
	if err != nil {
		t.Fatalf("sever: %v", err)
	}
	if slashAmount != 500 {
		t.Errorf("slash amount should be 500, got %d", slashAmount)
	}
	if updatedEdge.Status != vrg.TrustEdgeSevered {
		t.Error("edge should be SEVERED")
	}
}

func TestTrustEdgeDuplicate(t *testing.T) {
	tem := vrg.NewTrustEdgeManager()
	partyA := [32]byte{0xAA}
	partyB := [32]byte{0xBB}

	// 创建活跃通道
	edge, _ := tem.ProposeEdge(partyA, partyB, 1, 2, 100, 100)
	tem.ActivateEdge(edge.EdgeID)

	// 尝试创建重复通道 → 应失败
	_, err := tem.ProposeEdge(partyA, partyB, 1, 2, 100, 100)
	if err == nil {
		t.Error("duplicate edge should be rejected")
	}
}

// ============================================================
// E9: 社区 Slashing 测试
// ============================================================

func TestCommunitySlashing(t *testing.T) {
	rootID := [32]byte{0x00}
	tree := vrg.NewGenesisTree(rootID)
	// 设置根节点 Bond（NewGenesisTree 默认 Bond=0）
	root := tree.GetCommunity(rootID)
	root.BondAmount = 20000

	child := [32]byte{0x01}
	tree.AddCommunity(child, rootID, 10000, "inviter", 100)

	csm := vrg.NewCommunitySlashingManager(tree)

	// Slash 子社区 30%
	slashing, err := csm.SlashCommunity(child, "double_spend", 0.30, true)
	if err != nil {
		t.Fatalf("slash: %v", err)
	}

	if slashing.SlashAmount != 3000 {
		t.Errorf("slash amount should be 3000 (30%% of 10000), got %d", slashing.SlashAmount)
	}
	if !slashing.DemotedToBlacklist {
		t.Error("community should be demoted to blacklist")
	}

	// 检查黑名单
	if !csm.IsBlacklisted(child) {
		t.Error("community should be in blacklist")
	}

	// 上游连带（根社区 Bond=20000 → 30%×50% = 3000）
	if slashing.UpstreamSlashAmount < 1000 {
		t.Errorf("upstream slash too low: %d (expected ≥1000)", slashing.UpstreamSlashAmount)
	}

	t.Logf("slash: community=%x amount=%d upstream=%d blacklisted=%v",
		child[:4], slashing.SlashAmount, slashing.UpstreamSlashAmount, slashing.DemotedToBlacklist)
}

func TestCommunitySlashingTotal(t *testing.T) {
	rootID := [32]byte{0x00}
	tree := vrg.NewGenesisTree(rootID)
	tree.GetCommunity(rootID).BondAmount = 20000
	a := [32]byte{0x01}
	b := [32]byte{0x02}
	tree.AddCommunity(a, rootID, 5000, "x", 1)
	tree.AddCommunity(b, rootID, 5000, "x", 1)

	csm := vrg.NewCommunitySlashingManager(tree)
	csm.SlashCommunity(a, "fraud", 0.20, false)
	csm.SlashCommunity(b, "fraud", 0.40, false)

	total := csm.TotalSlashed()
	if total < 3000 { // A:1000 + B:2000 + upstreams
		t.Errorf("total slashed too low: %d", total)
	}
	t.Logf("total slashed: %d", total)
}

// ============================================================
// E5: Namespace 测试
// ============================================================

func TestNamespaceRouter(t *testing.T) {
	nr := vrg.NewNamespaceRouter()
	owner := [32]byte{0x01}

	ns, err := nr.RegisterNamespace("test-app", owner)
	if err != nil {
		t.Fatalf("register: %v", err)
	}
	if ns.ID == 0 {
		t.Error("namespace ID should not be 0")
	}

	// 查询
	retrieved := nr.GetNamespace(ns.ID)
	if retrieved == nil {
		t.Fatal("namespace should be retrievable")
	}
	if retrieved.Name != "test-app" {
		t.Errorf("name mismatch: got %s", retrieved.Name)
	}

	// 按所有者查询
	owned := nr.GetNamespacesByOwner(owner)
	if len(owned) != 1 {
		t.Fatalf("owner should have 1 namespace, got %d", len(owned))
	}
}

// ============================================================
// E3: 摆渡节点测试
// ============================================================

func TestFerryNodeLifecycle(t *testing.T) {
	nodeID := [32]byte{0x01}
	communityID := [32]byte{0xAA}

	fn, err := vrg.NewFerryNodeFull(nodeID, communityID, "/tmp/ferry-test")
	if err != nil {
		t.Fatalf("new ferry: %v", err)
	}

	state, carrying, deliveries := fn.GetState()
	if state != vrg.FerryIdle {
		t.Errorf("initial state should be idle, got %s", state)
	}
	if carrying != 0 {
		t.Errorf("initial carrying should be 0, got %d", carrying)
	}
	if deliveries != 0 {
		t.Errorf("initial deliveries should be 0, got %d", deliveries)
	}

	// 上下线
	fn.GoOffline()
	state, _, _ = fn.GetState()
	if state != vrg.FerryOffline {
		t.Error("should be offline")
	}

	fn.GoOnline()
	state, _, _ = fn.GetState()
	if state != vrg.FerryIdle {
		t.Error("should be idle after online")
	}

	// 工资计算
	cfg := vrg.DefaultFerryWageConfig()
	wages := fn.CalculateWages(cfg)
	if wages != 0 {
		t.Errorf("initial wages should be 0, got %d", wages)
	}
}

// ============================================================
// 跨模块集成测试
// ============================================================

func TestFullVRGFlow(t *testing.T) {
	// 1. 创建创世树
	rootID := [32]byte{0x00}
	tree := vrg.NewGenesisTree(rootID)
	commA := [32]byte{0x0A}
	commB := [32]byte{0x0B}
	tree.AddCommunity(commA, rootID, 10000, "genesis", 1)
	tree.AddCommunity(commB, rootID, 10000, "genesis", 1)

	// 2. 创建门限签名社区虚拟化身
	keys := make([][32]byte, 5)
	for i := range keys {
		keys[i] = [32]byte{byte(i + 1)}
	}
	avatar := crypto.NewCommunityAvatar(keys, []byte("genesis-invite-chain"), 100)
	if avatar.Threshold != 4 { // ceil(2*5/3) = 4
		t.Errorf("threshold should be 4, got %d", avatar.Threshold)
	}

	// 3. 创建纪元计数器
	ec := vrg.NewEpochCounter(100)
	for i := 0; i < 500; i++ {
		ec.AdvanceBlock(int64(i))
	}
	if ec.CurrentEpoch() != 5 {
		t.Fatalf("epoch should be 5, got %d", ec.CurrentEpoch())
	}

	// 4. 建立外交通道
	tem := vrg.NewTrustEdgeManager()
	edge, err := tem.ProposeEdge(commA, commB, 1, 2, 5000, 5000)
	if err != nil {
		t.Fatalf("propose edge: %v", err)
	}
	tem.ActivateEdge(edge.EdgeID)

	// 5. 计算拓扑距离和安全系数
	d, err := tree.Distance(commA, commB)
	if err != nil {
		t.Fatalf("distance: %v", err)
	}
	if d != 2 {
		t.Errorf("d(A,B) should be 2, got %d", d)
	}

	s, _ := tree.SecurityCoefficient(commA, commB)
	t.Logf("VRG flow: communities=%d distance=%d security=%.1f edge=%s",
		tree.TotalCommunities(), d, s, edge.EdgeID)

	// 6. 双花检测
	dsd := vrg.NewDoubleSpendDetector()
	_, err = dsd.RegisterReceipt("asset-1", ec.CurrentEpoch(), [32]byte{0x01}, commA)
	if err != nil {
		t.Fatalf("register receipt: %v", err)
	}

	// 7. 社区 Slashing
	csm := vrg.NewCommunitySlashingManager(tree)
	slashing, err := csm.SlashCommunity(commB, "test_slash", 0.25, true)
	if err != nil {
		t.Fatalf("slash: %v", err)
	}
	t.Logf("slashing: community=%x amount=%d blacklisted=%v",
		commB[:4], slashing.SlashAmount, slashing.DemotedToBlacklist)

	// 验证一致性
	if tree.TotalCommunities() != 3 {
		t.Errorf("should have 3 communities, got %d", tree.TotalCommunities())
	}
	if len(tem.ActiveEdges()) != 1 {
		t.Errorf("should have 1 active edge, got %d", len(tem.ActiveEdges()))
	}
}
