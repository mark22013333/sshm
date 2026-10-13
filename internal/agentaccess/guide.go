package agentaccess

// Guide 是給 AI agent 指引檔（CLAUDE.md、AGENTS.md 等）的說明文字。
const Guide = `## 連線到遠端機器（sshm）

這台電腦用 sshm 管理 SSH 機器。已開通給 AI agent 的機器各有一個 ssh 別名與專用金鑰，
你不需要、也不可以取得任何密碼。

1. 先列出可用的機器：
     sshm agent list --json
   每筆含 name（機器名稱）、alias（ssh 別名）、host、port、user、group。
2. 用別名連線或執行指令：
     ssh <alias> '<指令>'
   例：ssh sshm-prod-vm 'uptime'
3. 規則：
   - 只能連清單裡的機器；清單沒有的，請使用者用 sshm 開通（sshm agent enable <機器名稱>）。
   - 不要要求、搜尋或讀取密碼，也不要讀 ~/.config/sshm/hosts.json。
   - 連線失敗（Permission denied 等）時回報給使用者，不要改用密碼或其他帳號重試。
   - 不要修改 ~/.ssh/sshm_agents.conf 或 ~/.ssh/sshm/ 底下的金鑰。
`
