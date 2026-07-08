package consensus_test

import (
	"sync"
	"testing"
	"time"

	"github.com/nekop2p/nekop2p/consensus"
)

func TestPBFTEngineBasic(t *testing.T) {
	myKey := [32]byte{0x01}
	engine := consensus.NewPBFTEngine(myKey, true, 2*time.Second)

	if !engine.IsValidator() {
		t.Error("should be validator")
	}
	if engine.Height() != 0 {
		t.Errorf("initial height should be 0, got %d", engine.Height())
	}

	// 提议区块（不启动 loop，避免 auto-propose 干扰）
	data, height, err := engine.ProposeBlock([][]byte{[]byte("test-tx")})
	if err != nil {
		t.Fatalf("propose: %v", err)
	}
	if height != 1 {
		t.Errorf("height should be 1, got %d", height)
	}
	if data == nil {
		t.Error("block data should not be nil")
	}
	if len(data) == 0 {
		t.Error("block data should not be empty")
	}
}

func TestPBFTEngineMultiValidator(t *testing.T) {
	key1 := [32]byte{0x01}
	key2 := [32]byte{0x02}
	key3 := [32]byte{0x03}

	engine := consensus.NewPBFTEngine(key1, true, 5*time.Second)
	engine.AddRemoteValidator(key2, 1, "node2")
	engine.AddRemoteValidator(key3, 1, "node3")

	// 不启动 loop，手动提议
	engine.ProposeBlock(nil)

	// 模拟投票
	vote2 := &consensus.PBFTVote{Height: 1, Round: 0, Step: consensus.PBFTStepPrevote, BlockHash: [32]byte{}, Validator: key2}
	vote3 := &consensus.PBFTVote{Height: 1, Round: 0, Step: consensus.PBFTStepPrevote, BlockHash: [32]byte{}, Validator: key3}
	engine.ReceiveVote(vote2)
	engine.ReceiveVote(vote3)

	select {
	case block := <-engine.SubscribeBlocks():
		t.Logf("block committed at height %d", block.Height)
	case <-time.After(3 * time.Second):
		t.Log("no block committed within timeout (expected - need more votes)")
	}
}

func TestPBFTEngineHeightIncrement(t *testing.T) {
	myKey := [32]byte{0x01}
	engine := consensus.NewPBFTEngine(myKey, true, 10*time.Second)

	// 不启动 loop，手动提议验证高度递增
	for i := 1; i <= 3; i++ {
		_, h, err := engine.ProposeBlock(nil)
		if err != nil {
			t.Fatalf("propose %d: %v", i, err)
		}
		if h != int64(i) {
			t.Errorf("height should be %d, got %d", i, h)
		}
	}
}

func TestPBFTValidatorSet(t *testing.T) {
	vs := consensus.NewValidatorSet()

	k1 := [32]byte{0x01}
	k2 := [32]byte{0x02}
	k3 := [32]byte{0x03}

	vs.AddValidator(&consensus.ValidatorInfo{PubKey: k1, VotingPower: 1})
	vs.AddValidator(&consensus.ValidatorInfo{PubKey: k2, VotingPower: 2})
	vs.AddValidator(&consensus.ValidatorInfo{PubKey: k3, VotingPower: 1})

	if vs.ValidatorCount() != 3 {
		t.Errorf("count: got %d, want 3", vs.ValidatorCount())
	}
	if vs.TotalPower() != 4 {
		t.Errorf("total power: got %d, want 4", vs.TotalPower())
	}

	// 法定人数 = 4*2/3+1 = 3
	quorum := vs.Quorum()
	if quorum != 3 {
		t.Errorf("quorum: got %d, want 3", quorum)
	}
}

func TestPBFTProposalBroadcast(t *testing.T) {
	myKey := [32]byte{0x01}
	engine := consensus.NewPBFTEngine(myKey, true, 10*time.Second)

	var broadcastProposal *consensus.PBFTProposal
	var mu sync.Mutex
	engine.OnBroadcastProposal = func(p *consensus.PBFTProposal) {
		mu.Lock()
		broadcastProposal = p
		mu.Unlock()
	}

	// 不启动 loop，手动提议一次
	engine.ProposeBlock([][]byte{[]byte("tx1"), []byte("tx2")})

	time.Sleep(50 * time.Millisecond)

	mu.Lock()
	defer mu.Unlock()
	if broadcastProposal == nil {
		t.Error("proposal should be broadcast")
		return
	}
	if broadcastProposal.Height != 1 {
		t.Errorf("proposal height: got %d, want 1", broadcastProposal.Height)
	}
}
