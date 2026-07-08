package tests

import (
	"crypto/sha256"
	"os"
	"testing"

	"github.com/nekop2p/nekop2p/app"
	"github.com/nekop2p/nekop2p/store"
	"github.com/nekop2p/nekop2p/x/brightchain/keeper"
	"github.com/nekop2p/nekop2p/x/brightchain/types"
)

func newTestChain(t *testing.T) (*app.NekoApp, func()) {
	t.Helper()
	dir, err := os.MkdirTemp("", "nekop2p-test-*")
	if err != nil {
		t.Fatalf("create temp dir: %v", err)
	}
	a, err := app.NewNekoApp(dir)
	if err != nil {
		t.Fatalf("create app: %v", err)
	}
	return a, func() { a.Shutdown(); os.RemoveAll(dir) }
}

func mkKeyPair(seed byte) (recv, send []byte) {
	r := make([]byte, 32)
	s := make([]byte, 32)
	for i := range r {
		r[i] = seed + byte(i)
		s[i] = seed + byte(i) + 100
	}
	return r, s
}

// ===== 场景 1: 链生命周期持久化 =====

func TestChainLifecycle(t *testing.T) {
	dir, err := os.MkdirTemp("", "lifecycle-*")
	if err != nil { t.Fatal(err) }
	defer os.RemoveAll(dir)

	a1, _ := app.NewNekoApp(dir)
	for i := 0; i < 150; i++ {
		a1.RunOnce()
	}
	a1.Shutdown()

	a2, _ := app.NewNekoApp(dir)
	defer a2.Shutdown()

	if a2.Height() < 100 {
		t.Errorf("height persisted: got %d, expected >= 100", a2.Height())
	}
	t.Logf("✅ Chain lifecycle: height=%d persisted", a2.Height())
}

// ===== 场景 2: 用户注册 =====

func TestUserRegistration(t *testing.T) {
	a, clean := newTestChain(t)
	defer clean()

	r, s := mkKeyPair(1)
	a.SubmitTx("register", append(r, s...))
	a.RunOnce()

	id := sha256.Sum256(s)
	u := a.BrightChainKeeper.GetUserBlock(nil, id)
	if u == nil {
		t.Fatal("user registration failed")
	}
	// 创世用户 (前3个) 不处于种子期，是信任锚点
	if u.TrustWeight < 10 {
		t.Error("genesis user should have trust weight")
	}
	t.Logf("✅ Genesis user registered (seed_phase=%v, trust=%d)", u.SeedPhase, u.TrustWeight)
}

// ===== 场景 3: 批量注册 =====

func TestBulkRegistration(t *testing.T) {
	a, clean := newTestChain(t)
	defer clean()

	// 创世阶段：前3个用户无需凭证即可注册
	for i := 0; i < 3; i++ {
		r, s := mkKeyPair(byte(i))
		a.SubmitTx("register", append(r, s...))
	}
	a.RunOnce()

	n := len(a.BrightChainKeeper.GetAllUsers(nil))
	if n != 3 {
		t.Errorf("expected 3 genesis users, got %d", n)
	}

	// 第4个用户无凭证 → 应被拒绝
	r, s := mkKeyPair(100)
	a.SubmitTx("register", append(r, s...))
	a.RunOnce()

	n2 := len(a.BrightChainKeeper.GetAllUsers(nil))
	if n2 != 3 {
		t.Errorf("user 4 should be rejected without credentials, got %d users", n2)
	}
	t.Logf("✅ Genesis: %d users, user 4 rejected (need ≥3 invitations)", n)
}

// ===== 场景 4: 担保债券 =====

func TestGuaranteeFlow(t *testing.T) {
	a, clean := newTestChain(t)
	defer clean()

	r1, s1 := mkKeyPair(10)
	r2, s2 := mkKeyPair(20)
	a.SubmitTx("register", append(r1, s1...))
	a.SubmitTx("register", append(r2, s2...))
	a.RunOnce()
	a.SubmitTx("guarantee", append(s1, s2...))
	a.RunOnce()

	bonds := a.BrightChainKeeper.GetAllBonds(nil)
	if len(bonds) == 0 {
		t.Fatal("no bonds")
	}
	t.Log("✅ Guarantee bond created")
}

// ===== 场景 5: 资金池增长 =====

func TestPoolGrowth(t *testing.T) {
	a, clean := newTestChain(t)
	defer clean()

	pay := make([]byte, 32)
	amt := make([]byte, 8)
	amt[7] = 100
	a.SubmitTx("repay", append(pay, amt...))
	a.RunOnce()

	pool := a.BrightChainKeeper.GetPool(nil)
	if pool.TotalBalance != 100 {
		t.Errorf("pool: got %d, want 100", pool.TotalBalance)
	}
	t.Logf("✅ Pool: total=%d", pool.TotalBalance)
}

// ===== 场景 6: 贷款流程 =====

func TestLoanFlow(t *testing.T) {
	a, clean := newTestChain(t)
	defer clean()

	anon := make([]byte, 32)
	for i := range anon {
		anon[i] = byte(i + 42)
	}
	a.SubmitTx("loan", anon)
	a.RunOnce()

	loans := a.DarkChainKeeper.GetAllLoans()
	if len(loans) == 0 {
		t.Fatal("no loans")
	}
	t.Logf("✅ Loan: %d loans, status=%d", len(loans), loans[0].Status)
}

// ===== 场景 7: 内存池 =====

func TestMemPool(t *testing.T) {
	mp := app.NewMemPool()
	for i := 0; i < 150; i++ {
		mp.Submit(&app.Tx{ID: "tx", Type: "test", Data: []byte{byte(i)}})
	}
	if mp.Size() != 150 {
		t.Error("size")
	}
	taken := mp.Drain(100)
	if len(taken) != 100 || mp.Size() != 50 {
		t.Error("drain")
	}
	mp.Drain(100)
	if mp.Size() != 0 {
		t.Error("empty")
	}
	t.Log("✅ MemPool")
}

// ===== 场景 8: BoltDB 持久化 =====

func TestBoltDBPersist(t *testing.T) {
	dir, _ := os.MkdirTemp("", "bolt-*")
	defer os.RemoveAll(dir)

	s, _ := store.New(dir)
	s.Write(func(tx *store.Tx) error {
		tx.SetHeight(42)
		tx.PutUser("alice", []byte(`{"name":"Alice"}`))
		return nil
	})
	s.Close()

	s2, _ := store.New(dir)
	defer s2.Close()
	var h int64
	s2.Read(func(tx *store.Tx) error { h = tx.Height(); return nil })
	if h != 42 {
		t.Errorf("height: got %d, want 42", h)
	}
	t.Log("✅ BoltDB persist")
}

// ===== 场景 9: Keeper 持久化 =====

func TestKeeperPersist(t *testing.T) {
	dir, _ := os.MkdirTemp("", "keep-*")
	defer os.RemoveAll(dir)

	s, _ := store.New(dir)
	k := keeper.NewKeeper(s, "test")
	r, send := mkKeyPair(99)
	k.RegisterUser(nil, &types.MsgRegister{RecvPk: r, SendPk: send})
	s.Close()

	s2, _ := store.New(dir)
	defer s2.Close()
	k2 := keeper.NewKeeper(s2, "test")
	if len(k2.GetAllUsers(nil)) != 1 {
		t.Error("users lost")
	}
	t.Log("✅ Keeper persist")
}

// ===== 场景 10: 并发交易 =====

func TestConcurrentTx(t *testing.T) {
	a, clean := newTestChain(t)
	defer clean()

	ch := make(chan struct{}, 50)
	for i := 0; i < 50; i++ {
		go func(idx int) {
			r, s := mkKeyPair(byte(idx))
			a.SubmitTx("register", append(r, s...))
			ch <- struct{}{}
		}(i)
	}
	for i := 0; i < 50; i++ {
		<-ch
	}
	a.RunOnce()
	t.Logf("✅ Concurrent: %d users", len(a.BrightChainKeeper.GetAllUsers(nil)))
}

// ===== 场景 11: 综合场景 =====

func TestFullScenario(t *testing.T) {
	a, clean := newTestChain(t)
	defer clean()

	// 注册 3 个用户
	r1, s1 := mkKeyPair(1)
	r2, s2 := mkKeyPair(2)
	r3, s3 := mkKeyPair(3)
	a.SubmitTx("register", append(r1, s1...))
	a.SubmitTx("register", append(r2, s2...))
	a.SubmitTx("register", append(r3, s3...))
	a.RunOnce()

	// Alice 担保 Bob
	a.SubmitTx("guarantee", append(s1, s2...))
	a.RunOnce()

	// Charlie 还款
	pay := make([]byte, 32)
	copy(pay, s3)
	amt := make([]byte, 8)
	amt[7] = 100
	a.SubmitTx("repay", append(pay, amt...))
	a.RunOnce()

	// 验证
	users := a.BrightChainKeeper.GetAllUsers(nil)
	bonds := a.BrightChainKeeper.GetAllBonds(nil)
	pool := a.BrightChainKeeper.GetPool(nil)

	if len(users) != 3 {
		t.Errorf("users: %d", len(users))
	}
	if len(bonds) != 1 {
		t.Errorf("bonds: %d", len(bonds))
	}
	if pool.TotalBalance != 100 {
		t.Errorf("pool: %d", pool.TotalBalance)
	}
	t.Logf("✅ Full scenario: %d users, %d bonds, pool=%d", len(users), len(bonds), pool.TotalBalance)
}
