package sandbox

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"

	"code-agent/internal/safety"
)

const PytzName = "pytz-2026.5-py2.py3-none-any.whl"
const PytzSource = "https://files.pythonhosted.org/packages/4f/ef/c66110d46fb800dda0bf33164182dfadabe26a90e4476844d502a23dca8e/" + PytzName
const PytzSHA256 = "e658af3757f9e26a9d25dd2aff38335acd92bc9104f890a894b2c1ba28311b03"
const PytzSize = 506342

// PythonUnittestScript installs the approved wheel only in sandbox scratch.
const PythonUnittestScript = "import os,subprocess,sys;subprocess.run([sys.executable,'-m','pip','install','--no-index','--no-deps','--disable-pip-version-check','--target','/run/codeops/python-site','/deps/" + PytzName + "'],check=True);os.execv(sys.executable,[sys.executable,'-m','unittest','discover','-v'])"

// PreparePytz has no URL/package selector and never installs on the host.
func PreparePytz(ctx context.Context, storage, operation string, client *http.Client) error {
	if !openSandboxHex(operation, 64) {
		return errors.New("invalid dependency operation")
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	transport := http.DefaultTransport
	if client != nil && client.Transport != nil {
		transport = client.Transport
	}
	bounded := &http.Client{Transport: transport, Timeout: 30 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, PytzSource, nil)
	if err != nil {
		return err
	}
	response, err := bounded.Do(request)
	if err != nil {
		return errors.New("approved dependency transfer failed")
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK || response.ContentLength != PytzSize {
		return errors.New("approved dependency response differs from approval")
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, PytzSize+1))
	if err != nil || len(data) != PytzSize {
		return errors.New("approved dependency transfer is incomplete or oversized")
	}
	if err := validatePytz(data); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	storage, err = filepath.Abs(storage)
	if err != nil {
		return err
	}
	release, err := safety.PinDirectories(storage)
	if err != nil {
		return err
	}
	defer release()
	root, err := os.OpenRoot(storage)
	if err != nil {
		return err
	}
	defer root.Close()
	directory := "dependency-" + operation
	if err := root.Mkdir(directory, 0700); err != nil {
		return errors.New("dependency artifact already exists or storage is unavailable")
	}
	if err := safety.ProtectPrivatePath(filepath.Join(storage, directory)); err != nil {
		return err
	}
	file, err := root.OpenFile(directory+"/"+PytzName, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0444)
	if err != nil {
		return err
	}
	_, writeErr := file.Write(data)
	syncErr := file.Sync()
	closeErr := file.Close()
	return errors.Join(writeErr, syncErr, closeErr)
}

func ValidatePytz(ctx context.Context, storage, operation string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if !openSandboxHex(operation, 64) {
		return errors.New("invalid dependency operation")
	}
	root, err := os.OpenRoot(storage)
	if err != nil {
		return err
	}
	defer root.Close()
	directory, err := root.OpenRoot("dependency-" + operation)
	if err != nil {
		return err
	}
	defer directory.Close()
	file, err := directory.Open(PytzName)
	if err != nil {
		return err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Size() != PytzSize {
		return errors.New("prepared dependency identity differs from approval")
	}
	data, err := io.ReadAll(io.LimitReader(file, PytzSize+1))
	if err != nil {
		return err
	}
	return validatePytz(data)
}

func validatePytz(data []byte) error {
	digest := sha256.Sum256(data)
	if len(data) != PytzSize || hex.EncodeToString(digest[:]) != PytzSHA256 {
		return errors.New("approved dependency hash or size differs from approval")
	}
	archive, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil || len(archive.File) > 2048 {
		return errors.New("approved dependency archive is invalid")
	}
	metadata, wheel, license := "", "", ""
	var total uint64
	for _, file := range archive.File {
		name := strings.TrimSuffix(file.Name, "/")
		if path.Clean(name) != name || strings.ContainsAny(name, "\\\x00:") || !(strings.HasPrefix(name, "pytz/") || strings.HasPrefix(name, "pytz-2026.5.dist-info/")) || file.Mode()&os.ModeSymlink != 0 || file.UncompressedSize64 > 8<<20 || total > 8<<20-file.UncompressedSize64 {
			return errors.New("approved dependency archive exceeds its contract")
		}
		total += file.UncompressedSize64
		reader, err := file.Open()
		if err != nil {
			return err
		}
		contents, readErr := io.ReadAll(io.LimitReader(reader, 8<<20+1))
		closeErr := reader.Close()
		if readErr != nil || closeErr != nil || uint64(len(contents)) != file.UncompressedSize64 {
			return errors.New("approved dependency archive is incomplete")
		}
		switch name {
		case "pytz-2026.5.dist-info/METADATA":
			metadata = string(contents)
		case "pytz-2026.5.dist-info/WHEEL":
			wheel = string(contents)
		default:
			if strings.HasPrefix(name, "pytz-2026.5.dist-info/") && strings.HasPrefix(strings.ToUpper(path.Base(name)), "LICENSE") {
				license = string(contents)
			}
		}
	}
	metadata = strings.ReplaceAll(metadata, "\r\n", "\n")
	if !strings.Contains(metadata, "\nName: pytz\n") || !strings.Contains(metadata, "\nVersion: 2026.5\n") || !(strings.Contains(metadata, "\nLicense: MIT\n") || strings.Contains(metadata, "\nLicense-Expression: MIT\n")) || strings.Contains(metadata, "\nRequires-Dist:") || !strings.Contains(wheel, "Root-Is-Purelib: true") || !strings.Contains(wheel, "Tag: py3-none-any") || !strings.Contains(license, "Permission is hereby granted, free of charge") || !strings.Contains(license, `THE SOFTWARE IS PROVIDED "AS IS"`) {
		return errors.New("approved dependency package or license identity differs from approval")
	}
	return nil
}
