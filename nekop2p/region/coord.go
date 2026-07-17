// Package region 区域节点共识协议 — 坐标系统 (Phase A)。
//
// 虚拟坐标派生:
//   用户坐标 = f(邀请人坐标, 信任距离, 随机角度)
//   坐标用于将用户分配到两个独立网格的区域中
//
// 双网格叠加:
//   网格1: 标准网格 (offset=0, angle=0)
//   网格2: 偏移网格 (offset≠0, angle≠0)
//   两套网格的用户集几乎完全不重叠
//
// Package region 提供虚拟空间坐标计算。
package region

import (
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"math"
	"time"
)

// ============================================================
// 坐标类型
// ============================================================

// SpatialCoord 虚拟空间坐标。
type SpatialCoord struct {
	X, Y      float64  // 虚拟坐标
	OriginID  string   // 邀请人 ChainID (坐标锚点)
	TrustDist uint8    // 到最近锚点的信任距离
	DerivedAt int64    // 坐标生成时间
}

// GridConfig 网格配置。
type GridConfig struct {
	GridSize float64 // 网格单元大小
	OffsetX  float64 // X偏移
	OffsetY  float64 // Y偏移
	Angle    float64 // 旋转角度 (弧度)
}

// DefaultGrid1 网格1配置 (标准网格)。
func DefaultGrid1() GridConfig {
	return GridConfig{
		GridSize: 1.0,
		OffsetX:  0,
		OffsetY:  0,
		Angle:    0,
	}
}

// DefaultGrid2 网格2配置 (偏移网格 — 用户集不重叠)。
func DefaultGrid2() GridConfig {
	return GridConfig{
		GridSize: 1.7,           // 黄金比例偏移 (与 1.0 最小公倍数极大)
		OffsetX:  0.5,            // 半格偏移
		OffsetY:  0.5,
		Angle:    math.Pi / 4,    // 45度旋转 (最大化分离)
	}
}

// ============================================================
// 坐标派生
// ============================================================

// DeriveCoord 从邀请人坐标派生新用户坐标。
//
// 算法:
//   angle = Hash(inviterID || inviteeID) → 归一化到 [0, 2π)
//   X = inviter.X + cos(angle) × trustDist
//   Y = inviter.Y + sin(angle) × trustDist
//
// 创世用户: 传入零值 inviter, 坐标为 (0, 0)
func DeriveCoord(inviter SpatialCoord, inviterID, inviteeID string, trustDist uint8) SpatialCoord {
	seed := sha256.Sum256([]byte(inviterID + inviteeID))
	angle := float64(binary.BigEndian.Uint64(seed[:8])) / float64(^uint64(0)) * 2 * math.Pi

	return SpatialCoord{
		X:         inviter.X + math.Cos(angle)*float64(trustDist),
		Y:         inviter.Y + math.Sin(angle)*float64(trustDist),
		OriginID:  inviter.OriginID,
		TrustDist: inviter.TrustDist + trustDist,
		DerivedAt: time.Now().Unix(),
	}
}

// GenesisCoord 返回创世坐标 (原点)。
func GenesisCoord(chainID string) SpatialCoord {
	return SpatialCoord{
		X:         0,
		Y:         0,
		OriginID:  chainID,
		TrustDist: 0,
		DerivedAt: time.Now().Unix(),
	}
}

// ============================================================
// 区域ID计算 (双网格)
// ============================================================

// RegionID 计算用户所属区域的唯一标识。
// gridIndex: 0=网格1, 1=网格2
func RegionID(coord SpatialCoord, grid GridConfig, gridIndex int) string {
	// 旋转 + 偏移
	x := (coord.X+grid.OffsetX)*math.Cos(grid.Angle) - (coord.Y+grid.OffsetY)*math.Sin(grid.Angle)
	y := (coord.X+grid.OffsetX)*math.Sin(grid.Angle) + (coord.Y+grid.OffsetY)*math.Cos(grid.Angle)

	// 计算网格坐标
	gx := int(math.Floor(x / grid.GridSize))
	gy := int(math.Floor(y / grid.GridSize))

	// 哈希生成区域ID
	h := sha256.New()
	h.Write([]byte{byte(gridIndex)})
	binary.Write(h, binary.BigEndian, int64(gx))
	binary.Write(h, binary.BigEndian, int64(gy))
	return fmt.Sprintf("R%d-%x", gridIndex, h.Sum(nil)[:4])
}

// ============================================================
// 用户-区域映射
// ============================================================

// UserRegions 返回用户的两个区域ID。
func UserRegions(coord SpatialCoord) (grid1Region, grid2Region string) {
	return RegionID(coord, DefaultGrid1(), 0),
		RegionID(coord, DefaultGrid2(), 1)
}

// ============================================================
// 安全属性验证
// ============================================================

// CheckNonOverlap 验证关键安全属性:
// 在网格1中同一区域的用户，在网格2中应分布在不同区域。
// 返回: 满足此属性的比例 (理论上应接近 1.0)
func CheckNonOverlap(users []SpatialCoord) float64 {
	if len(users) < 2 {
		return 1.0
	}

	grid1 := DefaultGrid1()
	grid2 := DefaultGrid2()

	// 按网格1区域分组
	groups := make(map[string][]SpatialCoord)
	for _, u := range users {
		r1 := RegionID(u, grid1, 0)
		groups[r1] = append(groups[r1], u)
	}

	// 检查: 同一网格1区域的用户，在网格2中应分散
	total := 0
	satisfied := 0
	for _, group := range groups {
		if len(group) < 2 {
			continue
		}
		// 检查组内每对用户
		for i := 0; i < len(group); i++ {
			for j := i + 1; j < len(group); j++ {
				total++
				r2_i := RegionID(group[i], grid2, 1)
				r2_j := RegionID(group[j], grid2, 1)
				if r2_i != r2_j {
					satisfied++
				}
			}
		}
	}

	if total == 0 {
		return 1.0
	}
	return float64(satisfied) / float64(total)
}
