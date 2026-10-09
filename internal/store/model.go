package store

// CurrentVersion 是本程式寫出的資料格式版本。
const CurrentVersion = 1

// 認證方式。
const (
	AuthPassword = "password"
	AuthKey      = "key"
	AuthNone     = "none"
)

// File 對應 hosts.json 的頂層結構。
type File struct {
	Version int      `json:"version"`
	Groups  []Group  `json:"groups"`
	Hosts   []Host   `json:"hosts"`
	Tunnels []Tunnel `json:"tunnels"`
	Extra   Extra    `json:"-"`
}

type Group struct {
	Name  string `json:"name"`
	Color string `json:"color"`
	Extra Extra  `json:"-"`
}

type Host struct {
	ID            string `json:"id"`
	Name          string `json:"name"`
	Group         string `json:"group"`
	Host          string `json:"host"`
	Port          int    `json:"port"`
	User          string `json:"user"`
	Auth          string `json:"auth"`
	Password      string `json:"password"`
	IdentityFile  string `json:"identityFile"`
	Color         string `json:"color"`
	ExtraArgs     string `json:"extraArgs"`
	CustomCommand string `json:"customCommand"`
	Extra         Extra  `json:"-"`
}

type Tunnel struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	HostID string `json:"hostId"`
	Rules  []Rule `json:"rules"`
	Extra  Extra  `json:"-"`
}

// Rule 是一條轉送規則；type 為 L、R 或 D。
type Rule struct {
	Type        string `json:"type"`
	BindAddress string `json:"bindAddress,omitempty"`
	BindPort    int    `json:"bindPort"`
	TargetHost  string `json:"targetHost,omitempty"`
	TargetPort  int    `json:"targetPort,omitempty"`
	Extra       Extra  `json:"-"`
}

// 以別名型別避開自訂 Marshal 的遞迴。
type (
	fileAlias   File
	groupAlias  Group
	hostAlias   Host
	tunnelAlias Tunnel
	ruleAlias   Rule
)

func (f *File) UnmarshalJSON(b []byte) error {
	var a fileAlias
	extra, err := decodeWithExtra(b, &a)
	if err != nil {
		return err
	}
	*f = File(a)
	f.Extra = extra
	return nil
}

func (f File) MarshalJSON() ([]byte, error) {
	a := fileAlias(f)
	if a.Groups == nil {
		a.Groups = []Group{}
	}
	if a.Hosts == nil {
		a.Hosts = []Host{}
	}
	if a.Tunnels == nil {
		a.Tunnels = []Tunnel{}
	}
	return encodeWithExtra(a, f.Extra)
}

func (g *Group) UnmarshalJSON(b []byte) error {
	var a groupAlias
	extra, err := decodeWithExtra(b, &a)
	if err != nil {
		return err
	}
	*g = Group(a)
	g.Extra = extra
	return nil
}

func (g Group) MarshalJSON() ([]byte, error) { return encodeWithExtra(groupAlias(g), g.Extra) }

func (h *Host) UnmarshalJSON(b []byte) error {
	var a hostAlias
	extra, err := decodeWithExtra(b, &a)
	if err != nil {
		return err
	}
	*h = Host(a)
	h.Extra = extra
	return nil
}

func (h Host) MarshalJSON() ([]byte, error) { return encodeWithExtra(hostAlias(h), h.Extra) }

func (t *Tunnel) UnmarshalJSON(b []byte) error {
	var a tunnelAlias
	extra, err := decodeWithExtra(b, &a)
	if err != nil {
		return err
	}
	*t = Tunnel(a)
	t.Extra = extra
	return nil
}

func (t Tunnel) MarshalJSON() ([]byte, error) {
	a := tunnelAlias(t)
	if a.Rules == nil {
		a.Rules = []Rule{}
	}
	return encodeWithExtra(a, t.Extra)
}

func (r *Rule) UnmarshalJSON(b []byte) error {
	var a ruleAlias
	extra, err := decodeWithExtra(b, &a)
	if err != nil {
		return err
	}
	*r = Rule(a)
	r.Extra = extra
	return nil
}

func (r Rule) MarshalJSON() ([]byte, error) { return encodeWithExtra(ruleAlias(r), r.Extra) }
