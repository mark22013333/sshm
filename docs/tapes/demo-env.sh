# 截圖用的展示環境。在 repo 根目錄執行：. docs/tapes/demo-env.sh
# 每次建立一個全新的暫存資料目錄，放入 demo/ 的虛構資料與假 orca，不會讀寫 ~/.config/sshm 與 ~/.ssh。
# 固定放在 /tmp 底下（以 pwd -P 解析成 /private/tmp），讓「關於」頁顯示的路徑短且不含使用者名稱。
SSHM_DEMO=$(cd "$(mktemp -d /tmp/sshm-demo.XXXXXX)" && pwd -P) || return 1
mkdir -p "$SSHM_DEMO/sshm" "$SSHM_DEMO/bin" "$SSHM_DEMO/home/.ssh"
chmod 700 "$SSHM_DEMO/home/.ssh"
# agent 欄位的金鑰路徑必須在（臨時）HOME 的 ~/.ssh/sshm/ 底下，清單才會顯示正常的 🤖 標記
sed "s|__DEMO_HOME__|$SSHM_DEMO/home|g" docs/tapes/demo/hosts.json > "$SSHM_DEMO/sshm/hosts.json"
cp docs/tapes/demo/state.json "$SSHM_DEMO/sshm/"
chmod 700 "$SSHM_DEMO/sshm"
chmod 600 "$SSHM_DEMO/sshm/hosts.json" "$SSHM_DEMO/sshm/state.json"
cp docs/tapes/demo/orca "$SSHM_DEMO/bin/orca"
chmod 755 "$SSHM_DEMO/bin/orca"
# 以最近的 tag 當版本號，「關於」頁才會顯示正式版而非 (devel)
SSHM_DEMO_VERSION=$(git describe --tags --abbrev=0 2>/dev/null)
go build -ldflags "-X main.version=$SSHM_DEMO_VERSION" -o "$SSHM_DEMO/bin/sshm" . || return 1
# 預先寫好今天的更新檢查快取：錄製時不連 GitHub，「關於」頁固定顯示「已是最新版」
printf '{"checkedAt":"%s","latest":"%s"}\n' "$(date -u +%Y-%m-%dT%H:%M:%SZ)" "$SSHM_DEMO_VERSION" > "$SSHM_DEMO/sshm/update-check.json"
chmod 600 "$SSHM_DEMO/sshm/update-check.json"
# 讓 sshm 以為在 Orca 中，但 ORCA_CLI_BIN_DIR 指向假 orca；HOME 在 go build 之後才換成臨時目錄（避免重抓模組快取）
export XDG_CONFIG_HOME="$SSHM_DEMO" TERM_PROGRAM=Orca ORCA_CLI_BIN_DIR="$SSHM_DEMO/bin" HOME="$SSHM_DEMO/home"
export PATH="$SSHM_DEMO/bin:$PATH" PS1='$ '
