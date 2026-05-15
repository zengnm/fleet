package fleetnode

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

const defaultApprovalAgent = "fleetn"

type approvalsFile struct {
	Agents map[string]approvalAgent `json:"agents"`
}

type approvalAgent struct {
	Allowlist []approvalEntry `json:"allowlist"`
}

type approvalEntry struct {
	Pattern string `json:"pattern"`
}

func execApprovalsGet(cfg Config) (map[string]any, error) {
	file, exists, raw, err := loadApprovalsFile(cfg.ApprovalsPath)
	if err != nil {
		return nil, err
	}
	hash := ""
	if exists {
		sum := sha256.Sum256(raw)
		hash = hex.EncodeToString(sum[:])
	}
	return map[string]any{
		"path":   cfg.ApprovalsPath,
		"exists": exists,
		"hash":   hash,
		"file":   approvalsFileToMap(file),
	}, nil
}

func execApprovalsAdd(cfg Config, patterns []string) (map[string]any, error) {
	file, _, _, err := loadApprovalsFile(cfg.ApprovalsPath)
	if err != nil {
		return nil, err
	}
	normalizeApprovalsFile(&file)
	agent := file.Agents[defaultApprovalAgent]
	entries := make([]approvalEntry, 0, len(agent.Allowlist)+len(patterns))
	seen := map[string]struct{}{}
	for _, entry := range agent.Allowlist {
		pattern := strings.TrimSpace(entry.Pattern)
		if pattern == "" {
			continue
		}
		if _, ok := seen[pattern]; ok {
			continue
		}
		seen[pattern] = struct{}{}
		entries = append(entries, approvalEntry{Pattern: pattern})
	}
	for _, pattern := range patterns {
		pattern = strings.TrimSpace(pattern)
		if pattern == "" {
			continue
		}
		if _, ok := seen[pattern]; ok {
			continue
		}
		seen[pattern] = struct{}{}
		entries = append(entries, approvalEntry{Pattern: pattern})
	}
	if len(entries) == 0 {
		return nil, errors.New("pattern is required")
	}
	agent.Allowlist = entries
	file.Agents[defaultApprovalAgent] = agent
	if err := writeApprovalsFile(cfg.ApprovalsPath, file); err != nil {
		return nil, err
	}
	return execApprovalsGet(cfg)
}

func execApprovalsClear(cfg Config) (map[string]any, error) {
	file, _, _, err := loadApprovalsFile(cfg.ApprovalsPath)
	if err != nil {
		return nil, err
	}
	normalizeApprovalsFile(&file)
	file.Agents[defaultApprovalAgent] = approvalAgent{Allowlist: []approvalEntry{}}
	if err := writeApprovalsFile(cfg.ApprovalsPath, file); err != nil {
		return nil, err
	}
	return execApprovalsGet(cfg)
}

func requireRunApproval(cfg Config, argv []string) error {
	if len(argv) == 0 {
		return errors.New("command is required")
	}
	file, _, _, err := loadApprovalsFile(cfg.ApprovalsPath)
	if err != nil {
		return err
	}
	resolved, err := resolveExecutable(argv[0])
	if err != nil {
		return err
	}
	candidates := []string{argv[0], resolved, filepath.Base(resolved)}
	for _, agent := range file.Agents {
		for _, entry := range agent.Allowlist {
			pattern := strings.TrimSpace(entry.Pattern)
			if pattern == "" {
				continue
			}
			for _, candidate := range candidates {
				if approvalPatternMatches(pattern, candidate) {
					return nil
				}
			}
		}
	}
	return fmt.Errorf("approval required for %s", resolved)
}

func loadApprovalsFile(path string) (approvalsFile, bool, []byte, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return emptyApprovalsFile(), false, nil, nil
		}
		return approvalsFile{}, false, nil, err
	}
	var file approvalsFile
	if err := json.Unmarshal(raw, &file); err != nil {
		return approvalsFile{}, true, raw, err
	}
	normalizeApprovalsFile(&file)
	return file, true, raw, nil
}

func writeApprovalsFile(path string, file approvalsFile) error {
	normalizeApprovalsFile(&file)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	payload, err := json.MarshalIndent(file, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(payload, '\n'), 0o600)
}

func emptyApprovalsFile() approvalsFile {
	return approvalsFile{Agents: map[string]approvalAgent{}}
}

func normalizeApprovalsFile(file *approvalsFile) {
	if file.Agents == nil {
		file.Agents = map[string]approvalAgent{}
	}
}

func approvalsFileToMap(file approvalsFile) map[string]any {
	raw, _ := json.Marshal(file)
	var out map[string]any
	_ = json.Unmarshal(raw, &out)
	if out == nil {
		return map[string]any{"agents": map[string]any{}}
	}
	return out
}

func resolveExecutable(name string) (string, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return "", errors.New("command is required")
	}
	if strings.ContainsRune(name, filepath.Separator) {
		abs, err := filepath.Abs(name)
		if err != nil {
			return "", err
		}
		return abs, nil
	}
	resolved, err := exec.LookPath(name)
	if err != nil {
		return "", err
	}
	return resolved, nil
}

func approvalPatternMatches(pattern, value string) bool {
	if pattern == value {
		return true
	}
	if ok, _ := filepath.Match(pattern, value); ok {
		return true
	}
	return false
}
