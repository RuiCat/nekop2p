// Package node 提供正式节点的能力考试框架。
//
// 能力考试是正式节点（OFFICIAL_RELAY / OFFICIAL_RECORD）的准入门槛。
// 考试内容：转发能力、存储完整性、在线时间、信任权重。
package node

import (
	"crypto/sha256"
	"fmt"
	"time"
)

// ExamResult 表示一次能力考试的结果。
type ExamResult struct {
	CandidateID  string    // 考生 chain_id
	ExamType     ExamType  // 考试类型
	Passed       bool      // 是否通过
	Score        uint64    // 评分 (0-100)
	CompletedAt  time.Time // 完成时间
	ResultHash   [32]byte  // 结果哈希（链上锚定）
	EvaluatorID  string    // 评估者 chain_id
}

// ExamType 枚举考试类型。
type ExamType int

const (
	ExamRelay  ExamType = iota // 转发能力考试
	ExamRecord                 // 存储能力考试
)

func (et ExamType) String() string {
	switch et {
	case ExamRelay: return "relay"
	case ExamRecord: return "record"
	default: return "unknown"
	}
}

// ExamConfig 考试配置。
type ExamConfig struct {
	MinTrustWeight    uint64        // 最低信任权重
	MinOnlineDuration time.Duration // 最低在线时长
	MinRelayCount     uint64        // 最低转发次数（仅转发考试）
	MinStorageProofs  uint64        // 最低存储证明数（仅记录考试）
	PassScore         uint64        // 及格分数
}

// DefaultRelayExamConfig 返回默认的转发节点考试配置。
func DefaultRelayExamConfig() ExamConfig {
	return ExamConfig{
		MinTrustWeight:    500,
		MinOnlineDuration: 90 * 24 * time.Hour, // 3 个月
		MinRelayCount:     10000,
		PassScore:         75, // 4 项中通过 3 项即可
	}
}

// DefaultRecordExamConfig 返回默认的记录节点考试配置。
func DefaultRecordExamConfig() ExamConfig {
	return ExamConfig{
		MinTrustWeight:    1000,
		MinOnlineDuration: 90 * 24 * time.Hour, // 3 个月
		MinStorageProofs:  100,
		PassScore:         75, // 4 项中通过 3 项即可
	}
}

// Examiner 评估候选人的能力。
type Examiner struct {
	relayConfig  ExamConfig
	recordConfig ExamConfig
}

// NewExaminer 创建新的考试评估器。
func NewExaminer(relayCfg, recordCfg ExamConfig) *Examiner {
	return &Examiner{
		relayConfig:  relayCfg,
		recordConfig: recordCfg,
	}
}

// Evaluate 评估候选人是否满足考试要求。
func (e *Examiner) Evaluate(candidateID string, examType ExamType,
	trustWeight uint64, onlineDuration time.Duration,
	relayCount uint64, storageProofs uint64) (*ExamResult, error) {

	var cfg ExamConfig
	switch examType {
	case ExamRelay:
		cfg = e.relayConfig
	case ExamRecord:
		cfg = e.recordConfig
	default:
		return nil, fmt.Errorf("exam: unknown exam type: %v", examType)
	}

	result := &ExamResult{
		CandidateID: candidateID,
		ExamType:    examType,
		CompletedAt: time.Now(),
	}

	// 逐项评分
	var checksPassed int
	totalChecks := 3

	// 信任权重检查
	if trustWeight >= cfg.MinTrustWeight {
		checksPassed++
	}

	// 在线时长检查
	if onlineDuration >= cfg.MinOnlineDuration {
		checksPassed++
	}

	// 专项能力检查
	switch examType {
	case ExamRelay:
		totalChecks = 4
		if relayCount >= cfg.MinRelayCount {
			checksPassed++
		}
	case ExamRecord:
		totalChecks = 4
		if storageProofs >= cfg.MinStorageProofs {
			checksPassed++
		}
	}

	result.Score = uint64(checksPassed) * 100 / uint64(totalChecks)
	result.Passed = result.Score >= cfg.PassScore

	// 生成结果哈希
	result.ResultHash = computeResultHash(result)

	return result, nil
}

func computeResultHash(r *ExamResult) [32]byte {
	data := fmt.Sprintf("%s:%d:%t:%d:%d",
		r.CandidateID, r.ExamType, r.Passed, r.Score, r.CompletedAt.Unix())
	return sha256.Sum256([]byte(data))
}
