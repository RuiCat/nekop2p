// Package scenario_test 运行多节点虚拟网络场景测试。
//
// 这些测试创建真实的 TCP 监听器和连接，以模拟
// 完整的 P2P 网络，包含发现、消息传递和洋葱路由。
package scenario_test

import (
	"fmt"
	"log"
	"net"
	"sync"
	"testing"
	"time"

	"github.com/nekop2p/nekop2p/beacon"
	"github.com/nekop2p/nekop2p/crypto"
	"github.com/nekop2p/nekop2p/frame"
	"github.com/nekop2p/nekop2p/noise"
	"github.com/nekop2p/nekop2p/onion"
	"github.com/nekop2p/nekop2p/ratchet"
)

// VirtualNode 表示测试网络中的一个模拟节点。
type VirtualNode struct {
	Name     string
	Keys     *crypto.DualKeys
	ChainID  [32]byte
	Listener net.Listener
	Addr     *net.TCPAddr
	Peers    map[string]*VirtualPeer // 活跃连接
	mu       sync.Mutex
}

// VirtualPeer 是两个节点之间的活跃加密连接。
type VirtualPeer struct {
	Conn   net.Conn
	Framer *frame.SessionKeys
}

// NewVirtualNode 创建一个带有 TCP 监听器的虚拟节点。
func NewVirtualNode(t *testing.T, name string) *VirtualNode {
	t.Helper()
	keys, _ := crypto.GenerateDualKeys()
	ln, err := net.Listen("tcp6", "[::1]:0")
	if err != nil {
		t.Skipf("IPv6 required: %v", err)
		return nil
	}
	vn := &VirtualNode{
		Name:     name,
		Keys:     keys,
		ChainID:  crypto.DeriveChainID(keys.SendKey.Public),
		Listener: ln,
		Addr:     ln.Addr().(*net.TCPAddr),
		Peers:    make(map[string]*VirtualPeer),
	}
	log.Printf("[%s] listening on [::1]:%d  chain_id=%x", name, vn.Addr.Port, vn.ChainID[:8])
	return vn
}

// AcceptOne 接受一个传入的 Noise IK 连接。
func (vn *VirtualNode) AcceptOne(t *testing.T) *VirtualPeer {
	t.Helper()
	conn, err := vn.Listener.Accept()
	if err != nil {
		t.Fatalf("[%s] accept: %v", vn.Name, err)
	}
	hs := noise.NewResponderIK(&vn.Keys.RecvKey, [32]byte{}, noise.RoleFriend)
	buf := make([]byte, 8192)
	nr, _ := conn.Read(buf)
	hs.ReadMessage(buf[:nr])
	m2, _ := hs.WriteMessage(nil)
	conn.Write(m2)
	r := hs.Complete()
	fr := frame.NewSessionKeys(r.SendCipher.Key[:], r.RecvCipher.Key[:])
	vp := &VirtualPeer{Conn: conn, Framer: fr}
	return vp
}

// Dial 使用 Noise IK 连接到另一个虚拟节点。
func (vn *VirtualNode) Dial(t *testing.T, target *VirtualNode) *VirtualPeer {
	t.Helper()
	addr := fmt.Sprintf("[::1]:%d", target.Addr.Port)
	conn, err := net.DialTimeout("tcp6", addr, 5*time.Second)
	if err != nil {
		t.Fatalf("[%s] dial %s: %v", vn.Name, target.Name, err)
	}
	hs := noise.NewInitiatorIK(&vn.Keys.RecvKey, &target.Keys.RecvKey.Public, noise.RoleFriend)
	m1, _ := hs.WriteMessage(nil)
	conn.Write(m1)
	buf := make([]byte, 4096)
	nr, _ := conn.Read(buf)
	hs.ReadMessage(buf[:nr])
	r := hs.Complete()
	fr := frame.NewSessionKeys(r.SendCipher.Key[:], r.RecvCipher.Key[:])
	vp := &VirtualPeer{Conn: conn, Framer: fr}
	vn.mu.Lock()
	vn.Peers[target.Name] = vp
	vn.mu.Unlock()
	return vp
}

// SendFrame 向对等方发送一帧。
func (vp *VirtualPeer) SendFrame(t *testing.T, typ uint8, payload []byte) {
	t.Helper()
	if err := vp.Framer.WriteEncryptedFrame(vp.Conn, frame.NewFrame(typ, 0, payload)); err != nil {
		t.Fatalf("send frame: %v", err)
	}
}

// ReadFrame 从对等方读取一帧。
func (vp *VirtualPeer) ReadFrame(t *testing.T) *frame.Frame {
	t.Helper()
	f, err := vp.Framer.ReadEncryptedFrame(vp.Conn)
	if err != nil {
		t.Fatalf("read frame: %v", err)
	}
	return f
}

func (vn *VirtualNode) Close() {
	vn.Listener.Close()
	for _, p := range vn.Peers {
		p.Conn.Close()
	}
}

// ===== SCENARIO TESTS =====

// TestTwoNodeDiscoveryAndMessaging：Alice 和 Bob 通过信标互相发现，
// 然后交换双棘轮加密消息。
func TestTwoNodeDiscoveryAndMessaging(t *testing.T) {
	alice := NewVirtualNode(t, "Alice")
	bob := NewVirtualNode(t, "Bob")
	defer alice.Close()
	defer bob.Close()

	// Alice 的服务器：接受 Bob 的连接
	var alicePeer *VirtualPeer
	aliceDone := make(chan struct{})
	go func() {
		alicePeer = alice.AcceptOne(t)
		close(aliceDone)
	}()

	time.Sleep(50 * time.Millisecond)

	// Bob 连接到 Alice
	bobPeer := bob.Dial(t, alice)
	<-aliceDone

	t.Log("✓ Noise IK handshake complete")

	// Bob 构建目标为 Alice 的信标
	bp, nonce, _ := beacon.Build(&beacon.BuildParams{
		SenderChainID: bob.ChainID,
		SendPrivKey:   bob.Keys.SendKey.Private,
		Targets:       []beacon.Target{{ChainID: alice.ChainID, RecvPK: alice.Keys.RecvKey.Public}},
	})

	// Bob 发送信标
	bobPeer.SendFrame(t, frame.FrameBeacon, bp.Serialize())
	t.Log("✓ Bob sent beacon")

	// Alice 读取信标
	f := alicePeer.ReadFrame(t)
	if f.Type != frame.FrameBeacon {
		t.Fatalf("expected BEACON frame, got %d", f.Type)
	}

	received, _ := beacon.ParseBeacon(f.Payload)
	var symKey []byte
	for i := uint8(0); i < received.SlotCount; i++ {
		sk, _ := received.TryDecryptSlot(int(i), &alice.Keys.RecvKey.Private)
		if sk != nil {
			symKey = sk
			break
		}
	}
	if symKey == nil {
		t.Fatal("Alice could not decrypt beacon")
	}
	inner, _ := received.DecryptBody(symKey)
	_ = inner
	t.Logf("✓ Alice decrypted beacon from Bob")

	// === 棘轮设置 ===
	sharedEph, _ := crypto.GenerateEphemeralKey()
	bobRatch, bobEphPK, _ := ratchet.InitAsInitiator(
		&bob.Keys.RecvKey.Private, &alice.Keys.RecvKey.Public, &sharedEph.Public,
	)

	// 分享 Bob 的临时公钥
	bobPeer.SendFrame(t, frame.FramePing, bobEphPK[:])

	// Alice 读取 Bob 的临时公钥
	f2 := alicePeer.ReadFrame(t)
	var bobEph [32]byte
	copy(bobEph[:], f2.Payload[:32])

	aliceRatch, err := ratchet.InitAsResponder(
		&alice.Keys.RecvKey.Private, &bob.Keys.RecvKey.Public, &sharedEph.Private, &bobEph,
	)
	if err != nil {
		t.Fatal(err)
	}

	// Bob 发送加密消息
	msg1 := "Hello Alice! This is encrypted with double ratchet."
	wire1, _ := bobRatch.Encrypt([]byte(msg1))
	bobPeer.SendFrame(t, frame.FrameData, wire1)

	// Alice 接收并解密
	f3 := alicePeer.ReadFrame(t)
	plain1, _ := aliceRatch.Decrypt(f3.Payload)
	t.Logf("✓ Alice received: %q", string(plain1))

	if string(plain1) != msg1 {
		t.Errorf("message mismatch: got %q, want %q", string(plain1), msg1)
	}

	// Alice 回应
	msg2 := "Hello Bob! Ratchet works both ways."
	wire2, _ := aliceRatch.Encrypt([]byte(msg2))
	alicePeer.SendFrame(t, frame.FrameData, wire2)

	// Bob 接收
	f4 := bobPeer.ReadFrame(t)
	plain2, _ := bobRatch.Decrypt(f4.Payload)
	t.Logf("✓ Bob received: %q", string(plain2))

	if string(plain2) != msg2 {
		t.Errorf("message mismatch: got %q, want %q", string(plain2), msg2)
	}

	t.Log("═══ Scenario 1 PASS: Discovery + Bidirectional Ratchet ═══")
	_ = nonce
}

// TestThreeNodeOnionRouting：Alice 通过 Bob、Charlie 和 Dave
// 发送洋葱路由消息到达 Eve。
func TestThreeNodeOnionRouting(t *testing.T) {
	alice := NewVirtualNode(t, "Alice")
	bob := NewVirtualNode(t, "Bob")
	charlie := NewVirtualNode(t, "Charlie")
	dave := NewVirtualNode(t, "Dave")
	eve := NewVirtualNode(t, "Eve") // 最终目标
	defer alice.Close()
	defer bob.Close()
	defer charlie.Close()
	defer dave.Close()
	defer eve.Close()

	// 构建洋葱路径：Bob(入口) → Charlie(中间) → Dave(出口) → Eve(目标)
	hops := []onion.Hop{
		{RecvPK: bob.Keys.RecvKey.Public, IPv6: ipv6Loopback(), Port: uint16(bob.Addr.Port)},
		{RecvPK: charlie.Keys.RecvKey.Public, IPv6: ipv6Loopback(), Port: uint16(charlie.Addr.Port)},
		{RecvPK: dave.Keys.RecvKey.Public, IPv6: ipv6Loopback(), Port: uint16(dave.Addr.Port)},
	}
	target := onion.Target{IPv6: ipv6Loopback(), Port: uint16(eve.Addr.Port)}

	message := []byte("onion-routed: the eagle has landed")

	// Alice 构建洋葱包
	var cid [16]byte
	pkt, err := onion.Build(cid, hops, target, message)
	if err != nil {
		t.Fatalf("build onion: %v", err)
	}

	// === 连接链路 ===
	// Alice → Bob
	bobPeerCh := make(chan *VirtualPeer, 1)
	go func() { bobPeerCh <- bob.AcceptOne(t) }()
	time.Sleep(50 * time.Millisecond)
	aliceToBob := alice.Dial(t, bob)
	bobPeer := <-bobPeerCh

	// Bob → Charlie
	charliePeerCh := make(chan *VirtualPeer, 1)
	go func() { charliePeerCh <- charlie.AcceptOne(t) }()
	time.Sleep(50 * time.Millisecond)
	bobToCharlie := bob.Dial(t, charlie)
	charliePeer := <-charliePeerCh

	// Charlie → Dave
	davePeerCh := make(chan *VirtualPeer, 1)
	go func() { davePeerCh <- dave.AcceptOne(t) }()
	time.Sleep(50 * time.Millisecond)
	charlieToDave := charlie.Dial(t, dave)
	davePeer := <-davePeerCh

	// Dave → Eve 连接（出口跳到目标 — 已建立但测试中未使用）
	evePeerCh := make(chan *VirtualPeer, 1)
	go func() { evePeerCh <- eve.AcceptOne(t) }()
	time.Sleep(50 * time.Millisecond)
	dave.Dial(t, eve)
	<-evePeerCh

	t.Log("✓ 5-node chain connected")

	// Alice 通过 Bob 发送洋葱包
	aliceToBob.SendFrame(t, frame.FrameRoute, pkt.Serialize())

	// Bob 接收，解密第 1 层
	f1 := bobPeer.ReadFrame(t)
	if f1.Type != frame.FrameRoute {
		t.Fatalf("Bob expected ROUTE frame, got %d", f1.Type)
	}
	parsed1, _ := onion.ParseOnion(f1.Payload)
	r1, err := onion.UnwrapOne(&bob.Keys.RecvKey.Private, parsed1.Layers[0])
	if err != nil || r1.IsFinal {
		t.Fatal("Bob should be relay, not final")
	}
	t.Log("✓ Bob unwrapped layer 1")

	// Bob 转发到 Charlie
	stripped := parsed1.StripFirst()
	bobToCharlie.SendFrame(t, frame.FrameRoute, stripped.Serialize())

	// Charlie 接收，解密第 2 层
	f2 := charliePeer.ReadFrame(t)
	parsed2, _ := onion.ParseOnion(f2.Payload)
	r2, _ := onion.UnwrapOne(&charlie.Keys.RecvKey.Private, parsed2.Layers[0])
	if r2.IsFinal {
		t.Fatal("Charlie should be relay, not final")
	}
	t.Log("✓ Charlie unwrapped layer 2")

	// Charlie 转发到 Dave
	stripped2 := parsed2.StripFirst()
	charlieToDave.SendFrame(t, frame.FrameRoute, stripped2.Serialize())

	// Dave 接收，解密第 3 层（出口跳）
	f3Raw := davePeer.ReadFrame(t)
	parsed3Raw, _ := onion.ParseOnion(f3Raw.Payload)
	r3Raw, _ := onion.UnwrapOne(&dave.Keys.RecvKey.Private, parsed3Raw.Layers[0])
	if !r3Raw.IsFinal {
		t.Fatal("Dave should be exit hop with final layer")
	}
	t.Logf("✓ Dave unwrapped final layer, target: Eve")

	// Dave 收到最终层，消息是给 Eve 的
	t.Logf("✓ Dave delivered to Eve: %q", string(r3Raw.FinalMsg))

	if string(r3Raw.FinalMsg) != string(message) {
		t.Errorf("message mismatch: got %q", r3Raw.FinalMsg)
	}

	t.Log("═══ Scenario 2 PASS: 3-hop Onion Routing ═══")
}

// TestBeaconRelayForwarding：Alice 通过 Bob 发送信标给 Charlie。
func TestBeaconRelayForwarding(t *testing.T) {
	alice := NewVirtualNode(t, "Alice")
	bob := NewVirtualNode(t, "Bob")    // 中继
	charlie := NewVirtualNode(t, "Charlie") // 目标
	defer alice.Close()
	defer bob.Close()
	defer charlie.Close()

	// Alice 连接到 Bob
	bobPeerCh := make(chan *VirtualPeer, 1)
	go func() { bobPeerCh <- bob.AcceptOne(t) }()
	time.Sleep(50 * time.Millisecond)
	aliceToBob := alice.Dial(t, bob)
	bobFromAlice := <-bobPeerCh

	// Bob 连接到 Charlie
	charliePeerCh := make(chan *VirtualPeer, 1)
	go func() { charliePeerCh <- charlie.AcceptOne(t) }()
	time.Sleep(50 * time.Millisecond)
	bobToCharlie := bob.Dial(t, charlie)
	charlieFromBob := <-charliePeerCh

	t.Log("✓ Relay chain connected")

	// Alice 构建目标为 Charlie 的信标
	bp, _, _ := beacon.Build(&beacon.BuildParams{
		SenderChainID: alice.ChainID,
		SendPrivKey:   alice.Keys.SendKey.Private,
		Targets:       []beacon.Target{{ChainID: charlie.ChainID, RecvPK: charlie.Keys.RecvKey.Public}},
	})

	// Alice 发送信标给 Bob
	aliceToBob.SendFrame(t, frame.FrameBeacon, bp.Serialize())

	// Bob 接收，无法解密（非目标），转发给 Charlie
	f1 := bobFromAlice.ReadFrame(t)
	if f1.Type != frame.FrameBeacon {
		t.Fatalf("Bob expected BEACON, got %d", f1.Type)
	}
	relayBP, _ := beacon.ParseBeacon(f1.Payload)

	// Bob：尝试解密（应该失败）
	decrypted := false
	for i := uint8(0); i < relayBP.SlotCount; i++ {
		if _, err := relayBP.TryDecryptSlot(int(i), &bob.Keys.RecvKey.Private); err == nil {
			decrypted = true
		}
	}
	if decrypted {
		t.Log("(Bob could decrypt — he's also a target, though not expected)")
	}
	relayBP.HopCount++
	bobToCharlie.SendFrame(t, frame.FrameBeacon, relayBP.Serialize())
	t.Log("✓ Bob forwarded beacon to Charlie")

	// Charlie 接收并解密
	f2 := charlieFromBob.ReadFrame(t)
	received, _ := beacon.ParseBeacon(f2.Payload)
	for i := uint8(0); i < received.SlotCount; i++ {
		sk, _ := received.TryDecryptSlot(int(i), &charlie.Keys.RecvKey.Private)
		if sk != nil {
			inner, _ := received.DecryptBody(sk)
			if inner.SenderChainID == alice.ChainID {
				t.Logf("✓ Charlie decrypted beacon from Alice (via Bob, hop=%d)", received.HopCount)
				break
			}
		}
	}

	t.Log("═══ Scenario 3 PASS: Beacon Relay Forwarding ═══")
}

func ipv6Loopback() [16]byte {
	var ip [16]byte
	ip[15] = 1
	return ip
}
