package randbeacon_test

import (
	"testing"
	"time"

	"github.com/nekop2p/nekop2p/randbeacon"
)

func TestRandomBeaconRound(t *testing.T) {
	r := randbeacon.NewRound(1, 10*time.Second, 10*time.Second, 50)

	// 三个参与者提交承诺
	var seeds [3][32]byte
	var nonces [3][32]byte
	participants := []string{"alice", "bob", "charlie"}

	for i, p := range participants {
		seeds[i][0] = byte(i + 1)
		nonces[i][0] = byte(i + 10)
		if err := r.SubmitCommitment(p, seeds[i], nonces[i]); err != nil {
			t.Fatalf("commit %s: %v", p, err)
		}
	}

	// 三者全部揭示
	for i, p := range participants {
		if err := r.SubmitReveal(p, seeds[i], nonces[i]); err != nil {
			t.Fatalf("reveal %s: %v", p, err)
		}
	}

	val, err := r.Finalize()
	if err != nil {
		t.Fatal(err)
	}

	// 最终值应为非零
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
	r := randbeacon.NewRound(1, 10*time.Second, 10*time.Second, 50)

	var seed, nonce [32]byte
	seed[0] = 0x42
	nonce[0] = 0x99

	r.SubmitCommitment("alice", seed, nonce)

	// 使用错误的种子揭示
	var badSeed [32]byte
	badSeed[0] = 0xFF
	err := r.SubmitReveal("alice", badSeed, nonce)
	if err == nil {
		t.Error("bad reveal should fail")
	}
}

func TestRandomBeaconNoReveal(t *testing.T) {
	r := randbeacon.NewRound(1, 10*time.Second, 10*time.Second, 50)

	var seed, nonce [32]byte
	r.SubmitCommitment("alice", seed, nonce)

	// Alice 未揭示 → 应该出现在违约列表中
	defaults := r.DefaultParticipants()
	if len(defaults) != 1 || defaults[0] != "alice" {
		t.Errorf("expected alice in defaults, got %v", defaults)
	}
}
