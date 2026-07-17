//go:build !cosmos

package brightchain

import (
	"context"
	"fmt"

	"github.com/nekop2p/nekop2p/x/brightchain/keeper"
	"github.com/nekop2p/nekop2p/x/brightchain/types"
	zk "github.com/nekop2p/nekop2p/x/zk"
)

// MsgServerImpl 实现 MsgServer 接口。
type MsgServerImpl struct {
	keeper *keeper.Keeper
}

// NewMsgServerImpl 创建新的 MsgServer。
func NewMsgServerImpl(k *keeper.Keeper) types.MsgServer {
	return &MsgServerImpl{keeper: k}
}

// Register 在明链上创建新用户。
func (m *MsgServerImpl) Register(ctx context.Context, msg *types.MsgRegister) (*types.MsgRegisterResponse, error) {
	if len(msg.RecvPk) == 0 || len(msg.SendPk) == 0 {
		return nil, fmt.Errorf("register: recv_pk and send_pk are required")
	}
	block, err := m.keeper.RegisterUser(types.UnwrapSDKContext(ctx), msg)
	if err != nil {
		return nil, err
	}
	return &types.MsgRegisterResponse{
		Address:  block.Address,
		Sequence: 0,
	}, nil
}

// UpdateFriends 更新用户的好友列表。
func (m *MsgServerImpl) UpdateFriends(ctx context.Context, msg *types.MsgUpdateFriends) (*types.MsgUpdateFriendsResponse, error) {
	if msg.Sender == "" {
		return nil, fmt.Errorf("update_friends: sender is required")
	}
	err := m.keeper.UpdateFriends(types.UnwrapSDKContext(ctx), msg)
	if err != nil {
		return nil, err
	}
	return &types.MsgUpdateFriendsResponse{}, nil
}

// Guarantee 创建担保金。
func (m *MsgServerImpl) Guarantee(ctx context.Context, msg *types.MsgGuarantee) (*types.MsgGuaranteeResponse, error) {
	if msg.Inviter == "" || msg.Invitee == "" {
		return nil, fmt.Errorf("guarantee: inviter and invitee are required")
	}
	if msg.Coefficient > 100 {
		return nil, fmt.Errorf("guarantee: coefficient must be ≤ 100")
	}
	bond, err := m.keeper.CreateBond(types.UnwrapSDKContext(ctx), msg)
	if err != nil {
		return nil, err
	}
	return &types.MsgGuaranteeResponse{BondId: bond.BondId}, nil
}

// ReleaseBond 释放担保金。
// 验证债券状态为 ACTIVE 后才释放。
func (m *MsgServerImpl) ReleaseBond(ctx context.Context, msg *types.MsgReleaseBond) (*types.MsgReleaseBondResponse, error) {
	if msg.BondId == "" {
		return nil, fmt.Errorf("release_bond: bond_id is required")
	}
	if msg.Inviter == "" {
		return nil, fmt.Errorf("release_bond: inviter is required")
	}
	err := m.keeper.ReleaseBond(types.UnwrapSDKContext(ctx), msg.BondId)
	if err != nil {
		return nil, err
	}
	return &types.MsgReleaseBondResponse{}, nil
}

// Repay 偿还暗链贷款（在明链上显示为资金池存款）。
// 验证基础参数后，将还款金额存入资金池。
func (m *MsgServerImpl) Repay(ctx context.Context, msg *types.MsgRepay) (*types.MsgRepayResponse, error) {
	if msg.FromAddress == "" {
		return nil, fmt.Errorf("repay: from_address is required")
	}
	if msg.Amount == 0 {
		return nil, fmt.Errorf("repay: amount must be > 0")
	}
	// ZK 还款证明验证: 证明暗链某借条已结清
	if len(msg.ZkRepayProof) > 0 {
		verifier := m.keeper.ZkVerifier()
		if verifier != nil {
			repayAssignment := zk.NewRepayAssignment(string(msg.InkwellRef), msg.Amount, msg.Amount, 0)
			if err := verifier.VerifyRepayProof(msg.ZkRepayProof, repayAssignment); err != nil {
				return nil, fmt.Errorf("zk repay proof invalid: %w", err)
			}
		}
	}

	// 将还款作为资金池存款处理
	err := m.keeper.CollectFees(types.UnwrapSDKContext(ctx), msg.Amount)
	if err != nil {
		return nil, err
	}
	return &types.MsgRepayResponse{}, nil
}

// RegisterNode 注册正式节点。
// 验证目标用户存在后，设置节点角色。
func (m *MsgServerImpl) RegisterNode(ctx context.Context, msg *types.MsgRegisterNode) (*types.MsgRegisterNodeResponse, error) {
	if msg.NodeAddress == "" {
		return nil, fmt.Errorf("register_node: node_address is required")
	}
	if msg.Role != types.NodeRole_OFFICIAL_RELAY && msg.Role != types.NodeRole_OFFICIAL_RECORD {
		return nil, fmt.Errorf("register_node: role must be OFFICIAL_RELAY or OFFICIAL_RECORD")
	}
	// ZK 工作量证明验证: 证明节点完成了转发/存储工作
	if len(msg.ZkWeightProof) > 0 {
		verifier := m.keeper.ZkVerifier()
		if verifier != nil {
			workAssignment := zk.NewWorkAssignment(0, 1000, 1024, 100, 0, 0, 0, 0)
			if err := verifier.VerifyWorkProof(msg.ZkWeightProof, workAssignment); err != nil {
				return nil, fmt.Errorf("zk work proof invalid: %w", err)
			}
		}
	}

	err := m.keeper.SetNodeRole(types.UnwrapSDKContext(ctx), msg.NodeAddress, msg.Role)
	if err != nil {
		return nil, err
	}
	return &types.MsgRegisterNodeResponse{}, nil
}

var _ types.MsgServer = (*MsgServerImpl)(nil)
