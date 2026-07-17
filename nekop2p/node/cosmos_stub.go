//go:build cosmos

// Package node 节点编排器（Cosmos SDK 适配层）。
//
// P2P 节点编排逻辑在非 Cosmos 模式中实现。
// Cosmos SDK 模式下由 CometBFT + BaseApp 替代节点状态机。
// 完整版本将在 Phase 7 实现。
package node
