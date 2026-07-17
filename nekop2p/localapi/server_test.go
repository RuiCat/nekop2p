package localapi_test

import (
	"net/http"
	"strings"
	"testing"

	"github.com/nekop2p/nekop2p/localapi"
)

func TestServerNew(t *testing.T) {
	cfg := localapi.Config{
		ListenAddr: "127.0.0.1:0",
	}
	srv := localapi.New(cfg)
	if srv == nil {
		t.Fatal("nil server")
	}
	if srv.Token() == "" {
		t.Error("token should not be empty")
	}
}

func TestServerTokenIdempotent(t *testing.T) {
	cfg := localapi.Config{
		ListenAddr: "127.0.0.1:0",
	}
	srv := localapi.New(cfg)
	t1 := srv.Token()
	t2 := srv.Token()
	if t1 != t2 {
		t.Error("Token() should be idempotent")
	}
	if len(t1) < 16 {
		t.Errorf("token too short: %d bytes", len(t1))
	}
}

func TestServerStartStop(t *testing.T) {
	cfg := localapi.Config{
		ListenAddr: "127.0.0.1:0",
	}
	srv := localapi.New(cfg)
	if err := srv.Start(); err != nil {
		t.Fatalf("start: %v", err)
	}
	// 服务器应能正常停止
	if err := srv.Stop(); err != nil {
		t.Fatalf("stop: %v", err)
	}
}

func TestPushEvent(t *testing.T) {
	cfg := localapi.Config{
		ListenAddr: "127.0.0.1:0",
	}
	srv := localapi.New(cfg)
	// 推送事件不应 panic
	srv.PushEvent("test", map[string]string{"key": "value"})
}

func TestPushMessage(t *testing.T) {
	cfg := localapi.Config{
		ListenAddr: "127.0.0.1:0",
	}
	srv := localapi.New(cfg)
	// 推送消息不应 panic
	srv.PushMessage("from-abc", "hello world")
}

func TestStartStop(t *testing.T) {
	cfg := localapi.Config{
		ListenAddr: "127.0.0.1:19971",
	}
	srv := localapi.New(cfg)
	if err := srv.Start(); err != nil {
		t.Fatal(err)
	}
	if err := srv.Stop(); err != nil {
		t.Error(err)
	}
}

func TestIdentityEndpoint(t *testing.T) {
	cfg := localapi.Config{
		ListenAddr: "127.0.0.1:19973",
		OnIdentity: func() (string, string) {
			return "abc123def456", "ONLINE"
		},
	}
	srv := localapi.New(cfg)
	if err := srv.Start(); err != nil {
		t.Fatal(err)
	}
	defer srv.Stop()

	req, _ := http.NewRequest("GET", "http://127.0.0.1:19973/identity", nil)
	req.Header.Set("X-API-Token", srv.Token())
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET /identity: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 400 {
		t.Errorf("identity status: %d (want 2xx)", resp.StatusCode)
	}
}

func TestStatusEndpoint(t *testing.T) {
	cfg := localapi.Config{
		ListenAddr: "127.0.0.1:19974",
		OnStatus: func() localapi.StatusInfo {
			return localapi.StatusInfo{State: "ONLINE", Peers: 5, Height: 100}
		},
	}
	srv := localapi.New(cfg)
	if err := srv.Start(); err != nil {
		t.Fatal(err)
	}
	defer srv.Stop()

	req, _ := http.NewRequest("GET", "http://127.0.0.1:19974/status", nil)
	req.Header.Set("X-API-Token", srv.Token())
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET /status: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 400 {
		t.Errorf("status: %d", resp.StatusCode)
	}
}

func TestAuthRequired(t *testing.T) {
	cfg := localapi.Config{
		ListenAddr: "127.0.0.1:19975",
		OnIdentity: func() (string, string) { return "test", "ONLINE" },
	}
	srv := localapi.New(cfg)
	if err := srv.Start(); err != nil {
		t.Fatal(err)
	}
	defer srv.Stop()

	// 不带 token 应被拒绝
	resp, err := http.Get("http://127.0.0.1:19975/identity")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("expected 401 without token, got %d", resp.StatusCode)
	}
}

func TestMessageEndpoint(t *testing.T) {
	sentTo := ""
	cfg := localapi.Config{
		ListenAddr: "127.0.0.1:19976",
		OnSendMsg: func(chainID, msg string) error {
			sentTo = chainID
			return nil
		},
	}
	srv := localapi.New(cfg)
	if err := srv.Start(); err != nil {
		t.Fatal(err)
	}
	defer srv.Stop()

	body := `{"chain_id":"target123","message":"hello"}`
	req, _ := http.NewRequest("POST", "http://127.0.0.1:19976/message", strings.NewReader(body))
	req.Header.Set("X-API-Token", srv.Token())
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST /message: %v", err)
	}
	defer resp.Body.Close()

	if sentTo != "target123" {
		t.Errorf("sentTo = %q", sentTo)
	}
}

func TestEventPush(t *testing.T) {
	cfg := localapi.Config{
		ListenAddr: "127.0.0.1:19978",
	}
	srv := localapi.New(cfg)
	if err := srv.Start(); err != nil {
		t.Fatal(err)
	}
	defer srv.Stop()

	// Push 事件不应 panic
	srv.PushEvent(localapi.EventMessage, map[string]string{"from": "alice"})
	srv.PushMessage("alice", "hello")
	srv.PushFriendOnline("bob")
	srv.PushFriendOffline("charlie")
	srv.PushStateChange("OFFLINE", "ONLINE")
}
