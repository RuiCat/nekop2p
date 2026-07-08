package live_test

import (
	"encoding/binary"
	"fmt"
	"net"
	"testing"
	"time"

	"github.com/nekop2p/nekop2p/beacon"
	"github.com/nekop2p/nekop2p/crypto"
	"github.com/nekop2p/nekop2p/frame"
	"github.com/nekop2p/nekop2p/noise"
	"github.com/nekop2p/nekop2p/ratchet"
)

// TestFullDiscoveryFlow 通过 localhost 上两个节点之间的真实 TCP 连接，
// 测试完整的信标发现和消息传递流程。
func TestFullDiscoveryFlow(t *testing.T) {
	// 生成身份
	bobKeys, _ := crypto.GenerateDualKeys()
	aliceKeys, _ := crypto.GenerateDualKeys()

	var bobCID, aliceCID [32]byte
	copy(bobCID[:], bobKeys.SendKey.Public[:])
	copy(aliceCID[:], aliceKeys.SendKey.Public[:])

	// === Start Alice's listener ===
	aliceLn, err := net.Listen("tcp6", "[::1]:0") // 随机端口
	if err != nil {
		t.Skipf("IPv6 not available: %v", err)
		return
	}
	defer aliceLn.Close()
	aliceAddr := aliceLn.Addr().(*net.TCPAddr)

	t.Logf("Alice listening on [::1]:%d", aliceAddr.Port)

	// Alice 收到信标的通知 channel
	aliceReady := make(chan *beacon.InnerPayload, 1)
	aliceDone := make(chan struct{})

	// Alice: 接受循环
	go func() {
		conn, err := aliceLn.Accept()
		if err != nil {
			return
		}
		defer conn.Close()

		// Noise_IK 响应方（Bob 是好友）
		hs, _ := noise.NewResponderIK(&aliceKeys.RecvKey, bobKeys.RecvKey.Public, noise.RoleFriend)
		msgBuf := make([]byte, 4096)
		nr, _ := conn.Read(msgBuf)
		if _, err := hs.ReadMessage(msgBuf[:nr]); err != nil {
			t.Logf("Alice IK read: %v", err)
			return
		}

		// 发送 IK msg2
		msg2, _ := hs.WriteMessage(nil)
		conn.Write(msg2)

		result := hs.Complete()
		sendKey := result.SendCipher.Key
		recvKey := result.RecvCipher.Key
		framer := frame.NewSessionKeys(sendKey[:], recvKey[:])

		// 读取帧
		for {
			f, err := framer.ReadEncryptedFrame(conn)
			if err != nil {
				return
			}

			switch f.Type {
			case frame.FrameBeacon:
				bp, err := beacon.ParseBeacon(f.Payload)
				if err != nil {
					continue
				}

				// 尝试解密每个 slot
				for i := uint8(0); i < bp.SlotCount; i++ {
					symKey, err := bp.TryDecryptSlot(int(i), &aliceKeys.RecvKey.Private)
					if err != nil {
						continue
					}
					inner, err := bp.DecryptBody(symKey)
					if err != nil {
						continue
					}
					if inner.SenderChainID == bobCID {
						aliceReady <- inner
						close(aliceDone)
						return
					}
				}
			}
		}
	}()

	// === Bob: 连接到 Alice 并发送信标 ===
	bobConn, err := net.DialTimeout("tcp6", fmt.Sprintf("[::1]:%d", aliceAddr.Port), 5*time.Second)
	if err != nil {
		t.Fatalf("Bob dial: %v", err)
	}
	defer bobConn.Close()

	// Noise_IK 发起方
	bobHS := noise.NewInitiatorIK(&bobKeys.RecvKey, &aliceKeys.RecvKey.Public, noise.RoleFriend)
	msg1, _ := bobHS.WriteMessage(nil)
	bobConn.Write(msg1)

	msgBuf := make([]byte, 4096)
	nr, _ := bobConn.Read(msgBuf)
	bobHS.ReadMessage(msgBuf[:nr])
	bobResult := bobHS.Complete()

	bobFramer := frame.NewSessionKeys(bobResult.SendCipher.Key[:], bobResult.RecvCipher.Key[:])

	// 构建针对 Alice 的信标
	params := &beacon.BuildParams{
		SenderChainID: bobCID,
		SendPrivKey:   bobKeys.SendKey.Private,
		Targets: []beacon.Target{
			{ChainID: aliceCID, RecvPK: aliceKeys.RecvKey.Public},
		},
	}
	bp, _, err := beacon.Build(params)
	if err != nil {
		t.Fatalf("Bob build beacon: %v", err)
	}

	// 以帧格式发送信标
	beaconFrame := frame.NewFrame(frame.FrameBeacon, 0, bp.Serialize())
	if err := bobFramer.WriteEncryptedFrame(bobConn, beaconFrame); err != nil {
		t.Fatalf("Bob send beacon frame: %v", err)
	}

	// 等待 Alice 解密
	select {
	case inner := <-aliceReady:
		t.Logf("Alice received beacon from %x", inner.SenderChainID[:8])

		// Alice 发送响应（模拟 — 真实代码中这将是 UDP 响应）
		aliceEph, _ := crypto.GenerateEphemeralKey()
		var aliceIPv6 [16]byte
		var aliceCIDArr [32]byte
		copy(aliceCIDArr[:], aliceCID[:])

		resp, _ := beacon.BuildResponse(inner, aliceCIDArr, aliceIPv6, 9070, aliceEph.Public, aliceKeys.SendKey.Private, &bobKeys.RecvKey.Public)

		// Bob 验证 (载荷已由 BuildResponse 内部加密)

		// Bob 验证
		var expectedNonce [32]byte
		copy(expectedNonce[:], inner.BeaconNonce[:])
		innerResp, err := beacon.VerifyResponse(resp.EncryptedPayload, &bobKeys.RecvKey.Private, &aliceKeys.SendKey.Public, expectedNonce)
		if err != nil {
			t.Fatalf("Bob verify response: %v", err)
		}
		t.Logf("Bob verified response from Alice, ratchet PK: %x", innerResp.EphemeralRatchetPK[:8])

		// === Phase 2: Double Ratchet messaging ===
		bobRatchet, bobEphPK, _ := ratchet.InitAsInitiator(&bobKeys.RecvKey.Private, &aliceKeys.RecvKey.Public, &aliceEph.Public)
		aliceRatchet, err := ratchet.InitAsResponder(&aliceKeys.RecvKey.Private, &bobKeys.RecvKey.Public, &aliceEph.Private, &bobEphPK)
		if err != nil {
			t.Fatal(err)
		}

		// Bob → Alice
		wire1, _ := bobRatchet.Encrypt([]byte("hello alice, let's build something"))
		dec1, _ := aliceRatchet.Decrypt(wire1)
		t.Logf("Alice received: %q", string(dec1))

		// Alice → Bob
		wire2, _ := aliceRatchet.Encrypt([]byte("hello bob, the network is alive!"))
		dec2, _ := bobRatchet.Decrypt(wire2)
		t.Logf("Bob received: %q", string(dec2))

		if string(dec1) != "hello alice, let's build something" {
			t.Error("message 1 corrupted")
		}
		if string(dec2) != "hello bob, the network is alive!" {
			t.Error("message 2 corrupted")
		}

	case <-time.After(10 * time.Second):
		t.Fatal("timeout waiting for Alice to receive beacon")
	}

	<-aliceDone
}

// TestThreeNodeRelay 通过中间节点测试信标中继。
func TestThreeNodeRelay(t *testing.T) {
	bobKeys, _ := crypto.GenerateDualKeys()
	aliceKeys, _ := crypto.GenerateDualKeys()
	relayKeys, _ := crypto.GenerateDualKeys()

	var bobCID, aliceCID [32]byte
	copy(bobCID[:], bobKeys.SendKey.Public[:])
	copy(aliceCID[:], aliceKeys.SendKey.Public[:])

	// 启动 Alice
	aliceLn, err := net.Listen("tcp6", "[::1]:0")
	if err != nil {
		t.Skipf("IPv6 not available: %v", err)
		return
	}
	defer aliceLn.Close()
	aliceAddr := aliceLn.Addr().(*net.TCPAddr)

	// 启动中继
	relayLn, _ := net.Listen("tcp6", "[::1]:0")
	defer relayLn.Close()
	relayAddr := relayLn.Addr().(*net.TCPAddr)

	beaconReceived := make(chan *beacon.BeaconPacket, 1)

	// Alice: 接受来自中继的连接
		go func() {
		conn, _ := aliceLn.Accept()
		defer conn.Close()
		hs, _ := noise.NewResponderIK(&aliceKeys.RecvKey, relayKeys.RecvKey.Public, noise.RoleFriend)
		buf := make([]byte, 4096)
		nr, _ := conn.Read(buf)
		hs.ReadMessage(buf[:nr])
		msg2, _ := hs.WriteMessage(nil)
		conn.Write(msg2)
		result := hs.Complete()
		framer := frame.NewSessionKeys(result.SendCipher.Key[:], result.RecvCipher.Key[:])

		for {
			f, err := framer.ReadEncryptedFrame(conn)
			if err != nil {
				return
			}
			if f.Type == frame.FrameBeacon {
				bp, _ := beacon.ParseBeacon(f.Payload)
				for i := uint8(0); i < bp.SlotCount; i++ {
					symKey, err := bp.TryDecryptSlot(int(i), &aliceKeys.RecvKey.Private)
					if err == nil {
						inner, _ := bp.DecryptBody(symKey)
						if inner.SenderChainID == bobCID {
							beaconReceived <- bp
							return
						}
					}
				}
			}
		}
	}()

	// 中继: 接受来自 Bob 的连接，转发给 Alice
	go func() {
		conn, _ := relayLn.Accept()
		defer conn.Close()
		hs, _ := noise.NewResponderIK(&relayKeys.RecvKey, bobKeys.RecvKey.Public, noise.RoleFriend)
		buf := make([]byte, 4096)
		nr, _ := conn.Read(buf)
		hs.ReadMessage(buf[:nr])
		msg2, _ := hs.WriteMessage(nil)
		conn.Write(msg2)

		result := hs.Complete()
		framer := frame.NewSessionKeys(result.SendCipher.Key[:], result.RecvCipher.Key[:])

		for {
			f, err := framer.ReadEncryptedFrame(conn)
			if err != nil {
				return
			}
			if f.Type == frame.FrameBeacon {
				bp, _ := beacon.ParseBeacon(f.Payload)

				// 中继: 无法解密（不是目标），所以转发
				bp.HopCount++
				relayFrame := frame.NewFrame(frame.FrameBeacon, 0, bp.Serialize())

				// 连接到 Alice 并转发
				aConn, _ := net.DialTimeout("tcp6", fmt.Sprintf("[::1]:%d", aliceAddr.Port), 5*time.Second)
				if aConn == nil {
					return
				}
				defer aConn.Close()

				relayHS := noise.NewInitiatorIK(&relayKeys.RecvKey, &aliceKeys.RecvKey.Public, noise.RoleFriend)
				rm1, _ := relayHS.WriteMessage(nil)
				aConn.Write(rm1)
				rbuf := make([]byte, 4096)
				rn, _ := aConn.Read(rbuf)
				relayHS.ReadMessage(rbuf[:rn])
				rResult := relayHS.Complete()
				rFramer := frame.NewSessionKeys(rResult.SendCipher.Key[:], rResult.RecvCipher.Key[:])
				rFramer.WriteEncryptedFrame(aConn, relayFrame)
				return
			}
		}
	}()

	// Bob: 连接到中继，发送信标
	bobConn, _ := net.DialTimeout("tcp6", fmt.Sprintf("[::1]:%d", relayAddr.Port), 5*time.Second)
	defer bobConn.Close()

	bobHS := noise.NewInitiatorIK(&bobKeys.RecvKey, &relayKeys.RecvKey.Public, noise.RoleFriend)
	bm1, _ := bobHS.WriteMessage(nil)
	bobConn.Write(bm1)
	bbuf := make([]byte, 4096)
	bn, _ := bobConn.Read(bbuf)
	bobHS.ReadMessage(bbuf[:bn])
	bobResult := bobHS.Complete()
	bobFramer := frame.NewSessionKeys(bobResult.SendCipher.Key[:], bobResult.RecvCipher.Key[:])

	params := &beacon.BuildParams{
		SenderChainID: bobCID,
		SendPrivKey:   bobKeys.SendKey.Private,
		Targets: []beacon.Target{
			{ChainID: aliceCID, RecvPK: aliceKeys.RecvKey.Public},
		},
	}
	bp, _, _ := beacon.Build(params)
	bf := frame.NewFrame(frame.FrameBeacon, 0, bp.Serialize())
	bobFramer.WriteEncryptedFrame(bobConn, bf)

	select {
	case received := <-beaconReceived:
		if received.HopCount < 1 {
			t.Error("relayed beacon should have hop_count >= 1")
		}
		t.Logf("Beacon reached Alice through relay! hops: %d", received.HopCount)
	case <-time.After(10 * time.Second):
		t.Fatal("timeout: beacon did not reach Alice through relay")
	}
}

var _ = binary.BigEndian // 抑制未使用的导入告警
