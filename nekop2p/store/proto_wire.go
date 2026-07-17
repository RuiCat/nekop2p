//go:build !cosmos

// Package store 二进制序列化支持。
//
// 提供紧凑的二进制编解码，用于替换 JSON 序列化。
// 支持两种格式：
//   - gob: Go 标准库二进制编码（紧凑、快速、无需额外依赖）
//   - json: 现有 JSON 编码（向后兼容）
//
// 混合格式: [1B 标记(0x01=gob) + payload]
// 旧数据以 { 或 [ 开头 === JSON 格式
package store

import (
	"bytes"
	"encoding/gob"
	"fmt"
)

func init() {
	// 注册 gob 已知类型
	gob.Register(map[string]interface{}{})
	gob.Register([]interface{}{})
}

// GobEncode 使用 gob 二进制编码序列化值。
// 比 JSON 紧凑 30-60%，编解码速度更快。
func GobEncode(v interface{}) ([]byte, error) {
	var buf bytes.Buffer
	enc := gob.NewEncoder(&buf)
	if err := enc.Encode(v); err != nil {
		return nil, fmt.Errorf("gob encode: %w", err)
	}
	return buf.Bytes(), nil
}

// GobDecode 从 gob 二进制编码反序列化值。
func GobDecode(data []byte, v interface{}) error {
	buf := bytes.NewReader(data)
	dec := gob.NewDecoder(buf)
	if err := dec.Decode(v); err != nil {
		return fmt.Errorf("gob decode: %w", err)
	}
	return nil
}

// HybridMarshal 混合序列化：使用 gob 二进制格式。
// 数据以 [1B 格式标记 + payload] 格式存储：
//
//	0x01 = gob 二进制
//	原始 JSON（以 { 或 [ 开头）= 向后兼容旧数据
func HybridMarshal(v interface{}) ([]byte, error) {
	data, err := GobEncode(v)
	if err != nil {
		// 回退到 JSON
		return Marshal(v)
	}
	result := make([]byte, 1+len(data))
	result[0] = 0x01 // gob format marker
	copy(result[1:], data)
	return result, nil
}

// HybridUnmarshal 混合反序列化：自动检测格式并解码。
func HybridUnmarshal(data []byte, v interface{}) error {
	if len(data) == 0 {
		return fmt.Errorf("empty data")
	}
	switch {
	case data[0] == 0x01 && len(data) > 1:
		// Gob 二进制格式
		return GobDecode(data[1:], v)
	case data[0] == '{' || data[0] == '[':
		// JSON 格式（向后兼容）
		return Unmarshal(data, v)
	default:
		// 未知格式标记，报错而非静默回退（防止数据损坏传播）
		return fmt.Errorf("unknown data format (marker=0x%02x, len=%d)", data[0], len(data))
	}
}

// ============================================================
// Store Tx 方法：二进制版本
// ============================================================

// PutUserBinary 以二进制格式存储用户数据。
func (tx *Tx) PutUserBinary(key string, v interface{}) error {
	data, err := HybridMarshal(v)
	if err != nil {
		return err
	}
	return tx.PutUser(key, data)
}

// GetUserBinary 读取二进制格式的用户数据。
func (tx *Tx) GetUserBinary(key string, v interface{}) bool {
	data := tx.GetUser(key)
	if data == nil {
		return false
	}
	return HybridUnmarshal(data, v) == nil
}

// PutBondBinary 以二进制格式存储担保债券。
func (tx *Tx) PutBondBinary(key string, v interface{}) error {
	data, err := HybridMarshal(v)
	if err != nil {
		return err
	}
	return tx.PutBond(key, data)
}

// GetBondBinary 读取二进制格式的担保债券。
func (tx *Tx) GetBondBinary(key string, v interface{}) bool {
	data := tx.GetBond(key)
	if data == nil {
		return false
	}
	return HybridUnmarshal(data, v) == nil
}

// PutLoanBinary 以二进制格式存储贷款记录。
func (tx *Tx) PutLoanBinary(key string, v interface{}) error {
	data, err := HybridMarshal(v)
	if err != nil {
		return err
	}
	return tx.PutLoan(key, data)
}

// GetLoanBinary 读取二进制格式的贷款记录。
func (tx *Tx) GetLoanBinary(key string, v interface{}) bool {
	data := tx.GetLoan(key)
	if data == nil {
		return false
	}
	return HybridUnmarshal(data, v) == nil
}
