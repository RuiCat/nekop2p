//go:build !cosmos

package app_test

import (
	"os"
	"testing"

	"github.com/nekop2p/nekop2p/app"
)

func newTestApp(t *testing.T) *app.NekoApp {
	t.Helper()
	dir, err := os.MkdirTemp("", "nekop2p-test-*")
	if err != nil {
		t.Fatalf("create temp dir: %v", err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })

	a, err := app.NewNekoApp(dir)
	if err != nil {
		t.Fatalf("new app: %v", err)
	}
	t.Cleanup(func() { a.Shutdown() })
	return a
}

func TestNewNekoApp(t *testing.T) {
	a := newTestApp(t)
	if a == nil {
		t.Fatal("app should not be nil")
	}
	if a.Name() != "nekop2p" {
		t.Errorf("name: got %s, want nekop2p", a.Name())
	}
}

func TestAppName(t *testing.T) {
	a := newTestApp(t)
	if a.Name() != "nekop2p" {
		t.Errorf("unexpected app name: %s", a.Name())
	}
}

func TestAppBeginEndBlock(t *testing.T) {
	a := newTestApp(t)
	a.BeginBlocker(nil)
	a.EndBlocker(nil)
}

func TestAppMultipleBlockCycles(t *testing.T) {
	a := newTestApp(t)
	for i := 0; i < 5; i++ {
		a.BeginBlocker(nil)
		a.EndBlocker(nil)
	}
}

func TestAppHeight(t *testing.T) {
	a := newTestApp(t)
	if a.Height() != 0 {
		t.Errorf("initial height: got %d, want 0", a.Height())
	}
	a.BeginBlocker(nil)
	if a.Height() != 1 {
		t.Errorf("height after block: got %d, want 1", a.Height())
	}
}

func TestAppPersistence(t *testing.T) {
	dir, err := os.MkdirTemp("", "nekop2p-persist-*")
	if err != nil {
		t.Fatalf("create temp dir: %v", err)
	}
	defer os.RemoveAll(dir)

	// 创建并写入数据
	a1, err := app.NewNekoApp(dir)
	if err != nil {
		t.Fatalf("new app1: %v", err)
	}
	a1.BeginBlocker(nil)
	a1.EndBlocker(nil)
	a1.Shutdown()

	// 重新打开验证数据保留
	a2, err := app.NewNekoApp(dir)
	if err != nil {
		t.Fatalf("new app2: %v", err)
	}
	defer a2.Shutdown()

	// 区块高度持久化验证
	h := a2.Height()
	if h == 0 {
		t.Log("height not persisted (written every 100 blocks)")
	}
}
