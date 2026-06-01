package sessionaffinity

import "testing"

func TestComposeAffinity(t *testing.T) {
	cases := []struct {
		name          string
		base          string
		agentID       string
		parentID      string
		includeParent bool
		want          string
		wantChanged   bool
	}{
		{
			name:        "main agent is left untouched",
			base:        "abc123",
			agentID:     "", // no agent id => main/top-level agent
			want:        "",
			wantChanged: false,
		},
		{
			name:        "subagent is namespaced under the project base",
			base:        "abc123",
			agentID:     "Explore@team",
			want:        "abc123:Explore@team",
			wantChanged: true,
		},
		{
			name:        "subagent with no base falls back to agent id alone",
			base:        "",
			agentID:     "Explore@team",
			want:        "Explore@team",
			wantChanged: true,
		},
		{
			name:          "parent id is appended only when requested",
			base:          "abc123",
			agentID:       "Explore@team",
			parentID:      "main-agent",
			includeParent: true,
			want:          "abc123:Explore@team:main-agent",
			wantChanged:   true,
		},
		{
			name:          "parent id is ignored when includeParent is false",
			base:          "abc123",
			agentID:       "Explore@team",
			parentID:      "main-agent",
			includeParent: false,
			want:          "abc123:Explore@team",
			wantChanged:   true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, changed := composeAffinity(tc.base, tc.agentID, tc.parentID, tc.includeParent, false)
			if got != tc.want || changed != tc.wantChanged {
				t.Fatalf("composeAffinity(%q,%q,%q,%v) = (%q,%v); want (%q,%v)",
					tc.base, tc.agentID, tc.parentID, tc.includeParent, got, changed, tc.want, tc.wantChanged)
			}
		})
	}
}

// TestComposeAffinityHash verifies hashing yields a stable, fixed-width hex digest.
func TestComposeAffinityHash(t *testing.T) {
	a, changedA := composeAffinity("abc123", "Explore@team", "", false, true)
	b, changedB := composeAffinity("abc123", "Explore@team", "", false, true)

	if !changedA || !changedB {
		t.Fatalf("expected changed=true for a subagent request")
	}
	if a != b {
		t.Fatalf("hash is not deterministic: %q != %q", a, b)
	}
	if len(a) != 32 {
		t.Fatalf("expected 32-char md5 hex, got %d chars: %q", len(a), a)
	}

	// Different inputs must land in different buckets.
	if other, _ := composeAffinity("abc123", "Plan@team", "", false, true); other == a {
		t.Fatalf("distinct agent ids collided into the same affinity bucket")
	}
}
