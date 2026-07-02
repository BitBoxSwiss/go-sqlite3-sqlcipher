//go:build ignore
// +build ignore

package main

import (
	"bufio"
	"bytes"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

const (
	buildConstraint = "sqlcipher && !libsqlcipher && !darwin"
	legacyBuildTag  = "sqlcipher,!libsqlcipher,!darwin"
)

var includeCFileRE = regexp.MustCompile(`^\s*#\s*include\s+"([^"]+\.c)"`)

// Keep this list to the features used by SQLCipher's LibTomCrypt provider:
// AES-CBC, SHA1/SHA256/SHA512, HMAC, PBKDF2, Fortuna, and descriptor registries.
var libtomcryptCFiles = []string{
	"ciphers/aes/aes.c",
	"hashes/sha1.c",
	"hashes/helper/hash_memory.c",
	"hashes/sha2/sha256.c",
	"hashes/sha2/sha512.c",
	"mac/hmac/hmac_done.c",
	"mac/hmac/hmac_init.c",
	"mac/hmac/hmac_memory.c",
	"mac/hmac/hmac_process.c",
	"misc/crypt/crypt.c",
	"misc/crypt/crypt_argchk.c",
	"misc/crypt/crypt_cipher_descriptor.c",
	"misc/crypt/crypt_cipher_is_valid.c",
	"misc/crypt/crypt_find_cipher.c",
	"misc/crypt/crypt_find_hash.c",
	"misc/crypt/crypt_hash_descriptor.c",
	"misc/crypt/crypt_hash_is_valid.c",
	"misc/crypt/crypt_prng_descriptor.c",
	"misc/crypt/crypt_prng_is_valid.c",
	"misc/crypt/crypt_register_cipher.c",
	"misc/crypt/crypt_register_hash.c",
	"misc/crypt/crypt_register_prng.c",
	"misc/pkcs5/pkcs_5_2.c",
	"misc/zeromem.c",
	"modes/cbc/cbc_decrypt.c",
	"modes/cbc/cbc_done.c",
	"modes/cbc/cbc_encrypt.c",
	"modes/cbc/cbc_start.c",
	"prngs/fortuna.c",
	"prngs/rng_get_bytes.c",
}

func main() {
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		fatal(err)
	}
	if wd, err := os.Getwd(); err == nil && filepath.Base(wd) != "sqlcipher" {
		root = "."
	}
	root, err = filepath.Abs(root)
	if err != nil {
		fatal(err)
	}

	outDir := filepath.Join(root, "internal", "libtomcrypt")
	submoduleDir := filepath.Join(outDir, "src")
	sourceDir := filepath.Join(submoduleDir, "src")
	headerDir := filepath.Join(sourceDir, "headers")

	if _, err := os.Stat(filepath.Join(submoduleDir, "LICENSE")); err != nil {
		fatal(fmt.Errorf("LibTomCrypt submodule is not initialized at %s: %w", submoduleDir, err))
	}

	if err := cleanGenerated(outDir); err != nil {
		fatal(err)
	}
	if err := copyHeaders(headerDir, outDir); err != nil {
		fatal(err)
	}
	if err := copyFile(filepath.Join(submoduleDir, "LICENSE"), filepath.Join(root, "LICENSE.LIBTOMCRYPT")); err != nil {
		fatal(err)
	}

	cFiles, err := listCFiles(sourceDir)
	if err != nil {
		fatal(err)
	}
	included, err := findIncludedCFiles(sourceDir, cFiles)
	if err != nil {
		fatal(err)
	}
	for _, rel := range cFiles {
		ext := ".c"
		if included[rel] {
			ext = ".inc"
		}
		if err := generateSource(sourceDir, outDir, rel, ext); err != nil {
			fatal(err)
		}
	}
}

func cleanGenerated(outDir string) error {
	entries, err := os.ReadDir(outDir)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() {
			continue
		}
		if strings.HasPrefix(name, "ltc_") ||
			(strings.HasPrefix(name, "tomcrypt") && strings.HasSuffix(name, ".h")) {
			if err := os.Remove(filepath.Join(outDir, name)); err != nil {
				return err
			}
		}
	}
	return nil
}

func copyHeaders(headerDir, outDir string) error {
	headers, err := filepath.Glob(filepath.Join(headerDir, "*.h"))
	if err != nil {
		return err
	}
	sort.Strings(headers)
	for _, src := range headers {
		if err := copyFile(src, filepath.Join(outDir, filepath.Base(src))); err != nil {
			return err
		}
	}
	return nil
}

func listCFiles(sourceDir string) ([]string, error) {
	files := map[string]bool{}
	var add func(rel string) error
	add = func(rel string) error {
		rel = filepath.ToSlash(rel)
		if files[rel] {
			return nil
		}
		if _, err := os.Stat(filepath.Join(sourceDir, filepath.FromSlash(rel))); err != nil {
			return err
		}
		files[rel] = true
		includes, err := cFileIncludes(sourceDir, rel, filepath.Join(sourceDir, filepath.FromSlash(rel)))
		if err != nil {
			return err
		}
		for _, include := range includes {
			if err := add(include); err != nil {
				return err
			}
		}
		return nil
	}
	for _, rel := range libtomcryptCFiles {
		if err := add(rel); err != nil {
			return nil, err
		}
	}
	result := make([]string, 0, len(files))
	for rel := range files {
		result = append(result, rel)
	}
	sort.Strings(result)
	return result, nil
}

func findIncludedCFiles(sourceDir string, cFiles []string) (map[string]bool, error) {
	included := make(map[string]bool)
	for _, rel := range cFiles {
		src := filepath.Join(sourceDir, filepath.FromSlash(rel))
		includes, err := cFileIncludes(sourceDir, rel, src)
		if err != nil {
			return nil, err
		}
		for _, include := range includes {
			included[include] = true
		}
	}
	return included, nil
}

func generateSource(sourceDir, outDir, rel, ext string) error {
	src := filepath.Join(sourceDir, filepath.FromSlash(rel))
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()

	outName := generatedName(rel, ext)
	out, err := os.Create(filepath.Join(outDir, outName))
	if err != nil {
		return err
	}
	defer out.Close()

	if ext == ".c" {
		fmt.Fprintf(out, "//go:build %s\n", buildConstraint)
		fmt.Fprintf(out, "// +build %s\n\n", legacyBuildTag)
	}
	fmt.Fprintln(out, "/* Code generated by upgrade/sqlcipher/generate_libtomcrypt.go; DO NOT EDIT. */")
	fmt.Fprintf(out, "/* Source: internal/libtomcrypt/src/src/%s */\n\n", rel)

	scanner := bufio.NewScanner(in)
	for scanner.Scan() {
		line := scanner.Text()
		if match := includeCFileRE.FindStringSubmatch(line); match != nil {
			includeRel, err := normalizeInclude(sourceDir, rel, match[1])
			if err != nil {
				return err
			}
			line = includeCFileRE.ReplaceAllString(line, `#include "`+generatedName(includeRel, ".inc")+`"`)
		}
		fmt.Fprintln(out, line)
	}
	return scanner.Err()
}

func cFileIncludes(sourceDir, rel, src string) ([]string, error) {
	file, err := os.Open(src)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	var includes []string
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := scanner.Text()
		match := includeCFileRE.FindStringSubmatch(line)
		if match == nil {
			continue
		}
		includeRel, err := normalizeInclude(sourceDir, rel, match[1])
		if err != nil {
			return nil, err
		}
		includes = append(includes, includeRel)
	}
	return includes, scanner.Err()
}

func normalizeInclude(sourceDir, rel, include string) (string, error) {
	dir := filepath.Dir(filepath.Join(sourceDir, filepath.FromSlash(rel)))
	path := filepath.Clean(filepath.Join(dir, filepath.FromSlash(include)))
	normalized, err := filepath.Rel(sourceDir, path)
	if err != nil {
		return "", err
	}
	return filepath.ToSlash(normalized), nil
}

func generatedName(rel, ext string) string {
	name := strings.TrimSuffix(filepath.ToSlash(rel), ".c")
	replacer := strings.NewReplacer("/", "_", "-", "_", ".", "_")
	return "ltc_" + replacer.Replace(name) + ext
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()

	var buf bytes.Buffer
	if _, err := io.Copy(&buf, in); err != nil {
		return err
	}
	return os.WriteFile(dst, buf.Bytes(), 0o644)
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, err)
	os.Exit(1)
}
