# 截圖用的展示環境。在 repo 根目錄執行：. docs/tapes/demo-env.sh
# 每次建立一個全新的暫存資料目錄，放入 demo/ 的虛構資料與假 orca，不會讀寫 ~/.config/sshm。
SSHM_DEMO=$(mktemp -d "${TMPDIR:-/tmp}/sshm-demo.XXXXXX") || return 1
mkdir -p "$SSHM_DEMO/sshm" "$SSHM_DEMO/bin"
cp docs/tapes/demo/hosts.json docs/tapes/demo/state.json "$SSHM_DEMO/sshm/"
chmod 700 "$SSHM_DEMO/sshm"
chmod 600 "$SSHM_DEMO/sshm/hosts.json" "$SSHM_DEMO/sshm/state.json"
cp docs/tapes/demo/orca "$SSHM_DEMO/bin/orca"
chmod 755 "$SSHM_DEMO/bin/orca"
go build -o "$SSHM_DEMO/bin/sshm" . || return 1
# 讓 sshm 以為在 Orca 中，但 ORCA_CLI_BIN_DIR 指向假 orca
export XDG_CONFIG_HOME="$SSHM_DEMO" TERM_PROGRAM=Orca ORCA_CLI_BIN_DIR="$SSHM_DEMO/bin"
export PATH="$SSHM_DEMO/bin:$PATH" PS1='$ '
