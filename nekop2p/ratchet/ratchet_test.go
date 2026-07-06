package ratchet_test

import (
	"bytes"
	"testing"

	"github.com/nekop2p/nekop2p/crypto"
	"github.com/nekop2p/nekop2p/ratchet"
)

func TestRatchetRoundtrip(t *testing.T) {
	// 为双方生成身份密钥
	aliceID, _ := crypto.GenerateEphemeralKey()
	bobID, _ := crypto.GenerateEphemeralKey()
	aliceEph, _ := crypto.GenerateEphemeralKey()

	// Bob 发起
	bobRatchet, bobEphPK, err := ratchet.InitAsInitiator(
		&bobID.Private, &aliceID.Public, &aliceEph.Public,
	)
	if err != nil {
		t.Fatal(err)
	}

	// Alice 响应
	aliceRatchet, err := ratchet.InitAsResponder(
		&aliceID.Private, &bobID.Public, &aliceEph.Private, &bobEphPK,
	)
	if err != nil {
		t.Fatal(err)
	}

	// Bob → Alice
	msg1 := []byte("hello alice from bob")
	wire1, err := bobRatchet.Encrypt(msg1)
	if err != nil {
		t.Fatal(err)
	}
	dec1, err := aliceRatchet.Decrypt(wire1)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(msg1, dec1) {
		t.Errorf("roundtrip 1: got %q, want %q", dec1, msg1)
	}

	// Alice → Bob
	msg2 := []byte("hello bob from alice")
	wire2, err := aliceRatchet.Encrypt(msg2)
	if err != nil {
		t.Fatal(err)
	}
	dec2, err := bobRatchet.Decrypt(wire2)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(msg2, dec2) {
		t.Errorf("roundtrip 2: got %q, want %q", dec2, msg2)
	}
}

func TestRatchetMultipleMessages(t *testing.T) {
	aliceID, _ := crypto.GenerateEphemeralKey()
	bobID, _ := crypto.GenerateEphemeralKey()
	aliceEph, _ := crypto.GenerateEphemeralKey()

	bobR, bobPK, _ := ratchet.InitAsInitiator(&bobID.Private, &aliceID.Public, &aliceEph.Public)
	aliceR, err := ratchet.InitAsResponder(&aliceID.Private, &bobID.Public, &aliceEph.Private, &bobPK)
	if err != nil {
		t.Fatal(err)
	}

	messages := []string{
		"msg1", "msg2", "msg3", "msg4", "msg5",
	}

	for i, msg := range messages {
		wire, err := bobR.Encrypt([]byte(msg))
		if err != nil {
			t.Fatalf("encrypt %d: %v", i, err)
		}
		dec, err := aliceR.Decrypt(wire)
		if err != nil {
			t.Fatalf("decrypt %d: %v", i, err)
		}
		if string(dec) != msg {
			t.Errorf("msg %d: got %q, want %q", i, dec, msg)
		}
	}
}

func TestRatchetBidirectional(t *testing.T) {
	aliceID, _ := crypto.GenerateEphemeralKey()
	bobID, _ := crypto.GenerateEphemeralKey()
	aliceEph, _ := crypto.GenerateEphemeralKey()

	bobR, bobPK, _ := ratchet.InitAsInitiator(&bobID.Private, &aliceID.Public, &aliceEph.Public)
	aliceR, err := ratchet.InitAsResponder(&aliceID.Private, &bobID.Public, &aliceEph.Private, &bobPK)
	if err != nil {
		t.Fatal(err)
	}

	// 交错消息
	// Bob 发送 3 条
	for i := 0; i < 3; i++ {
		wire, _ := bobR.Encrypt([]byte{byte('B'), byte(i)})
		dec, err := aliceR.Decrypt(wire)
		if err != nil {
			t.Fatalf("bob→alice %d: %v", i, err)
		}
		if dec[0] != 'B' || dec[1] != byte(i) {
			t.Errorf("bob→alice %d: wrong content", i)
		}
	}

	// Alice 发送 3 条
	for i := 0; i < 3; i++ {
		wire, _ := aliceR.Encrypt([]byte{byte('A'), byte(i)})
		dec, err := bobR.Decrypt(wire)
		if err != nil {
			t.Fatalf("alice→bob %d: %v", i, err)
		}
		if dec[0] != 'A' || dec[1] != byte(i) {
			t.Errorf("alice→bob %d: wrong content", i)
		}
	}
}

func TestRatchetForwardSecrecy(t *testing.T) {
	aliceID, _ := crypto.GenerateEphemeralKey()
	bobID, _ := crypto.GenerateEphemeralKey()
	aliceEph, _ := crypto.GenerateEphemeralKey()

	bobR, bobPK, _ := ratchet.InitAsInitiator(&bobID.Private, &aliceID.Public, &aliceEph.Public)
	aliceR, err := ratchet.InitAsResponder(&aliceID.Private, &bobID.Public, &aliceEph.Private, &bobPK)
	if err != nil {
		t.Fatal(err)
	}

	// Bob 发送 msg1，Alice 解密
	wire1, _ := bobR.Encrypt([]byte("secret-message-1"))
	_, err = aliceR.Decrypt(wire1)

	// Bob 发送 msg2
	wire2, _ := bobR.Encrypt([]byte("secret-message-2"))

	// 如果我们交换了棘轮状态，msg1 应该无法解密
	// （消息密钥在使用后被销毁）
	_, err = aliceR.Decrypt(wire1) // 重放尝试
	if err == nil {
		t.Error("replay of msg1 should fail (forward secrecy)")
	}

	// msg2 应该仍然可以正常解密
	dec2, err := aliceR.Decrypt(wire2)
	if err != nil {
		t.Fatalf("msg2 decrypt: %v", err)
	}
	if string(dec2) != "secret-message-2" {
		t.Errorf("msg2: got %q", dec2)
	}
}

func TestRatchetHeaderSize(t *testing.T) {
	aliceID, _ := crypto.GenerateEphemeralKey()
	bobID, _ := crypto.GenerateEphemeralKey()
	aliceEph, _ := crypto.GenerateEphemeralKey()

	bobR, bobPK, _ := ratchet.InitAsInitiator(&bobID.Private, &aliceID.Public, &aliceEph.Public)
	aliceR, err := ratchet.InitAsResponder(&aliceID.Private, &bobID.Public, &aliceEph.Private, &bobPK)
	if err != nil {
		t.Fatal(err)
	}

	wire, _ := bobR.Encrypt([]byte("test"))
	dec, _ := aliceR.Decrypt(wire)

	if len(wire) <= ratchet.HeaderSize {
		t.Errorf("wire message too short: %d", len(wire))
	}
	if string(dec) != "test" {
		t.Errorf("content mismatch")
	}
}

func TestRatchetWrongRecipient(t *testing.T) {
	aliceID, _ := crypto.GenerateEphemeralKey()
	bobID, _ := crypto.GenerateEphemeralKey()
	eveID, _ := crypto.GenerateEphemeralKey()
	aliceEph, _ := crypto.GenerateEphemeralKey()
	var err error

	bobR, bobPK, _ := ratchet.InitAsInitiator(&bobID.Private, &aliceID.Public, &aliceEph.Public)
	_, err = ratchet.InitAsResponder(&aliceID.Private, &bobID.Public, &aliceEph.Private, &bobPK)
	if err != nil {
		t.Fatal(err)
	}

	// Eve 尝试用自己的密钥解密
	eveR, err := ratchet.InitAsResponder(&eveID.Private, &bobID.Public, &aliceEph.Private, &bobPK)
	if err != nil {
		t.Fatal(err)
	}

	wire, _ := bobR.Encrypt([]byte("for alice only"))
	_, err = eveR.Decrypt(wire)
	if err == nil {
		t.Error("eve should not be able to decrypt alice's message")
	}
}
