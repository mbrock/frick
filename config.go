package main

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

var defaultEnvFiles = []string{
	"~/.config/frick/.env",
	"~/frick/.env",
}

var defaultSecretDirs = []string{
	"~/.config/frick/secret",
	"~/frick/secret",
}

func loadDefaultEnv() (string, error) {
	if explicit := os.Getenv("FRICK_ENV_FILE"); explicit != "" {
		path, err := expandHome(explicit)
		if err != nil {
			return "", err
		}
		return path, loadEnvFile(path)
	}
	for _, candidate := range defaultEnvFiles {
		path, err := expandHome(candidate)
		if err != nil {
			return "", err
		}
		if _, err := os.Stat(path); err == nil {
			return path, loadEnvFile(path)
		} else if !os.IsNotExist(err) {
			return "", err
		}
	}
	return "", nil
}

func loadEnvFile(path string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	for lineNo := 1; scanner.Scan(); lineNo++ {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		line = strings.TrimPrefix(line, "export ")
		k, v, ok := strings.Cut(line, "=")
		if !ok {
			return fmt.Errorf("%s:%d: expected KEY=value", path, lineNo)
		}
		key := strings.TrimSpace(k)
		if key == "" {
			return fmt.Errorf("%s:%d: empty key", path, lineNo)
		}
		value, err := parseEnvValue(strings.TrimSpace(v))
		if err != nil {
			return fmt.Errorf("%s:%d: %w", path, lineNo, err)
		}
		if _, exists := os.LookupEnv(key); !exists {
			_ = os.Setenv(key, value)
		}
	}
	return scanner.Err()
}

func parseEnvValue(raw string) (string, error) {
	if raw == "" {
		return "", nil
	}
	if raw[0] == '\'' {
		if len(raw) < 2 || raw[len(raw)-1] != '\'' {
			return "", fmt.Errorf("unterminated single-quoted value")
		}
		return raw[1 : len(raw)-1], nil
	}
	if raw[0] == '"' {
		return strconv.Unquote(raw)
	}
	if i := strings.IndexByte(raw, '#'); i >= 0 {
		raw = strings.TrimSpace(raw[:i])
	}
	return raw, nil
}

func normalizeCredentialEnv() error {
	keyFile := os.Getenv("FRICK_KEY_FILE")
	if keyFile == "" {
		return nil
	}
	expanded, err := expandHome(keyFile)
	if err != nil {
		return err
	}
	if fileExists(expanded) {
		return os.Setenv("FRICK_KEY_FILE", expanded)
	}

	base := filepath.Base(expanded)
	if base == "." || base == string(filepath.Separator) {
		return nil
	}
	for _, dir := range defaultSecretDirs {
		expandedDir, err := expandHome(dir)
		if err != nil {
			return err
		}
		candidate := filepath.Join(expandedDir, base)
		if fileExists(candidate) {
			return os.Setenv("FRICK_KEY_FILE", candidate)
		}
	}
	return nil
}

func expandHome(path string) (string, error) {
	if path == "~" || strings.HasPrefix(path, "~/") {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		if path == "~" {
			return home, nil
		}
		return filepath.Join(home, path[2:]), nil
	}
	return path, nil
}

func fileExists(path string) bool {
	st, err := os.Stat(path)
	return err == nil && !st.IsDir()
}
