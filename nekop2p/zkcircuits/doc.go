// Package zkcircuits 提供 nekop2p 的所有 ZK 电路定义。
//
// 电路列表：
//   identity/  — 证明 ≥3 个有效担保人（Ed25519 + Merkle）
//   credit/    — 证明有效信用票据花费（UTXO 模型）
//   marker/    — 证明身份标记派生正确性（防自我交易）
//   repay/     — 证明通过明链分片偿还贷款
//   work/      — 证明节点工作量以符合薪资资格
//
// 所有电路使用 groth16 在 BN254 曲线上通过 gnark 实现。
package zkcircuits

// CircuitInfo 包含关于已编译电路的元数据。
type CircuitInfo struct {
	Name           string
	ConstraintCount int
	ProvingTime    string // 预估
	VerifyTime     string // 预估
	ProofSize      int    // 字节
}

// AllCircuits 返回所有电路的元数据（约束数为实际编译结果）。
func AllCircuits() []CircuitInfo {
	return []CircuitInfo{
		{Name: "identity", ConstraintCount: 62891, ProvingTime: "3-8s", VerifyTime: "3ms", ProofSize: 200},
		{Name: "credit",   ConstraintCount: 14429, ProvingTime: "1-3s", VerifyTime: "3ms", ProofSize: 200},
		{Name: "marker",   ConstraintCount: 661,   ProvingTime: "0.5s", VerifyTime: "3ms", ProofSize: 200},
		{Name: "repay",    ConstraintCount: 8503,  ProvingTime: "1-2s", VerifyTime: "3ms", ProofSize: 200},
		{Name: "work",     ConstraintCount: 6224,  ProvingTime: "0.8s", VerifyTime: "3ms", ProofSize: 200},
	}
}

// TotalConstraints 返回所有电路的约束总数。
func TotalConstraints() int {
	total := 0
	for _, c := range AllCircuits() {
		total += c.ConstraintCount
	}
	return total
}

// CircuitVersions 记录电路版本号，约束变更后需递增。
// 版本变更必须重新执行可信设置。
const CircuitVersion = 2

// ⚠️ 重新生成可信设置:
//   go run ./zkcircuits/setup/ -out ./keys/
//   → 生成 5 组 ProvingKey + VerifyingKey
//   → 将 VerifyingKey 更新到 x/zk/verifier.go

// ZK 电路使用说明:
//
// 1. 可信设置 (一次性):
//    go run setup.go → 生成 ProvingKey + VerifyingKey
//    VerifyingKey 存储到链上参数模块
//
// 2. 证明生成 (用户本地):
//    go run prove.go -circuit=identity -input=witness.json
//    → 生成 proof (约200字节)
//
// 3. 链上验证 (Cosmos SDK BeginBlocker/CheckTx):
//    zkKeeper.Verify(circuitType, proof, publicInputs)
//    → 通过/拒绝 (约3毫秒)
