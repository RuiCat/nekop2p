# 🏙️ 双层城邦 · 双链清算网络

**Nekop2p — The Dual-Chain Settlement Network**

> **可验证性取代信任** — 所有操作通过链上数据、零知识证明或公开算法实现事后可审计。

---

## 项目简介

**双层城邦 · 双链清算网络（Nekop2p）** 是一个融合 P2P 通信、双链架构（明链/暗链）、零知识证明（ZK）和混沌结算池的去中心化网络系统。

其核心理念是以**密码学可验证性**取代传统的社会信任机制。在 Nekop2p 中，节点不被信任，信任仅存在于密钥持有者之间。

---

## 核心特性

| 特性 | 描述 |
|:---|:---|
| 🔗 双层城邦模型 | 明域（透明公开的阳光世界）与暗域（匿名遮蔽的防空洞）组成双轨经济体系 |
| 🛡️ 五层防御体系 | E2E加密 → ZK遮蔽 → 洋葱路由 → 线下邀请 → 社会识别 |
| 🔐 零知识证明 | 基于 Groth16 的 6 组 ZK 电路（身份/信用/还款/工作量/门锁/票据） |
| 🌀 混沌结算池 | 金额 3~7 片拆分 · 时间 1~90 天随机化 · 来源伪装 |
| 🧅 洋葱路由 | ≥3 跳中继转发，端到端身份隐匿 |
| 💱 双链清算 | 明链（信用/担保/追偿）+ 暗链（匿名借贷/票据/门锁） |
| 🎮 游戏应用层 | 明域内置策略博弈玩法，暗域提供隐私支付通道 |
| 📡 P2P 通信层 | Noise IK/NK 握手 · Signal 双棘轮 · ChaCha20-Poly1305 帧加密 |

---

## 技术栈

```
语言        : Go 1.26.4
区块链框架   : Cosmos SDK v0.50+ · CometBFT
密码学      : Ed25519 · Curve25519 · ChaCha20-Poly1305 · BLAKE3
ZK 框架     : gnark (Groth16) · BN254 / BLS12-381
序列化      : Protobuf · CBOR
API         : gRPC · WebSocket
存储        : bbolt · IAVL
```

---

## 模块结构

```
nekop2p/
├── anon/         → 三通道匿名切换（明道/暗道/防空洞）
├── app/          → ABCI 胶水层
├── beacon/       → 加密信标洪泛发现
├── cmd/          → 入口程序（neko-node / observer / neko-demo）
├── config/       → 节点配置
├── consensus/    → BFT 共识引擎（95.8% 覆盖）
├── crypto/       → 密码学基础
├── dark/         → 暗域核心（交易/信用票据/匿名身份）
├── frame/        → TCP 传输帧加密
├── inkwell/      → 混沌结算池
├── intro/        → 线下邀请凭证
├── keystore/     → 双密钥对管理
├── localapi/     → 本地 gRPC/WebSocket API
├── node/         → 节点主控（三环路由拓扑）
├── noise/        → Noise IK/NK 握手协议
├── onion/        → 洋葱路由
├── peer/         → 对等连接
├── proto/        → Protobuf 定义
├── randbeacon/   → 分布式随机信标
├── ratchet/      → Signal 双棘轮
├── store/        → 链状态持久化
├── x/brightchain/ → 明链模块
├── x/darkchain/  → 暗链模块
├── x/node/       → 节点劳动力市场
├── x/zk/         → ZK 证明链上验证
└── zkcircuits/   → 6 组 Groth16 ZK 电路
```

---

## 快速开始

```bash
git clone https://github.com/nekop2p/nekop2p.git
cd nekop2p/nekop2p

go build -o neko-node ./cmd/neko-node
go build -o observer ./cmd/observer

go test -race ./...

./neko-node init
./neko-node start
```

---

## 项目状态

| 指标 | 数据 |
|:---|:---|
| 版本 | 3.0.0 |
| 源代码文件 | 92 个 |
| 测试文件 | 23 个 |
| 缺陷修复 | 71 项（八轮审计） |
| 竞态检测 | 零告警 |
| 核心覆盖率 | 78~100% |
| 生产就绪 | 🟢 是 |

---

## 许可证

本项目全部内容（文档、设计、源代码）以 **CC0 1.0 通用** 协议发布至公有领域。任何人可自由使用、修改、分发，无需署名、无需授权。

详见 [LICENSE](./LICENSE) 文件。

---

<p align="center"><sub>可验证性取代信任 · Verifiability replaces trust</sub></p>
