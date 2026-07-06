package frame_test

import (
	"bytes"
	"testing"

	"github.com/nekop2p/nekop2p/frame"
)

func TestFrameRoundtrip(t *testing.T) {
	// 创建会话密钥
	sendKey, _ := frame.GenerateSessionKey()
	recvKey, _ := frame.GenerateSessionKey()

	buf := new(bytes.Buffer)

	// Writer 使用 sendKey
	writer := frame.NewSessionKeys(sendKey, recvKey)
	// Reader 使用 recvKey（相同密钥，但交换使用方向）
	reader := frame.NewSessionKeys(recvKey, sendKey)

	payload := []byte("hello world frame test")

	// 写入
	f := frame.NewFrame(frame.FrameData, 0, payload)
	if err := writer.WriteEncryptedFrame(buf, f); err != nil {
		t.Fatal(err)
	}

	// 读取
	got, err := reader.ReadEncryptedFrame(buf)
	if err != nil {
		t.Fatal(err)
	}

	if got.Type != frame.FrameData {
		t.Errorf("type: got %d, want %d", got.Type, frame.FrameData)
	}
	if !bytes.Equal(got.Payload, payload) {
		t.Errorf("payload: got %q, want %q", got.Payload, payload)
	}
}

func TestFrameTypes(t *testing.T) {
	key, _ := frame.GenerateSessionKey()
	buf := new(bytes.Buffer)
	sk := frame.NewSessionKeys(key, key)

	types := []uint8{
		frame.FrameBeacon,
		frame.FrameData,
		frame.FramePing,
		frame.FramePong,
		frame.FrameClose,
		frame.FrameRoute,
	}

	for _, typ := range types {
		f := frame.NewFrame(typ, 0, []byte("test"))
		if err := sk.WriteEncryptedFrame(buf, f); err != nil {
			t.Fatalf("write type %d: %v", typ, err)
		}

		got, err := sk.ReadEncryptedFrame(buf)
		if err != nil {
			t.Fatalf("read type %d: %v", typ, err)
		}
		if got.Type != typ {
			t.Errorf("type mismatch: got %d, want %d", got.Type, typ)
		}
	}
}

func TestFrameTampering(t *testing.T) {
	key, _ := frame.GenerateSessionKey()
	var buf bytes.Buffer
	sk := frame.NewSessionKeys(key, key)

	f := frame.NewFrame(frame.FrameData, 0, []byte("tamper test"))
	if err := sk.WriteEncryptedFrame(&buf, f); err != nil {
		t.Fatal(err)
	}

	// 篡改缓冲区
	data := make([]byte, buf.Len())
	copy(data, buf.Bytes())
	data[len(data)-5] ^= 0xFF // 翻转认证标签中的比特

	// 尝试读取被篡改的数据
	buf2 := bytes.NewBuffer(data)
	sk2 := frame.NewSessionKeys(key, key)
	_, err := sk2.ReadEncryptedFrame(buf2)
	if err == nil {
		t.Error("tampered frame should fail decryption")
	}
}

func TestMultipleFrames(t *testing.T) {
	key, _ := frame.GenerateSessionKey()
	sk := frame.NewSessionKeys(key, key)

	messages := [][]byte{
		[]byte("message one"),
		[]byte("message two"),
		[]byte("message three"),
	}

	var readBuf bytes.Buffer
	for _, msg := range messages {
		f := frame.NewFrame(frame.FrameData, 0, msg)
		if err := sk.WriteEncryptedFrame(&readBuf, f); err != nil {
			t.Fatal(err)
		}
	}

	reader := frame.NewSessionKeys(key, key)
	for i, expected := range messages {
		got, err := reader.ReadEncryptedFrame(&readBuf)
		if err != nil {
			t.Fatalf("frame %d: %v", i, err)
		}
		if !bytes.Equal(got.Payload, expected) {
			t.Errorf("frame %d: got %q, want %q", i, got.Payload, expected)
		}
	}
}
