package main

import (
	"runtime/debug"
	"testing"
)

func TestVersionFromBuildInfo(t *testing.T) {
	cases := []struct {
		name string
		bi   debug.BuildInfo
		want string
	}{
		{"tagged", debug.BuildInfo{Main: debug.Module{Version: "v0.2.0"}}, "v0.2.0"},
		{"devel no vcs", debug.BuildInfo{Main: debug.Module{Version: "(devel)"}}, "dev"},
		{"checkout clean", debug.BuildInfo{Main: debug.Module{Version: "(devel)"}, Settings: []debug.BuildSetting{
			{Key: "vcs.revision", Value: "0123456789abcdef0123"}, {Key: "vcs.modified", Value: "false"}}}, "dev (0123456789ab)"},
		{"checkout dirty", debug.BuildInfo{Main: debug.Module{Version: "(devel)"}, Settings: []debug.BuildSetting{
			{Key: "vcs.revision", Value: "abc"}, {Key: "vcs.modified", Value: "true"}}}, "dev (abc, modified)"},
		{"pseudo version embeds revision", debug.BuildInfo{Main: debug.Module{Version: "v0.0.0-20260912073851-6a7880e0d4fc"}, Settings: []debug.BuildSetting{
			{Key: "vcs.revision", Value: "6a7880e0d4fc0123456789ab"}, {Key: "vcs.modified", Value: "false"}}}, "v0.0.0-20260912073851-6a7880e0d4fc"},
		{"pseudo version dirty", debug.BuildInfo{Main: debug.Module{Version: "v0.0.0-20260912073851-6a7880e0d4fc+dirty"}, Settings: []debug.BuildSetting{
			{Key: "vcs.revision", Value: "6a7880e0d4fc0123456789ab"}, {Key: "vcs.modified", Value: "true"}}}, "v0.0.0-20260912073851-6a7880e0d4fc+dirty"},
		{"tag with other revision", debug.BuildInfo{Main: debug.Module{Version: "v0.2.0"}, Settings: []debug.BuildSetting{
			{Key: "vcs.revision", Value: "abcdef"}}}, "v0.2.0 (abcdef)"},
	}
	for _, tc := range cases {
		if got := versionFromBuildInfo(&tc.bi); got != tc.want {
			t.Errorf("%s: got %q want %q", tc.name, got, tc.want)
		}
	}
}
