//go:build windows

package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"unicode"
	"unicode/utf16"
	"unicode/utf8"
	"unsafe"
)

type launchDocument struct {
	Path   string `json:"path"`
	SHA256 string `json:"sha256"`
}
type launchSelection struct {
	Options   string           `json:"options"`
	Scope     string           `json:"scope"`
	Documents []launchDocument `json:"documents,omitempty"`
}

func isLaunchInstructionFile(name string) bool {
	base := filepath.Base(name)
	return strings.EqualFold(filepath.Ext(base), ".txt") && strings.Contains(base, "启动") && strings.Contains(base, "指令")
}

func decodeLaunchText(data []byte) (string, error) {
	if bytes.HasPrefix(data, []byte{0xff, 0xfe}) || bytes.HasPrefix(data, []byte{0xfe, 0xff}) {
		little := data[0] == 0xff
		data = data[2:]
		if len(data)%2 != 0 {
			return "", fmt.Errorf("UTF-16 文件长度异常")
		}
		units := make([]uint16, len(data)/2)
		for i := range units {
			if little {
				units[i] = binary.LittleEndian.Uint16(data[i*2:])
			} else {
				units[i] = binary.BigEndian.Uint16(data[i*2:])
			}
		}
		text := string(utf16.Decode(units))
		if strings.ContainsRune(text, utf8.RuneError) {
			return "", fmt.Errorf("UTF-16 编码无效")
		}
		return text, nil
	}
	data = bytes.TrimPrefix(data, []byte{0xef, 0xbb, 0xbf})
	if utf8.Valid(data) {
		return string(data), nil
	}
	// Older Chinese Windows TXT files often use GBK/CP936, not UTF-8.
	proc := kernel32.NewProc("MultiByteToWideChar")
	n, _, e := proc.Call(936, 8, uintptr(unsafe.Pointer(&data[0])), uintptr(len(data)), 0, 0)
	if n == 0 {
		return "", fmt.Errorf("TXT 编码无效: %v", e)
	}
	buf := make([]uint16, int(n))
	n, _, e = proc.Call(936, 8, uintptr(unsafe.Pointer(&data[0])), uintptr(len(data)), uintptr(unsafe.Pointer(&buf[0])), uintptr(len(buf)))
	if n == 0 {
		return "", fmt.Errorf("TXT 解码失败: %v", e)
	}
	return string(utf16.Decode(buf[:n])), nil
}

// Preserve quoted values, collapse whitespace outside quotes and discard comments.
// These are Steam/game arguments; nothing from the document is executed as a shell.
func normalizeLaunchLine(line string) (string, error) {
	var out strings.Builder
	quoted := false
	space := false
	for i := 0; i < len(line); {
		c := line[i]
		if !quoted && (c == '#' || (c == '/' && i+1 < len(line) && line[i+1] == '/')) && (i == 0 || line[i-1] == ' ' || line[i-1] == '\t') {
			break
		}
		if c == '\\' && quoted && i+1 < len(line) && (line[i+1] == '"' || line[i+1] == '\\') {
			out.WriteByte(c)
			out.WriteByte(line[i+1])
			i += 2
			continue
		}
		if c == '"' {
			if space && out.Len() > 0 {
				out.WriteByte(' ')
			}
			space = false
			quoted = !quoted
			out.WriteByte(c)
			i++
			continue
		}
		r, size := utf8.DecodeRuneInString(line[i:])
		if unicode.IsControl(r) && r != '\t' {
			return "", fmt.Errorf("启动指令含控制字符")
		}
		if !quoted && unicode.IsSpace(r) {
			space = true
			i += size
			continue
		}
		if space && out.Len() > 0 {
			out.WriteByte(' ')
		}
		space = false
		out.WriteRune(r)
		i += size
	}
	if quoted {
		return "", fmt.Errorf("启动指令引号未闭合")
	}
	return strings.TrimSpace(out.String()), nil
}

func extractLaunchOptions(text string) (string, error) {
	text = strings.ReplaceAll(strings.ReplaceAll(text, "\r\n", "\n"), "\r", "\n")
	start := regexp.MustCompile(`^[+-][A-Za-z_][A-Za-z0-9_-]*(?:\s|$)`)
	var blocks []string
	var lines []string
	flush := func() {
		if len(lines) > 0 {
			blocks = append(blocks, strings.Join(lines, " "))
			lines = nil
		}
	}
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		for _, label := range []string{"启动项指令", "启动指令", "启动参数", "启动项", "指令"} {
			for _, colon := range []string{"：", ":"} {
				prefix := label + colon
				if strings.HasPrefix(line, prefix) {
					line = strings.TrimSpace(strings.TrimPrefix(line, prefix))
				}
			}
		}
		line = strings.Trim(line, "`")
		if !start.MatchString(line) {
			flush()
			continue
		}
		value, e := normalizeLaunchLine(line)
		if e != nil {
			return "", e
		}
		if value != "" {
			lines = append(lines, value)
		}
	}
	flush()
	if len(blocks) == 0 {
		return "", fmt.Errorf("TXT 中没有找到以 -参数 或 +命令 开头的启动指令")
	}
	for _, block := range blocks[1:] {
		if block != blocks[0] {
			return "", fmt.Errorf("TXT 中存在多组不同启动指令，请只保留一组")
		}
	}
	return blocks[0], nil
}

func readLaunchDocuments(root string, recursive bool) (*launchSelection, error) {
	var paths []string
	if recursive {
		files, e := packageInventory(root)
		if e != nil {
			return nil, e
		}
		for _, p := range files {
			if isLaunchInstructionFile(p) {
				paths = append(paths, filepath.Join(root, p))
			}
		}
	} else {
		entries, e := os.ReadDir(root)
		if os.IsNotExist(e) {
			return nil, nil
		}
		if e != nil {
			return nil, e
		}
		for _, entry := range entries {
			if !entry.IsDir() && isLaunchInstructionFile(entry.Name()) {
				paths = append(paths, filepath.Join(root, entry.Name()))
			}
		}
	}
	if len(paths) == 0 {
		return nil, nil
	}
	selection := &launchSelection{}
	for _, path := range paths {
		st, e := os.Lstat(path)
		if e != nil {
			return nil, e
		}
		if !st.Mode().IsRegular() {
			return nil, fmt.Errorf("启动指令文件必须是普通文件: %s", path)
		}
		f, e := os.Open(path)
		if e != nil {
			return nil, e
		}
		data, e := io.ReadAll(io.LimitReader(f, (1<<20)+1))
		closeErr := f.Close()
		if e != nil {
			return nil, e
		}
		if closeErr != nil {
			return nil, closeErr
		}
		if len(data) > 1<<20 {
			return nil, fmt.Errorf("启动指令 TXT 超过 1 MiB: %s", path)
		}
		text, e := decodeLaunchText(data)
		if e != nil {
			return nil, fmt.Errorf("%s: %w", path, e)
		}
		options, e := extractLaunchOptions(text)
		if e != nil {
			return nil, fmt.Errorf("%s: %w", path, e)
		}
		if selection.Options != "" && selection.Options != options {
			return nil, fmt.Errorf("启动指令文件冲突: %s", path)
		}
		selection.Options = options
		digest := sha256.Sum256(data)
		selection.Documents = append(selection.Documents, launchDocument{Path: path, SHA256: hex.EncodeToString(digest[:])})
	}
	return selection, nil
}

func resolveL4nLaunchOptions(source packageSource, resRoot string) (launchSelection, error) {
	if source.LaunchInstructions != nil {
		result := *source.LaunchInstructions
		result.Scope = "selected-l4n"
		return result, nil
	}
	for _, item := range []struct{ dir, scope string }{{filepath.Join(resRoot, l4nVersionsDirName), "l4n-shared"}, {resRoot, "resources-root"}} {
		result, e := readLaunchDocuments(item.dir, false)
		if e != nil {
			return launchSelection{}, e
		}
		if result != nil {
			result.Scope = item.scope
			return *result, nil
		}
	}
	return launchSelection{Options: defaultLaunchOptions, Scope: "built-in-default"}, nil
}
