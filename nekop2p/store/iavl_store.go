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
	"cosmossdk.io/store/metrics"
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
	rm := rootmulti.NewStore(db, logger, metrics.NewNoOpMetrics())

	// 挂载 IAVL 子树
	for _, key := range allStoreKeys {
		rm.MountStoreWithDB(key, types.StoreTypeIAVL, nil)
	}

	// 加载最新版本，首次启动自动初始化
	if err := rm.LoadLatestVersion(); err != nil {
		return nil, fmt.Errorf("store: load version: %w", err)
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

	newCID := ns.root.Commit()
	if newCID.Version != nextVersion {
		return nil, 0, fmt.Errorf("store: expected version %d, got %d", nextVersion, newCID.Version)
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
// (Phase 6 实现 — 需要 bbolt 导入和双构建兼容)
// ============================================================

// MigrateFromBoltDB 从旧的 BoltDB 链存储迁移数据到 IAVL 树。
// TODO(Phase 6): 实现 BoltDB → IAVL 数据迁移
func (ns *NekoStore) MigrateFromBoltDB(boltDBPath string) error {
	return fmt.Errorf("store: BoltDB migration not yet implemented for Cosmos build (Phase 6)")
}
