package presets

import (
	"reflect"
	"testing"
)

func TestResolve(t *testing.T) {
	r := Resolver{
		Secrets: Secrets{MiniMaxAPIKey: "mm", RelayStationBaseURL: "https://rs.example", RelayStationAPIKey: "rk"},
		Custom:  map[string]map[string]string{"mine": {"B": "2", "A": "1"}},
	}
	tests := []struct {
		name    string
		want    []string
		wantErr bool
	}{
		{"", nil, false},
		{"anthropic", nil, false},
		{"minimax", []string{"ANTHROPIC_AUTH_TOKEN=mm", "ANTHROPIC_BASE_URL=" + DefaultMiniMaxBaseURL}, false},
		{"relay-station", []string{"ANTHROPIC_AUTH_TOKEN=rk", "ANTHROPIC_BASE_URL=https://rs.example"}, false},
		{"mine", []string{"A=1", "B=2"}, false},
		{"nope", nil, true},
	}
	for _, tc := range tests {
		got, err := r.Resolve(tc.name)
		if (err != nil) != tc.wantErr {
			t.Errorf("%q: err=%v wantErr=%v", tc.name, err, tc.wantErr)
			continue
		}
		if !reflect.DeepEqual(got, tc.want) {
			t.Errorf("%q: got %v want %v", tc.name, got, tc.want)
		}
	}
	if _, err := (Resolver{}).Resolve("minimax"); err == nil {
		t.Error("minimax without key should fail")
	}
}
