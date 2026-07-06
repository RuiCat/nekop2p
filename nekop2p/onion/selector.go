package onion

// Selector 使用本地拓扑知识选择洋葱路径。
// 每个节点只知道自己的核心好友和已知的公开节点。
type Selector struct {
	coreFriends []Hop
	publicNodes []Hop
	allNodes    []Hop // 合并列表用于随机选择
}

// NewSelector 从本地拓扑创建路径选择器。
func NewSelector(coreFriends, publicNodes []Hop) *Selector {
	s := &Selector{
		coreFriends: coreFriends,
		publicNodes: publicNodes,
	}
	s.allNodes = make([]Hop, 0, len(coreFriends)+len(publicNodes))
	s.allNodes = append(s.allNodes, coreFriends...)
	s.allNodes = append(s.allNodes, publicNodes...)
	return s
}

// SelectPath 通过网络选择一条路径。
//
// 策略：
//   入口跳: 优先选核心好友（可信），回退到公开节点
//   中间跳: 从所有已知节点中随机选择（与相邻跳不同）
//   出口跳: 随机公开节点或核心好友
//
// 所有跳必须互不相同（没有节点在路径中出现两次）。
func (s *Selector) SelectPath(length int) (Path, error) {
	if length < MinHops || length > MaxHops {
		return nil, ErrInvalidLength
	}
	if len(s.allNodes) < length {
		return nil, ErrNotEnoughNodes
	}

	path := make(Path, length)
	used := make(map[string]bool) // 键 = chain_id 十六进制

	// 入口跳：优先选择核心好友
	entry := s.pickEntry()
	path[0] = entry
	markUsed(used, entry)

	// 中间跳 + 出口跳
	for i := 1; i < length; i++ {
		candidate, err := s.pickDistinct(path[i-1], used)
		if err != nil {
			return nil, err
		}
		path[i] = candidate
		markUsed(used, candidate)
	}

	return path, nil
}

// pickEntry 选择第一跳，优先选择核心好友。
func (s *Selector) pickEntry() Hop {
	if len(s.coreFriends) > 0 {
		return s.coreFriends[cryptoRandIntn(len(s.coreFriends))]
	}
	return s.publicNodes[cryptoRandIntn(len(s.publicNodes))]
}

// pickDistinct 选择一个与 prev 不同且不在 used 中的节点。
func (s *Selector) pickDistinct(prev Hop, used map[string]bool) (Hop, error) {
	// 最多尝试 100 次寻找不重复的节点
	for attempt := 0; attempt < 100; attempt++ {
		candidate := s.allNodes[cryptoRandIntn(len(s.allNodes))]
		key := nodeKey(candidate)
		if !used[key] {
			return candidate, nil
		}
	}
	return Hop{}, ErrNotEnoughNodes
}

// ErrInvalidLength 在路径长度超出范围时返回。
var ErrInvalidLength = errInvalidLength{}

// ErrNotEnoughNodes 在已知节点不足以构建路径时返回。
var ErrNotEnoughNodes = errNotEnoughNodes{}

type errInvalidLength struct{}
func (e errInvalidLength) Error() string { return "onion: invalid path length" }

type errNotEnoughNodes struct{}
func (e errNotEnoughNodes) Error() string { return "onion: not enough known nodes" }

func nodeKey(h Hop) string {
	return string(h.ChainID[:])
}

func markUsed(used map[string]bool, h Hop) {
	used[nodeKey(h)] = true
}
