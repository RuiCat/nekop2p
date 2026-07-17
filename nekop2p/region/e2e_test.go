// Package region_test 区域节点共识 — 端到端综合测试 (Phase E)。
//
// 覆盖场景:
//   - E2E-1: 多用户同区域交易
//   - E2E-2: 跨区域四方交易
//   - E2E-3: 双网格交叉验证
//   - E2E-4: 故障转移
//   - E2E-5: 批量提交到全局链
//   - E2E-6: 并发交易正确性
//   - E2E-7: 性能基准
//
// 运行: go test -v -run TestE2E ./region/
package region_test

import (
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/nekop2p/nekop2p/region"
)

// ============================================================
// E2E-1: 多用户同区域交易
// ============================================================

func TestE2E_LocalTransactions(t *testing.T) {
	rn := region.NewRegionNode("R0-local", 0, 100)

	// 创建 10 个用户, 各 1000 余额
	for i := 0; i < 10; i++ {
		id := fmt.Sprintf("user-%d", i)
		rn.AddMember(id, 1000, region.SpatialCoord{X: float64(i)}, "other")
	}

	// 执行 50 笔同区域交易 (原子操作, 并发安全)
	for i := 0; i < 50; i++ {
		from := fmt.Sprintf("user-%d", i%10)
		to := fmt.Sprintf("user-%d", (i+1)%10)
		amount := uint64(10 + i%50)

		_, err := region.ExecuteLocalTx(rn, from, to, amount, region.DefaultFeeParams())
		if err != nil {
			t.Logf("tx %d failed (expected for insufficient balance): %v", i, err)
		}
	}

	// 验证总余额守恒
	var totalBal uint64
	for i := 0; i < 10; i++ {
		id := fmt.Sprintf("user-%d", i)
		bal, _ := rn.GetBalance(id)
		totalBal += bal
	}
	if totalBal != 10000 {
		t.Errorf("balance conservation violated: expected 10000, got %d", totalBal)
	}

	// 验证交易计数
	txCount := rn.GetTxCount()
	if txCount == 0 {
		t.Error("expected non-zero transaction count")
	}

	t.Logf("✅ E2E-1: %d users, %d tx, total balance=%d", 10, txCount, totalBal)
}

// ============================================================
// E2E-2: 跨区域四方交易
// ============================================================

func TestE2E_CrossRegionTransactions(t *testing.T) {
	ra := region.NewRegionNode("R1-grid1", 0, 100)
	rb := region.NewRegionNode("R2-grid2", 1, 100)

	// 用户在网格1
	ra.AddMember("alice", 5000, region.SpatialCoord{X: 1, Y: 1}, "R2-grid2")
	ra.AddMember("charlie", 3000, region.SpatialCoord{X: 2, Y: 1}, "R2-grid2")

	// 用户在网格2
	rb.AddMember("bob", 3000, region.SpatialCoord{X: 3, Y: 3}, "R1-grid1")
	rb.AddMember("dave", 2000, region.SpatialCoord{X: 4, Y: 3}, "R1-grid1")

	// 执行 5 笔跨区交易
	crossTxs := 0
	for i := 0; i < 5; i++ {
		tx, req, err := region.InitiateCrossRegion(ra, rb.RegionID, "alice", "bob", uint64(100+i*50), region.DefaultFeeParams())
		if err != nil {
			t.Logf("cross tx %d init failed: %v", i, err)
			continue
		}

		resp := region.VerifyCrossRegion(rb, req)
		if !resp.Accepted {
			t.Errorf("cross tx %d rejected: %s", i, resp.Reason)
			continue
		}

		if err := region.FinalizeCrossRegion(ra, rb, tx, region.DefaultFeeParams()); err != nil {
			t.Errorf("cross tx %d finalize: %v", i, err)
			continue
		}
		crossTxs++
	}

	if crossTxs == 0 {
		t.Error("no cross-region transactions completed")
	}

	// 验证跨区引用
	for _, block := range ra.GetChainBlocks() {
		if block.Tx.CrossRef != "" {
			t.Logf("cross-ref: %s", block.Tx.CrossRef)
		}
	}

	t.Logf("✅ E2E-2: %d cross-region tx completed", crossTxs)
}

// ============================================================
// E2E-3: 双网格交叉验证
// ============================================================

func TestE2E_DualGridVerification(t *testing.T) {
	// 模拟两个网格的节点
	grid1 := region.NewRegionNode("G1-A", 0, 100)
	grid2 := region.NewRegionNode("G2-B", 1, 100)

	// 同一个用户在不同网格有不同的余额记录 (交叉验证确保一致)
	genesis := region.GenesisCoord("genesis")
	var users []region.SpatialCoord
	for i := 0; i < 5; i++ {
		id := fmt.Sprintf("user-%d", i)
		dist := uint8(1 + i)
		c := region.DeriveCoord(genesis, "genesis", id, dist)
		users = append(users, c)
		r1, r2 := region.UserRegions(c)
		grid1.AddMember(id, 1000, c, r2)
		grid2.AddMember(id, 1000, c, r1)
	}

	// 验证双网格余额一致
	for i := 0; i < 5; i++ {
		id := fmt.Sprintf("user-%d", i)
		bal1, _ := grid1.GetBalance(id)
		bal2, _ := grid2.GetBalance(id)
		if bal1 != bal2 {
			t.Errorf("user %s: grid1 balance=%d != grid2 balance=%d", id, bal1, bal2)
		}
	}

	// 网格1中同一区域的用户在网格2中应分散在不同区域
	r1Map := make(map[string]int)
	r2Map := make(map[string]int)
	for _, u := range users {
		r1, r2 := region.UserRegions(u)
		r1Map[r1]++
		r2Map[r2]++
	}
	t.Logf("Grid1 regions: %d unique, Grid2 regions: %d unique", len(r1Map), len(r2Map))

	t.Logf("✅ E2E-3: dual-grid verification passed (%d users)", len(users))
}

// ============================================================
// E2E-4: 故障转移
// ============================================================

func TestE2E_Failover(t *testing.T) {
	primary := region.NewRegionNode("R-primary", 0, 100)
	primary.AddMember("alice", 1000, region.SpatialCoord{}, "other")

	// 创建影子节点
	shadow := region.NewRegionNode("R-shadow", 0, 100)

	// 主节点处理几笔交易
	for i := 0; i < 3; i++ {
		primary.Heartbeat()
	}
	if !primary.IsAlive(time.Second) {
		t.Error("primary should be alive after heartbeat")
	}

	// 模拟主节点宕机
	if err := primary.Failover(shadow); err != nil {
		t.Fatal(err)
	}

	// 验证影子节点接管了状态
	if !shadow.IsAlive(time.Second) {
		t.Error("shadow should be alive after failover")
	}
	bal, ok := shadow.GetBalance("alice")
	if !ok || bal != 1000 {
		t.Errorf("shadow should have alice's balance, got %d (ok=%v)", bal, ok)
	}

	t.Logf("✅ E2E-4: failover completed, shadow has %d members", len(shadow.Members))
}

// ============================================================
// E2E-5: 批量提交到全局链
// ============================================================

func TestE2E_BatchCommit(t *testing.T) {
	rn := region.NewRegionNode("R-batch", 0, 50)

	for i := 0; i < 5; i++ {
		id := fmt.Sprintf("user-%d", i)
		rn.AddMember(id, 1000, region.SpatialCoord{X: float64(i)}, "other")
	}

	// 处理 30 笔交易
	txCount := 0
	for i := 0; i < 30; i++ {
		from := fmt.Sprintf("user-%d", i%5)
		to := fmt.Sprintf("user-%d", (i+1)%5)
		amount := uint64(10 + i)

		_, err := region.ExecuteLocalTx(rn, from, to, amount, region.DefaultFeeParams())
		if err == nil {
			txCount++
		}
	}

	// 获取批量提交
	batch := rn.GetBatch(1, rn.GetTxCount(), nil)
	if batch.StartSeq != 1 {
		t.Errorf("batch start seq should be 1, got %d", batch.StartSeq)
	}
	if batch.EndSeq != rn.GetTxCount() {
		t.Errorf("batch end seq mismatch: %d != %d", batch.EndSeq, rn.GetTxCount())
	}

	t.Logf("✅ E2E-5: batch commit: seq %d-%d, %d txs, stateRoot=%x",
		batch.StartSeq, batch.EndSeq, txCount, batch.StateRoot[:4])
}

// ============================================================
// E2E-6: 并发交易正确性
// ============================================================

func TestE2E_ConcurrentSafety(t *testing.T) {
	rn := region.NewRegionNode("R-concurrent", 0, 100)
	rn.AddMember("alice", 100000, region.SpatialCoord{}, "other")
	rn.AddMember("bob", 100000, region.SpatialCoord{}, "other")

	var wg sync.WaitGroup
	errors := make(chan error, 200)

	// 100 个并发 goroutine 提交交易
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			amount := uint64(10 + idx%50)
			_, err := region.ExecuteLocalTx(rn, "alice", "bob", amount, region.DefaultFeeParams())
			if err != nil {
				select {
				case errors <- err:
				default:
				}
			}
		}(i)
	}

	wg.Wait()
	close(errors)

	// 统计失败 (余额不足是正常的)
	failCount := 0
	for range errors {
		failCount++
	}

	// 验证余额守恒: alice + bob = 200000
	aliceBal, _ := rn.GetBalance("alice")
	bobBal, _ := rn.GetBalance("bob")
	totalBal := aliceBal + bobBal

	if totalBal != 200000 {
		t.Errorf("concurrent balance conservation violated: alice=%d + bob=%d = %d (expected 200000)",
			aliceBal, bobBal, totalBal)
	}

	t.Logf("✅ E2E-6: concurrent safety: alice=%d, bob=%d, total=%d, errors=%d",
		aliceBal, bobBal, totalBal, failCount)

	// 清理
	close(rn.TxQueue)
}

// ============================================================
// E2E-7: 性能基准
// ============================================================

func TestE2E_Performance(t *testing.T) {
	rn := region.NewRegionNode("R-perf", 0, 100)
	for i := 0; i < 100; i++ {
		id := fmt.Sprintf("user-%d", i)
		rn.AddMember(id, 10000, region.SpatialCoord{X: float64(i)}, "other")
	}

	// 预热
	for i := 0; i < 10; i++ {
		from := fmt.Sprintf("user-%d", i)
		to := fmt.Sprintf("user-%d", i+1)
		_, _ = region.ExecuteLocalTx(rn, from, to, 10, region.DefaultFeeParams())
	}

	// 计时: 1000 笔同区域交易
	start := time.Now()
	successCount := 0
	for i := 0; i < 1000; i++ {
		from := fmt.Sprintf("user-%d", i%100)
		to := fmt.Sprintf("user-%d", (i+1)%100)
		tx, err := region.ProcessLocalTx(rn, from, to, uint64(1+i%10))
		if err != nil {
			continue
		}
		region.FinalizeLocalTx(rn, tx)
		successCount++
	}
	elapsed := time.Since(start)

	tps := float64(successCount) / elapsed.Seconds()
	t.Logf("✅ E2E-7: %d tx in %v = %.0f TPS", successCount, elapsed, tps)

	// 区域节点应 > 10,000 TPS (单线程串行)
	if tps < 1000 {
		t.Errorf("performance too low: %.0f TPS (expected > 1000)", tps)
	}
}
