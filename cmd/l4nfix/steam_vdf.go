//go:build windows

package main

import (
	"fmt"
	"strconv"
	"strings"
)

type vdfToken struct {
	value      string
	start, end int
	brace      bool
}
type vdfNode struct {
	key, value vdfToken
	children   []vdfNode
	close      int
	block      bool
}

// Keep byte offsets so edits preserve every unrelated field and comment.
func parseVDF(text string) ([]vdfNode, error) {
	var tokens []vdfToken
	for i := 0; i < len(text); {
		if strings.ContainsRune(" \t\r\n", rune(text[i])) {
			i++
			continue
		}
		if i+1 < len(text) && text[i:i+2] == "//" {
			for i < len(text) && text[i] != '\n' {
				i++
			}
			continue
		}
		if i == 0 && strings.HasPrefix(text, "\xef\xbb\xbf") {
			i += 3
			continue
		}
		start := i
		if text[i] == '{' || text[i] == '}' {
			tokens = append(tokens, vdfToken{string(text[i]), i, i + 1, true})
			i++
			continue
		}
		if text[i] == '"' {
			i++
			var b strings.Builder
			closed := false
			for i < len(text) {
				if text[i] == '"' {
					i++
					closed = true
					break
				}
				if text[i] == '\\' && i+1 < len(text) && (text[i+1] == '"' || text[i+1] == '\\') {
					i++
				}
				b.WriteByte(text[i])
				i++
			}
			if !closed {
				return nil, fmt.Errorf("VDF unterminated string at %d", start)
			}
			tokens = append(tokens, vdfToken{b.String(), start, i, false})
			continue
		}
		for i < len(text) && !strings.ContainsRune(" \t\r\n{}\"", rune(text[i])) {
			i++
		}
		if i == start {
			return nil, fmt.Errorf("VDF invalid token at %d", i)
		}
		tokens = append(tokens, vdfToken{text[start:i], start, i, false})
	}
	pos := 0
	var parse func(int, bool) ([]vdfNode, int, error)
	parse = func(depth int, nested bool) ([]vdfNode, int, error) {
		if depth > 128 {
			return nil, 0, fmt.Errorf("VDF nesting exceeds 128")
		}
		var nodes []vdfNode
		for pos < len(tokens) {
			if tokens[pos].value == "}" && tokens[pos].brace {
				if !nested {
					return nil, 0, fmt.Errorf("VDF unexpected closing brace")
				}
				close := tokens[pos].start
				pos++
				return nodes, close, nil
			}
			key := tokens[pos]
			pos++
			if key.brace || pos == len(tokens) {
				return nil, 0, fmt.Errorf("VDF missing value for %q", key.value)
			}
			value := tokens[pos]
			pos++
			node := vdfNode{key: key, value: value}
			if value.brace {
				if value.value != "{" {
					return nil, 0, fmt.Errorf("VDF missing value for %q", key.value)
				}
				node.block = true
				var err error
				node.children, node.close, err = parse(depth+1, true)
				if err != nil {
					return nil, 0, err
				}
			}
			nodes = append(nodes, node)
		}
		if nested {
			return nil, 0, fmt.Errorf("VDF unclosed block")
		}
		return nodes, len(text), nil
	}
	nodes, _, err := parse(0, false)
	return nodes, err
}

func setAppLaunchOptionsInText(text, options string) (string, error) {
	nodes, err := parseVDF(text)
	if err != nil {
		return "", err
	}
	var apps []vdfNode
	var visit func([]vdfNode)
	visit = func(ns []vdfNode) {
		for _, n := range ns {
			if n.block {
				if strings.EqualFold(n.key.value, "apps") {
					apps = append(apps, n)
				} else {
					visit(n.children)
				}
			}
		}
	}
	visit(nodes)
	if len(apps) != 1 {
		return "", fmt.Errorf("localconfig.vdf must contain exactly one apps block (found %d)", len(apps))
	}
	escaped := strconv.Quote(options)
	var games []vdfNode
	for _, n := range apps[0].children {
		if n.key.value == "550" {
			games = append(games, n)
		}
	}
	if len(games) > 1 {
		return "", fmt.Errorf("duplicate AppID 550")
	}
	if len(games) == 0 {
		insert := "\r\n\t\t\t\t\t\"550\"\r\n\t\t\t\t\t{\r\n\t\t\t\t\t\t\"LaunchOptions\"\t" + escaped + "\r\n\t\t\t\t\t}\r\n"
		at := apps[0].close
		return text[:at] + insert + text[at:], nil
	}
	game := games[0]
	if !game.block {
		return "", fmt.Errorf("AppID 550 is not a block")
	}
	var fields []vdfNode
	for _, n := range game.children {
		if strings.EqualFold(n.key.value, "LaunchOptions") {
			fields = append(fields, n)
		}
	}
	if len(fields) > 1 {
		return "", fmt.Errorf("duplicate LaunchOptions")
	}
	if len(fields) == 1 {
		f := fields[0]
		if f.block {
			return "", fmt.Errorf("LaunchOptions is not a string")
		}
		return text[:f.value.start] + escaped + text[f.value.end:], nil
	}
	return text[:game.close] + "\r\n\t\t\t\t\t\t\"LaunchOptions\"\t" + escaped + "\r\n" + text[game.close:], nil
}
