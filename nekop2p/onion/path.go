package onion

import (
	"crypto/rand"
	"fmt"
	"math/big"
)

// cryptoRandIntn 返回 [0, max) 范围内的密码学安全无偏随机整数。
// 使用 rejection sampling 避免模偏差 (crypto/rand.Int 保证均匀分布)。
func cryptoRandIntn(max int) int {
	if max <= 0 {
		return 0
	}
	n, err := rand.Int(rand.Reader, big.NewInt(int64(max)))
	if err != nil {
		panic(fmt.Sprintf("onion: crypto/rand failed: %v", err))
	}
	return int(n.Int64())
}

func nodeKey(h Hop) string {
	return string(h.ChainID[:])
}

// PathSelector 使用节点本地拓扑选择洋葱路径。
type PathSelector struct {
	coreFriends []Hop
	publicNodes []Hop
}

// NewPathSelector 创建路径选择器。
func NewPathSelector(coreFriends, publicNodes []Hop) *PathSelector {
	return &PathSelector{
		coreFriends: coreFriends,
		publicNodes: publicNodes,
	}
}

// SelectPath 选择指定长度的路径。
// 选择策略：
//   入口: 核心好友（可信）
//   中间: 公开/中继节点（随机）
//   出口: 公开/中继节点（随机）
func (ps *PathSelector) SelectPath(length int) (Path, error) {
	if length < MinHops || length > MaxHops {
		return nil, fmt.Errorf("invalid path length: %d", length)
	}

	path := make(Path, length)

	// 入口：优先选择核心好友
	if len(ps.coreFriends) > 0 {
		path[0] = ps.coreFriends[cryptoRandIntn(len(ps.coreFriends))]
	} else if len(ps.publicNodes) > 0 {
		path[0] = ps.publicNodes[cryptoRandIntn(len(ps.publicNodes))]
	} else {
		return nil, fmt.Errorf("no available entry nodes")
	}

	// 中间跳：从所有可用节点中随机选择，确保全局不重复
	allNodes := append([]Hop{}, ps.coreFriends...)
	allNodes = append(allNodes, ps.publicNodes...)

	used := make(map[string]bool)
	used[nodeKey(path[0])] = true

	for i := 1; i < length; i++ {
		if len(allNodes) == 0 {
			return nil, fmt.Errorf("no available nodes for hop %d", i)
		}
		// 从未使用的节点中选择，最多尝试 100 次
		chosen := false
		for attempt := 0; attempt < 100; attempt++ {
			candidate := allNodes[cryptoRandIntn(len(allNodes))]
			if !used[nodeKey(candidate)] {
				path[i] = candidate
				used[nodeKey(candidate)] = true
				chosen = true
				break
			}
		}
		if !chosen {
			return nil, fmt.Errorf("onion: not enough distinct nodes for hop %d/%d", i+1, length)
		}
	}

	return path, nil
}

// SelectSecurePath 选择一条所有节点均不重复的路径。
// 最多重试 10 次以避免节点池不足时的无限递归。
const maxSecurePathRetries = 10

func (ps *PathSelector) SelectSecurePath(length int) (Path, error) {
	for attempt := 0; attempt < maxSecurePathRetries; attempt++ {
		path, err := ps.SelectPath(length)
		if err != nil {
			return nil, err
		}

		// 验证所有跳是否唯一
		seen := make(map[string]bool)
		dup := false
		for _, h := range path {
			key := fmt.Sprintf("%x:%d", h.IPv6[:], h.Port)
			if seen[key] {
				dup = true
				break
			}
			seen[key] = true
		}
		if !dup {
			return path, nil
		}
	}
	return nil, fmt.Errorf("onion: could not select unique path after %d retries", maxSecurePathRetries)
}
