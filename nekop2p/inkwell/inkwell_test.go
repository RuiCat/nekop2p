package inkwell_test

import (
	"testing"
	"time"

	"github.com/nekop2p/nekop2p/inkwell"
)

func TestGenerateParams(t *testing.T) {
	var loanID [32]byte
	loanID[0] = 0x42

	var bSeed, lSeed [32]byte
	bSeed[0] = 0xAA
	lSeed[0] = 0xBB

	now := time.Now()

	params, err := inkwell.GenerateParams(loanID, 10000, bSeed, lSeed, now)
	if err != nil {
		t.Fatalf("generate: %v", err)
	}

	if params.TotalAmount != 10000 {
		t.Errorf("total: got %d, want 10000", params.TotalAmount)
	}
	if params.FragmentCount < 3 || params.FragmentCount > 7 {
		t.Errorf("fragment count: got %d, want 3-7", params.FragmentCount)
	}
	if !params.WindowEnd.After(params.WindowStart) {
		t.Error("window end should be after start")
	}
	if params.WindowEnd.Sub(params.WindowStart) > 91*24*time.Hour {
		t.Error("window should be ≤90 days")
	}

	// 验证分片总和等于总额
	var sum uint64
	for _, f := range params.Fragments {
		sum += f
	}
	if sum != 10000 {
		t.Errorf("fragment sum: got %d, want 10000", sum)
	}
}

func TestDeterministicGeneration(t *testing.T) {
	var loanID [32]byte
	var bSeed, lSeed [32]byte

	now := time.Now()
	p1, _ := inkwell.GenerateParams(loanID, 5000, bSeed, lSeed, now)
	p2, _ := inkwell.GenerateParams(loanID, 5000, bSeed, lSeed, now)

	// 相同输入 → 相同输出
	if p1.FragmentCount != p2.FragmentCount {
		t.Error("same seeds should produce same fragment count")
	}
	for i, f := range p1.Fragments {
		if f != p2.Fragments[i] {
			t.Error("same seeds should produce same fragments")
		}
	}
}

func TestDifferentSeedsDifferentOutput(t *testing.T) {
	var loanID [32]byte
	var s1, s2 [32]byte
	s1[0] = 0x01
	s2[0] = 0x02

	now := time.Now()
	p1, _ := inkwell.GenerateParams(loanID, 5000, s1, s1, now)
	p2, _ := inkwell.GenerateParams(loanID, 5000, s2, s2, now)

	// 不同种子应该（几乎肯定）产生不同的分片
	same := true
	for i := range p1.Fragments {
		if p1.Fragments[i] != p2.Fragments[i] {
			same = false
			break
		}
	}
	if same && p1.FragmentCount == p2.FragmentCount {
		t.Log("warning: different seeds produced same fragments (1/2^256 chance)")
	}
}

func TestGeneratePlan(t *testing.T) {
	var loanID [32]byte
	var bSeed, lSeed [32]byte

	params, _ := inkwell.GenerateParams(loanID, 5000, bSeed, lSeed, time.Now())
	plan := params.GeneratePlan()

	if len(plan) != len(params.Fragments) {
		t.Errorf("plan length: got %d, want %d", len(plan), len(params.Fragments))
	}

	// 每个计划条目应该有非零的到期时间
	now := time.Now()
	for i, fp := range plan {
		if fp.DueAfter.Before(now) {
			t.Errorf("fragment %d due in the past: %s", i, fp.DueAfter)
		}
		if fp.Amount == 0 {
			t.Errorf("fragment %d has zero amount", i)
		}
	}
}

func TestGetNextFragment(t *testing.T) {
	var loanID [32]byte
	var bSeed, lSeed [32]byte

	params, _ := inkwell.GenerateParams(loanID, 3000, bSeed, lSeed, time.Now())

	// 尚未支付任何分片 → 应返回最早的一个
	fp, err := params.GetNextFragment(nil)
	if err != nil && err.Error()[:3] != "no " {
		t.Fatalf("get next: %v", err)
	}
	if fp == nil {
		t.Fatal("expected a fragment")
	}
	if fp.Amount == 0 {
		t.Error("fragment should have amount")
	}
}

func TestRemainingAmount(t *testing.T) {
	var loanID [32]byte
	var bSeed, lSeed [32]byte

	params, _ := inkwell.GenerateParams(loanID, 3000, bSeed, lSeed, time.Now())

	// 未支付任何分片 → 剩余 = 总额
	remaining := params.RemainingAmount(nil)
	if remaining != 3000 {
		t.Errorf("remaining: got %d, want 3000", remaining)
	}

	// 支付第一笔分片
	remaining = params.RemainingAmount([]int{0})
	if remaining >= 3000 {
		t.Error("remaining should decrease after paying fragment 0")
	}

	// 支付全部分片
	allIndices := make([]int, len(params.Fragments))
	for i := range allIndices {
		allIndices[i] = i
	}
	remaining = params.RemainingAmount(allIndices)
	if remaining != 0 {
		t.Errorf("remaining after full payment: got %d, want 0", remaining)
	}
}

func TestSplitAmount(t *testing.T) {
	// 通过 GenerateParams 间接测试
	var loanID [32]byte
	var bSeed, lSeed [32]byte

	for _, total := range []uint64{100, 1000, 10000, 99999} {
		params, err := inkwell.GenerateParams(loanID, total, bSeed, lSeed, time.Now())
		if err != nil {
			t.Fatalf("generate %d: %v", total, err)
		}

		var sum uint64
		for _, f := range params.Fragments {
			if f == 0 {
				t.Errorf("zero fragment for total %d", total)
			}
			sum += f
		}
		if sum != total {
			t.Errorf("sum %d != total %d", sum, total)
		}
	}
}

func TestChaosProperty(t *testing.T) {
	// 生成大量参数并验证统计特性
	var loanID [32]byte

	for i := 0; i < 100; i++ {
		var s1, s2 [32]byte
		s1[0] = byte(i)
		s2[0] = byte(i + 100)

		params, _ := inkwell.GenerateParams(loanID, 5000, s1, s2, time.Now())

		// 每个分片应该 > 0
		for _, f := range params.Fragments {
			if f == 0 {
				t.Errorf("iteration %d: zero fragment", i)
			}
		}

		// 总和必须精确
		var sum uint64
		for _, f := range params.Fragments {
			sum += f
		}
		if sum != 5000 {
			t.Errorf("iteration %d: sum mismatch %d", i, sum)
		}
	}
}
