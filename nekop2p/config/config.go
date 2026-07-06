// Package config 管理节点配置持久化。
//
// 节点配置 (~/.nekop2p/config.json) 包含:
//   - 节点身份密钥（明域）
//   - 引导节点
//   - 缓存的好友列表
//   - 目标连接数
//
// 配置不包含:
//   - 暗域密钥（由用户单独存储）
//   - 链数据（由 Tendermint/Cosmos SDK 管理）
//   - 连接状态（临时性的）
package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// NodeConfig 保存持久化节点配置。
type NodeConfig struct {
	// 身份
	RecvSK  [32]byte `json:"recv_sk"`
	RecvPK  [32]byte `json:"recv_pk"`
	SendSK  [64]byte `json:"send_sk"`
	SendPK  [32]byte `json:"send_pk"`
	ChainID [32]byte `json:"chain_id"`

	// 网络
	ListenAddr        string     `json:"listen_addr"`
	APIAddr           string     `json:"api_addr"`
	BootstrapIPs      [][16]byte `json:"bootstrap_ips"`
	TargetConnections int        `json:"target_connections"`

	// 好友缓存（用于快速重连；实际列表在链上）
	CachedFriends []CachedFriend `json:"cached_friends"`
}

// CachedFriend 是一个好友的最后已知网络信息。
type CachedFriend struct {
	ChainID [32]byte `json:"chain_id"`
	RecvPK  [32]byte `json:"recv_pk"`
	SendPK  [32]byte `json:"send_pk"`
	IPv6    [16]byte `json:"ipv6"`
	Port    uint16   `json:"port"`
}

// DefaultPath 返回默认配置文件路径。
func DefaultPath() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".nekop2p", "config.json")
}

// Load 从文件读取配置。
func Load(path string) (*NodeConfig, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("config not found: %s (run init first)", path)
		}
		return nil, fmt.Errorf("read config: %w", err)
	}

	cfg := &NodeConfig{}
	if err := json.Unmarshal(data, cfg); err != nil {
		return nil, fmt.Errorf("parse config: %w", err)
	}

	if cfg.TargetConnections == 0 {
		cfg.TargetConnections = 20
	}
	if cfg.ListenAddr == "" {
		cfg.ListenAddr = "[::]:9070"
	}
	if cfg.APIAddr == "" {
		cfg.APIAddr = "127.0.0.1:9071"
	}

	return cfg, nil
}

// Save 将配置写入文件。
func Save(path string, cfg *NodeConfig) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return fmt.Errorf("create config dir: %w", err)
	}

	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal config: %w", err)
	}

	return os.WriteFile(path, data, 0600)
}

// Default 返回默认配置。
func Default() *NodeConfig {
	return &NodeConfig{
		ListenAddr:        "[::]:9070",
		APIAddr:           "127.0.0.1:9071",
		TargetConnections: 20,
	}
}
