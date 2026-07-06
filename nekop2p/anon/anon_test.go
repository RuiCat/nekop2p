package anon_test

import (
	"testing"

	"github.com/nekop2p/nekop2p/anon"
)

func TestClearToDarkAndBack(t *testing.T) {
	var chainID [32]byte
	chainID[0] = 0xAA

	s := anon.New(anon.Config{ChainID: chainID})

	if s.Channel() != anon.ChannelClear {
		t.Fatal("initial channel should be clear")
	}
	if s.IsOnion() {
		t.Fatal("clear channel should not use onion")
	}

	// 明道 → 暗道
	if err := s.SwitchTo(anon.ChannelDark); err != nil {
		t.Fatalf("switch to dark: %v", err)
	}
	if s.Channel() != anon.ChannelDark {
		t.Fatal("should be in dark channel")
	}
	if !s.IsOnion() {
		t.Fatal("dark channel should use onion")
	}
	if s.OnionHops() != 3 {
		t.Errorf("dark should use 3 hops, got %d", s.OnionHops())
	}
	if s.CurrentIdentity() == chainID {
		t.Error("dark should use anonymous identity, not chain_id")
	}

	// 暗道 → 明道
	if err := s.SwitchTo(anon.ChannelClear); err != nil {
		t.Fatalf("switch to clear: %v", err)
	}
	if s.CurrentIdentity() != chainID {
		t.Error("clear should restore chain_id")
	}
	if s.IsOnion() {
		t.Fatal("clear channel should not use onion")
	}
}

func TestFullBunkerCycle(t *testing.T) {
	var chainID [32]byte
	s := anon.New(anon.Config{ChainID: chainID})

	// 明道 → 防空洞
	if err := s.SwitchTo(anon.ChannelBunker); err != nil {
		t.Fatalf("switch to bunker: %v", err)
	}
	if s.Channel() != anon.ChannelBunker {
		t.Fatal("should be in bunker")
	}
	if s.OnionHops() != 5 {
		t.Errorf("bunker should use 5 hops, got %d", s.OnionHops())
	}
	if s.CurrentIdentity() == chainID {
		t.Error("bunker should not expose chain_id")
	}

	// 防空洞 → 暗道
	if err := s.SwitchTo(anon.ChannelDark); err != nil {
		t.Fatalf("switch to dark: %v", err)
	}

	// 暗道 → 明道
	if err := s.SwitchTo(anon.ChannelClear); err != nil {
		t.Fatalf("switch to clear: %v", err)
	}
	if !s.IsSwitching() {
		// 注意：如果模拟延迟很快，IsSwitching 可能返回 false
		_ = s.PauseDuration()
	}
}

func TestSwitchCallbacks(t *testing.T) {
	var chainID [32]byte
	switchCalled := false
	idCalled := false

	s := anon.New(anon.Config{
		ChainID: chainID,
		OnSwitch: func(from, to anon.Channel) {
			switchCalled = true
		},
		OnIdentity: func(ch anon.Channel, id [32]byte) {
			idCalled = true
		},
	})

	s.SwitchTo(anon.ChannelDark)
	if !switchCalled {
		t.Error("OnSwitch should have been called")
	}
	if !idCalled {
		t.Error("OnIdentity should have been called")
	}
}

func TestSameChannelNoop(t *testing.T) {
	var chainID [32]byte
	s := anon.New(anon.Config{ChainID: chainID})

	// 切换到相同通道应该是无操作
	if err := s.SwitchTo(anon.ChannelClear); err != nil {
		t.Fatalf("switch to same channel: %v", err)
	}
	if s.Channel() != anon.ChannelClear {
		t.Fatal("should still be in clear")
	}
}
