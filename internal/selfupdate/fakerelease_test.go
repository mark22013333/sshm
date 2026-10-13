package selfupdate

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"
)

// tarEntry 是假壓縮檔裡的一個項目。
type tarEntry struct {
	name     string
	body     []byte
	typeflag byte
	linkname string
}

func makeTarGz(t *testing.T, entries ...tarEntry) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for _, e := range entries {
		tf := e.typeflag
		if tf == 0 {
			tf = tar.TypeReg
		}
		hdr := &tar.Header{Name: e.name, Mode: 0o755, Size: int64(len(e.body)), Typeflag: tf, Linkname: e.linkname}
		if tf != tar.TypeReg {
			hdr.Size = 0
		}
		if err := tw.WriteHeader(hdr); err != nil {
			t.Fatal(err)
		}
		if tf == tar.TypeReg {
			if _, err := tw.Write(e.body); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func sha(b []byte) string {
	s := sha256.Sum256(b)
	return hex.EncodeToString(s[:])
}

// fakeRelease 用 httptest TLS server 模擬 GitHub 的 API 與下載。
type fakeRelease struct {
	srv       *httptest.Server
	tag       string
	archive   []byte
	checksums string // 空字串時依 archive 自動產生
	apiCalls  atomic.Int32
	apiStatus int
	// archiveHandler 非 nil 時取代壓縮檔下載的回應
	archiveHandler http.HandlerFunc
	// redirectArchiveTo 非空時壓縮檔下載改為 302 到這個網址
	redirectArchiveTo string
}

const testAsset = "sshm_darwin_arm64.tar.gz"

func newFakeRelease(t *testing.T, tag string, binary []byte) *fakeRelease {
	t.Helper()
	fr := &fakeRelease{tag: tag, archive: makeTarGz(t, tarEntry{name: "README.md", body: []byte("說明")}, tarEntry{name: "sshm", body: binary})}
	mux := http.NewServeMux()
	mux.HandleFunc("/repos/mark22013333/sshm/releases/latest", func(w http.ResponseWriter, r *http.Request) {
		fr.apiCalls.Add(1)
		if r.Header.Get("User-Agent") == "" {
			http.Error(w, "缺 User-Agent", http.StatusForbidden)
			return
		}
		if fr.apiStatus != 0 {
			w.WriteHeader(fr.apiStatus)
			return
		}
		base := fr.srv.URL + "/download/" + fr.tag + "/"
		_ = json.NewEncoder(w).Encode(map[string]any{
			"tag_name": fr.tag,
			"assets": []map[string]any{
				{"name": "checksums.txt", "browser_download_url": base + "checksums.txt", "size": 100},
				{"name": testAsset, "browser_download_url": base + testAsset, "size": len(fr.archive)},
				{"name": "sshm_darwin_amd64.tar.gz", "browser_download_url": base + "sshm_darwin_amd64.tar.gz", "size": len(fr.archive)},
			},
		})
	})
	mux.HandleFunc("/download/", func(w http.ResponseWriter, r *http.Request) {
		switch filepath.Base(r.URL.Path) {
		case "checksums.txt":
			sums := fr.checksums
			if sums == "" {
				sums = fmt.Sprintf("%s  sshm_darwin_amd64.tar.gz\n%s  %s\n", sha([]byte("其他架構")), sha(fr.archive), testAsset)
			}
			_, _ = w.Write([]byte(sums))
		case testAsset:
			if fr.redirectArchiveTo != "" {
				http.Redirect(w, r, fr.redirectArchiveTo, http.StatusFound)
				return
			}
			if fr.archiveHandler != nil {
				fr.archiveHandler(w, r)
				return
			}
			_, _ = w.Write(fr.archive)
		default:
			http.NotFound(w, r)
		}
	})
	fr.srv = httptest.NewTLSServer(mux)
	t.Cleanup(fr.srv.Close)
	return fr
}

// updater 回傳只允許連到這個假 server 的 Updater。
func (fr *fakeRelease) updater(t *testing.T, version, exe string) *Updater {
	t.Helper()
	host := mustHost(t, fr.srv.URL)
	return &Updater{
		Version:   version,
		APIBase:   fr.srv.URL,
		Client:    fr.srv.Client(),
		AllowURL:  func(u *url.URL) bool { return u.Scheme == "https" && u.Host == host },
		CachePath: filepath.Join(t.TempDir(), "update-check.json"),
		Now:       time.Now,
		GOOS:      "darwin",
		GOARCH:    "arm64",
		ExePath:   exe,
	}
}

func mustHost(t *testing.T, raw string) string {
	t.Helper()
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	return u.Host
}

// installFake 在臨時目錄放一個假的舊版執行檔，回傳路徑。
func installFake(t *testing.T, perm os.FileMode) string {
	t.Helper()
	dir := t.TempDir()
	exe := filepath.Join(dir, "sshm")
	if err := os.WriteFile(exe, []byte("舊版 sshm"), perm); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(exe, perm); err != nil {
		t.Fatal(err)
	}
	return exe
}

type fileSnap struct {
	data  []byte
	mode  os.FileMode
	mtime time.Time
}

func snap(t *testing.T, path string) fileSnap {
	t.Helper()
	st, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return fileSnap{data: b, mode: st.Mode(), mtime: st.ModTime()}
}

// assertUnchanged 確認原檔內容、權限、mtime 不變，且目錄裡沒有殘留暫存檔。
func assertUnchanged(t *testing.T, path string, before fileSnap) {
	t.Helper()
	after := snap(t, path)
	if !bytes.Equal(after.data, before.data) || after.mode != before.mode || !after.mtime.Equal(before.mtime) {
		t.Fatalf("原執行檔被動到：before=%q %v %v after=%q %v %v", before.data, before.mode, before.mtime, after.data, after.mode, after.mtime)
	}
	entries, err := os.ReadDir(filepath.Dir(path))
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if e.Name() != filepath.Base(path) {
			t.Fatalf("目錄殘留檔案：%s", e.Name())
		}
	}
}

func mustURL(t *testing.T, raw string) *url.URL {
	t.Helper()
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	return u
}
