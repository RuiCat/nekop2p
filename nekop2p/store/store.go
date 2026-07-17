//go:build !cosmos

// Package store 提供基于 BoltDB 的链上持久化存储。
//
// 所有链上数据（用户、债券、贷款、nullifier等）通过 BoltDB 持久化。
// 每次区块提交时创建一个 BoltDB 写事务，确保 ACID 特性。
package store

import (
	"encoding/binary"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"time"

	bolt "go.etcd.io/bbolt"
)

// 预定义 bucket 名称
var (
	bucketMeta       = []byte("meta")        // 元数据（区块高度）
	bucketUsers      = []byte("users")       // 用户账户
	bucketBonds      = []byte("bonds")       // 担保债券
	bucketLoans      = []byte("loans")       // 暗链贷款
	bucketNullifiers = []byte("nullifiers")  // 信用票据 nullifier
	bucketPool       = []byte("pool")        // 资金池
	bucketCredits    = []byte("credits")      // 信用票据承诺（Merkle树叶子）
)

// ChainStore 是 BoltDB 支持的链上存储。
type ChainStore struct {
	db   *bolt.DB
	path string
}

// New 创建或打开一个 BoltDB 链存储。
func New(dataDir string) (*ChainStore, error) {
	if err := os.MkdirAll(dataDir, 0700); err != nil {
		return nil, fmt.Errorf("store: create data dir: %w", err)
	}

	path := filepath.Join(dataDir, "chain.db")
	lockPath := path + ".lock"

	// 尝试打开，根据错误类型处理
	db, err := bolt.Open(path, 0600, &bolt.Options{Timeout: 1 * time.Second})
	if err != nil {
		// 超时 = 锁争用 → 仅删除锁文件，保留数据库
		if os.IsTimeout(err) {
			log.Printf("[store] lock timeout, removing stale lock: %s", lockPath)
			os.Remove(lockPath)
			db, err = bolt.Open(path, 0600, &bolt.Options{Timeout: 5 * time.Second})
			if err != nil {
				return nil, fmt.Errorf("store: open db after lock cleanup: %w", err)
			}
		} else {
			// 其他错误（文件损坏等）→ 记录并尝试恢复
			log.Printf("[store] db open failed: %v — attempting recovery", err)
			os.Remove(path)
			os.Remove(lockPath)
			db, err = bolt.Open(path, 0600, &bolt.Options{Timeout: 1 * time.Second})
			if err != nil {
				return nil, fmt.Errorf("store: open db after recovery: %w", err)
			}
		}
	}

	// 创建所有 bucket
	err = db.Update(func(tx *bolt.Tx) error {
		for _, name := range [][]byte{bucketMeta, bucketUsers, bucketBonds, bucketLoans, bucketNullifiers, bucketPool, bucketCredits} {
			if _, err := tx.CreateBucketIfNotExists(name); err != nil {
				return fmt.Errorf("create bucket %s: %w", name, err)
			}
		}
		return nil
	})
	if err != nil {
		db.Close()
		return nil, err
	}

	return &ChainStore{db: db, path: path}, nil
}

// Close 关闭数据库。
func (s *ChainStore) Close() error {
	return s.db.Close()
}

// Path 返回数据库文件路径。
func (s *ChainStore) Path() string { return s.path }

// ===== 写入（在一个事务中原子提交）=====

// Write 在单个 BoltDB 写事务中执行操作，保证原子性。
func (s *ChainStore) Write(fn func(tx *Tx) error) error {
	return s.db.Update(func(btx *bolt.Tx) error {
		return fn(&Tx{btx: btx})
	})
}

// Read 在只读事务中执行操作。
func (s *ChainStore) Read(fn func(tx *Tx) error) error {
	return s.db.View(func(btx *bolt.Tx) error {
		return fn(&Tx{btx: btx})
	})
}

// Tx 包装 BoltDB 事务，提供类型安全的存取方法。
type Tx struct {
	btx *bolt.Tx
}

// ===== 元数据 =====

// SetHeight 存储当前区块高度。
func (tx *Tx) SetHeight(h int64) error {
	b := tx.btx.Bucket(bucketMeta)
	var buf [8]byte
	binary.BigEndian.PutUint64(buf[:], uint64(h))
	return b.Put([]byte("height"), buf[:])
}

// Height 返回当前区块高度。
func (tx *Tx) Height() int64 {
	b := tx.btx.Bucket(bucketMeta)
	data := b.Get([]byte("height"))
	if data == nil {
		return 0
	}
	return int64(binary.BigEndian.Uint64(data))
}

// ===== 用户账户 =====

// PutUser 存储用户数据。
func (tx *Tx) PutUser(key string, data []byte) error {
	return tx.btx.Bucket(bucketUsers).Put([]byte(key), data)
}

// GetUser 读取用户数据。
func (tx *Tx) GetUser(key string) []byte {
	return tx.btx.Bucket(bucketUsers).Get([]byte(key))
}

// DeleteUser 删除用户。
func (tx *Tx) DeleteUser(key string) error {
	return tx.btx.Bucket(bucketUsers).Delete([]byte(key))
}

// ForEachUser 遍历所有用户。
func (tx *Tx) ForEachUser(fn func(key string, data []byte) error) error {
	return tx.btx.Bucket(bucketUsers).ForEach(func(k, v []byte) error {
		return fn(string(k), v)
	})
}

// ===== 担保债券 =====

func (tx *Tx) PutBond(key string, data []byte) error {
	return tx.btx.Bucket(bucketBonds).Put([]byte(key), data)
}

func (tx *Tx) GetBond(key string) []byte {
	return tx.btx.Bucket(bucketBonds).Get([]byte(key))
}

func (tx *Tx) ForEachBond(fn func(key string, data []byte) error) error {
	return tx.btx.Bucket(bucketBonds).ForEach(func(k, v []byte) error {
		return fn(string(k), v)
	})
}

// ===== 暗链贷款 =====

func (tx *Tx) PutLoan(key string, data []byte) error {
	return tx.btx.Bucket(bucketLoans).Put([]byte(key), data)
}

func (tx *Tx) GetLoan(key string) []byte {
	return tx.btx.Bucket(bucketLoans).Get([]byte(key))
}

func (tx *Tx) ForEachLoan(fn func(key string, data []byte) error) error {
	return tx.btx.Bucket(bucketLoans).ForEach(func(k, v []byte) error {
		return fn(string(k), v)
	})
}

// ===== Nullifier =====

func (tx *Tx) PutNullifier(key string) error {
	return tx.btx.Bucket(bucketNullifiers).Put([]byte(key), []byte{1})
}

func (tx *Tx) HasNullifier(key string) bool {
	return tx.btx.Bucket(bucketNullifiers).Get([]byte(key)) != nil
}

// ===== 信用票据承诺 (Merkle 树叶子) =====

// PutCreditCommitment 添加信用票据承诺。
func (tx *Tx) PutCreditCommitment(key string, data []byte) error {
	return tx.btx.Bucket(bucketCredits).Put([]byte(key), data)
}

// GetCreditCommitment 获取信用票据承诺。
func (tx *Tx) GetCreditCommitment(key string) []byte {
	return tx.btx.Bucket(bucketCredits).Get([]byte(key))
}

// DeleteCreditCommitment 删除信用票据承诺。
func (tx *Tx) DeleteCreditCommitment(key string) error {
	return tx.btx.Bucket(bucketCredits).Delete([]byte(key))
}

// ForEachCreditCommitment 遍历所有信用票据承诺。
func (tx *Tx) ForEachCreditCommitment(fn func(key string, data []byte) error) error {
	return tx.btx.Bucket(bucketCredits).ForEach(func(k, v []byte) error {
		return fn(string(k), v)
	})
}

// ===== 资金池 =====

func (tx *Tx) PutPool(data []byte) error {
	return tx.btx.Bucket(bucketPool).Put([]byte("pool"), data)
}

func (tx *Tx) GetPool() []byte {
	return tx.btx.Bucket(bucketPool).Get([]byte("pool"))
}

// ===== 辅助函数 =====

// JSON 编解码

func Marshal(v interface{}) ([]byte, error) { return json.Marshal(v) }
func Unmarshal(data []byte, v interface{}) error { return json.Unmarshal(data, v) }
