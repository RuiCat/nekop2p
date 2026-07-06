package beacon_test

import (
	"bytes"
	"testing"

	"github.com/nekop2p/nekop2p/beacon"
	"github.com/nekop2p/nekop2p/crypto"
)

func TestBeaconBuildAndDecrypt(t *testing.T) {
	// 发送方密钥
	sender, _ := crypto.GenerateDualKeys()
	var senderChainID [32]byte
	copy(senderChainID[:], sender.SendKey.Public[:])

	// 目标方密钥
	target, _ := crypto.GenerateDualKeys()
	var targetChainID [32]byte
	copy(targetChainID[:], target.SendKey.Public[:])

	// 构建信标
	params := &beacon.BuildParams{
		SenderChainID: senderChainID,
		SendPrivKey:   sender.SendKey.Private,
		Targets: []beacon.Target{
			{ChainID: targetChainID, RecvPK: target.RecvKey.Public},
		},
	}

	bp, nonce, err := beacon.Build(params)
	if err != nil {
		t.Fatalf("build beacon: %v", err)
	}

	if len(nonce) != 32 {
		t.Errorf("nonce len: got %d, want 32", len(nonce))
	}
	if bp.HopCount != 0 {
		t.Errorf("hop count: got %d, want 0", bp.HopCount)
	}
	// 槽位计数应被填充到 8
	if bp.SlotCount != 8 {
		t.Errorf("slot count: got %d, want 8 (padded)", bp.SlotCount)
	}

	// 目标方解密
	for i := uint8(0); i < bp.SlotCount; i++ {
		symKey, err := bp.TryDecryptSlot(int(i), &target.RecvKey.Private)
		if err != nil {
			if i == 0 {
				t.Fatalf("target decrypt slot %d: %v", i, err)
			}
			continue // 填充槽位应解密失败
		}

		inner, err := bp.DecryptBody(symKey)
		if err != nil {
			t.Fatalf("decrypt body: %v", err)
		}

		if inner.SenderChainID != senderChainID {
			t.Errorf("sender chain ID mismatch")
		}
		break
	}
}

func TestBeaconPaddingSlots(t *testing.T) {
	sender, _ := crypto.GenerateDualKeys()
	target, _ := crypto.GenerateDualKeys()

	var sID, tID [32]byte
	copy(sID[:], sender.SendKey.Public[:])
	copy(tID[:], target.SendKey.Public[:])

	params := &beacon.BuildParams{
		SenderChainID: sID,
		SendPrivKey:   sender.SendKey.Private,
		Targets: []beacon.Target{
			{ChainID: tID, RecvPK: target.RecvKey.Public},
		},
	}

	bp, _, _ := beacon.Build(params)

	// 1 个真实目标 → 填充到 8 个槽位
	if bp.SlotCount != 8 {
		t.Errorf("expected 8 slots, got %d", bp.SlotCount)
	}

	// 只有槽位 0 应能解密
	for i := uint8(0); i < bp.SlotCount; i++ {
		_, err := bp.TryDecryptSlot(int(i), &target.RecvKey.Private)
		if i == 0 && err != nil {
			t.Errorf("slot 0 should decrypt: %v", err)
		}
	}
}

func TestBeaconInvalidSlot(t *testing.T) {
	sender, _ := crypto.GenerateDualKeys()
	target, _ := crypto.GenerateDualKeys()

	var sID, tID [32]byte
	copy(sID[:], sender.SendKey.Public[:])
	copy(tID[:], target.SendKey.Public[:])

	params := &beacon.BuildParams{
		SenderChainID: sID,
		SendPrivKey:   sender.SendKey.Private,
		Targets: []beacon.Target{
			{ChainID: tID, RecvPK: target.RecvKey.Public},
		},
	}

	bp, _, _ := beacon.Build(params)

	// 尝试一个越界的槽位索引
	_, err := bp.TryDecryptSlot(100, &target.RecvKey.Private)
	if err == nil {
		t.Error("out of range slot should fail")
	}
}

func TestBeaconSerializeRoundtrip(t *testing.T) {
	sender, _ := crypto.GenerateDualKeys()
	target, _ := crypto.GenerateDualKeys()

	var sID, tID [32]byte
	copy(sID[:], sender.SendKey.Public[:])
	copy(tID[:], target.SendKey.Public[:])

	params := &beacon.BuildParams{
		SenderChainID: sID,
		SendPrivKey:   sender.SendKey.Private,
		Targets: []beacon.Target{
			{ChainID: tID, RecvPK: target.RecvKey.Public},
		},
	}

	original, _, _ := beacon.Build(params)
	data := original.Serialize()

	parsed, err := beacon.ParseBeacon(data)
	if err != nil {
		t.Fatalf("parse beacon: %v", err)
	}

	if parsed.HopCount != original.HopCount {
		t.Errorf("hop count mismatch")
	}
	if parsed.SlotCount != original.SlotCount {
		t.Errorf("slot count mismatch")
	}
	if !bytes.Equal(parsed.RandomTag[:], original.RandomTag[:]) {
		t.Errorf("random tag mismatch")
	}

	// 验证其仍可被解密
	symKey, err := parsed.TryDecryptSlot(0, &target.RecvKey.Private)
	if err != nil {
		t.Fatalf("decrypt after parse: %v", err)
	}
	inner, err := parsed.DecryptBody(symKey)
	if err != nil {
		t.Fatalf("decrypt body after parse: %v", err)
	}
	if inner.SenderChainID != sID {
		t.Errorf("sender chain ID mismatch after roundtrip")
	}
}

func TestBeaconMultiTarget(t *testing.T) {
	sender, _ := crypto.GenerateDualKeys()
	t1, _ := crypto.GenerateDualKeys()
	t2, _ := crypto.GenerateDualKeys()
	t3, _ := crypto.GenerateDualKeys()

	var sID, id1, id2, id3 [32]byte
	copy(sID[:], sender.SendKey.Public[:])
	copy(id1[:], t1.SendKey.Public[:])
	copy(id2[:], t2.SendKey.Public[:])
	copy(id3[:], t3.SendKey.Public[:])

	params := &beacon.BuildParams{
		SenderChainID: sID,
		SendPrivKey:   sender.SendKey.Private,
		Targets: []beacon.Target{
			{ChainID: id1, RecvPK: t1.RecvKey.Public},
			{ChainID: id2, RecvPK: t2.RecvKey.Public},
			{ChainID: id3, RecvPK: t3.RecvKey.Public},
		},
	}

	bp, _, _ := beacon.Build(params)

	// 3 个目标 → 填充到 8 个槽位
	if bp.SlotCount != 8 {
		t.Errorf("expected 8 slots, got %d", bp.SlotCount)
	}

	// 每个目标应能在不同槽位中解密
	targets := []crypto.DualKeys{*t1, *t2, *t3}
	found := make(map[int]bool)

	for i, target := range targets {
		for j := uint8(0); j < bp.SlotCount; j++ {
			_, err := bp.TryDecryptSlot(int(j), &target.RecvKey.Private)
			if err == nil {
				found[i] = true
				break
			}
		}
		if !found[i] {
			t.Errorf("target %d not found in any slot", i)
		}
	}
}
