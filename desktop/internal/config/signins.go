package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"sync"
)

// 应用可以同时登录多个 JMAP 服务器。config.json 的顶层字段仍然描述主登录 (旧版本照样读得懂,
// 只是看不到其余的登录), 其余登录放在 signIns 里, 各带一个本机账号 id 的前缀 (见 internal/accountid)。
//
// 前缀按 lastSignIn 递增分配, 删掉的登录不归还前缀: 受信发件人、译文、设置都按账号 id 记在本机,
// 前缀复用会让新登录继承上一个登录留下的这些数据。

// SignIn 是主登录以外的一个登录。
type SignIn struct {
	// ID 是该登录的账号 id 前缀, 如 "s2"。
	ID string `json:"id"`
	Config
}

// File 是 config.json 的全部内容。
type File struct {
	// Config 是主登录, 字段直接写在顶层。
	Config
	// SignIns 是其余登录, 按添加顺序。
	SignIns []SignIn `json:"signIns,omitempty"`
	// LastSignIn 是最后分配出去的前缀序号; 主登录算 1, 下一个附加登录是 s2。
	LastSignIn int `json:"lastSignIn,omitempty"`
}

// signInPrefix 是附加登录前缀的固定开头, 后接序号。
const signInPrefix = "s"

// AddSignIn 登记一个附加登录, 返回分配的前缀。前缀不会与任何用过的重复。
func (f *File) AddSignIn(cfg Config) string {
	id := f.ReserveSignIn()
	f.SignIns = append(f.SignIns, SignIn{ID: id, Config: cfg})
	return id
}

// ReserveSignIn 分配一个新前缀但还不登记登录: 添加登录要先连通再落盘, 连通之前就得知道前缀。
// 连不通时前缀作废, 不会再分配给别的登录。
func (f *File) ReserveSignIn() string {
	if f.LastSignIn < 1 {
		f.LastSignIn = 1
	}
	// 文件被手改过 (或从更新的版本降级回来) 时, 序号可能落后于已有的前缀: 跳过它们。
	for _, s := range f.SignIns {
		if n, ok := signInNumber(s.ID); ok && n > f.LastSignIn {
			f.LastSignIn = n
		}
	}
	f.LastSignIn++
	return signInPrefix + strconv.Itoa(f.LastSignIn)
}

// PutSignIn 改写某个登录的配置, 附加登录不存在时登记它 (用 ReserveSignIn 分到的前缀)。空 id 是主登录。
func (f *File) PutSignIn(id string, cfg Config) {
	if f.SetSignIn(id, cfg) != nil {
		f.SignIns = append(f.SignIns, SignIn{ID: id, Config: cfg})
	}
}

// RemoveSignIn 删掉一个附加登录, 返回它原来的配置。LastSignIn 不回退。
func (f *File) RemoveSignIn(id string) (Config, bool) {
	for i, s := range f.SignIns {
		if s.ID == id {
			f.SignIns = append(f.SignIns[:i], f.SignIns[i+1:]...)
			return s.Config, true
		}
	}
	return Config{}, false
}

// SignIn 返回某个登录的配置, 空 id 是主登录。
func (f *File) SignIn(id string) (Config, bool) {
	if id == "" {
		return f.Config, true
	}
	for _, s := range f.SignIns {
		if s.ID == id {
			return s.Config, true
		}
	}
	return Config{}, false
}

// SetSignIn 改写某个已有登录的配置, 空 id 是主登录。
func (f *File) SetSignIn(id string, cfg Config) error {
	if id == "" {
		f.Config = cfg
		return nil
	}
	for i := range f.SignIns {
		if f.SignIns[i].ID == id {
			f.SignIns[i].Config = cfg
			return nil
		}
	}
	return fmt.Errorf("2021 unknown sign-in %q", id)
}

// Find 返回已登录同一服务器、同一用户的登录前缀 (主登录为空串)。同一个身份不能登录两次。
func (f *File) Find(sessionEndpoint, username string) (string, bool) {
	same := func(c Config) bool {
		return c.Configured() && c.SessionEndpoint == sessionEndpoint && strings.EqualFold(c.Username, username)
	}
	if same(f.Config) {
		return "", true
	}
	for _, s := range f.SignIns {
		if same(s.Config) {
			return s.ID, true
		}
	}
	return "", false
}

func signInNumber(id string) (int, bool) {
	n, err := strconv.Atoi(strings.TrimPrefix(id, signInPrefix))
	return n, err == nil && strings.HasPrefix(id, signInPrefix)
}

// fileMu 串行化 config.json 的读改写: 两个登录同时刷新配置时不能互相覆盖。
var fileMu sync.Mutex

// LoadFile 读取整个 config.json, 文件不存在时返回零值。
func LoadFile() (File, error) {
	fileMu.Lock()
	defer fileMu.Unlock()
	return loadFile()
}

func loadFile() (File, error) {
	var f File
	path, err := filePath()
	if err != nil {
		return f, err
	}
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return f, nil
	}
	if err != nil {
		return f, fmt.Errorf("2012 read config: %w", err)
	}
	if err := json.Unmarshal(data, &f); err != nil {
		return f, fmt.Errorf("2013 parse config: %w", err)
	}
	return f, nil
}

func saveFile(f File) error {
	path, err := filePath()
	if err != nil {
		return err
	}
	data, err := json.MarshalIndent(f, "", "  ")
	if err != nil {
		return fmt.Errorf("2014 encode config: %w", err)
	}
	// 权限 0600: 虽不含凭据, 但暴露了邮箱地址与服务器。
	return os.WriteFile(path, data, 0o600)
}

// UpdateFile 读出 config.json, 交给 fn 修改后写回; fn 返回错误时不写。
func UpdateFile(fn func(*File) error) error {
	fileMu.Lock()
	defer fileMu.Unlock()
	f, err := loadFile()
	if err != nil {
		return err
	}
	if err := fn(&f); err != nil {
		return err
	}
	return saveFile(f)
}
