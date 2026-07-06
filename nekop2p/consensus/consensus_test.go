package consensus_test

import (
	"testing"
	"time"

	"github.com/nekop2p/nekop2p/consensus"
)

func TestSimpleEngineStartStop(t *testing.T) {
	e := consensus.NewSimpleEngine(100*time.Millisecond, true)

	if err := e.Start(); err != nil {
		t.Fatalf("start: %v", err)
	}
	if !e.IsValidator() {
		t.Error("should be validator")
	}

	time.Sleep(50 * time.Millisecond)

	if err := e.Stop(); err != nil {
		t.Fatalf("stop: %v", err)
	}
}

func TestSimpleEngineNonValidator(t *testing.T) {
	e := consensus.NewSimpleEngine(100*time.Millisecond, false)

	if e.IsValidator() {
		t.Error("should not be validator")
	}
	if e.Height() != 0 {
		t.Errorf("initial height: got %d, want 0", e.Height())
	}
}

func TestSimpleEngineProposeBlock(t *testing.T) {
	e := consensus.NewSimpleEngine(100*time.Millisecond, true)

	_, height, err := e.ProposeBlock(nil)
	if err != nil {
		t.Fatalf("propose: %v", err)
	}
	if height != 1 {
		t.Errorf("first block height: got %d, want 1", height)
	}

	_, height2, _ := e.ProposeBlock(nil)
	if height2 != 2 {
		t.Errorf("second block height: got %d, want 2", height2)
	}

	if e.Height() != 2 {
		t.Errorf("height after 2 proposes: got %d, want 2", e.Height())
	}
}

func TestSimpleEngineCommitBlock(t *testing.T) {
	e := consensus.NewSimpleEngine(100*time.Millisecond, true)

	err := e.CommitBlock(nil, 1)
	if err != nil {
		t.Fatalf("commit: %v", err)
	}
}

func TestSimpleEngineSubscribeBlocks(t *testing.T) {
	e := consensus.NewSimpleEngine(100*time.Millisecond, true)

	ch := e.SubscribeBlocks()
	if ch == nil {
		t.Fatal("subscribe returned nil channel")
	}

	// 提交一个区块
	e.CommitBlock(nil, 1)

	select {
	case event := <-ch:
		if event.Height != 1 {
			t.Errorf("event height: got %d, want 1", event.Height)
		}
	case <-time.After(100 * time.Millisecond):
		t.Error("timeout waiting for block event")
	}
}

func TestSimpleEngineAutoBlock(t *testing.T) {
	e := consensus.NewSimpleEngine(50*time.Millisecond, true)
	ch := e.SubscribeBlocks()

	e.Start()
	defer e.Stop()

	// 等待自动出块
	select {
	case event := <-ch:
		if event.Height < 1 {
			t.Error("auto block should have height >= 1")
		}
	case <-time.After(200 * time.Millisecond):
		t.Error("timeout waiting for auto block")
	}
}

func TestSimpleEngineDoubleStop(t *testing.T) {
	e := consensus.NewSimpleEngine(100*time.Millisecond, true)
	e.Start()
	e.Stop()
	// 第二次停止不应 panic
	e.Stop()
}

func TestBlockEventFields(t *testing.T) {
	e := consensus.NewSimpleEngine(100*time.Millisecond, true)

	e.CommitBlock([]byte("block-data"), 42)

	event := <-e.SubscribeBlocks()
	if event.Height != 42 {
		t.Errorf("height: got %d, want 42", event.Height)
	}
	if event.Timestamp.IsZero() {
		t.Error("timestamp should not be zero")
	}
}
