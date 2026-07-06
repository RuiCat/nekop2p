//go:build cosmos

// Package keeper 实现暗链模块的状态管理 (Cosmos SDK v0.50+ 版本)。
package keeper

import (
	"crypto/sha256"
	"fmt"
	"sync"
	"time"

	storetypes "cosmossdk.io/store/types"
	"github.com/cosmos/cosmos-sdk/codec"
	sdk "github.com/cosmos/cosmos-sdk/types"

	"github.com/nekop2p/nekop2p/x/darkchain/types"
)

type Keeper struct {
	cdc      codec.BinaryCodec
	storeKey storetypes.StoreKey

	// 身份标记缓存 (运行时，防自我交易)
	mu              sync.RWMutex
	identityMarkers map[string]bool
}

func NewKeeper(cdc codec.BinaryCodec, storeKey storetypes.StoreKey) Keeper {
	return Keeper{
		cdc:             cdc,
		storeKey:        storeKey,
		identityMarkers: make(map[string]bool),
	}
}

func (k Keeper) StoreKey() storetypes.StoreKey { return k.storeKey }

// ============================================================
// 贷款管理
// ============================================================

func (k Keeper) GetLoan(ctx sdk.Context, loanID []byte) (*types.LoanRecord, error) {
	store := ctx.KVStore(k.storeKey)
	key := loanKey(loanID)
	bz := store.Get(key)
	if bz == nil {
		return nil, fmt.Errorf("loan not found: %x", loanID[:8])
	}
	var loan types.LoanRecord
	k.cdc.MustUnmarshal(bz, &loan)
	return &loan, nil
}

func (k Keeper) SetLoan(ctx sdk.Context, loan *types.LoanRecord) error {
	store := ctx.KVStore(k.storeKey)
	bz, err := k.cdc.Marshal(loan)
	if err != nil {
		return fmt.Errorf("marshal loan: %w", err)
	}
	store.Set(loanKey(loan.LoanId), bz)
	return nil
}

func (k Keeper) GetAllLoans(ctx sdk.Context) []*types.LoanRecord {
	store := ctx.KVStore(k.storeKey)
	iter := storetypes.KVStorePrefixIterator(store, loanPrefix)
	defer iter.Close()

	var loans []*types.LoanRecord
	for ; iter.Valid(); iter.Next() {
		var loan types.LoanRecord
		k.cdc.MustUnmarshal(iter.Value(), &loan)
		loans = append(loans, &loan)
	}
	return loans
}

// RequestLoan 创建贷款请求。
func (k Keeper) RequestLoan(ctx sdk.Context, msg *types.MsgRequestLoan) (*types.LoanRecord, error) {
	// TODO: 验证 ZK 信用证明 (credit circuit)
	// TODO: 验证 nullifier 未双花
	// TODO: 验证身份标记 (防自我交易)

	loanID := generateLoanID(msg.BorrowerAnon, uint64(ctx.BlockTime().Unix()))

	loan := &types.LoanRecord{
		LoanId:       loanID,
		BorrowerAnon: msg.BorrowerAnon,
		Amount:       msg.Amount,
		TermDays:     msg.TermDays,
		Status:       types.LoanStatus_REQUESTED,
		CreatedAt:    ctx.BlockTime().Unix(),
		DueAt:        ctx.BlockTime().Unix() + msg.TermDays*86400,
	}

	if err := k.SetLoan(ctx, loan); err != nil {
		return nil, err
	}

	return loan, nil
}

// ApproveLoan 批准并放款。
func (k Keeper) ApproveLoan(ctx sdk.Context, msg *types.MsgApproveLoan) (*types.LoanRecord, error) {
	loan, err := k.GetLoan(ctx, msg.LoanId)
	if err != nil {
		return nil, err
	}

	if loan.Status != types.LoanStatus_REQUESTED {
		return nil, fmt.Errorf("loan %x is not in requested state", msg.LoanId[:8])
	}

	loan.Status = types.LoanStatus_APPROVED
	loan.LenderAnon = msg.LenderAnon
	loan.InkwellParams = msg.InkwellParams

	if err := k.SetLoan(ctx, loan); err != nil {
		return nil, err
	}

	ctx.EventManager().EmitEvent(
		sdk.NewEvent(
			types.EventTypeApproveLoan,
			sdk.NewAttribute(types.AttributeKeyLoanID, fmt.Sprintf("%x", msg.LoanId[:8])),
		),
	)

	return loan, nil
}

// SettleLoan 结清贷款。
func (k Keeper) SettleLoan(ctx sdk.Context, loanID []byte) error {
	loan, err := k.GetLoan(ctx, loanID)
	if err != nil {
		return err
	}

	loan.Status = types.LoanStatus_SETTLED
	loan.SettledAt = time.Now().Unix()

	if err := k.SetLoan(ctx, loan); err != nil {
		return err
	}

	return nil
}

// ============================================================
// Nullifier 管理 (防双花)
// ============================================================

func (k Keeper) IsNullifierSpent(ctx sdk.Context, nullifier []byte) bool {
	store := ctx.KVStore(k.storeKey)
	return store.Has(nullifierKey(nullifier))
}

func (k Keeper) MarkNullifier(ctx sdk.Context, nullifier, spender []byte) error {
	store := ctx.KVStore(k.storeKey)
	n := types.Nullifier{
		Value:    nullifier,
		SpentBy:  spender,
		SpentAt:  time.Now().Unix(),
	}
	bz, err := k.cdc.Marshal(&n)
	if err != nil {
		return err
	}
	store.Set(nullifierKey(nullifier), bz)
	return nil
}

// ============================================================
// 身份标记 (防自我交易)
// ============================================================

func (k Keeper) RecordIdentityMarker(marker string) {
	k.mu.Lock()
	defer k.mu.Unlock()
	k.identityMarkers[marker] = true
}

func (k Keeper) HasIdentityMarker(marker string) bool {
	k.mu.RLock()
	defer k.mu.RUnlock()
	return k.identityMarkers[marker]
}

// ============================================================
// BeginBlock / EndBlock
// ============================================================

func (k Keeper) BeginBlocker(ctx sdk.Context) {
	k.advanceCycle(ctx)
}

func (k Keeper) EndBlocker(ctx sdk.Context) {
	// 检查逾期贷款
	k.checkOverdueLoans(ctx)
}

func (k Keeper) advanceCycle(ctx sdk.Context) {
	now := ctx.BlockTime().Unix()

	for _, loan := range k.GetAllLoans(ctx) {
		if loan.Status == types.LoanStatus_APPROVED && now > loan.DueAt {
			loan.Status = types.LoanStatus_DEFAULTED
			k.SetLoan(ctx, loan)
		}
	}

	// 每个周期清理标记
	k.mu.Lock()
	k.identityMarkers = make(map[string]bool)
	k.mu.Unlock()
}

func (k Keeper) checkOverdueLoans(ctx sdk.Context) {
	// TODO: 实现逾期处理逻辑
}

// ============================================================
// InitGenesis / ExportGenesis
// ============================================================

func (k Keeper) InitGenesis(ctx sdk.Context, genesis *types.GenesisState) {
	for _, loan := range genesis.Loans {
		k.SetLoan(ctx, loan)
	}
	for _, nullifier := range genesis.Nullifiers {
		bz, _ := k.cdc.Marshal(nullifier)
		ctx.KVStore(k.storeKey).Set(nullifierKey(nullifier.Value), bz)
	}
}

func (k Keeper) ExportGenesis(ctx sdk.Context) *types.GenesisState {
	return &types.GenesisState{
		Loans:      k.GetAllLoans(ctx),
		Nullifiers: k.getAllNullifiers(ctx),
	}
}

func (k Keeper) getAllNullifiers(ctx sdk.Context) []*types.Nullifier {
	store := ctx.KVStore(k.storeKey)
	iter := storetypes.KVStorePrefixIterator(store, nullifierPrefix)
	defer iter.Close()

	var nullifiers []*types.Nullifier
	for ; iter.Valid(); iter.Next() {
		var n types.Nullifier
		k.cdc.MustUnmarshal(iter.Value(), &n)
		nullifiers = append(nullifiers, &n)
	}
	return nullifiers
}

// ============================================================
// 存储键前缀
// ============================================================

var (
	loanPrefix      = []byte{0x10}
	nullifierPrefix = []byte{0x11}
)

func loanKey(loanID []byte) []byte      { return append(loanPrefix, loanID...) }
func nullifierKey(v []byte) []byte       { return append(nullifierPrefix, v...) }

func generateLoanID(borrowerAnon []byte, ts uint64) []byte {
	h := sha256.New()
	h.Write(borrowerAnon)
	h.Write(sdk.Uint64ToBigEndian(ts))
	return h.Sum(nil)
}
