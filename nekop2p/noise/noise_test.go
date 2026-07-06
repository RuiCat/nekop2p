package noise_test

import (
	"testing"

	"github.com/nekop2p/nekop2p/crypto"
	"github.com/nekop2p/nekop2p/noise"
)

func TestNoiseIKHandshake(t *testing.T) {
	aliceKey, _ := crypto.GenerateEphemeralKey()
	bobKey, _ := crypto.GenerateEphemeralKey()

	// Bob 以 IK 模式向 Alice 发起握手
	bobHS := noise.NewInitiatorIK(bobKey, &aliceKey.Public, noise.RoleFriend)
	aliceHS := noise.NewResponderIK(aliceKey, nil, noise.RoleFriend)

	// IK msg1: Bob → Alice
	msg1, err := bobHS.WriteMessage([]byte("hello from bob"))
	if err != nil {
		t.Fatalf("bob write msg1: %v", err)
	}

	// Alice 读取 msg1
	payload1, err := aliceHS.ReadMessage(msg1)
	if err != nil {
		t.Fatalf("alice read msg1: %v", err)
	}
	if string(payload1) != "hello from bob" {
		t.Errorf("payload1: got %q, want 'hello from bob'", payload1)
	}

	// IK msg2: Alice → Bob
	msg2, err := aliceHS.WriteMessage([]byte("hello from alice"))
	if err != nil {
		t.Fatalf("alice write msg2: %v", err)
	}

	// Bob 读取 msg2
	payload2, err := bobHS.ReadMessage(msg2)
	if err != nil {
		t.Fatalf("bob read msg2: %v", err)
	}
	if string(payload2) != "hello from alice" {
		t.Errorf("payload2: got %q, want 'hello from alice'", payload2)
	}

	// 双方完成 → 应持有有效的密码状态
	bobResult := bobHS.Complete()
	aliceResult := aliceHS.Complete()
	if bobResult.SendCipher == nil || bobResult.RecvCipher == nil {
		t.Error("bob cipher states nil")
	}
	if aliceResult.SendCipher == nil || aliceResult.RecvCipher == nil {
		t.Error("alice cipher states nil")
	}
}

func TestNoiseIKEncryptedTransport(t *testing.T) {
	aliceKey, _ := crypto.GenerateEphemeralKey()
	bobKey, _ := crypto.GenerateEphemeralKey()

	bobHS := noise.NewInitiatorIK(bobKey, &aliceKey.Public, noise.RoleFriend)
	aliceHS := noise.NewResponderIK(aliceKey, nil, noise.RoleFriend)

	msg1, _ := bobHS.WriteMessage(nil)
	aliceHS.ReadMessage(msg1)
	msg2, _ := aliceHS.WriteMessage(nil)
	bobHS.ReadMessage(msg2)

	bobR := bobHS.Complete()
	aliceR := aliceHS.Complete()

	// Bob 加密 → Alice 解密
	plaintext := []byte("encrypted transport test")
	ct, err := bobR.SendCipher.Encrypt(plaintext, nil)
	if err != nil {
		t.Fatalf("bob encrypt: %v", err)
	}

	pt, err := aliceR.RecvCipher.Decrypt(ct, nil)
	if err != nil {
		t.Fatalf("alice decrypt: %v", err)
	}
	if string(pt) != string(plaintext) {
		t.Errorf("transport roundtrip failed")
	}

	// Alice 加密 → Bob 解密
	ct2, _ := aliceR.SendCipher.Encrypt([]byte("alice to bob"), nil)
	pt2, _ := bobR.RecvCipher.Decrypt(ct2, nil)
	if string(pt2) != "alice to bob" {
		t.Errorf("reverse transport failed")
	}
}

func TestNoiseNKHandshake(t *testing.T) {
	serverKey, _ := crypto.GenerateEphemeralKey()

	// 客户端以 NK 模式发起握手（知道服务器公钥）
	clientHS := noise.NewInitiatorNK(&serverKey.Public, noise.RolePublic)
	serverHS := noise.NewResponderNK(serverKey, noise.RolePublic)

	// NK msg1: client → server
	msg1, err := clientHS.WriteMessage([]byte("anonymous client"))
	if err != nil {
		t.Fatalf("client write msg1: %v", err)
	}

	// 服务器读取 msg1
	payload1, err := serverHS.ReadMessage(msg1)
	if err != nil {
		t.Fatalf("server read msg1: %v", err)
	}
	if string(payload1) != "anonymous client" {
		t.Errorf("payload1: got %q", payload1)
	}

	// NK msg2: server → client
	msg2, err := serverHS.WriteMessage([]byte("server response"))
	if err != nil {
		t.Fatalf("server write msg2: %v", err)
	}

	payload2, err := clientHS.ReadMessage(msg2)
	if err != nil {
		t.Fatalf("client read msg2: %v", err)
	}
	if string(payload2) != "server response" {
		t.Errorf("payload2: got %q", payload2)
	}

	// 验证密码状态是否正常工作
	clientR := clientHS.Complete()
	serverR := serverHS.Complete()

	ct, _ := clientR.SendCipher.Encrypt([]byte("test"), nil)
	pt, err := serverR.RecvCipher.Decrypt(ct, nil)
	if err != nil {
		t.Fatalf("NK transport decrypt: %v", err)
	}
	if string(pt) != "test" {
		t.Errorf("NK transport failed")
	}
}

func TestNoisePrologueSeparation(t *testing.T) {
	aliceKey, _ := crypto.GenerateEphemeralKey()
	bobKey, _ := crypto.GenerateEphemeralKey()

	// 相同密钥但不同角色 → 产生不同的会话密钥
	bobFriend := noise.NewInitiatorIK(bobKey, &aliceKey.Public, noise.RoleFriend)
	aliceFriend := noise.NewResponderIK(aliceKey, nil, noise.RoleFriend)

	msg1, _ := bobFriend.WriteMessage(nil)
	aliceFriend.ReadMessage(msg1)
	msg2, _ := aliceFriend.WriteMessage(nil)
	bobFriend.ReadMessage(msg2)

	bobFriendR := bobFriend.Complete()

	// 现在用不同角色尝试同一对密钥
	bobPadding := noise.NewInitiatorIK(bobKey, &aliceKey.Public, noise.RolePadding)
	alicePadding := noise.NewResponderIK(aliceKey, nil, noise.RolePadding)

	msg1b, _ := bobPadding.WriteMessage(nil)
	alicePadding.ReadMessage(msg1b)
	msg2b, _ := alicePadding.WriteMessage(nil)
	bobPadding.ReadMessage(msg2b)

	bobPaddingR := bobPadding.Complete()

	// Friend 和 Padding 应产生不同的会话密钥
	if bobFriendR.SendCipher.Key == bobPaddingR.SendCipher.Key {
		t.Error("different prologues should produce different session keys")
	}
}
