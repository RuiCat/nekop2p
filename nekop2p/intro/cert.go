// Package intro 实现好友介绍协议。
//
// Alice（共同好友）可以通过共享 Charlie 的公钥
// 和签名的介绍证书，将 Bob 介绍给 Charlie。
package intro

import (
	"crypto/ed25519"
	"encoding/binary"
	"fmt"
	"time"

	"github.com/nekop2p/nekop2p/crypto"
)

// Cert 是由介绍人签名的介绍证书。
type Cert struct {
	Introducer [32]byte // Alice 的 chain_id
	SubjectA   [32]byte // Bob 的 chain_id
	SubjectB   [32]byte // Charlie 的 chain_id
	Timestamp  int64
	Expiry     int64 // 时间戳 + 30 天
	Signature  []byte // Ed25519(alice.send_sk, 以上全部)
}

const (
	CertSize    = 32 + 32 + 32 + 8 + 8 + 64 // 176 字节
	DefaultTTL  = 30 * 24 * time.Hour
)

// Issue 创建新的介绍证书。
// Alice（介绍人）签署证书将 Bob 介绍给 Charlie。
func Issue(introducerSK [64]byte, introducer, subjectA, subjectB [32]byte) *Cert {
	now := time.Now().Unix()
	c := &Cert{
		Introducer: introducer,
		SubjectA:   subjectA,
		SubjectB:   subjectB,
		Timestamp:  now,
		Expiry:     now + int64(DefaultTTL.Seconds()),
	}
	c.Signature = crypto.Sign(&introducerSK, c.signedData())
	return c
}

// Verify 检查证书的签名和有效期。
func (c *Cert) Verify(introducerPK [32]byte) error {
	// 检查签名
	if !ed25519.Verify(introducerPK[:], c.signedData(), c.Signature) {
		return fmt.Errorf("introduction cert: invalid signature")
	}
	// 检查有效期
	if time.Now().Unix() > c.Expiry {
		return fmt.Errorf("introduction cert: expired at %d", c.Expiry)
	}
	return nil
}

// Serialize 将证书编码为字节。
func (c *Cert) Serialize() []byte {
	buf := make([]byte, CertSize)
	copy(buf[0:32], c.Introducer[:])
	copy(buf[32:64], c.SubjectA[:])
	copy(buf[64:96], c.SubjectB[:])
	binary.BigEndian.PutUint64(buf[96:104], uint64(c.Timestamp))
	binary.BigEndian.PutUint64(buf[104:112], uint64(c.Expiry))
	copy(buf[112:176], c.Signature)
	return buf
}

// ParseCert 从字节反序列化证书。
func ParseCert(data []byte) (*Cert, error) {
	if len(data) < CertSize {
		return nil, fmt.Errorf("cert too short: %d", len(data))
	}
	c := &Cert{}
	copy(c.Introducer[:], data[0:32])
	copy(c.SubjectA[:], data[32:64])
	copy(c.SubjectB[:], data[64:96])
	c.Timestamp = int64(binary.BigEndian.Uint64(data[96:104]))
	c.Expiry = int64(binary.BigEndian.Uint64(data[104:112]))
	c.Signature = make([]byte, 64)
	copy(c.Signature, data[112:176])
	return c, nil
}

func (c *Cert) signedData() []byte {
	buf := make([]byte, 32+32+32+8+8)
	copy(buf[0:32], c.Introducer[:])
	copy(buf[32:64], c.SubjectA[:])
	copy(buf[64:96], c.SubjectB[:])
	binary.BigEndian.PutUint64(buf[96:104], uint64(c.Timestamp))
	binary.BigEndian.PutUint64(buf[104:112], uint64(c.Expiry))
	return buf
}
