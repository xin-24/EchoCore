package sticker

import (
	"bytes"
	"image"
	"image/color"
	"image/gif"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func testImage(t *testing.T, animated bool) []byte {
	t.Helper()
	var b bytes.Buffer
	if animated {
		frame := image.NewPaletted(image.Rect(0, 0, 2, 2), color.Palette{color.Black, color.White})
		if err := gif.EncodeAll(&b, &gif.GIF{Image: []*image.Paletted{frame, frame}, Delay: []int{10, 10}}); err != nil {
			t.Fatal(err)
		}
	} else if err := png.Encode(&b, image.NewRGBA(image.Rect(0, 0, 2, 2))); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

func testCatalog(t *testing.T) (*Catalog, string) {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "cat.gif"), testImage(t, true), 0600); err != nil {
		t.Fatal(err)
	}
	manifest := "stickers:\n  - id: cat\n    name: 猫猫无语\n    description: 表达无奈\n    tags: [无奈]\n    file: cat.gif\n    enabled: true\n"
	if err := os.WriteFile(filepath.Join(dir, "catalog.yaml"), []byte(manifest), 0600); err != nil {
		t.Fatal(err)
	}
	c, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	return c, dir
}

func TestCatalogSearchResolveAndBounds(t *testing.T) {
	c, dir := testCatalog(t)
	got := c.Search("无奈", 8)
	if len(got) != 1 || got[0].ID != "cat" || len(c.Search("找不到", 8)) != 0 {
		t.Fatal("查询错误")
	}
	got[0].Tags[0] = "污染"
	if c.Search("", 1)[0].Tags[0] != "无奈" {
		t.Fatal("目录被外部修改")
	}
	_, data, err := c.Resolve("cat")
	if err != nil || !ValidImage(data) {
		t.Fatal("无法读取 GIF")
	}
	if _, _, err = c.Resolve("../../secret"); err == nil {
		t.Fatal("接受了未知素材")
	}
	if err = os.WriteFile(filepath.Join(dir, "cat.gif"), []byte("失效"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, _, err = c.Resolve("cat"); err == nil {
		t.Fatal("没有重新校验失效文件")
	}
	if !ValidImage(testImage(t, false)) || ValidImage([]byte("not image")) || ValidImage(make([]byte, MaxImageBytes+1)) {
		t.Fatal("类型或大小检查错误")
	}
}

func TestCatalogRejectsUnsafeManifest(t *testing.T) {
	for _, mode := range []string{"duplicate", "missing", "escape", "absolute", "symlink", "invalid", "oversize", "unknown_field"} {
		t.Run(mode, func(t *testing.T) {
			_, dir := testCatalog(t)
			path := "cat.gif"
			switch mode {
			case "missing":
				path = "missing.gif"
			case "escape":
				path = "../cat.gif"
			case "absolute":
				path = filepath.Join(dir, "cat.gif")
			case "symlink":
				outside := filepath.Join(t.TempDir(), "private.gif")
				if err := os.WriteFile(outside, testImage(t, true), 0600); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(outside, filepath.Join(dir, "link.gif")); err != nil {
					t.Fatal(err)
				}
				path = "link.gif"
			case "invalid":
				if err := os.WriteFile(filepath.Join(dir, path), []byte("text"), 0600); err != nil {
					t.Fatal(err)
				}
			case "oversize":
				if err := os.WriteFile(filepath.Join(dir, path), make([]byte, MaxImageBytes+1), 0600); err != nil {
					t.Fatal(err)
				}
			}
			row := "  - id: cat\n    name: 猫\n    description: 无奈\n    file: " + path + "\n    enabled: true\n"
			manifest := "stickers:\n" + row
			if mode == "duplicate" {
				manifest += row
			}
			if mode == "unknown_field" {
				manifest += "    secret: nope\n"
			}
			if err := os.WriteFile(filepath.Join(dir, "catalog.yaml"), []byte(manifest), 0600); err != nil {
				t.Fatal(err)
			}
			if _, err := Load(dir); err != ErrCatalog {
				t.Fatal("应拒绝非法素材且不暴露输入")
			}
		})
	}
}

func TestCatalogDoesNotExposePaths(t *testing.T) {
	c, dir := testCatalog(t)
	for _, item := range c.Search("", 20) {
		if strings.Contains(item.Description, dir) {
			t.Fatal("泄漏本地路径")
		}
	}
}
