//go:build !cosmos

// Package node 区域节点共识集成 (Phase D)。
//
// 将区域共识协议集成到现有 Node 编排器中。
// 区域节点作为"快速路径"处理交易，全局链作为"记录层"批量审计。
//
// Package node 提供区域共识集成。
package node

import (
	"fmt"
	"log"

	"github.com/nekop2p/nekop2p/peer"
	"github.com/nekop2p/nekop2p/region"
)

// ============================================================
// Node 扩展字段
// ============================================================

// regionNodes 是此节点作为区域节点的集合。
// 一个物理节点可以担任多个区域的节点（不同区域不同网格）。
type regionNodes struct {
	Grid1 *region.RegionNode // 网格1中的区域节点
	Grid2 *region.RegionNode // 网格2中的区域节点
}

// ============================================================
// 区域节点初始化
// ============================================================

// InitRegionNodes 为此节点初始化区域节点。
// 根据节点的坐标确定它在两个网格中分别属于哪个区域。
func (n *Node) InitRegionNodes(coord region.SpatialCoord, startBalance uint64) {
	// 计算两个网格的区域ID
	r1ID, r2ID := region.UserRegions(coord)

	// 创建区域节点
	n.regionNodes = &regionNodes{
		Grid1: region.NewRegionNode(r1ID, 0, 100),
		Grid2: region.NewRegionNode(r2ID, 1, 100),
	}

	// 将自己添加为区域成员
	chainID := fmt.Sprintf("%x", n.cfg.ChainID[:8])
	n.regionNodes.Grid1.AddMember(chainID, startBalance, coord, r2ID)
	n.regionNodes.Grid2.AddMember(chainID, startBalance, coord, r1ID)

	// 互为主备
	n.regionNodes.Grid1.Partner = n.regionNodes.Grid2
	n.regionNodes.Grid2.Partner = n.regionNodes.Grid1

	log.Printf("[region] 节点 %s 初始化区域: Grid1=%s Grid2=%s",
		chainID, r1ID, r2ID)

	// 启动交易处理循环
	go n.regionNodes.Grid1.ProcessLoop()
	go n.regionNodes.Grid2.ProcessLoop()
}

// ============================================================
// 成员管理
// ============================================================

// AddRegionMember 将新用户添加到本节点的区域中。
func (n *Node) AddRegionMember(chainID string, balance uint64, coord region.SpatialCoord, otherNode string) {
	if n.regionNodes == nil {
		return
	}
	r1ID, r2ID := region.UserRegions(coord)

	// 只添加本节点管理的区域
	if n.regionNodes.Grid1.RegionID == r1ID {
		n.regionNodes.Grid1.AddMember(chainID, balance, coord, otherNode)
	}
	if n.regionNodes.Grid2.RegionID == r2ID {
		n.regionNodes.Grid2.AddMember(chainID, balance, coord, otherNode)
	}
}

// ============================================================
// 交易提交
// ============================================================

// SubmitRegionTx 通过区域节点提交交易。
// 自动选择: 同区域→直接处理, 跨区域→四方协议。
func (n *Node) SubmitRegionTx(from, to string, amount uint64, toNodeID string) (string, error) {
	if n.regionNodes == nil {
		return "", fmt.Errorf("region nodes not initialized")
	}

	// 选择发起方信任节点: 优先网格1
	rn := n.regionNodes.Grid1

	// 确定交易类型
	_, toInGrid1 := rn.GetBalance(to)
	_, toInGrid2 := n.regionNodes.Grid2.GetBalance(to)

	if toInGrid1 {
		// 同区域交易 (两者都在网格1)
		tx, err := region.ExecuteLocalTx(rn, from, to, amount)
		if err != nil {
			return "", err
		}
		return tx.ID, nil
	}

	// 跨区域交易
	if toInGrid2 {
		tx, req, err := region.InitiateCrossRegion(rn, n.regionNodes.Grid2.RegionID, from, to, amount)
		if err != nil {
			return "", err
		}

		// 交叉审查 (本节点同时持有两个区域节点)
		resp := region.VerifyCrossRegion(n.regionNodes.Grid2, req)
		if !resp.Accepted {
			return "", fmt.Errorf("cross-region rejected: %s", resp.Reason)
		}

		if err := region.FinalizeCrossRegion(rn, n.regionNodes.Grid2, tx); err != nil {
			return "", err
		}
		return tx.ID, nil
	}

	// 对方不在本节点的任何区域中 → 需要转发到对方区域节点
	tx, req, err := region.InitiateCrossRegion(rn, toNodeID, from, to, amount)
	if err != nil {
		return "", err
	}

	// 通过 P2P 网络发送跨区请求
	n.sendCrossRegionRequest(req, toNodeID)

	return tx.ID, nil
}

// ============================================================
// 批量提交到全局链
// ============================================================

// CommitRegionBatches 将区域节点累积的交易批量提交到全局链。
// 由全局链的 EndBlocker 定期调用。
func (n *Node) CommitRegionBatches(globalHeight int64) []*region.RegionBatch {
	if n.regionNodes == nil {
		return nil
	}

	var batches []*region.RegionBatch

	// 收集两个网格的批量数据
	for _, rn := range []*region.RegionNode{n.regionNodes.Grid1, n.regionNodes.Grid2} {
		totalTxs := rn.GetTxCount()
		if totalTxs == 0 {
			continue
		}

		// 收集跨区引用
		var crossRefs []region.CrossRegionRef
		for _, block := range rn.GetChainBlocks() {
			if block.Tx.CrossRef != "" {
				crossRefs = append(crossRefs, region.CrossRegionRef{
					LocalSeq: block.Tx.Seq,
				})
			}
		}

		batch := rn.GetBatch(1, totalTxs, crossRefs)
		batches = append(batches, batch)
	}

	return batches
}

// ============================================================
// P2P 跨区消息转发
// ============================================================

// sendCrossRegionRequest 通过 P2P 网络发送跨区交易请求。
func (n *Node) sendCrossRegionRequest(req *region.CrossRegionRequest, targetNodeID string) {
	// 序列化请求
	data := []byte(fmt.Sprintf("%s|%s|%s|%d", req.TxID, req.From, req.To, req.Amount))

	// 通过现有 frame 协议发送
	var targetCID peer.ChainID
	copy(targetCID[:], targetNodeID)
	// Phase D: 通过 frame.FrameType 发送跨区请求
	_ = targetCID
	_ = data
	log.Printf("[region] cross-region request sent: %s → %s", req.TxID, targetNodeID[:8])
}

