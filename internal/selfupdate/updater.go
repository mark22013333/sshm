package selfupdate

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

const (
	// Repo 是 GitHub 上的 owner/name。
	Repo = "mark22013333/sshm"
	// RepoURL 是專案首頁。
	RepoURL = "https://github.com/" + Repo
	// DefaultAPIBase 是 GitHub REST API 的位址。
	DefaultAPIBase = "https://api.github.com"
	// GoInstallCmd 是開發版的手動更新指令。
	GoInstallCmd = "go install github.com/mark22013333/sshm@latest"

	checkInterval   = 24 * time.Hour
	apiTimeout      = 15 * time.Second
	downloadTimeout = 5 * time.Minute

	defaultMaxAPI       = 1 << 20
	defaultMaxChecksums = 64 << 10
	defaultMaxArchive   = 64 << 20
	defaultMaxBinary    = 128 << 20
)

// ErrUpToDate 表示目前已是最新版。
var ErrUpToDate = errors.New("已是最新版")

// 只從 GitHub 官方網域下載；release 資產會 redirect 到 githubusercontent 的下載網域。
var allowedHosts = map[string]bool{
	"github.com":                           true,
	"api.github.com":                       true,
	"objects.githubusercontent.com":        true,
	"release-assets.githubusercontent.com": true,
}

// DefaultAllowURL 只允許 https、無帳密、預設 port 的 GitHub 官方網域。
func DefaultAllowURL(u *url.URL) bool {
	if u == nil || u.Scheme != "https" || u.User != nil {
		return false
	}
	if p := u.Port(); p != "" && p != "443" {
		return false
	}
	return allowedHosts[strings.ToLower(u.Hostname())]
}

// Updater 檢查與套用更新；網路、檔案系統、平台都可注入以便測試。
type Updater struct {
	Version   string // 目前版本（ldflags 或 build info）
	APIBase   string
	Client    *http.Client
	AllowURL  func(*url.URL) bool
	CachePath string // 每日檢查的快取檔
	Now       func() time.Time
	GOOS      string
	GOARCH    string
	ExePath   string // 已解析 symlink 的執行檔真實路徑；空字串表示取不到
	// Writable 判斷目錄可否寫入；nil 時用系統預設。
	Writable func(dir string) error

	MaxArchive int64 // 0 時用預設上限
	MaxBinary  int64
}

// New 以正式環境的預設值建立 Updater。
func New(version, cachePath string) *Updater {
	exe, _ := ResolveExecutable(os.Executable)
	return &Updater{
		Version:   version,
		APIBase:   DefaultAPIBase,
		Client:    &http.Client{},
		AllowURL:  DefaultAllowURL,
		CachePath: cachePath,
		Now:       time.Now,
		GOOS:      runtime.GOOS,
		GOARCH:    runtime.GOARCH,
		ExePath:   exe,
	}
}

// ResolveExecutable 回傳執行檔解析 symlink 後的絕對路徑。
func ResolveExecutable(executable func() (string, error)) (string, error) {
	p, err := executable()
	if err != nil {
		return "", err
	}
	if p, err = filepath.EvalSymlinks(p); err != nil {
		return "", err
	}
	return filepath.Abs(p)
}

// CurrentVersion 回傳顯示用的目前版本。
func (u *Updater) CurrentVersion() string {
	if v := Normalize(u.Version); v != "" {
		return v
	}
	return "(devel)"
}

// ExecutablePath 回傳執行檔的真實路徑。
func (u *Updater) ExecutablePath() string { return u.ExePath }

func (u *Updater) now() time.Time {
	if u.Now != nil {
		return u.Now()
	}
	return time.Now()
}

func (u *Updater) allow(x *url.URL) bool {
	if u.AllowURL != nil {
		return u.AllowURL(x)
	}
	return DefaultAllowURL(x)
}

func orDefault(v, def int64) int64 {
	if v > 0 {
		return v
	}
	return def
}

func supportedArch(goos, goarch string) (string, bool) {
	if goos != "darwin" {
		return "", false
	}
	switch goarch {
	case "arm64", "amd64":
		return goarch, true
	}
	return "", false
}

// AssetName 是這個平台要下載的壓縮檔名稱。
func (u *Updater) AssetName() string {
	return "sshm_" + u.GOOS + "_" + u.GOARCH + ".tar.gz"
}

// Eligibility 是能否自動更新的判斷結果。
type Eligibility struct {
	OK     bool
	Reason string // 不能自動更新的原因
	Manual string // 手動更新的指令或說明
}

func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

func manualCurl(dir, arch string) string {
	return fmt.Sprintf(`curl -fsSL "%s/releases/latest/download/sshm_darwin_%s.tar.gz" | tar xz -C %s sshm`, RepoURL, arch, shellQuote(dir))
}

// Eligibility 判斷目前能否自動更新；不能時附上手動做法。
func (u *Updater) Eligibility() Eligibility {
	if !IsRelease(u.Version) {
		return Eligibility{Reason: "目前是開發版（" + u.CurrentVersion() + "），不自動更新", Manual: GoInstallCmd}
	}
	arch, ok := supportedArch(u.GOOS, u.GOARCH)
	if !ok {
		return Eligibility{
			Reason: "自動更新只支援 macOS（Apple Silicon／Intel），目前是 " + u.GOOS + "/" + u.GOARCH,
			Manual: "到 " + RepoURL + "/releases 下載，或執行 " + GoInstallCmd,
		}
	}
	if u.ExePath == "" {
		return Eligibility{Reason: "找不到目前執行檔的位置", Manual: "依 README 重新安裝：" + manualCurl("~/.local/bin", arch)}
	}
	dir := filepath.Dir(u.ExePath)
	writable := u.Writable
	if writable == nil {
		writable = dirWritable
	}
	if err := writable(dir); err != nil {
		return Eligibility{Reason: "無法寫入 " + dir + "（不會嘗試 sudo）", Manual: manualCurl(dir, arch)}
	}
	return Eligibility{OK: true, Manual: manualCurl(dir, arch)}
}

type cacheFile struct {
	CheckedAt time.Time `json:"checkedAt"`
	Latest    string    `json:"latest,omitempty"`
}

func readCache(path string) cacheFile {
	var c cacheFile
	if b, err := os.ReadFile(path); err == nil {
		_ = json.Unmarshal(b, &c)
	}
	return c
}

func writeCache(path string, c cacheFile) error {
	if path == "" {
		return nil
	}
	b, err := json.Marshal(c)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	return writeFileAtomic(path, b, 0o600)
}

// CheckLatest 回傳 GitHub 上的最新正式版；24 小時內只查一次（失敗也算），其餘時間沿用快取。
// 失敗時回傳上次已知的最新版與錯誤，由呼叫端決定是否顯示。
func (u *Updater) CheckLatest(ctx context.Context) (string, error) {
	c := readCache(u.CachePath)
	now := u.now()
	if age := now.Sub(c.CheckedAt); !c.CheckedAt.IsZero() && age >= 0 && age < checkInterval {
		return c.Latest, nil
	}
	rel, err := u.fetchRelease(ctx)
	c.CheckedAt = now
	if err == nil {
		c.Latest = rel.Tag
	}
	_ = writeCache(u.CachePath, c)
	return c.Latest, err
}

type asset struct {
	Name string `json:"name"`
	URL  string `json:"browser_download_url"`
	Size int64  `json:"size"`
}

type release struct {
	Tag    string  `json:"tag_name"`
	Assets []asset `json:"assets"`
}

func (r release) asset(name string) (asset, bool) {
	for _, a := range r.Assets {
		if a.Name == name {
			return a, true
		}
	}
	return asset{}, false
}

func (u *Updater) fetchRelease(ctx context.Context) (release, error) {
	ctx, cancel := context.WithTimeout(ctx, apiTimeout)
	defer cancel()
	base := u.APIBase
	if base == "" {
		base = DefaultAPIBase
	}
	body, err := u.get(ctx, strings.TrimRight(base, "/")+"/repos/"+Repo+"/releases/latest", "application/vnd.github+json", defaultMaxAPI)
	if err != nil {
		return release{}, err
	}
	var r release
	if err := json.Unmarshal(body, &r); err != nil {
		return release{}, fmt.Errorf("無法解析 GitHub 回應：%w", err)
	}
	if !IsRelease(r.Tag) {
		return release{}, fmt.Errorf("最新版標籤 %q 不是正式版號", r.Tag)
	}
	r.Tag = Normalize(r.Tag)
	return r, nil
}

// get 下載 rawURL（含每一跳 redirect 與最終網址）都必須通過 AllowURL，且不超過 limit bytes。
func (u *Updater) get(ctx context.Context, rawURL, accept string, limit int64) ([]byte, error) {
	target, err := url.Parse(rawURL)
	if err != nil || !u.allow(target) {
		return nil, fmt.Errorf("拒絕連線到非 GitHub 官方網址：%s", rawURL)
	}
	client := http.Client{}
	if u.Client != nil {
		client = *u.Client
	}
	client.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		if len(via) >= 10 {
			return errors.New("redirect 次數過多")
		}
		if !u.allow(req.URL) {
			return fmt.Errorf("拒絕 redirect 到非 GitHub 官方網址：%s", req.URL.Host)
		}
		return nil
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "sshm/"+u.CurrentVersion()+" (+"+RepoURL+")")
	req.Header.Set("Accept", accept)
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if !u.allow(resp.Request.URL) {
		return nil, fmt.Errorf("拒絕來自非 GitHub 官方網址的回應：%s", resp.Request.URL.Host)
	}
	name := filepath.Base(target.Path)
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("下載 %s 失敗：HTTP %d", name, resp.StatusCode)
	}
	if resp.ContentLength > limit {
		return nil, fmt.Errorf("%s 超過大小上限（%d bytes）", name, limit)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, limit+1))
	if err != nil {
		return nil, fmt.Errorf("下載 %s 中斷：%w", name, err)
	}
	if int64(len(data)) > limit {
		return nil, fmt.Errorf("%s 超過大小上限（%d bytes）", name, limit)
	}
	return data, nil
}

// lookupChecksum 從 checksums.txt（sha256sum 格式）找出 name 的 SHA-256。
func lookupChecksum(sums []byte, name string) (string, error) {
	for _, line := range strings.Split(string(sums), "\n") {
		f := strings.Fields(line)
		if len(f) != 2 || strings.TrimPrefix(f[1], "*") != name {
			continue
		}
		sum := strings.ToLower(f[0])
		if b, err := hex.DecodeString(sum); err != nil || len(b) != sha256.Size {
			return "", fmt.Errorf("checksums.txt 中 %s 的 SHA-256 格式不正確", name)
		}
		return sum, nil
	}
	return "", fmt.Errorf("checksums.txt 沒有 %s", name)
}

// Apply 下載最新版、驗證 SHA-256、解出 sshm 後原子替換目前的執行檔，回傳新版本。
// 任何一步失敗都不會改動原執行檔。
func (u *Updater) Apply(ctx context.Context) (string, error) {
	if e := u.Eligibility(); !e.OK {
		return "", fmt.Errorf("%s；請手動更新：%s", e.Reason, e.Manual)
	}
	rel, err := u.fetchRelease(ctx)
	if err != nil {
		return "", fmt.Errorf("查詢最新版失敗：%w", err)
	}
	if !IsNewer(rel.Tag, u.Version) {
		return rel.Tag, ErrUpToDate
	}
	name := u.AssetName()
	arc, ok := rel.asset(name)
	if !ok {
		return "", fmt.Errorf("%s 沒有 %s", rel.Tag, name)
	}
	sumsAsset, ok := rel.asset("checksums.txt")
	if !ok {
		return "", fmt.Errorf("%s 沒有 checksums.txt，無法驗證，已中止", rel.Tag)
	}
	maxArchive := orDefault(u.MaxArchive, defaultMaxArchive)
	if arc.Size > maxArchive {
		return "", fmt.Errorf("%s 超過大小上限（%d bytes）", name, maxArchive)
	}
	ctx, cancel := context.WithTimeout(ctx, downloadTimeout)
	defer cancel()
	sums, err := u.get(ctx, sumsAsset.URL, "application/octet-stream", defaultMaxChecksums)
	if err != nil {
		return "", err
	}
	want, err := lookupChecksum(sums, name)
	if err != nil {
		return "", err
	}
	data, err := u.get(ctx, arc.URL, "application/octet-stream", maxArchive)
	if err != nil {
		return "", err
	}
	got := sha256.Sum256(data)
	if hex.EncodeToString(got[:]) != want {
		return "", fmt.Errorf("%s 的 SHA-256 與 checksums.txt 不符，已中止更新", name)
	}
	bin, err := extractBinary(data, orDefault(u.MaxBinary, defaultMaxBinary))
	if err != nil {
		return "", err
	}
	if err := writeFileAtomic(u.ExePath, bin, 0); err != nil {
		return "", fmt.Errorf("替換執行檔失敗（原檔未變更）：%w", err)
	}
	return rel.Tag, nil
}
