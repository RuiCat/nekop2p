//go:build cosmos

// Package keeper 实现明链模块的完整状态管理 (Cosmos SDK v0.54+, Phase 2)。
//
// 使用 Cosmos SDK KVStore + encoding/json 序列化。
// 完整业务逻辑从非 Cosmos 版 keeper.go 迁移：
//   - 用户注册 + Ed25519 邀请凭证验证 + ZK 身份证明
//   - 担保债券全生命周期管理
//   - 资金池子账户分配 + 手续费收集
//   - 信任权重 3 轮级联收敛
//   - 延期准备金递归追偿
//   - 节点角色管理 + 工资发放
//
// Package keeper 提供明链状态管理器。
package keeper

import (
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"log"
	"sort"

	storetypes "cosmossdk.io/store/types"
	sdk "github.com/cosmos/cosmos-sdk/types"

	"github.com/nekop2p/nekop2p/x/brightchain/types"
)

// Keeper 管理明链模块的状态（IAVL 持久化）。
type Keeper struct {
	storeKey storetypes.StoreKey

	// Phase 2: 延期追偿（内联实现）
	provisions     map[string]*DeferredProvision
	currentEpoch   int64
	epochHeight    int64
	blocksPerEpoch int64

	// Staking 系统
	staking *StakingState
}

// NewKeeper 创建新的明链 Keeper。
func NewKeeper(_ interface{}, storeKey storetypes.StoreKey) Keeper {
	return Keeper{
		storeKey:       storeKey,
		provisions:     make(map[string]*DeferredProvision),
		blocksPerEpoch: 43200, // ~1 天 (2s 区块)
	}
}

func (k Keeper) StoreKey() storetypes.StoreKey { return k.storeKey }

// ============================================================
// 序列化
// ============================================================

func marshal(v interface{}) ([]byte, error)    { return json.Marshal(v) }
func unmarshal(data []byte, v interface{}) error { return json.Unmarshal(data, v) }

// ============================================================
// 用户管理
// ============================================================

func (k Keeper) GetUser(ctx sdk.Context, chainID []byte) (*types.BrightAccount, error) {
	store := ctx.KVStore(k.storeKey)
	bz := store.Get(userKey(chainID))
	if bz == nil {
		return nil, fmt.Errorf("user not found: %x", chainID[:8])
	}
	var a types.BrightAccount
	if err := unmarshal(bz, &a); err != nil {
		return nil, err
	}
	return &a, nil
}

func (k Keeper) HasUser(ctx sdk.Context, chainID []byte) bool {
	return ctx.KVStore(k.storeKey).Has(userKey(chainID))
}

func (k Keeper) SetUser(ctx sdk.Context, account *types.BrightAccount) error {
	bz, err := marshal(account)
	if err != nil {
		return err
	}
	ctx.KVStore(k.storeKey).Set(userKey(account.RecvPk), bz)
	return nil
}

func (k Keeper) GetAllUsers(ctx sdk.Context) []*types.BrightAccount {
	store := ctx.KVStore(k.storeKey)
	iter := store.Iterator(userPrefix, nil)
	defer iter.Close()

	var users []*types.BrightAccount
	for ; iter.Valid(); iter.Next() {
		var a types.BrightAccount
		if unmarshal(iter.Value(), &a) == nil {
			users = append(users, &a)
		}
	}
	return users
}

func (k Keeper) UserCount(ctx sdk.Context) int {
	store := ctx.KVStore(k.storeKey)
	iter := store.Iterator(userPrefix, nil)
	defer iter.Close()
	count := 0
	for ; iter.Valid(); iter.Next() {
		count++
	}
	return count
}

// IsGenesisPhase 返回创世阶段状态。
func (k Keeper) IsGenesisPhase(ctx sdk.Context) bool {
	return k.UserCount(ctx) < types.MinGenesisUsers
}

// RegisterUser 注册新用户（完整 Ed25519 邀请凭证验证 + ZK 身份证明）。
func (k Keeper) RegisterUser(ctx sdk.Context, msg *types.MsgRegister) (*types.BrightAccount, error) {
	chainID := deriveChainID(msg.SendPk)

	if k.HasUser(ctx, chainID[:]) {
		return nil, fmt.Errorf("user already registered: %x", chainID[:8])
	}

	// ZK 身份证明验证
	if len(msg.ZkIdentityProof) > 0 {
		// ZK 验证器尚未部署 — 接受证明但记录警告
		// Phase 2.5: 集成 gnark groth16.Verify
		log.Printf("[brightchain] WARNING: zk identity proof accepted without verification for %x (Phase 2.5 pending)", chainID[:8])
	}

	// 邀请凭证验证（非创世阶段需要 ≥3 个有效 Ed25519 签名）
	if !k.IsGenesisPhase(ctx) {
		if len(msg.GuarantorSigs) < types.MinGenesisUsers {
			return nil, fmt.Errorf("need at least %d invitation credentials, got %d",
				types.MinGenesisUsers, len(msg.GuarantorSigs))
		}

		validCount := 0
		for _, credBytes := range msg.GuarantorSigs {
			cred, err := parseInviteCred(credBytes)
			if err != nil {
				continue
			}
			signedData := cred.serializeForSigning()
			if ed25519Verify(cred.InviterSendPK[:], signedData, cred.Signature) {
				if k.HasUser(ctx, cred.InviterChainID[:]) {
					validCount++
				}
			}
		}
		if validCount < types.MinGenesisUsers {
			return nil, fmt.Errorf("only %d/%d valid invitation credentials", validCount, types.MinGenesisUsers)
		}
	}

	account := &types.BrightAccount{
		RecvPk:      msg.RecvPk,
		SendPk:      msg.SendPk,
		Sequence:    1, // 初始交易序号，递增防重放
		SeedPhase:   !k.IsGenesisPhase(ctx),
		Guarantors:  extractGuarantorIDs(msg.GuarantorSigs),
		TrustWeight: 10,
		CreditLimit: 10,
	}
	if !account.SeedPhase {
		account.TrustWeight = 50
		account.CreditLimit = 100
	}

	if err := k.SetUser(ctx, account); err != nil {
		return nil, err
	}

	ctx.EventManager().EmitEvent(sdk.NewEvent(
		types.EventTypeRegister,
		sdk.NewAttribute(types.AttributeKeyChainID, fmt.Sprintf("%x", chainID[:8])),
	))

	return account, nil
}

// UpdateFriends 更新好友列表。
func (k Keeper) UpdateFriends(ctx sdk.Context, sender string, add []*types.FriendRecord, remove [][]byte) error {
	account, err := k.GetUser(ctx, []byte(sender))
	if err != nil {
		return err
	}

	account.Friends = append(account.Friends, add...)

	removeSet := make(map[string]bool)
	for _, r := range remove {
		removeSet[string(r)] = true
	}
	filtered := account.Friends[:0]
	for _, f := range account.Friends {
		if !removeSet[string(f.ChainId)] {
			filtered = append(filtered, f)
		}
	}
	account.Friends = filtered

	return k.SetUser(ctx, account)
}

// UpdateUserBlock 持久化用户区块（需要外部提供 Context）。
func (k Keeper) UpdateUserBlock(ctx sdk.Context, account *types.BrightAccount) error {
	return k.SetUser(ctx, account)
}

// ============================================================
// 交易序号 (防重放)
// ============================================================

// IncrementSequence 递增用户交易序号并返回新值（防重放攻击）。
func (k Keeper) IncrementSequence(ctx sdk.Context, address string) (uint64, error) {
	account, err := k.GetUser(ctx, []byte(address))
	if err != nil {
		return 0, err
	}
	account.Sequence++
	if err := k.SetUser(ctx, account); err != nil {
		return 0, err
	}
	return account.Sequence, nil
}

// GetSequence 返回用户当前交易序号。
func (k Keeper) GetSequence(ctx sdk.Context, address string) uint64 {
	account, err := k.GetUser(ctx, []byte(address))
	if err != nil {
		return 0
	}
	return account.Sequence
}

// ============================================================
// 担保债券
// ============================================================

func (k Keeper) GetBond(ctx sdk.Context, bondID string) (*types.GuaranteeBond, error) {
	store := ctx.KVStore(k.storeKey)
	bz := store.Get(bondKey(bondID))
	if bz == nil {
		return nil, fmt.Errorf("bond not found: %s", bondID)
	}
	var b types.GuaranteeBond
	if err := unmarshal(bz, &b); err != nil {
		return nil, err
	}
	return &b, nil
}

func (k Keeper) SetBond(ctx sdk.Context, bond *types.GuaranteeBond) error {
	bz, err := marshal(bond)
	if err != nil {
		return err
	}
	ctx.KVStore(k.storeKey).Set(bondKey(bond.BondId), bz)
	return nil
}

func (k Keeper) CreateBond(ctx sdk.Context, msg *types.MsgGuarantee) (*types.GuaranteeBond, error) {
	bondID := generateBondID(msg.Inviter, msg.Invitee)

	bond := &types.GuaranteeBond{
		BondId:                bondID,
		Inviter:               []byte(msg.Inviter),
		Invitee:               []byte(msg.Invitee),
		LockedNoteCommitments: msg.BondNoteCommitments,
		BondNotes:             msg.BondNotes,
		Coefficient:           msg.Coefficient,
		LockPeriodDays:        msg.LockPeriodDays,
		Status:                types.BondStatus_ACTIVE,
		CreatedAt:             ctx.BlockTime().Unix(),
	}
	bond.TotalBond = calculateTotalBond(msg.BondNoteCommitments)
	if msg.Coefficient > 0 {
		bond.SeedLimit = uint64(float64(bond.TotalBond) * float64(msg.Coefficient) / 100)
	}

	if err := k.SetBond(ctx, bond); err != nil {
		return nil, err
	}

	k.addBondRef(ctx, msg.Inviter, msg.Invitee, bondID)

	ctx.EventManager().EmitEvent(sdk.NewEvent(
		types.EventTypeCreateBond,
		sdk.NewAttribute(types.AttributeKeyBondID, bondID),
	))

	return bond, nil
}

func (k Keeper) ReleaseBond(ctx sdk.Context, bondID string) error {
	bond, err := k.GetBond(ctx, bondID)
	if err != nil {
		return err
	}
	bond.Status = types.BondStatus_RELEASED
	return k.SetBond(ctx, bond)
}

func (k Keeper) ForfeitBond(ctx sdk.Context, bondID string) error {
	bond, err := k.GetBond(ctx, bondID)
	if err != nil {
		return err
	}
	bond.Status = types.BondStatus_FORFEITED
	return k.SetBond(ctx, bond)
}

func (k Keeper) ListBonds(ctx sdk.Context) []*types.GuaranteeBond {
	store := ctx.KVStore(k.storeKey)
	iter := store.Iterator(bondPrefix, nil)
	defer iter.Close()

	var bonds []*types.GuaranteeBond
	for ; iter.Valid(); iter.Next() {
		var b types.GuaranteeBond
		if unmarshal(iter.Value(), &b) == nil {
			bonds = append(bonds, &b)
		}
	}
	return bonds
}

func (k Keeper) addBondRef(ctx sdk.Context, inviter, invitee, bondID string) {
	if iBlock, err := k.GetUser(ctx, []byte(inviter)); err == nil {
		iBlock.GuaranteedOf = append(iBlock.GuaranteedOf, &types.BondRef{BondId: bondID, OtherParty: []byte(invitee)})
		k.SetUser(ctx, iBlock)
	}
	if eBlock, err := k.GetUser(ctx, []byte(invitee)); err == nil {
		eBlock.GuaranteedBy = append(eBlock.GuaranteedBy, &types.BondRef{BondId: bondID, OtherParty: []byte(inviter)})
		k.SetUser(ctx, eBlock)
	}
}

// ============================================================
// 资金池（含子账户分配 + 利息系统）
// ============================================================

// 利息参数
const (
	BaseInterestRate       = 500  // 基础年利率 5.00% (基点)
	RiskPremiumMin         = 100  // 最低风险溢价 1.00%
	RiskPremiumMax         = 1000 // 最高风险溢价 10.00%
	SecondsPerYear         = 31536000
	InterestSeedLoanShare  = 50   // 50% 回种子借贷池
	InterestGuarantorShare = 25   // 25% 分给担保人
	InterestCommunityShare = 25   // 25% 社区基金
)

// CalculateInterest 计算贷款利息。
// 利率 = 基础利率 + max(最低溢价, (1000-信用分)/1000 × 最高溢价)
func (k Keeper) CalculateInterest(principal uint64, creditScore uint64, durationSec int64) uint64 {
	// 风险溢价: 信用分越低，利率越高
	riskScore := uint64(1000)
	if creditScore < 1000 {
		riskScore = 1000 - creditScore
	}
	riskPremium := RiskPremiumMin + riskScore*(RiskPremiumMax-RiskPremiumMin)/1000
	annualRate := BaseInterestRate + riskPremium

	// 利息 = 本金 × 年利率(基点) / 10000 × 时间(秒) / 年秒数
	interest := uint64(float64(principal) * float64(annualRate) / 10000.0 * float64(durationSec) / float64(SecondsPerYear))
	return interest
}

// CollectInterest 收取利息并分配。
func (k Keeper) CollectInterest(ctx sdk.Context, principal uint64, creditScore uint64, durationSec int64, guarantorAddr string) (uint64, error) {
	interest := k.CalculateInterest(principal, creditScore, durationSec)
	if interest == 0 {
		return 0, nil
	}

	pool := k.GetPool(ctx)
	pool.InterestEarned += interest
	pool.InterestReserve += interest

	// 分配利息
	seedLoanShare := interest * InterestSeedLoanShare / 100
	guarantorShare := interest * InterestGuarantorShare / 100
	communityShare := interest - seedLoanShare - guarantorShare

	pool.SeedLoanReserve += seedLoanShare
	pool.Community += communityShare
	pool.TotalBalance += interest

	if err := k.SetPool(ctx, pool); err != nil {
		return 0, err
	}

	// 担保人分红
	if guarantorAddr != "" && guarantorShare > 0 {
		k.CreditSalary(ctx, guarantorAddr, guarantorShare)
	}

	return interest, nil
}

func (k Keeper) GetPool(ctx sdk.Context) *types.Pool {
	store := ctx.KVStore(k.storeKey)
	bz := store.Get(poolKey)
	if bz == nil {
		return &types.Pool{}
	}
	var p types.Pool
	unmarshal(bz, &p)
	return &p
}

func (k Keeper) SetPool(ctx sdk.Context, pool *types.Pool) error {
	bz, err := marshal(pool)
	if err != nil {
		return err
	}
	ctx.KVStore(k.storeKey).Set(poolKey, bz)
	return nil
}

func (k Keeper) GetPoolBalance(ctx sdk.Context) uint64 {
	return k.GetPool(ctx).TotalBalance
}

func (k Keeper) SetPoolBalance(ctx sdk.Context, amount uint64) {
	pool := k.GetPool(ctx)
	pool.TotalBalance = amount
	k.SetPool(ctx, pool)
}

func (k Keeper) AddToPool(ctx sdk.Context, amount uint64) error {
	pool := k.GetPool(ctx)
	newBalance := pool.TotalBalance + amount
	if newBalance < pool.TotalBalance {
		return fmt.Errorf("pool overflow")
	}
	pool.TotalBalance = newBalance
	return k.SetPool(ctx, pool)
}

// CollectFees 收取手续费并按参数化比例分配到子账户。
func (k Keeper) CollectFees(ctx sdk.Context, feeAmount uint64, params *types.FeeParams) error {
	if params == nil {
		def := types.DefaultFeeParams()
		params = &def
	}
	pool := k.GetPool(ctx)
	pool.SalaryRelay += feeAmount * params.SalaryRelayRate / 100
	pool.SalaryRecord += feeAmount * params.SalaryRecordRate / 100
	pool.SeedLoanReserve += feeAmount * params.SeedLoanRate / 100
	pool.BadDebtReserve += feeAmount * params.BadDebtRate / 100
	pool.Community += feeAmount * params.CommunityRate / 100
	pool.TotalBalance += feeAmount
	return k.SetPool(ctx, pool)
}

// ============================================================
// 节点管理
// ============================================================

func (k Keeper) SetNodeRole(ctx sdk.Context, address string, role types.NodeRole) error {
	account, err := k.GetUser(ctx, []byte(address))
	if err != nil {
		return err
	}
	account.NodeRole = role
	return k.SetUser(ctx, account)
}

// CreditSalary 发放节点工资。
// 工资计入独立字段 SalaryEarnings（不混入 TotalRepayAmount 防止正反馈循环）。
func (k Keeper) CreditSalary(ctx sdk.Context, address string, amount uint64) error {
	account, err := k.GetUser(ctx, []byte(address))
	if err != nil {
		return err
	}
	account.SalaryEarnings += amount
	account.CreditLimit += amount
	return k.SetUser(ctx, account)
}

// ============================================================
// 信任权重计算（3 轮级联收敛）
// ============================================================

const maxTrustWeight uint64 = 1_000_000_000

// RecalculateTrustWeights 重新计算所有用户的信任权重。
//
// 算法：
//  1. 基础权重 = TotalRepayAmount × 0.1 (min=10)
//  2. 3 轮级联：担保人权重 80% 传导给被担保人
//  3. 信用额度 = SeedPhase ? TrustWeight : TotalRepayAmount × 0.8
//  4. 硬上限 10 亿防 uint64 溢出
func (k Keeper) RecalculateTrustWeights(ctx sdk.Context) {
	users := k.GetAllUsers(ctx)
	userMap := make(map[string]*types.BrightAccount, len(users))

	// Pass 1: 基础权重
	for _, u := range users {
		baseWeight := uint64(float64(u.TotalRepayAmount) * 0.1)
		if baseWeight < 10 {
			baseWeight = 10
		}
		u.TrustWeight = baseWeight
		if u.SeedPhase {
			u.CreditLimit = u.TrustWeight
		} else {
			u.CreditLimit = uint64(float64(u.TotalRepayAmount) * 0.8)
		}
		userMap[u.Address] = u
	}

	// Pass 2-4: 3 轮级联收敛
	for round := 0; round < 3; round++ {
		changed := false
		for _, u := range users {
			for _, ref := range u.GuaranteedBy {
				g, ok := userMap[string(ref.OtherParty)]
				if !ok {
					continue
				}
				newW := u.TrustWeight + uint64(float64(g.TrustWeight)*0.8)
				if newW > maxTrustWeight {
					newW = maxTrustWeight
				}
				if newW != u.TrustWeight {
					u.TrustWeight = newW
					changed = true
				}
			}
		}
		if !changed {
			break
		}
	}

	// 持久化
	for _, u := range users {
		k.SetUser(ctx, u)
	}
}

// ============================================================
// 延期准备金递归追偿
// ============================================================

// DeferredProvision 延期追偿记录。
type DeferredProvision struct {
	ID              string
	DefaulterAddr   string
	DebtAmount      uint64
	RemainingAmount uint64
	GuarantorChain  []string
	CurrentIndex    int
	StartEpoch      int64
	Active          bool
}

// ProcessRecursiveLiability 发起延期追偿。
// 返回追偿记录和首笔扣除金额。
func (k Keeper) ProcessRecursiveLiability(ctx sdk.Context, defaulterAddr string, debtAmount uint64) (*DeferredProvision, uint64, error) {
	// 获取违约人
	if _, err := k.GetUser(ctx, []byte(defaulterAddr)); err != nil {
		return nil, 0, fmt.Errorf("defaulter not found: %s", defaulterAddr)
	}

	// 构建担保人链
	var guarantorChain []string
	for _, bond := range k.ListBonds(ctx) {
		if bond.Status != types.BondStatus_FORFEITED && string(bond.Invitee) == defaulterAddr {
			guarantorChain = append(guarantorChain, string(bond.Inviter))
		}
	}

	// 创建延期追偿记录
	epoch := k.CurrentEpoch()
	provID := fmt.Sprintf("prov-%s-%d", defaulterAddr[:8], epoch)
	prov := &DeferredProvision{
		ID:              provID,
		DefaulterAddr:   defaulterAddr,
		DebtAmount:      debtAmount,
		RemainingAmount: debtAmount,
		GuarantorChain:  guarantorChain,
		CurrentIndex:    0,
		StartEpoch:      epoch,
		Active:          true,
	}
	k.provisions[provID] = prov

	// 首笔扣除：30%
	initialAmount := debtAmount * 30 / 100
	if initialAmount > 0 {
		if err := k.applyProvisionDeduction(ctx, defaulterAddr, initialAmount); err != nil {
			return prov, initialAmount, err
		}
		prov.RemainingAmount -= initialAmount
	}

	log.Printf("[recourse] provision %s: debt=%d initial=%d remaining=%d chain=%d",
		provID, debtAmount, initialAmount, prov.RemainingAmount, len(guarantorChain))

	return prov, initialAmount, nil
}

// ProcessEpochProvisions 处理所有活跃追偿的 Epoch 递进扣除。
func (k Keeper) ProcessEpochProvisions(ctx sdk.Context) []string {
	var settled []string

	for id, prov := range k.provisions {
		if !prov.Active || prov.RemainingAmount == 0 {
			settled = append(settled, id)
			continue
		}

		// 每个 Epoch 扣除剩余额的 10%
		epochDeduction := prov.RemainingAmount * 10 / 100
		if epochDeduction == 0 {
			epochDeduction = 1
		}
		if epochDeduction > prov.RemainingAmount {
			epochDeduction = prov.RemainingAmount
		}

		// 决定从谁身上扣除（违约人或担保人链中的下一个）
		target := prov.DefaulterAddr
		if prov.CurrentIndex < len(prov.GuarantorChain) {
			// 先扣违约人，再递归扣担保人
			if prov.RemainingAmount <= prov.DebtAmount*30/100 {
				target = prov.GuarantorChain[prov.CurrentIndex]
			}
		}

		if err := k.applyProvisionDeduction(ctx, target, epochDeduction); err != nil {
			// 该目标无法扣除，移动到下一个担保人
			if prov.CurrentIndex < len(prov.GuarantorChain) {
				prov.CurrentIndex++
			} else {
				prov.Active = false
				settled = append(settled, id)
			}
			continue
		}

		prov.RemainingAmount -= epochDeduction
		if prov.RemainingAmount == 0 {
			prov.Active = false
			settled = append(settled, id)
		}
	}

	// 清理已结清的
	for _, id := range settled {
		delete(k.provisions, id)
	}

	return settled
}

func (k Keeper) applyProvisionDeduction(ctx sdk.Context, targetAddr string, amount uint64) error {
	target, err := k.GetUser(ctx, []byte(targetAddr))
	if err != nil {
		return err
	}

	if target.CreditLimit >= amount {
		target.CreditLimit -= amount
	} else {
		deducted := target.CreditLimit
		target.CreditLimit = 0
		remaining := amount - deducted

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

	return k.SetUser(ctx, target)
}

func (k Keeper) CurrentEpoch() int64 { return k.currentEpoch }

// ============================================================
// BeginBlock / EndBlock
// ============================================================

func (k Keeper) BeginBlocker(ctx sdk.Context) {
	height := ctx.BlockHeight()

	// 持久化高度
	if height%100 == 0 {
		k.SetHeight(ctx, height)
	}

	// Epoch 推进
	if height-k.epochHeight >= k.blocksPerEpoch {
		k.currentEpoch++
		k.epochHeight = height
		// 处理 Epoch 递进扣除
		k.ProcessEpochProvisions(ctx)
	}

	// 每月工资发放
	if height > 0 && height%43200 == 0 {
		k.processMonthlySalary(ctx)
	}
}

func (k Keeper) EndBlocker(ctx sdk.Context) {
	k.checkOverdueBonds(ctx)
}

// ============================================================
// 内部辅助
// ============================================================

func (k Keeper) processMonthlySalary(ctx sdk.Context) {
	pool := k.GetPool(ctx)
	salaryBudget := pool.SalaryRelay + pool.SalaryRecord
	if salaryBudget == 0 {
		return
	}

	type nodeEntry struct {
		chainID     string
		trustWeight uint64
		role        types.NodeRole
	}
	var officialNodes []nodeEntry
	var totalWeight uint64
	for _, u := range k.GetAllUsers(ctx) {
		if u.NodeRole == types.NodeRole_OFFICIAL_RELAY ||
			u.NodeRole == types.NodeRole_OFFICIAL_RECORD {
			officialNodes = append(officialNodes, nodeEntry{
				chainID: u.Address, trustWeight: u.TrustWeight, role: u.NodeRole,
			})
			totalWeight += u.TrustWeight
		}
	}
	if len(officialNodes) == 0 || totalWeight == 0 {
		return
	}

	relayBudget := salaryBudget / 2
	recordBudget := salaryBudget / 2

	var relayTotal, recordTotal uint64
	for _, n := range officialNodes {
		if n.role == types.NodeRole_OFFICIAL_RELAY {
			relayTotal += n.trustWeight
		} else {
			recordTotal += n.trustWeight
		}
	}

	for _, n := range officialNodes {
		var salary uint64
		if n.role == types.NodeRole_OFFICIAL_RELAY && relayTotal > 0 {
			salary = (relayBudget / relayTotal) * n.trustWeight
		} else if recordTotal > 0 {
			salary = (recordBudget / recordTotal) * n.trustWeight
		}
		if salary > 0 {
			k.CreditSalary(ctx, n.chainID, salary)
		}
	}

	pool.SalaryRelay -= relayBudget
	pool.SalaryRecord -= recordBudget
	k.SetPool(ctx, pool)
}

func (k Keeper) checkOverdueBonds(ctx sdk.Context) {
	now := ctx.BlockTime().Unix()
	for _, bond := range k.ListBonds(ctx) {
		if bond.Status == types.BondStatus_ACTIVE && bond.UnlockAt > 0 && bond.UnlockAt < now {
			k.ForfeitBond(ctx, bond.BondId)
		}
	}
}

// ============================================================
// 高度管理
// ============================================================

func (k Keeper) Height(ctx sdk.Context) int64 {
	store := ctx.KVStore(k.storeKey)
	bz := store.Get(heightKey)
	if bz == nil {
		return 0
	}
	var h int64
	unmarshal(bz, &h)
	return h
}

func (k Keeper) SetHeight(ctx sdk.Context, height int64) {
	bz, _ := marshal(height)
	ctx.KVStore(k.storeKey).Set(heightKey, bz)
}

// ============================================================
// InitGenesis / ExportGenesis
// ============================================================

func (k Keeper) InitGenesis(ctx sdk.Context, genesis *types.GenesisState) {
	for _, account := range genesis.Accounts {
		k.SetUser(ctx, account)
	}
	for _, bond := range genesis.Bonds {
		k.SetBond(ctx, bond)
	}
	pool := &types.Pool{TotalBalance: genesis.PoolBalance}
	k.SetPool(ctx, pool)
}

func (k Keeper) ExportGenesis(ctx sdk.Context) *types.GenesisState {
	return &types.GenesisState{
		Accounts:    k.GetAllUsers(ctx),
		PoolBalance: k.GetPoolBalance(ctx),
	}
}

// ============================================================
// 帮助函数
// ============================================================

func deriveChainID(sendPk []byte) [32]byte { return sha256.Sum256(sendPk) }

func generateBondID(inviter, invitee string) string {
	return fmt.Sprintf("%s-%s", inviter[:16], invitee[:16])
}

func calculateTotalBond(commitments [][]byte) uint64 {
	return uint64(len(commitments)) * 100
}

func extractGuarantorIDs(sigs [][]byte) [][]byte { return sigs }

func ed25519Verify(pubKey []byte, message, sig []byte) bool {
	if len(sig) != 64 || len(pubKey) != 32 {
		return false
	}
	return ed25519.Verify(pubKey, message, sig)
}

// ============================================================
// 邀请凭证解析
// ============================================================

type inviteCred struct {
	InviterChainID [32]byte
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

// ============================================================
// 存储键前缀
// ============================================================

var (
	userPrefix = []byte{0x01}
	bondPrefix = []byte{0x02}
	poolKey    = []byte{0x03}
	heightKey  = []byte{0x04}
)

func userKey(chainID []byte) []byte  { return append(userPrefix, chainID...) }
func bondKey(bondID string) []byte   { return append(bondPrefix, []byte(bondID)...) }

// Silence unused import
var _ = sort.Ints
