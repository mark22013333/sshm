// Package transfer 處理 hosts.json 的 JSON 匯出與匯入（格式同資料檔）。
package transfer

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"reflect"
	"strconv"
	"strings"

	"github.com/mark22013333/sshm/internal/store"
)

// ExportOptions 是 sshm export 的選項。
type ExportOptions struct {
	WithPasswords bool
	Force         bool
}

// ExportResult 是匯出結果。
type ExportResult struct {
	Hosts          int
	SkippedTunnels []string // 指向已不存在機器（或設定不合法）而未匯出的 tunnel 名稱
}

// Export 把 hostsPath 的內容寫到 dest（0600）；預設清空密碼，目的檔已存在時需 Force。
// 孤兒 tunnel 不匯出，避免匯出檔匯回時整份被拒。
func Export(hostsPath, dest string, opt ExportOptions) (ExportResult, error) {
	var res ExportResult
	if err := checkExportDest(hostsPath, dest, opt.Force); err != nil {
		return res, err
	}
	f, err := store.Open(hostsPath)
	if err != nil {
		return res, err
	}
	out := *f
	out.Hosts = make([]store.Host, len(f.Hosts))
	for i, h := range f.Hosts {
		if !opt.WithPasswords {
			h.Password = ""
		}
		out.Hosts[i] = h
	}
	out.Tunnels = nil
	for _, t := range f.Tunnels {
		if err := f.ValidateTunnelIn(t); err != nil {
			res.SkippedTunnels = append(res.SkippedTunnels, store.Printable(t.Name))
			continue
		}
		out.Tunnels = append(out.Tunnels, t)
	}
	data, err := store.Encode(&out)
	if err != nil {
		return res, err
	}
	if err := store.WriteFileAtomic(dest, data, 0o600); err != nil {
		return res, err
	}
	res.Hosts = len(out.Hosts)
	return res, nil
}

// checkExportDest 拒絕 symlink 目的檔，以及與 hosts.json（含其 symlink 目標、硬連結）為同一檔的目的檔。
func checkExportDest(hostsPath, dest string, force bool) error {
	lst, err := os.Lstat(dest)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if lst.Mode()&fs.ModeSymlink != 0 {
		return fmt.Errorf("%s 是 symlink；為避免寫到其他檔案，請改用實際路徑", dest)
	}
	if hst, err := os.Stat(hostsPath); err == nil && os.SameFile(lst, hst) {
		return fmt.Errorf("%s 就是 sshm 的資料檔，不能匯出到它自己", dest)
	}
	if !force {
		return fmt.Errorf("%s 已存在；要覆蓋請加 --force", dest)
	}
	return nil
}

// Change 是一個同 id（群組為同名）但內容不同的項目。
type Change struct {
	Kind  string // 機器、群組、Tunnel
	Label string
	Diffs []string
}

// Plan 是匯入前的差異摘要。
type Plan struct {
	NewHosts, NewGroups, NewTunnels int
	Unchanged                       int
	Conflicts                       []Change
}

// Empty 表示沒有任何要寫入的東西。
func (p Plan) Empty() bool {
	return p.NewHosts+p.NewGroups+p.NewTunnels == 0 && len(p.Conflicts) == 0
}

// ReadImportFile 讀取並驗證要匯入的檔案；任何一台機器、群組或 tunnel 不合法，或檔內 id 重複，就整份拒絕。
func ReadImportFile(path string) (*store.File, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("讀取 %s 失敗：%w", path, err)
	}
	in, err := store.Parse(data)
	if err != nil {
		return nil, fmt.Errorf("匯入檔 %s %w", path, err)
	}
	var problems []string
	hostIDs := map[string]bool{}
	for i, h := range in.Hosts {
		label := fmt.Sprintf("第 %d 台機器「%s」", i+1, store.Printable(h.Name))
		if err := store.ValidateHost(h); err != nil {
			problems = append(problems, fmt.Sprintf("%s：%v", label, err))
		}
		if h.ID != "" {
			if hostIDs[h.ID] {
				problems = append(problems, fmt.Sprintf("%s：id %s 在檔內重複", label, store.Printable(h.ID)))
			}
			hostIDs[h.ID] = true
		}
	}
	groupNames := map[string]bool{}
	for i, g := range in.Groups {
		label := fmt.Sprintf("第 %d 個群組「%s」", i+1, store.Printable(g.Name))
		switch {
		case strings.TrimSpace(g.Name) == "":
			problems = append(problems, fmt.Sprintf("第 %d 個群組：缺少名稱", i+1))
		case g.Name != store.Printable(g.Name):
			problems = append(problems, label+"：名稱不可包含控制字元")
		case groupNames[g.Name]:
			problems = append(problems, label+"：名稱在檔內重複")
		}
		groupNames[g.Name] = true
	}
	tunnelIDs := map[string]bool{}
	for i, t := range in.Tunnels {
		label := fmt.Sprintf("第 %d 條 tunnel「%s」", i+1, store.Printable(t.Name))
		if err := store.ValidateTunnel(t); err != nil {
			problems = append(problems, fmt.Sprintf("%s：%v", label, err))
		}
		if t.ID != "" {
			if tunnelIDs[t.ID] {
				problems = append(problems, fmt.Sprintf("%s：id %s 在檔內重複", label, store.Printable(t.ID)))
			}
			tunnelIDs[t.ID] = true
		}
	}
	if len(problems) > 0 {
		return nil, fmt.Errorf("匯入檔 %s 有不合法的項目，整份未匯入：\n  %s", path, strings.Join(problems, "\n  "))
	}
	for i := range in.Hosts {
		if in.Hosts[i].ID == "" {
			in.Hosts[i].ID = store.NewID("h")
		}
	}
	for i := range in.Tunnels {
		if in.Tunnels[i].ID == "" {
			in.Tunnels[i].ID = store.NewID("t")
		}
	}
	return in, nil
}

// checkTunnelHosts 確認每條 tunnel 的 hostId 在匯入後的資料中存在。
func checkTunnelHosts(cur, in *store.File) error {
	var missing []string
	for _, t := range in.Tunnels {
		if cur.HostIndex(t.HostID) < 0 && in.HostIndex(t.HostID) < 0 {
			missing = append(missing, fmt.Sprintf("tunnel「%s」指向不存在的機器 %s", store.Printable(t.Name), store.Printable(t.HostID)))
		}
	}
	if len(missing) > 0 {
		return fmt.Errorf("匯入檔有不合法的項目，整份未匯入：\n  %s", strings.Join(missing, "\n  "))
	}
	return nil
}

func sameEndpoint(a, b store.Host) bool {
	pa, pb := a.Port, b.Port
	if pa <= 0 {
		pa = 22
	}
	if pb <= 0 {
		pb = 22
	}
	return a.Host == b.Host && pa == pb && a.User == b.User
}

// keepPassword 匯入檔的密碼為空（預設匯出不含密碼）時，只有 host、port、user 都沒變才沿用現有密碼，
// 避免把舊密碼配到別台。
func keepPassword(cur, in store.Host) (store.Host, bool) {
	if in.Password == "" && cur.Password != "" && in.Auth == store.AuthPassword && cur.Auth == store.AuthPassword && sameEndpoint(cur, in) {
		in.Password = cur.Password
		return in, true
	}
	return in, false
}

func hostDiffs(cur, in store.Host) []string {
	in, kept := keepPassword(cur, in)
	var d []string
	str := func(label, a, b string) {
		if a != b {
			d = append(d, fmt.Sprintf("%s：%q → %q", label, a, b))
		}
	}
	str("名稱", cur.Name, in.Name)
	str("群組", cur.Group, in.Group)
	str("host", cur.Host, in.Host)
	if cur.Port != in.Port {
		d = append(d, fmt.Sprintf("port：%d → %d", cur.Port, in.Port))
	}
	str("user", cur.User, in.User)
	str("認證方式", cur.Auth, in.Auth)
	switch {
	case cur.Password == in.Password:
	case in.Password == "":
		d = append(d, "密碼將清空（匯入檔沒有密碼，且 host／port／user 有變更或認證方式不同，不沿用舊密碼）")
	default:
		d = append(d, "密碼不同")
	}
	str("金鑰檔", cur.IdentityFile, in.IdentityFile)
	str("顏色", cur.Color, in.Color)
	// 可能含祕密，只說明已變更
	if cur.ExtraArgs != in.ExtraArgs {
		d = append(d, "額外 ssh 參數已變更")
	}
	if cur.CustomCommand != in.CustomCommand {
		d = append(d, "自訂指令已變更")
	}
	if !extraEqual(cur.Extra, in.Extra) {
		d = append(d, "其他欄位不同")
	}
	if kept && len(d) > 0 {
		d = append(d, "密碼將沿用（匯入檔沒有密碼，host／port／user 未變）")
	}
	return d
}

func tunnelDiffs(cur, in store.Tunnel) []string {
	var d []string
	if cur.Name != in.Name {
		d = append(d, fmt.Sprintf("名稱：%q → %q", cur.Name, in.Name))
	}
	if cur.HostID != in.HostID {
		d = append(d, fmt.Sprintf("機器：%s → %s", cur.HostID, in.HostID))
	}
	if !jsonEqual(cur.Rules, in.Rules) {
		d = append(d, "規則不同（"+strconv.Itoa(len(cur.Rules))+" 條 → "+strconv.Itoa(len(in.Rules))+" 條）")
	}
	if !extraEqual(cur.Extra, in.Extra) {
		d = append(d, "其他欄位不同")
	}
	return d
}

func groupDiffs(cur, in store.Group) []string {
	var d []string
	if cur.Color != in.Color {
		d = append(d, fmt.Sprintf("顏色：%q → %q", cur.Color, in.Color))
	}
	if !extraEqual(cur.Extra, in.Extra) {
		d = append(d, "其他欄位不同")
	}
	return d
}

func jsonEqual(a, b any) bool {
	ja, _ := json.Marshal(a)
	jb, _ := json.Marshal(b)
	return string(ja) == string(jb)
}

// extraEqual 以 JSON 語意比較未知欄位（忽略空白差異）。
func extraEqual(a, b store.Extra) bool {
	if len(a) != len(b) {
		return false
	}
	for k, va := range a {
		vb, ok := b[k]
		if !ok {
			return false
		}
		var xa, xb any
		if json.Unmarshal(va, &xa) != nil || json.Unmarshal(vb, &xb) != nil || !reflect.DeepEqual(xa, xb) {
			return false
		}
	}
	return true
}

func groupIndex(f *store.File, name string) int {
	for i, g := range f.Groups {
		if g.Name == name {
			return i
		}
	}
	return -1
}

func tunnelIndex(f *store.File, id string) int {
	for i, t := range f.Tunnels {
		if t.ID == id {
			return i
		}
	}
	return -1
}

// Diff 比較現有資料與匯入檔。
func Diff(cur, in *store.File) Plan {
	var p Plan
	for _, g := range in.Groups {
		if i := groupIndex(cur, g.Name); i < 0 {
			p.NewGroups++
		} else if d := groupDiffs(cur.Groups[i], g); len(d) > 0 {
			p.Conflicts = append(p.Conflicts, Change{Kind: "群組", Label: store.Printable(g.Name), Diffs: d})
		} else {
			p.Unchanged++
		}
	}
	for _, h := range in.Hosts {
		if i := cur.HostIndex(h.ID); i < 0 {
			p.NewHosts++
		} else if d := hostDiffs(cur.Hosts[i], h); len(d) > 0 {
			p.Conflicts = append(p.Conflicts, Change{Kind: "機器", Label: fmt.Sprintf("%s（%s）", store.Printable(cur.Hosts[i].Name), store.Printable(h.ID)), Diffs: d})
		} else {
			p.Unchanged++
		}
	}
	for _, t := range in.Tunnels {
		if i := tunnelIndex(cur, t.ID); i < 0 {
			p.NewTunnels++
		} else if d := tunnelDiffs(cur.Tunnels[i], t); len(d) > 0 {
			p.Conflicts = append(p.Conflicts, Change{Kind: "Tunnel", Label: fmt.Sprintf("%s（%s）", store.Printable(cur.Tunnels[i].Name), store.Printable(t.ID)), Diffs: d})
		} else {
			p.Unchanged++
		}
	}
	return p
}

// Stats 是實際寫入的數量。
type Stats struct {
	Added, Overwritten, Skipped int
	NeedPassword                []string // 寫入後為密碼認證但沒有密碼的機器名稱
}

// Merge 把匯入檔併入 f；overwrite 為 false 時同 id 的項目保持不動。未知欄位隨項目一併保留。
// 只為實際寫入的機器建立缺少的群組。
func Merge(f, in *store.File, overwrite bool) Stats {
	var s Stats
	for _, g := range in.Groups {
		switch i := groupIndex(f, g.Name); {
		case i < 0:
			f.Groups = append(f.Groups, g)
			s.Added++
		case len(groupDiffs(f.Groups[i], g)) == 0:
		case overwrite:
			f.Groups[i] = g
			s.Overwritten++
		default:
			s.Skipped++
		}
	}
	for _, h := range in.Hosts {
		var written *store.Host
		switch i := f.HostIndex(h.ID); {
		case i < 0:
			f.Hosts = append(f.Hosts, h)
			written = &f.Hosts[len(f.Hosts)-1]
			s.Added++
		case len(hostDiffs(f.Hosts[i], h)) == 0:
		case overwrite:
			f.Hosts[i], _ = keepPassword(f.Hosts[i], h)
			written = &f.Hosts[i]
			s.Overwritten++
		default:
			s.Skipped++
		}
		if written == nil {
			continue
		}
		f.EnsureGroup(written.Group)
		if written.Auth == store.AuthPassword && written.Password == "" {
			s.NeedPassword = append(s.NeedPassword, store.Printable(written.Name))
		}
	}
	for _, t := range in.Tunnels {
		switch i := tunnelIndex(f, t.ID); {
		case i < 0:
			f.Tunnels = append(f.Tunnels, t)
			s.Added++
		case len(tunnelDiffs(f.Tunnels[i], t)) == 0:
		case overwrite:
			f.Tunnels[i] = t
			s.Overwritten++
		default:
			s.Skipped++
		}
	}
	for k, v := range in.Extra {
		if f.Extra == nil {
			f.Extra = store.Extra{}
		}
		if _, ok := f.Extra[k]; !ok {
			f.Extra[k] = v
		}
	}
	return s
}

// RunImport 列出差異、詢問後寫入；in 是使用者輸入（通常為 stdin）。
func RunImport(hostsPath, src string, in io.Reader, out io.Writer) error {
	incoming, err := ReadImportFile(src)
	if err != nil {
		return err
	}
	cur, err := store.Open(hostsPath)
	if err != nil {
		return err
	}
	if err := checkTunnelHosts(cur, incoming); err != nil {
		return err
	}
	plan := Diff(cur, incoming)
	if plan.Empty() {
		fmt.Fprintln(out, "沒有需要匯入的項目（內容都相同）。")
		return nil
	}
	fmt.Fprintf(out, "新增：機器 %d 台、群組 %d 個、Tunnel %d 條；內容相同 %d 項。\n",
		plan.NewHosts, plan.NewGroups, plan.NewTunnels, plan.Unchanged)
	overwrite := false
	if len(plan.Conflicts) > 0 {
		fmt.Fprintf(out, "\n以下 %d 項已存在且內容不同：\n", len(plan.Conflicts))
		for _, c := range plan.Conflicts {
			fmt.Fprintf(out, "  %s %s\n", c.Kind, c.Label)
			for _, d := range c.Diffs {
				fmt.Fprintf(out, "    - %s\n", d)
			}
		}
		fmt.Fprint(out, "\n要怎麼處理？ y＝覆蓋這些項目並匯入、n＝只匯入新項目、其他＝取消：")
		line, _ := bufio.NewReader(in).ReadString('\n')
		switch strings.ToLower(strings.TrimSpace(line)) {
		case "y", "yes":
			overwrite = true
		case "n", "no":
		default:
			fmt.Fprintln(out, "已取消，未寫入任何資料。")
			return nil
		}
	}
	var stats Stats
	if _, err := store.Update(hostsPath, func(f *store.File) error {
		stats = Merge(f, incoming, overwrite)
		return nil
	}); err != nil {
		return err
	}
	fmt.Fprintf(out, "完成：新增 %d 項、覆蓋 %d 項、略過 %d 項。\n", stats.Added, stats.Overwritten, stats.Skipped)
	if len(stats.NeedPassword) > 0 {
		fmt.Fprintf(out, "以下 %d 台使用密碼登入但沒有密碼，請在 sshm 中選取後按 Ctrl+E 補上：\n", len(stats.NeedPassword))
		for _, n := range stats.NeedPassword {
			fmt.Fprintf(out, "  - %s\n", n)
		}
	}
	return nil
}
