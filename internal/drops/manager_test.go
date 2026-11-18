package drops

import (
	"testing"

	"kickautodrops/internal/kick"
	"kickautodrops/internal/log"
)

func TestMain(m *testing.M) {
	// Keep test output readable; manager methods log every credit.
	log.SetLogger(func(string, ...any) {})
	m.Run()
}

func testManager() *Manager {
	return NewManager(&kick.CampaignsResponse{Data: []kick.Campaign{
		{
			ID:       "rust-general",
			Name:     "Rust general drops",
			Status:   "active",
			Category: kick.Category{ID: 13, Name: "Rust"},
			Rewards: []kick.Reward{
				{ID: "r1", Name: "First Rust reward", RequiredUnits: 120},
				{ID: "r2", Name: "Second Rust reward", RequiredUnits: 240},
			},
		},
		{
			ID:       "other-general",
			Name:     "Other general drops",
			Status:   "active",
			Category: kick.Category{ID: 99, Name: "Other"},
			Rewards: []kick.Reward{
				{ID: "r3", Name: "Other reward", RequiredUnits: 30},
			},
		},
		{
			ID:       "rust-streamers",
			Name:     "Rust streamer drops",
			Status:   "active",
			Category: kick.Category{ID: 13, Name: "Rust"},
			Channels: []kick.Channel{{Slug: "alice"}},
			Rewards: []kick.Reward{
				{ID: "r4", Name: "Streamer reward", RequiredUnits: 60},
			},
		},
		{
			ID:       "expired-general",
			Name:     "Expired general drops",
			Status:   "expired",
			Category: kick.Category{ID: 13, Name: "Rust"},
			Rewards: []kick.Reward{
				{ID: "r5", Name: "Expired reward", RequiredUnits: 10},
			},
		},
	}})
}

func TestNewManagerClassifiesCategoryWideRewards(t *testing.T) {
	rewards := testManager().GetRewards()
	if len(rewards) != 4 {
		t.Fatalf("got %d rewards, want 4 (expired campaign must be skipped)", len(rewards))
	}
	for _, r := range rewards {
		switch r.ID {
		case "r1", "r2", "r3":
			if r.Type != 2 {
				t.Errorf("reward %s: type = %d, want 2 (general)", r.ID, r.Type)
			}
		case "r4":
			if r.Type != 1 {
				t.Errorf("reward %s: type = %d, want 1 (streamer)", r.ID, r.Type)
			}
		default:
			t.Errorf("unexpected reward %s", r.ID)
		}
	}
}

func TestGeneralCategoriesAndRemaining(t *testing.T) {
	m := testManager()

	categories := m.GeneralCategories()
	if len(categories) != 2 || categories[0] != 13 || categories[1] != 99 {
		t.Fatalf("categories = %v, want [13 99]", categories)
	}

	// Watching one Rust stream advances both Rust general rewards, so the
	// session must last until the longest unclaimed reward is done.
	if got, want := m.GeneralRemainingSeconds(13), 240*60; got != want {
		t.Errorf("Rust remaining = %d, want %d", got, want)
	}
	if got, want := m.GeneralRemainingSeconds(99), 30*60; got != want {
		t.Errorf("other remaining = %d, want %d", got, want)
	}

	m.rewards[0].Claimed = true
	if got, want := m.GeneralRemainingSeconds(13), 240*60; got != want {
		t.Errorf("Rust remaining after first reward claimed = %d, want %d", got, want)
	}
	m.rewards[1].Claimed = true
	if got := m.GeneralRemainingSeconds(13); got != 0 {
		t.Errorf("Rust remaining after all rewards claimed = %d, want 0", got)
	}
	if got := m.GeneralCategories(); len(got) != 1 || got[0] != 99 {
		t.Errorf("categories after Rust completed = %v, want [99]", got)
	}
}

func TestRecordGeneralProgressAdvancesEveryRewardInCategory(t *testing.T) {
	m := testManager()

	m.RecordGeneralProgress(13, 60) // one minute watched in a Rust stream

	byID := rewardMap(m)
	if got, want := byID["r1"].RemainingUnits, 119.0; got != want {
		t.Errorf("r1 remaining = %v, want %v", got, want)
	}
	if got, want := byID["r2"].RemainingUnits, 239.0; got != want {
		t.Errorf("r2 remaining = %v, want %v", got, want)
	}
	if got, want := byID["r3"].RemainingUnits, 30.0; got != want {
		t.Errorf("r3 (other category) remaining = %v, want %v", got, want)
	}
	if got, want := byID["r4"].RemainingUnits, 60.0; got != want {
		t.Errorf("r4 (streamer drop) remaining = %v, want %v", got, want)
	}
}

func TestRecordStreamerProgressDoesNotCreditGeneralRewards(t *testing.T) {
	m := testManager()

	m.RecordStreamerProgress("alice", 60)

	byID := rewardMap(m)
	if got, want := byID["r4"].RemainingUnits, 59.0; got != want {
		t.Errorf("r4 remaining = %v, want %v", got, want)
	}
	for _, id := range []string{"r1", "r2", "r3"} {
		if byID[id].RemainingUnits != byID[id].RequiredUnits {
			t.Errorf("%s was credited by a streamer-specific session", id)
		}
	}
}

func TestCollectUsernamesIncludesCampaignCategory(t *testing.T) {
	targets := testManager().CollectUsernames()
	if len(targets) != 1 {
		t.Fatalf("got %d targets, want 1", len(targets))
	}
	if targets[0].Username != "alice" || targets[0].CategoryID != 13 {
		t.Errorf("target = %+v, want alice in category 13", targets[0])
	}
}

func TestSyncFromServerSkipsClaimedGeneralRewards(t *testing.T) {
	m := testManager()
	progress := &kick.DropsProgressResponse{Data: []kick.CampaignProgress{
		{
			ID: "rust-general",
			Rewards: []kick.RewardProgress{
				{ID: "r1", Progress: 1, Claimed: true},
				{ID: "r2", Progress: 0.25, Claimed: false},
			},
		},
	}}

	if err := m.SyncFromServer(progress, nil, nil); err != nil {
		t.Fatalf("SyncFromServer: %v", err)
	}
	if got, want := rewardMap(m)["r2"].RemainingUnits, 180.0; got != want {
		t.Errorf("r2 remaining = %v, want %v", got, want)
	}
	if got, want := m.GeneralRemainingSeconds(13), 180*60; got != want {
		t.Errorf("Rust remaining = %d, want %d", got, want)
	}
	// r1 is claimed, so it must not keep the general loop running at zero.
	if got := m.GeneralRemainingSeconds(13); got == 0 {
		t.Error("claimed reward was counted as remaining work")
	}
}

func rewardMap(m *Manager) map[string]*TrackedReward {
	out := make(map[string]*TrackedReward, len(m.rewards))
	for i := range m.rewards {
		out[m.rewards[i].ID] = &m.rewards[i]
	}
	return out
}
