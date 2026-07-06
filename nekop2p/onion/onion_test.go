package onion_test

import (
	"bytes"
	"crypto/rand"
	"testing"

	"github.com/nekop2p/nekop2p/crypto"
	"github.com/nekop2p/nekop2p/onion"
)

func TestOnionBuildAndUnwrapThreeHops(t *testing.T) {
	hops := make([]onion.Hop, 3)
	keys := make([]*crypto.DualKeys, 3)
	for i := range hops {
		k, _ := crypto.GenerateDualKeys()
		keys[i] = k
		hops[i].RecvPK = k.RecvKey.Public
		hops[i].IPv6 = [16]byte{byte(i + 1)}
		hops[i].Port = uint16(9070 + i)
	}

	target := onion.Target{
		IPv6: [16]byte{0x20, 0x01, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0xFF},
		Port: 8080,
	}

	message := []byte("onion routed secret message")

	var circuitID [16]byte
	rand.Read(circuitID[:])
	pkt, err := onion.Build(circuitID, hops, target, message)
	if err != nil {
		t.Fatalf("build: %v", err)
	}

	if pkt.PathLen != 3 {
		t.Fatalf("path len: %d", pkt.PathLen)
	}

	// 序列化并解析
	wire := pkt.Serialize()
	parsed, err := onion.ParseOnion(wire)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}

	current := parsed

	// 第 1 跳解密
	r1, err := onion.UnwrapOne(&keys[0].RecvKey.Private, current.Layers[0])
	if err != nil {
		t.Fatalf("hop1 unwrap: %v", err)
	}
	if r1.IsFinal {
		t.Fatal("hop1 should not be final")
	}
	if r1.NextHopIP != hops[1].IPv6 {
		t.Errorf("hop1 next hop IP mismatch")
	}

	// 第 2 跳解密
	r2, err := onion.UnwrapOne(&keys[1].RecvKey.Private, r1.Payload)
	if err != nil {
		t.Fatalf("hop2 unwrap: %v", err)
	}
	if r2.IsFinal {
		t.Fatal("hop2 should not be final")
	}

	// 第 3 跳解密（出口跳 → 递送到最终目标）
	r3, err := onion.UnwrapOne(&keys[2].RecvKey.Private, r2.Payload)
	if err != nil {
		t.Fatalf("hop3 unwrap: %v", err)
	}
	if !r3.IsFinal {
		t.Fatal("hop3 should be final")
	}
	if r3.FinalTarget.IPv6 != target.IPv6 {
		t.Errorf("final target IP mismatch")
	}
	if r3.FinalTarget.Port != target.Port {
		t.Errorf("final target port: got %d, want %d", r3.FinalTarget.Port, target.Port)
	}
	if !bytes.Equal(r3.FinalMsg, message) {
		t.Errorf("final message: got %q, want %q", r3.FinalMsg, message)
	}

	_ = current
}

func TestOnionFiveHops(t *testing.T) {
	hops := make([]onion.Hop, 5)
	keys := make([]*crypto.DualKeys, 5)
	for i := range hops {
		k, _ := crypto.GenerateDualKeys()
		keys[i] = k
		hops[i].RecvPK = k.RecvKey.Public
		hops[i].IPv6 = [16]byte{byte(i + 1)}
		hops[i].Port = 9070
	}

	target := onion.Target{IPv6: [16]byte{0xFF}, Port: 9999}
	msg := []byte("five hop test message")

	var cid [16]byte
	pkt, _ := onion.Build(cid, hops, target, msg)

	// 依次解密全部 5 跳
	current := pkt.Layers[0]
	for i := 0; i < 4; i++ {
		r, err := onion.UnwrapOne(&keys[i].RecvKey.Private, current)
		if err != nil {
			t.Fatalf("hop %d unwrap: %v", i, err)
		}
		if r.IsFinal {
			t.Fatalf("hop %d should not be final", i)
		}
		current = r.Payload
	}

	// 最终一跳
	r5, err := onion.UnwrapOne(&keys[4].RecvKey.Private, current)
	if err != nil {
		t.Fatalf("hop5 unwrap: %v", err)
	}
	if !r5.IsFinal {
		t.Fatal("hop5 should be final")
	}
	if !bytes.Equal(r5.FinalMsg, msg) {
		t.Errorf("final message: got %q, want %q", r5.FinalMsg, msg)
	}
}

func TestOnionSerialization(t *testing.T) {
	hops := make([]onion.Hop, 3)
	for i := range hops {
		k, _ := crypto.GenerateDualKeys()
		hops[i].RecvPK = k.RecvKey.Public
	}
	target := onion.Target{Port: 8080}
	msg := []byte("serialization roundtrip")

	var cid [16]byte
	original, _ := onion.Build(cid, hops, target, msg)
	wire := original.Serialize()

	parsed, err := onion.ParseOnion(wire)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}

	if parsed.PathLen != original.PathLen {
		t.Errorf("path len: %d vs %d", parsed.PathLen, original.PathLen)
	}
	if len(parsed.Layers) != len(original.Layers) {
		t.Errorf("layer count: %d vs %d", len(parsed.Layers), len(original.Layers))
	}
	for i := range parsed.Layers {
		if len(parsed.Layers[i]) != len(original.Layers[i]) {
			t.Errorf("layer %d size: %d vs %d", i, len(parsed.Layers[i]), len(original.Layers[i]))
		}
	}
}

func TestOnionWrongKey(t *testing.T) {
	hops := make([]onion.Hop, 3)
	keys := make([]*crypto.DualKeys, 3)
	for i := range hops {
		k, _ := crypto.GenerateDualKeys()
		keys[i] = k
		hops[i].RecvPK = k.RecvKey.Public
	}

	eve, _ := crypto.GenerateDualKeys()
	target := onion.Target{Port: 8080}

	var cid [16]byte
	pkt, _ := onion.Build(cid, hops, target, []byte("secret"))

	// Eve 尝试用自己的密钥解密
	_, err := onion.UnwrapOne(&eve.RecvKey.Private, pkt.Layers[0])
	if err == nil {
		t.Error("eve should not be able to unwrap")
	}
}

func TestOnionTooShortPath(t *testing.T) {
	hops := make([]onion.Hop, 2)
	for i := range hops {
		k, _ := crypto.GenerateDualKeys()
		hops[i].RecvPK = k.RecvKey.Public
	}
	target := onion.Target{Port: 8080}
	var cid [16]byte
	_, err := onion.Build(cid, hops, target, []byte("test"))
	if err == nil {
		t.Error("path of 2 hops should fail (min 3)")
	}
}

func TestSelector(t *testing.T) {
	keys := make([]*crypto.DualKeys, 10)
	friends := make([]onion.Hop, 5)
	publics := make([]onion.Hop, 5)
	for i := 0; i < 10; i++ {
		k, _ := crypto.GenerateDualKeys()
		keys[i] = k
		hop := onion.Hop{
			RecvPK:  k.RecvKey.Public,
			ChainID: k.SendKey.Public,
			IPv6:    [16]byte{byte(i)},
			Port:    9070,
		}
		if i < 5 {
			friends[i] = hop
		} else {
			publics[i-5] = hop
		}
	}

	s := onion.NewSelector(friends, publics)

	// 选择一条 3 跳路径
	path, err := s.SelectPath(3)
	if err != nil {
		t.Fatalf("select path: %v", err)
	}
	if len(path) != 3 {
		t.Errorf("path length: got %d, want 3", len(path))
	}

	// 入口应该是一个好友
	if path[0].ChainID != friends[0].ChainID &&
		path[0].ChainID != friends[1].ChainID &&
		path[0].ChainID != friends[2].ChainID &&
		path[0].ChainID != friends[3].ChainID &&
		path[0].ChainID != friends[4].ChainID {
		t.Error("entry hop should be a core friend")
	}
}

func TestBuildCircuit(t *testing.T) {
	hops := make([]onion.Hop, 3)
	keys := make([]*crypto.DualKeys, 3)
	for i := range hops {
		k, _ := crypto.GenerateDualKeys()
		keys[i] = k
		hops[i].RecvPK = k.RecvKey.Public
		hops[i].IPv6 = [16]byte{byte(i + 1)}
		hops[i].Port = 9070
	}

	target := onion.Target{IPv6: [16]byte{0xFF}, Port: 8080}
	msg := []byte("circuit test message")

	circuit, pkt, err := onion.BuildCircuit(hops, target, msg)
	if err != nil {
		t.Fatalf("build circuit: %v", err)
	}
	if circuit.ID == [16]byte{} {
		t.Error("circuit ID should not be zero")
	}
	if pkt.PathLen != 3 {
		t.Errorf("path len: %d", pkt.PathLen)
	}

	// 验证消息可以通过所有跳解密
	current := pkt.Layers[0]
	for i := 0; i < 2; i++ {
		r, _ := onion.UnwrapOne(&keys[i].RecvKey.Private, current)
		current = r.Payload
	}
	r3, _ := onion.UnwrapOne(&keys[2].RecvKey.Private, current)
	if !bytes.Equal(r3.FinalMsg, msg) {
		t.Error("circuit message corrupted")
	}
}
