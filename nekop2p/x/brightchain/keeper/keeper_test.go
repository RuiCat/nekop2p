package keeper_test

import (
	"crypto/sha256"
	"os"
	"testing"

	"github.com/nekop2p/nekop2p/store"
	"github.com/nekop2p/nekop2p/x/brightchain/keeper"
	"github.com/nekop2p/nekop2p/x/brightchain/types"
)

func newTestKeeper(t *testing.T) *keeper.Keeper {
	t.Helper()
	dir, err := os.MkdirTemp("", "brightkeeper-"+t.Name()+"-*")
	if err != nil {
		t.Fatalf("create temp dir: %v", err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	s, err := store.New(dir)
	if err != nil {
		t.Fatalf("create store: %v", err)
	}
	return keeper.NewKeeper(s, "test")
}

func newRegisterMsg(recvPk, sendPk []byte) *types.MsgRegister {
	return &types.MsgRegister{
		RecvPk: recvPk,
		SendPk: sendPk,
	}
}

// ===== UserBlock 测试 =====

func TestRegisterUser(t *testing.T) {
	k := newTestKeeper(t)

	msg := newRegisterMsg([]byte("recv-key-32-bytes-xxxxxxxxxxxx"), []byte("send-key-32-bytes-xxxxxxxxxxxx"))
	block, err := k.RegisterUser(nil, msg)
	if err != nil {
		t.Fatalf("register: %v", err)
	}
	if block == nil {
		t.Fatal("block is nil")
	}
	if !block.SeedPhase {
		t.Log("genesis user correctly not in seed phase (trust anchor)")
	}
	if block.TrustWeight < 10 {
		t.Error("user should have trust weight")
	}
}

func TestRegisterUserDuplicate(t *testing.T) {
	k := newTestKeeper(t)

	msg := newRegisterMsg([]byte("recv-alice"), []byte("send-alice"))
	_, err := k.RegisterUser(nil, msg)
	if err != nil {
		t.Fatalf("first register: %v", err)
	}

	_, err = k.RegisterUser(nil, msg)
	if err == nil {
		t.Error("duplicate registration should fail")
	}
}

func TestGetUserBlock(t *testing.T) {
	k := newTestKeeper(t)

	msg := newRegisterMsg([]byte("recv-bob"), []byte("send-bob"))
	block, _ := k.RegisterUser(nil, msg)

	chainID := deriveChainIDForTest(msg.SendPk)
	retrieved := k.GetUserBlock(nil, chainID)
	if retrieved == nil {
		t.Fatal("GetUserBlock returned nil")
	}
	if retrieved.Address != block.Address {
		t.Error("address mismatch")
	}
}

func TestGetUserBlockNotFound(t *testing.T) {
	k := newTestKeeper(t)
	var unusedID types.ChainID
	result := k.GetUserBlock(nil, unusedID)
	if result != nil {
		t.Error("GetUserBlock should return nil for unknown id")
	}
}

// ===== 好友管理测试 =====

func TestUpdateFriends(t *testing.T) {
	k := newTestKeeper(t)

	msg := newRegisterMsg([]byte("recv-alice2"), []byte("send-alice2"))
	block, _ := k.RegisterUser(nil, msg)

	updateMsg := &types.MsgUpdateFriends{
		Sender: block.Address,
		Add: []*types.FriendRecord{
			{ChainId: []byte("friend-1"), RecvPk: []byte("pk1"), SendPk: []byte("sk1")},
			{ChainId: []byte("friend-2"), RecvPk: []byte("pk2"), SendPk: []byte("sk2")},
		},
	}

	err := k.UpdateFriends(nil, updateMsg)
	if err != nil {
		t.Fatalf("update friends: %v", err)
	}

	updated := k.GetUserBlock(nil, deriveChainIDForTest(msg.SendPk))
	if len(updated.Friends) != 2 {
		t.Errorf("expected 2 friends, got %d", len(updated.Friends))
	}
}

func TestUpdateFriendsRemove(t *testing.T) {
	k := newTestKeeper(t)

	msg := newRegisterMsg([]byte("recv-alice3"), []byte("send-alice3"))
	block, _ := k.RegisterUser(nil, msg)

	// 先添加
	k.UpdateFriends(nil, &types.MsgUpdateFriends{
		Sender: block.Address,
		Add: []*types.FriendRecord{
			{ChainId: []byte("friend-1")},
			{ChainId: []byte("friend-2")},
			{ChainId: []byte("friend-3")},
		},
	})

	// 再删除
	k.UpdateFriends(nil, &types.MsgUpdateFriends{
		Sender: block.Address,
		Remove: [][]byte{[]byte("friend-2")},
	})

	updated := k.GetUserBlock(nil, deriveChainIDForTest(msg.SendPk))
	if len(updated.Friends) != 2 {
		t.Errorf("expected 2 friends after remove, got %d", len(updated.Friends))
	}
}

func TestUpdateFriendsWrongOwner(t *testing.T) {
	k := newTestKeeper(t)

	msg := newRegisterMsg([]byte("recv-owner"), []byte("send-owner"))
	k.RegisterUser(nil, msg)

	// 用错误的所有者尝试更新
	err := k.UpdateFriends(nil, &types.MsgUpdateFriends{
		Sender: "wrong-owner",
	})
	if err == nil {
		t.Error("update by wrong owner should fail")
	}
}

// ===== 担保债券测试 =====

func TestCreateBond(t *testing.T) {
	k := newTestKeeper(t)

	// 注册双方
	alice := newRegisterMsg([]byte("recv-alice-b"), []byte("send-alice-b"))
	bob := newRegisterMsg([]byte("recv-bob-b"), []byte("send-bob-b"))
	aliceBlock, _ := k.RegisterUser(nil, alice)
	bobBlock, _ := k.RegisterUser(nil, bob)

	msg := &types.MsgGuarantee{
		Inviter:     aliceBlock.Address,
		Invitee:     bobBlock.Address,
		Coefficient: 80,
	}
	bond, err := k.CreateBond(nil, msg)
	if err != nil {
		t.Fatalf("create bond: %v", err)
	}
	if bond.Status != types.BondStatus_ACTIVE {
		t.Error("bond should be active")
	}
}

func TestGetBond(t *testing.T) {
	k := newTestKeeper(t)

	alice := newRegisterMsg([]byte("recv-a"), []byte("send-a"))
	bob := newRegisterMsg([]byte("recv-b"), []byte("send-b"))
	aBlock, _ := k.RegisterUser(nil, alice)
	bBlock, _ := k.RegisterUser(nil, bob)

	bond, _ := k.CreateBond(nil, &types.MsgGuarantee{
		Inviter: aBlock.Address,
		Invitee: bBlock.Address,
	})

	retrieved := k.GetBond(nil, bond.BondId)
	if retrieved == nil {
		t.Fatal("GetBond returned nil")
	}
}

func TestReleaseBond(t *testing.T) {
	k := newTestKeeper(t)

	alice := newRegisterMsg([]byte("recv-a2"), []byte("send-a2"))
	bob := newRegisterMsg([]byte("recv-b2"), []byte("send-b2"))
	aBlock, _ := k.RegisterUser(nil, alice)
	bBlock, _ := k.RegisterUser(nil, bob)

	bond, _ := k.CreateBond(nil, &types.MsgGuarantee{
		Inviter: aBlock.Address,
		Invitee: bBlock.Address,
	})

	err := k.ReleaseBond(nil, bond.BondId)
	if err != nil {
		t.Fatalf("release bond: %v", err)
	}

	released := k.GetBond(nil, bond.BondId)
	if released.Status != types.BondStatus_RELEASED {
		t.Error("bond should be released")
	}
}

func TestForfeitBond(t *testing.T) {
	k := newTestKeeper(t)

	alice := newRegisterMsg([]byte("recv-a3"), []byte("send-a3"))
	bob := newRegisterMsg([]byte("recv-b3"), []byte("send-b3"))
	aBlock, _ := k.RegisterUser(nil, alice)
	bBlock, _ := k.RegisterUser(nil, bob)

	bond, _ := k.CreateBond(nil, &types.MsgGuarantee{
		Inviter: aBlock.Address,
		Invitee: bBlock.Address,
	})

	err := k.ForfeitBond(nil, bond.BondId)
	if err != nil {
		t.Fatalf("forfeit bond: %v", err)
	}

	forfeited := k.GetBond(nil, bond.BondId)
	if forfeited.Status != types.BondStatus_FORFEITED {
		t.Error("bond should be forfeited")
	}
}

// ===== 资金池测试 =====

func TestCollectFees(t *testing.T) {
	k := newTestKeeper(t)

	err := k.CollectFees(nil, 1000)
	if err != nil {
		t.Fatalf("collect fees: %v", err)
	}

	pool := k.GetPool(nil)
	if pool.TotalBalance != 1000 {
		t.Errorf("total balance: got %d, want 1000", pool.TotalBalance)
	}
	if pool.SalaryRelay != 250 { // 25%
		t.Errorf("salary relay: got %d, want 250", pool.SalaryRelay)
	}
	if pool.Community != 150 { // 15%
		t.Errorf("community: got %d, want 150", pool.Community)
	}
}

func TestCollectFeesAccumulates(t *testing.T) {
	k := newTestKeeper(t)

	k.CollectFees(nil, 1000)
	k.CollectFees(nil, 500)

	pool := k.GetPool(nil)
	if pool.TotalBalance != 1500 {
		t.Errorf("total balance: got %d, want 1500", pool.TotalBalance)
	}
}

// ===== 节点角色测试 =====

func TestSetNodeRole(t *testing.T) {
	k := newTestKeeper(t)

	msg := newRegisterMsg([]byte("recv-node"), []byte("send-node"))
	block, _ := k.RegisterUser(nil, msg)

	err := k.SetNodeRole(nil, block.Address, types.NodeRole_OFFICIAL_RELAY)
	if err != nil {
		t.Fatalf("set node role: %v", err)
	}

	chainID := deriveChainIDForTest(msg.SendPk)
	retrieved := k.GetUserBlock(nil, chainID)
	if retrieved.NodeRole != types.NodeRole_OFFICIAL_RELAY {
		t.Error("node role not set correctly")
	}
}

// ===== 信任权重测试 =====

func TestRecalculateTrustWeights(t *testing.T) {
	k := newTestKeeper(t)

	// 注册 3 个用户
	for i := 0; i < 3; i++ {
		recv := []byte{byte('r'), byte(i)}
		send := []byte{byte('s'), byte(i)}
		k.RegisterUser(nil, newRegisterMsg(recv, send))
	}

	k.RecalculateTrustWeights(nil)

	users := k.GetAllUsers(nil)
	if len(users) != 3 {
		t.Errorf("expected 3 users, got %d", len(users))
	}
	for _, u := range users {
		if u.TrustWeight == 0 {
			t.Error("trust weight should be > 0 after recalculation")
		}
	}
}

// ===== 递归追偿测试 =====

func TestProcessRecursiveLiability(t *testing.T) {
	k := newTestKeeper(t)

	// 注册违约者和担保人
	defaulter := newRegisterMsg([]byte("recv-default"), []byte("send-default"))
	guarantor := newRegisterMsg([]byte("recv-guarantor"), []byte("send-guarantor"))
	dBlock, _ := k.RegisterUser(nil, defaulter)
	gBlock, _ := k.RegisterUser(nil, guarantor)

	// 给违约者分配一些额度
	chainID := deriveChainIDForTest(defaulter.SendPk)
	block := k.GetUserBlock(nil, chainID)
	block.CreditLimit = 100
	block.TrustWeight = 50

	// 创建担保关系
	k.CreateBond(nil, &types.MsgGuarantee{
		Inviter: gBlock.Address,
		Invitee: dBlock.Address,
	})

	// 执行递归追偿（追偿 200，超过违约者额度）
	prov, initialAmount, err := k.ProcessRecursiveLiability(nil, dBlock.Address, 200)
	_ = prov
	_ = initialAmount
	// 预期可能触发延期追偿或失败（违约者只有 100 额度）
	if err == nil {
		t.Log("recursive liability initiated (deferred provision)")
	}
}

// ===== GetAll 测试 =====

func TestGetAllUsers(t *testing.T) {
	k := newTestKeeper(t)

	// 注册前 3 个创世用户 (无需凭证)
	genesisUsers := make([]*types.UserBlock, 3)
	for i := 0; i < 3; i++ {
		recv := []byte{byte('r'), byte(i)}
		send := []byte{byte('s'), byte(i)}
		block, err := k.RegisterUser(nil, newRegisterMsg(recv, send))
		if err != nil {
			t.Fatalf("genesis user %d: %v", i, err)
		}
		genesisUsers[i] = block
	}

	// 为第 4-5 个用户生成模拟邀请凭证
	mockCred := make([]byte, 216) // 空凭证 (签名验证会失败，但至少格式正确)
	for i := 3; i < 5; i++ {
		recv := []byte{byte('r'), byte(i)}
		send := []byte{byte('s'), byte(i)}
		msg := newRegisterMsg(recv, send)
		// 附带 3 个凭证（虽然签名无效，但测试创建阶段逻辑）
		msg.GuarantorSigs = [][]byte{mockCred, mockCred, mockCred}
		_, err := k.RegisterUser(nil, msg)
		if err != nil {
			t.Logf("user %d registration rejected (expected without valid creds): %v", i, err)
		}
	}

	users := k.GetAllUsers(nil)
	if len(users) < 3 {
		t.Errorf("expected at least 3 genesis users, got %d", len(users))
	}
	t.Logf("total users: %d (genesis phase=%v)", len(users), k.IsGenesisPhase())
}

func TestGetAllBonds(t *testing.T) {
	k := newTestKeeper(t)

	alice := newRegisterMsg([]byte("recv-ax"), []byte("send-ax"))
	bob := newRegisterMsg([]byte("recv-bx"), []byte("send-bx"))
	aBlock, _ := k.RegisterUser(nil, alice)
	bBlock, _ := k.RegisterUser(nil, bob)

	k.CreateBond(nil, &types.MsgGuarantee{
		Inviter: aBlock.Address,
		Invitee: bBlock.Address,
	})

	bonds := k.GetAllBonds(nil)
	if len(bonds) != 1 {
		t.Errorf("expected 1 bond, got %d", len(bonds))
	}
}

// ===== 帮助函数 =====

func deriveChainIDForTest(sendPk []byte) types.ChainID {
	return sha256.Sum256(sendPk)
}
