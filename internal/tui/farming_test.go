package tui

import (
	"slices"
	"testing"

	"kickautodrops/internal/drops"
	"kickautodrops/internal/kick"
)

func TestActiveDropTargets(t *testing.T) {
	mgr := drops.NewManager(&kick.CampaignsResponse{Data: []kick.Campaign{
		{
			ID:       "general-campaign",
			Name:     "Rust General Drops",
			Category: kick.Category{ID: 13, Name: "Rust"},
			Rewards: []kick.Reward{
				{ID: "g1", Name: "Large Box", RequiredUnits: 120},
				{ID: "g2", Name: "Garage Door", RequiredUnits: 240},
			},
		},
		{
			ID:       "other-campaign",
			Name:     "Other General Drops",
			Category: kick.Category{ID: 99, Name: "Other"},
			Rewards: []kick.Reward{
				{ID: "g3", Name: "Badge", RequiredUnits: 30},
			},
		},
		{
			ID:       "streamer-campaign",
			Name:     "Team Spoonkid",
			Category: kick.Category{ID: 13, Name: "Rust"},
			Channels: []kick.Channel{{Slug: "spoonkid"}},
			Rewards: []kick.Reward{
				{ID: "s1", Name: "Pickaxe", RequiredUnits: 60},
			},
		},
	}})

	ids, label := activeDropTargets(mgr, true, 13, "random-streamer", "Rust")
	if !slices.Equal(ids, []string{"g1", "g2"}) {
		t.Errorf("general ids = %v, want [g1 g2]", ids)
	}
	if label != "Rust General Drops" {
		t.Errorf("general label = %q, want %q", label, "Rust General Drops")
	}

	ids, label = activeDropTargets(mgr, false, 13, "spoonkid", "")
	if !slices.Equal(ids, []string{"s1"}) {
		t.Errorf("streamer ids = %v, want [s1]", ids)
	}
	if label != "Pickaxe" {
		t.Errorf("streamer label = %q, want %q", label, "Pickaxe")
	}

	// A random general streamer must not highlight streamer-specific drops.
	ids, _ = activeDropTargets(mgr, true, 99, "spoonkid", "Other")
	if !slices.Equal(ids, []string{"g3"}) {
		t.Errorf("other-category ids = %v, want [g3]", ids)
	}
}
