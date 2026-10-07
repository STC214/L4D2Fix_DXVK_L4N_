//go:build windows

package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"l4nfix/internal/pathguard"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const configArchiveDirName = "config_backups"

type configArchiveRecord struct {
	ArchivedAt    string `json:"archivedAt"`
	GameRoot      string `json:"gameRoot"`
	Source        string `json:"source"`
	SHA256        string `json:"sha256"`
	Bytes         int    `json:"bytes"`
	SourceModTime string `json:"sourceModTime"`
	Baseline      string `json:"baseline"`
	BaselineHash  string `json:"baselineSHA256"`
	IncomingHash  string `json:"incomingSHA256"`
}

// A differing config without installation history is conservatively retained;
// differences alone do not prove whether the user edited it or changed versions.
// These archives are independent of the installation rollback manifest.
func archiveL4nConfigIfChanged(gameRoot, resRoot, incoming string, man *manifest) (string, error) {
	source := filepath.Join(gameRoot, filepath.FromSlash(configRelativePath))
	if err := pathguard.Within(gameRoot, source); err != nil {
		return "", err
	}
	info, err := os.Lstat(source)
	if os.IsNotExist(err) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	if !info.Mode().IsRegular() {
		return "", fmt.Errorf("config.vdf 不是常规文件: %s", source)
	}
	data, err := os.ReadFile(source)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(data)
	hash := hex.EncodeToString(sum[:])
	incomingHash := ""
	if incoming != "" {
		incomingHash, err = fileHash(incoming)
		if err != nil {
			return "", err
		}
	}
	baseline, baselineHash := "selected-package", incomingHash
	if incoming == "" {
		baseline = "unknown-previous-install"
	}
	if man != nil && strings.EqualFold(clean(man.GameRoot), clean(gameRoot)) {
		for _, e := range man.Files {
			if strings.EqualFold(clean(e.Target), clean(source)) && e.SourceSHA256 != "" {
				baseline, baselineHash = "last-installed-config", e.SourceSHA256
				break
			}
		}
	}
	if strings.EqualFold(hash, baselineHash) {
		return "", nil
	}
	gameID := sha256.Sum256([]byte(strings.ToLower(clean(gameRoot))))
	parent := filepath.Join(resRoot, configArchiveDirName, hex.EncodeToString(gameID[:8]))
	dest := filepath.Join(parent, hash)
	if err := pathguard.Within(resRoot, filepath.Join(dest, "config.vdf")); err != nil {
		return "", err
	}
	archived := filepath.Join(dest, "config.vdf")
	if _, err := os.Stat(dest); err == nil {
		stored, err := fileHash(archived)
		if err != nil || stored != hash {
			return "", fmt.Errorf("已有配置留档校验失败: %s", archived)
		}
		metadata, err := os.ReadFile(filepath.Join(dest, "metadata.json"))
		var record configArchiveRecord
		if err != nil || json.Unmarshal(metadata, &record) != nil || record.SHA256 != hash || !strings.EqualFold(clean(record.GameRoot), clean(gameRoot)) {
			return "", fmt.Errorf("已有配置留档记录无效: %s", dest)
		}
		return archived, nil
	} else if !os.IsNotExist(err) {
		return "", err
	}
	if err := os.MkdirAll(parent, 0755); err != nil {
		return "", err
	}
	tmp, err := os.MkdirTemp(parent, ".pending-")
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(tmp)
	if err := os.WriteFile(filepath.Join(tmp, "config.vdf"), data, 0600); err != nil {
		return "", err
	}
	zone := time.FixedZone("Asia/Shanghai", 8*60*60)
	record := configArchiveRecord{ArchivedAt: time.Now().In(zone).Format(time.RFC3339),
		GameRoot: gameRoot, Source: source, SHA256: hash, Bytes: len(data),
		SourceModTime: info.ModTime().Format(time.RFC3339Nano), Baseline: baseline,
		BaselineHash: baselineHash, IncomingHash: incomingHash}
	metadata, err := json.MarshalIndent(record, "", "  ")
	if err != nil {
		return "", err
	}
	if err := os.WriteFile(filepath.Join(tmp, "metadata.json"), metadata, 0600); err != nil {
		return "", err
	}
	stored, err := fileHash(filepath.Join(tmp, "config.vdf"))
	if err != nil || stored != hash {
		return "", fmt.Errorf("配置留档内容校验失败")
	}
	// Detect edits while taking the snapshot rather than archiving stale bytes.
	current, err := fileHash(source)
	if err != nil || current != hash {
		return "", fmt.Errorf("留档期间 config.vdf 发生变化，请重试")
	}
	if err := os.Rename(tmp, dest); err != nil {
		return "", err
	}
	return archived, nil
}
