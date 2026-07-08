// Package keeper 实现明链模块的状态管理。
//
// 使用 BoltDB 嵌入式数据库持久化所有链上状态：
//   - UserBlocks（身份、密钥、好友、信用）
//   - GuaranteeBonds（基于抵押的担保）
//   - Pool（来自交易费的共享资金）
package keeper

import (
	"crypto/ed25519"
	"crypto/sha256"
	"fmt"
	"log"

	"github.com/nekop2p/nekop2p/store"
	"github.com/nekop2p/nekop2p/x/brightchain/types"
	zk "github.com/nekop2p/nekop2p/x/zk"
)

// Keeper 管理明链模块的状态（BoltDB 持久化）。
type Keeper struct {
	store        *store.ChainStore
	storeKey     string
	zkVerifier   *zk.Verifier        // ZK 证明验证器
	recourse     *RecourseManager     // 延期追偿管理器
	economy      *EconomyGovernor     // 经济参数治理器
	shadowClaims *ShadowClaimManager  // 影子凭证管理器
}

// NewKeeper 创建新的明链 Keeper。
func NewKeeper(s *store.ChainStore, storeKey string) *Keeper {
	return &Keeper{
		store:        s,
		storeKey:     storeKey,
		recourse:     NewRecourseManager(DefaultEpochConfig()),
		economy:      NewEconomyGovernor(DefaultEconomyParams()),
		shadowClaims: NewShadowClaimManager(),
	}
}

// initEconomyGovernor 延迟初始化经济治理器（防御性，已在 NewKeeper 中初始化）。
func (k *Keeper) initEconomyGovernor() {
	if k.economy == nil {
		k.economy = NewEconomyGovernor(DefaultEconomyParams())
	}
}

// RecourseManager 返回延期追偿管理器。
func (k *Keeper) RecourseManager() *RecourseManager {
	return k.recourse
}

// SetZkVerifier 设置 ZK 证明验证器。
// 应在初始化时调用，在加载验证密钥之后。
func (k *Keeper) SetZkVerifier(v *zk.Verifier) {
	k.zkVerifier = v
}

// ZkVerifier 返回当前的 ZK 验证器（可能为 nil）。
func (k *Keeper) ZkVerifier() *zk.Verifier {
	return k.zkVerifier
}

// ===== UserBlock =====

const MinGenesisUsers = 3 // 创世阶段需要的最少种子用户数

// UserCount 返回已注册用户总数。
func (k *Keeper) UserCount() int {
	count := 0
	k.store.Read(func(tx *store.Tx) error {
		return tx.ForEachUser(func(_ string, _ []byte) error {
			count++
			return nil
		})
	})
	return count
}

// IsGenesisPhase 返回当前是否处于创世阶段（用户数 < MinGenesisUsers）。
func (k *Keeper) IsGenesisPhase() bool {
	return k.UserCount() < MinGenesisUsers
}

// RegisterUser 从 MsgRegister 创建新的 UserBlock。
// 创世阶段：前 MinGenesisUsers 个用户无需邀请凭证即可注册。
// 正常阶段：必须提供 ≥3 个有效邀请凭证（Ed25519签名验证）。
func (k *Keeper) RegisterUser(ctx types.Context, msg *types.MsgRegister) (*types.UserBlock, error) {
	chainID := deriveChainID(msg.SendPk)
	key := string(chainID[:])

	// 检查是否已注册
	exists := false
	k.store.Read(func(tx *store.Tx) error {
		exists = tx.GetUser(key) != nil
		return nil
	})
	if exists {
		return nil, fmt.Errorf("user already registered: %x", chainID[:8])
	}

	// ===== ZK 身份证明验证 =====
	if len(msg.ZkIdentityProof) > 0 && k.zkVerifier != nil {
		// 构造身份电路赋值（公开输入: MySendPK）
		// 注意: 链上验证只需要公共输入，秘密输入（担保人信息）由证明生成者提供
		identityAssignment := zk.NewIdentityAssignment(msg.SendPk)
		if err := k.zkVerifier.VerifyIdentityProof(msg.ZkIdentityProof, identityAssignment); err != nil {
			return nil, fmt.Errorf("zk identity proof invalid: %w", err)
		}
		log.Printf("[brightchain] zk identity proof verified for %x", chainID[:8])
	}

	// ===== 邀请凭证验证 =====
	if !k.IsGenesisPhase() {
		// 正常阶段：必须提供 ≥3 个有效邀请凭证
		if len(msg.GuarantorSigs) < MinGenesisUsers {
			return nil, fmt.Errorf("need at least %d invitation credentials, got %d (chain is past genesis phase)",
				MinGenesisUsers, len(msg.GuarantorSigs))
		}

		// 验证每个凭证的 Ed25519 签名
		validCount := 0
		for _, credBytes := range msg.GuarantorSigs {
			cred, err := parseInviteCred(credBytes)
			if err != nil {
				continue
			}
			// 验证签名
			signedData := cred.serializeForSigning()
			if ed25519Verify(cred.InviterSendPK, signedData, cred.Signature) {
				// 验证担保人已在链上注册
				gBlock := k.GetUserBlock(ctx, cred.InviterChainID)
				if gBlock != nil && !bytesEqual(gBlock.SendPk, cred.InviterSendPK[:]) {
					continue // 担保人 send_pk 不匹配
				}
				if gBlock != nil {
					validCount++
				}
			}
		}
		if validCount < MinGenesisUsers {
			return nil, fmt.Errorf("only %d/%d valid invitation credentials", validCount, MinGenesisUsers)
		}
	}

	block := &types.UserBlock{
		Address:     chainID.String(),
		RecvPk:      msg.RecvPk,
		SendPk:      msg.SendPk,
		SeedPhase:   !k.IsGenesisPhase(), // 创世用户跳过种子期
		Guarantors:  extractGuarantorIDs(msg.GuarantorSigs),
		TrustWeight: 10,
		CreditLimit: 10,
	}
	if !block.SeedPhase {
		block.TrustWeight = 50 // 创世用户初始信任权重更高
		block.CreditLimit = 100
	}

	data := mustMarshal(block)
	err := k.store.Write(func(tx *store.Tx) error {
		return tx.PutUser(key, data)
	})
	return block, err
}

// GetUserBlock 按 chain_id 返回用户区块。
func (k *Keeper) GetUserBlock(ctx types.Context, id types.ChainID) *types.UserBlock {
	var result *types.UserBlock
	k.store.Read(func(tx *store.Tx) error {
		data := tx.GetUser(string(id[:]))
		if data != nil {
			mustUnmarshal(data, &result)
			if result != nil {
				result.Address = id.String() // 从 key 恢复 Address
			}
		}
		return nil
	})
	return result
}

// UpdateFriends 更新 UserBlock 的好友列表。
func (k *Keeper) UpdateFriends(ctx types.Context, msg *types.MsgUpdateFriends) error {
	chainID := parseChainID(msg.Sender)
	block := k.GetUserBlock(ctx, chainID)
	if block == nil {
		return fmt.Errorf("user not found: %s", msg.Sender)
	}
	if block.Address != msg.Sender {
		return fmt.Errorf("only the block owner can update friends")
	}

	block.Friends = append(block.Friends, msg.Add...)
	removeSet := make(map[string]bool)
	for _, r := range msg.Remove {
		removeSet[string(r)] = true
	}
	filtered := make([]*types.FriendRecord, 0, len(block.Friends))
	for _, f := range block.Friends {
		if !removeSet[string(f.ChainId)] {
			filtered = append(filtered, f)
		}
	}
	block.Friends = filtered

	data := mustMarshal(block)
	return k.store.Write(func(tx *store.Tx) error {
		return tx.PutUser(string(chainID[:]), data)
	})
}

// ===== GuaranteeBond =====

func (k *Keeper) CreateBond(ctx types.Context, msg *types.MsgGuarantee) (*types.GuaranteeBond, error) {
	bond := &types.GuaranteeBond{
		BondId:                generateBondID(msg.Inviter, msg.Invitee),
		Inviter:               []byte(msg.Inviter),
		Invitee:               []byte(msg.Invitee),
		LockedNoteCommitments: msg.BondNoteCommitments,
		Coefficient:           msg.Coefficient,
		Status:                types.BondStatus_ACTIVE,
	}
	bond.TotalBond = calculateTotalBond(msg.BondNoteCommitments)
	if msg.Coefficient > 0 {
		bond.SeedLimit = uint64(float64(bond.TotalBond) * float64(msg.Coefficient) / 100)
	}

	data := mustMarshal(bond)
	err := k.store.Write(func(tx *store.Tx) error {
		return tx.PutBond(bond.BondId, data)
	})
	if err != nil {
		return nil, err
	}
	k.addBondRef(ctx, msg.Inviter, msg.Invitee, bond.BondId)
	return bond, nil
}

func (k *Keeper) GetBond(ctx types.Context, bondID string) *types.GuaranteeBond {
	var result *types.GuaranteeBond
	k.store.Read(func(tx *store.Tx) error {
		data := tx.GetBond(bondID)
		if data != nil {
			store.HybridUnmarshal(data, &result)
		}
		return nil
	})
	return result
}

func (k *Keeper) ReleaseBond(ctx types.Context, bondID string) error {
	bond := k.GetBond(ctx, bondID)
	if bond == nil {
		return fmt.Errorf("bond not found: %s", bondID)
	}
	bond.Status = types.BondStatus_RELEASED
	data := mustMarshal(bond)
	return k.store.Write(func(tx *store.Tx) error { return tx.PutBond(bondID, data) })
}

func (k *Keeper) ForfeitBond(ctx types.Context, bondID string) error {
	bond := k.GetBond(ctx, bondID)
	if bond == nil {
		return fmt.Errorf("bond not found: %s", bondID)
	}
	bond.Status = types.BondStatus_FORFEITED
	data := mustMarshal(bond)
	return k.store.Write(func(tx *store.Tx) error { return tx.PutBond(bondID, data) })
}

func (k *Keeper) ListBonds(ctx types.Context) []*types.GuaranteeBond {
	var result []*types.GuaranteeBond
	k.store.Read(func(tx *store.Tx) error {
		return tx.ForEachBond(func(_ string, data []byte) error {
			var b types.GuaranteeBond
			if store.HybridUnmarshal(data, &b) == nil {
				result = append(result, &b)
			}
			return nil
		})
	})
	return result
}

func (k *Keeper) GetAllBonds(ctx types.Context) []*types.GuaranteeBond { return k.ListBonds(ctx) }

// ===== Pool =====

func (k *Keeper) GetPool(ctx types.Context) *types.Pool {
	pool := &types.Pool{}
	k.store.Read(func(tx *store.Tx) error {
		data := tx.GetPool()
		if data != nil {
			store.HybridUnmarshal(data, pool)
		}
		return nil
	})
	return pool
}

func (k *Keeper) setPool(tx *store.Tx, pool *types.Pool) error {
	data := mustMarshal(pool)
	return tx.PutPool(data)
}

// SetPool 持久化资金池状态。
func (k *Keeper) SetPool(pool *types.Pool) error {
	return k.store.Write(func(tx *store.Tx) error { return k.setPool(tx, pool) })
}

// UpdateUserBlock 持久化用户区块更新。
func (k *Keeper) UpdateUserBlock(block *types.UserBlock) error {
	data := mustMarshal(block)
	return k.store.Write(func(tx *store.Tx) error { return tx.PutUser(block.Address, data) })
}

func (k *Keeper) CollectFees(ctx types.Context, feeAmount uint64) error {
	pool := k.GetPool(ctx)
	pool.SalaryRelay += feeAmount * 25 / 100
	pool.SalaryRecord += feeAmount * 25 / 100
	pool.SeedLoanReserve += feeAmount * 20 / 100
	pool.BadDebtReserve += feeAmount * 15 / 100
	pool.Community += feeAmount * 15 / 100
	pool.TotalBalance += feeAmount

	return k.store.Write(func(tx *store.Tx) error { return k.setPool(tx, pool) })
}

// ===== 节点角色 =====

func (k *Keeper) SetNodeRole(ctx types.Context, address string, role types.NodeRole) error {
	chainID := parseChainID(address)
	block := k.GetUserBlock(ctx, chainID)
	if block == nil {
		return fmt.Errorf("user not found: %s", address)
	}
	block.NodeRole = role
	data := mustMarshal(block)
	return k.store.Write(func(tx *store.Tx) error { return tx.PutUser(string(chainID[:]), data) })
}

func (k *Keeper) CreditSalary(ctx types.Context, address string, amount uint64) error {
	chainID := parseChainID(address)
	block := k.GetUserBlock(ctx, chainID)
	if block == nil {
		return fmt.Errorf("user not found: %s", address)
	}
	// 工资计入独立字段，不混入 TotalRepayAmount (防止正反馈循环)
	block.SalaryEarnings += amount
	// CreditLimit 仍可增长 — 节点工作应获得借贷能力
	block.CreditLimit += amount
	data := mustMarshal(block)
	return k.store.Write(func(tx *store.Tx) error { return tx.PutUser(string(chainID[:]), data) })
}

// ===== GetAll =====

func (k *Keeper) GetAllUsers(ctx types.Context) []*types.UserBlock {
	var result []*types.UserBlock
	k.store.Read(func(tx *store.Tx) error {
		return tx.ForEachUser(func(key string, data []byte) error {
			var u types.UserBlock
			if store.HybridUnmarshal(data, &u) == nil {
				u.Address = key // 从 key 恢复 Address
				result = append(result, &u)
			}
			return nil
		})
	})
	return result
}

// ===== 信任权重 =====

// RecalculateTrustWeights 重新计算信任权重（含硬上限防溢出）。
func (k *Keeper) RecalculateTrustWeights(ctx types.Context) {
	const maxTrustWeight uint64 = 1_000_000_000 // 10亿硬上限防 uint64 溢出

	users := k.GetAllUsers(ctx)
	for _, block := range users {
		baseWeight := uint64(float64(block.TotalRepayAmount) * 0.1)
		if baseWeight < 10 {
			baseWeight = 10
		}
		block.TrustWeight = baseWeight
		if block.SeedPhase {
			block.CreditLimit = block.TrustWeight
		} else {
			block.CreditLimit = uint64(float64(block.TotalRepayAmount) * 0.8)
		}
	}
	// 级联更新
	for round := 0; round < 3; round++ {
		changed := false
		userMap := make(map[string]*types.UserBlock)
		for _, u := range users {
			userMap[u.Address] = u
			for _, ref := range u.GuaranteedBy {
				if g := userMap[string(ref.OtherParty)]; g != nil {
					newW := u.TrustWeight + uint64(float64(g.TrustWeight)*0.8)
					if newW > maxTrustWeight {
						newW = maxTrustWeight // 硬上限防溢出
					}
					if newW != u.TrustWeight {
						u.TrustWeight = newW
						changed = true
					}
				}
			}
		}
		if !changed {
			break
		}
	}
	// 持久化
	for _, u := range users {
		addr := u.Address
		data := mustMarshal(u)
		k.store.Write(func(tx *store.Tx) error {
			return tx.PutUser(addr, data)
		})
	}
}

// ===== 递归追偿（延期准备金版本）=====

// ProcessRecursiveLiability 处理递归追偿。
// 使用延期准备金拨备机制，将惩罚从瞬时脉冲展开为基于 Epoch 的分级递进扣除。
//
// 旧行为（已废弃）：瞬间全额扣除担保人 Bond
// 新行为：InitiateProvision → 30% 首扣 → 每 Epoch 递进（70%→100%）
func (k *Keeper) ProcessRecursiveLiability(ctx types.Context, defaulterAddr string, debtAmount uint64) (*DeferredProvision, uint64, error) {
	defaulter := k.GetUserBlock(ctx, parseChainID(defaulterAddr))
	if defaulter == nil {
		return nil, 0, fmt.Errorf("user not found: %s", defaulterAddr)
	}

	// 构建担保人链
	var guarantorChain []string
	bonds := k.ListBonds(ctx)
	for _, bond := range bonds {
		if bond.Status != types.BondStatus_FORFEITED && string(bond.Invitee) == defaulterAddr {
			guarantorChain = append(guarantorChain, string(bond.Inviter))
		}
	}

	// 发起延期追偿
	blockHeight := k.Height()
	prov, initialAmount, err := k.recourse.InitiateProvision(
		defaulterAddr,
		debtAmount,
		guarantorChain,
		blockHeight,
	)
	if err != nil {
		return nil, 0, fmt.Errorf("initiate provision: %w", err)
	}

	// 即时扣除首笔金额（30%）
	if initialAmount > 0 {
		if err := k.applyProvisionDeduction(ctx, defaulterAddr, initialAmount); err != nil {
			return prov, initialAmount, fmt.Errorf("apply initial deduction: %w", err)
		}
	}

	log.Printf("[recourse] deferred provision for %s: debt=%d initial_deduct=%d remaining=%d epoch=%d",
		defaulterAddr[:8], debtAmount, initialAmount, prov.RemainingAmount,
		k.recourse.CurrentEpoch())

	return prov, initialAmount, nil
}

// ProcessEpochProvisions 处理所有活跃追偿的 Epoch 递进扣除。
// 应在 EndBlocker 中每 Epoch 调用一次。
func (k *Keeper) ProcessEpochProvisions(ctx types.Context) []ProvisionAction {
	blockHeight := k.Height()
	actions := k.recourse.ProcessEpochProvision(blockHeight)

	for _, action := range actions {
		k.applyProvisionDeduction(ctx, action.TargetNode, action.Amount)
	}

	return actions
}

// applyProvisionDeduction 对目标节点应用扣除。
func (k *Keeper) applyProvisionDeduction(ctx types.Context, targetAddr string, amount uint64) error {
	target := k.GetUserBlock(ctx, parseChainID(targetAddr))
	if target == nil {
		return fmt.Errorf("target not found: %s", targetAddr)
	}

	// 优先从 CreditLimit 扣除
	if target.CreditLimit >= amount {
		target.CreditLimit -= amount
	} else {
		deducted := target.CreditLimit
		target.CreditLimit = 0
		remaining := amount - deducted

		// 防御性检查：防止 TrustWeight=0 导致除零 panic
		if target.TrustWeight < 1 {
			target.TrustWeight = 1
		}
		penalty := remaining * 10 / target.TrustWeight
		if penalty > 100 {
			penalty = 100
		}
		target.TrustWeight = target.TrustWeight * (100 - penalty) / 100
		if target.TrustWeight < 1 {
			target.TrustWeight = 1
		}
	}

	return k.UpdateUserBlock(target)
}

// ProcessProvisionAppeal 处理追偿申诉。
func (k *Keeper) ProcessProvisionAppeal(provisionID, evidence string) error {
	blockHeight := k.Height()
	return k.recourse.SubmitAppeal(provisionID, evidence, blockHeight)
}

// ResolveProvisionAppeal 处理申诉裁决。
func (k *Keeper) ResolveProvisionAppeal(provisionID string, upheld bool) error {
	return k.recourse.ResolveAppeal(provisionID, upheld)
}

// CurrentEpoch 返回当前 Epoch。
func (k *Keeper) CurrentEpoch() int64 {
	return k.recourse.CurrentEpoch()
}

// ===== 旧版递归追偿（保留向后兼容）=====

// ProcessRecursiveLiabilityInstant 原始的即时全额追偿（已废弃，保留用于测试对比）。
func (k *Keeper) ProcessRecursiveLiabilityInstant(ctx types.Context, defaulterAddr string, debtAmount uint64) error {
	defaulter := k.GetUserBlock(ctx, parseChainID(defaulterAddr))
	if defaulter == nil {
		return fmt.Errorf("user not found: %s", defaulterAddr)
	}

	remaining := debtAmount
	if defaulter.CreditLimit >= remaining {
		defaulter.CreditLimit -= remaining
		defaulter.TrustWeight = defaulter.TrustWeight * 9 / 10
		data := mustMarshal(defaulter)
		return k.store.Write(func(tx *store.Tx) error { return tx.PutUser(defaulter.Address, data) })
	}
	remaining -= defaulter.CreditLimit
	defaulter.CreditLimit = 0
	defaulter.TrustWeight = 0
	data := mustMarshal(defaulter)
	k.store.Write(func(tx *store.Tx) error { tx.PutUser(defaulter.Address, data); return nil })

	// 递归追偿担保人
	for depth := 0; depth < 5 && remaining > 0; depth++ {
		bonds := k.ListBonds(ctx)
		recovered := false
		for _, bond := range bonds {
			if bond.Status != types.BondStatus_FORFEITED || string(bond.Invitee) != defaulterAddr {
				continue
			}
			guarantor := k.GetUserBlock(ctx, parseChainID(string(bond.Inviter)))
			if guarantor == nil {
				continue
			}
			if bond.TotalBond >= remaining {
				bond.TotalBond -= remaining
				remaining = 0
			} else {
				remaining -= bond.TotalBond
				bond.TotalBond = 0
			}
			bd := mustMarshal(bond)
			k.store.Write(func(tx *store.Tx) error { return tx.PutBond(bond.BondId, bd) })

			if remaining > 0 && guarantor.CreditLimit > 0 {
				if guarantor.CreditLimit >= remaining {
					guarantor.CreditLimit -= remaining
					remaining = 0
				} else {
					remaining -= guarantor.CreditLimit
					guarantor.CreditLimit = 0
				}
				guarantor.TrustWeight = guarantor.TrustWeight * 8 / 10
			}
			gd := mustMarshal(guarantor)
			k.store.Write(func(tx *store.Tx) error { return tx.PutUser(guarantor.Address, gd) })
			defaulterAddr = guarantor.Address
			recovered = true
			break
		}
		if !recovered {
			break
		}
	}

	if remaining > 0 {
		pool := k.GetPool(ctx)
		if pool.BadDebtReserve >= remaining {
			pool.BadDebtReserve -= remaining
			pool.TotalBalance -= remaining
			k.store.Write(func(tx *store.Tx) error { return k.setPool(tx, pool) })
			return nil
		}
		return fmt.Errorf("recursive liability: unable to cover %d of %d debt", remaining, debtAmount)
	}
	return nil
}

// ===== 帮助函数（未依赖内存）=====

func (k *Keeper) addBondRef(ctx types.Context, inviter, invitee, bondID string) {
	iBlock := k.GetUserBlock(ctx, parseChainID(inviter))
	eBlock := k.GetUserBlock(ctx, parseChainID(invitee))
	if iBlock != nil {
		iBlock.GuaranteedOf = append(iBlock.GuaranteedOf, &types.BondRef{BondId: bondID, OtherParty: []byte(invitee)})
		d := mustMarshal(iBlock)
		k.store.Write(func(tx *store.Tx) error { return tx.PutUser(iBlock.Address, d) })
	}
	if eBlock != nil {
		eBlock.GuaranteedBy = append(eBlock.GuaranteedBy, &types.BondRef{BondId: bondID, OtherParty: []byte(inviter)})
		d := mustMarshal(eBlock)
		k.store.Write(func(tx *store.Tx) error { return tx.PutUser(eBlock.Address, d) })
	}
}

// ===== 持久化的 Height 管理 =====

func (k *Keeper) SetHeight(h int64) {
	k.store.Write(func(tx *store.Tx) error { return tx.SetHeight(h) })
}

func (k *Keeper) Height() int64 {
	var h int64
	k.store.Read(func(tx *store.Tx) error { h = tx.Height(); return nil })
	return h
}

// ===== 基础帮助函数 =====

func deriveChainID(sendPk []byte) types.ChainID { return sha256.Sum256(sendPk) }

func generateBondID(inviter, invitee string) string {
	return fmt.Sprintf("%s-%s", inviter[:16], invitee[:16])
}

func calculateTotalBond(commitments [][]byte) uint64 {
	// 每笔承诺贡献信用额度 = 100 × len(commitments)（简化模型）
	// 生产环境：每笔承诺应从信用票据面额计算
	return uint64(len(commitments)) * 100
}

func extractGuarantorIDs(sigs [][]byte) [][]byte {
	// 从签名中提取担保人 chain_id（签名前32字节包含担保人信息）
	// 生产环境：应使用 Ed25519 公钥恢复或从链上查询
	return sigs
}

func parseChainID(addr string) types.ChainID {
	var id types.ChainID
	copy(id[:], []byte(addr))
	return id
}

// ===== 邀请凭证 =====

// inviteCred 解析后的邀请凭证（与 neko-invite 工具格式兼容）
type inviteCred struct {
	InviterChainID types.ChainID
	InviterSendPK  [32]byte
	InviteeSendPK  [32]byte
	InviteeRecvPK  [32]byte
	Timestamp      int64
	ExpiresAt      int64
	SeedCredit     uint64
	Signature      []byte
}

func parseInviteCred(data []byte) (*inviteCred, error) {
	if len(data) < 144 {
		return nil, fmt.Errorf("credential too short: %d", len(data))
	}
	c := &inviteCred{}
	copy(c.InviterChainID[:], data[0:32])
	copy(c.InviterSendPK[:], data[32:64])
	copy(c.InviteeSendPK[:], data[64:96])
	copy(c.InviteeRecvPK[:], data[96:128])
	c.Timestamp = int64(data[128])<<56 | int64(data[129])<<48 | int64(data[130])<<40 | int64(data[131])<<32 |
		int64(data[132])<<24 | int64(data[133])<<16 | int64(data[134])<<8 | int64(data[135])
	c.ExpiresAt = int64(data[136])<<56 | int64(data[137])<<48 | int64(data[138])<<40 | int64(data[139])<<32 |
		int64(data[140])<<24 | int64(data[141])<<16 | int64(data[142])<<8 | int64(data[143])
	c.SeedCredit = uint64(data[144])<<56 | uint64(data[145])<<48 | uint64(data[146])<<40 | uint64(data[147])<<32 |
		uint64(data[148])<<24 | uint64(data[149])<<16 | uint64(data[150])<<8 | uint64(data[151])
	if len(data) >= 216 {
		c.Signature = make([]byte, 64)
		copy(c.Signature, data[152:216])
	}
	return c, nil
}

func (c *inviteCred) serializeForSigning() []byte {
	buf := make([]byte, 152)
	copy(buf[0:32], c.InviterChainID[:])
	copy(buf[32:64], c.InviterSendPK[:])
	copy(buf[64:96], c.InviteeSendPK[:])
	copy(buf[96:128], c.InviteeRecvPK[:])
	buf[128] = byte(c.Timestamp >> 56)
	buf[129] = byte(c.Timestamp >> 48)
	buf[130] = byte(c.Timestamp >> 40)
	buf[131] = byte(c.Timestamp >> 32)
	buf[132] = byte(c.Timestamp >> 24)
	buf[133] = byte(c.Timestamp >> 16)
	buf[134] = byte(c.Timestamp >> 8)
	buf[135] = byte(c.Timestamp)
	buf[136] = byte(c.ExpiresAt >> 56)
	buf[137] = byte(c.ExpiresAt >> 48)
	buf[138] = byte(c.ExpiresAt >> 40)
	buf[139] = byte(c.ExpiresAt >> 32)
	buf[140] = byte(c.ExpiresAt >> 24)
	buf[141] = byte(c.ExpiresAt >> 16)
	buf[142] = byte(c.ExpiresAt >> 8)
	buf[143] = byte(c.ExpiresAt)
	buf[144] = byte(c.SeedCredit >> 56)
	buf[145] = byte(c.SeedCredit >> 48)
	buf[146] = byte(c.SeedCredit >> 40)
	buf[147] = byte(c.SeedCredit >> 32)
	buf[148] = byte(c.SeedCredit >> 24)
	buf[149] = byte(c.SeedCredit >> 16)
	buf[150] = byte(c.SeedCredit >> 8)
	buf[151] = byte(c.SeedCredit)
	return buf
}

func ed25519Verify(pubKey [32]byte, message, sig []byte) bool {
	if len(sig) != 64 {
		return false
	}
	return ed25519.Verify(pubKey[:], message, sig)
}

func bytesEqual(a, b []byte) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// mustMarshal 二进制序列化（优先 gob，回退 JSON）并在失败时 panic。
func mustMarshal(v interface{}) []byte {
	data, err := store.HybridMarshal(v)
	if err != nil {
		panic(fmt.Sprintf("brightchain: marshal failed for %T: %v", v, err))
	}
	return data
}

// mustUnmarshal 二进制反序列化（自动检测 gob/JSON 格式）。
func mustUnmarshal(data []byte, v interface{}) error {
	return store.HybridUnmarshal(data, v)
}

var _ = types.ModuleName
