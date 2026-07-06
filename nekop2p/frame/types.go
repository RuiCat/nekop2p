// Package frame 提供传输帧协议。
// 帧是直连节点之间通信的基本单元。
// 每个帧使用 ChaCha20-Poly1305 及每个连接的会话密钥进行加密。
package frame

import "fmt"

// 通过线缆发送的帧类型。
const (
	FrameBeacon uint8 = 0x01 // 加密信标转发
	FrameData   uint8 = 0x02 // 端到端加密消息 (双棘轮后)
	FramePing   uint8 = 0x03 // 心跳保活
	FramePong   uint8 = 0x04 // 心跳应答
	FrameClose  uint8 = 0x05 // 优雅关闭
	FrameRoute  uint8 = 0x06 // 洋葱路由中继 (Phase 2)
	FrameSync   uint8 = 0x07 // 链状态同步
)

// 信标标志
const (
	BeaconFlagDontForward uint8 = 1 << 0
)

// 数据标志
const (
	DataFlagCompressed uint8 = 1 << 0
	DataFlagFragmented uint8 = 1 << 1
)

// 关闭标志
const (
	CloseFlagReconnect uint8 = 1 << 0
)

// 帧开销常量。
const (
	VersionLen  = 1  // 协议版本字节
	TypeLen     = 1  // 帧类型
	FlagsLen    = 1  // 各类型标志
	StreamIDLen = 2  // 流标识符

	InnerHeaderLen = VersionLen + TypeLen + FlagsLen + StreamIDLen // 5 字节

	LenLen    = 2  // 帧长度字段（大端序 uint16）
	NonceLen  = 8  // ChaCha20-Poly1305 nonce（64 位计数器）
	AuthTagLen = 16 // Poly1305 认证标签

	OuterOverhead = LenLen + NonceLen + AuthTagLen // 26 字节
	MaxFrameSize  = 65535                         // UDP/IPv6 最大安全值

	// 当前协议版本
	ProtocolVersion = 0x01
)

// Frame 是解码后的内层帧。
type Frame struct {
	Version  uint8
	Type     uint8
	Flags    uint8
	StreamID uint16
	Payload  []byte
}

// NewFrame 创建一个新的内层帧。
func NewFrame(typ uint8, flags uint8, payload []byte) *Frame {
	return &Frame{
		Version:  ProtocolVersion,
		Type:     typ,
		Flags:    flags,
		StreamID: 0, // TCP: 始终为 0; QUIC: 流 ID
		Payload:  payload,
	}
}

// ParseRawFrame 从原始字节解析 Frame（用于未加密的握手载荷）。
// 格式: Version(1) | Type(1) | Flags(1) | StreamID(2) | Payload(...)
func ParseRawFrame(data []byte) (*Frame, error) {
	if len(data) < InnerHeaderLen {
		return nil, fmt.Errorf("frame too short: %d < %d", len(data), InnerHeaderLen)
	}

	f := &Frame{
		Version:  data[0],
		Type:     data[1],
		Flags:    data[2],
		StreamID: uint16(data[3])<<8 | uint16(data[4]),
		Payload:  make([]byte, len(data)-InnerHeaderLen),
	}
	copy(f.Payload, data[InnerHeaderLen:])
	return f, nil
}
