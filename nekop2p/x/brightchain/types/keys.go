//go:build !cosmos

// Package types 定义明链模块的类型。
//
// 明链 = "阳光市场" — 公开、透明、假名化。
// 所有状态在链上可见。chain_id 是假名。
package types

// ModuleName 是明链模块的名称。
const ModuleName = "brightchain"

// 明链模块 KV 存储的存储键。
const (
	StoreKey = ModuleName

	// 状态存储的键前缀
	UserBlockKeyPrefix      = 0x01 // 用户区块：chain_id → UserBlock
	BondKeyPrefix           = 0x02 // 担保金：bond_id → GuaranteeBond
	PoolKeyPrefix           = 0x03 // 资金池状态
	BondByInviterPrefix     = 0x04 // 索引：邀请者 → bond_ids
	BondByInviteePrefix     = 0x05 // 索引：被邀请者 → bond_ids
	CreditTreeRootPrefix    = 0x06 // 信用票据 Merkle 树根
	NullifierSetPrefix      = 0x07 // 已花费信用票据的 nullifier
)

// RouterKey 是明链模块的消息路由。
const RouterKey = ModuleName
