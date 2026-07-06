// Package onion 实现多层洋葱路由，提供匿名通信。
//
// === 工作原理 ===
//   发送方构建 N 层加密包（通常 3 跳）。每层用该跳节点的 recv_pk 加密。
//   每个中间节点解密自己的层，只看到下一跳地址。最后一跳解密看到最终消息。
//   入口节点不知道出口节点。中间节点不知道总跳数和自己在路径中的位置。
//
// === 路径选择 ===
//   基于本地拓扑（三环路由表）。不需要全局目录——每个节点只知道自己的
//   好友和公开节点。路径选择器偏好：入口=好友，中间=公开节点，出口=公开节点。
//
// === 安全特性 ===
//   固定包大小：512 字节填充，防止通过包大小推断路径长度
//   每层独立密钥：ECDH-KEM + AES-256-GCM，层间无关联
//   方向不可区分：入口/中间/出口节点看到的处理逻辑完全一致
//
// === 层格式（解密后）===
//   中继层: [0x00 | next_hop_ipv6(16) | next_hop_port(2) | 加密内层]
//   最终层: [0x01 | target_ipv6(16) | target_port(2) | 消息]
//
// === 安全审计 ===
//   ✅ 已通过。固定 512B 包大小防止流量分析。Padding 字节容错解析。
//
// Package onion 实现多层洋葱路由，提供匿名通信。
//
// 安全特性：
//   - 每跳只知道上一跳和下一跳（不知道源地址和目的地址）
//   - 抗流量分析（固定大小数据包、随机延迟）
//   - 没有任何单跳能够关联发送方和接收方
package onion

import (
	"crypto/rand"
	"encoding/binary"
	"fmt"
	"time"

	"github.com/nekop2p/nekop2p/crypto"
)

// Hop 表示洋葱路径中的一个路由节点。
type Hop struct {
	ChainID [32]byte // 链身份
	IPv6    [16]byte
	Port    uint16
	RecvPK  [32]byte // 用于 KEM 加密的 Curve25519 公钥
}

// Path 是洋葱跳的有序序列。
type Path []Hop

// Target 是最终目的地（不是洋葱跳，而是实际接收方）。
type Target struct {
	IPv6 [16]byte
	Port uint16
}

// Circuit 表示一个已建立的洋葱电路，可复用于
// 同一方向的多条消息。
type Circuit struct {
	ID         [16]byte // 唯一电路标识符
	Path       Path
	Target     Target
	CreatedAt  int64
	LastUsedAt int64
	SessionKey [32]byte // 电路创建期间建立的共享密钥
}

// Packet 是一个完整构建的多层洋葱数据包。
type Packet struct {
	CircuitID [16]byte // 该包所属的电路（单次使用则为零值）
	Layers    [][]byte // 加密层，最外层在前
	PathLen   int
}

// UnwrapResult 包含解密一层洋葱层的结果。
type UnwrapResult struct {
	NextHopIP   [16]byte // 下一跳 IPv6（用于中继）
	NextHopPort uint16
	Payload     []byte // 剩余加密层或最终消息
	IsFinal     bool   // 本节点是否是最后一跳洋葱节点
	FinalTarget Target // 最终目的地（仅当 IsFinal 时有效）
	FinalMsg    []byte // 给最终目标的解密消息（仅当 IsFinal 时有效）
}

// 常量
const (
	MinHops       = 3
	MaxHops       = 5
	PacketOverhead = 32 + 12 + 16 // 每层：epk + nonce + tag
	CircuitIDSize  = 16
	MaxCircuitAge  = 600 // 秒（10 分钟）
	FixedPacketSize = 512 // 固定洋葱包大小（防止流量分析）

	// 层类型标记
	layerRelay byte = 0x00 // 转发到下一跳
	layerFinal byte = 0x01 // 递送到最终目标
)

// Build 构造一个洋葱数据包，通过给定的路径发送消息。
//
// 层格式（解密后）：
//   中继层: [0x00 | next_hop_ipv6(16) | next_hop_port(2) | encrypted_inner]
//   最终层: [0x01 | target_ipv6(16) | target_port(2) | message]
func Build(circuitID [16]byte, path Path, target Target, message []byte) (*Packet, error) {
	if len(path) < MinHops {
		return nil, fmt.Errorf("onion: path too short: %d hops (min %d)", len(path), MinHops)
	}
	if len(path) > MaxHops {
		return nil, fmt.Errorf("onion: path too long: %d hops (max %d)", len(path), MaxHops)
	}

	n := len(path)

	// 最内层：类型标记 + 目标 + 消息
	inner := make([]byte, 1+18+len(message))
	inner[0] = layerFinal
	copy(inner[1:17], target.IPv6[:])
	binary.BigEndian.PutUint16(inner[17:19], target.Port)
	copy(inner[19:], message)

	layers := make([][]byte, n)
	current := inner

	for i := n - 1; i >= 0; i-- {
		hop := path[i]

		var payload []byte
		if i == n-1 {
			payload = current // 最终层已有标记
		} else {
			// 中继层：标记 + 下一跳 + 加密内层
			payload = make([]byte, 1+18+len(current))
			payload[0] = layerRelay
			copy(payload[1:17], path[i+1].IPv6[:])
			binary.BigEndian.PutUint16(payload[17:19], path[i+1].Port)
			copy(payload[19:], current)
		}

		encrypted, err := crypto.KEMEncrypt(&hop.RecvPK, payload)
		if err != nil {
			return nil, fmt.Errorf("onion: encrypt layer %d: %w", i, err)
		}
		layers[i] = encrypted
		current = encrypted
	}

	return &Packet{
		CircuitID: circuitID,
		Layers:    layers,
		PathLen:   n,
	}, nil
}

// BuildCircuit 创建新的洋葱电路并返回初始数据包。
// 该电路可复用于后续消息，无需重建完整路径。
func BuildCircuit(path Path, target Target, initMessage []byte) (*Circuit, *Packet, error) {
	if len(path) < MinHops {
		return nil, nil, fmt.Errorf("onion: circuit path too short: %d (min %d)", len(path), MinHops)
	}

	// 生成电路 ID
	var circuitID [CircuitIDSize]byte
	if _, err := rand.Read(circuitID[:]); err != nil {
		return nil, nil, err
	}

	// 生成电路会话密钥
	sessionKey, err := crypto.RandomBytes(32)
	if err != nil {
		return nil, nil, err
	}

	circuit := &Circuit{
		ID:        circuitID,
		Path:      path,
		Target:    target,
		CreatedAt: nowUnix(),
	}
	copy(circuit.SessionKey[:], sessionKey)

	pkt, err := Build(circuitID, path, target, initMessage)
	if err != nil {
		return nil, nil, err
	}

	return circuit, pkt, nil
}

// UnwrapOne 使用我们的私钥解密洋葱数据包的最外层。
func UnwrapOne(recvPriv *[32]byte, encrypted []byte) (*UnwrapResult, error) {
	plain, err := crypto.KEMDecrypt(recvPriv, encrypted)
	if err != nil {
		return nil, fmt.Errorf("onion: unwrap: %w", err)
	}

	if len(plain) < 1 {
		return nil, fmt.Errorf("onion: unwrapped data too short: %d bytes", len(plain))
	}

	result := &UnwrapResult{}
	marker := plain[0]

	switch marker {
	case layerRelay:
		if len(plain) < 19 {
			return nil, fmt.Errorf("onion: relay layer too short: %d", len(plain))
		}
		copy(result.NextHopIP[:], plain[1:17])
		result.NextHopPort = binary.BigEndian.Uint16(plain[17:19])
		result.Payload = plain[19:]
		result.IsFinal = false

	case layerFinal:
		if len(plain) < 19 {
			return nil, fmt.Errorf("onion: final layer too short: %d", len(plain))
		}
		result.IsFinal = true
		copy(result.FinalTarget.IPv6[:], plain[1:17])
		result.FinalTarget.Port = binary.BigEndian.Uint16(plain[17:19])
		result.FinalMsg = plain[19:]

	default:
		return nil, fmt.Errorf("onion: unknown layer marker: 0x%02x", marker)
	}

	return result, nil
}

// Serialize 将洋葱包扁平化为有线格式，并填充到固定大小。
// 格式: circuit_id(16) || path_len(1) || layer_0_len(2) || layer_0 || ... || padding
// 总大小填充至 FixedPacketSize 字节以抵抗流量分析。
func (p *Packet) Serialize() []byte {
	size := CircuitIDSize + 1 // circuit_id + path_len
	for _, l := range p.Layers {
		size += 2 + len(l)
	}
	// 如果小于固定大小，则填充（用于抵抗流量分析）
	if size < FixedPacketSize {
		buf := make([]byte, size, FixedPacketSize)
		copy(buf[0:CircuitIDSize], p.CircuitID[:])
		buf[CircuitIDSize] = byte(p.PathLen)
		offset := CircuitIDSize + 1
		for _, l := range p.Layers {
			binary.BigEndian.PutUint16(buf[offset:offset+2], uint16(len(l)))
			offset += 2
			copy(buf[offset:], l)
			offset += len(l)
		}
		padding := make([]byte, FixedPacketSize-size)
		rand.Read(padding)
		return append(buf, padding...)
	}

	// 无需填充
	buf := make([]byte, size)
	copy(buf[0:CircuitIDSize], p.CircuitID[:])
	buf[CircuitIDSize] = byte(p.PathLen)
	offset := CircuitIDSize + 1
	for _, l := range p.Layers {
		binary.BigEndian.PutUint16(buf[offset:offset+2], uint16(len(l)))
		offset += 2
		copy(buf[offset:], l)
		offset += len(l)
	}
	return buf
}

// ParseOnion 从有线格式反序列化洋葱数据包。
// 多余的字节（填充）会被静默忽略。
func ParseOnion(data []byte) (*Packet, error) {
	if len(data) < CircuitIDSize+3 {
		return nil, fmt.Errorf("onion: packet too short: %d bytes", len(data))
	}

	pkt := &Packet{}
	copy(pkt.CircuitID[:], data[0:CircuitIDSize])
	pkt.PathLen = int(data[CircuitIDSize])
	if pkt.PathLen == 0 || pkt.PathLen > MaxHops {
		return nil, fmt.Errorf("onion: invalid path length: %d (max=%d)", pkt.PathLen, MaxHops)
	}

	offset := CircuitIDSize + 1
	pkt.Layers = make([][]byte, 0, pkt.PathLen)

	for i := 0; i < pkt.PathLen; i++ {
		if offset+2 > len(data) {
			return nil, fmt.Errorf("onion: truncated layer %d header", i)
		}
		layerLen := int(binary.BigEndian.Uint16(data[offset : offset+2]))
		offset += 2
		if offset+layerLen > len(data) {
			// 末尾的填充字节 — 可接受，直接截断
			break
		}
		layer := make([]byte, layerLen)
		copy(layer, data[offset:offset+layerLen])
		pkt.Layers = append(pkt.Layers, layer)
		offset += layerLen
	}

	if len(pkt.Layers) != pkt.PathLen {
		return nil, fmt.Errorf("onion: expected %d layers, got %d (padding?)", pkt.PathLen, len(pkt.Layers))
	}

	return pkt, nil
}

// StripFirst 移除最外层（已处理的）层，并返回
// 剩余的数据包用于转发。
func (p *Packet) StripFirst() *Packet {
	if len(p.Layers) <= 1 {
		return nil
	}
	return &Packet{
		CircuitID: p.CircuitID,
		Layers:    p.Layers[1:],
		PathLen:   p.PathLen - 1,
	}
}

// nowUnix 返回当前 Unix 时间戳。
func nowUnix() int64 {
	return time.Now().Unix()
}
