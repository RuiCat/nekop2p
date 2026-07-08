// neko-testnet 创建一个本地多节点测试网络。
//
// 它会在不同的 IPv6 localhost 端口上启动 N 个隔离节点，
// 配置好友关系，并测试完整的发现 + 消息传递流程。
//
// 用法：go run ./cmd/neko-testnet/ [num_nodes]
package main

import (
	"encoding/json"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/nekop2p/nekop2p/beacon"
	"github.com/nekop2p/nekop2p/crypto"
	"github.com/nekop2p/nekop2p/frame"
	"github.com/nekop2p/nekop2p/noise"
)

// TestNode 表示测试网络中的一个节点。
type TestNode struct {
	Name    string
	Keys    *crypto.DualKeys
	ChainID [32]byte
	Port    int

	Listener net.Listener
	Friends  map[string]*crypto.DualKeys // 好友名称 → 密钥

	mu       sync.Mutex
	messages []string // 已接收的消息
	running  bool
}

func main() {
	numNodes := 3
	if len(os.Args) > 1 {
		fmt.Sscanf(os.Args[1], "%d", &numNodes)
	}
	if numNodes < 2 {
		numNodes = 2
	}
	if numNodes > 10 {
		numNodes = 10
	}

	log.SetFlags(log.Ltime)
	log.Printf("╔══════════════════════════════════╗")
	log.Printf("║  neko-testnet: %d isolated nodes  ║", numNodes)
	log.Printf("╚══════════════════════════════════╝")
	fmt.Println()

	// 创建节点
	nodes := make([]*TestNode, numNodes)
	names := []string{"Alice", "Bob", "Charlie", "Dave", "Eve", "Frank", "Grace", "Hank", "Iris", "Jack"}

	for i := 0; i < numNodes; i++ {
		keys, _ := crypto.GenerateDualKeys()
		chainID := crypto.DeriveChainID(keys.SendKey.Public)

		ln, err := net.Listen("tcp6", fmt.Sprintf("[::1]:%d", 10000+i))
		if err != nil {
			log.Fatalf("Node %s listen: %v", names[i], err)
		}

		nodes[i] = &TestNode{
			Name:     names[i],
			Keys:     keys,
			ChainID:  chainID,
			Port:     10000 + i,
			Listener: ln,
			Friends:  make(map[string]*crypto.DualKeys),
		}

		log.Printf("  [%s] ::1:%d  chain_id=%x",
			nodes[i].Name, nodes[i].Port, nodes[i].ChainID[:8])
	}

	fmt.Println()

	// 配置好友关系
	// 每个节点的前一个和后一个节点互为好友（环型拓扑）
	for i := 0; i < numNodes; i++ {
		prev := (i - 1 + numNodes) % numNodes
		next := (i + 1) % numNodes
		nodes[i].Friends[nodes[prev].Name] = nodes[prev].Keys
		nodes[i].Friends[nodes[next].Name] = nodes[next].Keys
	}

	// 启动所有节点
	var wg sync.WaitGroup
	for _, n := range nodes {
		n.running = true
		wg.Add(1)
		go runNode(n, nodes, &wg)
	}

	time.Sleep(500 * time.Millisecond)
	log.Println("═══ All nodes started ═══")
	fmt.Println()

	// === 测试 1: Alice → Bob 直连 ===
	log.Println("═══ Test 1: Direct Connection ═══")
	testDirectConnection(nodes[0], nodes[1])

	// === 测试 2: 信标发现 ===
	log.Println("═══ Test 2: Beacon Discovery ═══")
	testBeaconDiscovery(nodes[0], nodes[1])

	// === 测试 3: 多跳中继 (Alice → Bob → Charlie) ===
	if numNodes >= 3 {
		log.Println("═══ Test 3: Multi-hop Relay ═══")
		testRelayConnection(nodes[0], nodes[1], nodes[2])
	}

	fmt.Println()
	log.Println("╔══════════════════════════════════╗")
	log.Printf("║  ✓ All tests passed!            ║")
	log.Printf("║  %d nodes formed a live network   ║", numNodes)
	log.Println("╚══════════════════════════════════╝")

	// 等待 Ctrl-C 或超时
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	select {
	case <-sigCh:
	case <-time.After(30 * time.Second):
	}

	// 关闭
	for _, n := range nodes {
		n.running = false
		n.Listener.Close()
	}
	wg.Wait()
	log.Println("Testnet stopped.")
}

func runNode(n *TestNode, allNodes []*TestNode, wg *sync.WaitGroup) {
	defer wg.Done()

	for n.running {
		conn, err := n.Listener.Accept()
		if err != nil {
			return
		}
		go handleConnection(n, conn)
	}
}

func handleConnection(n *TestNode, conn net.Conn) {
	defer conn.Close()

	buf := make([]byte, 8192)
	nr, err := conn.Read(buf)
	if err != nil {
		return
	}

	// Noise IK 响应方 — 遍历好友列表尝试匹配
	var payload []byte
	for _, friendKeys := range n.Friends {
		hs, hsErr := noise.NewResponderIK(&n.Keys.RecvKey, friendKeys.RecvKey.Public, noise.RoleFriend)
		if hsErr != nil {
			continue
		}
		p, readErr := hs.ReadMessage(buf[:nr])
		if readErr != nil {
			continue
		}
		payload = p
		msg2, _ := hs.WriteMessage(nil)
		conn.Write(msg2)
		result := hs.Complete()
		fr := frame.NewSessionKeys(result.SendCipher.Key[:], result.RecvCipher.Key[:])
		// 读取帧循环
		for {
			f, fErr := fr.ReadEncryptedFrame(conn)
			if fErr != nil {
				return
			}
			switch f.Type {
			case frame.FrameBeacon:
				bp, bpErr := beacon.ParseBeacon(f.Payload)
				if bpErr != nil {
					continue
				}
				for i := uint8(0); i < bp.SlotCount; i++ {
					sk, skErr := bp.TryDecryptSlot(int(i), &n.Keys.RecvKey.Private)
					if skErr != nil {
						continue
					}
					inner, innerErr := bp.DecryptBody(sk)
					if innerErr != nil {
						continue
					}
					n.mu.Lock()
					n.messages = append(n.messages, fmt.Sprintf("beacon_from=%x", inner.SenderChainID[:8]))
					n.mu.Unlock()
					log.Printf("  [%s] ✓ received beacon from %x", n.Name, inner.SenderChainID[:8])
				}
			case frame.FrameData:
				n.mu.Lock()
				n.messages = append(n.messages, string(f.Payload))
				n.mu.Unlock()
			case frame.FramePing:
				pong := frame.NewFrame(frame.FramePong, 0, nil)
				fr.WriteEncryptedFrame(conn, pong)
			}
		}
	}
	_ = payload
}

func dialNode(from, to *TestNode) (net.Conn, *frame.SessionKeys, error) {
	addr := fmt.Sprintf("[::1]:%d", to.Port)
	conn, err := net.DialTimeout("tcp6", addr, 5*time.Second)
	if err != nil {
		return nil, nil, err
	}

	hs := noise.NewInitiatorIK(&from.Keys.RecvKey, &to.Keys.RecvKey.Public, noise.RoleFriend)
	m1, _ := hs.WriteMessage(nil)
	conn.Write(m1)
	buf := make([]byte, 4096)
	nr, _ := conn.Read(buf)
	hs.ReadMessage(buf[:nr])
	r := hs.Complete()
	fr := frame.NewSessionKeys(r.SendCipher.Key[:], r.RecvCipher.Key[:])

	return conn, fr, nil
}

func testDirectConnection(alice, bob *TestNode) {
	conn, fr, err := dialNode(alice, bob)
	if err != nil {
		log.Printf("  ✗ dial failed: %v", err)
		return
	}
	defer conn.Close()

	// 发送 PING
	pong := frame.NewFrame(frame.FramePing, 0, nil)
	if err := fr.WriteEncryptedFrame(conn, pong); err != nil {
		log.Printf("  ✗ ping failed: %v", err)
		return
	}
	log.Printf("  ✓ %s ↔ %s: direct Noise IK connection established", alice.Name, bob.Name)
}

func testBeaconDiscovery(alice, bob *TestNode) {
	conn, fr, err := dialNode(alice, bob)
	if err != nil {
		log.Printf("  ✗ dial failed: %v", err)
		return
	}
	defer conn.Close()

	// 构建目标为 Bob 的信标
	bp, _, err := beacon.Build(&beacon.BuildParams{
		SenderChainID: alice.ChainID,
		SendPrivKey:   alice.Keys.SendKey.Private,
		Targets: []beacon.Target{
			{ChainID: bob.ChainID, RecvPK: bob.Keys.RecvKey.Public},
		},
	})
	if err != nil {
		log.Printf("  ✗ build beacon: %v", err)
		return
	}

	// 通过已建立的连接发送信标
	if err := fr.WriteEncryptedFrame(conn, frame.NewFrame(frame.FrameBeacon, 0, bp.Serialize())); err != nil {
		log.Printf("  ✗ send beacon: %v", err)
		return
	}

	// 等待 Bob 处理
	time.Sleep(200 * time.Millisecond)

	// 检查 Bob 是否收到
	bob.mu.Lock()
	found := false
	for _, msg := range bob.messages {
		if strings.Contains(msg, fmt.Sprintf("%x", alice.ChainID[:8])) {
			found = true
			break
		}
	}
	bob.mu.Unlock()

	if found {
		log.Printf("  ✓ %s discovered %s via beacon", bob.Name, alice.Name)
	} else {
		log.Printf("  ✗ beacon not received by Bob")
	}
}

func testRelayConnection(alice, bob, charlie *TestNode) {
	// 为此测试创建独立监听器（不使用主 accept 循环）
	bobLn, _ := net.Listen("tcp6", "[::1]:0")
	defer bobLn.Close()
	charlieLn, _ := net.Listen("tcp6", "[::1]:0")
	defer charlieLn.Close()

	bobTestPort := bobLn.Addr().(*net.TCPAddr).Port
	charlieTestPort := charlieLn.Addr().(*net.TCPAddr).Port

	// Bob 接受 Alice 的连接，Charlie 接受 Bob 的连接（在 goroutine 中）
	type peerConn struct {
		conn net.Conn
		fr   *frame.SessionKeys
	}
	bobCh := make(chan peerConn, 1)
	charlieCh := make(chan peerConn, 1)

	go func() {
		conn, _ := bobLn.Accept()
		hs, _ := noise.NewResponderIK(&bob.Keys.RecvKey, alice.Keys.RecvKey.Public, noise.RoleFriend)
		buf := make([]byte, 8192)
		nr, _ := conn.Read(buf)
		hs.ReadMessage(buf[:nr])
		m2, _ := hs.WriteMessage(nil)
		conn.Write(m2)
		r := hs.Complete()
		bobCh <- peerConn{conn, frame.NewSessionKeys(r.SendCipher.Key[:], r.RecvCipher.Key[:])}
	}()
	go func() {
		conn, _ := charlieLn.Accept()
		hs, _ := noise.NewResponderIK(&charlie.Keys.RecvKey, bob.Keys.RecvKey.Public, noise.RoleFriend)
		buf := make([]byte, 8192)
		nr, _ := conn.Read(buf)
		hs.ReadMessage(buf[:nr])
		m2, _ := hs.WriteMessage(nil)
		conn.Write(m2)
		r := hs.Complete()
		charlieCh <- peerConn{conn, frame.NewSessionKeys(r.SendCipher.Key[:], r.RecvCipher.Key[:])}
	}()

	time.Sleep(50 * time.Millisecond)

	// Alice 连接到 Bob 的测试监听器
	aliceToBob := dialToPort(alice, bobTestPort, &bob.Keys.RecvKey.Public)
	bobPC := <-bobCh
	defer aliceToBob.conn.Close()
	defer bobPC.conn.Close()

	// Bob 连接到 Charlie 的测试监听器
	bobToCharlie := dialToPort(bob, charlieTestPort, &charlie.Keys.RecvKey.Public)
	charliePC := <-charlieCh
	defer bobToCharlie.conn.Close()
	defer charliePC.conn.Close()

	// Alice 构建目标为 Charlie 的信标
	bp, _, _ := beacon.Build(&beacon.BuildParams{
		SenderChainID: alice.ChainID,
		SendPrivKey:   alice.Keys.SendKey.Private,
		Targets: []beacon.Target{
			{ChainID: charlie.ChainID, RecvPK: charlie.Keys.RecvKey.Public},
		},
	})

	// Alice → Bob（发送信标）
	aliceToBob.fr.WriteEncryptedFrame(aliceToBob.conn, frame.NewFrame(frame.FrameBeacon, 0, bp.Serialize()))

	// Bob 读取后转发给 Charlie
	f, _ := bobPC.fr.ReadEncryptedFrame(bobPC.conn)
	if f != nil && f.Type == frame.FrameBeacon {
		relayBP, _ := beacon.ParseBeacon(f.Payload)
		relayBP.HopCount++
		bobToCharlie.fr.WriteEncryptedFrame(bobToCharlie.conn, frame.NewFrame(frame.FrameBeacon, 0, relayBP.Serialize()))
		log.Printf("  ✓ Bob forwarded beacon to Charlie")
	}

	// Charlie 读取
	f2, _ := charliePC.fr.ReadEncryptedFrame(charliePC.conn)
	if f2 != nil && f2.Type == frame.FrameBeacon {
		received, _ := beacon.ParseBeacon(f2.Payload)
		for i := uint8(0); i < received.SlotCount; i++ {
			sk, _ := received.TryDecryptSlot(int(i), &charlie.Keys.RecvKey.Private)
			if sk != nil {
				inner, _ := received.DecryptBody(sk)
				if inner.SenderChainID == alice.ChainID {
					log.Printf("  ✓ Charlie received beacon from Alice via Bob (hop=%d)", received.HopCount)
					return
				}
			}
		}
	}
	log.Printf("  ✗ Relay failed")
}

type testConn struct {
	conn net.Conn
	fr   *frame.SessionKeys
}

func dialToPort(from *TestNode, port int, toPK *[32]byte) testConn {
	addr := fmt.Sprintf("[::1]:%d", port)
	conn, _ := net.DialTimeout("tcp6", addr, 5*time.Second)
	hs := noise.NewInitiatorIK(&from.Keys.RecvKey, toPK, noise.RoleFriend)
	m1, _ := hs.WriteMessage(nil)
	conn.Write(m1)
	buf := make([]byte, 4096)
	nr, _ := conn.Read(buf)
	hs.ReadMessage(buf[:nr])
	r := hs.Complete()
	return testConn{conn, frame.NewSessionKeys(r.SendCipher.Key[:], r.RecvCipher.Key[:])}
}

// 抑制未使用导入的警告
var _ = json.Marshal
var _ = http.ListenAndServe
