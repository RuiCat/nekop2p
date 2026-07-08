// Package localapi gRPC 服务端实现。
//
// 使用手动编写的 protobuf 兼容消息类型，
// 在 localhost 上提供与 HTTP API 并行的 gRPC 服务。
package localapi

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/anypb"
)

// ============================================================
// gRPC 消息类型（手动编写，对应 proto/nekop2p/local/service.proto）
// ============================================================

// GEmpty 空消息。
type GEmpty struct{}

func (m *GEmpty) Reset()         { *m = GEmpty{} }
func (m *GEmpty) String() string { return "{}" }
func (*GEmpty) ProtoMessage()    {}

// GMyIdentity 节点身份。
type GMyIdentity struct {
	ChainId     string `protobuf:"bytes,1,opt,name=chain_id,json=chainId,proto3" json:"chain_id,omitempty"`
	RecvPk      []byte `protobuf:"bytes,2,opt,name=recv_pk,json=recvPk,proto3" json:"recv_pk,omitempty"`
	SendPk      []byte `protobuf:"bytes,3,opt,name=send_pk,json=sendPk,proto3" json:"send_pk,omitempty"`
	CreditScore uint64 `protobuf:"varint,4,opt,name=credit_score,json=creditScore,proto3" json:"credit_score,omitempty"`
	TrustWeight uint64 `protobuf:"varint,5,opt,name=trust_weight,json=trustWeight,proto3" json:"trust_weight,omitempty"`
	NodeRole    string `protobuf:"bytes,6,opt,name=node_role,json=nodeRole,proto3" json:"node_role,omitempty"`
}

func (m *GMyIdentity) Reset()         { *m = GMyIdentity{} }
func (m *GMyIdentity) String() string { return fmt.Sprintf("GMyIdentity{chain_id:%s}", m.ChainId) }
func (*GMyIdentity) ProtoMessage()    {}

// GSendMessageRequest 发送消息请求。
type GSendMessageRequest struct {
	TargetChainId string `protobuf:"bytes,1,opt,name=target_chain_id,json=targetChainId,proto3" json:"target_chain_id,omitempty"`
	Plaintext     []byte `protobuf:"bytes,2,opt,name=plaintext,proto3" json:"plaintext,omitempty"`
	AnonLevel     int32  `protobuf:"varint,3,opt,name=anon_level,json=anonLevel,proto3" json:"anon_level,omitempty"`
}

func (m *GSendMessageRequest) Reset()         { *m = GSendMessageRequest{} }
func (m *GSendMessageRequest) String() string { return fmt.Sprintf("to:%s", m.TargetChainId) }
func (*GSendMessageRequest) ProtoMessage()    {}

// GSendMessageResponse 发送消息响应。
type GSendMessageResponse struct {
	MessageId []byte `protobuf:"bytes,1,opt,name=message_id,json=messageId,proto3" json:"message_id,omitempty"`
}

func (m *GSendMessageResponse) Reset()         { *m = GSendMessageResponse{} }
func (m *GSendMessageResponse) String() string { return "ok" }
func (*GSendMessageResponse) ProtoMessage()    {}

// GSubmitTxRequest 提交交易请求。
type GSubmitTxRequest struct {
	TxType string `protobuf:"bytes,1,opt,name=tx_type,json=txType,proto3" json:"tx_type,omitempty"`
	TxData []byte `protobuf:"bytes,2,opt,name=tx_data,json=txData,proto3" json:"tx_data,omitempty"`
}

func (m *GSubmitTxRequest) Reset()         { *m = GSubmitTxRequest{} }
func (m *GSubmitTxRequest) String() string { return m.TxType }
func (*GSubmitTxRequest) ProtoMessage()    {}

// GSubmitTxResponse 提交交易响应。
type GSubmitTxResponse struct {
	TxHash string `protobuf:"bytes,1,opt,name=tx_hash,json=txHash,proto3" json:"tx_hash,omitempty"`
	Height int64  `protobuf:"varint,2,opt,name=height,proto3" json:"height,omitempty"`
}

func (m *GSubmitTxResponse) Reset()         { *m = GSubmitTxResponse{} }
func (m *GSubmitTxResponse) String() string { return m.TxHash }
func (*GSubmitTxResponse) ProtoMessage()    {}

// GQueryRequest 查询请求。
type GQueryRequest struct {
	QueryType string `protobuf:"bytes,1,opt,name=query_type,json=queryType,proto3" json:"query_type,omitempty"`
	QueryData []byte `protobuf:"bytes,2,opt,name=query_data,json=queryData,proto3" json:"query_data,omitempty"`
}

func (m *GQueryRequest) Reset()         { *m = GQueryRequest{} }
func (m *GQueryRequest) String() string { return m.QueryType }
func (*GQueryRequest) ProtoMessage()    {}

// GQueryResponse 查询响应。
type GQueryResponse struct {
	Result []byte `protobuf:"bytes,1,opt,name=result,proto3" json:"result,omitempty"`
}

func (m *GQueryResponse) Reset()         { *m = GQueryResponse{} }
func (m *GQueryResponse) String() string { return "query_result" }
func (*GQueryResponse) ProtoMessage()    {}

// GNodeStatus 节点状态。
type GNodeStatus struct {
	SyncProfile    string `protobuf:"bytes,1,opt,name=sync_profile,json=syncProfile,proto3" json:"sync_profile,omitempty"`
	PeersOnline    int32  `protobuf:"varint,2,opt,name=peers_online,json=peersOnline,proto3" json:"peers_online,omitempty"`
	ChainHeight    int64  `protobuf:"varint,3,opt,name=chain_height,json=chainHeight,proto3" json:"chain_height,omitempty"`
	PoolBalance    uint64 `protobuf:"varint,4,opt,name=pool_balance,json=poolBalance,proto3" json:"pool_balance,omitempty"`
	FriendsOnline  int32  `protobuf:"varint,5,opt,name=friends_online,json=friendsOnline,proto3" json:"friends_online,omitempty"`
}

func (m *GNodeStatus) Reset()         { *m = GNodeStatus{} }
func (m *GNodeStatus) String() string { return fmt.Sprintf("GNodeStatus{sync:%s}", m.SyncProfile) }
func (*GNodeStatus) ProtoMessage()    {}

// ============================================================
// gRPC 服务端
// ============================================================

// GRPCServer 提供基于 gRPC 的本地 API 服务。
// 利用自定义服务描述符手动注册方法，避免 protoc 代码生成依赖。
type GRPCServer struct {
	grpcServer *grpc.Server
	listener   net.Listener
	svc        *Server // 包装的 HTTP 服务器以获取回调
	started    time.Time
}

// NewGRPCServer 创建新的 gRPC 服务器。
// 注册所有 LocalAPI 服务的 RPC 方法。
func NewGRPCServer(svc *Server, listenAddr string) (*GRPCServer, error) {
	ln, err := net.Listen("tcp", listenAddr)
	if err != nil {
		return nil, fmt.Errorf("grpc listen: %w", err)
	}

	gs := &GRPCServer{
		svc:      svc,
		listener: ln,
		started:  time.Now(),
	}

	// 创建带认证拦截器的 gRPC 服务器
	gs.grpcServer = grpc.NewServer(
		grpc.UnaryInterceptor(gs.authInterceptor),
	)
	gs.grpcServer.RegisterService(&grpc.ServiceDesc{
		ServiceName: "nekop2p.local.LocalAPI",
		HandlerType: (*GRPCLocalAPIServer)(nil),
		Methods: []grpc.MethodDesc{
			{
				MethodName: "GetMyIdentity",
				Handler:    gs.handleGetMyIdentity,
			},
			{
				MethodName: "SendMessage",
				Handler:    gs.handleSendMessage,
			},
			{
				MethodName: "SubmitTransaction",
				Handler:    gs.handleSubmitTransaction,
			},
			{
				MethodName: "QueryChain",
				Handler:    gs.handleQueryChain,
			},
			{
				MethodName: "GetNodeStatus",
				Handler:    gs.handleGetNodeStatus,
			},
		},
		Streams:  []grpc.StreamDesc{},
		Metadata: "localapi/grpc_server.go",
	}, gs)

	return gs, nil
}

// Start 启动 gRPC 服务器（非阻塞）。
func (gs *GRPCServer) Start() {
	log.Printf("[localapi-grpc] listening on %s", gs.listener.Addr().String())
	go func() {
		if err := gs.grpcServer.Serve(gs.listener); err != nil {
			log.Printf("[localapi-grpc] serve error: %v", err)
		}
	}()
}

// Stop 优雅地停止 gRPC 服务器。
func (gs *GRPCServer) Stop() {
	gs.grpcServer.GracefulStop()
}

// Addr 返回监听地址。
func (gs *GRPCServer) Addr() string {
	return gs.listener.Addr().String()
}

// ============================================================
// GRPCLocalAPIServer 接口（用于 grpc.ServiceDesc.HandlerType）
// ============================================================

// GRPCLocalAPIServer 定义 gRPC LocalAPI 服务的接口。
type GRPCLocalAPIServer interface {
	GetMyIdentity(context.Context, *GEmpty) (*GMyIdentity, error)
	SendMessage(context.Context, *GSendMessageRequest) (*GSendMessageResponse, error)
	SubmitTransaction(context.Context, *GSubmitTxRequest) (*GSubmitTxResponse, error)
	QueryChain(context.Context, *GQueryRequest) (*GQueryResponse, error)
	GetNodeStatus(context.Context, *GEmpty) (*GNodeStatus, error)
}

// ============================================================
// RPC 处理器
// ============================================================

func (gs *GRPCServer) handleGetMyIdentity(srv interface{}, ctx context.Context, dec func(interface{}) error, interceptor grpc.UnaryServerInterceptor) (interface{}, error) {
	var req GEmpty
	if err := dec(&req); err != nil {
		return nil, err
	}
	if interceptor != nil {
		return interceptor(ctx, &req, &grpc.UnaryServerInfo{Server: srv, FullMethod: "/nekop2p.local.LocalAPI/GetMyIdentity"}, func(ctx context.Context, req interface{}) (interface{}, error) {
		return gs.GetMyIdentity(ctx, req.(*GEmpty))
	})
	}
	return gs.GetMyIdentity(ctx, &req)
}

func (gs *GRPCServer) handleSendMessage(srv interface{}, ctx context.Context, dec func(interface{}) error, interceptor grpc.UnaryServerInterceptor) (interface{}, error) {
	var req GSendMessageRequest
	if err := dec(&req); err != nil {
		return nil, err
	}
	if interceptor != nil {
		return interceptor(ctx, &req, &grpc.UnaryServerInfo{Server: srv, FullMethod: "/nekop2p.local.LocalAPI/SendMessage"}, func(ctx context.Context, req interface{}) (interface{}, error) {
		return gs.SendMessage(ctx, req.(*GSendMessageRequest))
	})
	}
	return gs.SendMessage(ctx, &req)
}

func (gs *GRPCServer) handleSubmitTransaction(srv interface{}, ctx context.Context, dec func(interface{}) error, interceptor grpc.UnaryServerInterceptor) (interface{}, error) {
	var req GSubmitTxRequest
	if err := dec(&req); err != nil {
		return nil, err
	}
	if interceptor != nil {
		return interceptor(ctx, &req, &grpc.UnaryServerInfo{Server: srv, FullMethod: "/nekop2p.local.LocalAPI/SubmitTransaction"}, func(ctx context.Context, req interface{}) (interface{}, error) {
		return gs.SubmitTransaction(ctx, req.(*GSubmitTxRequest))
	})
	}
	return gs.SubmitTransaction(ctx, &req)
}

func (gs *GRPCServer) handleQueryChain(srv interface{}, ctx context.Context, dec func(interface{}) error, interceptor grpc.UnaryServerInterceptor) (interface{}, error) {
	var req GQueryRequest
	if err := dec(&req); err != nil {
		return nil, err
	}
	if interceptor != nil {
		return interceptor(ctx, &req, &grpc.UnaryServerInfo{Server: srv, FullMethod: "/nekop2p.local.LocalAPI/QueryChain"}, func(ctx context.Context, req interface{}) (interface{}, error) {
		return gs.QueryChain(ctx, req.(*GQueryRequest))
	})
	}
	return gs.QueryChain(ctx, &req)
}

func (gs *GRPCServer) handleGetNodeStatus(srv interface{}, ctx context.Context, dec func(interface{}) error, interceptor grpc.UnaryServerInterceptor) (interface{}, error) {
	var req GEmpty
	if err := dec(&req); err != nil {
		return nil, err
	}
	if interceptor != nil {
		return interceptor(ctx, &req, &grpc.UnaryServerInfo{Server: srv, FullMethod: "/nekop2p.local.LocalAPI/GetNodeStatus"}, func(ctx context.Context, req interface{}) (interface{}, error) {
		return gs.GetNodeStatus(ctx, req.(*GEmpty))
	})
	}
	return gs.GetNodeStatus(ctx, &req)
}

// ============================================================
// RPC 实现方法
// ============================================================

// GetMyIdentity 返回节点身份信息。
func (gs *GRPCServer) GetMyIdentity(ctx context.Context, req *GEmpty) (*GMyIdentity, error) {
	if gs.svc.cfg.OnIdentity == nil {
		return nil, status.Error(codes.Unavailable, "identity callback not configured")
	}
	chainID, state := gs.svc.cfg.OnIdentity()
	return &GMyIdentity{
		ChainId:     chainID,
		NodeRole:    state,
	}, nil
}

// SendMessage 发送加密消息到目标好友。
func (gs *GRPCServer) SendMessage(ctx context.Context, req *GSendMessageRequest) (*GSendMessageResponse, error) {
	if gs.svc.cfg.OnSendMsg == nil {
		return nil, status.Error(codes.Unavailable, "messaging not configured")
	}
	if req.TargetChainId == "" {
		return nil, status.Error(codes.InvalidArgument, "target_chain_id required")
	}
	if err := gs.svc.cfg.OnSendMsg(req.TargetChainId, string(req.Plaintext)); err != nil {
		return nil, status.Errorf(codes.Internal, "send failed: %v", err)
	}
	msgID := fmt.Sprintf("msg-%d", time.Now().UnixNano())
	return &GSendMessageResponse{
		MessageId: []byte(msgID),
	}, nil
}

// SubmitTransaction 提交链上交易。
func (gs *GRPCServer) SubmitTransaction(ctx context.Context, req *GSubmitTxRequest) (*GSubmitTxResponse, error) {
	if gs.svc.cfg.OnSubmitTx == nil {
		return nil, status.Error(codes.Unavailable, "chain not connected")
	}
	if req.TxType == "" {
		return nil, status.Error(codes.InvalidArgument, "tx_type required")
	}
	txID := gs.svc.cfg.OnSubmitTx(req.TxType, req.TxData)
	return &GSubmitTxResponse{
		TxHash: txID,
		Height: 0, // 由后续区块填充
	}, nil
}

// QueryChain 执行链上查询。
func (gs *GRPCServer) QueryChain(ctx context.Context, req *GQueryRequest) (*GQueryResponse, error) {
	if gs.svc.cfg.OnQuery == nil {
		return nil, status.Error(codes.Unavailable, "query not configured")
	}
	queryMap := map[string]string{"type": req.QueryType}
	result := gs.svc.cfg.OnQuery(req.QueryType, queryMap)

	resultJSON, err := json.Marshal(result)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "marshal result: %v", err)
	}
	return &GQueryResponse{
		Result: resultJSON,
	}, nil
}

// GetNodeStatus 返回节点运行状态。
func (gs *GRPCServer) GetNodeStatus(ctx context.Context, req *GEmpty) (*GNodeStatus, error) {
	info := StatusInfo{State: "unknown", Version: "0.2.0"}
	if gs.svc.cfg.OnStatus != nil {
		info = gs.svc.cfg.OnStatus()
	}
	return &GNodeStatus{
		SyncProfile:   info.State,
		PeersOnline:   int32(info.Peers),
		ChainHeight:   info.Height,
		PoolBalance:   0, // 生产环境从链上查询
		FriendsOnline: int32(info.OnlineFriends),
	}, nil
}

// suppress unused imports
var _ = proto.Marshal
var _ = anypb.Any{}
var _ = context.Background

// ============================================================
// 认证拦截器
// ============================================================

// authInterceptor gRPC 一元拦截器：验证 X-API-Token 与 HTTP API 使用同一令牌。
func (gs *GRPCServer) authInterceptor(
	ctx context.Context,
	req interface{},
	info *grpc.UnaryServerInfo,
	handler grpc.UnaryHandler,
) (interface{}, error) {
	// 允许 GetNodeStatus 无需认证（与 HTTP /status 端点一致）
	if info.FullMethod == "/nekop2p.local.LocalAPI/GetNodeStatus" {
		return handler(ctx, req)
	}

	md, ok := metadata.FromIncomingContext(ctx)
	if !ok {
		return nil, status.Error(codes.Unauthenticated, "missing metadata")
	}

	tokens := md.Get("x-api-token")
	if len(tokens) == 0 {
		return nil, status.Error(codes.Unauthenticated, "missing x-api-token")
	}

	if tokens[0] != gs.svc.token {
		return nil, status.Error(codes.PermissionDenied, "invalid token")
	}

	return handler(ctx, req)
}
