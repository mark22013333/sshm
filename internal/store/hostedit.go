package store

import (
	"errors"
	"fmt"
)

// ErrHostGone 表示要編輯的機器已被其他 sshm 刪除。
var ErrHostGone = errors.New("這台機器已被其他 sshm 刪除，無法儲存編輯")

// ErrEditConflict 表示連線欄位與密碼／金鑰被兩個 sshm 分別修改，合併會配錯。
var ErrEditConflict = errors.New("這台機器剛被其他 sshm 修改，請重新開啟表單")

func effectivePort(p int) int {
	if p <= 0 {
		return 22
	}
	return p
}

// connChanged 比較決定「連到哪台、用哪種方式」的欄位。
func connChanged(a, b Host) bool {
	return a.Host != b.Host || effectivePort(a.Port) != effectivePort(b.Port) || a.User != b.User || a.Auth != b.Auth
}

func credChanged(a, b Host) bool {
	return a.Password != b.Password || a.IdentityFile != b.IdentityFile
}

// ApplyHostEdit 以 id 找到最新的機器，只套用 orig→edited 之間有改動的欄位，保留其他實例對未改欄位的變更。
func (f *File) ApplyHostEdit(orig, edited Host) error {
	i := f.HostIndex(orig.ID)
	if i < 0 {
		return ErrHostGone
	}
	cur := f.Hosts[i]
	// 一方改了連線欄位、另一方改了密碼／金鑰：無法判斷密碼屬於哪個連線，要求重開表單
	if (connChanged(orig, cur) && credChanged(orig, edited)) || (credChanged(orig, cur) && connChanged(orig, edited)) {
		return ErrEditConflict
	}
	set := func(dst *string, o, e string) {
		if o != e {
			*dst = e
		}
	}
	set(&cur.Name, orig.Name, edited.Name)
	set(&cur.Group, orig.Group, edited.Group)
	set(&cur.Host, orig.Host, edited.Host)
	set(&cur.User, orig.User, edited.User)
	set(&cur.Auth, orig.Auth, edited.Auth)
	set(&cur.Password, orig.Password, edited.Password)
	set(&cur.IdentityFile, orig.IdentityFile, edited.IdentityFile)
	set(&cur.Color, orig.Color, edited.Color)
	set(&cur.ExtraArgs, orig.ExtraArgs, edited.ExtraArgs)
	set(&cur.CustomCommand, orig.CustomCommand, edited.CustomCommand)
	if effectivePort(orig.Port) != effectivePort(edited.Port) {
		cur.Port = edited.Port
	}
	// 合併後若不是密碼認證，不留下明碼密碼
	if cur.Auth != AuthPassword {
		cur.Password = ""
	}
	if err := ValidateHost(cur); err != nil {
		return fmt.Errorf("合併其他 sshm 的變更後資料不合法：%w", err)
	}
	f.EnsureGroup(cur.Group)
	f.Hosts[i] = cur
	return nil
}
