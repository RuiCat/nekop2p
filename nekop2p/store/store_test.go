package store_test

import (
	"os"
	"testing"

	"github.com/nekop2p/nekop2p/store"
)

func TestNewAndClose(t *testing.T) {
	dir := t.TempDir()
	s, err := store.New(dir)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if s == nil {
		t.Fatal("nil store")
	}

	path := s.Path()
	if _, err := os.Stat(path); os.IsNotExist(err) {
		t.Errorf("db file not found at %s", path)
	}

	if err := s.Close(); err != nil {
		t.Errorf("Close: %v", err)
	}
}

func TestWriteReadHeight(t *testing.T) {
	dir := t.TempDir()
	s, err := store.New(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	// 写入高度
	if err := s.Write(func(tx *store.Tx) error {
		tx.SetHeight(42)
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	// 读取高度
	var h int64
	if err := s.Read(func(tx *store.Tx) error {
		h = tx.Height()
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	if h != 42 {
		t.Errorf("height = %d, want 42", h)
	}
}

func TestUserCRUD(t *testing.T) {
	dir := t.TempDir()
	s, err := store.New(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	userData := []byte(`{"chain_id":"test123","recv_pk":"aaaa","send_pk":"bbbb"}`)
	userKey := "user:test123"

	// 创建用户
	if err := s.Write(func(tx *store.Tx) error {
		return tx.PutUser(userKey, userData)
	}); err != nil {
		t.Fatal(err)
	}

	// 读取用户
	if err := s.Read(func(tx *store.Tx) error {
		got := tx.GetUser(userKey)
		if got == nil {
			t.Error("GetUser returned nil")
		} else if string(got) != string(userData) {
			t.Errorf("GetUser = %s, want %s", got, userData)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	// 删除用户
	if err := s.Write(func(tx *store.Tx) error {
		return tx.DeleteUser(userKey)
	}); err != nil {
		t.Fatal(err)
	}

	// 验证已删除
	if err := s.Read(func(tx *store.Tx) error {
		got := tx.GetUser(userKey)
		if got != nil {
			t.Error("GetUser should return nil after delete")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func TestBondCRUD(t *testing.T) {
	dir := t.TempDir()
	s, err := store.New(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	bondData := []byte(`{"bond_id":"bond001","amount":1000}`)
	bondKey := "bond001"

	// 创建
	if err := s.Write(func(tx *store.Tx) error {
		return tx.PutBond(bondKey, bondData)
	}); err != nil {
		t.Fatal(err)
	}

	// 读取
	if err := s.Read(func(tx *store.Tx) error {
		got := tx.GetBond(bondKey)
		if got == nil {
			t.Error("GetBond returned nil")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func TestLoanCRUD(t *testing.T) {
	dir := t.TempDir()
	s, err := store.New(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	loanData := []byte(`{"loan_id":"loan001","amount":500}`)
	loanKey := "loan001"

	if err := s.Write(func(tx *store.Tx) error {
		return tx.PutLoan(loanKey, loanData)
	}); err != nil {
		t.Fatal(err)
	}

	if err := s.Read(func(tx *store.Tx) error {
		got := tx.GetLoan(loanKey)
		if got == nil {
			t.Error("GetLoan returned nil")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func TestPoolBalance(t *testing.T) {
	dir := t.TempDir()
	s, err := store.New(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	// 初始为 nil
	if err := s.Read(func(tx *store.Tx) error {
		b := tx.GetPool()
		if b != nil {
			t.Errorf("initial pool balance should be nil, got %v", b)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	// 设置余额
	if err := s.Write(func(tx *store.Tx) error {
		return tx.PutPool([]byte{0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x30, 0x39}) // 12345 big-endian
	}); err != nil {
		t.Fatal(err)
	}

	// 读取余额
	if err := s.Read(func(tx *store.Tx) error {
		b := tx.GetPool()
		if b == nil {
			t.Error("pool should not be nil")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func TestNullifier(t *testing.T) {
	dir := t.TempDir()
	s, err := store.New(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	nf := []byte("nullifier-abc123")

	// 初始未花费
	if err := s.Read(func(tx *store.Tx) error {
		if tx.HasNullifier(string(nf)) {
			t.Error("nullifier should not exist initially")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	// 记录 nullifier
	if err := s.Write(func(tx *store.Tx) error {
		return tx.PutNullifier(string(nf))
	}); err != nil {
		t.Fatal(err)
	}

	// 验证已存在
	if err := s.Read(func(tx *store.Tx) error {
		if !tx.HasNullifier(string(nf)) {
			t.Error("nullifier should exist after Put")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func TestForEachUser(t *testing.T) {
	dir := t.TempDir()
	s, err := store.New(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	// 写入 3 个用户
	users := map[string][]byte{
		"user:a": []byte(`{"name":"Alice"}`),
		"user:b": []byte(`{"name":"Bob"}`),
		"user:c": []byte(`{"name":"Carol"}`),
	}

	if err := s.Write(func(tx *store.Tx) error {
		for k, v := range users {
			if err := tx.PutUser(k, v); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	// 遍历
	count := 0
	if err := s.Read(func(tx *store.Tx) error {
		return tx.ForEachUser(func(key string, value []byte) error {
			count++
			return nil
		})
	}); err != nil {
		t.Fatal(err)
	}

	if count != 3 {
		t.Errorf("ForEachUser count = %d, want 3", count)
	}
}

func TestWriteAtomicity(t *testing.T) {
	dir := t.TempDir()
	s, err := store.New(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	// 模拟事务失败
	if err := s.Write(func(tx *store.Tx) error {
		tx.SetHeight(99)
		tx.PutUser("user:x", []byte("data"))
		return os.ErrNotExist // 模拟错误
	}); err == nil {
		t.Error("expected error")
	}

	// 验证回滚——user 不应存在
	if err := s.Read(func(tx *store.Tx) error {
		if tx.GetUser("user:x") != nil {
			t.Error("user should not exist after failed write")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}
