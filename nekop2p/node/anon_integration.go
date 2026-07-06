package node

import (
	"github.com/nekop2p/nekop2p/anon"
	"github.com/nekop2p/nekop2p/frame"
	"github.com/nekop2p/nekop2p/onion"
	"github.com/nekop2p/nekop2p/peer"
)

// EnableAnonSwitch 为节点启用按需匿名切换。
// 调用后，SendMessage 会根据当前通道自动选择路由方式。
func (n *Node) EnableAnonSwitch(anonState *anon.State) {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.anonState = anonState
}

// SendAnonMessage 通过当前匿名通道发送消息。
// 明道：直接端到端加密发送
// 暗道：3 跳洋葱路由 + ZK 匿名
// 防空洞：5 跳洋葱路由 + 身份剥离
func (n *Node) SendAnonMessage(target peer.ChainID, plaintext []byte) error {
	n.mu.RLock()
	as := n.anonState
	n.mu.RUnlock()

	if as == nil {
		return n.SendMessage(target, plaintext)
	}

	switch as.Channel() {
	case anon.ChannelClear:
		return n.SendMessage(target, plaintext)

	case anon.ChannelDark:
		return n.sendOnionRouted(target, plaintext, 3)

	case anon.ChannelBunker:
		return n.sendOnionRouted(target, plaintext, 5)

	default:
		return n.SendMessage(target, plaintext)
	}
}

// sendOnionRouted 通过洋葱路由发送消息。
func (n *Node) sendOnionRouted(target peer.ChainID, plaintext []byte, hops int) error {
	selector := n.buildPathSelector()
	path, err := selector.SelectSecurePath(hops)
	if err != nil {
		return n.SendMessage(target, plaintext)
	}

	targetIP := n.getTargetIPv6(target)
	targetPort := uint16(9070)
	tgt := onion.Target{IPv6: targetIP, Port: targetPort}

	var circuitID [16]byte
	copy(circuitID[:], target[:16])
	pkt, err := onion.Build(circuitID, path, tgt, plaintext)
	if err != nil {
		return n.SendMessage(target, plaintext)
	}

	entry := path[0]
	return n.peers.SendFrame(entry.ChainID, frame.NewFrame(frame.FrameRoute, 0, pkt.Serialize()))
}

func (n *Node) buildPathSelector() *onion.PathSelector {
	var coreHops []onion.Hop
	var publicHops []onion.Hop

	for _, pi := range n.peers.All() {
		if pi.IsPadding {
			continue
		}
		// 从拓扑中查找 RecvPK（核心好友有完整密钥信息）
		recvPK := [32]byte{}
		if friend := n.topology.GetCoreFriend(pi.ChainID); friend != nil {
			recvPK = friend.RecvPK
		} else {
			// 非好友的公开节点：无 RecvPK，无法用于洋葱路由 KEM 加密，跳过
			continue
		}

		hop := onion.Hop{
			ChainID: pi.ChainID,
			IPv6:    pi.IPv6,
			Port:    pi.Port,
			RecvPK:  recvPK,
		}

		if n.topology.IsFriend(pi.ChainID) {
			coreHops = append(coreHops, hop)
		} else {
			publicHops = append(publicHops, hop)
		}
	}

	return onion.NewPathSelector(coreHops, publicHops)
}

func (n *Node) getTargetIPv6(target peer.ChainID) [16]byte {
	if friend := n.topology.GetCoreFriend(target); friend != nil {
		return friend.IPv6
	}
	for _, pi := range n.peers.All() {
		if pi.ChainID == target {
			return pi.IPv6
		}
	}
	return [16]byte{}
}

