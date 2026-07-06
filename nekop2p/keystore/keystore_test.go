package keystore_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/nekop2p/nekop2p/keystore"
)

func TestGenerateBright(t *testing.T) {
	bk, err := keystore.GenerateBright()
	if err != nil {
		t.Fatal(err)
	}
	if bk.ChainID == [32]byte{} {
		t.Error("chain_id should not be zero")
	}
	if bk.RecvPK == [32]byte{} {
		t.Error("recv_pk should not be zero")
	}
}

func TestGenerateDark(t *testing.T) {
	dk, err := keystore.GenerateDark()
	if err != nil {
		t.Fatal(err)
	}
	if dk.MasterSecret == [32]byte{} {
		t.Error("master_secret should not be zero")
	}
}

func TestBrightSaveLoad(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "bright_keys.json")

	bk, _ := keystore.GenerateBright()
	if err := keystore.SaveBright(path, bk); err != nil {
		t.Fatal(err)
	}

	loaded, err := keystore.LoadBright(path)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.ChainID != bk.ChainID {
		t.Error("chain_id mismatch")
	}
}

func TestDarkSaveLoad(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "dark_keys.json")

	dk, _ := keystore.GenerateDark()
	if err := keystore.SaveDark(path, dk); err != nil {
		t.Fatal(err)
	}

	loaded, err := keystore.LoadDark(path)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.MasterSecret != dk.MasterSecret {
		t.Error("master_secret mismatch")
	}
}

func TestBrightFilePermissions(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "bright_keys.json")

	bk, _ := keystore.GenerateBright()
	keystore.SaveBright(path, bk)

	info, _ := os.Stat(path)
	if info.Mode().Perm() != 0600 {
		t.Errorf("permissions: %o", info.Mode().Perm())
	}
}

func TestKeysIndependence(t *testing.T) {
	bk1, _ := keystore.GenerateBright()
	bk2, _ := keystore.GenerateBright()
	if bk1.ChainID == bk2.ChainID {
		t.Error("different generations should have different chain_ids")
	}

	dk1, _ := keystore.GenerateDark()
	dk2, _ := keystore.GenerateDark()
	if dk1.MasterSecret == dk2.MasterSecret {
		t.Error("different generations should have different master_secrets")
	}
}
