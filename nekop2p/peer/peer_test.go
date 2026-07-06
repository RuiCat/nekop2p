package peer_test

import (
	"context"
	"net"
	"testing"
	"time"

	"github.com/nekop2p/nekop2p/crypto"
	"github.com/nekop2p/nekop2p/frame"
	"github.com/nekop2p/nekop2p/peer"
)

func TestNewManager(t *testing.T) {
	m := peer.NewManager(context.Background())
	if m.Count() != 0 {
		t.Error("new manager should have 0 peers")
	}
	m.Shutdown()
}

func TestAddAndRemove(t *testing.T) {
	m := peer.NewManager(context.Background())
	defer m.Shutdown()

	keys, _ := crypto.GenerateDualKeys()
	cid := crypto.DeriveChainID(keys.SendKey.Public)
	key, _ := frame.GenerateSessionKey()
	fr := frame.NewSessionKeys(key, key)

	srv, cli := net.Pipe()
	m.Add(cid, srv, fr, false)
	if m.Count() != 1 {
		t.Errorf("after add: count=%d", m.Count())
	}

	cli.Close()
	srv.Close()
	m.Remove(cid)
	time.Sleep(50 * time.Millisecond)
	if m.Count() != 0 {
		t.Errorf("after remove: count=%d", m.Count())
	}
}

func TestCountAndRealCount(t *testing.T) {
	m := peer.NewManager(context.Background())
	defer m.Shutdown()

	keys, _ := crypto.GenerateDualKeys()
	cid := crypto.DeriveChainID(keys.SendKey.Public)
	key, _ := frame.GenerateSessionKey()
	fr := frame.NewSessionKeys(key, key)

	srv, cli := net.Pipe()
	m.Add(cid, srv, fr, false)

	if m.Count() != 1 || m.RealCount() != 1 {
		t.Errorf("real add: total=%d real=%d", m.Count(), m.RealCount())
	}
	cli.Close()
	srv.Close()
}

func TestPaddingCount(t *testing.T) {
	m := peer.NewManager(context.Background())
	defer m.Shutdown()

	keys, _ := crypto.GenerateDualKeys()
	cid := crypto.DeriveChainID(keys.SendKey.Public)
	key, _ := frame.GenerateSessionKey()
	fr := frame.NewSessionKeys(key, key)

	srv, cli := net.Pipe()
	m.Add(cid, srv, fr, true)
	if m.RealCount() != 0 {
		t.Error("padding should not count as real")
	}
	if m.Count() != 1 {
		t.Error("padding should count in total")
	}
	cli.Close()
	srv.Close()
}

func TestAllEmpty(t *testing.T) {
	m := peer.NewManager(context.Background())
	defer m.Shutdown()
	if len(m.All()) != 0 {
		t.Error("empty manager: All should be empty")
	}
}

func TestGet(t *testing.T) {
	m := peer.NewManager(context.Background())
	defer m.Shutdown()

	keys, _ := crypto.GenerateDualKeys()
	cid := crypto.DeriveChainID(keys.SendKey.Public)
	key, _ := frame.GenerateSessionKey()
	fr := frame.NewSessionKeys(key, key)

	srv, cli := net.Pipe()
	m.Add(cid, srv, fr, false)

	pi := m.Get(cid)
	if pi == nil {
		t.Error("Get should return peer info")
	}
	if pi.IsPadding {
		t.Error("peer should not be padding")
	}
	cli.Close()
	srv.Close()
}

func TestGetNonExistent(t *testing.T) {
	m := peer.NewManager(context.Background())
	defer m.Shutdown()

	var cid peer.ChainID
	pi := m.Get(cid)
	if pi != nil {
		t.Error("Get for nonexistent should return nil")
	}
}

func TestSendFrameToNonexistent(t *testing.T) {
	m := peer.NewManager(context.Background())
	defer m.Shutdown()
	var cid peer.ChainID
	err := m.SendFrame(cid, frame.NewFrame(frame.FramePing, 0, nil))
	if err == nil {
		t.Error("send to nonexistent should fail")
	}
}

func TestSendFrame(t *testing.T) {
	m := peer.NewManager(context.Background())
	defer m.Shutdown()

	keys, _ := crypto.GenerateDualKeys()
	cid := crypto.DeriveChainID(keys.SendKey.Public)
	key, _ := frame.GenerateSessionKey()
	fr := frame.NewSessionKeys(key, key)

	srv, cli := net.Pipe()
	m.Add(cid, srv, fr, false)

	// 在 goroutine 中持续读取避免 WriteEncryptedFrame 阻塞
	readDone := make(chan struct{})
	go func() {
		buf := make([]byte, 1024)
		for {
			_, err := cli.Read(buf)
			if err != nil {
				close(readDone)
				return
			}
		}
	}()

	err := m.SendFrame(cid, frame.NewFrame(frame.FramePing, 0, nil))
	if err != nil {
		t.Logf("SendFrame (expected on pipe): %v", err)
	}

	cli.Close()
	srv.Close()
	<-readDone
}

func TestCallbacks(t *testing.T) {
	m := peer.NewManager(context.Background())
	defer m.Shutdown()

	connectCalled := make(chan struct{}, 1)

	m.SetCallbacks(
		nil,
		func(pi *peer.Info) {
			connectCalled <- struct{}{}
		},
		nil,
	)

	keys, _ := crypto.GenerateDualKeys()
	cid := crypto.DeriveChainID(keys.SendKey.Public)
	key, _ := frame.GenerateSessionKey()
	fr := frame.NewSessionKeys(key, key)

	srv, cli := net.Pipe()
	m.Add(cid, srv, fr, false)

	select {
	case <-connectCalled:
		// OK
	case <-time.After(100 * time.Millisecond):
		t.Error("onConnect callback not called")
	}

	cli.Close()
	srv.Close()
}

func TestBroadcastFrame(t *testing.T) {
	m := peer.NewManager(context.Background())
	defer m.Shutdown()

	key, _ := frame.GenerateSessionKey()

	for i := 0; i < 3; i++ {
		k, _ := crypto.GenerateDualKeys()
		cid := crypto.DeriveChainID(k.SendKey.Public)
		fr := frame.NewSessionKeys(key, key)
		srv, cli := net.Pipe()
		// 持续读取
		go func(c net.Conn) {
			buf := make([]byte, 1024)
			for {
				_, err := c.Read(buf)
				if err != nil {
					return
				}
			}
		}(cli)
		m.Add(cid, srv, fr, false)
	}

	var emptyID peer.ChainID
	m.BroadcastFrame(frame.NewFrame(frame.FramePing, 0, nil), emptyID)
}

func TestShutdown(t *testing.T) {
	m := peer.NewManager(context.Background())

	keys, _ := crypto.GenerateDualKeys()
	cid := crypto.DeriveChainID(keys.SendKey.Public)
	key, _ := frame.GenerateSessionKey()
	fr := frame.NewSessionKeys(key, key)

	srv, cli := net.Pipe()
	m.Add(cid, srv, fr, false)
	cli.Close()

	m.Shutdown()
	time.Sleep(50 * time.Millisecond)
	if m.Count() != 0 {
		t.Errorf("after shutdown: count=%d, want 0", m.Count())
	}
	srv.Close()
}

func TestRemoveNonExistent(t *testing.T) {
	m := peer.NewManager(context.Background())
	defer m.Shutdown()

	var cid peer.ChainID
	m.Remove(cid) // 不应该 panic
}

func TestConcurrentCountAll(t *testing.T) {
	m := peer.NewManager(context.Background())
	defer m.Shutdown()

	key, _ := frame.GenerateSessionKey()
	done := make(chan struct{}, 10)

	// 并发添加
	for i := 0; i < 5; i++ {
		go func(idx int) {
			k, _ := crypto.GenerateDualKeys()
			cid := crypto.DeriveChainID(k.SendKey.Public)
			fr := frame.NewSessionKeys(key, key)
			srv, cli := net.Pipe()
			go func(c net.Conn) {
				buf := make([]byte, 256)
				for {
					if _, err := c.Read(buf); err != nil {
						return
					}
				}
			}(cli)
			m.Add(cid, srv, fr, false)
			done <- struct{}{}
		}(i)
	}

	// 并发读取
	for i := 0; i < 5; i++ {
		go func() {
			m.Count()
			m.RealCount()
			m.All()
			done <- struct{}{}
		}()
	}

	for i := 0; i < 10; i++ {
		<-done
	}
}
