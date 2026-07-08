// Package crypto 门限签名实现。
//
// 实现 t-of-n 门限签名用于社区集合身份生成。
// 使用 Ed25519 的 Shamir 秘密共享变体（简化实现）。
//
// 安全参数:
//   - t = ceil(2n/3): 至少 2/3 成员签名才有效
//   - n: 社区内正式记录节点数量
package crypto

import (
	"crypto/ed25519"
	"crypto/sha256"
	"fmt"
	"sort"
)

// ThresholdConfig 门限签名配置。
type ThresholdConfig struct {
	T int // 门限值（最少需要的签名数）
	N int // 总成员数
}

// NewThresholdConfig 从成员数创建门限配置。
// t = ceil(2n/3)
func NewThresholdConfig(n int) ThresholdConfig {
	t := (2*n + 2) / 3 // ceil(2n/3)
	if t < 1 {
		t = 1
	}
	return ThresholdConfig{T: t, N: n}
}

// CommunityAvatar 社区的集合虚拟化身。
// 由门限签名方案生成的社区级身份。
type CommunityAvatar struct {
	CommunityID  [32]byte   // 社区唯一标识 = SHA256(创世邀请链拓扑位置)
	MemberKeys   [][32]byte // 成员 Ed25519 公钥列表（有序）
	Threshold    int         // 门限值 t
	TotalMembers int         // 总成员数 n
	TopoPosition []byte      // 创世邀请链的拓扑位置
	CreatedAt    int64       // 创建时间戳
}

// GenerateCommunityID 从创世邀请链拓扑位置生成社区 ID。
func GenerateCommunityID(topoPosition []byte) [32]byte {
	h := sha256.New()
	h.Write([]byte("nekop2p-community-v1"))
	h.Write(topoPosition)
	var result [32]byte
	copy(result[:], h.Sum(nil))
	return result
}

// NewCommunityAvatar 创建一个新的社区虚拟化身。
func NewCommunityAvatar(memberKeys [][32]byte, topoPosition []byte, createdAt int64) *CommunityAvatar {
	// 排序成员公钥以确保确定性
	sorted := make([][32]byte, len(memberKeys))
	copy(sorted, memberKeys)
	sort.Slice(sorted, func(i, j int) bool {
		for k := 0; k < 32; k++ {
			if sorted[i][k] != sorted[j][k] {
				return sorted[i][k] < sorted[j][k]
			}
		}
		return false
	})

	cfg := NewThresholdConfig(len(sorted))

	return &CommunityAvatar{
		CommunityID:  GenerateCommunityID(topoPosition),
		MemberKeys:   sorted,
		Threshold:    cfg.T,
		TotalMembers: cfg.N,
		TopoPosition: topoPosition,
		CreatedAt:    createdAt,
	}
}

// ============================================================
// 简化门限签名验证
// ============================================================

// ThresholdSignature 门限签名。
// 包含一组 Ed25519 签名和对应的成员索引。
type ThresholdSignature struct {
	Signatures  []ThresholdSigEntry // 成员签名列表
	SignedData  []byte              // 被签名的数据
	T           int                 // 门限值
}

// ThresholdSigEntry 单条门限签名条目。
type ThresholdSigEntry struct {
	MemberIndex int    // 成员在 MemberKeys 中的索引
	PublicKey   [32]byte
	Signature   [64]byte
}

// VerifyThresholdSignature 验证门限签名。
// 返回 true 如果有 >= t 个有效签名。
func (ca *CommunityAvatar) VerifyThresholdSignature(ts *ThresholdSignature) (bool, error) {
	if ts.T != ca.Threshold {
		return false, fmt.Errorf("threshold mismatch: got %d, want %d", ts.T, ca.Threshold)
	}

	validCount := 0
	seenMembers := make(map[int]bool)

	for _, entry := range ts.Signatures {
		// 检查成员索引有效性
		if entry.MemberIndex < 0 || entry.MemberIndex >= len(ca.MemberKeys) {
			continue
		}
		// 防止同一成员重复签名
		if seenMembers[entry.MemberIndex] {
			continue
		}
		// 验证公钥匹配
		if entry.PublicKey != ca.MemberKeys[entry.MemberIndex] {
			continue
		}
		// 验证 Ed25519 签名
		if ed25519.Verify(entry.PublicKey[:], ts.SignedData, entry.Signature[:]) {
			validCount++
			seenMembers[entry.MemberIndex] = true
		}
	}

	return validCount >= ca.Threshold, nil
}

// SignThreshold 成员对数据签名（门限签名中的一份）。
func SignThreshold(privateKey [64]byte, data []byte, memberIndex int) ThresholdSigEntry {
	pubKey := [32]byte{}
	copy(pubKey[:], privateKey[32:]) // Ed25519 私钥后 32 字节是公钥

	sig := ed25519.Sign(privateKey[:], data)
	entry := ThresholdSigEntry{
		MemberIndex: memberIndex,
		PublicKey:   pubKey,
	}
	copy(entry.Signature[:], sig)
	return entry
}

// GatherThresholdSignatures 收集门限签名。
func GatherThresholdSignatures(entries []ThresholdSigEntry, data []byte, t int) *ThresholdSignature {
	return &ThresholdSignature{
		Signatures: entries,
		SignedData: data,
		T:          t,
	}
}

// ============================================================
// 社区状态根
// ============================================================

// CommunityStateRoot 社区状态根（由门限签名签署）。
type CommunityStateRoot struct {
	CommunityID  [32]byte
	EpochNumber  uint64   // Epoch 编号
	StateHash    [32]byte // 社区状态哈希
	PrevRoot     [32]byte // 上一个状态根
	Timestamp    int64
}
