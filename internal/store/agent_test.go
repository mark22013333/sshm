package store

import (
	"strings"
	"testing"
)

func agentHost() Host {
	return Host{ID: "h_1", Name: "範例客戶-PROD-VM", Host: "10.0.0.24", User: "helpdesk", Port: 22, Auth: AuthPassword, Password: "test-pass",
		Agent: &AgentAccess{Alias: "sshm-prod-vm", KeyFile: "/tmp/home/.ssh/sshm/sshm-prod-vm"}}
}

func TestAgentFieldRoundTripAndOldFileCompat(t *testing.T) {
	// 舊檔沒有 agent 欄位
	old, err := Parse([]byte(`{"version":1,"hosts":[{"id":"h_1","name":"n","host":"10.0.0.1","user":"u","auth":"password"}]}`))
	if err != nil {
		t.Fatal(err)
	}
	if old.Hosts[0].Agent != nil {
		t.Fatal("舊檔不應有 agent")
	}
	data, _ := Encode(old)
	if strings.Contains(string(data), `"agent"`) {
		t.Fatalf("未開通時不應寫出 agent 欄位：%s", data)
	}
	f := NewFile()
	f.Hosts = append(f.Hosts, agentHost())
	f.Hosts[0].Agent.User = "readonly"
	data, err = Encode(f)
	if err != nil {
		t.Fatal(err)
	}
	back, err := Parse(data)
	if err != nil {
		t.Fatal(err)
	}
	a := back.Hosts[0].Agent
	if a == nil || a.Alias != "sshm-prod-vm" || a.User != "readonly" || a.KeyFile != "/tmp/home/.ssh/sshm/sshm-prod-vm" {
		t.Fatalf("round trip = %+v", a)
	}
	// agent 內的未知欄位保留
	withExtra, err := Parse([]byte(`{"hosts":[{"id":"h_1","name":"n","host":"h","user":"u","agent":{"alias":"sshm-a","keyFile":"/k","future":1}}]}`))
	if err != nil {
		t.Fatal(err)
	}
	out, _ := Encode(withExtra)
	if !strings.Contains(string(out), `"future": 1`) {
		t.Fatalf("agent 未知欄位遺失：%s", out)
	}
}

func TestValidateHostAgent(t *testing.T) {
	cases := []struct {
		name    string
		mod     func(a *AgentAccess)
		wantErr string
	}{
		{"合法", func(a *AgentAccess) {}, ""},
		{"別名大寫", func(a *AgentAccess) { a.Alias = "Sshm-A" }, "別名"},
		{"別名含空白", func(a *AgentAccess) { a.Alias = "a b" }, "別名"},
		{"別名萬用字元", func(a *AgentAccess) { a.Alias = "sshm-*" }, "別名"},
		{"別名 - 開頭", func(a *AgentAccess) { a.Alias = "-x" }, "別名"},
		{"金鑰非絕對路徑", func(a *AgentAccess) { a.KeyFile = "rel/k" }, "金鑰"},
		{"金鑰含換行", func(a *AgentAccess) { a.KeyFile = "/k\nHost *" }, "控制字元"},
		{"金鑰含雙引號", func(a *AgentAccess) { a.KeyFile = `/k"x` }, "金鑰"},
		{"帳號含 @", func(a *AgentAccess) { a.User = "a@b" }, "帳號"},
		{"帳號 - 開頭", func(a *AgentAccess) { a.User = "-o" }, "帳號"},
	}
	for _, c := range cases {
		h := agentHost()
		c.mod(h.Agent)
		err := ValidateHost(h)
		if c.wantErr == "" {
			if err != nil {
				t.Errorf("%s: unexpected %v", c.name, err)
			}
			continue
		}
		if err == nil || !strings.Contains(err.Error(), c.wantErr) {
			t.Errorf("%s: err = %v, want %q", c.name, err, c.wantErr)
		}
	}
}

func TestApplyHostEditKeepsAgentFromOtherInstance(t *testing.T) {
	base := agentHost()
	base.Agent = nil
	f := &File{Hosts: []Host{base}}
	// 另一個 sshm（agent enable）在表單開啟後開通
	f.Hosts[0].Agent = &AgentAccess{Alias: "sshm-x", KeyFile: "/k"}
	edited := base
	edited.Name = "renamed"
	if err := f.ApplyHostEdit(base, edited); err != nil {
		t.Fatal(err)
	}
	if f.Hosts[0].Agent == nil || f.Hosts[0].Agent.Alias != "sshm-x" {
		t.Fatalf("開通狀態被表單覆蓋：%+v", f.Hosts[0])
	}
}

func TestDuplicateHostClearsAgent(t *testing.T) {
	f := &File{Hosts: []Host{agentHost()}}
	id, err := f.DuplicateHost("h_1")
	if err != nil {
		t.Fatal(err)
	}
	if f.Hosts[f.HostIndex(id)].Agent != nil {
		t.Fatal("複本不應共用別名與金鑰")
	}
	if f.Hosts[0].Agent == nil {
		t.Fatal("原機器的開通狀態不應被清掉")
	}
}

func TestSetAgent(t *testing.T) {
	f := &File{Hosts: []Host{agentHost()}}
	if err := f.SetAgent("h_1", nil); err != nil || f.Hosts[0].Agent != nil {
		t.Fatalf("清除失敗：%v %+v", err, f.Hosts[0])
	}
	if err := f.SetAgent("h_x", nil); err == nil {
		t.Fatal("找不到機器應報錯")
	}
	if err := f.SetAgent("h_1", &AgentAccess{Alias: "BAD ALIAS", KeyFile: "/k"}); err == nil || f.Hosts[0].Agent != nil {
		t.Fatal("不合法的開通資料不應寫入")
	}
	if err := f.SetAgent("h_1", &AgentAccess{Alias: "sshm-ok", KeyFile: "/k"}); err != nil || f.Hosts[0].Agent.Alias != "sshm-ok" {
		t.Fatalf("設定失敗：%v", err)
	}
}

func TestAgentEffectiveUser(t *testing.T) {
	h := agentHost()
	if h.AgentUser() != "helpdesk" {
		t.Fatal("未指定帳號時沿用機器的 user")
	}
	h.Agent.User = "readonly"
	if h.AgentUser() != "readonly" {
		t.Fatal("指定帳號時用指定的")
	}
}
