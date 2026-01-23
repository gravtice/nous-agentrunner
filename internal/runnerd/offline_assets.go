package runnerd

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

type offlineAssets struct {
	VMImagePath          string
	VMImageDigest        string
	NerdctlArchivePath   string
	NerdctlArchiveDigest string
}

type offlineAssetsManifest struct {
	SchemaVersion     int               `json:"schema_version"`
	VMImage           offlineAssetEntry `json:"vm_image"`
	ContainerdArchive offlineAssetEntry `json:"containerd_archive"`
}

type offlineAssetEntry struct {
	Arch      string `json:"arch"`
	File      string `json:"file"`
	Digest    string `json:"digest"`
	SourceURL string `json:"source_url"`
}

func (s *Server) prepareOfflineAssets() (*offlineAssets, error) {
	if runtime.GOARCH != "arm64" {
		return nil, nil
	}

	srcDir := findBundledDir("nous-offline-assets")
	if srcDir == "" {
		return nil, nil
	}

	manifestPath := filepath.Join(srcDir, "manifest.json")
	b, err := os.ReadFile(manifestPath)
	if err != nil {
		return nil, fmt.Errorf("offline assets manifest not readable: %w", err)
	}

	var m offlineAssetsManifest
	if err := json.Unmarshal(b, &m); err != nil {
		return nil, fmt.Errorf("offline assets manifest invalid: %w", err)
	}
	if m.SchemaVersion != 1 {
		return nil, fmt.Errorf("offline assets schema_version %d not supported", m.SchemaVersion)
	}

	vm, err := validateOfflineAsset(m.VMImage, "vm_image")
	if err != nil {
		return nil, err
	}
	nerdctl, err := validateOfflineAsset(m.ContainerdArchive, "containerd_archive")
	if err != nil {
		return nil, err
	}
	if vm.Arch != "aarch64" || nerdctl.Arch != "aarch64" {
		return nil, fmt.Errorf("offline assets arch mismatch: vm_image=%q containerd_archive=%q", vm.Arch, nerdctl.Arch)
	}

	dstDir := filepath.Join(s.cfg.Paths.CachesDir, "OfflineAssets")
	if err := os.MkdirAll(dstDir, 0o700); err != nil {
		return nil, err
	}

	vmSrc := filepath.Join(srcDir, vm.File)
	vmDst := filepath.Join(dstDir, vm.File)
	if err := copyFileIfNeeded(vmSrc, vmDst); err != nil {
		return nil, fmt.Errorf("offline assets copy vm_image: %w", err)
	}

	nerdctlSrc := filepath.Join(srcDir, nerdctl.File)
	nerdctlDst := filepath.Join(dstDir, nerdctl.File)
	if err := copyFileIfNeeded(nerdctlSrc, nerdctlDst); err != nil {
		return nil, fmt.Errorf("offline assets copy containerd_archive: %w", err)
	}

	return &offlineAssets{
		VMImagePath:          vmDst,
		VMImageDigest:        vm.Digest,
		NerdctlArchivePath:   nerdctlDst,
		NerdctlArchiveDigest: nerdctl.Digest,
	}, nil
}

func validateOfflineAsset(e offlineAssetEntry, name string) (offlineAssetEntry, error) {
	e.Arch = strings.TrimSpace(e.Arch)
	e.File = strings.TrimSpace(e.File)
	e.Digest = strings.TrimSpace(e.Digest)
	e.SourceURL = strings.TrimSpace(e.SourceURL)

	if e.Arch == "" {
		return offlineAssetEntry{}, fmt.Errorf("offline assets %s.arch is required", name)
	}
	if e.File == "" {
		return offlineAssetEntry{}, fmt.Errorf("offline assets %s.file is required", name)
	}
	if filepath.Base(e.File) != e.File {
		return offlineAssetEntry{}, fmt.Errorf("offline assets %s.file must be a base filename", name)
	}
	if e.Digest == "" {
		return offlineAssetEntry{}, fmt.Errorf("offline assets %s.digest is required", name)
	}
	return e, nil
}

func copyFileIfNeeded(src, dst string) error {
	srcFi, err := os.Stat(src)
	if err != nil {
		return err
	}
	if !srcFi.Mode().IsRegular() {
		return fmt.Errorf("not a regular file: %s", src)
	}

	if dstFi, err := os.Stat(dst); err == nil {
		if dstFi.Mode().IsRegular() && dstFi.Size() == srcFi.Size() {
			return nil
		}
	}

	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()

	if err := os.MkdirAll(filepath.Dir(dst), 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(dst), filepath.Base(dst)+".tmp-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer func() { _ = os.Remove(tmpName) }()

	if _, err := io.Copy(tmp, in); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Chmod(0o644); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}

	_ = os.Remove(dst)
	return os.Rename(tmpName, dst)
}
