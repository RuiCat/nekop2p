// Package localapi 为上层应用提供本地 HTTP+WebSocket API。
//
// REST 端点 (HTTP):
//   GET  /identity  — 节点身份
//   GET  /friends   — 好友列表及在线状态
//   POST /message   — 发送加密消息
//   POST /tx        — 提交链上交易
//   GET  /status    — 节点状态
//
// WebSocket 端点:
//   GET  /events    — 实时事件流（新消息、好友状态变更）
//
// 认证: X-API-Token 头（首次启动时生成，打印到日志）
package localapi

import (
	"crypto/rand"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

// 推送到 WebSocket 客户端的事件类型。
const (
	EventMessage       = "message"        // 收到新的加密消息
	EventFriendOnline  = "friend_online"  // 好友上线
	EventFriendOffline = "friend_offline" // 好友下线
	EventBeaconFound   = "beacon_found"   // 信标响应已验证
	EventStateChange   = "state_change"   // 节点状态变更（OFFLINE→LIMBO→ONLINE）
)

// Event 被推送到 WebSocket 订阅者。
type Event struct {
	Type      string      `json:"type"`
	Timestamp int64       `json:"timestamp"`
	Data      interface{} `json:"data"`
}

// MessageEvent 数据。
type MessageEvent struct {
	From    string `json:"from"`    // 发送者 chain_id (十六进制)
	Message string `json:"message"` // 解密后的消息
}

// FriendEvent 数据。
type FriendEvent struct {
	ChainID string `json:"chain_id"`
}

// StateEvent 数据。
type StateEvent struct {
	OldState string `json:"old_state"`
	NewState string `json:"new_state"`
}

// Config 保存本地 API 配置。
type Config struct {
	ListenAddr string
	OnIdentity func() (chainID string, state string)
	OnFriends  func() []FriendInfo
	OnSendMsg  func(chainIDHex string, msg string) error
	OnSubmitTx func(txType string, data []byte) string // 提交链上交易
	OnStatus   func() StatusInfo
	OnGetTxs   func() []TxInfo // 获取最近交易
	OnGetPool  func() *PoolInfo // 获取资金池详情
	OnQuery    func(path string, query map[string]string) interface{} // 通用链查询
	OnBlacklist func(action, chainID string) interface{} // 黑名单管理
}

// QueryResult 通用查询结果
type QueryResult struct {
	Path   string      `json:"path"`
	Data   interface{} `json:"data"`
	Found  bool        `json:"found"`
}

// PoolInfo 资金池详情
type PoolInfo struct {
	TotalBalance   uint64 `json:"total_balance"`
	SalaryRelay    uint64 `json:"salary_relay"`
	SalaryRecord   uint64 `json:"salary_record"`
	SeedLoan       uint64 `json:"seed_loan"`
	BadDebt        uint64 `json:"bad_debt"`
	Community      uint64 `json:"community"`
	GameFees       uint64 `json:"game_fees"`
	GameCommission uint64 `json:"game_commission"`
}

// ChainStats 链上统计
type ChainStats struct {
	Users       int    `json:"users"`
	Loans       int    `json:"loans"`
	ActiveLoans int    `json:"active_loans"`
	PoolBalance uint64 `json:"pool_balance"`
	TotalCredit uint64 `json:"total_credit"`
}

// TxInfo 由 txs 端点返回。
type TxInfo struct {
	ID       string `json:"id"`
	Type     string `json:"type"`
	Status   string `json:"status"`
	BlockNum int64  `json:"block_num"`
	Time     int64  `json:"time"`
	Data     string `json:"data"`
}

// FriendInfo 由 friends 端点返回。
type FriendInfo struct {
	ChainID   string `json:"chain_id"`
	Online    bool   `json:"online"`
	TrustDist int    `json:"trust_dist"`
}

// StatusInfo 由 status 端点返回。
type StatusInfo struct {
	State         string      `json:"state"`
	Peers         int         `json:"peers"`
	OnlineFriends int         `json:"online_friends"`
	Height        int64       `json:"height"`
	Uptime        string      `json:"uptime"`
	Version       string      `json:"version"`
	Chain         *ChainStats `json:"chain,omitempty"`
}

// Server 提供本地 HTTP+WebSocket API 服务。
type Server struct {
	cfg     Config
	http    *http.Server
	token   string
	started time.Time

	// WebSocket 订阅者
	wsClients   map[*wsClient]bool
	wsMu        sync.Mutex
	wsUpgrader  websocket.Upgrader

	// 限流
	rateMu      sync.Mutex
	rateLimiters map[string]*rateLimiter
}

type rateLimiter struct {
	tokens   int
	lastSeen time.Time
}

type wsClient struct {
	conn *websocket.Conn
	send chan []byte
}

// New 创建新的本地 API 服务器。
func New(cfg Config) *Server {
	s := &Server{
		cfg:        cfg,
		token:      generateToken(),
		started:    time.Now(),
		wsClients:  make(map[*wsClient]bool),
		wsUpgrader: websocket.Upgrader{
			CheckOrigin: func(r *http.Request) bool {
				host := r.Host
				return host == "localhost" ||
					strings.HasPrefix(host, "127.0.0.1") ||
					strings.HasPrefix(host, "[::1")
			},
		},
		rateLimiters: make(map[string]*rateLimiter),
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/identity", s.handleIdentity)
	mux.HandleFunc("/friends", s.handleFriends)
	mux.HandleFunc("/message", s.handleMessage)
	mux.HandleFunc("/tx", s.handleTx)
	mux.HandleFunc("/status", s.handleStatus)
	mux.HandleFunc("/txs", s.handleTxs)
	mux.HandleFunc("/pool", s.handlePool)
	mux.HandleFunc("/query", s.handleQuery)
	mux.HandleFunc("/blacklist", s.handleBlacklist)
	mux.HandleFunc("/healthz", s.handleHealthz)
	mux.HandleFunc("/events", s.handleEvents)

	s.http = &http.Server{
		Addr:    cfg.ListenAddr,
		Handler: s.authMiddleware(mux),
	}

	return s
}

// Token 返回 API 令牌。
func (s *Server) Token() string { return s.token }

// Start 开始提供 API 服务。
func (s *Server) Start() error {
	ln, err := net.Listen("tcp", s.http.Addr)
	if err != nil {
		return fmt.Errorf("listen: %w", err)
	}
	log.Printf("[localapi] listening on %s", s.http.Addr)
	go s.http.Serve(ln)
	go s.rateCleanupLoop() // 定期清理过期限流条目
	return nil
}

// Stop 优雅地停止服务器。
func (s *Server) Stop() error {
	s.wsMu.Lock()
	for c := range s.wsClients {
		c.conn.Close()
	}
	s.wsClients = make(map[*wsClient]bool)
	s.wsMu.Unlock()
	return s.http.Close()
}

// PushEvent 向所有 WebSocket 订阅者广播事件。
func (s *Server) PushEvent(eventType string, data interface{}) {
	event := Event{
		Type:      eventType,
		Timestamp: time.Now().Unix(),
		Data:      data,
	}
	payload, err := json.Marshal(event)
	if err != nil {
		return
	}

	s.wsMu.Lock()
	defer s.wsMu.Unlock()
	for c := range s.wsClients {
		select {
		case c.send <- payload:
		default:
			// 客户端太慢，丢弃
			close(c.send)
			delete(s.wsClients, c)
		}
	}
}

// PushMessage 推送收到的消息事件。
func (s *Server) PushMessage(from string, msg string) {
	s.PushEvent(EventMessage, MessageEvent{From: from, Message: msg})
}

// PushFriendOnline 推送好友上线事件。
func (s *Server) PushFriendOnline(chainID string) {
	s.PushEvent(EventFriendOnline, FriendEvent{ChainID: chainID})
}

// PushFriendOffline 推送好友下线事件。
func (s *Server) PushFriendOffline(chainID string) {
	s.PushEvent(EventFriendOffline, FriendEvent{ChainID: chainID})
}

// PushStateChange 推送状态转换事件。
func (s *Server) PushStateChange(oldState, newState string) {
	s.PushEvent(EventStateChange, StateEvent{OldState: oldState, NewState: newState})
}

// === HTTP Handlers ===

func (s *Server) authMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// 限流检查 (100 req/s, burst 200)
		ip := r.RemoteAddr
		if idx := strings.LastIndex(ip, ":"); idx > 0 {
			ip = ip[:idx]
		}
		if !s.allowRequest(ip) {
			http.Error(w, `{"error":"rate limited"}`, http.StatusTooManyRequests)
			return
		}

		if r.URL.Path == "/status" && r.Method == "GET" {
			next.ServeHTTP(w, r)
			return
		}
		if r.URL.Path == "/txs" && r.Method == "GET" {
			next.ServeHTTP(w, r)
			return
		}
		if r.URL.Path == "/pool" && r.Method == "GET" {
			next.ServeHTTP(w, r)
			return
		}
		if r.URL.Path == "/query" && r.Method == "GET" {
			next.ServeHTTP(w, r)
			return
		}
		if r.URL.Path == "/healthz" && r.Method == "GET" {
			next.ServeHTTP(w, r)
			return
		}
		if r.URL.Path == "/events" && r.Method == "GET" {
			// WebSocket 通过查询参数或头部使用令牌
			token := r.URL.Query().Get("token")
			if token == "" {
				token = r.Header.Get("X-API-Token")
			}
			if token != s.token {
				http.Error(w, `{"error":"unauthorized"}`, http.StatusUnauthorized)
				return
			}
			next.ServeHTTP(w, r)
			return
		}
		token := r.Header.Get("X-API-Token")
		if token != s.token {
			http.Error(w, `{"error":"unauthorized"}`, http.StatusUnauthorized)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) handleIdentity(w http.ResponseWriter, r *http.Request) {
	if s.cfg.OnIdentity == nil {
		http.Error(w, `{"error":"not configured"}`, http.StatusServiceUnavailable)
		return
	}
	chainID, state := s.cfg.OnIdentity()
	json.NewEncoder(w).Encode(map[string]interface{}{
		"chain_id": chainID,
		"state":    state,
		"uptime":   time.Since(s.started).String(),
	})
}

func (s *Server) handleFriends(w http.ResponseWriter, r *http.Request) {
	if s.cfg.OnFriends == nil {
		http.Error(w, `{"error":"not configured"}`, http.StatusServiceUnavailable)
		return
	}
	friends := s.cfg.OnFriends()
	online := 0
	for _, f := range friends {
		if f.Online {
			online++
		}
	}
	json.NewEncoder(w).Encode(map[string]interface{}{
		"friends":      friends,
		"online_count": online,
	})
}

func (s *Server) handleMessage(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		http.Error(w, "POST required", http.StatusMethodNotAllowed)
		return
	}
	if s.cfg.OnSendMsg == nil {
		http.Error(w, `{"error":"not configured"}`, http.StatusServiceUnavailable)
		return
	}
	var req struct {
		ChainID string `json:"chain_id"`
		Message string `json:"message"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, `{"error":"invalid json"}`, http.StatusBadRequest)
		return
	}
	if err := s.cfg.OnSendMsg(req.ChainID, req.Message); err != nil {
		json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
		return
	}
	json.NewEncoder(w).Encode(map[string]string{"status": "sent"})
}

func (s *Server) handleTx(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		http.Error(w, "POST required", http.StatusMethodNotAllowed)
		return
	}
	if s.cfg.OnSubmitTx == nil {
		http.Error(w, `{"error":"chain not connected"}`, http.StatusServiceUnavailable)
		return
	}
	var req struct {
		Type string `json:"type"`
		Data []byte `json:"data"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, `{"error":"invalid json"}`, http.StatusBadRequest)
		return
	}
	txID := s.cfg.OnSubmitTx(req.Type, req.Data)
	json.NewEncoder(w).Encode(map[string]string{"status": "submitted", "tx_id": txID})
}

func (s *Server) handleStatus(w http.ResponseWriter, r *http.Request) {
	info := StatusInfo{State: "unknown", Version: "0.1.0"}
	if s.cfg.OnStatus != nil {
		info = s.cfg.OnStatus()
	}
	info.Uptime = time.Since(s.started).String()
	info.Version = "0.1.0"
	json.NewEncoder(w).Encode(info)
}

func (s *Server) handleTxs(w http.ResponseWriter, r *http.Request) {
	txs := []TxInfo{}
	if s.cfg.OnGetTxs != nil {
		txs = s.cfg.OnGetTxs()
	}
	json.NewEncoder(w).Encode(map[string]interface{}{
		"txs":   txs,
		"count": len(txs),
	})
}

func (s *Server) handlePool(w http.ResponseWriter, r *http.Request) {
	pool := &PoolInfo{}
	if s.cfg.OnGetPool != nil {
		if p := s.cfg.OnGetPool(); p != nil {
			pool = p
		}
	}
	json.NewEncoder(w).Encode(pool)
}

func (s *Server) handleQuery(w http.ResponseWriter, r *http.Request) {
	if s.cfg.OnQuery == nil {
		json.NewEncoder(w).Encode(QueryResult{Found: false})
		return
	}
	q := r.URL.Query()
	queryMap := make(map[string]string)
	for k := range q {
		queryMap[k] = q.Get(k)
	}
	result := s.cfg.OnQuery(r.URL.RawQuery, queryMap)
	json.NewEncoder(w).Encode(QueryResult{
		Path:  r.URL.RawQuery,
		Data:  result,
		Found: result != nil,
	})
}

func (s *Server) handleBlacklist(w http.ResponseWriter, r *http.Request) {
	if s.cfg.OnBlacklist == nil {
		json.NewEncoder(w).Encode(map[string]string{"error": "not available"})
		return
	}
	action := r.URL.Query().Get("action")
	cid := r.URL.Query().Get("chain_id")
	result := s.cfg.OnBlacklist(action, cid)
	json.NewEncoder(w).Encode(result)
}

// handleHealthz 健康检查端点（无需认证）。
func (s *Server) handleHealthz(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"status":  "ok",
		"uptime":  time.Since(s.started).String(),
		"version": "0.3.0",
	})
}

// === WebSocket Handler ===

func (s *Server) handleEvents(w http.ResponseWriter, r *http.Request) {
	conn, err := s.wsUpgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Printf("[localapi] ws upgrade: %v", err)
		return
	}

	client := &wsClient{
		conn: conn,
		send: make(chan []byte, 64),
	}

	s.wsMu.Lock()
	s.wsClients[client] = true
	s.wsMu.Unlock()

	// 发送初始连接事件
	initEvent := Event{
		Type:      "connected",
		Timestamp: time.Now().Unix(),
		Data:      map[string]string{"version": "0.1.0"},
	}
	payload, _ := json.Marshal(initEvent)
	conn.WriteMessage(websocket.TextMessage, payload)

	// 写入泵
	go func() {
		defer func() {
			conn.Close()
			s.wsMu.Lock()
			delete(s.wsClients, client)
			s.wsMu.Unlock()
		}()
		for msg := range client.send {
			if err := conn.WriteMessage(websocket.TextMessage, msg); err != nil {
				return
			}
		}
	}()

	// 读取泵（保持连接活跃，处理关闭）
	for {
		if _, _, err := conn.ReadMessage(); err != nil {
			break
		}
	}
	// 读泵退出：关闭 send channel 通知写泵退出（防止 goroutine 泄漏）
	close(client.send)
}

var upgrader = websocket.Upgrader{}
var _ = upgrader // 抑制未使用告警

func generateToken() string {
	// 生成 64 字符的 hex token（使用加密安全随机数）
	b := make([]byte, 32)
	if _, err := io.ReadFull(rand.Reader, b); err != nil {
		// crypto/rand 失败是灾难性的——不应静默降级
		panic(fmt.Sprintf("localapi: crypto/rand failed: %v", err))
	}
	return fmt.Sprintf("%x", b)
}

func (s *Server) allowRequest(ip string) bool {
	s.rateMu.Lock()
	defer s.rateMu.Unlock()

	rl, ok := s.rateLimiters[ip]
	if !ok {
		rl = &rateLimiter{tokens: 200, lastSeen: time.Now()}
		s.rateLimiters[ip] = rl
	}

	elapsed := time.Since(rl.lastSeen).Seconds()
	rl.tokens += int(elapsed * 100)
	if rl.tokens > 200 { rl.tokens = 200 }
	rl.lastSeen = time.Now()

	if rl.tokens <= 0 { return false }
	rl.tokens--
	return true
}

// rateCleanupLoop 定期清理过期的限流条目，防止内存泄漏。
func (s *Server) rateCleanupLoop() {
	ticker := time.NewTicker(5 * time.Minute)
	defer ticker.Stop()
	for range ticker.C {
		s.rateMu.Lock()
		now := time.Now()
		for ip, rl := range s.rateLimiters {
			if now.Sub(rl.lastSeen) > 10*time.Minute {
				delete(s.rateLimiters, ip)
			}
		}
		s.rateMu.Unlock()
	}
}
