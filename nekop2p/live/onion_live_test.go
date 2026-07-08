package live_test

import (
	"bytes"
	"crypto/rand"
	"fmt"
	"net"
	"testing"
	"time"

	"github.com/nekop2p/nekop2p/crypto"
	"github.com/nekop2p/nekop2p/frame"
	"github.com/nekop2p/nekop2p/noise"
	"github.com/nekop2p/nekop2p/onion"
)

type nodeCfg struct {
	keys     *crypto.DualKeys
	listener net.Listener
	addr     *net.TCPAddr
}

func ipToArray(ip net.IP) [16]byte {
	var arr [16]byte
	copy(arr[:], ip.To16())
	return arr
}

func TestOnionRoutingLive(t *testing.T) {
	// 生成 4 个独立密钥对（3 跳 + 1 发送者）
	nodes := make([]*nodeCfg, 4)
	hops := make([]onion.Hop, 3)
	senderKeys, _ := crypto.GenerateDualKeys()

	for i := 0; i < 4; i++ {
		k, _ := crypto.GenerateDualKeys()
		ln, err := net.Listen("tcp6", "[::1]:0")
		if err != nil {
			t.Skipf("IPv6: %v", err)
			return
		}
		addr := ln.Addr().(*net.TCPAddr)
		nodes[i] = &nodeCfg{keys: k, listener: ln, addr: addr}
		if i < 3 {
			hops[i] = onion.Hop{
				IPv6:   ipToArray(addr.IP),
				Port:   uint16(addr.Port),
				RecvPK: k.RecvKey.Public,
			}
		}
	}
	defer func() {
		for _, n := range nodes {
			n.listener.Close()
		}
	}()

	target := onion.Target{
		IPv6: ipToArray(nodes[3].addr.IP),
		Port: uint16(nodes[3].addr.Port),
	}
	message := []byte("the eagle has landed through the onion")

	msgCh := make(chan []byte, 1)
	errCh := make(chan error, 4)

	// 第 1 跳: 接受来自发送方的连接，解包，转发到第 2 跳
	go relayHop(t, "hop1", nodes[0], nodes[1], hops[1], msgCh, errCh, false, senderKeys.RecvKey.Public)
	// 第 2 跳: 接受来自第 1 跳的连接，解包，转发到第 3 跳
	go relayHop(t, "hop2", nodes[1], nodes[2], hops[2], msgCh, errCh, true, nodes[0].keys.RecvKey.Public)
	// 第 3 跳: 接受来自第 2 跳的连接，最终解包，送达目标
	go exitHop(t, nodes[2], nodes[3], target, message, msgCh, errCh, nodes[1].keys.RecvKey.Public)

	time.Sleep(100 * time.Millisecond) // 等待监听器就绪

	// 发送者: 构建洋葱，发送到第 1 跳
	var circuitID [16]byte
	rand.Read(circuitID[:])
	pkt, err := onion.Build(circuitID, hops, target, message)
	if err != nil {
		t.Fatalf("build: %v", err)
	}

	sendOnionTo(t, senderKeys, nodes[0], pkt, msgCh, errCh)

	// 等待送达
	select {
	case msg := <-msgCh:
		if bytes.Contains(msg, []byte("eagle")) {
			t.Logf("✓ onion routing: message delivered through 3 hops")
		}
	case e := <-errCh:
		t.Fatalf("error: %v", e)
	case <-time.After(15 * time.Second):
		t.Fatal("timeout")
	}
}

func relayHop(t *testing.T, name string, self, next *nodeCfg, nextHop onion.Hop,
	msgCh chan []byte, errCh chan error, isMiddle bool, expectedInitPK [32]byte) {
	t.Helper()
	conn, err := self.listener.Accept()
	if err != nil {
		errCh <- fmt.Errorf("%s accept: %w", name, err)
		return
	}
	defer conn.Close()

	hs, _ := noise.NewResponderIK(&self.keys.RecvKey, expectedInitPK, noise.RoleFriend)
	buf := make([]byte, 8192)
	nr, _ := conn.Read(buf)
	hs.ReadMessage(buf[:nr])
	msg2, _ := hs.WriteMessage(nil)
	conn.Write(msg2)
	result := hs.Complete()
	fr := frame.NewSessionKeys(result.SendCipher.Key[:], result.RecvCipher.Key[:])

	for {
		f, err := fr.ReadEncryptedFrame(conn)
		if err != nil {
			return
		}
		if f.Type != frame.FrameRoute {
			continue
		}
		pkt, err := onion.ParseOnion(f.Payload)
		if err != nil {
			continue
		}
		r, err := onion.UnwrapOne(&self.keys.RecvKey.Private, pkt.Layers[0])
		if err != nil || r.IsFinal {
			return
		}

		// 将剥离后的数据包转发到下一跳
		stripped := pkt.StripFirst()
		if stripped == nil {
			return
		}
		forwardToNext(t, stripped, self, next, nextHop, msgCh, errCh)
		return
	}
}

func exitHop(t *testing.T, self, target *nodeCfg, expectedTarget onion.Target,
	expectedMsg []byte, msgCh chan []byte, errCh chan error, expectedInitPK [32]byte) {
	t.Helper()
	conn, err := self.listener.Accept()
	if err != nil {
		errCh <- err
		return
	}
	defer conn.Close()

	hs, _ := noise.NewResponderIK(&self.keys.RecvKey, expectedInitPK, noise.RoleFriend)
	buf := make([]byte, 8192)
	nr, _ := conn.Read(buf)
	hs.ReadMessage(buf[:nr])
	msg2, _ := hs.WriteMessage(nil)
	conn.Write(msg2)
	result := hs.Complete()
	fr := frame.NewSessionKeys(result.SendCipher.Key[:], result.RecvCipher.Key[:])

	for {
		f, err := fr.ReadEncryptedFrame(conn)
		if err != nil {
			return
		}
		if f.Type != frame.FrameRoute {
			continue
		}
		pkt, err := onion.ParseOnion(f.Payload)
		if err != nil {
			continue
		}
		r, err := onion.UnwrapOne(&self.keys.RecvKey.Private, pkt.Layers[0])
		if err != nil {
			return
		}
		if r.IsFinal {
			msgCh <- r.FinalMsg
		}
		return
	}
}

func forwardToNext(t *testing.T, pkt *onion.Packet, self *nodeCfg, next *nodeCfg, nextHop onion.Hop,
	msgCh chan []byte, errCh chan error) {
	t.Helper()
	addr := fmt.Sprintf("[::1]:%d", next.addr.Port)
	conn, err := net.DialTimeout("tcp6", addr, 5*time.Second)
	if err != nil {
		errCh <- err
		return
	}
	defer conn.Close()

	// 转发方（self）向下一跳发起 IK 握手
	hs := noise.NewInitiatorIK(&self.keys.RecvKey, &nextHop.RecvPK, noise.RoleFriend)
	m1, _ := hs.WriteMessage(nil)
	conn.Write(m1)
	buf := make([]byte, 4096)
	nr, _ := conn.Read(buf)
	hs.ReadMessage(buf[:nr])
	result := hs.Complete()
	fr := frame.NewSessionKeys(result.SendCipher.Key[:], result.RecvCipher.Key[:])

	routeFrame := frame.NewFrame(frame.FrameRoute, 0, pkt.Serialize())
	if err := fr.WriteEncryptedFrame(conn, routeFrame); err != nil {
		errCh <- err
		return
	}
}

func sendOnionTo(t *testing.T, senderKeys *crypto.DualKeys, entry *nodeCfg,
	pkt *onion.Packet, msgCh chan []byte, errCh chan error) {
	t.Helper()
	addr := fmt.Sprintf("[::1]:%d", entry.addr.Port)
	conn, err := net.DialTimeout("tcp6", addr, 5*time.Second)
	if err != nil {
		errCh <- err
		return
	}
	defer conn.Close()

	hs := noise.NewInitiatorIK(&senderKeys.RecvKey, &entry.keys.RecvKey.Public, noise.RoleFriend)
	m1, _ := hs.WriteMessage(nil)
	conn.Write(m1)
	buf := make([]byte, 4096)
	nr, _ := conn.Read(buf)
	hs.ReadMessage(buf[:nr])
	result := hs.Complete()
	fr := frame.NewSessionKeys(result.SendCipher.Key[:], result.RecvCipher.Key[:])

	routeFrame := frame.NewFrame(frame.FrameRoute, 0, pkt.Serialize())
	if err := fr.WriteEncryptedFrame(conn, routeFrame); err != nil {
		errCh <- err
		return
	}
}
