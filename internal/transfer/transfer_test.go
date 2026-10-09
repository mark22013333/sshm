package transfer

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mark22013333/sshm/internal/store"
)

func seed(t *testing.T) (string, string) {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "sshm", "hosts.json")
	f := store.NewFile()
	f.Groups = []store.Group{{Name: "G", Color: "red"}}
	f.Hosts = []store.Host{
		{ID: "h_a", Name: "alpha", Group: "G", Host: "10.0.0.1", Port: 22, User: "root", Auth: "password", Password: "pw-a"},
		{ID: "h_b", Name: "beta", Host: "10.0.0.2", Port: 22, User: "ops", Auth: "none"},
	}
	f.Tunnels = []store.Tunnel{{ID: "t_1", Name: "db", HostID: "h_a", Rules: []store.Rule{{Type: "D", BindPort: 1080}}}}
	f.Extra = store.Extra{"future": []byte(`{"x":1}`)}
	if err := store.Save(path, f); err != nil {
		t.Fatal(err)
	}
	return path, dir
}

func TestExportClearsPasswordsByDefault(t *testing.T) {
	path, dir := seed(t)
	dest := filepath.Join(dir, "out.json")
	res, err := Export(path, dest, ExportOptions{})
	if err != nil || res.Hosts != 2 || len(res.SkippedTunnels) != 0 {
		t.Fatalf("res=%+v err=%v", res, err)
	}
	data, _ := os.ReadFile(dest)
	if strings.Contains(string(data), "pw-a") {
		t.Fatal("預設匯出不應含密碼")
	}
	for _, want := range []string{`"tunnels"`, `"future"`, `"groups"`} {
		if !strings.Contains(string(data), want) {
			t.Errorf("匯出缺 %s", want)
		}
	}
	st, _ := os.Stat(dest)
	if st.Mode().Perm() != 0o600 {
		t.Fatalf("perm = %o", st.Mode().Perm())
	}
	// 來源資料不受影響
	f, _ := store.Open(path)
	if f.Hosts[0].Password != "pw-a" {
		t.Fatal("匯出不應改動 hosts.json")
	}
}

func TestExportWithPasswordsAndForce(t *testing.T) {
	path, dir := seed(t)
	dest := filepath.Join(dir, "out.json")
	if err := os.WriteFile(dest, []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Export(path, dest, ExportOptions{WithPasswords: true}); err == nil || !strings.Contains(err.Error(), "--force") {
		t.Fatalf("已存在應要求 --force，err=%v", err)
	}
	if data, _ := os.ReadFile(dest); string(data) != "old" {
		t.Fatal("未加 --force 不應覆蓋")
	}
	if _, err := Export(path, dest, ExportOptions{WithPasswords: true, Force: true}); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(dest)
	if !strings.Contains(string(data), "pw-a") {
		t.Fatal("--with-passwords 應保留密碼")
	}
	st, _ := os.Stat(dest)
	if st.Mode().Perm() != 0o600 {
		t.Fatalf("覆蓋後 perm = %o", st.Mode().Perm())
	}
}

func writeJSON(t *testing.T, dir, body string) string {
	t.Helper()
	p := filepath.Join(dir, "in.json")
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

const incoming = `{
  "version": 1,
  "newTop": true,
  "groups": [{"name": "G", "color": "blue"}, {"name": "N", "color": ""}],
  "hosts": [
    {"id": "h_a", "name": "alpha2", "group": "G", "host": "10.0.0.1", "port": 2222, "user": "root",
     "auth": "password", "password": "changed-secret", "identityFile": "", "color": "", "extraArgs": "", "customCommand": ""},
    {"id": "h_b", "name": "beta", "group": "", "host": "10.0.0.2", "port": 22, "user": "ops",
     "auth": "none", "password": "", "identityFile": "", "color": "", "extraArgs": "", "customCommand": ""},
    {"id": "h_c", "name": "gamma", "group": "N", "host": "10.0.0.3", "port": 22, "user": "u",
     "auth": "password", "password": "pc", "identityFile": "", "color": "", "extraArgs": "", "customCommand": "", "plugin": {"k": 1}}
  ],
  "tunnels": [{"id": "t_1", "name": "db2", "hostId": "h_a", "rules": [{"type": "D", "bindPort": 1080}]}]
}`

func TestImportListsDiffsWithoutPasswordAndOverwrites(t *testing.T) {
	path, dir := seed(t)
	src := writeJSON(t, dir, incoming)
	var out bytes.Buffer
	if err := RunImport(path, src, strings.NewReader("y\n"), &out); err != nil {
		t.Fatal(err)
	}
	text := out.String()
	for _, want := range []string{"機器 alpha（h_a）", "密碼不同", "port：22 → 2222", "群組 G", "Tunnel db（t_1）", "新增：機器 1 台、群組 1 個", "內容相同 1 項"} {
		if !strings.Contains(text, want) {
			t.Errorf("輸出缺 %q：\n%s", want, text)
		}
	}
	if strings.Contains(text, "changed-secret") || strings.Contains(text, "pw-a") {
		t.Fatalf("差異列表不得顯示密碼：\n%s", text)
	}
	f, _ := store.Open(path)
	if len(f.Hosts) != 3 || f.Hosts[0].Name != "alpha2" || f.Hosts[0].Password != "changed-secret" || f.GroupColor("G") != "blue" || f.Tunnels[0].Name != "db2" {
		t.Fatalf("覆蓋結果：%+v %+v", f.Hosts, f.Tunnels)
	}
	if _, ok := f.Hosts[2].Extra["plugin"]; !ok {
		t.Error("匯入項目的未知欄位應保留")
	}
	if _, ok := f.Extra["newTop"]; !ok || f.Extra["future"] == nil {
		t.Error("頂層未知欄位應合併保留")
	}
}

func TestImportNoOverwriteAndCancel(t *testing.T) {
	path, dir := seed(t)
	src := writeJSON(t, dir, incoming)
	var out bytes.Buffer
	if err := RunImport(path, src, strings.NewReader("\n"), &out); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "已取消") {
		t.Fatalf("空白回答應取消：%s", out.String())
	}
	if f, _ := store.Open(path); len(f.Hosts) != 2 {
		t.Fatal("取消後不應寫入")
	}
	out.Reset()
	if err := RunImport(path, src, strings.NewReader("n\n"), &out); err != nil {
		t.Fatal(err)
	}
	f, _ := store.Open(path)
	if len(f.Hosts) != 3 || f.Hosts[0].Name != "alpha" || f.Hosts[0].Password != "pw-a" || f.GroupColor("G") != "red" || !f.HasGroup("N") {
		t.Fatalf("n 應只匯入新項目：%+v", f.Hosts)
	}
}

// 預設匯出不含密碼；再匯回時沿用現有密碼，不列為差異、不清掉密碼。
func TestImportEmptyPasswordKeepsExisting(t *testing.T) {
	path, dir := seed(t)
	dest := filepath.Join(dir, "export.json")
	if _, err := Export(path, dest, ExportOptions{}); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err := RunImport(path, dest, strings.NewReader(""), &out); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "沒有需要匯入") {
		t.Fatalf("round trip 應無差異：%s", out.String())
	}
	if f, _ := store.Open(path); f.Hosts[0].Password != "pw-a" {
		t.Fatal("密碼被清掉")
	}
}

func TestImportRejectsInvalidHosts(t *testing.T) {
	path, dir := seed(t)
	src := writeJSON(t, dir, `{"version":1,"hosts":[{"id":"h_z","name":"z","host":"-oProxyCommand=x","user":"u","auth":"none"},
		{"id":"h_y","name":"y","host":"h","user":"u","auth":"password","password":"a\nb"}]}`)
	var out bytes.Buffer
	err := RunImport(path, src, strings.NewReader("y\n"), &out)
	if err == nil || !strings.Contains(err.Error(), "「z」") || !strings.Contains(err.Error(), "控制字元") {
		t.Fatalf("err = %v", err)
	}
	if strings.Contains(err.Error(), "a\nb") {
		t.Fatal("錯誤訊息不得含密碼")
	}
	if f, _ := store.Open(path); len(f.Hosts) != 2 {
		t.Fatal("不合法時不應寫入任何資料")
	}
}

func hostsBytes(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

// 目的檔就是 hosts.json（直接路徑、硬連結、或經 symlink）時一律拒絕，hosts.json 內容不變。
func TestExportRefusesHostsFileItself(t *testing.T) {
	path, dir := seed(t)
	before := hostsBytes(t, path)
	hard := filepath.Join(dir, "hard.json")
	if err := os.Link(path, hard); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "link.json")
	if err := os.Symlink(path, link); err != nil {
		t.Fatal(err)
	}
	other := filepath.Join(dir, "other.json")
	if err := os.WriteFile(other, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	linkOther := filepath.Join(dir, "link-other.json")
	if err := os.Symlink(other, linkOther); err != nil {
		t.Fatal(err)
	}
	cases := []struct{ dest, want string }{
		{path, "資料檔"},
		{hard, "資料檔"},
		{link, "symlink"},
		{linkOther, "symlink"},
	}
	for _, c := range cases {
		_, err := Export(path, c.dest, ExportOptions{Force: true})
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: err = %v, want 含 %q", c.dest, err, c.want)
		}
	}
	if after := hostsBytes(t, path); after != before || !strings.Contains(after, "pw-a") {
		t.Fatal("hosts.json 內容被改動")
	}
	if hostsBytes(t, other) != "x" {
		t.Fatal("symlink 指向的檔案被改動")
	}
}

// 同 id 但 host 改成別台、匯入檔密碼為空：不得把舊密碼帶到新主機，並列出需補密碼。
func TestImportDoesNotCarryPasswordToNewEndpoint(t *testing.T) {
	path, dir := seed(t)
	src := writeJSON(t, dir, `{"version":1,"hosts":[
	  {"id":"h_a","name":"alpha","group":"G","host":"evil.example","port":22,"user":"root","auth":"password","password":""}]}`)
	var out bytes.Buffer
	if err := RunImport(path, src, strings.NewReader("y\n"), &out); err != nil {
		t.Fatal(err)
	}
	text := out.String()
	if !strings.Contains(text, "密碼將清空") || !strings.Contains(text, "請在 sshm 中選取後按 Ctrl+E 補上") || !strings.Contains(text, "  - alpha") {
		t.Fatalf("輸出：\n%s", text)
	}
	f, _ := store.Open(path)
	if h := f.Hosts[0]; h.Host != "evil.example" || h.Password != "" {
		t.Fatalf("舊密碼被帶到新主機：%+v", h)
	}
}

// host／port／user 未變、只改名稱：沿用密碼，差異清單明列「密碼將沿用」。
func TestImportKeepsPasswordWhenEndpointSame(t *testing.T) {
	path, dir := seed(t)
	src := writeJSON(t, dir, `{"version":1,"hosts":[
	  {"id":"h_a","name":"alpha-renamed","group":"G","host":"10.0.0.1","port":22,"user":"root","auth":"password","password":""}]}`)
	var out bytes.Buffer
	if err := RunImport(path, src, strings.NewReader("y\n"), &out); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "密碼將沿用") || strings.Contains(out.String(), "補上") {
		t.Fatalf("輸出：\n%s", out.String())
	}
	if f, _ := store.Open(path); f.Hosts[0].Password != "pw-a" || f.Hosts[0].Name != "alpha-renamed" {
		t.Fatalf("%+v", f.Hosts[0])
	}
}

func TestImportNoOverwriteDoesNotCreateSkippedGroups(t *testing.T) {
	path, dir := seed(t)
	src := writeJSON(t, dir, `{"version":1,"hosts":[
	  {"id":"h_a","name":"alpha","group":"NEWG","host":"10.0.0.1","port":22,"user":"root","auth":"password","password":"pw-a"}]}`)
	var out bytes.Buffer
	if err := RunImport(path, src, strings.NewReader("n\n"), &out); err != nil {
		t.Fatal(err)
	}
	if f, _ := store.Open(path); f.HasGroup("NEWG") {
		t.Fatal("略過的機器不應建立群組")
	}
}

func TestImportRejectsBadFileContents(t *testing.T) {
	cases := []struct{ name, body, want string }{
		{"重複 host id", `{"version":1,"hosts":[
			{"id":"h_d","name":"a","host":"h1","user":"u","auth":"none"},
			{"id":"h_d","name":"b","host":"h2","user":"u","auth":"none"}]}`, "在檔內重複"},
		{"tunnel type 不合法", `{"version":1,"tunnels":[{"id":"t_x","name":"x","hostId":"h_a","rules":[{"type":"Z","bindPort":1}]}]}`, "type 只能是"},
		{"tunnel port 不合法", `{"version":1,"tunnels":[{"id":"t_x","name":"x","hostId":"h_a","rules":[{"type":"D","bindPort":-5}]}]}`, "bindPort"},
		{"L 缺 target", `{"version":1,"tunnels":[{"id":"t_x","name":"x","hostId":"h_a","rules":[{"type":"L","bindPort":1}]}]}`, "targetHost"},
		{"hostId 不存在", `{"version":1,"tunnels":[{"id":"t_x","name":"x","hostId":"h_nope","rules":[{"type":"D","bindPort":1080}]}]}`, "不存在的機器"},
		{"名稱含控制序列", `{"version":1,"hosts":[{"id":"h_e","name":"bad\u001b[31m","host":"-x","user":"u","auth":"none"}]}`, "bad?[31m"},
		{"JSON 壞掉", `{"version":`, "in.json"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			path, dir := seed(t)
			before := hostsBytes(t, path)
			src := writeJSON(t, dir, c.body)
			var out bytes.Buffer
			err := RunImport(path, src, strings.NewReader("y\n"), &out)
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Fatalf("err = %v, want 含 %q", err, c.want)
			}
			if strings.Contains(err.Error(), "\x1b") {
				t.Fatal("錯誤訊息含控制字元")
			}
			if strings.Contains(err.Error(), "hosts.json") {
				t.Fatalf("錯誤應指向匯入檔：%v", err)
			}
			if hostsBytes(t, path) != before {
				t.Fatal("不合法時不應寫入")
			}
		})
	}
}

func TestImportDiffHidesExtraArgsAndCustomCommand(t *testing.T) {
	path, dir := seed(t)
	src := writeJSON(t, dir, `{"version":1,"hosts":[
	  {"id":"h_b","name":"beta","host":"10.0.0.2","port":22,"user":"ops","auth":"none","extraArgs":"-o IdentityAgent=SECRETPATH","customCommand":"sshpass -p TOPSECRET ssh x"}]}`)
	var out bytes.Buffer
	if err := RunImport(path, src, strings.NewReader("\n"), &out); err != nil {
		t.Fatal(err)
	}
	text := out.String()
	if !strings.Contains(text, "額外 ssh 參數已變更") || !strings.Contains(text, "自訂指令已變更") || strings.Contains(text, "SECRET") {
		t.Fatalf("輸出：\n%s", text)
	}
}

func TestImportRejectsAtInHost(t *testing.T) {
	path, dir := seed(t)
	src := writeJSON(t, dir, `{"version":1,"hosts":[{"id":"h_q","name":"q","host":"victim@attacker","user":"root","auth":"none"}]}`)
	var out bytes.Buffer
	if err := RunImport(path, src, strings.NewReader("y\n"), &out); err == nil || !strings.Contains(err.Error(), "host 不可包含 @") {
		t.Fatalf("err = %v", err)
	}
}

// 孤兒 tunnel 不匯出並列出名稱，匯出檔可以順利匯回。
func TestExportSkipsOrphanTunnels(t *testing.T) {
	path, dir := seed(t)
	if _, err := store.Update(path, func(f *store.File) error {
		f.Tunnels = append(f.Tunnels, store.Tunnel{ID: "t_orphan", Name: "孤兒", HostID: "h_gone",
			Rules: []store.Rule{{Type: "D", BindPort: 1081}}})
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	dest := filepath.Join(dir, "out.json")
	res, err := Export(path, dest, ExportOptions{})
	if err != nil || len(res.SkippedTunnels) != 1 || res.SkippedTunnels[0] != "孤兒" {
		t.Fatalf("res=%+v err=%v", res, err)
	}
	if strings.Contains(hostsBytes(t, dest), "t_orphan") {
		t.Fatal("孤兒 tunnel 不應出現在匯出檔")
	}
	var out bytes.Buffer
	if err := RunImport(path, dest, strings.NewReader(""), &out); err != nil {
		t.Fatalf("匯出檔應能匯回：%v", err)
	}
}
