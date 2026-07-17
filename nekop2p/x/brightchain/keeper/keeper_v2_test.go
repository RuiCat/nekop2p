//go:build cosmos

// Package keeper_test 明链 Keeper 测试 (Cosmos SDK, Phase 8)。
package keeper_test

import (
	"testing"

	storetypes "cosmossdk.io/store/types"
	dbm "github.com/cosmos/cosmos-db"

	"github.com/nekop2p/nekop2p/x/brightchain/keeper"
	"github.com/nekop2p/nekop2p/x/brightchain/types"
)

func newTestKeeper(t *testing.T) keeper.Keeper {
	t.Helper()
	db, _ := dbm.NewDB("test", dbm.MemDBBackend, "")
	t.Cleanup(func() { db.Close() })
	key := storetypes.NewKVStoreKey(types.StoreKey)
	return keeper.NewKeeper(nil, key)
}

func TestCosmosKeeperInit(t *testing.T) {
	k := newTestKeeper(t)
	if k.StoreKey().Name() != types.StoreKey {
		t.Errorf("expected %q, got %q", types.StoreKey, k.StoreKey().Name())
	}
}
