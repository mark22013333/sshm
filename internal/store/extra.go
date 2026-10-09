package store

import (
	"bytes"
	"encoding/json"
	"fmt"
	"reflect"
	"sort"
	"strings"
)

// Extra 保存讀入時不認得的欄位，寫回時原樣附加（為未來的 plugin 預留）。
type Extra map[string]json.RawMessage

// knownKeys 從 struct 的 json tag 取出已知欄位名（小寫）；encoding/json 比對欄位不分大小寫，這裡一致。
func knownKeys(t reflect.Type) map[string]bool {
	keys := map[string]bool{}
	for i := 0; i < t.NumField(); i++ {
		tag := t.Field(i).Tag.Get("json")
		name, _, _ := strings.Cut(tag, ",")
		if name != "" && name != "-" {
			keys[strings.ToLower(name)] = true
		}
	}
	return keys
}

// decodeWithExtra 先解進 dst（必須是指向「無自訂 Unmarshal 的別名型別」的指標），再收集未知欄位。
func decodeWithExtra(data []byte, dst any) (Extra, error) {
	if err := json.Unmarshal(data, dst); err != nil {
		return nil, err
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, err
	}
	known := knownKeys(reflect.TypeOf(dst).Elem())
	var extra Extra
	for k, v := range raw {
		if known[strings.ToLower(k)] {
			continue
		}
		if extra == nil {
			extra = Extra{}
		}
		extra[k] = v
	}
	return extra, nil
}

// encodeWithExtra 依 struct 欄位順序輸出，再把未知欄位依字母序接在後面。
func encodeWithExtra(src any, extra Extra) ([]byte, error) {
	base, err := json.Marshal(src)
	if err != nil {
		return nil, err
	}
	if len(extra) == 0 {
		return base, nil
	}
	known := knownKeys(reflect.TypeOf(src))
	names := make([]string, 0, len(extra))
	for k := range extra {
		if !known[strings.ToLower(k)] {
			names = append(names, k)
		}
	}
	sort.Strings(names)
	var buf bytes.Buffer
	buf.Write(bytes.TrimSuffix(base, []byte("}")))
	first := bytes.Equal(base, []byte("{}"))
	for _, k := range names {
		if !first {
			buf.WriteByte(',')
		}
		first = false
		key, _ := json.Marshal(k)
		buf.Write(key)
		buf.WriteByte(':')
		if !json.Valid(extra[k]) {
			return nil, fmt.Errorf("未知欄位 %q 的內容不是合法 JSON", k)
		}
		buf.Write(extra[k])
	}
	buf.WriteByte('}')
	return buf.Bytes(), nil
}
