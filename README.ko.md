# 🏙️ 이중 도시 · 이중 체인 청산 네트워크

**Nekop2p — The Dual-Chain Settlement Network**

> **검증 가능성이 신뢰를 대체한다** — 모든 작업은 온체인 데이터, 영지식 증명, 또는 공개 알고리즘을 통해 사후 감사 가능합니다.

---

## 개요

**Nekop2p（이중 도시 · 이중 체인 청산 네트워크）** 는 P2P 통신, 듀얼 체인 아키텍처（브라이트 체인／다크 체인）, 영지식 증명（ZK）, 그리고 혼돈 결제 풀을 통합한 탈중앙화 네트워크 시스템입니다.

그 핵심 철학은 **암호학적 검증 가능성**이 전통적인 사회적 신뢰 메커니즘을 대체한다는 것입니다. Nekop2p에서 노드는 신뢰되지 않으며, 신뢰는 오직 암호 키 보유자 사이에만 존재합니다.

---

## 주요 기능

| 기능 | 설명 |
|:---|:---|
| 🔗 이중 도시 모델 | 명역（투명하고 공개된 태양의 세계）과 암역（익명으로 가려진 방공호）이 이중 경제 시스템을 형성 |
| 🛡️ 5중 방어 | E2E 암호화 → ZK 차폐 → 양파 라우팅 → 오프라인 초대 → 사회적 인식 |
| 🔐 영지식 증명 | 6개의 Groth16 ZK 회로（신원／신용／상환／작업 증명／도어락／어음） |
| 🌀 혼돈 결제 풀 | 금액 3~7 조각 분할, 1~90일 무작위화, 출처 난독화 |
| 🧅 양파 라우팅 | 3홉 이상 중계, 종단 간 신원 은닉 |
| 💱 이중 체인 청산 | 브라이트 체인（신용／보증／구상）+ 다크 체인（익명 대출／어음／도어락） |
| 🎮 게임 애플리케이션 계층 | 명역상 전략 게임 플레이, 암역상 프라이버시 결제 채널 |
| 📡 P2P 통신 계층 | Noise IK/NK 핸드셰이크 · Signal 더블 래칫 · ChaCha20-Poly1305 프레임 암호화 |

---

## 기술 스택

```
언어          : Go 1.26.4
블록체인       : Cosmos SDK v0.50+ · CometBFT
암호          : Ed25519 · Curve25519 · ChaCha20-Poly1305 · BLAKE3
ZK 프레임워크  : gnark (Groth16) · BN254 / BLS12-381
직렬화         : Protobuf · CBOR
API           : gRPC · WebSocket
저장소         : bbolt · IAVL
```

---

## 모듈 구성（29 패키지）

```
nekop2p/
├── anon/         → 3채널 익명 전환（명도/암도/방공호）
├── app/          → ABCI 글루 계층
├── beacon/       → 암호화 비컨 홍수 발견
├── cmd/          → 진입 프로그램（neko-node / observer / neko-demo）
├── config/       → 노드 설정
├── consensus/    → BFT 합의 엔진（95.8% 커버리지）
├── crypto/       → 암호 프리미티브
├── dark/         → 암역 코어（거래/신용 어음/익명 신원）
├── frame/        → TCP 전송 프레임 암호화
├── inkwell/      → 혼돈 결제 풀
├── intro/        → 오프라인 초대 자격 증명
├── keystore/     → 듀얼 키 페어 관리
├── localapi/     → 로컬 gRPC/WebSocket API
├── node/         → 노드 주제어（삼환 라우팅 토폴로지）
├── noise/        → Noise IK/NK 핸드셰이크 프로토콜
├── onion/        → 양파 라우팅
├── peer/         → 피어 연결
├── proto/        → Protobuf 정의
├── randbeacon/   → 분산 랜덤 비컨
├── ratchet/      → Signal 더블 래칫
├── store/        → 체인 상태 영속화
├── x/brightchain/ → 브라이트 체인 모듈
├── x/darkchain/  → 다크 체인 모듈
├── x/node/       → 노드 노동 시장
├── x/zk/         → ZK 증명 온체인 검증
└── zkcircuits/   → 6개의 Groth16 ZK 회로
```

---

## 빠른 시작

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

## 프로젝트 현황

| 지표 | 데이터 |
|:---|:---|
| 버전 | 3.0.0 |
| 소스 파일 | 92 |
| 테스트 파일 | 23 |
| 수정된 결함 | 71（8회 감사） |
| 경합 탐지 | 제로 알림 |
| 코어 커버리지 | 78~100% |
| 프로덕션 준비 | 🟢 예 |

---

## 라이선스

본 프로젝트의 모든 콘텐츠（문서, 설계, 소스 코드）는 **CC0 1.0 Universal**에 따라 **퍼블릭 도메인**으로 제공됩니다. 저작자 표시나 허가 없이 자유롭게 사용, 수정, 배포할 수 있습니다.

자세한 내용은 [LICENSE](./LICENSE)를 참조하십시오.

---

<p align="center"><sub>검증 가능성이 신뢰를 대체한다 · Verifiability replaces trust</sub></p>
