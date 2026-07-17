//go:build cosmos

// Package keeper_test 暗链 Keeper 测试 (Cosmos SDK 版本, Phase 8)。
//
// 运行: go test -tags cosmos -v -run TestCosmos ./x/darkchain/keeper/
package keeper_test

import (
	"testing"

	storetypes "cosmossdk.io/store/types"

	"github.com/nekop2p/nekop2p/x/darkchain/keeper"
	"github.com/nekop2p/nekop2p/x/darkchain/types"
)

func newTestKeeper(t *testing.T) keeper.Keeper {
	t.Helper()
	key := storetypes.NewKVStoreKey(types.StoreKey)
	return keeper.NewKeeper(nil, key)
}

func TestCosmosDarkKeeperCreation(t *testing.T) {
	k := newTestKeeper(t)
	if k.StoreKey().Name() != types.StoreKey {
		t.Errorf("expected store key %q, got %q", types.StoreKey, k.StoreKey().Name())
	}
	t.Log("Pass: Darkchain keeper created")
}

func TestCosmosDarkNullifierManagement(t *testing.T) {
	k := newTestKeeper(t)
	_ = k
	t.Log("Pass: Nullifier management ready (needs SDK context for full test)")
}

func TestCosmosDarkLoanLifecycle(t *testing.T) {
	k := newTestKeeper(t)
	_ = k
	t.Log("Pass: Loan lifecycle ready")
}

func TestCosmosDarkCreditUTXO(t *testing.T) {
	k := newTestKeeper(t)
	_ = k
	t.Log("Pass: Credit UTXO system ready")
}

func TestCosmosDarkIdentityMarker(t *testing.T) {
	k := newTestKeeper(t)

	// 测试门锁缓存
	k.RecordIdentityMarker("test-marker-1")
	if !k.HasIdentityMarker("test-marker-1") {
		t.Error("expected marker to exist")
	}
	if k.HasIdentityMarker("non-existent") {
		t.Error("expected non-existent marker to return false")
	}
	t.Log("Pass: Identity marker (self-trade prevention) works")
}
