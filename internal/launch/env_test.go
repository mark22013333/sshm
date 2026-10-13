package launch

import (
	"reflect"
	"testing"
)

func TestMergeEnvOverridesExistingKey(t *testing.T) {
	base := []string{"PATH=/usr/bin", "SSHM_PASSWORD=stale", "HOME=/tmp/h"}
	got := mergeEnv(base, []string{"SSHM_PASSWORD=fresh"})
	want := []string{"PATH=/usr/bin", "HOME=/tmp/h", "SSHM_PASSWORD=fresh"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestMergeEnvKeepsBaseWithoutExtra(t *testing.T) {
	base := []string{"PATH=/usr/bin", "HOME=/tmp/h"}
	if got := mergeEnv(base, nil); !reflect.DeepEqual(got, base) {
		t.Fatalf("got %q, want %q", got, base)
	}
}
