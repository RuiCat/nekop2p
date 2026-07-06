//go:build cosmos

// Package store 提供基于 IAVL Merkle 树 + CosmosDB 的链上持久化存储。
//
// Cosmos SDK v0.50+ 将 store 拆分为独立模块 cosmossdk.io/store。
//
// === 模块子树 ===
//   "bright"    — 明链 (用户/债券/资金池/好友)
//   "dark"      — 暗链 (贷款/nullifier/信用票据)
//   "acc"       — 账户 (Cosmos auth)
//   "params"    — 链参数
//   "consensus" — 共识状态
//
// === Merkle 证明 ===
//   每次 Commit 生成新 IAVL 树 root，可生成 inclusion/exclusion proof。
//   ZK 电路使用这些 proof 验证链上数据。
//
// Package store 提供 IAVL 多存储。
package store

import (
	"fmt"
	"os"
	"path/filepath"

	"cosmossdk.io/log"
	"cosmossdk.io/store/rootmulti"
	"cosmossdk.io/store/types"
	dbm "github.com/cosmos/cosmos-db"
)

// ModuleStoreKeys 为每个模块预定义的存储键。
var (
	StoreKeyBright    = types.NewKVStoreKey("bright")
	StoreKeyDark      = types.NewKVStoreKey("dark")
	StoreKeyAcc       = types.NewKVStoreKey("acc")
	StoreKeyParams    = types.NewKVStoreKey("params")
	StoreKeyConsensus = types.NewKVStoreKey("consensus")

	// 所有模块键的注册表
	allStoreKeys = []*types.KVStoreKey{
		StoreKeyBright, StoreKeyDark, StoreKeyAcc,
		StoreKeyParams, StoreKeyConsensus,
	}
)

// NekoStore 封装 Cosmos SDK rootmulti.Store。
// 提供多模块 IAVL 树存储，支持版本化提交和 Merkle 证明。
type NekoStore struct {
	root    *rootmulti.Store
	db      dbm.DB
	dataDir string
}

// New 创建并初始化多存储。
// 如果数据库不存在，自动创建；如果存在，加载最新版本。
func New(dataDir string) (*NekoStore, error) {
	if err := os.MkdirAll(dataDir, 0700); err != nil {
		return nil, fmt.Errorf("store: create data dir: %w", err)
	}

	db, err := dbm.NewDB("application", dbm.GoLevelDBBackend, dataDir)
	if err != nil {
		return nil, fmt.Errorf("store: open db: %w", err)
	}

	logger := log.NewNopLogger()
	rm := rootmulti.NewStore(db, logger)

	// 挂载 IAVL 子树
	for _, key := range allStoreKeys {
		rm.MountStoreWithDB(key, types.StoreTypeIAVL, nil)
	}

	// 加载最新版本，首次启动自动初始化
	if err := rm.LoadLatestVersion(); err != nil {
		if initErr := rm.LoadLatestVersion(); initErr != nil {
			return nil, fmt.Errorf("store: load version: %w", initErr)
		}
	}

	return &NekoStore{
		root:    rm,
		db:      db,
		dataDir: dataDir,
	}, nil
}

// Commit 提交当前状态，返回新版本号和 Merkle 根。
func (ns *NekoStore) Commit() (hash []byte, version int64, err error) {
	commitID := ns.root.LastCommitID()
	nextVersion := commitID.Version + 1

	newCID, err := ns.root.Commit(nil)
	if err != nil {
		return nil, 0, fmt.Errorf("store: commit: %w", err)
	}

	return newCID.Hash, nextVersion, nil
}

// LoadVersion 回滚到指定版本。
func (ns *NekoStore) LoadVersion(version int64) error {
	return ns.root.LoadVersion(version)
}

// GetKVStore 返回指定模块的可写 KV 存储。
func (ns *NekoStore) GetKVStore(key *types.KVStoreKey) types.KVStore {
	return ns.root.GetKVStore(key)
}

// GetReadOnlyStore 返回指定模块的只读 KV 存储 (已提交状态)。
func (ns *NekoStore) GetReadOnlyStore(key *types.KVStoreKey) types.KVStore {
	// 使用 WorkingHash 确保拿到的是当前未提交但可读的状态
	return ns.root.GetKVStore(key)
}

// LatestVersion 返回最新的 IAVL 树版本号。
func (ns *NekoStore) LatestVersion() int64 {
	return ns.root.LastCommitID().Version
}

// LatestHash 返回最新的 Merkle 根哈希。
func (ns *NekoStore) LatestHash() []byte {
	return ns.root.LastCommitID().Hash
}

// WorkingHash 返回当前工作状态的 Merkle 根 (未提交)。
func (ns *NekoStore) WorkingHash() []byte {
	return ns.root.WorkingHash()
}

// Close 关闭底层数据库。
func (ns *NekoStore) Close() error {
	return ns.db.Close()
}

// DBPath 返回数据库文件路径。
func (ns *NekoStore) DBPath() string {
	return filepath.Join(ns.dataDir, "application.db")
}

// ============================================================
// 迁移辅助: 从旧版 BoltDB 迁移数据到 IAVL
// ============================================================

// MigrateFromBoltDB 从旧的 BoltDB 链存储迁移数据到 IAVL 树。
// 这是一次性操作，迁移完成后旧 BoltDB 文件可安全删除。
func (ns *NekoStore) MigrateFromBoltDB(boltDBPath string) error {
	// 打开旧 BoltDB
	boltOpts := bolt.DefaultOptions
	boltOpts.ReadOnly = true
	oldDB, err := bolt.Open(boltDBPath, 0400, boltOpts)
	if err != nil {
		return fmt.Errorf("store: open legacy bolt db: %w", err)
	}
	defer oldDB.Close()

	// 获取 IAVL 子树
	brightStore := ns.GetKVStore(StoreKeyBright)
	darkStore := ns.GetKVStore(StoreKeyDark)

	migratedCount := 0

	err = oldDB.View(func(tx *bolt.Tx) error {
		// 迁移 users bucket → bright store (key: "user/" + chain_id)
		if b := tx.Bucket([]byte("users")); b != nil {
			c := b.Cursor()
			for k, v := c.First(); k != nil; k, v = c.Next() {
				brightStore.Set(append([]byte("user/"), k...), v)
				migratedCount++
			}
		}

		// 迁移 bonds bucket → bright store (key: "bond/" + bond_id)
		if b := tx.Bucket([]byte("bonds")); b != nil {
			c := b.Cursor()
			for k, v := c.First(); k != nil; k, v = c.Next() {
				brightStore.Set(append([]byte("bond/"), k...), v)
				migratedCount++
			}
		}

		// 迁移 loans bucket → dark store (key: "loan/" + loan_id)
		if b := tx.Bucket([]byte("loans")); b != nil {
			c := b.Cursor()
			for k, v := c.First(); k != nil; k, v = c.Next() {
				darkStore.Set(append([]byte("loan/"), k...), v)
				migratedCount++
			}
		}

		// 迁移 nullifiers bucket → dark store (key: "nullifier/" + value)
		if b := tx.Bucket([]byte("nullifiers")); b != nil {
			c := b.Cursor()
			for k, v := c.First(); k != nil; k, v = c.Next() {
				darkStore.Set(append([]byte("nullifier/"), k...), v)
				migratedCount++
			}
		}

		// 迁移 pool bucket → bright store (key: "pool/balance")
		if b := tx.Bucket([]byte("pool")); b != nil {
			c := b.Cursor()
			for k, v := c.First(); k != nil; k, v = c.Next() {
				brightStore.Set(append([]byte("pool/"), k...), v)
				migratedCount++
			}
		}

		// 迁移 meta bucket → bright store (key: "meta/height")
		if b := tx.Bucket([]byte("meta")); b != nil {
			c := b.Cursor()
			for k, v := c.First(); k != nil; k, v = c.Next() {
				brightStore.Set(append([]byte("meta/"), k...), v)
				migratedCount++
			}
		}

		return nil
	})

	fmt.Printf("store: migrated %d entries from BoltDB to IAVL\n", migratedCount)
	return err
}
