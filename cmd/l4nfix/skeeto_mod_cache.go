//go:build windows

package main

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"hash/crc32"
	"io"
	"l4nfix/internal/pathguard"
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

const skeetoModCacheDir = "skeeto_mods"
const skeetoAddonListRel = "left4dead2/addonlist.txt"

var skeetoModIDs = []string{"3782512354", "3787239937"}

type skeetoModResource struct {
	Detail  workshopDetail `json:"detail"`
	Archive string         `json:"archive"`
	SHA256  string         `json:"sha256"`
}
type skeetoModIndex struct {
	SkeetoContent string              `json:"skeetoContent"`
	Mods          []skeetoModResource `json:"mods"`
}

func skeetoModRelative(id string) string { return "left4dead2/addons/workshop/" + id + ".vpk" }
func isSkeetoModID(id string) bool {
	for _, known := range skeetoModIDs {
		if id == known {
			return true
		}
	}
	return false
}
func isSkeetoModRelative(rel string) bool {
	rel = strings.ToLower(filepath.ToSlash(rel))
	for _, id := range skeetoModIDs {
		if rel == skeetoModRelative(id) {
			return true
		}
	}
	return false
}

// A Workshop .vpk can be a real VPK asset or Skeeto's ZIP update payload.
// Preserve its original bytes; never unpack or rename it inside the game.
func validateSkeetoModFile(id, path string, size int64) error {
	if !isSkeetoModID(id) {
		return fmt.Errorf("unexpected workshop mod ID")
	}
	f, e := os.Open(path)
	if e != nil {
		return e
	}
	defer f.Close()
	st, e := f.Stat()
	if e != nil {
		return e
	}
	if !st.Mode().IsRegular() || st.Size() != size || size <= 0 || size > maxPackageBytes {
		return fmt.Errorf("MOD file size/type mismatch")
	}
	var header [28]byte
	n, e := f.Read(header[:])
	if e != nil && e != io.EOF {
		return e
	}
	if id == "3787239937" && n >= 4 && string(header[:4]) == "PK\x03\x04" {
		z, e := zip.OpenReader(path)
		if e != nil {
			return e
		}
		defer z.Close()
		if e = validateZIPHeaders(z); e != nil {
			return e
		}
		for _, entry := range z.File {
			if entry.FileInfo().IsDir() {
				continue
			}
			r, e := entry.Open()
			if e != nil {
				return e
			}
			n, e := io.Copy(io.Discard, io.LimitReader(r, int64(entry.UncompressedSize64)+1))
			r.Close()
			if e != nil {
				return e
			}
			if n != int64(entry.UncompressedSize64) {
				return fmt.Errorf("MOD ZIP member length mismatch")
			}
		}
		return nil
	}
	if n < 12 || binary.LittleEndian.Uint32(header[:4]) != 0x55aa1234 {
		return fmt.Errorf("MOD is not a VPK payload")
	}
	version := binary.LittleEndian.Uint32(header[4:8])
	headerSize := int64(12)
	if version == 2 {
		headerSize = 28
	} else if version != 1 {
		return fmt.Errorf("unsupported VPK version")
	}
	tree := int64(binary.LittleEndian.Uint32(header[8:12]))
	if size < headerSize || tree <= 0 || tree > size-headerSize {
		return fmt.Errorf("invalid VPK directory size")
	}
	if tree > 64<<20 {
		return fmt.Errorf("VPK tree exceeds limit")
	}
	dataEnd := size
	if version == 2 {
		dataEnd = headerSize + tree + int64(binary.LittleEndian.Uint32(header[12:16]))
		footer := int64(binary.LittleEndian.Uint32(header[16:20])) + int64(binary.LittleEndian.Uint32(header[20:24])) + int64(binary.LittleEndian.Uint32(header[24:28]))
		if dataEnd+footer != size {
			return fmt.Errorf("invalid VPK v2 section lengths")
		}
	}
	treeData := make([]byte, int(tree))
	if _, e := f.ReadAt(treeData, headerSize); e != nil {
		return e
	}
	pos, entries := 0, 0
	var total int64
	readString := func() (string, error) {
		if pos >= len(treeData) {
			return "", io.ErrUnexpectedEOF
		}
		i := bytes.IndexByte(treeData[pos:], 0)
		if i < 0 || i > 4096 {
			return "", fmt.Errorf("invalid VPK name")
		}
		s := string(treeData[pos : pos+i])
		pos += i + 1
		return s, nil
	}
	for {
		ext, e := readString()
		if e != nil {
			return e
		}
		if ext == "" {
			break
		}
		for {
			dir, e := readString()
			if e != nil {
				return e
			}
			if dir == "" {
				break
			}
			for {
				name, e := readString()
				if e != nil {
					return e
				}
				if name == "" {
					break
				}
				entries++
				if entries > maxPackageFiles || pos+18 > len(treeData) {
					return fmt.Errorf("invalid VPK entry count/length")
				}
				entry := treeData[pos : pos+18]
				pos += 18
				crc := binary.LittleEndian.Uint32(entry[:4])
				preload := int(binary.LittleEndian.Uint16(entry[4:6]))
				archive := binary.LittleEndian.Uint16(entry[6:8])
				offset := int64(binary.LittleEndian.Uint32(entry[8:12]))
				length := int64(binary.LittleEndian.Uint32(entry[12:16]))
				if binary.LittleEndian.Uint16(entry[16:18]) != 0xffff || pos+preload > len(treeData) {
					return fmt.Errorf("invalid VPK entry/preload")
				}
				if length > 0 && (archive != 0x7fff || headerSize+tree+offset+length > dataEnd) {
					return fmt.Errorf("VPK needs missing external chunks or has invalid offsets")
				}
				total += int64(preload) + length
				if total > maxPackageBytes {
					return fmt.Errorf("VPK payload exceeds limit")
				}
				h := crc32.NewIEEE()
				h.Write(treeData[pos : pos+preload])
				pos += preload
				if length > 0 {
					n, e := io.Copy(h, io.NewSectionReader(f, headerSize+tree+offset, length))
					if e != nil {
						return e
					}
					if n != length {
						return io.ErrUnexpectedEOF
					}
				}
				if h.Sum32() != crc {
					return fmt.Errorf("VPK member CRC mismatch")
				}
			}
		}
	}
	if entries == 0 {
		return fmt.Errorf("VPK contains no entries")
	}
	for _, b := range treeData[pos:] {
		if b != 0 {
			return fmt.Errorf("unparsed VPK tree data")
		}
	}
	return nil
}

func readSkeetoModIndex(res string) (skeetoModIndex, error) {
	var idx skeetoModIndex
	p := filepath.Join(res, "skeeto-mods.json")
	if e := pathguard.Within(res, p); e != nil {
		return idx, e
	}
	f, e := os.Open(p)
	if e != nil {
		return idx, e
	}
	defer f.Close()
	b, e := io.ReadAll(io.LimitReader(f, (1<<20)+1))
	if e != nil {
		return idx, e
	}
	if len(b) > 1<<20 {
		return idx, fmt.Errorf("MOD cache index too large")
	}
	e = json.Unmarshal(b, &idx)
	return idx, e
}

func verifiedSkeetoMod(res string, r skeetoModResource) (string, error) {
	if !isSkeetoModID(r.Detail.ID) {
		return "", fmt.Errorf("invalid cached MOD ID")
	}
	if _, e := validateModDetail(r.Detail); e != nil {
		return "", e
	}
	expected := filepath.Join(skeetoModCacheDir, r.Detail.ID+"-"+r.Detail.Content+".vpk")
	if !strings.EqualFold(filepath.Clean(r.Archive), expected) {
		return "", fmt.Errorf("invalid MOD cache path")
	}
	p := filepath.Join(res, expected)
	if e := pathguard.Within(res, p); e != nil {
		return "", e
	}
	if e := validateSkeetoModFile(r.Detail.ID, p, int64(r.Detail.Size)); e != nil {
		return "", e
	}
	h, e := fileHash(p)
	if e != nil || h != r.SHA256 {
		return "", fmt.Errorf("MOD cache checksum mismatch")
	}
	return p, nil
}

func validateModDetail(d workshopDetail) (workshopPlatform, error) {
	p := workshopPlatform{Kind: "skeeto-mod", Name: "Skeeto 工坊 MOD", ID: d.ID}
	if !isSkeetoModID(d.ID) {
		return p, fmt.Errorf("unexpected MOD ID")
	}
	return p, validateWorkshopDetail(p, d)
}

func cacheSkeetoMod(ctx context.Context, c *http.Client, res string, d workshopDetail, existing []skeetoModResource, source string) (skeetoModResource, error) {
	r := skeetoModResource{Detail: d, Archive: filepath.Join(skeetoModCacheDir, d.ID+"-"+d.Content+".vpk")}
	if e := ctx.Err(); e != nil {
		return r, e
	}
	if _, e := validateModDetail(d); e != nil {
		return r, e
	}
	for _, old := range existing {
		if old.Detail.ID == d.ID && old.Detail.Content == d.Content && old.Detail.Updated == d.Updated && old.Detail.Size == d.Size {
			if _, e := verifiedSkeetoMod(res, old); e == nil {
				old.Detail = d
				return old, nil
			}
		}
	}
	target := filepath.Join(res, r.Archive)
	if e := pathguard.Within(res, target); e != nil {
		return r, e
	}
	if e := os.MkdirAll(filepath.Dir(target), 0755); e != nil {
		return r, e
	}
	f, e := os.CreateTemp(filepath.Dir(target), ".pending-mod-*.vpk")
	if e != nil {
		return r, e
	}
	tmp := f.Name()
	defer os.Remove(tmp)
	var reader io.ReadCloser
	if source != "" {
		reader, e = os.Open(source)
	} else {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, d.URL, nil)
		if err != nil {
			f.Close()
			return r, err
		}
		resp, err := c.Do(req)
		if err != nil {
			f.Close()
			return r, err
		}
		if resp.StatusCode != http.StatusOK {
			resp.Body.Close()
			f.Close()
			return r, fmt.Errorf("MOD download HTTP %d", resp.StatusCode)
		}
		reader = resp.Body
	}
	if e != nil {
		f.Close()
		return r, e
	}
	n, copyErr := io.Copy(f, io.LimitReader(reader, int64(d.Size)+1))
	reader.Close()
	syncErr := f.Sync()
	closeErr := f.Close()
	if copyErr != nil {
		return r, copyErr
	}
	if syncErr != nil {
		return r, syncErr
	}
	if closeErr != nil {
		return r, closeErr
	}
	if n != int64(d.Size) {
		return r, fmt.Errorf("MOD download length mismatch")
	}
	if e = validateSkeetoModFile(d.ID, tmp, int64(d.Size)); e != nil {
		return r, e
	}
	r.SHA256, e = fileHash(tmp)
	if e != nil {
		return r, e
	}
	if source != "" {
		h, e := fileHash(source)
		if e != nil || h != r.SHA256 {
			return r, fmt.Errorf("Skeeto resource changed during cache copy")
		}
	}
	if e := ctx.Err(); e != nil {
		return r, e
	}
	if e = renameFileRetry(tmp, target); e != nil {
		return r, e
	}
	appendLog("[update] MOD " + d.ID + " 原始 VPK 已缓存")
	return r, nil
}

func prepareSkeetoModCache(ctx context.Context, c *http.Client, endpoint, res string, skeeto workshopResource) error {
	if skeeto.Platform != "skeeto" || skeeto.Detail.ID != "3787239937" {
		return fmt.Errorf("invalid Skeeto platform identity")
	}
	source, e := verifiedWorkshopArchive(res, skeeto)
	if e != nil {
		return e
	}
	asset := workshopPlatform{Kind: "skeeto-mod", Name: "Skeeto 预设素材包", ID: "3782512354"}
	details, e := fetchWorkshopDetailsFor(ctx, c, endpoint, []workshopPlatform{asset})
	if e != nil {
		return e
	}
	if len(details) != 1 || details[0].ID != asset.ID {
		return fmt.Errorf("素材包元数据缺失或重复")
	}
	old, _ := readSkeetoModIndex(res)
	idx := skeetoModIndex{SkeetoContent: skeeto.Detail.Content}
	for _, d := range []workshopDetail{details[0], skeeto.Detail} {
		src := ""
		if d.ID == skeeto.Detail.ID {
			src = source
		}
		r, e := cacheSkeetoMod(ctx, c, res, d, old.Mods, src)
		if e != nil {
			return e
		}
		if src != "" && r.SHA256 != skeeto.SHA256 {
			return fmt.Errorf("Skeeto original VPK differs from checked platform ZIP")
		}
		idx.Mods = append(idx.Mods, r)
	}
	if e := ctx.Err(); e != nil {
		return e
	}
	var signatures []cleanupSignature
	for _, r := range idx.Mods {
		signatures = append(signatures, cleanupSignature{"skeeto", skeetoModRelative(r.Detail.ID), r.SHA256})
	}
	if e = rememberCleanupRows(res, signatures); e != nil {
		return e
	}
	b, e := json.MarshalIndent(idx, "", "  ")
	if e != nil {
		return e
	}
	if e := ctx.Err(); e != nil {
		return e
	}
	if e := writeFileAtomic(filepath.Join(res, "skeeto-mods.json"), b, 0644); e != nil {
		return e
	}
	appendLog("[update] Skeeto 两个工坊 MOD 已就绪，部署时从本地缓存复制")
	return nil
}

func preparedSkeetoMods(res, content string) (skeetoModIndex, error) {
	idx, e := readSkeetoModIndex(res)
	if e != nil {
		return idx, fmt.Errorf("请重新检查更新以准备 Skeeto 的两个工坊 MOD: %w", e)
	}
	if idx.SkeetoContent != content || len(idx.Mods) != 2 {
		return idx, fmt.Errorf("MOD 缓存与所选 Skeeto 不一致，请重新检查更新")
	}
	seen := map[string]bool{}
	for _, r := range idx.Mods {
		if seen[r.Detail.ID] {
			return idx, fmt.Errorf("MOD cache duplicate ID")
		}
		seen[r.Detail.ID] = true
		if _, e := verifiedSkeetoMod(res, r); e != nil {
			return idx, e
		}
	}
	for _, r := range idx.Mods {
		if r.Detail.ID == "3787239937" && r.Detail.Content != content {
			return idx, fmt.Errorf("Skeeto MOD content ID mismatch")
		}
	}
	for _, id := range skeetoModIDs {
		if !seen[id] {
			return idx, fmt.Errorf("MOD cache missing item")
		}
	}
	return idx, nil
}
