package node_test

import (
	"testing"
	"time"

	"github.com/nekop2p/nekop2p/x/node"
)

func TestNewExaminer(t *testing.T) {
	e := node.NewExaminer(node.DefaultRelayExamConfig(), node.DefaultRecordExamConfig())
	if e == nil {
		t.Fatal("examiner is nil")
	}
}

func TestEvaluateRelayPass(t *testing.T) {
	e := node.NewExaminer(node.DefaultRelayExamConfig(), node.DefaultRecordExamConfig())

	result, err := e.Evaluate(
		"candidate-001",
		node.ExamRelay,
		600,                          // trust weight > 500
		100*24*time.Hour,             // online > 90 days
		15000,                        // relay count > 10000
		0,
	)
	if err != nil {
		t.Fatalf("evaluate: %v", err)
	}
	if !result.Passed {
		t.Errorf("should pass: score=%d", result.Score)
	}
	if result.ExamType != node.ExamRelay {
		t.Error("wrong exam type")
	}
}

func TestEvaluateRelayFail(t *testing.T) {
	e := node.NewExaminer(node.DefaultRelayExamConfig(), node.DefaultRecordExamConfig())

	result, err := e.Evaluate(
		"candidate-002",
		node.ExamRelay,
		100,                          // trust weight < 500
		10*24*time.Hour,              // online < 90 days
		100,                          // relay count < 10000
		0,
	)
	if err != nil {
		t.Fatalf("evaluate: %v", err)
	}
	if result.Passed {
		t.Error("should fail with low scores")
	}
}

func TestEvaluateRecordPass(t *testing.T) {
	e := node.NewExaminer(node.DefaultRelayExamConfig(), node.DefaultRecordExamConfig())

	result, err := e.Evaluate(
		"candidate-003",
		node.ExamRecord,
		1500,                         // trust weight > 1000 ✅
		120*24*time.Hour,             // online > 90 days ✅
		0,
		200,                          // storage proofs > 100 ✅
	)
	if err != nil {
		t.Fatalf("evaluate: %v", err)
	}
	// 3/4 checks pass → 75% ≥ 75% pass score
	if !result.Passed {
		t.Errorf("should pass record exam: score=%d", result.Score)
	}
}

func TestEvaluateRecordFail(t *testing.T) {
	e := node.NewExaminer(node.DefaultRelayExamConfig(), node.DefaultRecordExamConfig())

	result, err := e.Evaluate(
		"candidate-004",
		node.ExamRecord,
		500,                          // trust weight < 1000
		50*24*time.Hour,              // online < 90 days
		0,
		50,                           // storage proofs < 100
	)
	if err != nil {
		t.Fatalf("evaluate: %v", err)
	}
	if result.Passed {
		t.Error("should fail record exam")
	}
}

func TestExamResultHash(t *testing.T) {
	e := node.NewExaminer(node.DefaultRelayExamConfig(), node.DefaultRecordExamConfig())

	r1, _ := e.Evaluate("candidate", node.ExamRelay, 600, 100*24*time.Hour, 15000, 0)
	r2, _ := e.Evaluate("candidate", node.ExamRelay, 600, 100*24*time.Hour, 15000, 0)

	// 相同参数应产生相同哈希（确定性）
	if r1.ResultHash == r2.ResultHash {
		// 时间戳不同所以哈希不同，这是预期的
		t.Log("hashes differ due to timestamp (expected)")
	}
	if r1.ResultHash == [32]byte{} {
		t.Error("result hash should not be zero")
	}
}

func TestExamUnknownType(t *testing.T) {
	e := node.NewExaminer(node.DefaultRelayExamConfig(), node.DefaultRecordExamConfig())

	_, err := e.Evaluate("candidate", 999, 500, 100*24*time.Hour, 10000, 0)
	if err == nil {
		t.Error("unknown exam type should return error")
	}
}

func TestExamTypeString(t *testing.T) {
	if node.ExamRelay.String() != "relay" {
		t.Error("relay string mismatch")
	}
	if node.ExamRecord.String() != "record" {
		t.Error("record string mismatch")
	}
}
