package social

import (
	"crypto/sha256"
	"fmt"
	"sync"
	"time"
)

type MemberRole int

const (
	RoleLeader  MemberRole = 0
	RoleOfficer MemberRole = 1
	RoleMember  MemberRole = 2
)

type Guild struct {
	GuildID   string
	Name      string
	LeaderID  string
	Members   []*GuildMember
	Treasury  map[string]uint64
	Level     uint32
	XP        uint64
	Skills    []*GuildSkill
	CreatedAt int64
	Proposals []*GuildProposal
}

type GuildMember struct {
	PlayerID     string
	Role         MemberRole
	JoinedAt     int64
	Contribution uint64
}

type GuildSkill struct {
	SkillID string
	Name    string
	Level   uint32
	Effect  string
}

type GuildProposal struct {
	ProposalID string
	Type       string
	ProposerID string
	TargetID   string
	Data       map[string]interface{}
	Votes      map[string]bool
	CreatedAt  int64
	ExpiresAt  int64
	Executed   bool
}

type GuildManager struct {
	mu       sync.RWMutex
	guilds   map[string]*Guild
	byPlayer map[string]string
}

func NewGuildManager() *GuildManager {
	return &GuildManager{
		guilds:   make(map[string]*Guild),
		byPlayer: make(map[string]string),
	}
}

func (gm *GuildManager) CreateGuild(name, leaderID string) (*Guild, error) {
	gm.mu.Lock()
	defer gm.mu.Unlock()

	if name == "" {
		return nil, fmt.Errorf("social: guild name cannot be empty")
	}
	if leaderID == "" {
		return nil, fmt.Errorf("social: leader ID cannot be empty")
	}
	if _, exists := gm.byPlayer[leaderID]; exists {
		return nil, fmt.Errorf("social: player %s is already in a guild", leaderID)
	}
	if len(name) < 3 || len(name) > 32 {
		return nil, fmt.Errorf("social: guild name must be 3-32 characters, got %d", len(name))
	}
	for _, g := range gm.guilds {
		if g.Name == name {
			return nil, fmt.Errorf("social: guild name %q is already taken", name)
		}
	}

	now := time.Now().Unix()
	guildID := generateID("guild-" + name)

	g := &Guild{
		GuildID:   guildID,
		Name:      name,
		LeaderID:  leaderID,
		Members:   make([]*GuildMember, 0),
		Treasury:  make(map[string]uint64),
		Level:     1,
		XP:        0,
		Skills:    make([]*GuildSkill, 0),
		CreatedAt: now,
		Proposals: make([]*GuildProposal, 0),
	}

	g.Members = append(g.Members, &GuildMember{
		PlayerID:     leaderID,
		Role:         RoleLeader,
		JoinedAt:     now,
		Contribution: 0,
	})

	gm.guilds[guildID] = g
	gm.byPlayer[leaderID] = guildID

	return g, nil
}

func (gm *GuildManager) JoinGuild(guildID, playerID string) error {
	gm.mu.Lock()
	defer gm.mu.Unlock()

	g, ok := gm.guilds[guildID]
	if !ok {
		return fmt.Errorf("social: guild %s not found", guildID)
	}
	if _, exists := gm.byPlayer[playerID]; exists {
		return fmt.Errorf("social: player %s is already in a guild", playerID)
	}

	for _, m := range g.Members {
		if m.PlayerID == playerID {
			return fmt.Errorf("social: player %s is already a member of guild %s", playerID, guildID)
		}
	}

	g.Members = append(g.Members, &GuildMember{
		PlayerID:     playerID,
		Role:         RoleMember,
		JoinedAt:     time.Now().Unix(),
		Contribution: 0,
	})
	gm.byPlayer[playerID] = guildID

	return nil
}

func (gm *GuildManager) LeaveGuild(guildID, playerID string) error {
	gm.mu.Lock()
	defer gm.mu.Unlock()

	g, ok := gm.guilds[guildID]
	if !ok {
		return fmt.Errorf("social: guild %s not found", guildID)
	}
	if g.LeaderID == playerID {
		return fmt.Errorf("social: leader cannot leave guild, must disband or transfer leadership first")
	}

	idx := -1
	for i, m := range g.Members {
		if m.PlayerID == playerID {
			idx = i
			break
		}
	}
	if idx == -1 {
		return fmt.Errorf("social: player %s is not a member of guild %s", playerID, guildID)
	}

	g.Members = append(g.Members[:idx], g.Members[idx+1:]...)
	delete(gm.byPlayer, playerID)

	return nil
}

func (gm *GuildManager) DisbandGuild(guildID, leaderID string) error {
	gm.mu.Lock()
	defer gm.mu.Unlock()

	g, ok := gm.guilds[guildID]
	if !ok {
		return fmt.Errorf("social: guild %s not found", guildID)
	}
	if g.LeaderID != leaderID {
		return fmt.Errorf("social: only the guild leader can disband the guild")
	}

	for _, m := range g.Members {
		delete(gm.byPlayer, m.PlayerID)
	}
	delete(gm.guilds, guildID)

	return nil
}

func (gm *GuildManager) TransferLeadership(guildID, currentLeaderID, newLeaderID string) error {
	gm.mu.Lock()
	defer gm.mu.Unlock()

	g, ok := gm.guilds[guildID]
	if !ok {
		return fmt.Errorf("social: guild %s not found", guildID)
	}
	if g.LeaderID != currentLeaderID {
		return fmt.Errorf("social: only the current guild leader can transfer leadership")
	}
	if currentLeaderID == newLeaderID {
		return fmt.Errorf("social: cannot transfer leadership to yourself")
	}

	var leader *GuildMember
	var newLeader *GuildMember
	for _, m := range g.Members {
		if m.PlayerID == currentLeaderID {
			leader = m
		}
		if m.PlayerID == newLeaderID {
			newLeader = m
		}
	}
	if leader == nil {
		return fmt.Errorf("social: current leader %s is not a member of guild %s", currentLeaderID, guildID)
	}
	if newLeader == nil {
		return fmt.Errorf("social: new leader %s is not a member of guild %s", newLeaderID, guildID)
	}

	leader.Role = RoleMember
	newLeader.Role = RoleLeader
	g.LeaderID = newLeaderID

	return nil
}

func (gm *GuildManager) PromoteMember(guildID, promoterID, targetID string, newRole MemberRole) error {
	gm.mu.Lock()
	defer gm.mu.Unlock()

	g, ok := gm.guilds[guildID]
	if !ok {
		return fmt.Errorf("social: guild %s not found", guildID)
	}
	if promoterID == targetID {
		return fmt.Errorf("social: cannot promote yourself")
	}
	if newRole < RoleLeader || newRole > RoleMember {
		return fmt.Errorf("social: invalid role %d", newRole)
	}
	if newRole == RoleLeader {
		return fmt.Errorf("social: cannot promote to leader, use TransferLeadership instead")
	}

	var promoterRole MemberRole = -1
	var target *GuildMember
	for _, m := range g.Members {
		if m.PlayerID == promoterID {
			promoterRole = m.Role
		}
		if m.PlayerID == targetID {
			target = m
		}
	}

	if promoterRole == -1 {
		return fmt.Errorf("social: promoter %s is not a member of guild %s", promoterID, guildID)
	}
	if target == nil {
		return fmt.Errorf("social: target %s is not a member of guild %s", targetID, guildID)
	}
	if target.Role == RoleLeader {
		return fmt.Errorf("social: cannot demote the guild leader")
	}
	if promoterRole != RoleLeader && promoterRole != RoleOfficer {
		return fmt.Errorf("social: only leader or officer can promote members")
	}
	if promoterRole == RoleOfficer && target.Role <= RoleOfficer {
		return fmt.Errorf("social: officer can only promote regular members")
	}

	target.Role = newRole
	return nil
}

func (gm *GuildManager) KickMember(guildID, kickerID, targetID string) error {
	gm.mu.Lock()
	defer gm.mu.Unlock()

	g, ok := gm.guilds[guildID]
	if !ok {
		return fmt.Errorf("social: guild %s not found", guildID)
	}
	if kickerID == targetID {
		return fmt.Errorf("social: cannot kick yourself")
	}

	var kickerRole MemberRole = -1
	var targetIdx int = -1
	var targetRole MemberRole = -1
	for i, m := range g.Members {
		if m.PlayerID == kickerID {
			kickerRole = m.Role
		}
		if m.PlayerID == targetID {
			targetIdx = i
			targetRole = m.Role
		}
	}

	if kickerRole == -1 {
		return fmt.Errorf("social: kicker %s is not a member of guild %s", kickerID, guildID)
	}
	if targetIdx == -1 {
		return fmt.Errorf("social: target %s is not a member of guild %s", targetID, guildID)
	}
	if targetRole == RoleLeader {
		return fmt.Errorf("social: cannot kick the guild leader")
	}
	if kickerRole != RoleLeader && kickerRole != RoleOfficer {
		return fmt.Errorf("social: only leader or officer can kick members")
	}
	if kickerRole == RoleOfficer && targetRole <= RoleOfficer {
		return fmt.Errorf("social: officer can only kick regular members")
	}

	g.Members = append(g.Members[:targetIdx], g.Members[targetIdx+1:]...)
	delete(gm.byPlayer, targetID)

	return nil
}

func (gm *GuildManager) DepositToTreasury(guildID, playerID string, assetID string, quantity uint64) error {
	gm.mu.Lock()
	defer gm.mu.Unlock()

	g, ok := gm.guilds[guildID]
	if !ok {
		return fmt.Errorf("social: guild %s not found", guildID)
	}
	if quantity == 0 {
		return fmt.Errorf("social: deposit quantity must be positive")
	}

	found := false
	for _, m := range g.Members {
		if m.PlayerID == playerID {
			found = true
			break
		}
	}
	if !found {
		return fmt.Errorf("social: player %s is not a member of guild %s", playerID, guildID)
	}

	g.Treasury[assetID] += quantity
	return nil
}

func (gm *GuildManager) WithdrawFromTreasury(guildID, requesterID string, assetID string, quantity uint64) error {
	gm.mu.Lock()
	defer gm.mu.Unlock()

	g, ok := gm.guilds[guildID]
	if !ok {
		return fmt.Errorf("social: guild %s not found", guildID)
	}
	if quantity == 0 {
		return fmt.Errorf("social: withdraw quantity must be positive")
	}

	currentQty, exists := g.Treasury[assetID]
	if !exists || currentQty < quantity {
		return fmt.Errorf("social: insufficient treasury balance for %s: have %d, need %d", assetID, currentQty, quantity)
	}

	var requesterRole MemberRole = -1
	for _, m := range g.Members {
		if m.PlayerID == requesterID {
			requesterRole = m.Role
			break
		}
	}
	if requesterRole == -1 {
		return fmt.Errorf("social: requester %s is not a member of guild %s", requesterID, guildID)
	}

	if requesterRole != RoleLeader && requesterRole != RoleOfficer {
		return fmt.Errorf("social: only leader or officer can withdraw from treasury directly; use a proposal for member withdrawals")
	}

	g.Treasury[assetID] -= quantity
	if g.Treasury[assetID] == 0 {
		delete(g.Treasury, assetID)
	}

	return nil
}

func (gm *GuildManager) CreateProposal(guildID, proposerID string, proposalType, targetID string, data map[string]interface{}) (*GuildProposal, error) {
	gm.mu.Lock()
	defer gm.mu.Unlock()

	g, ok := gm.guilds[guildID]
	if !ok {
		return nil, fmt.Errorf("social: guild %s not found", guildID)
	}

	found := false
	for _, m := range g.Members {
		if m.PlayerID == proposerID {
			found = true
			break
		}
	}
	if !found {
		return nil, fmt.Errorf("social: proposer %s is not a member of guild %s", proposerID, guildID)
	}

	now := time.Now().Unix()
	proposalID := generateID("proposal-" + guildID)

	p := &GuildProposal{
		ProposalID: proposalID,
		Type:       proposalType,
		ProposerID: proposerID,
		TargetID:   targetID,
		Data:       data,
		Votes:      make(map[string]bool),
		CreatedAt:  now,
		ExpiresAt:  now + 86400,
		Executed:   false,
	}

	if data == nil {
		p.Data = make(map[string]interface{})
	}

	p.Votes[proposerID] = true

	g.Proposals = append(g.Proposals, p)
	return p, nil
}

func (gm *GuildManager) VoteOnProposal(guildID, playerID, proposalID string, approve bool) error {
	gm.mu.Lock()
	defer gm.mu.Unlock()

	g, ok := gm.guilds[guildID]
	if !ok {
		return fmt.Errorf("social: guild %s not found", guildID)
	}

	found := false
	for _, m := range g.Members {
		if m.PlayerID == playerID {
			found = true
			break
		}
	}
	if !found {
		return fmt.Errorf("social: voter %s is not a member of guild %s", playerID, guildID)
	}

	var proposal *GuildProposal
	for _, p := range g.Proposals {
		if p.ProposalID == proposalID {
			proposal = p
			break
		}
	}
	if proposal == nil {
		return fmt.Errorf("social: proposal %s not found", proposalID)
	}
	if proposal.Executed {
		return fmt.Errorf("social: proposal %s has already been executed", proposalID)
	}
	if time.Now().Unix() > proposal.ExpiresAt {
		return fmt.Errorf("social: proposal %s has expired", proposalID)
	}

	proposal.Votes[playerID] = approve
	return nil
}

func (gm *GuildManager) ExecuteProposal(guildID, proposalID string) error {
	gm.mu.Lock()
	defer gm.mu.Unlock()

	g, ok := gm.guilds[guildID]
	if !ok {
		return fmt.Errorf("social: guild %s not found", guildID)
	}

	var proposal *GuildProposal
	for _, p := range g.Proposals {
		if p.ProposalID == proposalID {
			proposal = p
			break
		}
	}
	if proposal == nil {
		return fmt.Errorf("social: proposal %s not found", proposalID)
	}
	if proposal.Executed {
		return fmt.Errorf("social: proposal %s has already been executed", proposalID)
	}
	if time.Now().Unix() > proposal.ExpiresAt {
		return fmt.Errorf("social: proposal %s has expired", proposalID)
	}

	approveCount := 0
	for _, v := range proposal.Votes {
		if v {
			approveCount++
		}
	}

	majority := len(g.Members)/2 + 1
	if approveCount < majority {
		return fmt.Errorf("social: proposal %s does not have majority approval: %d/%d", proposalID, approveCount, len(g.Members))
	}

	switch proposal.Type {
	case "KICK_MEMBER":
		targetID := proposal.TargetID
		targetIdx := -1
		for i, m := range g.Members {
			if m.PlayerID == targetID {
				targetIdx = i
				break
			}
		}
		if targetIdx == -1 {
			return fmt.Errorf("social: kick target %s is not a member", targetID)
		}
		if g.Members[targetIdx].Role == RoleLeader {
			return fmt.Errorf("social: cannot kick the guild leader by proposal")
		}
		g.Members = append(g.Members[:targetIdx], g.Members[targetIdx+1:]...)
		delete(gm.byPlayer, targetID)

	case "PROMOTE":
		newRoleInt, ok := proposal.Data["new_role"]
		if !ok {
			return fmt.Errorf("social: promote proposal missing new_role")
		}
		nr, ok := newRoleInt.(int)
		if !ok {
			return fmt.Errorf("social: invalid new_role type")
		}
		newRole := MemberRole(nr)
		if newRole < RoleLeader || newRole > RoleMember || newRole == RoleLeader {
			return fmt.Errorf("social: invalid target role %d", newRole)
		}
		var target *GuildMember
		for _, m := range g.Members {
			if m.PlayerID == proposal.TargetID {
				target = m
				break
			}
		}
		if target == nil {
			return fmt.Errorf("social: promote target %s is not a member", proposal.TargetID)
		}
		if target.Role == RoleLeader {
			return fmt.Errorf("social: cannot demote leader by proposal")
		}
		target.Role = newRole

	case "SPEND_TREASURY":
		assetID, _ := proposal.Data["asset_id"].(string)
		qtyFloat, _ := proposal.Data["quantity"].(float64)
		quantity := uint64(qtyFloat)
		if assetID == "" || quantity == 0 {
			return fmt.Errorf("social: invalid treasury spend data")
		}
		currentQty, exists := g.Treasury[assetID]
		if !exists || currentQty < quantity {
			return fmt.Errorf("social: insufficient treasury balance for %s: have %d, need %d", assetID, currentQty, quantity)
		}
		g.Treasury[assetID] -= quantity
		if g.Treasury[assetID] == 0 {
			delete(g.Treasury, assetID)
		}

	case "CHANGE_NAME":
		newName, ok := proposal.Data["new_name"].(string)
		if !ok || newName == "" {
			return fmt.Errorf("social: invalid new name for guild")
		}
		g.Name = newName

	default:
		return fmt.Errorf("social: unknown proposal type: %s", proposal.Type)
	}

	proposal.Executed = true
	return nil
}

func (gm *GuildManager) GetGuild(guildID string) *Guild {
	gm.mu.RLock()
	defer gm.mu.RUnlock()
	return gm.guilds[guildID]
}

func (gm *GuildManager) GetPlayerGuild(playerID string) *Guild {
	gm.mu.RLock()
	defer gm.mu.RUnlock()
	guildID, ok := gm.byPlayer[playerID]
	if !ok {
		return nil
	}
	return gm.guilds[guildID]
}

func (gm *GuildManager) AddXP(guildID string, xp uint64) error {
	gm.mu.Lock()
	defer gm.mu.Unlock()

	g, ok := gm.guilds[guildID]
	if !ok {
		return fmt.Errorf("social: guild %s not found", guildID)
	}

	g.XP += xp

	for {
		xpNeeded := uint64(g.Level) * 10000
		if g.XP < xpNeeded {
			break
		}
		g.XP -= xpNeeded
		g.Level++
	}
	return nil
}

func (gm *GuildManager) AddGuildSkill(guildID, leaderID, skillName, effect string) error {
	gm.mu.Lock()
	defer gm.mu.Unlock()

	g, ok := gm.guilds[guildID]
	if !ok {
		return fmt.Errorf("social: guild %s not found", guildID)
	}
	if g.LeaderID != leaderID {
		return fmt.Errorf("social: only the guild leader can add skills")
	}
	if skillName == "" {
		return fmt.Errorf("social: skill name cannot be empty")
	}

	skillID := generateID("skill-" + guildID)
	g.Skills = append(g.Skills, &GuildSkill{
		SkillID: skillID,
		Name:    skillName,
		Level:   1,
		Effect:  effect,
	})
	return nil
}

func (gm *GuildManager) LevelUpGuildSkill(guildID, leaderID, skillID string) error {
	gm.mu.Lock()
	defer gm.mu.Unlock()

	g, ok := gm.guilds[guildID]
	if !ok {
		return fmt.Errorf("social: guild %s not found", guildID)
	}
	if g.LeaderID != leaderID {
		return fmt.Errorf("social: only the guild leader can level up skills")
	}

	xpCost := uint64(5000)
	if g.XP < xpCost {
		return fmt.Errorf("social: insufficient guild XP: have %d, need %d", g.XP, xpCost)
	}

	for _, s := range g.Skills {
		if s.SkillID == skillID {
			g.XP -= xpCost
			s.Level++
			return nil
		}
	}
	return fmt.Errorf("social: skill %s not found in guild %s", skillID, guildID)
}

func (gm *GuildManager) GetGuildSkills(guildID string) []*GuildSkill {
	gm.mu.RLock()
	defer gm.mu.RUnlock()

	g, ok := gm.guilds[guildID]
	if !ok {
		return nil
	}
	result := make([]*GuildSkill, len(g.Skills))
	copy(result, g.Skills)
	return result
}

func (gm *GuildManager) ListGuilds() []*Guild {
	gm.mu.RLock()
	defer gm.mu.RUnlock()

	result := make([]*Guild, 0, len(gm.guilds))
	for _, g := range gm.guilds {
		result = append(result, g)
	}
	return result
}

func generateID(prefix string) string {
	hash := sha256.Sum256([]byte(fmt.Sprintf("%s-%d", prefix, time.Now().UnixNano())))
	return fmt.Sprintf("%x", hash[:8])
}
