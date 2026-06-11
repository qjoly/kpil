package container

import (
	"reflect"
	"testing"
)

func TestParseAgentVersionLines(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want map[string]string
	}{
		{
			name: "all three agents present",
			in:   "claude=2.1.173\nopencode=1.17.3\ncopilot=1.0.35\n",
			want: map[string]string{
				"claude":   "2.1.173",
				"opencode": "1.17.3",
				"copilot":  "1.0.35",
			},
		},
		{
			name: "version embedded in noise text",
			in:   "claude=Claude Code 2.1.173 (build abc)\nopencode=opencode 1.17.3-rc.2\ncopilot=GitHub Copilot CLI v1.0.35\n",
			want: map[string]string{
				"claude":   "2.1.173",
				"opencode": "1.17.3-rc.2",
				"copilot":  "1.0.35",
			},
		},
		{
			name: "missing agent is silently dropped",
			in:   "claude=\nopencode=1.17.3\ncopilot=1.0.35\n",
			want: map[string]string{
				"opencode": "1.17.3",
				"copilot":  "1.0.35",
			},
		},
		{
			name: "completely empty output returns nil",
			in:   "",
			want: nil,
		},
		{
			name: "lines without = are ignored",
			in:   "this is not a version\nclaude=2.1.173\n",
			want: map[string]string{
				"claude": "2.1.173",
			},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := parseAgentVersionLines([]byte(tc.in))
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("parseAgentVersionLines(%q) = %v, want %v", tc.in, got, tc.want)
			}
		})
	}
}
