package legacyconfig

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"encoding/json"
	"github.com/qjfoidnh/BaiduPCS-Go/internal/domain/configuration"
	"github.com/qjfoidnh/BaiduPCS-Go/internal/domain/fault"
	"github.com/qjfoidnh/BaiduPCS-Go/internal/ports"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
)

type Sources struct {
	ServiceFile, LegacyFile string
	startupHash             [32]byte
}

func NewSources(service, legacy string) (*Sources, error) {
	s := &Sources{ServiceFile: service, LegacyFile: legacy}
	if service != "" {
		b, e := readFixed(service)
		if e != nil {
			return nil, e
		}
		h, e := startupHash(b)
		if e != nil {
			return nil, e
		}
		s.startupHash = h
	}
	return s, nil
}
func startupHash(b []byte) ([32]byte, error) {
	var v map[string]json.RawMessage
	if e := json.Unmarshal(b, &v); e != nil {
		return [32]byte{}, e
	}
	delete(v, "client")
	b, e := json.Marshal(v)
	return sha256.Sum256(b), e
}
func readFixed(name string) ([]byte, error) {
	info, e := os.Lstat(name)
	if e != nil || !info.Mode().IsRegular() || info.Size() > 1<<20 {
		return nil, fault.New(fault.Unavailable, "CONFIGURATION_SOURCE_UNAVAILABLE", "固定配置来源不可用")
	}
	f, e := os.Open(name)
	if e != nil {
		return nil, e
	}
	defer f.Close()
	after, e := f.Stat()
	if e != nil || !os.SameFile(info, after) {
		return nil, fault.New(fault.Conflict, "CONFIGURATION_SOURCE_CHANGED", "固定配置来源已变化")
	}
	b, e := io.ReadAll(io.LimitReader(f, (1<<20)+1))
	if e != nil || len(b) > 1<<20 {
		return nil, fault.New(fault.Unavailable, "CONFIGURATION_SOURCE_UNAVAILABLE", "配置来源读取失败")
	}
	return b, nil
}
func (s *Sources) LoadConfiguration(ctx context.Context, source string) (configuration.Patch, error) {
	var patch configuration.Patch
	name := s.ServiceFile
	if source == "legacy_cli" {
		name = s.LegacyFile
	}
	if name == "" {
		return patch, fault.New(fault.Unsupported, "CONFIGURATION_SOURCE_NOT_CONFIGURED", "此固定来源未配置")
	}
	b, e := readFixed(name)
	if e != nil {
		return patch, e
	}
	var data map[string]json.RawMessage
	if json.Unmarshal(b, &data) != nil {
		return patch, fault.New(fault.Unsupported, "INVALID_CONFIGURATION", "配置来源不是有效JSON对象")
	}
	if source == "service" {
		h, e := startupHash(b)
		if e != nil {
			return patch, e
		}
		if h != s.startupHash {
			return patch, fault.New(fault.Unsupported, "STARTUP_CONFIGURATION_CHANGE_REQUIRES_RESTART", "身份、根目录、监听或存储配置变更需要重启")
		}
		b = data["client"]
		if len(b) == 0 {
			b = []byte(`{}`)
		}
	} else {
		clean := map[string]json.RawMessage{}
		for _, field := range configuration.Fields {
			if v, ok := data[field]; ok {
				clean[field] = v
			}
		}
		if v, ok := data["u_policy"]; ok {
			clean["upload_policy"] = v
		}
		for _, field := range []string{"proxy_hostnames", "local_addrs"} {
			if v, ok := clean[field]; ok {
				var value string
				if json.Unmarshal(v, &value) == nil {
					values := []string{}
					if value != "" {
						values = strings.Split(value, ",")
					}
					clean[field], _ = json.Marshal(values)
				}
			}
		}
		if v, ok := clean["savedir"]; ok {
			var value string
			if json.Unmarshal(v, &value) == nil {
				if value != "" {
					return patch, fault.New(fault.Unsupported, "LOCAL_PATH_FORBIDDEN", "旧savedir必须先映射为受控根目录引用")
				}
				delete(clean, "savedir")
			}
		}
		b, _ = json.Marshal(clean)
	}
	// The service source uses wire decimal strings; legacy files use JSON numbers.
	var fields map[string]json.RawMessage
	if json.Unmarshal(b, &fields) != nil || fields == nil {
		return patch, fault.New(fault.Unsupported, "INVALID_CONFIGURATION", "客户端配置无效")
	}
	for _, raw := range fields {
		if bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
			return patch, fault.New(fault.Unsupported, "INVALID_CONFIGURATION", "重载字段不能为null")
		}
	}
	for _, field := range []string{"cache_size", "max_download_rate", "max_upload_rate"} {
		if raw, ok := fields[field]; ok && len(raw) > 0 && raw[0] == '"' {
			var value string
			if json.Unmarshal(raw, &value) != nil {
				return patch, fault.New(fault.Unsupported, "INVALID_CONFIGURATION", "字节字段无效")
			}
			n, e := strconv.ParseInt(value, 10, 64)
			if e != nil || n < 0 || strconv.FormatInt(n, 10) != value {
				return patch, fault.New(fault.Unsupported, "INVALID_CONFIGURATION", "字节字段无效")
			}
			fields[field] = json.RawMessage(value)
		}
	}
	b, _ = json.Marshal(fields)
	d := json.NewDecoder(bytes.NewReader(b))
	d.DisallowUnknownFields()
	if e = d.Decode(&patch); e != nil {
		return patch, fault.New(fault.Unsupported, "INVALID_CONFIGURATION", "客户端配置包含无效或启动字段")
	}
	return patch, ctx.Err()
}

type Defaults struct {
	File       string
	Bindings   map[int]string
	mu         sync.Mutex
	salt       [32]byte
	identities map[int][32]byte
}

func NewDefaults(file string, bindings map[int]string) *Defaults {
	s := &Defaults{File: file, Bindings: bindings, identities: map[int][32]byte{}}
	if file != "" {
		if b, e := readFixed(file); e == nil {
			var input struct {
				Users []map[string]json.RawMessage `json:"baidu_user_list"`
			}
			if json.Unmarshal(b, &input) == nil {
				for i, user := range input.Users {
					delete(user, "workdir")
					encoded, _ := json.Marshal(user)
					s.identities[i] = sha256.Sum256(encoded)
				}
			}
		}
	}
	_, _ = rand.Read(s.salt[:])
	return s
}
func (s *Defaults) revision(b []byte) uint64 {
	h := hmac.New(sha256.New, s.salt[:])
	h.Write(b)
	return binary.BigEndian.Uint64(h.Sum(nil)[:8])>>1 | 1
}
func (s *Defaults) read() (ports.CLIDefaults, map[string]json.RawMessage, error) {
	out := ports.CLIDefaults{Revision: 1, Accounts: []ports.CLIAccount{}}
	if s.File == "" {
		return out, nil, nil
	}
	b, e := readFixed(s.File)
	if e != nil {
		return out, nil, e
	}
	var v map[string]json.RawMessage
	if json.Unmarshal(b, &v) != nil {
		return out, nil, fault.New(fault.Unsupported, "INVALID_CONFIGURATION", "CLI配置无效")
	}
	var active json.Number
	_ = json.Unmarshal(v["baidu_active_uid"], &active)
	var users []map[string]json.RawMessage
	if json.Unmarshal(v["baidu_user_list"], &users) != nil {
		return out, nil, fault.New(fault.Unsupported, "INVALID_CONFIGURATION", "CLI账号列表无效")
	}
	for i, u := range users {
		copyUser := map[string]json.RawMessage{}
		for k, v := range u {
			if k != "workdir" {
				copyUser[k] = v
			}
		}
		encoded, _ := json.Marshal(copyUser)
		if known, ok := s.identities[i]; !ok || known != sha256.Sum256(encoded) {
			return out, nil, fault.New(fault.Conflict, "LEGACY_ACCOUNT_MAPPING_CHANGED", "旧CLI账号来源发生变化，需要重新装配映射")
		}
		id := s.Bindings[i]
		if id == "" {
			continue
		}
		var cwd string
		_ = json.Unmarshal(u["workdir"], &cwd)
		if cwd == "" {
			cwd = "/"
		}
		var uid json.Number
		_ = json.Unmarshal(u["uid"], &uid)
		if uid == active && active != "" {
			if out.Default != nil {
				return out, nil, fault.New(fault.Conflict, "AMBIGUOUS_LEGACY_ACCOUNT", "旧UID对应多个账号，不能猜测默认账号")
			}
			copy := id
			out.Default = &copy
		}
		out.Accounts = append(out.Accounts, ports.CLIAccount{ID: id, Cwd: cwd})
	}
	out.Revision = s.revision(b)
	return out, v, nil
}
func (s *Defaults) ReadDefaults(ctx context.Context) (ports.CLIDefaults, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	v, _, e := s.read()
	return v, e
}
func (s *Defaults) WriteDefaults(ctx context.Context, revision uint64, account *string, hasAccount bool, accounts []ports.CLIAccount, hasAccounts bool) (ports.CLIDefaults, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	old, data, e := s.read()
	if e != nil {
		return old, e
	}
	if s.File == "" {
		return old, fault.New(fault.Unsupported, "CLI_CONFIGURATION_NOT_CONFIGURED", "未配置独立的固定CLI配置快照")
	}
	if old.Revision != revision {
		return old, fault.New(fault.Precondition, "REVISION_MISMATCH", "CLI配置版本已改变")
	}
	var users []map[string]json.RawMessage
	_ = json.Unmarshal(data["baidu_user_list"], &users)
	indices := map[string]int{}
	for i, id := range s.Bindings {
		indices[id] = i
	}
	if hasAccount {
		uid := json.RawMessage(`0`)
		if account != nil {
			i, ok := indices[*account]
			if !ok || i >= len(users) {
				return old, fault.New(fault.Unsupported, "LEGACY_ACCOUNT_MAPPING_UNAVAILABLE", "账号没有旧CLI映射")
			}
			uid = users[i]["uid"]
			var canonical json.Number
			if json.Unmarshal(uid, &canonical) != nil {
				return old, fault.New(fault.Unsupported, "LEGACY_ACCOUNT_MAPPING_UNAVAILABLE", "CLI默认账号缺少规范UID")
			}
			n, err := strconv.ParseUint(string(canonical), 10, 64)
			if err != nil || n == 0 || strconv.FormatUint(n, 10) != string(canonical) {
				return old, fault.New(fault.Unsupported, "LEGACY_ACCOUNT_MAPPING_UNAVAILABLE", "CLI默认账号UID无效")
			}
			for j, user := range users {
				if j != i {
					var other json.Number
					_ = json.Unmarshal(user["uid"], &other)
					if canonical == other {
						return old, fault.New(fault.Conflict, "AMBIGUOUS_LEGACY_ACCOUNT", "重复UID不能表示唯一CLI默认账号")
					}
				}
			}
			if len(uid) == 0 {
				return old, fault.New(fault.Unsupported, "LEGACY_ACCOUNT_MAPPING_UNAVAILABLE", "旧CLI账号缺少UID")
			}
		}
		data["baidu_active_uid"] = uid
	}
	if hasAccounts {
		for _, a := range accounts {
			i, ok := indices[a.ID]
			if !ok || i >= len(users) {
				return old, fault.New(fault.Unsupported, "LEGACY_ACCOUNT_MAPPING_UNAVAILABLE", "账号没有旧CLI映射")
			}
			users[i]["workdir"], _ = json.Marshal(a.Cwd)
		}
		data["baidu_user_list"], _ = json.Marshal(users)
	}
	b, _ := json.MarshalIndent(data, "", "  ")
	if e := ctx.Err(); e != nil {
		return old, e
	}
	f, e := os.CreateTemp(filepath.Dir(s.File), ".http-cli-defaults-*")
	if e != nil {
		return old, e
	}
	name := f.Name()
	defer os.Remove(name)
	if _, e = f.Write(append(b, '\n')); e == nil {
		e = f.Sync()
	}
	e2 := f.Close()
	if e == nil {
		e = e2
	}
	if e != nil {
		return old, e
	}
	current, _, e := s.read()
	if e != nil {
		return old, e
	}
	if current.Revision != revision {
		return old, fault.New(fault.Precondition, "REVISION_MISMATCH", "CLI配置来源并发改变")
	}
	if e = os.Rename(name, s.File); e != nil {
		return old, e
	}
	out, _, e := s.read()
	return out, e
}
