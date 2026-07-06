# 🏙️ Nekop2p — The Dual-Chain Settlement Network

> **Verifiability replaces trust** — all operations are post-hoc auditable through on-chain data, zero-knowledge proofs, or public algorithms.

---

## Overview

**Nekop2p** (The Dual-City Dual-Chain Settlement Network) is a decentralized network system that combines P2P communication, a dual-chain architecture (Bright Chain / Dark Chain), Zero-Knowledge Proofs (ZK), and a Chaotic Settlement Pool.

Its core philosophy is that **cryptographic verifiability** replaces traditional social trust mechanisms. In Nekop2p, nodes are not trusted — trust exists only between cryptographic key holders.

---

## Core Features

| Feature | Description |
|:---|:---|
| 🔗 Dual-City Model | Bright Domain (transparent, sunlit world) + Dark Domain (anonymous, shielded shelter) form a dual-track economy |
| 🛡️ Five-Layer Defense | E2E Encryption → ZK Shielding → Onion Routing → Offline Invitation → Social Recognition |
| 🔐 Zero-Knowledge Proofs | 6 Groth16 ZK circuits (Identity / Credit / Repayment / PoW / Door-lock / Notes) |
| 🌀 Chaotic Settlement Pool | Amount split into 3~7 fragments, randomized over 1~90 days, source obfuscation |
| 🧅 Onion Routing | ≥3 relay hops for end-to-end identity concealment |
| 💱 Dual-Chain Settlement | Bright Chain (credit/guarantee/recourse) + Dark Chain (anonymous lending/notes/door-locks) |
| 🎮 Game Application Layer | Strategy gameplay on Bright Domain; private payment channels on Dark Domain |
| 📡 P2P Communication | Noise IK/NK handshake · Signal double-ratchet · ChaCha20-Poly1305 frame encryption |

---

## Tech Stack

```
Language     : Go 1.26.4
Blockchain   : Cosmos SDK v0.50+ · CometBFT
Cryptography : Ed25519 · Curve25519 · ChaCha20-Poly1305 · BLAKE3
ZK Framework : gnark (Groth16) · BN254 / BLS12-381
Serialization: Protobuf · CBOR
API          : gRPC · WebSocket
Storage      : bbolt · IAVL
```

---

## Module Structure (29 Packages)

```
nekop2p/
├── anon/         → Three-channel anonymous switching (Bright/Dark/Bunker)
├── app/          → ABCI glue layer
├── beacon/       → Encrypted beacon flood discovery
├── cmd/          → Entry programs (neko-node / observer / neko-demo)
├── config/       → Node configuration
├── consensus/    → BFT consensus engine (95.8% coverage)
├── crypto/       → Cryptographic primitives
├── dark/         → Dark domain core (transactions/credit notes/anonymous identity)
├── frame/        → TCP transport frame encryption
├── inkwell/      → Chaotic settlement pool
├── intro/        → Offline invitation credentials
├── keystore/     → Dual key-pair management
├── localapi/     → Local gRPC/WebSocket API
├── node/         → Node main control (three-ring routing topology)
├── noise/        → Noise IK/NK handshake protocol
├── onion/        → Onion routing
├── peer/         → Peer connections
├── proto/        → Protobuf definitions
├── randbeacon/   → Distributed random beacon
├── ratchet/      → Signal double-ratchet
├── store/        → Chain state persistence
├── x/brightchain/ → Bright Chain module
├── x/darkchain/  → Dark Chain module
├── x/node/       → Node labor market
├── x/zk/         → ZK proof on-chain verification
└── zkcircuits/   → 6 Groth16 ZK circuits
```

---

## Quick Start

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

## Project Status

| Metric | Value |
|:---|:---|
| Version | 3.0.0 |
| Source Files | 92 |
| Test Files | 23 |
| Defects Fixed | 71 (8 audit rounds) |
| Race Detection | Zero alerts |
| Core Coverage | 78~100% |
| Production Ready | 🟢 Yes |

---

## License

All contents of this project (documentation, design, source code) are dedicated to the **public domain** under the **CC0 1.0 Universal** license. You are free to use, modify, and distribute without attribution or permission.

See [LICENSE](./LICENSE) for details.

---

<p align="center"><sub>Verifiability replaces trust · 可验证性取代信任</sub></p>
