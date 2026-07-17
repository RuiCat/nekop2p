//go:build cosmos

// Package keeper_test 暗链 Keeper 测试 (Cosmos SDK, Phase 8)。
package keeper_test

import (
	"testing"

	storetypes "cosmossdk.io/store/types"
	dbm "github.com/cosmos/cosmos-db"

	"github.com/nekop2p/nekop2p/x/darkchain/keeper"
	"github.com/nekop2p/nekop2p/x/darkchain/types"
)

func newTestKeeper(t *testing.T) keeper.Keeper {
	t.Helper()
	db, _ := dbm.NewDB("test", dbm.MemDBBackend, "")
	t.Cleanup(func() { db.Close() })
	key := storetypes.NewKVStoreKey(types.StoreKey)
	return keeper.NewKeeper(nil, key)
}

func TestCosmosDarkInit(t *testing.T) {
	k := newTestKeeper(t)
	if k.StoreKey().Name() != types.StoreKey {
		t.Errorf("expected %q, got %q", types.StoreKey, k.StoreKey().Name())
	}
}

func TestCosmosDarkIdentityMarker(t *testing.T) {
	k := newTestKeeper(t)
	k.RecordIdentityMarker("m1")
	k.RecordIdentityMarker("m2")
	if !k.HasIdentityMarker("m1") {
		t.Error("m1 should exist")
	}
	if k.HasIdentityMarker("no") {
		t.Error("non-existent marker")
	}
}

func TestCosmosDarkCreditCommitment(t *testing.T) {
	k := newTestKeeper(t)
	commit := []byte("commitment-32-bytes-xxxxxxxxxxxxx")
	k.AddCreditCommitment(commit, 500)
	if p := k.GenerateCreditProof(commit); p == nil {
		t.Error("expected proof")
	}
	if r := k.CreditTreeRoot(); len(r) == 0 {
		t.Error("expected tree root")
	}
}
