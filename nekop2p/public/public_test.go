package public_test

import (
	"net"
	"testing"
	"time"

	"github.com/nekop2p/nekop2p/crypto"
	"github.com/nekop2p/nekop2p/public"
)

func TestNewServer(t *testing.T) {
	keys, _ := crypto.GenerateDualKeys()
	cfg := public.Config{
		ListenAddr:      "[::1]:0",
		StaticKey:       keys.RecvKey,
		MaxConns:        100,
		BeaconRateLimit: 5,
	}

	s := public.New(cfg)
	if s == nil {
		t.Fatal("server is nil")
	}
}

func TestDefaultConfig(t *testing.T) {
	keys, _ := crypto.GenerateDualKeys()
	cfg := public.Config{
		ListenAddr:      "[::1]:0",
		StaticKey:       keys.RecvKey,
		MaxConns:        10000,
		BeaconRateLimit: 5,
	}

	s := public.New(cfg)
	_ = s
}

func TestServerListenAndShutdown(t *testing.T) {
	keys, _ := crypto.GenerateDualKeys()
	cfg := public.Config{
		ListenAddr:      "[::1]:0",
		StaticKey:       keys.RecvKey,
		MaxConns:        100,
		BeaconRateLimit: 5,
	}

	s := public.New(cfg)

	// 应该能启动监听
	err := s.Listen()
	if err != nil {
		// IPv6 不可用则跳过
		if opErr, ok := err.(*net.OpError); ok {
			t.Skipf("IPv6 not available: %v", opErr)
		}
		t.Fatalf("listen: %v", err)
	}

	// 给监听器一些时间启动
	time.Sleep(50 * time.Millisecond)

	// 应该能优雅关闭
	s.Shutdown()
}

func TestServerListenInvalidAddr(t *testing.T) {
	keys, _ := crypto.GenerateDualKeys()
	cfg := public.Config{
		ListenAddr:      "invalid-addr",
		StaticKey:       keys.RecvKey,
		MaxConns:        100,
		BeaconRateLimit: 5,
	}

	s := public.New(cfg)

	err := s.Listen()
	if err == nil {
		t.Error("listen on invalid address should fail")
	}
}

func TestServerShutdownWithoutListen(t *testing.T) {
	keys, _ := crypto.GenerateDualKeys()
	cfg := public.Config{
		ListenAddr:      "[::1]:0",
		StaticKey:       keys.RecvKey,
		MaxConns:        100,
		BeaconRateLimit: 5,
	}

	s := public.New(cfg)

	// 没有 Listen 的情况下 Shutdown 不应该 panic
	s.Shutdown()
}

func TestServerResetPeers(t *testing.T) {
	keys, _ := crypto.GenerateDualKeys()
	cfg := public.Config{
		ListenAddr:      "[::1]:0",
		StaticKey:       keys.RecvKey,
		MaxConns:        100,
		BeaconRateLimit: 5,
	}

	s1 := public.New(cfg)
	s2 := public.New(cfg)

	// SetPeers 不应该 panic
	s1.SetPeers(nil)
	s2.SetPeers(nil)
}

func TestServerConcurrentAccess(t *testing.T) {
	keys, _ := crypto.GenerateDualKeys()
	cfg := public.Config{
		ListenAddr:      "[::1]:0",
		StaticKey:       keys.RecvKey,
		MaxConns:        100,
		BeaconRateLimit: 5,
	}

	s := public.New(cfg)

	// 并发创建多个 server 不应该有竞态
	done := make(chan bool, 5)
	for i := 0; i < 5; i++ {
		go func() {
			s2 := public.New(cfg)
			_ = s2
			done <- true
		}()
	}

	for i := 0; i < 5; i++ {
		<-done
	}
	_ = s
}

func TestServerListenInvalidIPv6(t *testing.T) {
	keys, _ := crypto.GenerateDualKeys()
	cfg := public.Config{
		ListenAddr:      "256.256.256.256:99999", // 无效地址
		StaticKey:       keys.RecvKey,
		MaxConns:        100,
		BeaconRateLimit: 5,
	}

	s := public.New(cfg)
	err := s.Listen()
	if err == nil {
		s.Shutdown()
		t.Error("listen on invalid address should fail")
	}
}
