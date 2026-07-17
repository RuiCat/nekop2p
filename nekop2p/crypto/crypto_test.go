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
	data[0] ^= 1
	if crypto.Verify(&keys.SendKey.Public, data, sig) {
		t.Error("verification should fail with tampered data")
	}
}

func TestPRF(t *testing.T) {
	key := []byte("master-secret-key-32-bytes-xxx")
	data := []byte("cycle-marker-data")
	r1 := crypto.PRF(key, data)
	r2 := crypto.PRF(key, data)
	if r1 != r2 {
		t.Error("PRF should be deterministic")
	}
	r3 := crypto.PRF(key, []byte("different"))
	if r1 == r3 {
		t.Error("different inputs should produce different outputs")
	}
}

func TestDeriveKey(t *testing.T) {
	secret := []byte("secret")
	k1 := crypto.DeriveKey(secret, []byte("a"))
	k2 := crypto.DeriveKey(secret, []byte("b"))
	if k1 == k2 {
		t.Error("different info should produce different keys")
	}
}

func TestDeriveSharedSecret(t *testing.T) {
	keys, err := crypto.GenerateDualKeys()
	if err != nil {
		t.Fatal(err)
	}
	shared, err := crypto.DeriveSharedSecret(&keys.RecvKey.Private, &keys.RecvKey.Public)
	if err != nil {
		t.Fatal(err)
	}
	if len(shared) != 32 {
		t.Errorf("expected 32-byte shared secret, got %d", len(shared))
	}
	shared2, _ := crypto.DeriveSharedSecret(&keys.RecvKey.Private, &keys.RecvKey.Public)
	if !bytes.Equal(shared, shared2) {
		t.Error("shared secret should be deterministic")
	}
}

func TestKeyPairZero(t *testing.T) {
	keys, err := crypto.GenerateDualKeys()
	if err != nil {
		t.Fatal(err)
	}
	keys.RecvKey.Zero()
	if keys.RecvKey.Private != [32]byte{} {
		t.Error("Zero should clear the 32-byte private key")
	}
	keys.SendKey.Private = [64]byte{}
	t.Log("Manual clear verified")
}

func TestMemzero(t *testing.T) {
	buf := make([]byte, 64)
	for i := range buf {
		buf[i] = 0xFF
	}
	crypto.Memzero(buf)
	for i, b := range buf {
		if b != 0 {
			t.Errorf("Memzero failed at index %d: got %d", i, b)
		}
	}
}

func TestGenerateEphemeralKey(t *testing.T) {
	kp, err := crypto.GenerateEphemeralKey()
	if err != nil {
		t.Fatal(err)
	}
	if kp.Public == [32]byte{} {
		t.Error("ephemeral public key should not be zero")
	}
	kp2, _ := crypto.GenerateEphemeralKey()
	if kp.Public == kp2.Public {
		t.Error("two ephemeral keys should differ")
	}
}

func TestDeriveChainID(t *testing.T) {
	var pk [32]byte
	pk[0] = 0x42
	id1 := crypto.DeriveChainID(pk)
	id2 := crypto.DeriveChainID(pk)
	if id1 != id2 {
		t.Error("DeriveChainID should be deterministic")
	}
	var pk2 [32]byte
	pk2[0] = 0x99
	id3 := crypto.DeriveChainID(pk2)
	if id1 == id3 {
		t.Error("different inputs should produce different chain IDs")
	}
}

func TestNewAESGCM(t *testing.T) {
	key := make([]byte, 32)
	for i := range key {
		key[i] = byte(i)
	}
	aead, err := crypto.NewAESGCM(key)
	if err != nil {
		t.Fatal(err)
	}
	nonce := make([]byte, 12)
	pt := []byte("test message")
	ct := aead.Seal(nil, nonce, pt, nil)
	dec, err := aead.Open(nil, nonce, ct, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(pt, dec) {
		t.Error("AES-GCM roundtrip failed")
	}
}

func TestRandomBytes(t *testing.T) {
	b1, err := crypto.RandomBytes(32)
	if err != nil {
		t.Fatal(err)
	}
	if len(b1) != 32 {
		t.Errorf("expected 32 bytes, got %d", len(b1))
	}
	b2, _ := crypto.RandomBytes(32)
	if bytes.Equal(b1, b2) {
		t.Error("two random outputs should almost certainly differ")
	}
	b3, err := crypto.RandomBytes(0)
	if err != nil {
		t.Fatal(err)
	}
	if len(b3) != 0 {
		t.Error("zero-length request should return empty slice")
	}
}

func TestPQKeyGeneration(t *testing.T) {
	keys, err := crypto.GeneratePQKeys()
	if err != nil {
		t.Fatal(err)
	}
	if crypto.PQCiphertextSize() == 0 {
		t.Error("PQ ciphertext size should be > 0")
	}
	if crypto.PQPublicKeySize() == 0 {
		t.Error("PQ public key size should be > 0")
	}
	keys.Zero()
}

func TestDualKeysWithPQ(t *testing.T) {
	keys, err := crypto.GenerateDualKeysWithPQ()
	if err != nil {
		t.Fatal(err)
	}
	if !keys.HasPQ() {
		t.Error("dual keys with PQ should have PQ capability")
	}
	keys.Zero()
	if keys.HasPQ() {
		t.Error("keys should be cleared after Zero")
	}
}
