package runnerd

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
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
	Images               map[string]string // image_ref -> tar path (host path; also visible in guest)
}

type offlineAssetsManifest struct {
	SchemaVersion     int               `json:"schema_version"`
	VMImage           offlineAssetEntry `json:"vm_image"`
	ContainerdArchive offlineAssetEntry `json:"containerd_archive"`
	Images            []offlineImage    `json:"images"`
}

type offlineAssetEntry struct {
	Arch      string `json:"arch"`
	File      string `json:"file"`
	Digest    string `json:"digest"`
	SourceURL string `json:"source_url"`
}

type offlineImage struct {
	Ref  string `json:"ref"`
	File string `json:"file"`
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
		log.Printf("vm.offline_assets: disabled (read manifest): %v", err)
		return nil, nil
	}

	var m offlineAssetsManifest
	if err := json.Unmarshal(b, &m); err != nil {
		log.Printf("vm.offline_assets: disabled (invalid manifest): %v", err)
		return nil, nil
	}
	if m.SchemaVersion != 1 {
		log.Printf("vm.offline_assets: disabled (unsupported schema_version=%d)", m.SchemaVersion)
		return nil, nil
	}

	vm, err := validateOfflineAsset(m.VMImage, "vm_image")
	if err != nil {
		log.Printf("vm.offline_assets: disabled (vm_image invalid): %v", err)
		return nil, nil
	}
	nerdctl, err := validateOfflineAsset(m.ContainerdArchive, "containerd_archive")
	if err != nil {
		log.Printf("vm.offline_assets: disabled (containerd_archive invalid): %v", err)
		return nil, nil
	}
	vmArch := normalizeOfflineArch(vm.Arch)
	nerdctlArch := normalizeOfflineArch(nerdctl.Arch)
	if vmArch != "aarch64" || nerdctlArch != "aarch64" {
		log.Printf("vm.offline_assets: disabled (arch mismatch: vm_image=%q containerd_archive=%q)", vm.Arch, nerdctl.Arch)
		return nil, nil
	}

	// Put large assets under the default shared tmp dir so they are always visible in the guest,
	// even if the user configured shares without including /Users.
	dstDir := filepath.Join(s.cfg.Paths.DefaultSharedTmpDir, "OfflineAssets")
	if err := os.MkdirAll(dstDir, 0o700); err != nil {
		log.Printf("vm.offline_assets: disabled (mkdir): %v", err)
		return nil, nil
	}

	vmSrc := filepath.Join(srcDir, vm.File)
	vmDst := filepath.Join(dstDir, vm.File)
	if err := copyFileIfNeeded(vmSrc, vmDst); err != nil {
		log.Printf("vm.offline_assets: disabled (copy vm_image): %v", err)
		return nil, nil
	}

	nerdctlSrc := filepath.Join(srcDir, nerdctl.File)
	nerdctlDst := filepath.Join(dstDir, nerdctl.File)
	if err := copyFileIfNeeded(nerdctlSrc, nerdctlDst); err != nil {
		log.Printf("vm.offline_assets: disabled (copy containerd_archive): %v", err)
		return nil, nil
	}

	images := make(map[string]string)
	for _, img := range m.Images {
		img, err := validateOfflineImage(img)
		if err != nil {
			log.Printf("vm.offline_assets: skip image (invalid): %v", err)
			continue
		}
		src := filepath.Join(srcDir, img.File)
		dst := filepath.Join(dstDir, img.File)
		if err := copyFileIfNeeded(src, dst); err != nil {
			log.Printf("vm.offline_assets: skip image %q (copy): %v", img.Ref, err)
			continue
		}
		images[img.Ref] = dst
	}

	return &offlineAssets{
		VMImagePath:          vmDst,
		VMImageDigest:        vm.Digest,
		NerdctlArchivePath:   nerdctlDst,
		NerdctlArchiveDigest: nerdctl.Digest,
		Images:               images,
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
	return e, nil
}

func validateOfflineImage(img offlineImage) (offlineImage, error) {
	img.Ref = normalizeImageRef(img.Ref)
	img.File = strings.TrimSpace(img.File)
	if img.Ref == "" {
		return offlineImage{}, fmt.Errorf("offline image ref is required")
	}
	if img.File == "" {
		return offlineImage{}, fmt.Errorf("offline image file is required")
	}
	if filepath.IsAbs(img.File) {
		return offlineImage{}, fmt.Errorf("offline image file must be relative: %q", img.File)
	}
	clean := filepath.Clean(img.File)
	if clean == "." || clean == string(filepath.Separator) || strings.HasPrefix(clean, ".."+string(filepath.Separator)) || clean == ".." {
		return offlineImage{}, fmt.Errorf("offline image file must not escape assets dir: %q", img.File)
	}
	img.File = clean
	return img, nil
}

func normalizeOfflineArch(arch string) string {
	arch = strings.TrimSpace(strings.ToLower(arch))
	switch arch {
	case "arm64":
		return "aarch64"
	default:
		return arch
	}
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
