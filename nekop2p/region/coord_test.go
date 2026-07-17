// Package region 区域节点共识协议测试。
package region_test

import (
	"fmt"
	"testing"

	"github.com/nekop2p/nekop2p/region"
)

func TestGenesisCoord(t *testing.T) {
	c := region.GenesisCoord("genesis-chain-id")
	if c.X != 0 || c.Y != 0 {
		t.Errorf("genesis coord should be (0,0), got (%.2f, %.2f)", c.X, c.Y)
	}
	if c.TrustDist != 0 {
		t.Errorf("genesis trust dist should be 0, got %d", c.TrustDist)
	}
}

func TestDeriveCoord(t *testing.T) {
	genesis := region.GenesisCoord("genesis")
	c1 := region.DeriveCoord(genesis, "genesis", "user-a", 1)
	c2 := region.DeriveCoord(genesis, "genesis", "user-b", 1)

	// 不同用户应得到不同坐标
	if c1.X == c2.X && c1.Y == c2.Y {
		t.Error("different users should have different coordinates")
	}

	// 信任距离应该递增
	if c1.TrustDist != 1 {
		t.Errorf("trust dist should be 1, got %d", c1.TrustDist)
	}

	// 确定性: 相同输入 → 相同输出
	c1Again := region.DeriveCoord(genesis, "genesis", "user-a", 1)
	if c1.X != c1Again.X || c1.Y != c1Again.Y {
		t.Error("DeriveCoord should be deterministic")
	}
}

func TestUserRegions(t *testing.T) {
	genesis := region.GenesisCoord("genesis")
	c := region.DeriveCoord(genesis, "genesis", "user-x", 1)

	r1, r2 := region.UserRegions(c)

	if r1 == "" || r2 == "" {
		t.Error("region IDs should not be empty")
	}
	if r1 == r2 {
		t.Error("grid1 and grid2 should produce different region IDs")
	}
}

func TestDualGridNonOverlap(t *testing.T) {
	genesis := region.GenesisCoord("genesis")
	var users []region.SpatialCoord

	// 生成多层用户: 不同信任距离产生更分散的坐标
	for i := 0; i < 20; i++ {
		id := fmt.Sprintf("user-%d", i)
		dist := uint8(1 + i%5) // 1-5 的信任距离
		c := region.DeriveCoord(genesis, "genesis", id, dist)
		users = append(users, c)
	}

	ratio := region.CheckNonOverlap(users)
	t.Logf("Non-overlap ratio: %.4f (%d users, varied trustDist)", ratio, len(users))
}

func TestRegionNodeOperations(t *testing.T) {
	rn := region.NewRegionNode("R0-test", 0, 100)

	// 添加成员
	rn.AddMember("user-a", 1000, region.SpatialCoord{X: 1, Y: 1}, "R1-other")
	rn.AddMember("user-b", 500, region.SpatialCoord{X: 1.5, Y: 1.5}, "R1-other")

	// 查询余额
	bal, ok := rn.GetBalance("user-a")
	if !ok || bal != 1000 {
		t.Errorf("expected 1000, got %d (ok=%v)", bal, ok)
	}

	// 锁定余额
	if err := rn.LockBalance("user-a", 300); err != nil {
		t.Fatal(err)
	}
	bal, _ = rn.GetBalance("user-a")
	if bal != 700 {
		t.Errorf("after lock expected 700, got %d", bal)
	}

	// 解锁
	if err := rn.UnlockBalance("user-a", 300); err != nil {
		t.Fatal(err)
	}

	// 增加余额
	if err := rn.CreditBalance("user-b", 200); err != nil {
		t.Fatal(err)
	}
	bal, _ = rn.GetBalance("user-b")
	if bal != 700 {
		t.Errorf("after credit expected 700, got %d", bal)
	}
}

func TestLocalTransaction(t *testing.T) {
	rn := region.NewRegionNode("R0-test", 0, 100)
	rn.AddMember("alice", 1000, region.SpatialCoord{}, "other")
	rn.AddMember("bob", 500, region.SpatialCoord{}, "other")

	// ProcessLocalTx 现在是原子操作 (含处理费)
	tx, err := region.ProcessLocalTx(rn, "alice", "bob", 200)
	if err != nil {
		t.Fatal(err)
	}
	if tx.Status != region.TxConfirmed {
		t.Errorf("expected Confirmed, got %d", tx.Status)
	}

	// 验证余额: alice扣了200+处理费, bob收了200
	aliceBal, _ := rn.GetBalance("alice")
	bobBal, _ := rn.GetBalance("bob")
	fee := region.DefaultFeeParams().CalcLocalFee(200)
	if aliceBal != 1000-200-fee {
		t.Errorf("alice: expected %d, got %d", 1000-200-fee, aliceBal)
	}
	if bobBal < 700 {
		t.Errorf("bob: expected >=700, got %d", bobBal)
	}
	t.Logf("alice=%d bob=%d fee=%d", aliceBal, bobBal, fee)
}

func TestCrossRegionTransaction(t *testing.T) {
	ra := region.NewRegionNode("R1-grid1", 0, 100)
	rb := region.NewRegionNode("R2-grid2", 1, 100)

	ra.AddMember("alice", 1000, region.SpatialCoord{X: 1, Y: 1}, "R2-grid2")
	rb.AddMember("bob", 500, region.SpatialCoord{X: 3, Y: 3}, "R1-grid1")

	// Phase 1-2: 发起
	tx, req, err := region.InitiateCrossRegion(ra, rb.RegionID, "alice", "bob", 300, region.DefaultFeeParams())
	if err != nil {
		t.Fatal(err)
	}
	if tx == nil || req == nil {
		t.Fatal("tx and req should not be nil")
	}

	// Phase 3: 交叉审查
	resp := region.VerifyCrossRegion(rb, req)
	if !resp.Accepted {
		t.Fatalf("cross-region rejected: %s", resp.Reason)
	}

	// Phase 4: 结算
	if err := region.FinalizeCrossRegion(ra, rb, tx, region.DefaultFeeParams()); err != nil {
		t.Fatal(err)
	}
	if tx.Status != region.TxConfirmed {
		t.Errorf("expected Confirmed, got %d", tx.Status)
	}
	if tx.CrossRef == "" {
		t.Error("cross-region tx should have CrossRef")
	}
}

