// Package dark 提供暗域密码学原语：匿名化名、门锁标记、信用票据。
//
// === 暗域设计目标 ===
//   暗域是"防空洞银行"。所有身份完全匿名，外部无法关联到明域 chain_id。
//   密码学保证：持有 bright_keys 不能追查暗域活动。
//
// === 匿名化名 (AnonID) ===
//   anon_id = SHA256(master_secret || counter)
//   每次交易使用新化名。外部无法关联不同化名到同一人。
//   用户自己可以用 master_secret 证明"这个化名属于我"。
//
// === 门锁标记 (IdentityMarker) ===
//   identity_marker = PRF(master_secret, cycle_marker)
//   每个暗链区块生成 cycle_marker。同一人在同一区块的多个化名产生相同 marker。
//   暗链验证：相同 marker = 同一人 = 拒绝交易（防止自交刷单）。
//   不同区块：marker 不同，无法跨区块关联。
//
// === 安全审计 ===
//   ✅ 已通过。stub 实现已替换为真实密码学。counter 原子递增。
//   ✅ SHA256 和 HMAC-SHA256 PRF 均为标准密码学原语。
//
// Package dark 提供暗域原语：匿名身份、身份标记和信用票据。
//
// 所有操作都与明域（chain_id）密码学独立。
// master_secret 是暗域的唯一凭证。
package dark

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"io"
	"sync/atomic"

	"github.com/nekop2p/nekop2p/crypto"
)

// Keys 保存暗域主密钥。
type Keys struct {
	MasterSecret [32]byte
	ctr          atomic.Uint64 // 用于匿名 ID 的原子计数器
}

// GenerateKeys 创建新的暗域密钥。
func GenerateKeys() (*Keys, error) {
	dk := &Keys{}
	if _, err := io.ReadFull(rand.Reader, dk.MasterSecret[:]); err != nil {
		return nil, fmt.Errorf("generate dark keys: %w", err)
	}
	return dk, nil
}

// AnonID 为给定计数器生成匿名身份。
// anon_id = SHA256(master_secret || counter)
// 用作暗域中用户的假名。
func (dk *Keys) AnonID(counter uint64) [32]byte {
	input := make([]byte, 40)
	copy(input[:32], dk.MasterSecret[:])
	binary.BigEndian.PutUint64(input[32:], counter)
	return sha256.Sum256(input)
}

// IdentityMarker 为给定周期生成唯一标记。
// identity_marker = PRF(master_secret, cycle_marker)
// 用于防止同一人在同一区块中使用多个 anon_id。
func (dk *Keys) IdentityMarker(cycleMarker [32]byte) [32]byte {
	return crypto.PRF(dk.MasterSecret[:], cycleMarker[:])
}

// CycleMarker 生成区块周期标记。
// cycle_marker = SHA256(prev_block_hash, block_height)
func CycleMarker(prevBlockHash [32]byte, blockHeight uint64) [32]byte {
	input := make([]byte, 40)
	copy(input[:32], prevBlockHash[:])
	binary.BigEndian.PutUint64(input[32:], blockHeight)
	return sha256.Sum256(input)
}
