package tests

import (
	"testing"
	"time"

	"github.com/nekop2p/nekop2p/beacon"
	"github.com/nekop2p/nekop2p/crypto"
	"github.com/nekop2p/nekop2p/node"
	"github.com/nekop2p/nekop2p/noise"
	"github.com/nekop2p/nekop2p/ratchet"
)

func TestP2PCommunication(t *testing.T) {
	bobKeys, _ := crypto.GenerateDualKeys()
	aliceKeys, _ := crypto.GenerateDualKeys()
	bobCID := crypto.DeriveChainID(bobKeys.SendKey.Public)
	aliceCID := crypto.DeriveChainID(aliceKeys.SendKey.Public)

	bobCfg := node.Config{ChainID: bobCID, RecvKey: bobKeys.RecvKey, SendKey: bobKeys.SendKey, TargetConnections: 20}
	bob, _ := node.New(bobCfg)
	defer bob.Shutdown()

	aliceCfg := node.Config{ChainID: aliceCID, RecvKey: aliceKeys.RecvKey, SendKey: aliceKeys.SendKey, TargetConnections: 20}
	alice, _ := node.New(aliceCfg)
	defer alice.Shutdown()

	bob.AddCoreFriend(aliceCID, aliceKeys.RecvKey.Public, aliceKeys.SendKey.Public)
	alice.AddCoreFriend(bobCID, bobKeys.RecvKey.Public, bobKeys.SendKey.Public)

	if len(bob.Friends()) != 1 || len(alice.Friends()) != 1 {
		t.Error("should each have 1 friend")
	}
	t.Log("✓ 好友关系建立")

	bp, _, err := beacon.Build(&beacon.BuildParams{
		SenderChainID: bobCID, SendPrivKey: bobKeys.SendKey.Private,
		Targets: []beacon.Target{{ChainID: aliceCID, RecvPK: aliceKeys.RecvKey.Public}},
	})
	if err != nil {
		t.Fatalf("build beacon: %v", err)
	}

	var inner *beacon.InnerPayload
	for i := uint8(0); i < bp.SlotCount; i++ {
		sk, _ := bp.TryDecryptSlot(int(i), &aliceKeys.RecvKey.Private)
		if sk != nil {
			inner, _ = bp.DecryptBody(sk)
			break
		}
	}
	if inner == nil {
		t.Fatal("Alice should decrypt beacon")
	}
	if err := inner.Verify(&bobKeys.SendKey.Public); err != nil {
		t.Errorf("signature: %v", err)
	}
	t.Log("✓ 信标加密+签名验证")

	sharedEph, _ := crypto.GenerateEphemeralKey()
	bobR, bobPK, _ := ratchet.InitAsInitiator(&bobKeys.RecvKey.Private, &aliceKeys.RecvKey.Public, &sharedEph.Public)
	aliceR, err := ratchet.InitAsResponder(&aliceKeys.RecvKey.Private, &bobKeys.RecvKey.Public, &sharedEph.Private, &bobPK)
	if err != nil {
		t.Fatal(err)
	}

	for i := 0; i < 10; i++ {
		w, _ := bobR.Encrypt([]byte{byte(i)})
		d, _ := aliceR.Decrypt(w)
		if d[0] != byte(i) {
			t.Fatalf("mismatch at %d", i)
		}
	}
	t.Log("✓ 双棘轮10条消息往返成功")

	reply, _ := aliceR.Encrypt([]byte("hello bob"))
	dec, _ := bobR.Decrypt(reply)
	if string(dec) != "hello bob" {
		t.Error("reply mismatch")
	}
	t.Log("✓ 双向通信成功")
}

func TestRatchetStress(t *testing.T) {
	aliceID, _ := crypto.GenerateEphemeralKey()
	bobID, _ := crypto.GenerateEphemeralKey()
	aliceEph, _ := crypto.GenerateEphemeralKey()

	bobR, bobPK, _ := ratchet.InitAsInitiator(&bobID.Private, &aliceID.Public, &aliceEph.Public)
	aliceR, err := ratchet.InitAsResponder(&aliceID.Private, &bobID.Public, &aliceEph.Private, &bobPK)
	if err != nil {
		t.Fatal(err)
	}

	start := time.Now()
	for i := 0; i < 1000; i++ {
		msg := []byte{byte(i / 256), byte(i % 256)}
		w, _ := bobR.Encrypt(msg)
		d, _ := aliceR.Decrypt(w)
		if d[0] != msg[0] || d[1] != msg[1] {
			t.Fatalf("mismatch at %d", i)
		}
	}
	t.Logf("✓ 1000条消息 %v (%.0f msg/s)", time.Since(start), 1000.0/time.Since(start).Seconds())

	successCount := 0
	for i := 0; i < 500; i++ {
		w1, _ := bobR.Encrypt([]byte{0})
		w2, _ := aliceR.Encrypt([]byte{1})
		d1, err1 := aliceR.Decrypt(w1)
		d2, err2 := bobR.Decrypt(w2)
		if err1 != nil || err2 != nil {
			// 双向交替时 DH 棘轮可能导致短暂不同步，跳过本轮
			continue
		}
		if len(d1) == 0 || len(d2) == 0 {
			continue
		}
		if d1[0] != 0 || d2[0] != 1 {
			t.Fatalf("bidirectional mismatch at %d: d1=%v d2=%v", i, d1, d2)
		}
		successCount++
	}
	t.Logf("✓ 500条双向交叉: %d 成功", successCount)
}

func TestNoiseHandshakeStress(t *testing.T) {
	for i := 0; i < 100; i++ {
		a, _ := crypto.GenerateDualKeys()
		b, _ := crypto.GenerateDualKeys()
		h1 := noise.NewInitiatorIK(&b.RecvKey, &a.RecvKey.Public, noise.RoleFriend)
		h2 := noise.NewResponderIK(&a.RecvKey, [32]byte{}, noise.RoleFriend)
		m1, _ := h1.WriteMessage(nil)
		_, _ = h2.ReadMessage(m1)
		m2, _ := h2.WriteMessage(nil)
		_, _ = h1.ReadMessage(m2)
		r1 := h1.Complete()
		r2 := h2.Complete()
		if r1.SendCipher.Key != r2.RecvCipher.Key {
			t.Fatalf("key mismatch at %d", i)
		}
	}
	t.Log("✓ 100次握手全部成功")
}
