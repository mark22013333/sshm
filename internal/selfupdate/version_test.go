package selfupdate

import "testing"

func TestNormalize(t *testing.T) {
	cases := map[string]string{
		"0.3.0":    "v0.3.0", // GoReleaser ldflags 不帶 v
		"v0.3.0":   "v0.3.0",
		" v1.2.3 ": "v1.2.3",
		"(devel)":  "(devel)",
		"":         "",
	}
	for in, want := range cases {
		if got := Normalize(in); got != want {
			t.Errorf("Normalize(%q) = %q，want %q", in, got, want)
		}
	}
}

func TestIsRelease(t *testing.T) {
	release := []string{"v0.3.0", "0.3.0", "v1.10.2"}
	dev := []string{
		"(devel)", "", "dev",
		"v0.3.1-0.20261013081500-4de9e5f12345", // go install 的 pseudo-version
		"0.3.1-SNAPSHOT-4de9e5f",               // goreleaser --snapshot
		"v0.3.0+dirty",                         // 未提交變更的建置
		"v0.4.0-rc.1",                          // 預發版
		"v0.3",                                 // 非完整 semver
	}
	for _, v := range release {
		if !IsRelease(v) {
			t.Errorf("IsRelease(%q) = false，應為正式版", v)
		}
	}
	for _, v := range dev {
		if IsRelease(v) {
			t.Errorf("IsRelease(%q) = true，應為開發版", v)
		}
	}
}

func TestIsNewer(t *testing.T) {
	cases := []struct {
		latest, current string
		want            bool
	}{
		{"v0.4.0", "0.3.0", true},
		{"v0.3.10", "v0.3.9", true}, // 不能用字串比較
		{"v0.3.0", "v0.3.0", false},
		{"v0.2.9", "v0.3.0", false},
		{"v1.0.0", "(devel)", false}, // 開發版無法比較
		{"garbage", "v0.3.0", false},
		{"v0.4.0-rc.1", "v0.3.0", false}, // 只接受正式版當目標
	}
	for _, c := range cases {
		if got := IsNewer(c.latest, c.current); got != c.want {
			t.Errorf("IsNewer(%q, %q) = %v，want %v", c.latest, c.current, got, c.want)
		}
	}
}
