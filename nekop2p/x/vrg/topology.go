//go:build !cosmos

// Package vrg 虚拟根网图拓扑计算。
//
// 基于创世邀请链的 LCA (Lowest Common Ancestor) 计算任意两社区的距离。
//
// 社区拓扑:
//   创世社区 (Genesis) 邀请 → 子社区 A, B
//   子社区 A 邀请 → 孙社区 A1, A2
//   形成树状拓扑（邀请链 = 有向无环的生成树）
//
// 距离公式:
//   d(V_A, V_B) = depth(V_A) + depth(V_B) - 2 × depth(LCA(V_A, V_B))
//
// 安全系数:
//   S = f(d, Bond质押额) — 距离越远，所需质押越多
package vrg

import (
	"fmt"
	"math"
	"sync"
)

// ============================================================
// 创世邀请链树
// ============================================================

// GenesisTree 创世邀请链树——社区的谱系结构。
type GenesisTree struct {
	mu     sync.RWMutex
	nodes  map[[32]byte]*TreeNode // communityID → node
	rootID [32]byte
}

// TreeNode 树节点。
type TreeNode struct {
	CommunityID [32]byte   // 社区虚拟化身 ID
	ParentID    [32]byte   // 邀请方社区 ID（根节点为零值）
	Depth       int        // 从根开始的深度
	Children    [][32]byte // 被邀请的子社区
	BondAmount  uint64     // 社区质押金额
	CreatedAt   int64      // 创建时间
	InviterID   string     // 邀请人 chain_id
}

// NewGenesisTree 创建创世邀请链树。
func NewGenesisTree(rootID [32]byte) *GenesisTree {
	tree := &GenesisTree{
		nodes:  make(map[[32]byte]*TreeNode),
		rootID: rootID,
	}
	tree.nodes[rootID] = &TreeNode{
		CommunityID: rootID,
		Depth:       0,
		CreatedAt:   0,
	}
	return tree
}

// AddCommunity 向邀请链树中添加社区。
func (gt *GenesisTree) AddCommunity(communityID, parentID [32]byte, bondAmount uint64, inviterID string, createdAt int64) error {
	gt.mu.Lock()
	defer gt.mu.Unlock()

	// 检查父节点存在
	parent, exists := gt.nodes[parentID]
	if !exists {
		return fmt.Errorf("topology: parent community %x not in genesis tree", parentID[:8])
	}

	// 检查社区不重复
	if _, exists := gt.nodes[communityID]; exists {
		return fmt.Errorf("topology: community %x already exists", communityID[:8])
	}

	node := &TreeNode{
		CommunityID: communityID,
		ParentID:    parentID,
		Depth:       parent.Depth + 1,
		BondAmount:  bondAmount,
		CreatedAt:   createdAt,
		InviterID:   inviterID,
	}
	gt.nodes[communityID] = node
	parent.Children = append(parent.Children, communityID)

	return nil
}

// GetCommunity 获取社区节点。
func (gt *GenesisTree) GetCommunity(communityID [32]byte) *TreeNode {
	gt.mu.RLock()
	defer gt.mu.RUnlock()
	return gt.nodes[communityID]
}

// ============================================================
// LCA 计算
// ============================================================

// LCA 计算两社区的最低共同祖先 (Lowest Common Ancestor)。
// 使用深度对齐 + 同步上溯算法。
func (gt *GenesisTree) LCA(idA, idB [32]byte) ([32]byte, error) {
	gt.mu.RLock()
	defer gt.mu.RUnlock()

	nodeA := gt.nodes[idA]
	nodeB := gt.nodes[idB]
	if nodeA == nil || nodeB == nil {
		return [32]byte{}, fmt.Errorf("topology: community not found")
	}

	// 将较深的节点上溯到相同深度
	for nodeA.Depth > nodeB.Depth {
		nodeA = gt.nodes[nodeA.ParentID]
	}
	for nodeB.Depth > nodeA.Depth {
		nodeB = gt.nodes[nodeB.ParentID]
	}

	// 同步上溯直到找到共同祖先
	for nodeA.CommunityID != nodeB.CommunityID {
		nodeA = gt.nodes[nodeA.ParentID]
		nodeB = gt.nodes[nodeB.ParentID]
	}

	return nodeA.CommunityID, nil
}

// Distance 计算两社区的拓扑距离。
// d(A, B) = depth(A) + depth(B) - 2 × depth(LCA(A, B))
func (gt *GenesisTree) Distance(idA, idB [32]byte) (int, error) {
	lca, err := gt.LCA(idA, idB)
	if err != nil {
		return 0, err
	}

	gt.mu.RLock()
	nodeA := gt.nodes[idA]
	nodeB := gt.nodes[idB]
	lcaNode := gt.nodes[lca]
	gt.mu.RUnlock()

	if nodeA == nil || nodeB == nil || lcaNode == nil {
		return 0, fmt.Errorf("topology: node not found")
	}

	return nodeA.Depth + nodeB.Depth - 2*lcaNode.Depth, nil
}

// ============================================================
// 安全系数
// ============================================================

// SecurityCoefficient 计算两社区交易的安全系数。
// S = f(d, Bond) — 综合拓扑距离和质押金额。
//
// 公式:
//
//	S = Bond_min / (1 + d)²
//	其中 Bond_min = min(Bond_A, Bond_B)
//	d 越小 → S 越高 → 越安全
func (gt *GenesisTree) SecurityCoefficient(idA, idB [32]byte) (float64, error) {
	d, err := gt.Distance(idA, idB)
	if err != nil {
		return 0, err
	}

	gt.mu.RLock()
	nodeA := gt.nodes[idA]
	nodeB := gt.nodes[idB]
	gt.mu.RUnlock()

	if nodeA == nil || nodeB == nil {
		return 0, fmt.Errorf("topology: community not found")
	}

	minBond := nodeA.BondAmount
	if nodeB.BondAmount < minBond {
		minBond = nodeB.BondAmount
	}

	// S = Bond_min / (1+d)²
	s := float64(minBond) / math.Pow(float64(1+d), 2)
	return s, nil
}

// ============================================================
// 清算阈值检查
// ============================================================

// TrustThreshold 信任阈值配置。
type TrustThreshold struct {
	MaxDistance       int     // 最大允许拓扑距离
	MinSecurityCoeff  float64 // 最小安全系数
	AdditionalBond    uint64  // 超过距离阈值时需要的额外质押
}

// DefaultTrustThreshold 默认信任阈值。
func DefaultTrustThreshold() TrustThreshold {
	return TrustThreshold{
		MaxDistance:      3,    // 距离 ≤ 3 跳可直接清算
		MinSecurityCoeff: 100.0, // 安全系数 ≥ 100
		AdditionalBond:   500,   // 额外质押金额
	}
}

// CanClearMacro 检查两社区是否可以进行宏观清算。
func (gt *GenesisTree) CanClearMacro(idA, idB [32]byte, threshold TrustThreshold) (bool, uint64, error) {
	d, err := gt.Distance(idA, idB)
	if err != nil {
		return false, 0, err
	}

	s, err := gt.SecurityCoefficient(idA, idB)
	if err != nil {
		return false, 0, err
	}

	// 距离在阈值内 → 允许清算
	if d <= threshold.MaxDistance {
		return true, 0, nil
	}

	// 距离超过阈值 → 需要额外质押
	if s >= threshold.MinSecurityCoeff {
		return true, 0, nil
	}

	// 不满足条件 → 需要额外质押
	additionalBond := uint64(float64(threshold.AdditionalBond) * float64(d-threshold.MaxDistance))
	return false, additionalBond, nil
}

// ============================================================
// 树遍历
// ============================================================

// SubtreeCommunities 返回指定社区的所有子社区（含自身）。
func (gt *GenesisTree) SubtreeCommunities(communityID [32]byte) [][32]byte {
	gt.mu.RLock()
	defer gt.mu.RUnlock()

	var result [][32]byte
	gt.collectSubtree(communityID, &result)
	return result
}

func (gt *GenesisTree) collectSubtree(id [32]byte, result *[][32]byte) {
	node := gt.nodes[id]
	if node == nil {
		return
	}
	*result = append(*result, id)
	for _, child := range node.Children {
		gt.collectSubtree(child, result)
	}
}

// AncestorChain 返回从根到指定社区的祖先链。
func (gt *GenesisTree) AncestorChain(communityID [32]byte) [][32]byte {
	gt.mu.RLock()
	defer gt.mu.RUnlock()

	var chain [][32]byte
	current := gt.nodes[communityID]
	for current != nil {
		chain = append(chain, current.CommunityID)
		if current.Depth == 0 {
			break
		}
		current = gt.nodes[current.ParentID]
	}

	// 反转顺序（从根到叶）
	for i, j := 0, len(chain)-1; i < j; i, j = i+1, j-1 {
		chain[i], chain[j] = chain[j], chain[i]
	}
	return chain
}

// TotalCommunities 返回社区总数。
func (gt *GenesisTree) TotalCommunities() int {
	gt.mu.RLock()
	defer gt.mu.RUnlock()
	return len(gt.nodes)
}

// MaxDepth 返回树的最大深度。
func (gt *GenesisTree) MaxDepth() int {
	gt.mu.RLock()
	defer gt.mu.RUnlock()
	maxD := 0
	for _, n := range gt.nodes {
		if n.Depth > maxD {
			maxD = n.Depth
		}
	}
	return maxD
}
