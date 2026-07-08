package social

import (
	"testing"
)

func TestCreateGuild(t *testing.T) {
	gm := NewGuildManager()

	g, err := gm.CreateGuild("DragonSlayers", "leader-1")
	if err != nil {
		t.Fatalf("CreateGuild failed: %v", err)
	}
	if g.Name != "DragonSlayers" {
		t.Fatalf("expected DragonSlayers, got %s", g.Name)
	}
	if g.LeaderID != "leader-1" {
		t.Fatalf("expected leader-1, got %s", g.LeaderID)
	}

	_, err = gm.CreateGuild("ab", "leader-2")
	if err == nil {
		t.Fatal("expected error for name too short")
	}

	_, err = gm.CreateGuild("DragonSlayers", "leader-2")
	if err == nil {
		t.Fatal("expected error for duplicate guild name")
	}

	_, err = gm.CreateGuild("NewGuild", "leader-1")
	if err == nil {
		t.Fatal("expected error for leader already in guild")
	}
}

func TestJoinGuild(t *testing.T) {
	gm := NewGuildManager()
	gm.CreateGuild("TestGuild", "leader-1")

	err := gm.JoinGuild(gm.GetPlayerGuild("leader-1").GuildID, "player-2")
	if err != nil {
		t.Fatalf("JoinGuild failed: %v", err)
	}

	err = gm.JoinGuild(gm.GetPlayerGuild("leader-1").GuildID, "player-2")
	if err == nil {
		t.Fatal("expected error for already-a-member")
	}

	err = gm.JoinGuild(gm.GetPlayerGuild("leader-1").GuildID, "leader-1")
	if err == nil {
		t.Fatal("expected error for leader re-join")
	}
}

func TestTransferLeadership(t *testing.T) {
	gm := NewGuildManager()
	gm.CreateGuild("TestGuild", "leader-1")
	guildID := gm.GetPlayerGuild("leader-1").GuildID
	gm.JoinGuild(guildID, "member-1")

	err := gm.TransferLeadership(guildID, "leader-1", "member-1")
	if err != nil {
		t.Fatalf("TransferLeadership failed: %v", err)
	}

	g := gm.GetGuild(guildID)
	if g.LeaderID != "member-1" {
		t.Fatalf("expected member-1 as leader, got %s", g.LeaderID)
	}

	err = gm.TransferLeadership(guildID, "leader-1", "member-1")
	if err == nil {
		t.Fatal("expected error: old leader no longer has authority")
	}
}

func TestAddGuildSkill(t *testing.T) {
	gm := NewGuildManager()
	gm.CreateGuild("TestGuild", "leader-1")
	guildID := gm.GetPlayerGuild("leader-1").GuildID

	err := gm.AddGuildSkill(guildID, "leader-1", "Fireball", "AoE damage")
	if err != nil {
		t.Fatalf("AddGuildSkill failed: %v", err)
	}

	skills := gm.GetGuildSkills(guildID)
	if len(skills) != 1 {
		t.Fatalf("expected 1 skill, got %d", len(skills))
	}
	if skills[0].Name != "Fireball" {
		t.Fatalf("expected Fireball, got %s", skills[0].Name)
	}

	err = gm.AddGuildSkill(guildID, "member-1", "IceBlast", "Slow")
	if err == nil {
		t.Fatal("expected error: non-leader cannot add skills")
	}
}

func TestLevelUpGuildSkill(t *testing.T) {
	gm := NewGuildManager()
	gm.CreateGuild("TestGuild", "leader-1")
	guildID := gm.GetPlayerGuild("leader-1").GuildID
	gm.AddGuildSkill(guildID, "leader-1", "Fireball", "AoE")

	skills := gm.GetGuildSkills(guildID)
	skillID := skills[0].SkillID

	err := gm.LevelUpGuildSkill(guildID, "leader-1", skillID)
	if err == nil {
		t.Fatal("expected error: insufficient XP")
	}

	gm.AddXP(guildID, 5000)

	err = gm.LevelUpGuildSkill(guildID, "leader-1", skillID)
	if err != nil {
		t.Fatalf("LevelUpGuildSkill failed: %v", err)
	}

	skills = gm.GetGuildSkills(guildID)
	if skills[0].Level != 2 {
		t.Fatalf("expected level 2, got %d", skills[0].Level)
	}
}

func TestAddXP(t *testing.T) {
	gm := NewGuildManager()
	gm.CreateGuild("TestGuild", "leader-1")
	guildID := gm.GetPlayerGuild("leader-1").GuildID

	err := gm.AddXP(guildID, 500)
	if err != nil {
		t.Fatalf("AddXP failed: %v", err)
	}

	g := gm.GetGuild(guildID)
	if g.XP != 500 {
		t.Fatalf("expected 500 XP, got %d", g.XP)
	}

	err = gm.AddXP("nonexistent", 100)
	if err == nil {
		t.Fatal("expected error for nonexistent guild")
	}

	gm.AddXP(guildID, 15000)
	g = gm.GetGuild(guildID)
	if g.Level < 2 {
		t.Fatalf("expected level >= 2, got %d", g.Level)
	}
}
