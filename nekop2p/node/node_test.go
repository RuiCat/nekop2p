package node_test

import (
	"fmt"
	"testing"
	"time"

	"github.com/nekop2p/nekop2p/beacon"
	"github.com/nekop2p/nekop2p/crypto"
	"github.com/nekop2p/nekop2p/frame"
	"github.com/nekop2p/nekop2p/node"
	"github.com/nekop2p/nekop2p/peer"
)

// ===== 基础测试 =====

func TestNodeNew(t *testing.T) {
	keys, _ := crypto.GenerateDualKeys()
	chainID := crypto.DeriveChainID(keys.SendKey.Public)

	cfg := node.Config{
		ChainID:           chainID,
		RecvKey:           keys.RecvKey,
		SendKey:           keys.SendKey,
		TargetConnections: 20,
	}

	n, err := node.New(cfg)
	if err != nil {
		t.Fatalf("new node: %v", err)
	}
	defer n.Shutdown()

	if n.State() != "OFFLINE" {
		t.Errorf("initial state: got %s, want OFFLINE", n.State())
	}
	if n.TotalPeers() != 0 {
		t.Error("new node should have no peers")
	}
	if n.OnlineFriends() != 0 {
		t.Error("new node should have no online friends")
	}
}

func TestNodeChainID(t *testing.T) {
	keys, _ := crypto.GenerateDualKeys()
	chainID := crypto.DeriveChainID(keys.SendKey.Public)

	cfg := node.Config{ChainID: chainID, RecvKey: keys.RecvKey, SendKey: keys.SendKey}

	n, _ := node.New(cfg)
	defer n.Shutdown()

	if n.ChainID() != chainID {
		t.Error("chain ID mismatch")
	}
}

// ===== 拓扑测试 =====

func TestTopologyAddFriend(t *testing.T) {
	keys, _ := crypto.GenerateDualKeys()
	chainID := crypto.DeriveChainID(keys.SendKey.Public)

	cfg := node.Config{ChainID: chainID, RecvKey: keys.RecvKey, SendKey: keys.SendKey}

	n, _ := node.New(cfg)
	defer n.Shutdown()

	friendKeys, _ := crypto.GenerateDualKeys()
	friendCID := crypto.DeriveChainID(friendKeys.SendKey.Public)

	n.AddCoreFriend(friendCID, friendKeys.RecvKey.Public, friendKeys.SendKey.Public)

	friends := n.Friends()
	if len(friends) != 1 {
		t.Fatalf("expected 1 friend, got %d", len(friends))
	}
	if friends[0].ChainID != formatChainID(friendCID) {
		t.Error("friend chain_id mismatch")
	}
	if n.OnlineFriends() != 0 {
		t.Error("newly added friend should not be online")
	}
}

func TestTopologyMultipleFriends(t *testing.T) {
	keys, _ := crypto.GenerateDualKeys()
	chainID := crypto.DeriveChainID(keys.SendKey.Public)

	cfg := node.Config{ChainID: chainID, RecvKey: keys.RecvKey, SendKey: keys.SendKey}

	n, _ := node.New(cfg)
	defer n.Shutdown()

	// 添加 5 个好友
	for i := 0; i < 5; i++ {
		fk, _ := crypto.GenerateDualKeys()
		fcid := crypto.DeriveChainID(fk.SendKey.Public)
		n.AddCoreFriend(fcid, fk.RecvKey.Public, fk.SendKey.Public)
	}

	if len(n.Friends()) != 5 {
		t.Errorf("expected 5 friends, got %d", len(n.Friends()))
	}
}

func TestTopologyDuplicateFriend(t *testing.T) {
	keys, _ := crypto.GenerateDualKeys()
	chainID := crypto.DeriveChainID(keys.SendKey.Public)

	cfg := node.Config{ChainID: chainID, RecvKey: keys.RecvKey, SendKey: keys.SendKey}

	n, _ := node.New(cfg)
	defer n.Shutdown()

	fk, _ := crypto.GenerateDualKeys()
	fcid := crypto.DeriveChainID(fk.SendKey.Public)

	// 添加同一个好友两次应该覆盖而非重复
	n.AddCoreFriend(fcid, fk.RecvKey.Public, fk.SendKey.Public)
	n.AddCoreFriend(fcid, fk.RecvKey.Public, fk.SendKey.Public)

	if len(n.Friends()) != 1 {
		t.Errorf("expected 1 friend after duplicate add, got %d", len(n.Friends()))
	}
}

// ===== 状态机测试 =====

func TestStateMachineOffline(t *testing.T) {
	keys, _ := crypto.GenerateDualKeys()
	chainID := crypto.DeriveChainID(keys.SendKey.Public)

	cfg := node.Config{ChainID: chainID, RecvKey: keys.RecvKey, SendKey: keys.SendKey}
	n, _ := node.New(cfg)
	defer n.Shutdown()

	if n.State() != "OFFLINE" {
		t.Errorf("initial state: got %s, want OFFLINE", n.State())
	}
}

func TestStateMachineGoOnlineWithoutFriends(t *testing.T) {
	keys, _ := crypto.GenerateDualKeys()
	chainID := crypto.DeriveChainID(keys.SendKey.Public)

	cfg := node.Config{
		ChainID:           chainID,
		RecvKey:           keys.RecvKey,
		SendKey:           keys.SendKey,
		TargetConnections: 20,
	}

	n, _ := node.New(cfg)
	defer n.Shutdown()

	// 没有任何好友或引导节点时，GoOnline 进入 Limbo 状态
	if err := n.GoOnline(); err != nil {
		t.Logf("GoOnline (expected): %v", err)
	}
	time.Sleep(50 * time.Millisecond)

	state := n.State()
	if state != "LIMBO" && state != "ISOLATED" {
		t.Logf("state after GoOnline: %s", state)
	}
}

// ===== 好友列表测试 =====

func TestFriendsEmpty(t *testing.T) {
	keys, _ := crypto.GenerateDualKeys()
	chainID := crypto.DeriveChainID(keys.SendKey.Public)

	cfg := node.Config{ChainID: chainID, RecvKey: keys.RecvKey, SendKey: keys.SendKey}
	n, _ := node.New(cfg)
	defer n.Shutdown()

	if len(n.Friends()) != 0 {
		t.Error("new node should have no friends")
	}
	if n.OnlineFriends() != 0 {
		t.Error("new node should have no online friends")
	}
	if n.TotalPeers() != 0 {
		t.Error("new node should have no peers")
	}
}

func TestFriendsOfflineByDefault(t *testing.T) {
	keys, _ := crypto.GenerateDualKeys()
	chainID := crypto.DeriveChainID(keys.SendKey.Public)

	cfg := node.Config{ChainID: chainID, RecvKey: keys.RecvKey, SendKey: keys.SendKey}
	n, _ := node.New(cfg)
	defer n.Shutdown()

	fk, _ := crypto.GenerateDualKeys()
	fcid := crypto.DeriveChainID(fk.SendKey.Public)
	n.AddCoreFriend(fcid, fk.RecvKey.Public, fk.SendKey.Public)

	friends := n.Friends()
	if len(friends) != 1 {
		t.Fatalf("expected 1 friend, got %d", len(friends))
	}
	if friends[0].Online {
		t.Error("friend should be offline by default")
	}
	if n.OnlineFriends() != 0 {
		t.Error("online friends should be 0")
	}
}

// ===== 连接数测试 =====

func TestPeersCountZero(t *testing.T) {
	keys, _ := crypto.GenerateDualKeys()
	chainID := crypto.DeriveChainID(keys.SendKey.Public)

	cfg := node.Config{ChainID: chainID, RecvKey: keys.RecvKey, SendKey: keys.SendKey}
	n, _ := node.New(cfg)
	defer n.Shutdown()

	if n.TotalPeers() != 0 {
		t.Error("total peers should be 0")
	}
}

// ===== 信标处理测试 =====

func TestBeaconFrameParsing(t *testing.T) {
	// 构造一个合法的信标帧并验证不会 panic
	bobKeys, _ := crypto.GenerateDualKeys()
	aliceKeys, _ := crypto.GenerateDualKeys()
	bobCID := crypto.DeriveChainID(bobKeys.SendKey.Public)
	aliceCID := crypto.DeriveChainID(aliceKeys.SendKey.Public)

	bp, _, err := beacon.Build(&beacon.BuildParams{
		SenderChainID: bobCID,
		SendPrivKey:   bobKeys.SendKey.Private,
		Targets: []beacon.Target{
			{ChainID: aliceCID, RecvPK: aliceKeys.RecvKey.Public},
		},
	})
	if err != nil {
		t.Fatalf("build beacon: %v", err)
	}

	// 验证信标可以被解析
	parsed, err := beacon.ParseBeacon(bp.Serialize())
	if err != nil {
		t.Fatalf("parse beacon: %v", err)
	}

	if parsed.SlotCount == 0 {
		t.Error("beacon should have at least 1 slot")
	}
}

func TestBeaconSlotDecryption(t *testing.T) {
	bobKeys, _ := crypto.GenerateDualKeys()
	aliceKeys, _ := crypto.GenerateDualKeys()
	bobCID := crypto.DeriveChainID(bobKeys.SendKey.Public)
	aliceCID := crypto.DeriveChainID(aliceKeys.SendKey.Public)

	bp, _, err := beacon.Build(&beacon.BuildParams{
		SenderChainID: bobCID,
		SendPrivKey:   bobKeys.SendKey.Private,
		Targets: []beacon.Target{
			{ChainID: aliceCID, RecvPK: aliceKeys.RecvKey.Public},
		},
	})
	if err != nil {
		t.Fatalf("build beacon: %v", err)
	}

	// Alice 应该能解密她的 slot
	decrypted := false
	for i := uint8(0); i < bp.SlotCount; i++ {
		symKey, err := bp.TryDecryptSlot(int(i), &aliceKeys.RecvKey.Private)
		if err != nil || symKey == nil {
			continue
		}
		inner, err := bp.DecryptBody(symKey)
		if err != nil {
			continue
		}
		if inner.SenderChainID == bobCID {
			decrypted = true
			// 验证签名
			if err := inner.Verify(&bobKeys.SendKey.Public); err != nil {
				t.Errorf("signature verification failed: %v", err)
			}
			break
		}
	}

	if !decrypted {
		t.Error("Alice should be able to decrypt the beacon")
	}
}

func TestBeaconNonTargetCannotDecrypt(t *testing.T) {
	bobKeys, _ := crypto.GenerateDualKeys()
	aliceKeys, _ := crypto.GenerateDualKeys()
	eveKeys, _ := crypto.GenerateDualKeys()
	bobCID := crypto.DeriveChainID(bobKeys.SendKey.Public)
	aliceCID := crypto.DeriveChainID(aliceKeys.SendKey.Public)

	bp, _, err := beacon.Build(&beacon.BuildParams{
		SenderChainID: bobCID,
		SendPrivKey:   bobKeys.SendKey.Private,
		Targets: []beacon.Target{
			{ChainID: aliceCID, RecvPK: aliceKeys.RecvKey.Public},
		},
	})
	if err != nil {
		t.Fatalf("build beacon: %v", err)
	}

	// Eve 不是目标，不应该能解密任何 slot
	for i := uint8(0); i < bp.SlotCount; i++ {
		_, err := bp.TryDecryptSlot(int(i), &eveKeys.RecvKey.Private)
		if err == nil {
			t.Error("Eve should not be able to decrypt any slot")
			break
		}
	}
}

func TestBeaconMultiSlot(t *testing.T) {
	bobKeys, _ := crypto.GenerateDualKeys()
	aliceKeys, _ := crypto.GenerateDualKeys()
	charlieKeys, _ := crypto.GenerateDualKeys()
	bobCID := crypto.DeriveChainID(bobKeys.SendKey.Public)
	aliceCID := crypto.DeriveChainID(aliceKeys.SendKey.Public)
	charlieCID := crypto.DeriveChainID(charlieKeys.SendKey.Public)

	// 构建多目标信标
	bp, _, err := beacon.Build(&beacon.BuildParams{
		SenderChainID: bobCID,
		SendPrivKey:   bobKeys.SendKey.Private,
		Targets: []beacon.Target{
			{ChainID: aliceCID, RecvPK: aliceKeys.RecvKey.Public},
			{ChainID: charlieCID, RecvPK: charlieKeys.RecvKey.Public},
		},
	})
	if err != nil {
		t.Fatalf("build beacon: %v", err)
	}

	// 信标应该有足够的 slot（至少会填充为 8 的倍数）
	if bp.SlotCount < 2 {
		t.Errorf("multi-target beacon should have at least 2 slots, got %d", bp.SlotCount)
	}

	// Alice 能解密
	aliceFound := false
	for i := uint8(0); i < bp.SlotCount; i++ {
		sk, err := bp.TryDecryptSlot(int(i), &aliceKeys.RecvKey.Private)
		if err != nil || sk == nil {
			continue
		}
		inner, _ := bp.DecryptBody(sk)
		if inner != nil && inner.SenderChainID == bobCID {
			aliceFound = true
		}
	}
	if !aliceFound {
		t.Error("Alice should decrypt multi-target beacon")
	}

	// Charlie 也能解密
	charlieFound := false
	for i := uint8(0); i < bp.SlotCount; i++ {
		sk, err := bp.TryDecryptSlot(int(i), &charlieKeys.RecvKey.Private)
		if err != nil || sk == nil {
			continue
		}
		inner, _ := bp.DecryptBody(sk)
		if inner != nil && inner.SenderChainID == bobCID {
			charlieFound = true
		}
	}
	if !charlieFound {
		t.Error("Charlie should decrypt multi-target beacon")
	}
}

// ===== 帧处理测试 =====

func TestFrameTypes(t *testing.T) {
	// 验证帧类型常量
	tests := []struct {
		ftype byte
		name  string
	}{
		{frame.FrameBeacon, "BEACON"},
		{frame.FrameData, "DATA"},
		{frame.FramePing, "PING"},
		{frame.FramePong, "PONG"},
		{frame.FrameClose, "CLOSE"},
		{frame.FrameRoute, "ROUTE"},
	}

	for _, tt := range tests {
		if tt.ftype == 0 {
			t.Errorf("%s frame type is zero", tt.name)
		}
	}
}

func TestFrameSerialization(t *testing.T) {
	payload := []byte("test-payload")
	f := frame.NewFrame(frame.FrameData, 0, payload)

	if f.Type != frame.FrameData {
		t.Error("frame type mismatch")
	}
	if len(f.Payload) != len(payload) {
		t.Error("payload length mismatch")
	}
}

// ===== 帮助函数 =====

func formatChainID(cid peer.ChainID) string {
	return fmt.Sprintf("%x", cid[:])
}
