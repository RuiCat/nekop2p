package randbeacon_test

import (
	"testing"
	"time"

	"github.com/nekop2p/nekop2p/randbeacon"
)

func TestRandomBeaconRound(t *testing.T) {
	// 使用短窗口以便测试
	r := randbeacon.NewRound(1, 50*time.Millisecond, 100*time.Millisecond, 50)

	var seeds [3][32]byte
	var nonces [3][32]byte
	participants := []string{"alice", "bob", "charlie"}

	// 提交阶段
	for i, p := range participants {
		seeds[i][0] = byte(i + 1)
		nonces[i][0] = byte(i + 10)
		if err := r.SubmitCommitment(p, seeds[i], nonces[i]); err != nil {
			t.Fatalf("commit %s: %v", p, err)
		}
	}

	// 等待进入揭示窗口
	time.Sleep(60 * time.Millisecond)

	// 揭示阶段
	for i, p := range participants {
		if err := r.SubmitReveal(p, seeds[i], nonces[i]); err != nil {
			t.Fatalf("reveal %s: %v", p, err)
		}
	}

	val, err := r.Finalize()
	if err != nil {
		t.Fatal(err)
	}
	if val == [32]byte{} {
		t.Error("final value should not be zero")
	}

	// 第二次 Finalize 应该返回相同的值
	val2, _ := r.Finalize()
	if val != val2 {
		t.Error("Finalize should be idempotent")
	}
}

func TestRandomBeaconBadReveal(t *testing.T) {
	r := randbeacon.NewRound(1, 50*time.Millisecond, 100*time.Millisecond, 50)

	var seed, nonce [32]byte
	seed[0] = 0x42
	nonce[0] = 0x99
	// 在提交窗口内提交
	if err := r.SubmitCommitment("alice", seed, nonce); err != nil {
		t.Fatalf("commit: %v", err)
	}

	time.Sleep(60 * time.Millisecond) // 进入揭示窗口

	// 使用错误的种子揭示 → 应失败
	var badSeed [32]byte
	badSeed[0] = 0xFF
	err := r.SubmitReveal("alice", badSeed, nonce)
	if err == nil {
		t.Error("bad reveal should fail")
	}
}

func TestRandomBeaconNoReveal(t *testing.T) {
	r := randbeacon.NewRound(1, 50*time.Millisecond, 100*time.Millisecond, 50)

	var seed, nonce [32]byte
	r.SubmitCommitment("alice", seed, nonce)

	defaults := r.DefaultParticipants()
	if len(defaults) != 1 || defaults[0] != "alice" {
		t.Errorf("expected alice in defaults, got %v", defaults)
	}
}
