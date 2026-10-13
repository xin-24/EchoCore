// Package sticker 管理可信本地素材；不调用模型，也不依赖 QQ 协议。
package sticker

import (
	"bytes"
	"errors"
	"image"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"unicode/utf8"

	"gopkg.in/yaml.v3"
)

const MaxImageBytes = 2 << 20

var ErrCatalog = errors.New("表情素材配置无效，请检查清单、文件类型、大小及路径")
var ErrUnavailable = errors.New("表情素材不可用")

type Config struct {
	Enabled        bool   `yaml:"enabled"`
	Directory      string `yaml:"directory"`
	MaxCandidates  int    `yaml:"max_candidates"`
	MaxToolRounds  int    `yaml:"max_tool_rounds"`
	CooldownMS     int64  `yaml:"cooldown_ms"`
	RepeatWindowMS int64  `yaml:"repeat_window_ms"`
}

func DefaultConfig() Config {
	return Config{Directory: "data/stickers", MaxCandidates: 8, MaxToolRounds: 4, CooldownMS: 60000, RepeatWindowMS: 300000}
}

func (c Config) Validate() error {
	if c.MaxCandidates < 1 || c.MaxCandidates > 20 || c.MaxToolRounds < 1 || c.MaxToolRounds > 6 || c.CooldownMS < 0 || c.CooldownMS > 3600000 || c.RepeatWindowMS < 0 || c.RepeatWindowMS > 3600000 || len(c.Directory) > 2048 || (c.Enabled && strings.TrimSpace(c.Directory) == "") {
		return ErrCatalog
	}
	return nil
}

// Candidate 只包含可供模型阅读的描述，绝不包含路径或文件内容。
type Candidate struct {
	ID          string   `json:"id" yaml:"id"`
	Name        string   `json:"name" yaml:"name"`
	Description string   `json:"description" yaml:"description"`
	Tags        []string `json:"tags" yaml:"tags"`
}

type entry struct {
	Candidate `yaml:",inline"`
	File      string `yaml:"file"`
	Enabled   bool   `yaml:"enabled"`
}

// Catalog 启动时加载不可变清单，每次解析时重新读取并验证图片。
type Catalog struct {
	directory string
	entries   []entry
}

var validID = regexp.MustCompile(`^[a-zA-Z0-9_-]{1,64}$`)

func Load(directory string) (*Catalog, error) {
	directory, err := filepath.Abs(directory)
	if err != nil {
		return nil, ErrCatalog
	}
	root, err := os.OpenRoot(directory)
	if err != nil {
		return nil, ErrCatalog
	}
	defer root.Close()
	f, err := root.Open("catalog.yaml")
	if err != nil {
		return nil, ErrCatalog
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, (128<<10)+1))
	if err != nil || len(data) > 128<<10 {
		return nil, ErrCatalog
	}
	var manifest struct {
		Stickers []entry `yaml:"stickers"`
	}
	d := yaml.NewDecoder(bytes.NewReader(data))
	d.KnownFields(true)
	if d.Decode(&manifest) != nil || d.Decode(new(any)) != io.EOF || len(manifest.Stickers) > 500 {
		return nil, ErrCatalog
	}
	c := &Catalog{directory: directory}
	seen := map[string]bool{}
	for _, e := range manifest.Stickers {
		if !validID.MatchString(e.ID) || seen[e.ID] || !bounded(e.Name, 64) || !bounded(e.Description, 256) || len(e.Tags) > 12 || !filepath.IsLocal(e.File) {
			return nil, ErrCatalog
		}
		seen[e.ID] = true
		for _, tag := range e.Tags {
			if !bounded(tag, 32) {
				return nil, ErrCatalog
			}
		}
		if !e.Enabled {
			continue
		}
		if _, err := c.read(e.File); err != nil {
			return nil, ErrCatalog
		}
		c.entries = append(c.entries, e)
	}
	return c, nil
}

func bounded(s string, n int) bool {
	return strings.TrimSpace(s) != "" && utf8.ValidString(s) && utf8.RuneCountInString(s) <= n
}

func (c *Catalog) Len() int {
	if c == nil {
		return 0
	}
	return len(c.entries)
}

func (c *Catalog) Search(query string, limit int) []Candidate {
	result := []Candidate{}
	if c == nil || limit < 1 {
		return result
	}
	if limit > 20 {
		limit = 20
	}
	query = strings.ToLower(strings.TrimSpace(query))
	for _, e := range c.entries {
		if query != "" && !strings.Contains(strings.ToLower(e.Name+" "+e.Description+" "+strings.Join(e.Tags, " ")), query) {
			continue
		}
		v := e.Candidate
		v.Tags = append([]string(nil), v.Tags...)
		result = append(result, v)
		if len(result) == limit {
			break
		}
	}
	return result
}

func (c *Catalog) Resolve(id string) (Candidate, []byte, error) {
	if c != nil {
		for _, e := range c.entries {
			if e.ID == id {
				data, err := c.read(e.File)
				v := e.Candidate
				v.Tags = append([]string(nil), v.Tags...)
				return v, data, err
			}
		}
	}
	return Candidate{}, nil, ErrUnavailable
}

func (c *Catalog) read(name string) ([]byte, error) {
	// os.Root 在实际打开时阻止 .. 和符号链接逃逸，不依赖先检查后打开。
	root, err := os.OpenRoot(c.directory)
	if err != nil {
		return nil, ErrUnavailable
	}
	defer root.Close()
	f, err := root.Open(name)
	if err != nil {
		return nil, ErrUnavailable
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Size() < 1 || info.Size() > MaxImageBytes {
		return nil, ErrUnavailable
	}
	data, err := io.ReadAll(io.LimitReader(f, MaxImageBytes+1))
	if err != nil || !ValidImage(data) {
		return nil, ErrUnavailable
	}
	return data, nil
}

// ValidImage 限制压缩大小和解码像素；GIF 只验证首帧，不将所有动画帧展开到内存。
func ValidImage(data []byte) bool {
	if len(data) == 0 || len(data) > MaxImageBytes {
		return false
	}
	cfg, format, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil || (format != "png" && format != "jpeg" && format != "gif") || cfg.Width < 1 || cfg.Height < 1 || int64(cfg.Width)*int64(cfg.Height) > 4_000_000 {
		return false
	}
	_, _, err = image.Decode(bytes.NewReader(data))
	return err == nil
}
