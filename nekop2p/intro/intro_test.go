package intro_test

import (
	"testing"
	"time"

	"github.com/nekop2p/nekop2p/crypto"
	"github.com/nekop2p/nekop2p/intro"
)

func TestIssueAndVerify(t *testing.T) {
	alice, _ := crypto.GenerateDualKeys()
	bob, _ := crypto.GenerateDualKeys()
	charlie, _ := crypto.GenerateDualKeys()

	var aliceID, bobID, charlieID [32]byte
	copy(aliceID[:], alice.SendKey.Public[:])
	copy(bobID[:], bob.SendKey.Public[:])
	copy(charlieID[:], charlie.SendKey.Public[:])

	cert := intro.Issue(alice.SendKey.Private, aliceID, bobID, charlieID)

	if err := cert.Verify(alice.SendKey.Public); err != nil {
		t.Fatalf("verify: %v", err)
	}

	if cert.Introducer != aliceID {
		t.Error("introducer mismatch")
	}
	if cert.SubjectA != bobID {
		t.Error("subjectA mismatch")
	}
}

func TestCertExpiry(t *testing.T) {
	alice, _ := crypto.GenerateDualKeys()
	var aID, bID, cID [32]byte
	copy(aID[:], alice.SendKey.Public[:])

	cert := intro.Issue(alice.SendKey.Private, aID, bID, cID)

	// 手动将过期时间设为过去
	cert.Expiry = time.Now().Unix() - 1

	if err := cert.Verify(alice.SendKey.Public); err == nil {
		t.Error("expired cert should fail verification")
	}
}

func TestCertSerialization(t *testing.T) {
	alice, _ := crypto.GenerateDualKeys()
	var aID, bID, cID [32]byte
	copy(aID[:], alice.SendKey.Public[:])

	original := intro.Issue(alice.SendKey.Private, aID, bID, cID)
	data := original.Serialize()

	parsed, err := intro.ParseCert(data)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}

	if err := parsed.Verify(alice.SendKey.Public); err != nil {
		t.Fatalf("verify after parse: %v", err)
	}
	if parsed.Introducer != original.Introducer {
		t.Error("introducer mismatch after serialization")
	}
}

func TestCertForgery(t *testing.T) {
	alice, _ := crypto.GenerateDualKeys()
	eve, _ := crypto.GenerateDualKeys()
	var aID, bID, cID [32]byte
	copy(aID[:], alice.SendKey.Public[:])

	// Eve 试图伪造一个凭证，声称 Alice 把 Bob 介绍给了 Eve
	// Eve 用自己的密钥签名，但声称 Alice 是介绍人
	cert := intro.Issue(eve.SendKey.Private, aID, bID, cID)

	// 用 Alice 的公钥验证应该失败
	if err := cert.Verify(alice.SendKey.Public); err == nil {
		t.Error("forged cert should fail verification")
	}
}
