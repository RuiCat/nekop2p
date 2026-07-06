package node

import (
	"sync"
	"time"

	"github.com/nekop2p/nekop2p/peer"
)

// CoreFriend 是核心路由表中的好友（信任距离 = 1）。
type CoreFriend struct {
	ChainID      peer.ChainID
	RecvPK       [32]byte
	SendPK       [32]byte
	IPv6         [16]byte
	Port         uint16
	LastSeen     time.Time
	Online       bool
	IntroducedBy peer.ChainID // 介绍人的 chain_id（零值 = 直接好友）
}

// Neighbor 是邻居表中的节点（信任距离 ≤ 3）。
type Neighbor struct {
	ChainID   peer.ChainID
	TrustDist uint8
	IPv6      [16]byte
	Port      uint16
	LastSeen  time.Time
	Via       peer.ChainID // 哪个核心好友报告了此邻居
}

// Topology 维护三环路由表。
type Topology struct {
	mu        sync.RWMutex
	core      map[peer.ChainID]*CoreFriend
	neighbors map[peer.ChainID]*Neighbor
}

// NewTopology 创建一个新的拓扑。
func NewTopology() *Topology {
	return &Topology{
		core:      make(map[peer.ChainID]*CoreFriend),
		neighbors: make(map[peer.ChainID]*Neighbor),
	}
}

// AddCoreFriend 添加或更新一个核心好友。
func (t *Topology) AddCoreFriend(chainID peer.ChainID, recvPK, sendPK [32]byte) *CoreFriend {
	t.mu.Lock()
	defer t.mu.Unlock()

	cf := &CoreFriend{
		ChainID: chainID,
		RecvPK:  recvPK,
		SendPK:  sendPK,
	}
	t.core[chainID] = cf
	return cf
}

// GetCoreFriend 通过链 ID 返回指定的核心好友。
func (t *Topology) GetCoreFriend(chainID peer.ChainID) *CoreFriend {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.core[chainID]
}

// GetCoreFriends 返回所有核心好友。
func (t *Topology) GetCoreFriends() []*CoreFriend {
	t.mu.RLock()
	defer t.mu.RUnlock()
	result := make([]*CoreFriend, 0, len(t.core))
	for _, cf := range t.core {
		result = append(result, cf)
	}
	return result
}

// MarkOnline 将核心好友标记为在线。
func (t *Topology) MarkOnline(chainID peer.ChainID) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if cf, ok := t.core[chainID]; ok {
		cf.Online = true
		cf.LastSeen = time.Now()
	}
}

// MarkOffline 将核心好友标记为离线。
func (t *Topology) MarkOffline(chainID peer.ChainID) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if cf, ok := t.core[chainID]; ok {
		cf.Online = false
	}
}

// UpdateNeighbor 根据心跳更新邻居表。
func (t *Topology) UpdateNeighbor(neighborID peer.ChainID, trustDist uint8, ipv6 [16]byte, port uint16, via peer.ChainID) {
	t.mu.Lock()
	defer t.mu.Unlock()

	n := &Neighbor{
		ChainID:   neighborID,
		TrustDist: trustDist,
		IPv6:      ipv6,
		Port:      port,
		LastSeen:  time.Now(),
		Via:       via,
	}
	t.neighbors[neighborID] = n
}

// GetNextHop 查找前往目标节点的下一跳。
func (t *Topology) GetNextHop(target peer.ChainID) *CoreFriend {
	t.mu.RLock()
	defer t.mu.RUnlock()

	// 1. 直接的核心好友？
	if cf, ok := t.core[target]; ok && cf.Online {
		return cf
	}

	// 2. 通过邻居的核心好友可达？
	if n, ok := t.neighbors[target]; ok {
		if cf, ok := t.core[n.Via]; ok && cf.Online {
			return cf
		}
	}

	return nil // 不可达
}

// PruneNeighbors 移除过期的邻居条目。
func (t *Topology) PruneNeighbors(maxAge time.Duration) {
	t.mu.Lock()
	defer t.mu.Unlock()
	deadline := time.Now().Add(-maxAge)
	for id, n := range t.neighbors {
		if n.LastSeen.Before(deadline) {
			delete(t.neighbors, id)
		}
	}
}

// IsFriend 检查某个链 ID 是否为核心好友。
func (t *Topology) IsFriend(chainID peer.ChainID) bool {
	t.mu.RLock()
	defer t.mu.RUnlock()
	_, ok := t.core[chainID]
	return ok
}

// GetAnyNeighborIPv6 返回任意一个邻居的 IPv6（用于填充连接），持有读锁。
func (t *Topology) GetAnyNeighborIPv6() ([16]byte, bool) {
	t.mu.RLock()
	defer t.mu.RUnlock()
	for _, nb := range t.neighbors {
		return nb.IPv6, true
	}
	return [16]byte{}, false
}
