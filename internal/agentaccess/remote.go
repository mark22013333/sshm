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

// KeyOptions 是 authorized_keys 行首的限制。
const KeyOptions = "no-agent-forwarding,no-X11-forwarding"

// okMarker 是遠端腳本成功時印出的標記；expect 的輸出混有登入訊息，以此確認腳本確實跑完。
const okMarker = "SSHM_AGENT_OK"

// AuthorizedKeysLine 組出寫進遠端 authorized_keys 的一行。
func AuthorizedKeysLine(typeBlob, alias string) string {
	return KeyOptions + " " + typeBlob + " " + KeyComment(alias)
}

// keyAwk 處理 authorized_keys：含本機公鑰本體（key type 後緊接相同 base64）的行視為 sshm 的行。
// 行尾的 sshm-agent:<別名> 只給人看、不拿來比對：別台電腦可能以同一別名開通，使用者也可能手寫相同註解。
// line 為空時刪除所有含本公鑰的行；否則第一個就地換成 line、其餘刪除、沒有就附加在最後。
// 程式放在雙引號內（整段腳本不含單引號，才能包進 sh -c '…'），$ 以 \$ 跳脫；CRLF 行尾不影響比對。
const keyAwk = `awk -v kt="$kt" -v kb="$kb" -v line="$line" "{ mine=0; ` +
	`for (i=2; i<=NF; i++) { v=\$i; sub(/\r\$/, \"\", v); if (v==kb && \$(i-1)==kt) { mine=1; break } } ` +
	`if (mine) { if (line!=\"\" && done==0) { print line; done=1 } next } print } ` +
	`END { if (line!=\"\" && done==0) print line }"`

// remoteScript 產生在遠端執行的 POSIX sh 腳本：內容不變時不寫檔；改寫時用 cat > 保留原檔的 inode、權限與 symlink。
// typeBlob 只能來自 parsePublicKey（key type 與 base64 字元）；腳本不含單引號、% 與「${」，
// 可安全包進 /bin/sh -c '…' 並當成 ssh 的 RemoteCommand。
func remoteScript(typeBlob, line string) string {
	kt, kb, _ := strings.Cut(typeBlob, " ")
	steps := []string{
		"umask 077",
		`d="$HOME/.ssh"`,
		`f="$d/authorized_keys"`,
		`kt="` + kt + `"`,
		`kb="` + kb + `"`,
		`line="` + line + `"`,
	}
	if line != "" {
		steps = append(steps,
			`mkdir -p "$d" || exit 3`,
			`chmod 700 "$d" || exit 3`,
			`[ -e "$f" ] || : > "$f" || exit 3`,
			`chmod 600 "$f" || exit 3`,
		)
	} else {
		steps = append(steps, `[ -e "$f" ] || { echo `+okMarker+` absent; exit 0; }`)
	}
	steps = append(steps,
		`t=$(mktemp "$d/.sshm-agent.XXXXXX") || exit 3`,
		keyAwk+` "$f" > "$t" || { rm -f "$t"; exit 3; }`,
		`if cmp -s "$t" "$f"; then rm -f "$t"; echo `+okMarker+` unchanged; exit 0; fi`,
		`cat "$t" > "$f" || { rm -f "$t"; exit 3; }`,
		`rm -f "$t"`,
		`echo `+okMarker+` changed`,
	)
	return strings.Join(steps, "; ")
}

// InstallScript 回傳安裝公鑰的遠端腳本（冪等、只動含本公鑰的那一行）。
func InstallScript(alias, typeBlob string) string {
	return remoteScript(typeBlob, AuthorizedKeysLine(typeBlob, alias))
}

// RemoveScript 回傳移除公鑰的遠端腳本（只刪含本公鑰本體的行）。
func RemoveScript(typeBlob string) string { return remoteScript(typeBlob, "") }

// RemoteCommand 把腳本包成以 /bin/sh 執行的一行指令（避免遠端登入 shell 是 csh／fish 時語法不同）。
func RemoteCommand(script string) string { return "/bin/sh -c '" + script + "'" }

// RemoteRunner 以 sshm 存的密碼登入 h（帳號為 user）並執行 script，回傳輸出。
type RemoteRunner func(ctx context.Context, h store.Host, user, script string) (string, error)

// remoteTimeout 是一次遠端操作的上限；expect 的登入窗口預設 20 秒。
var remoteTimeout = 90 * time.Second

// ExpectRunner 沿用連線用的 expect 腳本自動輸入密碼，透過 ssh 的 RemoteCommand 執行腳本。
// sshArgs 會附加在 ssh 參數中（端對端測試用來指定臨時的 ssh 設定）。
func ExpectRunner(loginScript string, sshArgs ...string) RemoteRunner {
	return func(ctx context.Context, h store.Host, user, script string) (string, error) {
		h.User = user
		extra := []string{
			"-T",
			"-o", "RemoteCommand=" + RemoteCommand(script),
			// 只用密碼登入：避免 ssh-agent 裡的其他金鑰先被嘗試而用光 MaxAuthTries
			"-o", "PubkeyAuthentication=no",
			// 密碼錯誤時 expect 不會再送，讓 ssh 直接放棄而不是卡在第二次提示
			"-o", "NumberOfPasswordPrompts=1",
			"-o", "ControlPath=none", "-o", "ControlMaster=no",
			"-o", "ConnectTimeout=15",
		}
		extra = append(extra, sshArgs...)
		argv, env, err := connect.PasswordLoginArgv(h, loginScript, extra)
		if err != nil {
			return "", err
		}
		ctx, cancel := context.WithTimeout(ctx, remoteTimeout)
		defer cancel()
		cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
		cmd.Env = append(os.Environ(), env...)
		// expect 在使用者端 EOF 時會放棄自動輸入密碼，stdin 必須保持開啟、但不送任何東西
		r, w, err := os.Pipe()
		if err != nil {
			return "", err
		}
		defer r.Close()
		defer w.Close()
		var out strings.Builder
		cmd.Stdin, cmd.Stdout, cmd.Stderr = r, &out, &out
		err = cmd.Run()
		text := strings.ReplaceAll(out.String(), h.Password, "<密碼>")
		if ctx.Err() != nil {
			return text, errors.New("遠端操作逾時")
		}
		if err != nil {
			return text, fmt.Errorf("ssh 結束碼非 0（%v）", err)
		}
		return text, nil
	}
}

// runRemote 執行並確認成功標記，回傳標記後的狀態字（changed／unchanged／absent）。
func runRemote(ctx context.Context, run RemoteRunner, h store.Host, user, script string) (string, error) {
	out, err := run(ctx, h, user, script)
	if err == nil {
		if i := strings.LastIndex(out, okMarker+" "); i >= 0 {
			rest := strings.Fields(out[i+len(okMarker):])
			if len(rest) > 0 {
				return rest[0], nil
			}
		}
		err = errors.New("沒有看到遠端腳本完成的標記")
	}
	return "", &RemoteError{Err: err, Output: lastLines(out, 6)}
}

// RemoteError 是遠端操作失敗；Output 是最後幾行輸出（密碼已遮蔽）。
type RemoteError struct {
	Err    error
	Output string
}

func (e *RemoteError) Error() string {
	if e.Output == "" {
		return e.Err.Error()
	}
	return e.Err.Error() + "；最後輸出：\n" + e.Output
}

func (e *RemoteError) Unwrap() error { return e.Err }

func lastLines(s string, n int) string {
	lines := strings.Split(strings.TrimSpace(strings.ReplaceAll(s, "\r", "")), "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	for i := range lines {
		lines[i] = "  " + store.Printable(lines[i])
	}
	return strings.TrimRight(strings.Join(lines, "\n"), " ")
}
