// Package integration_test 端到端测试完整的信标发现流程。
package tests

import (
	"testing"

	"github.com/nekop2p/nekop2p/beacon"
	"github.com/nekop2p/nekop2p/crypto"
	"github.com/nekop2p/nekop2p/frame"
	"github.com/nekop2p/nekop2p/noise"
)

// TestBeaconDiscoveryFlow 测试完整流程：
// Bob 构建信标 → Alice 解密 → Alice 响应 → Bob 验证响应
func TestBeaconDiscoveryFlow(t *testing.T) {
	// 为双方生成密钥
	bob, _ := crypto.GenerateDualKeys()
	alice, _ := crypto.GenerateDualKeys()

	// Bob 的链 ID
	var bobChainID [32]byte
	copy(bobChainID[:], bob.SendKey.Public[:])
	var aliceChainID [32]byte
	copy(aliceChainID[:], alice.SendKey.Public[:])

	// === 阶段 1：Bob 构建以 Alice 为目标的信标 ===
	params := &beacon.BuildParams{
		SenderChainID: bobChainID,
		SendPrivKey:   bob.SendKey.Private,
		Targets: []beacon.Target{
			{ChainID: aliceChainID, RecvPK: alice.RecvKey.Public},
		},
	}

	bp, nonce, err := beacon.Build(params)
	if err != nil {
		t.Fatalf("build beacon: %v", err)
	}

	// 序列化为线格式（模拟网络传输）
	wireBeacon := bp.Serialize()

	// === 阶段 2：Alice 接收并解密信标 ===
	received, err := beacon.ParseBeacon(wireBeacon)
	if err != nil {
		t.Fatalf("parse beacon: %v", err)
	}

	var symKey []byte
	for i := uint8(0); i < received.SlotCount; i++ {
		sk, err := received.TryDecryptSlot(int(i), &alice.RecvKey.Private)
		if err == nil {
			symKey = sk
			break
		}
	}
	if symKey == nil {
		t.Fatal("alice could not decrypt any slot")
	}

	inner, err := received.DecryptBody(symKey)
	if err != nil {
		t.Fatalf("decrypt body: %v", err)
	}

	if inner.SenderChainID != bobChainID {
		t.Errorf("sender chain ID mismatch")
	}

	// === 阶段 3：Alice 构建响应 ===
	aliceEph, _ := crypto.GenerateEphemeralKey()
	var aliceIPv6 [16]byte
	copy(aliceIPv6[:], []byte{0x20, 0x01, 0x0d, 0xb8, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 1})
	var aliceCID [32]byte
	copy(aliceCID[:], aliceChainID[:])

	resp, err := beacon.BuildResponse(inner, aliceCID, aliceIPv6, 9070, aliceEph.Public, alice.SendKey.Private, &bob.RecvKey.Public)
	if err != nil {
		t.Fatalf("build response: %v", err)
	}

	// 直接使用已加密响应
	encryptedPayload := resp.EncryptedPayload

	// === 阶段 4：Bob 验证响应 ===
	var expectedNonce [32]byte
	copy(expectedNonce[:], nonce)

	innerResp, err := beacon.VerifyResponse(encryptedPayload, &bob.RecvKey.Private, &alice.SendKey.Public, expectedNonce)
	if err != nil {
		t.Fatalf("verify response: %v", err)
	}

	if innerResp.ResponderPort != 9070 {
		t.Errorf("port: got %d, want 9070", innerResp.ResponderPort)
	}
	if innerResp.EphemeralRatchetPK != aliceEph.Public {
		t.Errorf("ratchet PK mismatch")
	}

	// === 阶段 5：Bob 现在可以建立对等连接 ===
	_ = innerResp // 验证通过：Bob 拥有 Alice 的 IPv6、端口和棘轮密钥
}

// TestBeaconRelayFlow 测试信标中继（一个节点转发给另一个节点）。
func TestBeaconRelayFlow(t *testing.T) {
	bob, _ := crypto.GenerateDualKeys()
	alice, _ := crypto.GenerateDualKeys()
	_, _ = crypto.GenerateDualKeys() // 中继节点（测试中未使用）

	var bobCID, aliceCID [32]byte
	copy(bobCID[:], bob.SendKey.Public[:])
	copy(aliceCID[:], alice.SendKey.Public[:])

	// Bob 构建以 Alice 为目标的信标
	params := &beacon.BuildParams{
		SenderChainID: bobCID,
		SendPrivKey:   bob.SendKey.Private,
		Targets: []beacon.Target{
			{ChainID: aliceCID, RecvPK: alice.RecvKey.Public},
		},
	}

	bp, _, _ := beacon.Build(params)

	// 中继节点接收信标，无法解密，递增跳数
	bp.HopCount = 1
	relayWire := bp.Serialize()

	// 中继转发（模拟）
	forwarded, _ := beacon.ParseBeacon(relayWire)
	if forwarded.HopCount != 1 {
		t.Errorf("relay hop count: got %d, want 1", forwarded.HopCount)
	}
	forwarded.HopCount = 2

	// Alice 接收转发的信标
	aliceWire := forwarded.Serialize()
	received, _ := beacon.ParseBeacon(aliceWire)

	var symKey []byte
	for i := uint8(0); i < received.SlotCount; i++ {
		sk, err := received.TryDecryptSlot(int(i), &alice.RecvKey.Private)
		if err == nil {
			symKey = sk
			break
		}
	}
	if symKey == nil {
		t.Fatal("alice could not decrypt relayed beacon")
	}

	inner, _ := received.DecryptBody(symKey)
	if inner.SenderChainID != bobCID {
		t.Errorf("sender mismatch through relay")
	}
}

// TestNoiseFrameIntegration 测试 Noise 握手与帧传输的联合运行。
func TestNoiseFrameIntegration(t *testing.T) {
	bobKey, _ := crypto.GenerateEphemeralKey()
	aliceKey, _ := crypto.GenerateEphemeralKey()

	// IK 握手
	bobHS := noise.NewInitiatorIK(bobKey, &aliceKey.Public, noise.RoleFriend)
	aliceHS := noise.NewResponderIK(aliceKey, [32]byte{}, noise.RoleFriend)

	msg1, _ := bobHS.WriteMessage(nil)
	aliceHS.ReadMessage(msg1)
	msg2, _ := aliceHS.WriteMessage(nil)
	bobHS.ReadMessage(msg2)

	bobResult := bobHS.Complete()
	aliceResult := aliceHS.Complete()

	// 从 Noise 输出创建帧会话
	// Bob：发送=Bob.SendCipher，接收=Bob.RecvCipher
	bobSend := bobResult.SendCipher.Key
	bobRecv := bobResult.RecvCipher.Key
	bobFramer := frame.NewSessionKeys(bobSend[:], bobRecv[:])

	// Alice：发送=Alice.SendCipher，接收=Alice.RecvCipher
	aliceSend := aliceResult.SendCipher.Key
	aliceRecv := aliceResult.RecvCipher.Key
	aliceFramer := frame.NewSessionKeys(aliceSend[:], aliceRecv[:])

	// 通过帧测试消息往返
	payload := []byte("encrypted frame over noise transport")
	f := frame.NewFrame(frame.FrameData, 0, payload)

	var buf mockBuffer
	if err := bobFramer.WriteEncryptedFrame(&buf, f); err != nil {
		t.Fatalf("bob write frame: %v", err)
	}

	got, err := aliceFramer.ReadEncryptedFrame(&buf)
	if err != nil {
		t.Fatalf("alice read frame: %v", err)
	}
	if string(got.Payload) != string(payload) {
		t.Errorf("roundtrip: got %q, want %q", got.Payload, payload)
	}
}

// mockBuffer 实现 io.Reader 和 io.Writer 接口，用于测试。
type mockBuffer struct {
	buf []byte
	pos int
}

func (m *mockBuffer) Write(p []byte) (int, error) {
	m.buf = append(m.buf, p...)
	return len(p), nil
}

func (m *mockBuffer) Read(p []byte) (int, error) {
	if m.pos >= len(m.buf) {
		return 0, nil
	}
	n := copy(p, m.buf[m.pos:])
	m.pos += n
	return n, nil
}
