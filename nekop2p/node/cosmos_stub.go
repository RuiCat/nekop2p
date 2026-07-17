//go:build cosmos

// Package node 节点编排器（Cosmos SDK 适配层, Phase 7）。
//
// P2P 节点编排逻辑在非 Cosmos 模式中实现。
// Cosmos SDK 模式下由 CometBFT + BaseApp 替代节点状态机。
//
// Package node 提供 Cosmos 模式节点类型桩。
package node

import "fmt"

// NodeRole 节点角色。
type NodeRole int

const (
	RoleNone   NodeRole = 0
	RolePublic NodeRole = 1
	RoleRelay  NodeRole = 2
	RoleRecord NodeRole = 3
)

// NodeConfig Cosmos 模式节点配置。
type NodeConfig struct {
	ChainID    string
	ListenAddr string
	RPCAddr    string
}

// FormatNodeID 格式化节点 ID 为可读字符串。
func FormatNodeID(id string) string {
	if len(id) >= 16 {
		return fmt.Sprintf("%x", []byte(id[:16]))
	}
	return id
}

// OnMessageDelivered 消息送达回调类型。
type OnMessageDelivered func(msgID string)

// OnBeaconReceived 信标接收回调类型。
type OnBeaconReceived func(beaconData []byte)

// OnChainSync 链同步回调类型。
type OnChainSync func(height int64)
