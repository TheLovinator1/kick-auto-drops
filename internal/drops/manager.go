package drops

import (
	"math"
	"sort"

	"kickautodrops/internal/kick"
	"kickautodrops/internal/log"
)

// ---- Collect usernames ----

// CollectUsernames returns all type-1 (streamer) targets that still have
// remaining time. Claim state is left for the caller to decide.
func (m *Manager) CollectUsernames() []StreamerTarget {
	var targets []StreamerTarget

	for _, r := range m.rewards {
		if r.Type != 1 || len(r.Usernames) == 0 || r.Claimed {
			continue
		}
		for _, username := range r.Usernames {
			targets = append(targets, StreamerTarget{
				Username:        username,
				RequiredSeconds: int(r.RemainingUnits * 60),
				Claimed:         r.Claimed,
				CategoryID:      r.CategoryID,
			})
		}
	}

	return targets
}

// ---- Category-wide (type 2) queries ----

// GeneralCategories returns the IDs of categories that still have at least one
// unclaimed category-wide reward. This is what general mode should farm: the
// category belongs to the campaign, not to a CLI flag.
func (m *Manager) GeneralCategories() []int {
	seen := make(map[int]bool)
	var categories []int
	for _, r := range m.rewards {
		if r.Type != 2 || r.Claimed || r.CategoryID == 0 || seen[r.CategoryID] {
			continue
		}
		seen[r.CategoryID] = true
		categories = append(categories, r.CategoryID)
	}
	sort.Ints(categories)
	return categories
}

// GeneralRemainingSeconds returns how long to keep watching a stream in the
// given category. Watching one category-wide stream advances every unclaimed
// reward in that category, so we must wait for the one that needs the most
// time instead of only looking at the first reward.
func (m *Manager) GeneralRemainingSeconds(categoryID int) int {
	var remaining float64
	for _, r := range m.rewards {
		if r.Type != 2 || r.Claimed || r.RemainingUnits <= 0 {
			continue
		}
		if categoryID != 0 && r.CategoryID != categoryID {
			continue
		}
		if r.RemainingUnits > remaining {
			remaining = r.RemainingUnits
		}
	}
	return int(remaining * 60)
}

// ---- Record progress ----

// RecordGeneralProgress credits a category-wide watch session to every
// unclaimed type-2 reward in that category. It never touches rewards from
// other categories or streamer-specific rewards.
func (m *Manager) RecordGeneralProgress(categoryID int, watchedSeconds int) {
	watchedMinutes := round(float64(watchedSeconds)/60.0, 1)
	credited := 0

	for i := range m.rewards {
		r := &m.rewards[i]
		if r.Type != 2 || r.Claimed || r.RemainingUnits <= 0 {
			continue
		}
		if categoryID != 0 && r.CategoryID != categoryID {
			continue
		}
		old := r.RemainingUnits
		r.RemainingUnits = round(math.Max(0, old-watchedMinutes), 1)
		credited++
		log.Printf("  [Progress] General %s (category %d): %.1f → %.1f min remaining (-%.1f min watched)\n",
			r.Name, r.CategoryID, old, r.RemainingUnits, watchedMinutes)
	}

	if credited == 0 {
		log.Printf("⚠️ No unclaimed general rewards in category %d to credit\n", categoryID)
	}
}

// RecordStreamerProgress credits a streamer-specific watch session to the
// type-1 rewards that contain the username. It never falls back to general
// rewards: those have their own category and are farmed separately.
func (m *Manager) RecordStreamerProgress(username string, watchedSeconds int) {
	watchedMinutes := round(float64(watchedSeconds)/60.0, 1)
	credited := 0

	for i := range m.rewards {
		r := &m.rewards[i]
		if r.Type != 1 || r.Claimed || !contains(r.Usernames, username) {
			continue
		}
		old := r.RemainingUnits
		r.RemainingUnits = round(math.Max(0, old-watchedMinutes), 1)
		credited++
		log.Printf("  [Progress] %s: %.1f → %.1f min remaining (-%.1f min watched)\n",
			username, old, r.RemainingUnits, watchedMinutes)
	}

	if credited == 0 {
		log.Printf("⚠️ %s is not part of any streamer-specific drop\n", username)
	}
}

// ---- Sync from server ----

// SyncFromServer fetches the current progress from the server and auto-claims
// any fully-progressed rewards. Updates the Manager's claim state and
// RemainingUnits in place so the TUI tree shows accurate progress.
func (m *Manager) SyncFromServer(progress *kick.DropsProgressResponse, client *kick.Client, cookies map[string]string) error {
	// Index manager rewards by ID for fast lookup
	byID := make(map[string]*TrackedReward, len(m.rewards))
	for i := range m.rewards {
		byID[m.rewards[i].ID] = &m.rewards[i]
	}

	serverClaimed := make(map[string]bool)

	for _, campaign := range progress.Data {
		if campaign.Status == "expired" {
			continue
		}

		log.Printf("  Campaign: %s (ID: %s) [Status: %s]\n",
			campaign.Name, campaign.ID, campaign.Status)

		for _, reward := range campaign.Rewards {
			log.Printf("    Reward: %s (progress=%.1f, claimed=%v)\n",
				reward.Name, reward.Progress, reward.Claimed)

			// Sync RemainingUnits from server progress
			if local := byID[reward.ID]; local != nil {
				oldRemaining := local.RemainingUnits
				// Server progress is 0.0–1.0; compute remaining minutes
				local.RemainingUnits = round(local.RequiredUnits*(1.0-reward.Progress), 1)
				if local.RemainingUnits != oldRemaining {
					log.Printf("    → Synced progress: %.1f → %.1f min remaining (%.0f%%)\n",
						oldRemaining, local.RemainingUnits, reward.Progress*100)
				}
			}

			if reward.Progress >= 1 && !reward.Claimed {
				log.Printf("    → Progress is 100%% and reward is unclaimed! Attempting to claim...\n")
				claimResult, err := client.ClaimDropReward(reward.ID, campaign.ID, cookies)
				if err != nil {
					log.Printf("    ✗ Failed to claim %s: %v\n", reward.Name, err)
				} else if claimResult.Message == "Success" {
					serverClaimed[reward.ID] = true
					log.Printf("    ✓ Claimed: %s\n", reward.Name)
				} else {
					log.Printf("    ✗ Failed: %s (message: %s)\n", reward.Name, claimResult.Message)
				}
			} else if reward.Claimed && reward.Progress >= 1 {
				log.Printf("    → Already claimed on server, marking locally\n")
				serverClaimed[reward.ID] = true
			} else if reward.Progress < 1 {
				log.Printf("    → Progress %.0f%% complete, need 100%% before claiming\n", reward.Progress*100)
			}
		}
	}

	updated := 0
	for i := range m.rewards {
		r := &m.rewards[i]
		if serverClaimed[r.ID] && !r.Claimed {
			r.Claimed = true
			updated++
			log.Printf("  → Marked reward %s as claimed locally\n", r.ID)
		}
	}

	log.Printf("\n  → Sync complete: %d rewards updated\n", updated)
	return nil
}

// TotalRemainingSeconds returns the sum of remaining seconds for all
// unclaimed rewards. Returns 0 if everything is claimed or empty.
func (m *Manager) TotalRemainingSeconds() int {
	var total float64
	for _, r := range m.rewards {
		if !r.Claimed {
			total += r.RemainingUnits * 60
		}
	}
	return int(total)
}

// GetRewards returns a snapshot of all tracked rewards.
// The TUI uses this to build the drops tree with live progress/claim status.
func (m *Manager) GetRewards() []TrackedReward {
	out := make([]TrackedReward, len(m.rewards))
	copy(out, m.rewards)
	return out
}

// ---- Helpers ----

func round(val float64, decimals int) float64 {
	pow := math.Pow(10, float64(decimals))
	return math.Round(val*pow) / pow
}

func contains(slice []string, s string) bool {
	for _, v := range slice {
		if v == s {
			return true
		}
	}
	return false
}
