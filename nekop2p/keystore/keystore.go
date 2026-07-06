// Package keystore 管理明域和暗域的两个独立密钥文件。
//
//	bright_keys.json — recv_sk + send_sk → chain_id → 明域身份
//	dark_keys.json   — master_secret → 匿名化名 → 暗域身份
//
// 两个文件在密码学上是相互独立的。
// 拥有 bright_keys 并不能追踪暗域活动。
package keystore

import (
	"crypto/rand"
	"encoding/json"
	"fmt"
	"io"
	"os"

	"github.com/nekop2p/nekop2p/crypto"
)

// BrightKeys 保存明域身份密钥。
// 存储在 bright_keys.json 中 — 由用户保管。
type BrightKeys struct {
	RecvSK  [32]byte `json:"recv_sk"`  // Curve25519 私钥
	RecvPK  [32]byte `json:"recv_pk"`  // Curve25519 公钥
	SendSK  [64]byte `json:"send_sk"`  // Ed25519 私钥
	SendPK  [32]byte `json:"send_pk"`  // Ed25519 公钥
	ChainID [32]byte `json:"chain_id"` // SHA256(发送公钥)
}

// DarkKeys 保存暗域主密钥。
// 存储在 dark_keys.json 中 — 由用户保管。
// 绝不存储在链上。绝不可从 bright_keys 派生。
type DarkKeys struct {
	MasterSecret [32]byte `json:"master_secret"` // 32字节随机数
}

// GenerateBright 创建一个新的明域密钥对。
func GenerateBright() (*BrightKeys, error) {
	dual, err := crypto.GenerateDualKeys()
	if err != nil {
		return nil, err
	}

	bk := &BrightKeys{
		RecvSK: dual.RecvKey.Private,
		RecvPK: dual.RecvKey.Public,
		SendSK: dual.SendKey.Private,
		SendPK: dual.SendKey.Public,
	}
	bk.ChainID = crypto.DeriveChainID(bk.SendPK)
	return bk, nil
}

// GenerateDark 创建一个新的暗域主密钥。
func GenerateDark() (*DarkKeys, error) {
	dk := &DarkKeys{}
	if _, err := io.ReadFull(rand.Reader, dk.MasterSecret[:]); err != nil {
		return nil, fmt.Errorf("generate dark keys: %w", err)
	}
	return dk, nil
}

// SaveBright 将明域密钥写入文件。
func SaveBright(path string, bk *BrightKeys) error {
	data, err := json.MarshalIndent(bk, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0600)
}

// LoadBright 从文件读取明域密钥。
func LoadBright(path string) (*BrightKeys, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	bk := &BrightKeys{}
	if err := json.Unmarshal(data, bk); err != nil {
		return nil, err
	}
	return bk, nil
}

// SaveDark 将暗域密钥写入文件。
func SaveDark(path string, dk *DarkKeys) error {
	data, err := json.MarshalIndent(dk, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0600)
}

// LoadDark 从文件读取暗域密钥。
func LoadDark(path string) (*DarkKeys, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	dk := &DarkKeys{}
	if err := json.Unmarshal(data, dk); err != nil {
		return nil, err
	}
	return dk, nil
}
