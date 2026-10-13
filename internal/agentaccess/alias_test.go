package agentaccess

import (
	"strings"
	"testing"

	"github.com/mark22013333/sshm/internal/store"
)

func TestDefaultAlias(t *testing.T) {
	cases := []struct {
		name, host string
		taken      []string
		want       string
	}{
		{"範例客戶-PROD-VM-acme", "10.0.0.24", nil, "sshm-prod-vm-acme"},
		{"範例客戶 PROD__VM", "10.0.0.24", nil, "sshm-prod-vm"},
		{"--Web.01--", "10.0.0.24", nil, "sshm-web-01"},
		{"範例客戶正式機", "10.0.0.24", nil, "sshm-10-0-0-24"},
		{"範例客戶正式機", "fe80::1", nil, "sshm-fe80-1"},
		{"範例客戶正式機", "測試主機", nil, "sshm-host"},
		{"範例客戶-PROD-VM", "10.0.0.24", []string{"sshm-prod-vm"}, "sshm-prod-vm-2"},
		{"範例客戶-PROD-VM", "10.0.0.24", []string{"sshm-prod-vm", "sshm-prod-vm-2"}, "sshm-prod-vm-3"},
	}
	for _, c := range cases {
		taken := map[string]bool{}
		for _, a := range c.taken {
			taken[a] = true
		}
		got := DefaultAlias(store.Host{Name: c.name, Host: c.host}, taken)
		if got != c.want {
			t.Errorf("DefaultAlias(%q, %q) = %q, want %q", c.name, c.host, got, c.want)
		}
		if !store.ValidAgentAlias(got) {
			t.Errorf("預設別名 %q 不合法", got)
		}
	}
	// 太長的名稱截斷後仍合法，且不以 - 結尾
	long := DefaultAlias(store.Host{Name: strings.Repeat("ab-", 40), Host: "h"}, nil)
	if len(long) > store.AgentAliasMaxLen || strings.HasSuffix(long, "-") || !store.ValidAgentAlias(long) {
		t.Errorf("長名稱別名 = %q", long)
	}
}

func TestTakenAliasesExcludesSelf(t *testing.T) {
	f := &store.File{Hosts: []store.Host{
		{ID: "h_1", Agent: &store.AgentAccess{Alias: "sshm-a"}},
		{ID: "h_2", Agent: &store.AgentAccess{Alias: "sshm-b"}},
		{ID: "h_3"},
	}}
	got := TakenAliases(f, "h_1")
	if got["sshm-a"] || !got["sshm-b"] || len(got) != 1 {
		t.Fatalf("TakenAliases = %v", got)
	}
}
