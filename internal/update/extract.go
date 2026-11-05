package update

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"strings"
)

const maxBinary = 200 << 20

// ExtractBinary copies the executable called name out of the archive (zip when
// the file name ends in .zip, tar.gz otherwise) into a new file in dir and
// returns its path. Only an entry at the top level with exactly that name is
// taken; every other entry, whatever its path, is ignored, so a crafted archive
// cannot write anywhere else.
func ExtractBinary(archivePath, archiveName, name, dir string) (string, error) {
	if strings.HasSuffix(archiveName, ".zip") {
		return extractZip(archivePath, name, dir)
	}
	return extractTarGz(archivePath, name, dir)
}

func topLevel(entry, name string) bool {
	e := strings.TrimPrefix(strings.ReplaceAll(entry, `\`, "/"), "./")
	return e == name && path.Base(e) == e
}

func writeTemp(dir string, r io.Reader) (string, error) {
	out, err := os.CreateTemp(dir, "envmgr-new-*")
	if err != nil {
		return "", err
	}
	n, err := io.Copy(out, io.LimitReader(r, maxBinary+1))
	if cerr := out.Close(); err == nil {
		err = cerr
	}
	if err == nil && n > maxBinary {
		err = errors.New("binary in the archive is too large")
	}
	if err != nil {
		os.Remove(out.Name())
		return "", err
	}
	return out.Name(), nil
}

func extractZip(archivePath, name, dir string) (string, error) {
	zr, err := zip.OpenReader(archivePath)
	if err != nil {
		return "", fmt.Errorf("reading zip: %w", err)
	}
	defer zr.Close()
	for _, f := range zr.File {
		if f.FileInfo().IsDir() || !topLevel(f.Name, name) {
			continue
		}
		rc, err := f.Open()
		if err != nil {
			return "", err
		}
		defer rc.Close()
		return writeTemp(dir, rc)
	}
	return "", fmt.Errorf("%s not found at the top level of the archive", name)
}

func extractTarGz(archivePath, name, dir string) (string, error) {
	f, err := os.Open(archivePath)
	if err != nil {
		return "", err
	}
	defer f.Close()
	gz, err := gzip.NewReader(f)
	if err != nil {
		return "", fmt.Errorf("reading gzip: %w", err)
	}
	defer gz.Close()
	tr := tar.NewReader(gz)
	for {
		h, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return "", fmt.Errorf("%s not found at the top level of the archive", name)
		}
		if err != nil {
			return "", fmt.Errorf("reading tar: %w", err)
		}
		if h.Typeflag != tar.TypeReg || !topLevel(h.Name, name) {
			continue
		}
		return writeTemp(dir, tr)
	}
}
