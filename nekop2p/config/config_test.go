package config_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/nekop2p/nekop2p/config"
)

func TestDefault(t *testing.T) {
	cfg := config.Default()
	if cfg.ListenAddr != "[::]:9070" {
		t.Errorf("listen: %s", cfg.ListenAddr)
	}
	if cfg.TargetConnections != 20 {
		t.Errorf("target: %d", cfg.TargetConnections)
	}
}

func TestSaveAndLoad(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")

	cfg := config.Default()
	cfg.RecvSK[0] = 0x42
	cfg.SendSK[0] = 0x99

	if err := config.Save(path, cfg); err != nil {
		t.Fatalf("save: %v", err)
	}

	loaded, err := config.Load(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}

	if loaded.RecvSK != cfg.RecvSK {
		t.Error("recv_sk mismatch")
	}
	if loaded.ChainID != cfg.ChainID {
		t.Error("chain_id mismatch")
	}
}

func TestLoadMissing(t *testing.T) {
	_, err := config.Load("/nonexistent/config.json")
	if err == nil {
		t.Error("should fail for missing file")
	}
}

func TestFilePermissions(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")

	cfg := config.Default()
	config.Save(path, cfg)

	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0600 {
		t.Errorf("permissions: got %o, want 0600", info.Mode().Perm())
	}
}
