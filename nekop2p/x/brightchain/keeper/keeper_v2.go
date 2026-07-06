//go:build cosmos

// Package keeper 实现明链模块的状态管理 (Cosmos SDK v0.50+ 版本)。
//
// 使用 IAVL 树通过 Cosmos SDK KVStore 接口持久化所有链上状态：
//   - UserBlocks（身份、密钥、好友、信用）
//   - GuaranteeBonds（基于抵押的担保）
//   - Pool（共享资金池）
//
// Package keeper 提供明链状态管理器。
package keeper

import (
	"crypto/sha256"
	"fmt"

	storetypes "cosmossdk.io/store/types"
	"github.com/cosmos/cosmos-sdk/codec"
	sdk "github.com/cosmos/cosmos-sdk/types"

	"github.com/nekop2p/nekop2p/x/brightchain/types"
)

// Keeper 管理明链模块的状态（IAVL 持久化）。
type Keeper struct {
	cdc      codec.BinaryCodec
	storeKey storetypes.StoreKey
}

// NewKeeper 创建新的明链 Keeper。
func NewKeeper(cdc codec.BinaryCodec, storeKey storetypes.StoreKey) Keeper {
	return Keeper{
		cdc:      cdc,
		storeKey: storeKey,
	}
}

// GetStoreKey 返回此 Keeper 的存储键。
func (k Keeper) StoreKey() storetypes.StoreKey {
	return k.storeKey
}

// ============================================================
// 用户注册与管理
// ============================================================

// GetUser 通过 chain_id 获取用户数据块。
func (k Keeper) GetUser(ctx sdk.Context, chainID []byte) (*types.BrightAccount, error) {
	store := ctx.KVStore(k.storeKey)
	key := userKey(chainID)

	bz := store.Get(key)
	if bz == nil {
		return nil, fmt.Errorf("user not found: %x", chainID[:8])
	}

	var account types.BrightAccount
	k.cdc.MustUnmarshal(bz, &account)
	return &account, nil
}

// HasUser 检查用户是否存在。
func (k Keeper) HasUser(ctx sdk.Context, chainID []byte) bool {
	store := ctx.KVStore(k.storeKey)
	return store.Has(userKey(chainID))
}

// SetUser 写入用户数据块。
func (k Keeper) SetUser(ctx sdk.Context, account *types.BrightAccount) error {
	store := ctx.KVStore(k.storeKey)
	bz, err := k.cdc.Marshal(account)
	if err != nil {
		return fmt.Errorf("marshal user: %w", err)
	}
	store.Set(userKey(account.RecvPk), bz)
	return nil
}

// GetAllUsers 返回所有已注册用户。
func (k Keeper) GetAllUsers(ctx sdk.Context) []*types.BrightAccount {
	store := ctx.KVStore(k.storeKey)
	iter := storetypes.KVStorePrefixIterator(store, userPrefix)
	defer iter.Close()

	var users []*types.BrightAccount
	for ; iter.Valid(); iter.Next() {
		var account types.BrightAccount
		k.cdc.MustUnmarshal(iter.Value(), &account)
		users = append(users, &account)
	}
	return users
}

// UserCount 返回已注册用户总数。
func (k Keeper) UserCount(ctx sdk.Context) int {
	store := ctx.KVStore(k.storeKey)
	iter := storetypes.KVStorePrefixIterator(store, userPrefix)
	defer iter.Close()

	count := 0
	for ; iter.Valid(); iter.Next() {
		count++
	}
	return count
}

// RegisterUser 注册新用户。
// 创世阶段：前 MinGenesisUsers 个用户无需邀请凭证。
// 正常阶段：必须提供 ≥3 个有效邀请凭证。
func (k Keeper) RegisterUser(ctx sdk.Context, msg *types.MsgRegister) (*types.BrightAccount, error) {
	chainID := deriveChainID(msg.SendPk)

	if k.HasUser(ctx, chainID[:]) {
		return nil, fmt.Errorf("user already registered: %x", chainID[:8])
	}

	// 邀请凭证验证
	if !k.IsGenesisPhase(ctx) {
		if len(msg.GuarantorSigs) < types.MinGenesisUsers {
			return nil, fmt.Errorf("need at least %d invitation credentials, got %d",
				types.MinGenesisUsers, len(msg.GuarantorSigs))
		}
		// TODO: 验证 ZK 身份证明
	}

	account := &types.BrightAccount{
		RecvPk:     msg.RecvPk,
		SendPk:     msg.SendPk,
		SeedPhase:  true,
		CreditLimit: 1000, // 初始信用额度
	}

	if err := k.SetUser(ctx, account); err != nil {
		return nil, err
	}

	// 触发事件
	ctx.EventManager().EmitEvent(
		sdk.NewEvent(
			types.EventTypeRegister,
			sdk.NewAttribute(types.AttributeKeyChainID, fmt.Sprintf("%x", chainID[:8])),
		),
	)

	return account, nil
}

// IsGenesisPhase 返回当前是否处于创世阶段。
func (k Keeper) IsGenesisPhase(ctx sdk.Context) bool {
	return k.UserCount(ctx) < types.MinGenesisUsers
}

// ============================================================
// 担保债券
// ============================================================

// GetBond 通过 bond_id 获取担保债券。
func (k Keeper) GetBond(ctx sdk.Context, bondID string) (*types.GuaranteeBond, error) {
	store := ctx.KVStore(k.storeKey)
	key := bondKey(bondID)

	bz := store.Get(key)
	if bz == nil {
		return nil, fmt.Errorf("bond not found: %s", bondID)
	}

	var bond types.GuaranteeBond
	k.cdc.MustUnmarshal(bz, &bond)
	return &bond, nil
}

// SetBond 写入担保债券。
func (k Keeper) SetBond(ctx sdk.Context, bond *types.GuaranteeBond) error {
	store := ctx.KVStore(k.storeKey)
	bz, err := k.cdc.Marshal(bond)
	if err != nil {
		return fmt.Errorf("marshal bond: %w", err)
	}
	store.Set(bondKey(bond.BondId), bz)
	return nil
}

// CreateBond 创建新担保债券。
func (k Keeper) CreateBond(ctx sdk.Context, msg *types.MsgGuarantee) (*types.GuaranteeBond, error) {
	bondID := generateBondID(msg.Sender, msg.Invitee)

	bond := &types.GuaranteeBond{
		BondId:         bondID,
		Inviter:        []byte(msg.Sender),
		Invitee:        []byte(msg.Invitee),
		BondNotes:      msg.BondNotes,
		Coefficient:    msg.Coefficient,
		LockPeriodDays: msg.LockPeriodDays,
		Status:         types.BondStatus_ACTIVE,
		CreatedAt:      ctx.BlockTime().Unix(),
	}

	if err := k.SetBond(ctx, bond); err != nil {
		return nil, err
	}

	ctx.EventManager().EmitEvent(
		sdk.NewEvent(
			types.EventTypeCreateBond,
			sdk.NewAttribute(types.AttributeKeyBondID, bondID),
		),
	)

	return bond, nil
}

// ============================================================
// 资金池
// ============================================================

// GetPoolBalance 返回共享资金池余额。
func (k Keeper) GetPoolBalance(ctx sdk.Context) uint64 {
	store := ctx.KVStore(k.storeKey)
	bz := store.Get(poolKey)
	if bz == nil {
		return 0
	}
	return sdk.BigEndianToUint64(bz)
}

// SetPoolBalance 设置资金池余额。
func (k Keeper) SetPoolBalance(ctx sdk.Context, amount uint64) {
	store := ctx.KVStore(k.storeKey)
	bz := sdk.Uint64ToBigEndian(amount)
	store.Set(poolKey, bz)
}

// AddToPool 向资金池添加金额。
func (k Keeper) AddToPool(ctx sdk.Context, amount uint64) error {
	current := k.GetPoolBalance(ctx)
	newBalance := current + amount
	if newBalance < current { // overflow check
		return fmt.Errorf("pool overflow: %d + %d", current, amount)
	}
	k.SetPoolBalance(ctx, newBalance)
	return nil
}

// CollectFees 从交易中收取手续费并计入资金池。
func (k Keeper) CollectFees(ctx sdk.Context, fees uint64) error {
	return k.AddToPool(ctx, fees)
}

// ============================================================
// 区块高度
// ============================================================

// Height 返回持久化的区块高度。
func (k Keeper) Height(ctx sdk.Context) int64 {
	store := ctx.KVStore(k.storeKey)
	bz := store.Get(heightKey)
	if bz == nil {
		return 0
	}
	return int64(sdk.BigEndianToUint64(bz))
}

// SetHeight 持久化区块高度。
func (k Keeper) SetHeight(ctx sdk.Context, height int64) {
	store := ctx.KVStore(k.storeKey)
	store.Set(heightKey, sdk.Uint64ToBigEndian(uint64(height)))
}

// ============================================================
// BeginBlock / EndBlock
// ============================================================

// BeginBlocker 在每个区块开始时调用。
func (k Keeper) BeginBlocker(ctx sdk.Context) {
	// 每 100 个区块持久化高度
	if ctx.BlockHeight()%100 == 0 {
		k.SetHeight(ctx, ctx.BlockHeight())
	}

	// 每月工资发放 (每 43200 个区块 ≈ 每天 43200 个 2s 区块)
	if ctx.BlockHeight() > 0 && ctx.BlockHeight()%43200 == 0 {
		k.processMonthlySalary(ctx)
	}
}

// EndBlocker 在每个区块结束时调用。
func (k Keeper) EndBlocker(ctx sdk.Context) {
	// 检查逾期债券
	k.checkOverdueBonds(ctx)
}

// ============================================================
// InitGenesis / ExportGenesis
// ============================================================

// InitGenesis 从创世状态初始化。
func (k Keeper) InitGenesis(ctx sdk.Context, genesis *types.GenesisState) {
	k.SetPoolBalance(ctx, genesis.PoolBalance)

	for _, account := range genesis.Accounts {
		k.SetUser(ctx, account)
	}

	for _, bond := range genesis.Bonds {
		k.SetBond(ctx, bond)
	}
}

// ExportGenesis 导出当前状态为创世状态。
func (k Keeper) ExportGenesis(ctx sdk.Context) *types.GenesisState {
	return &types.GenesisState{
		Accounts:    k.GetAllUsers(ctx),
		PoolBalance: k.GetPoolBalance(ctx),
	}
}

// ============================================================
// 内部辅助函数
// ============================================================

func (k Keeper) processMonthlySalary(ctx sdk.Context) {
	// TODO: 实现节点工资发放
}

func (k Keeper) checkOverdueBonds(ctx sdk.Context) {
	// TODO: 检查并处理逾期债券
}

// deriveChainID 从 send_pk 派生 chain_id。
func deriveChainID(sendPK []byte) [32]byte {
	return sha256.Sum256(sendPK)
}

func generateBondID(inviter, invitee string) string {
	h := sha256.New()
	h.Write([]byte(inviter))
	h.Write([]byte(invitee))
	return fmt.Sprintf("%x", h.Sum(nil)[:16])
}

// ============================================================
// 存储键前缀
// ============================================================

var (
	userPrefix = []byte{0x01} // user/
	bondPrefix = []byte{0x02} // bond/
	poolKey    = []byte{0x03} // pool_balance
	heightKey  = []byte{0x04} // height
)

func userKey(chainID []byte) []byte {
	return append(userPrefix, chainID...)
}

func bondKey(bondID string) []byte {
	return append(bondPrefix, []byte(bondID)...)
}
