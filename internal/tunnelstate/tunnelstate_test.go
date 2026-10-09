package tunnelstate

import (
	"os"
	"path/filepath"
	"testing"
)

func TestUpdateWritesWithPermissions(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	path, err := Path()
	if err != nil {
		t.Fatal(err)
	}
	if s, err := Load(path); err != nil || len(s.Tunnels) != 0 {
		t.Fatalf("不存在時應為空：%+v %v", s, err)
	}
	if _, err := Update(path, func(s *State) error {
		s.Tunnels["t_1"] = Entry{Handle: "term_a"}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	st, err := os.Stat(path)
	if err != nil || st.Mode().Perm() != 0o600 {
		t.Fatalf("perm: %v %v", st, err)
	}
	if st, _ := os.Stat(filepath.Dir(path)); st.Mode().Perm() != 0o700 {
		t.Fatalf("dir perm = %o", st.Mode().Perm())
	}
	s, err := Load(path)
	if err != nil || s.Tunnels["t_1"].Handle != "term_a" {
		t.Fatalf("%+v %v", s, err)
	}
	if _, err := Update(path, func(s *State) error {
		delete(s.Tunnels, "t_1")
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if s, _ := Load(path); len(s.Tunnels) != 0 {
		t.Fatalf("%+v", s)
	}
}

func TestLoadRejectsBadJSON(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	if err := os.WriteFile(path, []byte("{bad"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(path); err == nil {
		t.Fatal("壞掉的 JSON 應回錯")
	}
}
