# 双层城邦 · 双链清算网络

<h1 align="center">
  <sub>🏙️ 双层城邦 · 双链清算网络</sub>
  <br>
  <sup>Nekop2p — The Dual-Chain Settlement Network</sup>
</h1>

<p align="center">
  <strong>可验证性取代信任 · 密码学保障自由</strong>
</p>

<p align="center">
  <a href="#-项目概述">🇨🇳 中文</a> ·
  <a href="#-project-overview">🇺🇸 English</a> ·
  <a href="#-プロジェクト概要">🇯🇵 日本語</a> ·
  <a href="#-프로젝트-개요">🇰🇷 한국어</a>
</p>

<p align="center">
  <img src="https://img.shields.io/badge/version-3.1.0-blue" alt="version">
  <img src="https://img.shields.io/badge/language-Go%201.22+-00ADD8?logo=go" alt="Go">
  <img src="https://img.shields.io/badge/framework-Cosmos%20SDK%20(迁移中)-2E3148?logo=cosmos" alt="Cosmos">
  <img src="https://img.shields.io/badge/status-audited-brightgreen" alt="status">
  <img src="https://img.shields.io/badge/license-CC0%201.0-lightgrey" alt="license">
  <img src="https://img.shields.io/badge/tests-28%2F28%20passing-success" alt="tests">
</p>

---

## 🇨🇳 项目概述

**双层城邦 · 双链清算网络（Nekop2p）** 是一个融合 P2P 通信、双链架构（明链/暗链）、零知识证明（ZK）和混沌结算池的去中心化网络系统。其核心理念为：

> **可验证性取代信任** — 所有操作通过链上数据、零知识证明或公开算法实现事后可审计。

### 核心特性

| 特性 | 描述 |
|:---|:---|
| 🔗 **双层城邦模型** | 明域（透明公开的阳光世界）与暗域（匿名遮蔽的防空洞）组成双轨经济体系 |
| 🛡️ **五层防御体系** | E2E加密 → ZK遮蔽 → 洋葱路由 → 线下邀请 → 社会识别 |
| 🔐 **零知识证明** | 基于 Groth16 的 6 组 ZK 电路（身份/信用/还款/工作量/门锁/票据） |
| 🌀 **混沌结算池** | 金额 3~7 片拆分 · 时间 1~90 天随机化 · 来源伪装 |
| 🧅 **洋葱路由** | ≥3 跳中继转发，端到端身份隐匿 |
| 💱 **双链清算** | 明链（信用/担保/追偿）+ 暗链（匿名借贷/票据/门锁） |
| 🎮 **游戏应用层** | 明域内置策略博弈玩法，暗域提供隐私支付通道 |
| 📡 **P2P 通信层** | Noise IK/NK 握手 · Signal 双棘轮 · ChaCha20-Poly1305 帧加密 |

### 技术栈

```
语言      : Go 1.26.4
区块链框架 : Cosmos SDK v0.50+ · CometBFT
密码学     : Ed25519 · Curve25519 · ChaCha20-Poly1305 · BLAKE3
ZK 框架   : gnark (Groth16) · BN254 / BLS12-381
序列化     : Protobuf · CBOR
API       : gRPC · WebSocket
存储      : bbolt · IAVL
```

### 模块结构（29 个包）

```
nekop2p/
├── anon/         三通道匿名切换（明道/暗道/防空洞）
├── app/          ABCI 胶水层
├── beacon/       加密信标洪泛发现
├── cmd/          入口程序（neko-node / observer / neko-demo）
├── config/       节点配置
├── consensus/    BFT 共识引擎（95.8% 覆盖）
├── crypto/       密码学基础
├── dark/         暗域核心（交易/信用票据/匿名身份）
├── frame/        TCP 传输帧加密
├── inkwell/      混沌结算池
├── intro/        线下邀请凭证
├── keystore/     双密钥对管理
├── localapi/     本地 gRPC/WebSocket API
├── node/         节点主控（三环路由拓扑）
├── noise/        Noise IK/NK 握手协议
├── onion/        洋葱路由
├── peer/         对等连接
├── proto/        Protobuf 定义
├── randbeacon/   分布式随机信标
├── ratchet/      Signal 双棘轮
├── store/        链状态持久化
├── x/brightchain/ 明链模块
├── x/darkchain/  暗链模块
├── x/node/       节点劳动力市场
├── x/zk/         ZK 证明链上验证
└── zkcircuits/   6 组 Groth16 ZK 电路
```

### 🚀 快速开始

```bash
# 克隆项目
git clone https://github.com/nekop2p/nekop2p.git
cd nekop2p

# 编译
cd nekop2p
go build -o neko-node ./cmd/neko-node
go build -o observer ./cmd/observer

# 运行测试
go test -race ./...

# 启动节点
./neko-node init
./neko-node start
```

### 📊 项目状态

| 指标 | 数据 |
|:---|:---|
| 版本 | **3.0.0** |
| 源代码文件 | 92 个 |
| 测试文件 | 23 个 |
| 缺陷修复 | 71 项（八轮审计） |
| 竞态检测 | 零告警 |
| 核心覆盖率 | 78~100% |
| 生产就绪 | 🟢 是 |

---

## 🇺🇸 Project Overview

**Nekop2p** (The Dual-City Dual-Chain Settlement Network) is a decentralized network system that combines P2P communication, dual-chain architecture (Bright Chain / Dark Chain), Zero-Knowledge Proofs (ZK), and a Chaotic Settlement Pool. Its core philosophy is:

> **Verifiability replaces trust** — all operations are post-hoc auditable through on-chain data, zero-knowledge proofs, or public algorithms.

### Core Features

- **Dual-City Model**: The Bright Domain (transparent, sunlit world) and Dark Domain (anonymous, shielded shelter) form a dual-track economic system.
- **Five-Layer Defense**: E2E Encryption → ZK Shielding → Onion Routing → Offline Invitation → Social Recognition.
- **Zero-Knowledge Proofs**: 6 Groth16 ZK circuits covering identity, credit, repayment, proof-of-work, door-lock, and note creation.
- **Chaotic Settlement Pool (Inkwell)**: Amount split into 3~7 fragments, randomized over 1~90 days, with source obfuscation.
- **Onion Routing**: ≥3 relay hops for end-to-end identity concealment.
- **Dual-Chain Settlement**: Bright Chain (credit/guarantee/recourse) + Dark Chain (anonymous lending/notes/door-locks).
- **Game Application Layer**: Strategy gameplay on the Bright Domain; private payment channels on the Dark Domain.

### Tech Stack

```
Language    : Go 1.26.4
Blockchain  : Cosmos SDK v0.50+ · CometBFT
Cryptography: Ed25519 · Curve25519 · ChaCha20-Poly1305 · BLAKE3
ZK Framework: gnark (Groth16) · BN254 / BLS12-381
Serialization: Protobuf · CBOR
API         : gRPC · WebSocket
Storage     : bbolt · IAVL
```

---

## 🇯🇵 プロジェクト概要

**Nekop2p（二層都市・二重チェーン清算ネットワーク）** は、P2P 通信、デュアルチェーンアーキテクチャ（ブライトチェーン/ダークチェーン）、ゼロ知識証明（ZK）、およびカオス決済プールを統合した分散型ネットワークシステムです。

> **検証可能性が信頼に代わる** — すべての操作は、オンチェーンデータ、ゼロ知識証明、または公開アルゴリズムを通じて事後監査可能です。

### 主な特徴

- **二層都市モデル**: 明域（透明で公開された陽光の世界）と暗域（匿名で遮蔽された防空壕）が二重の経済システムを形成します。
- **五層防御**: E2E 暗号化 → ZK 遮蔽 → オニオンルーティング → オフライン招待 → 社会的認識。
- **ゼロ知識証明**: 身分証明・信用・返済・作業証明・ドアロック・手形作成の 6 つの Groth16 ZK 回路。
- **カオス決済プール**: 金額を 3〜7 片に分割、1〜90 日のランダム化、ソース難読化。
- **オニオンルーティング**: 3 ホップ以上の中継でエンドツーエンドの身元秘匿を実現。

### 技術スタック

```
言語        : Go 1.26.4
ブロックチェーン : Cosmos SDK v0.50+ · CometBFT
暗号        : Ed25519 · Curve25519 · ChaCha20-Poly1305 · BLAKE3
ZK フレームワーク: gnark (Groth16) · BN254 / BLS12-381
シリアライズ    : Protobuf · CBOR
API         : gRPC · WebSocket
ストレージ    : bbolt · IAVL
```

---

## 🇰🇷 프로젝트 개요

**Nekop2p（이중 도시 · 이중 체인 청산 네트워크）** 는 P2P 통신, 듀얼 체인 아키텍처（브라이트 체인/다크 체인）, 영지식 증명（ZK）, 그리고 혼돈 결제 풀을 통합한 탈중앙화 네트워크 시스템입니다.

> **검증 가능성이 신뢰를 대체한다** — 모든 작업은 온체인 데이터, 영지식 증명, 또는 공개 알고리즘을 통해 사후 감사 가능합니다.

### 주요 기능

- **이중 도시 모델**: 명역（투명하고 공개된 태양의 세계）과 암역（익명으로 가려진 방공호）이 이중 경제 시스템을 형성합니다.
- **5중 방어**: E2E 암호화 → ZK 차폐 → 양파 라우팅 → 오프라인 초대 → 사회적 인식.
- **영지식 증명**: 신원·신용·상환·작업 증명·도어락·어음 생성을 위한 6개의 Groth16 ZK 회로.
- **혼돈 결제 풀**: 금액 3~7 조각 분할, 1~90일 무작위화, 출처 난독화.
- **양파 라우팅**: 3홉 이상 중계로 종단 간 신원 은닉.

### 기술 스택

```
언어        : Go 1.26.4
블록체인     : Cosmos SDK v0.50+ · CometBFT
암호        : Ed25519 · Curve25519 · ChaCha20-Poly1305 · BLAKE3
ZK 프레임워크 : gnark (Groth16) · BN254 / BLS12-381
직렬화       : Protobuf · CBOR
API         : gRPC · WebSocket
저장소       : bbolt · IAVL
```

---

## 📜 许可证 / License / ライセンス / 라이선스

本项目的**全部文档内容**（包括所有 `.md` 文件、设计文档与注释说明）均以 **[CC0 1.0 通用 (Creative Commons Zero v1.0 Universal)](LICENSE)** 协议发布至公有领域，任何人可自由使用、修改、分发，无需署名、无需授权。

对于项目中的**源代码**（`.go` 文件），同样适用 **CC0 1.0** 公有领域声明。您可以选择任何兼容的开源许可证来使用本项目的源代码。

> All documentation and source code in this project is dedicated to the **public domain** under the [CC0 1.0 Universal](LICENSE) license. You are free to use, modify, and distribute without attribution or permission.

> 本プロジェクトのすべての文書とソースコードは、[CC0 1.0 Universal](LICENSE) の下で**パブリックドメイン**に提供されています。帰属表示や許可なく自由に使用・改変・配布できます。

> 본 프로젝트의 모든 문서와 소스 코드는 [CC0 1.0 Universal](LICENSE)에 따라 **퍼블릭 도메인**으로 제공됩니다. 저작자 표시나 허가 없이 자유롭게 사용, 수정, 배포할 수 있습니다.

---

## 🤝 贡献 / Contributing / 貢献 / 기여

欢迎提交 Issue 和 Pull Request。请确保代码通过 `go test -race ./...` 检测。

> Contributions are welcome! Please ensure all code passes `go test -race ./...`.

---

<p align="center">
  <sub>Made with ❤️ · 可验证性取代信任 · Verifiability replaces trust</sub>
</p>
