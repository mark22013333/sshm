package agentaccess

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/mark22013333/sshm/internal/connect"
	"github.com/mark22013333/sshm/internal/store"
)

// CheckEligible 只開放「密碼認證、有密碼、沒有自訂指令與額外參數」的機器：
// 公鑰是用 sshm 存的密碼登入安裝的，而自訂指令／額外參數（例如 ProxyJump）無法可靠地轉成別名設定。
func CheckEligible(h store.Host) error {
	switch {
	case h.Auth != store.AuthPassword:
		return fmt.Errorf("「%s」的認證方式是 %s，只支援 password（需要用 sshm 存的密碼登入安裝公鑰）", store.Printable(h.Name), h.Auth)
	case h.Password == "":
		return fmt.Errorf("「%s」沒有存密碼，無法登入安裝公鑰；請先按 Ctrl+E 補上密碼", store.Printable(h.Name))
	case strings.TrimSpace(h.CustomCommand) != "":
		return fmt.Errorf("「%s」設定了自訂指令，無法轉成 ssh 別名", store.Printable(h.Name))
	case strings.TrimSpace(h.ExtraArgs) != "":
		return fmt.Errorf("「%s」設定了額外 ssh 參數，無法轉成 ssh 別名", store.Printable(h.Name))
	}
	if err := store.ValidateHost(h); err != nil {
		return err
	}
	if err := ValidConfHost(h.Host); err != nil {
		return fmt.Errorf("「%s」無法開通：%w", store.Printable(h.Name), err)
	}
	if err := ValidConfUser(h.User); err != nil {
		return fmt.Errorf("「%s」無法開通：%w", store.Printable(h.Name), err)
	}
	return nil
}

// FindHost 依名稱找機器：完全相同優先，其次唯一的不分大小寫相同；同名多台時要求改名，不猜。
func FindHost(f *store.File, name string) (store.Host, error) {
	var exact, fold []store.Host
	for _, h := range f.Hosts {
		switch {
		case h.Name == name:
			exact = append(exact, h)
		case strings.EqualFold(h.Name, name):
			fold = append(fold, h)
		}
	}
	matches := exact
	if len(matches) == 0 {
		matches = fold
	}
	switch len(matches) {
	case 0:
		return store.Host{}, fmt.Errorf("找不到名稱為「%s」的機器（請用 sshm 清單上的完整名稱）", store.Printable(name))
	case 1:
		return matches[0], nil
	}
	var ids []string
	for _, h := range matches {
		ids = append(ids, fmt.Sprintf("%s（%s@%s）", h.ID, store.Printable(h.User), store.Printable(h.Host)))
	}
	return store.Host{}, fmt.Errorf("有 %d 台機器叫「%s」，請先在 sshm 中改成不同名稱：%s", len(matches), store.Printable(name), strings.Join(ids, "、"))
}

// Manager 執行開通／停用；欄位可替換以便測試。
type Manager struct {
	Paths     Paths
	HostsPath string
	Remote    RemoteRunner
	Keygen    connect.Runner
	// Verify 以別名實際連線驗證；nil 時用 ssh -o BatchMode=yes -F <ssh config> <別名> true。
	Verify func(ctx context.Context, alias string) error
}

// NewManager 以預設路徑與 expect 腳本建立 Manager。
func NewManager(hostsPath, loginScript string) (*Manager, error) {
	p, err := DefaultPaths()
	if err != nil {
		return nil, err
	}
	return &Manager{Paths: p, HostsPath: hostsPath, Remote: ExpectRunner(loginScript), Keygen: connect.ExecRunner}, nil
}

func (m *Manager) verify(ctx context.Context, alias string) error {
	if m.Verify != nil {
		return m.Verify(ctx, alias)
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "ssh", "-o", "BatchMode=yes", "-o", "ConnectTimeout=15", "-F", m.Paths.Config, alias, "true").CombinedOutput()
	if err != nil {
		return fmt.Errorf("以別名連線驗證失敗（%v）：%s", err, strings.TrimSpace(store.Printable(string(out))))
	}
	return nil
}

// location 是 h 以 user 登入時公鑰所在的遠端位置。
func location(h store.Host, user, fingerprint string) store.AgentInstall {
	return store.AgentInstall{Host: h.Host, Port: effectivePort(h.Port), User: user, Fingerprint: fingerprint}
}

func sameLocation(a, b store.AgentInstall) bool {
	return strings.EqualFold(a.Host, b.Host) && effectivePort(a.Port) == effectivePort(b.Port) && a.User == b.User
}

// LocationString 以 user@host:port 顯示位置。
func LocationString(l store.AgentInstall) string {
	return fmt.Sprintf("%s@%s:%d", store.Printable(l.User), store.Printable(l.Host), effectivePort(l.Port))
}

// Relocated 判斷已開通機器目前的 host／port／user 是否不在公鑰安裝過的位置中（需重新 enable）。
// 沒有安裝紀錄的舊資料無從判斷，回傳 false。
func Relocated(h store.Host) bool {
	if h.Agent == nil || len(h.Agent.Installed) == 0 {
		return false
	}
	cur := location(h, h.AgentUser(), "")
	for _, l := range h.Agent.Installed {
		if sameLocation(l, cur) {
			return false
		}
	}
	return true
}

// RelocatedNotice 是機器位置變動後給使用者的提示。
func RelocatedNotice(h store.Host) string {
	return fmt.Sprintf("AI agent 別名 %s 已改指向 %s，需重新 enable 才能使用；舊位置的公鑰會在 disable 時清除",
		h.Agent.Alias, LocationString(location(h, h.AgentUser(), "")))
}

// cleanupLocations 是停用時要清的位置：所有安裝紀錄加上目前位置（沒有紀錄的舊資料只有目前位置）。
func cleanupLocations(h store.Host) []store.AgentInstall {
	locs := append([]store.AgentInstall(nil), h.Agent.Installed...)
	cur := location(h, h.AgentUser(), "")
	for _, l := range locs {
		if sameLocation(l, cur) {
			return locs
		}
	}
	return append(locs, cur)
}

// EnablePlan 是開通前要給使用者確認的內容。
type EnablePlan struct {
	Host       store.Host
	Alias      string
	User       string // 實際登入的帳號
	UserSet    bool   // 是否以 --user 指定（與機器的 user 不同）
	Key        Key
	Reinstall  bool     // 已開通，重新安裝公鑰
	LocalFiles []string // 會寫入的本機檔案
}

// Summary 是確認畫面的文字（不含密碼）。
func (p *EnablePlan) Summary() []string {
	h := p.Host
	lines := []string{
		fmt.Sprintf("機器：%s（%s:%d）", store.Printable(h.Name), h.Host, effectivePort(h.Port)),
		"登入帳號：" + p.User,
		"公鑰指紋：" + p.Key.Fingerprint + "（ed25519，無 passphrase）",
		"ssh 別名：" + p.Alias + "（agent 以 `ssh " + p.Alias + "` 連線）",
		fmt.Sprintf("遠端：用 sshm 存的密碼登入，把公鑰附加到 %s 的 ~/.ssh/authorized_keys", p.User),
		fmt.Sprintf("      （行首 %s、行尾 %s；不動其他行）", KeyOptions, KeyComment(p.Alias)),
		"本機寫入：",
	}
	for _, f := range p.LocalFiles {
		lines = append(lines, "  - "+f)
	}
	if p.UserSet {
		lines = append(lines, fmt.Sprintf("注意：密碼原本屬於 %s，將以同一組密碼嘗試登入 %s，只試一次；失敗就停止，不會重試", h.User, p.User))
	}
	if p.Reinstall {
		lines = append(lines, "這台已開通，將沿用同一把金鑰重新安裝公鑰（不會產生重複行）")
	}
	return lines
}

// PrepareEnable 檢查機器、決定別名與帳號、取得金鑰（必要時產生），供確認畫面使用。
// 使用者取消時呼叫 Discard 清掉本次新產生的金鑰。
func (m *Manager) PrepareEnable(ctx context.Context, f *store.File, hostID, alias, user string) (*EnablePlan, error) {
	i := f.HostIndex(hostID)
	if i < 0 {
		return nil, store.ErrHostGone
	}
	h := f.Hosts[i]
	if err := CheckEligible(h); err != nil {
		return nil, err
	}
	plan := &EnablePlan{Host: h}
	taken := TakenAliases(f, h.ID)
	switch {
	case h.Agent != nil:
		if alias != "" && alias != h.Agent.Alias {
			return nil, fmt.Errorf("「%s」已以別名 %s 開通；要改別名請先停用再開通", store.Printable(h.Name), h.Agent.Alias)
		}
		if user != "" && user != h.AgentUser() {
			return nil, fmt.Errorf("「%s」已以帳號 %s 開通；要換帳號請先停用再開通", store.Printable(h.Name), h.AgentUser())
		}
		plan.Alias, plan.Reinstall = h.Agent.Alias, true
		plan.User, plan.UserSet = h.AgentUser(), h.Agent.User != ""
		if h.Agent.KeyFile != m.Paths.KeyPath(plan.Alias) {
			return nil, fmt.Errorf("「%s」的金鑰路徑 %s 不是 sshm 管理的位置，請先停用再開通", store.Printable(h.Name), h.Agent.KeyFile)
		}
	case alias != "":
		if !store.ValidAgentAlias(alias) {
			return nil, fmt.Errorf("別名 %q 不合法：只能用小寫英數與 . _ -，且以英數開頭、最長 %d 字", alias, store.AgentAliasMaxLen)
		}
		if taken[alias] {
			return nil, fmt.Errorf("別名 %s 已被其他機器使用", alias)
		}
		plan.Alias = alias
	default:
		plan.Alias = DefaultAlias(h, taken)
	}
	if !plan.Reinstall {
		plan.User = h.User
		if user != "" && user != h.User {
			if err := ValidConfUser(user); err != nil {
				return nil, err
			}
			plan.User, plan.UserSet = user, true
		}
		if defined, err := m.Paths.ConfigDefinesHost(plan.Alias); err != nil {
			return nil, err
		} else if defined {
			return nil, fmt.Errorf("你的 %s 已有 Host %s，請用 --alias 換一個別名", m.Paths.Config, plan.Alias)
		}
	}
	// 先確認寫得進 ssh 設定（含金鑰路徑；家目錄含引號等字元時會在這裡擋下），再產生金鑰與登入遠端
	check := h
	check.Agent = &store.AgentAccess{Alias: plan.Alias, KeyFile: m.Paths.KeyPath(plan.Alias)}
	if plan.UserSet {
		check.Agent.User = plan.User
	}
	if err := m.Paths.confHost(check); err != nil {
		return nil, fmt.Errorf("「%s」無法開通：%w", store.Printable(h.Name), err)
	}
	key, err := EnsureKey(ctx, m.Keygen, m.Paths, plan.Alias)
	if err != nil {
		return nil, err
	}
	plan.Key = key
	plan.LocalFiles = []string{key.Private + "（私鑰 0600）", key.Public, m.Paths.Conf, m.Paths.Config + "（最上方加 Include，已有則不動）", m.HostsPath}
	return plan, nil
}

// Discard 清掉 PrepareEnable 新產生的金鑰（使用者取消時）。
func (m *Manager) Discard(plan *EnablePlan) {
	if plan != nil && plan.Key.Created {
		_, _ = RemoveKey(m.Paths, plan.Key.Private)
	}
}

// EnableResult 是開通結果。
type EnableResult struct {
	Alias    string
	Remote   string   // changed／unchanged
	Warnings []string // Include 位置、被略過的機器等
}

// Enable 依確認過的計畫：遠端安裝公鑰 → 寫 hosts.json → 重寫 conf、加 Include → 以別名驗證。
// 任何一步失敗都盡量復原，錯誤訊息會說明殘留了什麼。
func (m *Manager) Enable(ctx context.Context, plan *EnablePlan) (EnableResult, error) {
	res := EnableResult{Alias: plan.Alias}
	status, err := runRemote(ctx, m.Remote, plan.Host, plan.User, InstallScript(plan.Alias, plan.Key.TypeBlob))
	if err != nil {
		m.Discard(plan)
		return res, fmt.Errorf("遠端安裝公鑰失敗，本機未做任何變更：%w", err)
	}
	res.Remote = status
	agent := &store.AgentAccess{Alias: plan.Alias, KeyFile: plan.Key.Private}
	if plan.UserSet {
		agent.User = plan.User
	}
	here := location(plan.Host, plan.User, plan.Key.Fingerprint)
	f, err := store.Update(m.HostsPath, func(f *store.File) error {
		i := f.HostIndex(plan.Host.ID)
		if i < 0 {
			return store.ErrHostGone
		}
		cur := f.Hosts[i]
		if cur.Host != plan.Host.Host || effectivePort(cur.Port) != effectivePort(plan.Host.Port) || cur.User != plan.Host.User {
			return errors.New("機器的 host／port／user 在開通期間被其他 sshm 修改")
		}
		// 保留先前的安裝位置（重新開通到新位置時，舊位置留待停用時清除）
		if cur.Agent != nil {
			for _, l := range cur.Agent.Installed {
				if !sameLocation(l, here) {
					agent.Installed = append(agent.Installed, l)
				}
			}
		}
		agent.Installed = append(agent.Installed, here)
		if other := TakenAliases(f, cur.ID); other[plan.Alias] {
			return fmt.Errorf("別名 %s 在開通期間被其他機器使用", plan.Alias)
		}
		return f.SetAgent(cur.ID, agent)
	})
	if err != nil {
		return res, m.rollback(ctx, plan, fmt.Errorf("寫入 sshm 資料失敗：%w", err), false)
	}
	_, skipped, err := m.Paths.SyncConf(f)
	if err != nil {
		return res, m.rollback(ctx, plan, fmt.Errorf("寫入 %s 失敗：%w", m.Paths.Conf, err), true)
	}
	for _, s := range skipped {
		res.Warnings = append(res.Warnings, "以下機器資料不合法，未寫入別名設定："+s)
	}
	_, st, err := m.Paths.EnsureInclude()
	if err != nil {
		return res, m.rollback(ctx, plan, fmt.Errorf("寫入 %s 失敗：%w", m.Paths.Config, err), true)
	}
	if st.Misplaced {
		res.Warnings = append(res.Warnings, m.Paths.IncludeWarning(st))
	}
	if err := m.verify(ctx, plan.Alias); err != nil {
		return res, m.rollback(ctx, plan, err, true)
	}
	return res, nil
}

// rollback 開通中途失敗時復原：移除遠端公鑰、清掉 hosts.json 的開通狀態並重寫 conf、刪除新產生的金鑰。
// 回傳的錯誤會列出復原不了的殘留。
func (m *Manager) rollback(ctx context.Context, plan *EnablePlan, cause error, stored bool) error {
	if plan.Reinstall {
		// 原本就已開通：保持原狀，只回報
		return fmt.Errorf("%w（這台原本就已開通，開通狀態維持不變）", cause)
	}
	var left []string
	if _, err := runRemote(ctx, m.Remote, plan.Host, plan.User, RemoveScript(plan.Key.TypeBlob)); err != nil {
		left = append(left, fmt.Sprintf("遠端 %s 的 authorized_keys 仍有行尾為 %s 的公鑰（%v）", plan.User, KeyComment(plan.Alias), err))
	}
	if stored {
		f, err := store.Update(m.HostsPath, func(f *store.File) error { return f.SetAgent(plan.Host.ID, nil) })
		if err == nil {
			_, _, err = m.Paths.SyncConf(f)
		}
		if err != nil {
			left = append(left, "本機開通狀態未能清除（"+err.Error()+"），請執行 sshm agent disable")
		}
	}
	if plan.Key.Created {
		if _, err := RemoveKey(m.Paths, plan.Key.Private); err != nil {
			left = append(left, err.Error())
		}
	}
	msg := "已復原為未開通"
	if len(left) > 0 {
		msg = "已嘗試復原，但仍有殘留：" + strings.Join(left, "；")
	}
	return fmt.Errorf("%w。%s（~/.ssh/config 的 Include 行保留，不影響其他設定）", cause, msg)
}

// DisablePlan 是停用前要給使用者確認的內容。
type DisablePlan struct {
	Host  store.Host
	Alias string
	User  string
	Key   string
	// Locations 是要清除公鑰的遠端位置（安裝紀錄加上目前位置）。
	Locations []store.AgentInstall
	// PublicKey 是「類型 base64」，遠端只刪含這把公鑰本體的行。
	PublicKey   string
	Fingerprint string
}

// Summary 是確認畫面的文字。
func (p *DisablePlan) Summary(paths Paths) []string {
	return []string{
		fmt.Sprintf("機器：%s（%s:%d）", store.Printable(p.Host.Name), p.Host.Host, effectivePort(p.Host.Port)),
		"登入帳號：" + p.User,
		"ssh 別名：" + p.Alias,
		fmt.Sprintf("遠端：用 sshm 存的密碼登入，從 ~/.ssh/authorized_keys 移除這把公鑰（%s）的那一行（以公鑰本體比對，不動其他行）", p.Fingerprint),
		"      位置：" + p.locationList(),
		fmt.Sprintf("本機：從 %s 移除別名區塊、刪除 %s 與 .pub、清除 hosts.json 的開通狀態", paths.Conf, p.Key),
	}
}

func (p *DisablePlan) locationList() string {
	cur := location(p.Host, p.User, "")
	var parts []string
	for _, l := range p.Locations {
		label := "（舊位置）"
		if sameLocation(l, cur) {
			label = "（目前位置）"
		}
		parts = append(parts, LocationString(l)+label)
	}
	return strings.Join(parts, "、")
}

// PrepareDisable 取得停用計畫。
func (m *Manager) PrepareDisable(f *store.File, hostID string) (*DisablePlan, error) {
	i := f.HostIndex(hostID)
	if i < 0 {
		return nil, store.ErrHostGone
	}
	h := f.Hosts[i]
	if h.Agent == nil {
		return nil, fmt.Errorf("「%s」尚未開通給 AI agent", store.Printable(h.Name))
	}
	plan := &DisablePlan{Host: h, Alias: h.Agent.Alias, User: h.AgentUser(), Key: h.Agent.KeyFile, Locations: cleanupLocations(h)}
	// 讀不到公鑰時仍可停用，但遠端那一行無從辨識，DisableRemote 會失敗並讓使用者選擇只清本機
	if pub, err := os.ReadFile(h.Agent.KeyFile + ".pub"); err == nil {
		plan.PublicKey, plan.Fingerprint, _ = parsePublicKey(string(pub))
	}
	return plan, nil
}

// DisableRemote 用密碼逐一登入每個安裝位置移除公鑰（舊位置用同一組密碼試一次）；
// 任一位置失敗就回報是哪些位置，呼叫端應詢問是否只清本機。
func (m *Manager) DisableRemote(ctx context.Context, plan *DisablePlan) (string, error) {
	h := plan.Host
	if err := CheckEligible(h); err != nil {
		return "", fmt.Errorf("無法用密碼登入遠端：%w", err)
	}
	if plan.PublicKey == "" {
		return "", fmt.Errorf("讀不到 %s.pub，無法辨識遠端 authorized_keys 中要移除的那一行", plan.Key)
	}
	status := "unchanged"
	var errs []error
	for _, l := range plan.Locations {
		at := h
		at.Host, at.Port = l.Host, l.Port
		st, err := runRemote(ctx, m.Remote, at, l.User, RemoveScript(plan.PublicKey))
		switch {
		case err != nil:
			errs = append(errs, fmt.Errorf("%s：%w", LocationString(l), err))
		case st == "changed":
			status = st
		}
	}
	if len(errs) > 0 {
		return "", errors.Join(errs...)
	}
	return status, nil
}

// DisableLocal 清除本機狀態：hosts.json 的開通欄位、conf 的別名區塊、金鑰檔。
func (m *Manager) DisableLocal(plan *DisablePlan) ([]string, error) {
	f, err := store.Update(m.HostsPath, func(f *store.File) error {
		i := f.HostIndex(plan.Host.ID)
		if i < 0 || f.Hosts[i].Agent == nil {
			return nil
		}
		return f.SetAgent(plan.Host.ID, nil)
	})
	if err != nil {
		return nil, err
	}
	if _, _, err := m.Paths.SyncConf(f); err != nil {
		return nil, err
	}
	return RemoveKey(m.Paths, plan.Key)
}

// Entry 是給 agent 看的一台機器（不含密碼）。
type Entry struct {
	Name  string `json:"name"`
	Alias string `json:"alias"`
	Host  string `json:"host"`
	Port  int    `json:"port"`
	User  string `json:"user"`
	Group string `json:"group"`
	// Active 為 false 表示別名目前沒有寫進 ssh 設定（機器資料不合法），Problem 是原因。
	Active  bool   `json:"active"`
	Problem string `json:"problem,omitempty"`
}

// List 列出已開通的機器，依名稱原順序；別名因資料不合法而停用的也列出，標 Active=false。
func (p Paths) List(f *store.File) []Entry {
	entries := []Entry{}
	for _, h := range f.Hosts {
		if h.Agent == nil {
			continue
		}
		problem := p.ConfProblem(h)
		entries = append(entries, Entry{Name: h.Name, Alias: h.Agent.Alias, Host: h.Host, Port: effectivePort(h.Port), User: h.AgentUser(), Group: h.Group,
			Active: problem == "", Problem: problem})
	}
	return entries
}
