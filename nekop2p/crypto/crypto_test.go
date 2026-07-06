package crypto_test

import (
	"bytes"
	"testing"

	"github.com/nekop2p/nekop2p/crypto"
)

func TestDualKeys(t *testing.T) {
	keys, err := crypto.GenerateDualKeys()
	if err != nil {
		t.Fatal(err)
	}

	// 两对密钥都应该是非零的
	if keys.RecvKey.Public == [32]byte{} {
		t.Error("recv public key is zero")
	}
	if keys.SendKey.Public == [32]byte{} {
		t.Error("send public key is zero")
	}
}

func TestKEMRoundtrip(t *testing.T) {
	alice, _ := crypto.GenerateDualKeys()

	plaintext := []byte("hello from bob")
	encrypted, err := crypto.KEMEncrypt(&alice.RecvKey.Public, plaintext)
	if err != nil {
		t.Fatal(err)
	}

	decrypted, err := crypto.KEMDecrypt(&alice.RecvKey.Private, encrypted)
	if err != nil {
		t.Fatal(err)
	}

	if !bytes.Equal(plaintext, decrypted) {
		t.Errorf("roundtrip failed: got %q, want %q", decrypted, plaintext)
	}
}

func TestKEMWrongKey(t *testing.T) {
	alice, _ := crypto.GenerateDualKeys()
	eve, _ := crypto.GenerateDualKeys()

	plaintext := []byte("secret")
	encrypted, _ := crypto.KEMEncrypt(&alice.RecvKey.Public, plaintext)

	// Eve 尝试用自己的密钥解密
	_, err := crypto.KEMDecrypt(&eve.RecvKey.Private, encrypted)
	if err == nil {
		t.Error("decryption should have failed with wrong key")
	}
}

func TestSignVerify(t *testing.T) {
	keys, _ := crypto.GenerateDualKeys()

	data := []byte("important message")
	sig := crypto.Sign(&keys.SendKey.Private, data)

	if !crypto.Verify(&keys.SendKey.Public, data, sig) {
		t.Error("signature verification failed")
	}

	// 篡改的数据应该验证失败
	data[0] ^= 1
	if crypto.Verify(&keys.SendKey.Public, data, sig) {
		t.Error("verification should fail with tampered data")
	}
}

func TestPRF(t *testing.T) {
	key := []byte("master-secret-key-32-bytes-xxx")
	data := []byte("cycle-marker-data")

	result1 := crypto.PRF(key, data)
	result2 := crypto.PRF(key, data)

	// 相同输入 → 相同输出
	if result1 != result2 {
		t.Error("PRF should be deterministic")
	}

	// 不同数据 → 不同输出
	data2 := []byte("different-data")
	result3 := crypto.PRF(key, data2)
	if result1 == result3 {
		t.Error("different inputs should produce different outputs")
	}
}

func TestDeriveKey(t *testing.T) {
	secret := []byte("secret")
	info1 := []byte("purpose-a")
	info2 := []byte("purpose-b")

	k1 := crypto.DeriveKey(secret, info1)
	k2 := crypto.DeriveKey(secret, info2)

	if k1 == k2 {
		t.Error("different info should produce different keys")
	}
}
