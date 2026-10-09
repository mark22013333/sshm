package connect

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// OrcaEnv 描述是否在 Orca terminal 中執行。
type OrcaEnv struct {
	BinDir string
}

// DetectOrca 依 TERM_PROGRAM=Orca 且 ORCA_CLI_BIN_DIR 有值判斷；getenv 通常傳 os.Getenv。
func DetectOrca(getenv func(string) string) (OrcaEnv, bool) {
	if getenv("TERM_PROGRAM") != "Orca" {
		return OrcaEnv{}, false
	}
	dir := getenv("ORCA_CLI_BIN_DIR")
	if dir == "" {
		return OrcaEnv{}, false
	}
	return OrcaEnv{BinDir: dir}, true
}

// Title 組分頁標題「<emoji> <名稱>」；沒有顏色時只有名稱。
func Title(emoji, name string) string {
	if emoji == "" {
		return name
	}
	return emoji + " " + name
}

// CreateArgv 組 `orca terminal create` 的完整 argv。
func (o OrcaEnv) CreateArgv(title, text string) []string {
	return []string{
		filepath.Join(o.BinDir, "orca"), "terminal", "create",
		"--worktree", "active",
		"--title=" + title,
		"--command=" + text,
		"--focus", "--json",
	}
}

// Runner 執行外部指令並分別回傳 stdout、stderr；測試可替換。
type Runner func(ctx context.Context, argv []string) (stdout, stderr []byte, err error)

// ExecRunner 以 os/exec 執行。
func ExecRunner(ctx context.Context, argv []string) ([]byte, []byte, error) {
	var out, errBuf bytes.Buffer
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	cmd.Stdout, cmd.Stderr = &out, &errBuf
	err := cmd.Run()
	return out.Bytes(), errBuf.Bytes(), err
}

// OpenTab 在 Orca 開新分頁執行 text，回傳 terminal handle（可能為空）。JSON 只從 stdout 解析。
func (o OrcaEnv) OpenTab(run Runner, title, text string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	return openWith(ctx, run, o.CreateArgv(title, text))
}

func openWith(ctx context.Context, run Runner, argv []string) (string, error) {
	out, stderr, err := run(ctx, argv)
	handle, perr := parseHandle(out)
	if err != nil {
		if perr != nil {
			return "", perr
		}
		msg := strings.TrimSpace(string(stderr))
		if msg == "" {
			msg = strings.TrimSpace(string(out))
		}
		if msg == "" {
			return "", fmt.Errorf("orca terminal create 失敗：%w", err)
		}
		return "", fmt.Errorf("orca terminal create 失敗：%v：%s", err, msg)
	}
	return handle, perr
}

// parseHandle 解析 --json 回應；{"ok":false,"error":{...}} 視為失敗。
func parseHandle(out []byte) (string, error) {
	var resp struct {
		OK     *bool `json:"ok"`
		Result struct {
			Terminal struct {
				Handle string `json:"handle"`
			} `json:"terminal"`
		} `json:"result"`
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	trimmed := bytes.TrimSpace(out)
	if len(trimmed) == 0 {
		return "", nil
	}
	if err := json.Unmarshal(trimmed, &resp); err != nil {
		// 輸出格式不符只是拿不到 handle，成敗以結束碼為準。
		return "", nil
	}
	if resp.OK != nil && !*resp.OK {
		msg := resp.Error.Message
		if msg == "" {
			msg = resp.Error.Code
		}
		return "", errors.New("orca terminal create 失敗：" + msg)
	}
	return resp.Result.Terminal.Handle, nil
}
