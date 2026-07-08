// Package vrg 社区虚拟化身管理 (VRG-01)。
//
// 社区虚拟化身 (Community Avatar):
//   由门限签名方案为社区生成的集合身份，使社区在虚拟根网图上表现为单一节点。
//
// 核心机制:
//   - 门限 t = ceil(2n/3): 至少 2/3 正式记录节点签名才有效
//   - 社区状态根由成员联合签署
//   - 集合身份绑定创世邀请链的拓扑位置
//   - 成员生命周期管理（加入/退出/密钥轮换）
//
// 依赖:
//   - crypto/threshold_sign.go: CommunityAvatar, ThresholdSignature 基础结构
//   - topology.go: GenesisTree 拓扑位置
package vrg

import (
	"crypto/sha256"
	"fmt"
	"sort"
	"sync"

	"github.com/nekop2p/nekop2p/crypto"
)

// ============================================================
// AvatarRegistry 社区虚拟化身注册表
// ============================================================

// AvatarRegistry 管理所有社区的虚拟化身。
// 每个社区在创世邀请链上注册后，通过门限签名生成集合身份。
type AvatarRegistry struct {
	mu        sync.RWMutex
	avatars   map[[32]byte]*crypto.CommunityAvatar // communityID → avatar
	byName    map[string][32]byte                  // 社区名称 → communityID
	genesisID [32]byte                             // 创世社区 ID
}

// NewAvatarRegistry 创建虚拟化身注册表。
func NewAvatarRegistry() *AvatarRegistry {
	return &AvatarRegistry{
		avatars: make(map[[32]byte]*crypto.CommunityAvatar),
		byName:  make(map[string][32]byte),
	}
}

// RegisterCommunity 注册一个新社区的虚拟化身。
// 社区 ID 由拓扑位置确定性生成，成员公钥列表在注册时确定。
func (ar *AvatarRegistry) RegisterCommunity(
	name string,
	memberKeys [][32]byte,
	topoPosition []byte,
	createdAt int64,
) (*crypto.CommunityAvatar, error) {
	ar.mu.Lock()
	defer ar.mu.Unlock()

	// 检查名称不重复
	if _, exists := ar.byName[name]; exists {
		return nil, fmt.Errorf("avatar: community name %q already registered", name)
	}

	// 使用 crypto 层创建虚拟化身
	avatar := crypto.NewCommunityAvatar(memberKeys, topoPosition, createdAt)

	// 检查社区 ID 不重复
	if _, exists := ar.avatars[avatar.CommunityID]; exists {
		return nil, fmt.Errorf("avatar: community ID %x already registered", avatar.CommunityID[:8])
	}

	ar.avatars[avatar.CommunityID] = avatar
	ar.byName[name] = avatar.CommunityID

	return avatar, nil
}

// GetAvatar 按社区 ID 获取虚拟化身。
func (ar *AvatarRegistry) GetAvatar(communityID [32]byte) *crypto.CommunityAvatar {
	ar.mu.RLock()
	defer ar.mu.RUnlock()
	return ar.avatars[communityID]
}

// GetAvatarByName 按名称获取虚拟化身。
func (ar *AvatarRegistry) GetAvatarByName(name string) *crypto.CommunityAvatar {
	ar.mu.RLock()
	defer ar.mu.RUnlock()
	id, exists := ar.byName[name]
	if !exists {
		return nil
	}
	return ar.avatars[id]
}

// ListAvatars 列出所有已注册的社区虚拟化身。
func (ar *AvatarRegistry) ListAvatars() []*crypto.CommunityAvatar {
	ar.mu.RLock()
	defer ar.mu.RUnlock()

	result := make([]*crypto.CommunityAvatar, 0, len(ar.avatars))
	for _, avatar := range ar.avatars {
		result = append(result, avatar)
	}
	return result
}

// SetGenesisID 设置创世社区 ID。
func (ar *AvatarRegistry) SetGenesisID(id [32]byte) {
	ar.mu.Lock()
	defer ar.mu.Unlock()
	ar.genesisID = id
}

// GetGenesisID 获取创世社区 ID。
func (ar *AvatarRegistry) GetGenesisID() [32]byte {
	ar.mu.RLock()
	defer ar.mu.RUnlock()
	return ar.genesisID
}

// ============================================================
// 成员管理
// ============================================================

// AddMember 向社区添加新成员。
// 新成员必须是正式记录节点，且通过现有门限签名批准。
func (ar *AvatarRegistry) AddMember(
	communityID [32]byte,
	newMemberKey [32]byte,
	approvalSig *crypto.ThresholdSignature,
) error {
	ar.mu.Lock()
	defer ar.mu.Unlock()

	avatar, exists := ar.avatars[communityID]
	if !exists {
		return fmt.Errorf("avatar: community %x not found", communityID[:8])
	}

	// 验证批准签名的数据包含新成员公钥
	expectedData := makeAddMemberData(communityID, newMemberKey)
	if !bytesEqual(approvalSig.SignedData, expectedData) {
		return fmt.Errorf("avatar: approval data mismatch for add member")
	}

	// 验证门限签名
	valid, err := avatar.VerifyThresholdSignature(approvalSig)
	if err != nil {
		return fmt.Errorf("avatar: verify approval: %w", err)
	}
	if !valid {
		return fmt.Errorf("avatar: insufficient approval signatures for add member")
	}

	// 检查成员是否已存在
	for _, key := range avatar.MemberKeys {
		if key == newMemberKey {
			return fmt.Errorf("avatar: member already in community")
		}
	}

	// 添加成员并重新排序
	avatar.MemberKeys = append(avatar.MemberKeys, newMemberKey)
	sortMemberKeys(avatar.MemberKeys)
	avatar.TotalMembers = len(avatar.MemberKeys)

	// 重新计算门限
	cfg := crypto.NewThresholdConfig(avatar.TotalMembers)
	avatar.Threshold = cfg.T

	return nil
}

// RemoveMember 从社区移除成员。
func (ar *AvatarRegistry) RemoveMember(
	communityID [32]byte,
	memberKey [32]byte,
	removalSig *crypto.ThresholdSignature,
) error {
	ar.mu.Lock()
	defer ar.mu.Unlock()

	avatar, exists := ar.avatars[communityID]
	if !exists {
		return fmt.Errorf("avatar: community %x not found", communityID[:8])
	}

	// 验证移除签名
	expectedData := makeRemoveMemberData(communityID, memberKey)
	if !bytesEqual(removalSig.SignedData, expectedData) {
		return fmt.Errorf("avatar: removal data mismatch")
	}

	valid, err := avatar.VerifyThresholdSignature(removalSig)
	if err != nil {
		return fmt.Errorf("avatar: verify removal: %w", err)
	}
	if !valid {
		return fmt.Errorf("avatar: insufficient signatures for remove member")
	}

	// 不能移除所有成员
	if avatar.TotalMembers <= 1 {
		return fmt.Errorf("avatar: cannot remove last member")
	}

	// 查找并移除成员
	idx := -1
	for i, key := range avatar.MemberKeys {
		if key == memberKey {
			idx = i
			break
		}
	}
	if idx < 0 {
		return fmt.Errorf("avatar: member not found in community")
	}

	avatar.MemberKeys = append(avatar.MemberKeys[:idx], avatar.MemberKeys[idx+1:]...)
	avatar.TotalMembers = len(avatar.MemberKeys)

	// 重新计算门限
	cfg := crypto.NewThresholdConfig(avatar.TotalMembers)
	avatar.Threshold = cfg.T

	return nil
}

// RotateMemberKey 轮换社区成员的密钥。
func (ar *AvatarRegistry) RotateMemberKey(
	communityID [32]byte,
	oldKey, newKey [32]byte,
	rotationSig *crypto.ThresholdSignature,
) error {
	ar.mu.Lock()
	defer ar.mu.Unlock()

	avatar, exists := ar.avatars[communityID]
	if !exists {
		return fmt.Errorf("avatar: community %x not found", communityID[:8])
	}

	expectedData := makeRotateKeyData(communityID, oldKey, newKey)
	if !bytesEqual(rotationSig.SignedData, expectedData) {
		return fmt.Errorf("avatar: rotation data mismatch")
	}

	valid, err := avatar.VerifyThresholdSignature(rotationSig)
	if err != nil {
		return fmt.Errorf("avatar: verify rotation: %w", err)
	}
	if !valid {
		return fmt.Errorf("avatar: insufficient signatures for key rotation")
	}

	// 查找旧密钥并替换
	found := false
	for i, key := range avatar.MemberKeys {
		if key == oldKey {
			avatar.MemberKeys[i] = newKey
			found = true
			break
		}
	}
	if !found {
		return fmt.Errorf("avatar: old key not found in community")
	}

	// 重新排序
	sortMemberKeys(avatar.MemberKeys)

	return nil
}

// ============================================================
// 社区状态根签署
// ============================================================

// CommunityStateManager 社区状态管理器。
// 协调门限签名以签署社区状态根。
type CommunityStateManager struct {
	mu         sync.RWMutex
	registry   *AvatarRegistry
	stateRoots map[[32]byte]*crypto.CommunityStateRoot // communityID → latest root
	history    map[[32]byte][]*crypto.CommunityStateRoot // communityID → root history
}

// NewCommunityStateManager 创建社区状态管理器。
func NewCommunityStateManager(registry *AvatarRegistry) *CommunityStateManager {
	return &CommunityStateManager{
		registry:   registry,
		stateRoots: make(map[[32]byte]*crypto.CommunityStateRoot),
		history:    make(map[[32]byte][]*crypto.CommunityStateRoot),
	}
}

// ProposeStateRoot 提议新的社区状态根。
// 由任何正式记录节点发起。提案需要通过门限签名才能生效。
func (csm *CommunityStateManager) ProposeStateRoot(
	communityID [32]byte,
	stateHash [32]byte,
	epochNumber uint64,
	timestamp int64,
) (*crypto.CommunityStateRoot, error) {
	csm.mu.Lock()
	defer csm.mu.Unlock()

	avatar := csm.registry.GetAvatar(communityID)
	if avatar == nil {
		return nil, fmt.Errorf("state: community %x not registered", communityID[:8])
	}

	prevRoot := csm.stateRoots[communityID]
	var prevHash [32]byte
	if prevRoot != nil {
		prevHash = prevRoot.StateHash
	}

	root := &crypto.CommunityStateRoot{
		CommunityID: communityID,
		EpochNumber: epochNumber,
		StateHash:   stateHash,
		PrevRoot:    prevHash,
		Timestamp:   timestamp,
	}

	// 不直接替换，等门限签名收集完成后再 FinalizeStateRoot
	return root, nil
}

// FinalizeStateRoot 收集到足够门限签名后最终确定状态根。
func (csm *CommunityStateManager) FinalizeStateRoot(
	root *crypto.CommunityStateRoot,
	signature *crypto.ThresholdSignature,
) error {
	csm.mu.Lock()
	defer csm.mu.Unlock()

	avatar := csm.registry.GetAvatar(root.CommunityID)
	if avatar == nil {
		return fmt.Errorf("state: community %x not registered", root.CommunityID[:8])
	}

	// 验证门限签名
	valid, err := avatar.VerifyThresholdSignature(signature)
	if err != nil {
		return fmt.Errorf("state: verify signature: %w", err)
	}
	if !valid {
		return fmt.Errorf("state: insufficient threshold signatures")
	}

	// 保存状态根
	csm.stateRoots[root.CommunityID] = root
	csm.history[root.CommunityID] = append(csm.history[root.CommunityID], root)

	return nil
}

// GetStateRoot 获取社区最新状态根。
func (csm *CommunityStateManager) GetStateRoot(communityID [32]byte) *crypto.CommunityStateRoot {
	csm.mu.RLock()
	defer csm.mu.RUnlock()
	return csm.stateRoots[communityID]
}

// GetStateHistory 获取社区状态根历史。
func (csm *CommunityStateManager) GetStateHistory(communityID [32]byte) []*crypto.CommunityStateRoot {
	csm.mu.RLock()
	defer csm.mu.RUnlock()
	return csm.history[communityID]
}

// ============================================================
// 辅助函数
// ============================================================

// makeAddMemberData 生成添加成员批准数据。
func makeAddMemberData(communityID, newMemberKey [32]byte) []byte {
	h := sha256.New()
	h.Write([]byte("nekop2p-add-member-v1"))
	h.Write(communityID[:])
	h.Write(newMemberKey[:])
	return h.Sum(nil)
}

// makeRemoveMemberData 生成移除成员批准数据。
func makeRemoveMemberData(communityID, memberKey [32]byte) []byte {
	h := sha256.New()
	h.Write([]byte("nekop2p-remove-member-v1"))
	h.Write(communityID[:])
	h.Write(memberKey[:])
	return h.Sum(nil)
}

// makeRotateKeyData 生成密钥轮换批准数据。
func makeRotateKeyData(communityID, oldKey, newKey [32]byte) []byte {
	h := sha256.New()
	h.Write([]byte("nekop2p-rotate-key-v1"))
	h.Write(communityID[:])
	h.Write(oldKey[:])
	h.Write(newKey[:])
	return h.Sum(nil)
}

// sortMemberKeys 排序成员公钥列表（确定性）。
func sortMemberKeys(keys [][32]byte) {
	sort.Slice(keys, func(i, j int) bool {
		for k := 0; k < 32; k++ {
			if keys[i][k] != keys[j][k] {
				return keys[i][k] < keys[j][k]
			}
		}
		return false
	})
}
