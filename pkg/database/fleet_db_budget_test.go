package database

import "testing"

func TestAllocateFleetDBConnections(t *testing.T) {
	tests := []struct {
		name    string
		maximum int
		members []FleetMember
		want    []int
		valid   bool
	}{
		{"single", 50, []FleetMember{{"crowler-api", "api"}}, []int{47}, true},
		{"three service types", 50, []FleetMember{{"crowler-events", "events"}, {"crowler-engine", "engine"}, {"crowler-api", "api"}}, []int{16, 16, 15}, true},
		{"ten members", 50, tenMembers(), []int{5, 5, 5, 5, 5, 5, 5, 4, 4, 4}, true},
		{"duplicate tuple", 50, []FleetMember{{"crowler-api", "api"}, {" CROWLER-API ", " api "}}, []int{47}, true},
		{"same name different type", 50, []FleetMember{{"crowler-api", "shared"}, {"crowler-engine", "shared"}}, []int{24, 23}, true},
		{"unknown type", 50, []FleetMember{{"plugin", "ignored"}, {"crowler-events", "events"}}, []int{47}, true},
		{"normalization", 50, []FleetMember{{" CROWLER-API ", " api "}}, []int{47}, true},
		{"zero members", 3, nil, nil, true},
		{"insufficient", 4, []FleetMember{{"crowler-api", "a"}, {"crowler-api", "b"}}, nil, false},
		{"exact reserve", 3, []FleetMember{{"crowler-api", "a"}}, nil, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := AllocateFleetDBConnections(tt.maximum, tt.members)
			if got.Valid != tt.valid {
				t.Fatalf("Valid = %v, reason %q; want %v", got.Valid, got.Reason, tt.valid)
			}
			if len(got.Members) != len(tt.want) {
				t.Fatalf("got %d allocations, want %d", len(got.Members), len(tt.want))
			}
			sum := 0
			for i, allocation := range got.Members {
				if allocation.MaxConnections != tt.want[i] {
					t.Errorf("allocation %d = %d, want %d", i, allocation.MaxConnections, tt.want[i])
				}
				if allocation.MaxConnections <= 0 {
					t.Errorf("allocation %d is not positive", i)
				}
				sum += allocation.MaxConnections
			}
			if got.Valid && len(got.Members) > 0 && sum != got.UsableConnections {
				t.Errorf("allocation sum = %d, usable = %d", sum, got.UsableConnections)
			}
			if !got.Valid && got.Reason == "" {
				t.Error("invalid allocation has no reason")
			}
		})
	}
}

func TestNormalizeFleetMembers(t *testing.T) {
	got := NormalizeFleetMembers([]FleetMember{
		{" CROWLER-ENGINE ", " shared "},
		{"crowler-api", "shared"},     // same name, different type is retained
		{" CROWLER-API ", " shared "}, // normalized duplicate
		{"unknown", "ignored"},
		{"crowler-events", " z "},
	})
	want := []FleetMember{{"crowler-api", "shared"}, {"crowler-engine", "shared"}, {"crowler-events", "z"}}
	if len(got) != len(want) {
		t.Fatalf("got %#v, want %#v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("member %d = %#v, want %#v", i, got[i], want[i])
		}
	}
}

func tenMembers() []FleetMember {
	return []FleetMember{
		{"crowler-events", "e2"}, {"crowler-api", "a4"}, {"crowler-engine", "n2"},
		{"crowler-api", "a1"}, {"crowler-events", "e1"}, {"crowler-engine", "n4"},
		{"crowler-api", "a3"}, {"crowler-engine", "n1"}, {"crowler-api", "a2"},
		{"crowler-engine", "n3"},
	}
}
