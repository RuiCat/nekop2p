//go:build !cosmos

package store_test

import (
	"encoding/json"
	"testing"

	"github.com/nekop2p/nekop2p/store"
)

type testStruct struct {
	Name   string
	Age    int
	Tags   []string
	Active bool
}

func TestGobEncodeDecode(t *testing.T) {
	original := &testStruct{
		Name:   "neko",
		Age:    3,
		Tags:   []string{"cat", "kawaii"},
		Active: true,
	}

	data, err := store.GobEncode(original)
	if err != nil {
		t.Fatalf("gob encode: %v", err)
	}
	if len(data) == 0 {
		t.Error("encoded data should not be empty")
	}

	var decoded testStruct
	if err := store.GobDecode(data, &decoded); err != nil {
		t.Fatalf("gob decode: %v", err)
	}

	if decoded.Name != original.Name {
		t.Errorf("name: got %q, want %q", decoded.Name, original.Name)
	}
	if decoded.Age != original.Age {
		t.Errorf("age: got %d, want %d", decoded.Age, original.Age)
	}
	if len(decoded.Tags) != len(original.Tags) {
		t.Fatalf("tags len: got %d, want %d", len(decoded.Tags), len(original.Tags))
	}
}

func TestHybridMarshalRoundtrip(t *testing.T) {
	original := &testStruct{
		Name:   "hybrid_test",
		Age:    42,
		Tags:   []string{"a", "b", "c"},
		Active: false,
	}

	// 混合编码
	data, err := store.HybridMarshal(original)
	if err != nil {
		t.Fatalf("hybrid marshal: %v", err)
	}
	if len(data) <= 1 {
		t.Error("hybrid data should have marker byte + payload")
	}
	if data[0] != 0x01 {
		t.Errorf("format marker: got 0x%02x, want 0x01", data[0])
	}

	// 混合解码
	var decoded testStruct
	if err := store.HybridUnmarshal(data, &decoded); err != nil {
		t.Fatalf("hybrid unmarshal: %v", err)
	}

	if decoded.Name != original.Name {
		t.Errorf("name: got %q, want %q", decoded.Name, original.Name)
	}
	if decoded.Age != original.Age {
		t.Errorf("age: got %d, want %d", decoded.Age, original.Age)
	}
}

func TestHybridUnmarshalJSONBackward(t *testing.T) {
	original := &testStruct{
		Name:   "legacy_data",
		Age:    99,
		Tags:   []string{"old"},
		Active: true,
	}

	// JSON 格式（向后兼容）
	jsonData, err := json.Marshal(original)
	if err != nil {
		t.Fatalf("json marshal: %v", err)
	}

	// 确保以 { 开头
	if jsonData[0] != '{' {
		t.Fatal("JSON data must start with {")
	}

	// HybridUnmarshal 应该能解码纯 JSON 数据
	var decoded testStruct
	if err := store.HybridUnmarshal(jsonData, &decoded); err != nil {
		t.Fatalf("hybrid unmarshal JSON: %v", err)
	}

	if decoded.Name != original.Name {
		t.Errorf("name: got %q, want %q", decoded.Name, original.Name)
	}
}

func TestHybridUnmarshalEmpty(t *testing.T) {
	var v testStruct
	if err := store.HybridUnmarshal(nil, &v); err == nil {
		t.Error("should error on nil data")
	}
	if err := store.HybridUnmarshal([]byte{}, &v); err == nil {
		t.Error("should error on empty data")
	}
}

func TestGobVsJSONSize(t *testing.T) {
	original := &testStruct{
		Name:   "a_typical_credit_note_commitment_record",
		Age:    123456,
		Tags:   []string{"borrower", "darkchain", "anonymous", "utxo"},
		Active: true,
	}

	gobData, _ := store.GobEncode(original)
	jsonData, _ := json.Marshal(original)

	// gob 通常比 JSON 紧凑（对于结构化数据）
	t.Logf("gob size: %d bytes, JSON size: %d bytes (ratio: %.2f)",
		len(gobData), len(jsonData), float64(len(gobData))/float64(len(jsonData)))
}

func TestHybridMarshalLarge(t *testing.T) {
	// 测试大结构体
	largeStruct := struct {
		ID      [32]byte
		Owner   [32]byte
		Serial  [32]byte
		Value   uint64
		Friends [][32]byte
	}{
		Value: 999999,
		Friends: make([][32]byte, 100),
	}
	for i := range largeStruct.Friends {
		largeStruct.Friends[i] = [32]byte{byte(i), 0x42}
	}

	data, err := store.HybridMarshal(&largeStruct)
	if err != nil {
		t.Fatalf("hybrid marshal large: %v", err)
	}

	var decoded struct {
		ID      [32]byte
		Owner   [32]byte
		Serial  [32]byte
		Value   uint64
		Friends [][32]byte
	}
	if err := store.HybridUnmarshal(data, &decoded); err != nil {
		t.Fatalf("hybrid unmarshal large: %v", err)
	}
	if decoded.Value != largeStruct.Value {
		t.Errorf("value: got %d, want %d", decoded.Value, largeStruct.Value)
	}
	if len(decoded.Friends) != len(largeStruct.Friends) {
		t.Errorf("friends len: got %d, want %d", len(decoded.Friends), len(largeStruct.Friends))
	}
}
