//go:build !cosmos

// Package vrg VRG 状态持久化。
//
// 将虚拟根网图核心状态（创世树、纪元、外交通道、双花绑定）
// 持久化到 BoltDB 存储层。
package vrg

import (
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"

	"github.com/nekop2p/nekop2p/store"
)

// ============================================================
// VRG 存储键前缀
// ============================================================

var (
	vrgBucketGenesis   = []byte("vrg_genesis")    // 创世邀请链树
	vrgBucketEpoch     = []byte("vrg_epoch")      // 纪元计数器
	vrgBucketEdges     = []byte("vrg_edges")      // 外交通道
	vrgBucketNS        = []byte("vrg_namespaces") // 命名空间
	vrgBucketCollisions = []byte("vrg_collisions") // 双花冲突记录
)

// ============================================================
// VRGStore 持久化 VRG 状态
// ============================================================

// VRGStore 虚拟根网图持久化存储。
type VRGStore struct {
	chainStore *store.ChainStore
}

// NewVRGStore 创建 VRG 持久化存储。
// 初始化所需的 BoltDB bucket。
func NewVRGStore(cs *store.ChainStore) (*VRGStore, error) {
	vs := &VRGStore{chainStore: cs}

	// 创建所有 bucket
	err := cs.Write(func(tx *store.Tx) error {
		// 手动创建 bucket（通过直接操作底层 BoltDB）
		return nil // bucket 在 store.New() 中统一创建
	})
	if err != nil {
		return nil, fmt.Errorf("vrg: init buckets: %w", err)
	}

	return vs, nil
}

// ============================================================
// 创世树持久化
// ============================================================

// SaveGenesisTree 保存创世邀请链树。
func (vs *VRGStore) SaveGenesisTree(tree *GenesisTree) error {
	tree.mu.RLock()
	defer tree.mu.RUnlock()

	// JSON 不支持 [32]byte map key，转换为 hex 字符串
	stringNodes := make(map[string]*TreeNode, len(tree.nodes))
	for id, node := range tree.nodes {
		key := fmt.Sprintf("%x", id[:])
		stringNodes[key] = node
	}

	data, err := json.Marshal(stringNodes)
	if err != nil {
		return fmt.Errorf("vrg: marshal genesis tree: %w", err)
	}

	return vs.chainStore.Write(func(tx *store.Tx) error {
		return tx.PutUser("vrg_genesis_tree", data)
	})
}

// LoadGenesisTree 加载创世邀请链树。
func (vs *VRGStore) LoadGenesisTree() (*GenesisTree, error) {
	var stringNodes map[string]*TreeNode

	err := vs.chainStore.Read(func(tx *store.Tx) error {
		data := tx.GetUser("vrg_genesis_tree")
		if data == nil {
			return nil
		}
		return store.HybridUnmarshal(data, &stringNodes)
	})
	if err != nil {
		return nil, fmt.Errorf("vrg: load genesis tree: %w", err)
	}

	if stringNodes == nil {
		return nil, fmt.Errorf("vrg: no genesis tree found")
	}

	// 转换回 [32]byte key
	nodes := make(map[[32]byte]*TreeNode, len(stringNodes))
	for hexKey, node := range stringNodes {
		var id [32]byte
		decoded, err := hex.DecodeString(hexKey)
		if err != nil || len(decoded) != 32 {
			continue
		}
		copy(id[:], decoded)
		nodes[id] = node
	}

	tree := &GenesisTree{nodes: nodes}
	for id, node := range nodes {
		if node.Depth == 0 {
			tree.rootID = id
			break
		}
	}
	return tree, nil
}

// ============================================================
// 纪元持久化
// ============================================================

// SaveEpoch 持久化纪元计数器。
func (vs *VRGStore) SaveEpoch(ec *EpochCounter) error {
	ec.mu.RLock()
	data, err := json.Marshal(map[string]interface{}{
		"current_epoch":  ec.currentEpoch,
		"blocks_in_epoch": ec.blocksInEpoch,
		"isolation_count": ec.isolationCount,
	})
	ec.mu.RUnlock()

	if err != nil {
		return fmt.Errorf("vrg: marshal epoch: %w", err)
	}

	return vs.chainStore.Write(func(tx *store.Tx) error {
		return tx.PutUser("vrg_epoch_counter", data)
	})
}

// LoadEpoch 加载纪元计数器。
func (vs *VRGStore) LoadEpoch(blocksPerEpoch uint64) (*EpochCounter, error) {
	ec := NewEpochCounter(blocksPerEpoch)

	err := vs.chainStore.Read(func(tx *store.Tx) error {
		data := tx.GetUser("vrg_epoch_counter")
		if data == nil {
			return nil
		}
		var saved map[string]interface{}
		if err := store.HybridUnmarshal(data, &saved); err != nil {
			return err
		}
		if v, ok := saved["current_epoch"]; ok {
			ec.currentEpoch = uint64(v.(float64))
		}
		if v, ok := saved["blocks_in_epoch"]; ok {
			ec.blocksInEpoch = uint64(v.(float64))
		}
		if v, ok := saved["isolation_count"]; ok {
			ec.isolationCount = uint64(v.(float64))
		}
		return nil
	})

	return ec, err
}

// ============================================================
// 外交通道持久化
// ============================================================

// SaveTrustEdge 持久化外交通道。
func (vs *VRGStore) SaveTrustEdge(edge *TrustEdge) error {
	data, err := json.Marshal(edge)
	if err != nil {
		return fmt.Errorf("vrg: marshal edge: %w", err)
	}

	key := fmt.Sprintf("vrg_edge_%s", edge.EdgeID)
	return vs.chainStore.Write(func(tx *store.Tx) error {
		return tx.PutUser(key, data)
	})
}

// LoadAllTrustEdges 加载所有外交通道。
func (vs *VRGStore) LoadAllTrustEdges() ([]*TrustEdge, error) {
	var edges []*TrustEdge

	err := vs.chainStore.Read(func(tx *store.Tx) error {
		return tx.ForEachUser(func(key string, data []byte) error {
			if len(key) < 9 || key[:9] != "vrg_edge_" {
				return nil
			}
			var edge TrustEdge
			if err := store.HybridUnmarshal(data, &edge); err != nil {
				return nil // 跳过损坏数据
			}
			edges = append(edges, &edge)
			return nil
		})
	})

	return edges, err
}

// ============================================================
// 双花记录持久化
// ============================================================

// SaveCollision 持久化双花冲突事件。
func (vs *VRGStore) SaveCollision(event *CollisionEvent) error {
	data, err := json.Marshal(event)
	if err != nil {
		return fmt.Errorf("vrg: marshal collision: %w", err)
	}

	key := fmt.Sprintf("vrg_collision_%s_%d", event.AssetKey, event.Epoch)
	return vs.chainStore.Write(func(tx *store.Tx) error {
		return tx.PutUser(key, data)
	})
}

// LoadAllCollisions 加载所有双花冲突。
func (vs *VRGStore) LoadAllCollisions() ([]*CollisionEvent, error) {
	var events []*CollisionEvent

	err := vs.chainStore.Read(func(tx *store.Tx) error {
		return tx.ForEachUser(func(key string, data []byte) error {
			if len(key) < 14 || key[:14] != "vrg_collision_" {
				return nil
			}
			var event CollisionEvent
			if err := store.HybridUnmarshal(data, &event); err != nil {
				return nil
			}
			events = append(events, &event)
			return nil
		})
	})

	return events, err
}

// ============================================================
// 辅助
// ============================================================

func uint64ToBytesVRG(v uint64) []byte {
	b := make([]byte, 8)
	binary.BigEndian.PutUint64(b, v)
	return b
}
